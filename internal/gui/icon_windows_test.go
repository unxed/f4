//go:build windows

package gui

import (
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestIconSizesForDPI(t *testing.T) {
	tests := []struct {
		dpi         uint32
		wantSmall   int
		wantTaskbar int
	}{
		{dpi: 96, wantSmall: 16, wantTaskbar: 24},
		{dpi: 168, wantSmall: 28, wantTaskbar: 42},
		{dpi: 192, wantSmall: 32, wantTaskbar: 48},
	}

	for _, tt := range tests {
		small, taskbar := iconSizesForDPI(tt.dpi)
		if small != tt.wantSmall || taskbar != tt.wantTaskbar {
			t.Fatalf("iconSizesForDPI(%d) = (%d, %d), want (%d, %d)",
				tt.dpi, small, taskbar, tt.wantSmall, tt.wantTaskbar)
		}
	}
}

func TestWindowsThemeFromRegistry(t *testing.T) {
	if got := windowsThemeFromRegistry(0); got != windowsThemeDark {
		t.Fatalf("AppsUseLightTheme=0 maps to %v, want dark", got)
	}
	if got := windowsThemeFromRegistry(1); got != windowsThemeLight {
		t.Fatalf("AppsUseLightTheme=1 maps to %v, want light", got)
	}
}

func TestRestoreWindowIconsPutsBackWhatWasThere(t *testing.T) {
	// A window belongs to the thread that made it, and a message sent to it
	// from another one waits for that thread to pump. Keep everything here on
	// one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, err := iconUser32.NewProc("CreateWindowExW").Call(
		0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Skipf("cannot create a test window: %v", err)
	}
	defer iconUser32.NewProc("DestroyWindow").Call(hwnd)

	const (
		wmGetIcon      = 0x007F
		idiApplication = 32512
		idiWarning     = 32515
		idiInformation = 32516
	)
	loadIcon := iconUser32.NewProc("LoadIconW")
	own, _, _ := loadIcon.Call(0, idiApplication)
	other, _, _ := loadIcon.Call(0, idiInformation)
	ours, _, _ := loadIcon.Call(0, idiWarning)
	if own == 0 || other == 0 || ours == 0 {
		t.Skip("system icons did not load")
	}

	// The window starts with icons of its own, ours replace them, and what
	// WM_SETICON reported as replaced is what goes back.
	procIconSendMessageW.Call(hwnd, wmSetIcon, iconSmall, own)
	procIconSendMessageW.Call(hwnd, wmSetIcon, iconBig, other)
	var prev previousIcons
	prev.small, _, _ = procIconSendMessageW.Call(hwnd, wmSetIcon, iconSmall, ours)
	prev.big, _, _ = procIconSendMessageW.Call(hwnd, wmSetIcon, iconBig, ours)
	if prev.small != own || prev.big != other {
		t.Fatalf("WM_SETICON reported (%#x, %#x) as replaced, want (%#x, %#x)", prev.small, prev.big, own, other)
	}

	restoreWindowIcons(hwnd, prev)

	if got, _, _ := procIconSendMessageW.Call(hwnd, wmGetIcon, iconSmall, 0); got != own {
		t.Fatalf("small icon after restore = %#x, want %#x", got, own)
	}
	if got, _, _ := procIconSendMessageW.Call(hwnd, wmGetIcon, iconBig, 0); got != other {
		t.Fatalf("big icon after restore = %#x, want %#x", got, other)
	}
}
