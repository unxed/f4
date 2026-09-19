//go:build windows

package terminal

import (
	"testing"
	"unsafe"
)

// The API rejects a CONSOLE_SCREEN_BUFFER_INFOEX whose layout is off, and
// silently -- keepConsoleColorTable swallows every failure -- so pin it to the
// 96 bytes and the colour table offset winconp.h declares.
func TestConsoleBufferInfoExLayout(t *testing.T) {
	var info consoleBufferInfoEx
	if got := unsafe.Sizeof(info); got != 96 {
		t.Fatalf("sizeof(consoleBufferInfoEx) = %d, want 96", got)
	}
	if got := unsafe.Offsetof(info.ColorTable); got != 32 {
		t.Fatalf("offsetof(ColorTable) = %d, want 32", got)
	}
	if got := unsafe.Offsetof(info.Window); got != 14 {
		t.Fatalf("offsetof(Window) = %d, want 14", got)
	}
}

func TestKeepConsoleColorTableIsSafeAnywhere(t *testing.T) {
	// Under `go test` there is usually no classic console window, or none this
	// process may touch; either way the pair must be callable and quiet.
	restore := keepConsoleColorTable()
	if restore == nil {
		t.Fatal("keepConsoleColorTable returned a nil restore function")
	}
	restore()
	restore()
}
