//go:build darwin

package terminal

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/unxed/vtui"
	"golang.org/x/sys/unix"
)

// PTY handles pseudo-terminal allocation and process execution.
type PTY struct {
	Master    *os.File
	Slave     *os.File
	Cmd       *exec.Cmd
	closed    bool
	closeOnce sync.Once
	shellPgrp int
}

func NewPTY() (*PTY, error) {
	// O_CLOEXEC matters as much as O_NOCTTY here. Go sets close-on-exec on
	// every descriptor it opens itself, but a raw unix.Open wrapped in
	// os.NewFile keeps whatever flags the fd was created with, and forkExec
	// closes nothing on its own. Without the flag the shell inherits a copy
	// of its own master, so the master never drops to zero references when
	// f4 dies: the kernel sends no SIGHUP, the shell survives as an orphan
	// holding its /dev/ttys node, and enough restarts exhaust ptmx_max until
	// allocation fails with ENXIO.
	masterFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	// Unlike every other platform here (see pty_unix.go / pty_linux.go), the
	// master fd is deliberately kept blocking, and unwrapped in an os.File,
	// through the whole grantpt/unlockpt/get-slave-name/open-slave handshake
	// below. Darwin's /dev/ptmx driver is not safe to hand to the Go runtime
	// poller mid-handshake: registering the fd with kqueue (which is what
	// os.NewFile does the moment the fd is non-blocking) before TIOCPTYUNLK
	// has run races the driver's own bookkeeping for the pair and can corrupt
	// it, later surfacing as ENOTTY/EBADF out of an otherwise ordinary read
	// or ioctl -- see golang/go#22099 and creack/pty#52 / creack/pty#53.
	// Neither Apple's own libc (grantpt.c/pty.c in apple-oss-distributions/
	// Libc) nor creack/pty ever makes the master non-blocking at this stage;
	// this file used to, because the code was copied from the Linux branch,
	// where it is required. The race is intermittent -- recent macOS runners
	// under CI here don't seem to catch it -- but it doesn't need to occur
	// on every host to require avoiding it.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), unix.TIOCPTYGRANT, 0); e != 0 {
		unix.Close(masterFd)
		return nil, e
	}

	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), unix.TIOCPTYUNLK, 0); e != 0 {
		unix.Close(masterFd)
		return nil, e
	}

	ptyName := make([]byte, 128)
	// #nosec G103 -- ioctl writes at most the 128-byte ptyName buffer during this synchronous syscall.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&ptyName[0]))); e != 0 {
		unix.Close(masterFd)
		return nil, e
	}

	nameLen := bytes.IndexByte(ptyName, 0)
	if nameLen == -1 {
		nameLen = len(ptyName)
	}
	slaveName := string(ptyName[:nameLen])

	// The slave is marked close-on-exec too. Run() hands it to the child as
	// stdin, stdout and stderr, and the dup2 that installs it on 0, 1 and 2
	// clears the flag on those copies, so the child still gets its terminal
	// -- what the flag drops is only the surplus inherited descriptor.
	slaveFd, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Close(masterFd)
		return nil, err
	}

	// Only now, with the pty pair fully established (grantpt/unlockpt done,
	// slave opened), is it safe to switch the master to non-blocking mode
	// and hand it to os.NewFile -- same reasoning as pty_unix.go: Go only
	// registers a descriptor with the runtime poller when it is already
	// non-blocking at NewFile time, and the read loop in frame.go relies on
	// PTY.Close() closing this os.File to unblock a pending Master.Read().
	if err := unix.SetNonblock(masterFd, true); err != nil {
		unix.Close(masterFd)
		unix.Close(slaveFd)
		return nil, err
	}
	master := os.NewFile(uintptr(masterFd), "/dev/ptmx")
	slave := os.NewFile(uintptr(slaveFd), slaveName)

	p := &PTY{
		Master: master,
		Slave:  slave,
	}
	registerPTYOpened()
	return p, nil
}

func (p *PTY) Write(b []byte) (int, error) {
	return p.Master.Write(b)
}

func (p *PTY) Read(b []byte) (int, error) {
	return p.Master.Read(b)
}

func (p *PTY) Close() error {
	var err error
	p.closeOnce.Do(func() {
		vtui.DebugLog("PTY: Closing PTY and killing child process group")
		if p.Cmd != nil && p.Cmd.Process != nil {
			_ = syscall.Kill(-p.Cmd.Process.Pid, syscall.SIGKILL)
			p.Cmd.Process.Kill()
		}
		if p.Master != nil {
			err = p.Master.Close()
		}
		if p.Slave != nil {
			p.Slave.Close()
		}
		p.closed = true
		registerPTYClosed()
	})
	return err
}

func (p *PTY) Wait() error {
	return p.Cmd.Wait()
}

func (p *PTY) Run(name string, args ...string) error {
	args = historyQuietArgs(name, args)
	p.Cmd = exec.Command(name, args...)
	p.Cmd.Stdin = p.Slave
	p.Cmd.Stdout = p.Slave
	p.Cmd.Stderr = p.Slave
	p.Cmd.Env = historyQuietEnv(name, args, TerminalChildEnv())
	p.Cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	// Set initial size
	p.SetSize(80, 24)

	err := p.Cmd.Start()
	if err == nil {
		p.shellPgrp, _ = syscall.Getpgid(p.Cmd.Process.Pid)
	}
	return err
}

func (p *PTY) IsBusy() bool {
	if p.Master == nil {
		return false
	}
	var pgrp int32
	// #nosec G103 -- ioctl writes one int32 into this live stack variable during the synchronous syscall.
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, p.Master.Fd(), unix.TIOCGPGRP, uintptr(unsafe.Pointer(&pgrp)))
	if err != 0 {
		return false
	}
	return int(pgrp) != p.shellPgrp
}

func (p *PTY) SetSize(cols, rows int) {
	p.SetSizePixels(cols, rows, 0, 0)
}

// SetSizePixels also reports the size of the window in pixels, which is how
// a program in the terminal learns the shape of a character cell.
func (p *PTY) SetSizePixels(cols, rows, xpixel, ypixel int) {
	size := struct {
		Row, Col, Xpixel, Ypixel uint16
	}{
		Row: ptyPixels(rows), Col: ptyPixels(cols), Xpixel: ptyPixels(xpixel), Ypixel: ptyPixels(ypixel),
	}
	// #nosec G103 -- ioctl reads this fixed-size winsize-compatible stack struct only during the synchronous syscall.
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, p.Master.Fd(), unix.TIOCSWINSZ, uintptr(unsafe.Pointer(&size)))
}

func GetSystemShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "/bin/sh"
	}
	base := filepath.Base(shell)
	if base == "fish" || base == "csh" || base == "tcsh" {
		return "bash"
	}
	return shell
}
