//go:build windows

package winex11drag

import (
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

// Install wraps the GUI backend after it has registered itself. Under Wine,
// native X11 drops use XDND; the original backend remains the fallback for
// gestures that cannot open an X11 session.
func Install() {
	if !hostmode.Posix() {
		return
	}
	inner := vtui.CurrentDragBackend()
	if inner == nil {
		return
	}
	vtui.SetDragBackend(NewBackend(inner, systemPointer{}))
}
