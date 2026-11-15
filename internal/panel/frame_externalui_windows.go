//go:build windows

package panel

import "golang.org/x/sys/windows"

var shellExecuteFile = windows.ShellExecute

func runAssociatedFileOS(path, dir string) error {
	verb := windows.StringToUTF16Ptr("open")
	file := windows.StringToUTF16Ptr(path)
	var workingDir *uint16
	if dir != "" {
		workingDir = windows.StringToUTF16Ptr(dir)
	}
	return shellExecuteFile(0, verb, file, nil, workingDir, windows.SW_SHOWNORMAL)
}
