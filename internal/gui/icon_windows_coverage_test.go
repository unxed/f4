//go:build windows

package gui

import (
	"os"
	"testing"
	"time"
)

func TestIconScaleIconSize(t *testing.T) {
	for _, tc := range []struct {
		base int
		dpi  uint32
		want int
	}{
		{base: 16, dpi: 0, want: 0},
		{base: 16, dpi: 96, want: 16},
		{base: 16, dpi: 120, want: 20},
		{base: 16, dpi: 144, want: 24},
		{base: 16, dpi: 168, want: 28},
		{base: 16, dpi: 192, want: 32},
		{base: 24, dpi: 120, want: 30},
	} {
		if got := scaleIconSize(tc.base, tc.dpi); got != tc.want {
			t.Errorf("scaleIconSize(%d, %d) = %d, want %d", tc.base, tc.dpi, got, tc.want)
		}
	}
}

func TestIconSizesForDPIExtended(t *testing.T) {
	for _, tc := range []struct {
		dpi       uint32
		wantSmall int
		wantBig   int
	}{
		{dpi: 0, wantSmall: 16, wantBig: 24},
		{dpi: 96, wantSmall: 16, wantBig: 24},
		{dpi: 144, wantSmall: 24, wantBig: 36},
		{dpi: 192, wantSmall: 32, wantBig: 48},
	} {
		small, big := iconSizesForDPI(tc.dpi)
		if small != tc.wantSmall || big != tc.wantBig {
			t.Errorf("iconSizesForDPI(%d) = (%d, %d), want (%d, %d)", tc.dpi, small, big, tc.wantSmall, tc.wantBig)
		}
	}
}

func TestWindowsThemeFromRegistryExtended(t *testing.T) {
	for _, tc := range []struct {
		value uint64
		want  windowsTheme
	}{
		{value: 0, want: windowsThemeDark},
		{value: 1, want: windowsThemeLight},
		{value: 2, want: windowsThemeLight},
	} {
		if got := windowsThemeFromRegistry(tc.value); got != tc.want {
			t.Errorf("windowsThemeFromRegistry(%d) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestIconInvalidWindowQueries(t *testing.T) {
	if got := windowClassName(0); got != "" {
		t.Fatalf("windowClassName(0) = %q, want empty", got)
	}
	if isWindow(0) {
		t.Fatal("isWindow(0) reported a window")
	}
}

func TestIconWindowDPIFallback(t *testing.T) {
	if got := windowDPI(0); got != defaultDPI {
		t.Fatalf("windowDPI(0) = %d, want default %d", got, defaultDPI)
	}
}

func TestIconThemeInvalidWindow(t *testing.T) {
	if applyWindowTheme(0, windowsThemeLight) {
		t.Fatal("applyWindowTheme accepted a null window")
	}
	if applyWindowTheme(0, windowsThemeDark) {
		t.Fatal("applyWindowTheme accepted a null window in dark mode")
	}
}

func TestIconLoadingInvalidWindow(t *testing.T) {
	if applyWindowIcons(0, 96) {
		t.Fatal("applyWindowIcons accepted an invalid window")
	}
	if icon := loadIconResource(0); icon != 0 {
		t.Fatalf("loadIconResource(0) = %#x, want zero", icon)
	}
}

func TestIconFindGogpuWindowWithoutGogpu(t *testing.T) {
	if got := findGogpuWindow(uint32(os.Getpid())); got != 0 {
		t.Fatalf("findGogpuWindow(current process) = %#x, want no GoGPU window", got)
	}
}

func TestIconAppearanceManagerStops(t *testing.T) {
	called := make(chan struct{}, 1)
	stop := startWindowsWindowAppearanceManager(func() uintptr {
		select {
		case called <- struct{}{}:
		default:
		}
		return 0
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("appearance manager did not probe its window finder")
	}
	stop()
	stop()
}

func TestIconConsoleAppearanceManagerStops(t *testing.T) {
	stop := StartWindowsConsoleWindowAppearanceManager()
	stop()
	stop()
}
