//go:build freebsd || dragonfly

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

type PTY struct {
	Master    *os.File
	Slave     *os.File
	Cmd       *exec.Cmd
	closed    bool
	closeOnce sync.Once
	shellPgrp int
}

// ptyStep names the step of PTY allocation that failed and records its
// numeric errno. Reporting only the final error leaves a trace log saying
// that a PTY could not be allocated without saying which call refused it,
// which is not enough to tell a missing ioctl apart from an exhausted or
// unreachable device. Errno numbers differ per platform, so the number is
// logged alongside the name.
func ptyStep(step string, err error) error {
	if err == nil {
		return nil
	}
	if errno, ok := err.(syscall.Errno); ok {
		vtui.DebugLog("PTY: step=%s failed: errno=%d (%v)", step, int(errno), errno)
		return fmt.Errorf("%s: errno=%d: %w", step, int(errno), err)
	}
	vtui.DebugLog("PTY: step=%s failed: %v", step, err)
	return fmt.Errorf("%s: %w", step, err)
}

func NewPTY() (*PTY, error) {
	// O_CLOEXEC matters as much as O_NOCTTY here. Go sets close-on-exec on
	// every descriptor it opens itself, but a raw unix.Open wrapped in
	// os.NewFile keeps whatever flags the fd was created with, and forkExec
	// closes nothing on its own. Without the flag the shell inherits a copy
	// of its own master, so the master never drops to zero references when
	// f4 dies: the kernel sends no SIGHUP, the shell survives as an orphan
	// holding its pts node, and enough restarts exhaust the Pty limit until
	// allocation fails.
	masterFd, err := openPTYMaster()
	if err != nil {
		return nil, ptyStep("open PTY master", err)
	}
	// Put the master fd in non-blocking mode before wrapping it in
	// os.NewFile; see pty_unix.go for why Close() cannot otherwise
	// interrupt a Master.Read() blocked in another goroutine.
	if err := unix.SetNonblock(masterFd, true); err != nil {
		unix.Close(masterFd)
		return nil, ptyStep("set non-blocking", err)
	}
	master := os.NewFile(uintptr(masterFd), "/dev/ptmx")

	// Naming the slave is the one step that differs between the BSDs here,
	// so each of them supplies its own ptySlaveName. Neither needs grantpt
	// or unlockpt: in FreeBSD's libc both are strong references to
	// __isptmaster and do nothing beyond validating the descriptor.
	slaveName, err := ptySlaveName(masterFd)
	if err != nil {
		master.Close()
		return nil, ptyStep("ptySlaveName", err)
	}

	// The slave is marked close-on-exec too. Run() hands it to the child as
	// stdin, stdout and stderr, and the dup2 that installs it on 0, 1 and 2
	// clears the flag on those copies, so the child still gets its terminal
	// -- what the flag drops is only the surplus inherited descriptor.
	slaveFd, err := unix.Open(slaveName, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, ptyStep("open "+slaveName, err)
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
		Row: uint16(rows), Col: uint16(cols), Xpixel: ptyPixels(xpixel), Ypixel: ptyPixels(ypixel),
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
