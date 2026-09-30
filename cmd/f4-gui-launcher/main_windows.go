//go:build windows

// Command f4-gui-launcher is f4-gui.exe in the Windows packages (f4#1567).
//
// f4.exe is a console program; started from Explorer it would open a console
// window. Until now the package therefore held a second copy of the whole
// binary linked as a GUI program (-H windowsgui), which f4 recognises by the
// "gui" in its name. This launcher, linked the same way, is what f4-gui.exe is
// now: it starts the f4.exe next to it with --gui, without a console, passes
// on its own arguments, and exits at once. The package holds one f4, and
// f4-gui.exe weighs about a megabyte instead of tens.
//
// One thing changes: a per-binary settings file called f4-gui.exe.ini is no
// longer read, because the process that runs is f4.exe; f4.ini, which every
// binary in the directory shares, is unaffected.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	// detachedProcess is DETACHED_PROCESS: the child gets no console at all,
	// which is what a program linked with -H windowsgui has.
	detachedProcess = 0x00000008
	// quietEnv silences the error box, for tests and unattended runs.
	quietEnv = "F4_LAUNCHER_QUIET"
)

func main() {
	os.Exit(run(os.Args[1:], os.Executable, startDetached, showError))
}

// run starts f4.exe from the directory of the launcher and returns the exit
// code of the launcher itself.
func run(args []string, executable func() (string, error), start func(*exec.Cmd) error, fail func(string)) int {
	self, err := executable()
	if err != nil {
		fail("Cannot tell where f4-gui.exe is: " + err.Error())
		return 2
	}
	target := filepath.Join(filepath.Dir(self), "f4.exe")
	if st, err := os.Stat(target); err != nil || st.IsDir() {
		fail("f4.exe was not found next to " + filepath.Base(self) + ".")
		return 2
	}
	cmd := exec.Command(target, append([]string{"--gui"}, args...)...) // #nosec G204 -- f4.exe next to the launcher, the user's own arguments
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	if err := start(cmd); err != nil {
		fail("Cannot start f4.exe: " + err.Error())
		return 1
	}
	return 0
}

// startDetached starts the process and lets go of it.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// showError tells the user what went wrong: there is no console to print to.
func showError(text string) {
	if os.Getenv(quietEnv) != "" {
		return
	}
	msg, err1 := windows.UTF16PtrFromString(text)
	title, err2 := windows.UTF16PtrFromString("f4")
	if err1 != nil || err2 != nil {
		return
	}
	_, _ = windows.MessageBox(0, msg, title, windows.MB_OK|windows.MB_ICONERROR)
}
