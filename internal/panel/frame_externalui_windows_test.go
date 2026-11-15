//go:build windows

package panel

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestRunAssociatedFileOSPassesUnicodePathDirectlyToShellExecute(t *testing.T) {
	old := shellExecuteFile
	t.Cleanup(func() { shellExecuteFile = old })

	const path = `C:\Images\photo #100% — 夏.jpg`
	const dir = `C:\Images`
	called := false
	shellExecuteFile = func(_ windows.Handle, verb, file, args, workingDir *uint16, showCmd int32) error {
		called = true
		if got := windows.UTF16PtrToString(verb); got != "open" {
			t.Errorf("verb = %q, want open", got)
		}
		if got := windows.UTF16PtrToString(file); got != path {
			t.Errorf("file = %q, want original path %q", got, path)
		}
		if args != nil {
			t.Errorf("args = %v, want nil", args)
		}
		if got := windows.UTF16PtrToString(workingDir); got != dir {
			t.Errorf("workingDir = %q, want %q", got, dir)
		}
		if showCmd != windows.SW_SHOWNORMAL {
			t.Errorf("showCmd = %d, want SW_SHOWNORMAL", showCmd)
		}
		return nil
	}

	if err := runAssociatedFileOS(path, dir); err != nil {
		t.Fatalf("runAssociatedFileOS: %v", err)
	}
	if !called {
		t.Fatal("ShellExecuteW was not called")
	}
}
