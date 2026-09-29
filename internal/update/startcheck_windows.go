package update

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the console build started from the GUI
// one gets a console of its own, and without this it flashes on screen.
const createNoWindow = 0x08000000

func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
