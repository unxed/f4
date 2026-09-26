//go:build lite

package gui

import "fmt"

// RunGui is the lite build's stand-in for the real one (run_unix.go,
// run_windows.go): it never calls vtui.RunInGUIWindow, which is what pulls in
// vtui's per-backend renderers (x11/win32/ebiten/gogpu/wayland). Because
// nothing in a lite build's call graph reaches that entry point any more,
// the linker drops those renderers from the binary entirely — this is the
// single point of truth for "no graphical backends at all" in the lite
// build; console/terminal rendering (vtui's ANSI/ttyx path) is untouched and
// keeps working exactly as before.
func RunGui(backend string, setupUI func()) error {
	return fmt.Errorf("GUI backend %q is not available in a lite build; f4-lite is console/terminal only", backend)
}
