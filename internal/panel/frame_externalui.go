package panel

import (
	"os/exec"
	"runtime"
)

// externalUICommandRunner is the process boundary for commands that hand
// control to the desktop environment. Keeping this separate from ordinary
// command execution lets the test binary disable native UI globally.
type externalUICommandRunner func(command string, args []string, dir string) error

var DefaultExternalUICommandRunner externalUICommandRunner = runExternalUICommandOS

func runExternalUICommandOS(command string, args []string, dir string) error {
	// On Windows, opening an associated file through rundll32's
	// FileProtocolHandler treats a filesystem path as a URL. Characters with URL semantics can then be decoded or truncated.
	// Use ShellExecuteW with the original path instead.
	if command == "rundll32" && len(args) == 2 && args[0] == "url.dll,FileProtocolHandler" {
		return runAssociatedFileOS(args[1], dir)
	}
	cmd := exec.Command(command, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Run()
}

func (pf *PanelsFrame) RunExternalUICommand(command string, args []string, dir string) error {
	runner := DefaultExternalUICommandRunner
	if pf != nil && pf.ExternalUIRunner != nil {
		runner = pf.ExternalUIRunner
	}
	if runner == nil {
		return nil
	}
	return runner(command, append([]string(nil), args...), dir)
}

func SystemFileManagerCommand(path string, isDir bool) (string, []string, bool) {
	switch runtime.GOOS {
	case "linux":
		return "xdg-open", []string{path}, true
	case "windows":
		if isDir {
			return "explorer.exe", []string{path}, true
		}
		return "explorer.exe", []string{"/select,", path}, true
	case "darwin":
		return "open", []string{path}, true
	default:
		return "", nil, false
	}
}

func AssociatedFileCommand(path string) (string, []string, bool) {
	switch runtime.GOOS {
	case "linux":
		return "xdg-open", []string{path}, true
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", path}, true
	case "darwin":
		return "open", []string{path}, true
	default:
		return "", nil, false
	}
}
