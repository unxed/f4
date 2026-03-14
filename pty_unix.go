package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// PTY handles pseudo-terminal allocation and process execution.
type PTY struct {
	Master *os.File
	Slave  *os.File
	Cmd    *exec.Cmd
}

func NewPTY() (*PTY, error) {
	masterFd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}

	master := os.NewFile(uintptr(masterFd), "/dev/ptmx")

	var res uintptr
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&res))); err != 0 {
		return nil, err
	}

	var unlock int
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFd), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != 0 {
		return nil, err
	}

	slaveName := fmt.Sprintf("/dev/pts/%d", res)
	slaveFd, err := syscall.Open(slaveName, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}

	slave := os.NewFile(uintptr(slaveFd), slaveName)

	return &PTY{
		Master: master,
		Slave:  slave,
	}, nil
}

func (p *PTY) Run(name string, args ...string) error {
	p.Cmd = exec.Command(name, args...)
	p.Cmd.Stdin = p.Slave
	p.Cmd.Stdout = p.Slave
	p.Cmd.Stderr = p.Slave
	p.Cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	// Set initial size
	p.SetSize(80, 24)

	return p.Cmd.Start()
}

func (p *PTY) SetSize(cols, rows int) {
	size := struct {
		Row, Col, Xpixel, Ypixel uint16
	}{
		Row: uint16(rows),
		Col: uint16(cols),
		Xpixel: 0,
		Ypixel: 0,
	}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, p.Master.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&size)))
}

func GetSystemShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "/bin/sh"
	}
	return shell
}
