//go:build linux

package terminal

import (
	"fmt"
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
	// holding its /dev/pts node, and enough restarts exhaust the Pty limit
	// until allocation fails.
	masterFd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	// Put the master fd in non-blocking mode *before* wrapping it in
	// os.NewFile. Go's os.File only registers a descriptor with the
	// runtime poller when it is already non-blocking; if it isn't,
	// Master.Read() falls back to a raw blocking syscall.Read that
	// Close() from another goroutine cannot interrupt. Without this, a
	// terminal reader goroutine left blocked in Read() when its
	// PanelsFrame gets torn down leaks forever -- both the goroutine and
	// the underlying Pty slot -- which is exactly what eventually
	// exhausts the system's Pty limit across a long `go test ./...` run.
	if err := unix.SetNonblock(masterFd, true); err != nil {
		unix.Close(masterFd)
		return nil, err
	}

	master := os.NewFile(uintptr(masterFd), "/dev/ptmx")

	var res uintptr
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), unix.TIOCGPTN, uintptr(unsafe.Pointer(&res))); err != 0 {
		master.Close()
		return nil, err
	}

	// TIOCSPTLCK is used on Linux to unlock the slave Pty.
	// FreeBSD doesn't need/have it for /dev/ptmx.
	var unlock int
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))

	slaveName := fmt.Sprintf("/dev/pts/%d", res)
	// The slave is marked close-on-exec too. Run() hands it to the child as
	// stdin, stdout and stderr, and the dup2 that installs it on 0, 1 and 2
	// clears the flag on those copies, so the child still gets its terminal
	// -- what the flag drops is only the surplus inherited descriptor.
	slaveFd, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		// Every other failure above closes the master before giving up; this
		// path used to return without doing so, leaking the master fd.
		master.Close()
		return nil, err
	}

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
			// Kill the whole process group because we used Setsid
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
		Row:    uint16(rows),
		Col:    uint16(cols),
		Xpixel: ptyPixels(xpixel),
		Ypixel: ptyPixels(ypixel),
	}
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
