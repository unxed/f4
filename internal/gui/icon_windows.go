//go:build windows

package gui

import (
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	gogpuWindowClass = "GoGPUWindow"
	appIconResource  = 1

	imageIcon    = 1
	lrShared     = 0x00008000
	wmSetIcon    = 0x0080
	iconSmall    = 0
	iconBig      = 1
	defaultDPI   = 96
	taskbarAt96  = 24
	titlebarAt96 = 16

	dwmwaUseImmersiveDarkMode = 20
	themeRegistryPath         = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`
	themeRegistryValue        = "AppsUseLightTheme"
	themePollInterval         = 5 * time.Second
)

var (
	iconUser32                    = windows.NewLazySystemDLL("user32.dll")
	iconKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	iconDWMAPI                    = windows.NewLazySystemDLL("dwmapi.dll")
	procIconEnumWindows           = iconUser32.NewProc("EnumWindows")
	procIconGetWindowThreadPID    = iconUser32.NewProc("GetWindowThreadProcessId")
	procIconGetClassNameW         = iconUser32.NewProc("GetClassNameW")
	procIconIsWindow              = iconUser32.NewProc("IsWindow")
	procIconGetDPIForWindow       = iconUser32.NewProc("GetDpiForWindow")
	procIconLoadImageW            = iconUser32.NewProc("LoadImageW")
	procIconSendMessageW          = iconUser32.NewProc("SendMessageW")
	procIconGetModuleHandleW      = iconKernel32.NewProc("GetModuleHandleW")
	procIconGetConsoleWindow      = iconKernel32.NewProc("GetConsoleWindow")
	procDwmSetWindowAttribute     = iconDWMAPI.NewProc("DwmSetWindowAttribute")
	findGogpuWindowCallbackHandle = syscall.NewCallback(findGogpuWindowCallback)
)

type windowsTheme uint8

const (
	windowsThemeUnknown windowsTheme = iota
	windowsThemeLight
	windowsThemeDark
)

type windowSearch struct {
	pid  uint32
	hwnd uintptr
}

// startWindowsWindowIconManager fills two gaps in gogpu's Windows backend: it
// assigns the embedded icon to the HWND and opts the native title bar into the
// current Windows light/dark app theme. Polling lets both settings follow DPI
// and theme changes without replacing gogpu's window procedure.
func startWindowsWindowIconManager() func() {
	pid := uint32(os.Getpid())
	return startWindowsWindowAppearanceManager(func() uintptr {
		return findGogpuWindow(pid)
	}, false)
}

// StartWindowsConsoleWindowAppearanceManager does the same for the console
// window f4 runs in. That window outlives f4 and belongs to the shell, so the
// returned stop function also puts back the icons it found there (#1196).
func StartWindowsConsoleWindowAppearanceManager() func() {
	return startWindowsWindowAppearanceManager(func() uintptr {
		hwnd, _, _ := procIconGetConsoleWindow.Call()
		return hwnd
	}, true)
}

// iconRestoreTimeout bounds how long stopping the manager waits for the icons
// to go back. The console window belongs to another process; if that process
// is hung a stale icon is a lesser evil than an exit that never finishes.
const iconRestoreTimeout = time.Second

// startWindowsWindowAppearanceManager returns a stop function that ends the
// manager. With restoreOnStop it also waits, for up to iconRestoreTimeout, for
// the window's original icons to be put back.
func startWindowsWindowAppearanceManager(findWindow func() uintptr, restoreOnStop bool) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once

	go func() {
		defer close(done)
		manageWindowsWindowAppearance(stop, findWindow, restoreOnStop)
	}()

	return func() {
		once.Do(func() { close(stop) })
		if !restoreOnStop {
			return
		}
		select {
		case <-done:
		case <-time.After(iconRestoreTimeout):
		}
	}
}

func manageWindowsWindowAppearance(stop <-chan struct{}, findWindow func() uintptr, restoreOnStop bool) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var hwnd uintptr
	var appliedDPI uint32
	var appliedTheme windowsTheme
	var nextThemeCheck time.Time
	// The icons the window had before ours went on, taken from the first
	// successful WM_SETICON only: later ones (a DPI change) would report our
	// own icons as the originals.
	var original previousIcons
	var haveOriginal bool

	if restoreOnStop {
		defer func() {
			if hwnd != 0 && haveOriginal && isWindow(hwnd) {
				restoreWindowIcons(hwnd, original)
			}
		}()
	}

	for {
		if hwnd == 0 || !isWindow(hwnd) {
			hwnd = findWindow()
			appliedDPI = 0
			appliedTheme = windowsThemeUnknown
			nextThemeCheck = time.Time{}
			haveOriginal = false
			ticker.Reset(100 * time.Millisecond)
		}
		if hwnd != 0 {
			dpi := windowDPI(hwnd)
			if dpi != appliedDPI {
				if prev, ok := applyWindowIcons(hwnd, dpi); ok {
					appliedDPI = dpi
					if !haveOriginal {
						original, haveOriginal = prev, true
					}
				}
			}

			now := time.Now()
			if !now.Before(nextThemeCheck) {
				theme := currentWindowsAppTheme()
				if theme != windowsThemeUnknown && theme != appliedTheme && applyWindowTheme(hwnd, theme) {
					appliedTheme = theme
				}
				nextThemeCheck = now.Add(themePollInterval)
			}
			if appliedDPI != 0 && appliedTheme != windowsThemeUnknown {
				ticker.Reset(themePollInterval)
			}
		}

		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func currentWindowsAppTheme() windowsTheme {
	key, err := registry.OpenKey(registry.CURRENT_USER, themeRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return windowsThemeUnknown
	}
	defer key.Close()

	appsUseLightTheme, _, err := key.GetIntegerValue(themeRegistryValue)
	if err != nil {
		return windowsThemeUnknown
	}
	return windowsThemeFromRegistry(appsUseLightTheme)
}

func windowsThemeFromRegistry(appsUseLightTheme uint64) windowsTheme {
	if appsUseLightTheme == 0 {
		return windowsThemeDark
	}
	return windowsThemeLight
}

func applyWindowTheme(hwnd uintptr, theme windowsTheme) bool {
	var useDarkMode uint32
	if theme == windowsThemeDark {
		useDarkMode = 1
	}
	hresult, _, _ := procDwmSetWindowAttribute.Call(
		hwnd,
		dwmwaUseImmersiveDarkMode,
		uintptr(unsafe.Pointer(&useDarkMode)),
		unsafe.Sizeof(useDarkMode),
	)
	return int32(hresult) >= 0
}

func findGogpuWindow(pid uint32) uintptr {
	search := windowSearch{pid: pid}
	procIconEnumWindows.Call(
		findGogpuWindowCallbackHandle,
		uintptr(unsafe.Pointer(&search)),
	)
	return search.hwnd
}

// The lParam arrives as unsafe.Pointer rather than uintptr so the pointer to
// search never round-trips through an integer. EnumWindows keeps it alive for
// the duration of the call — Call is //go:uintptrescapes — but a bare uintptr
// would still lose the provenance that vet's unsafeptr check and the runtime's
// checkptr instrumentation both look for. syscall.NewCallback accepts any
// pointer-sized non-float argument, so the signature stays valid.
func findGogpuWindowCallback(hwnd uintptr, data unsafe.Pointer) uintptr {
	search := (*windowSearch)(data)
	var pid uint32
	procIconGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != search.pid || windowClassName(hwnd) != gogpuWindowClass {
		return 1
	}
	search.hwnd = hwnd
	return 0
}

func windowClassName(hwnd uintptr) string {
	buf := make([]uint16, 64)
	n, _, _ := procIconGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func isWindow(hwnd uintptr) bool {
	ok, _, _ := procIconIsWindow.Call(hwnd)
	return ok != 0
}

func windowDPI(hwnd uintptr) uint32 {
	// GetDpiForWindow was added in Windows 10 1607; on older systems (e.g.
	// Windows 8/8.1) it is absent from user32.dll and LazyProc.Call would
	// panic via mustFind. Find() performs the same lookup but returns an
	// error instead of panicking, and caches the result.
	if err := procIconGetDPIForWindow.Find(); err != nil {
		return defaultDPI
	}
	dpi, _, _ := procIconGetDPIForWindow.Call(hwnd)
	if dpi == 0 {
		return defaultDPI
	}
	return uint32(dpi)
}

func iconSizesForDPI(dpi uint32) (small, taskbar int) {
	if dpi == 0 {
		dpi = defaultDPI
	}
	return scaleIconSize(titlebarAt96, dpi), scaleIconSize(taskbarAt96, dpi)
}

func scaleIconSize(base int, dpi uint32) int {
	return (base*int(dpi) + defaultDPI/2) / defaultDPI
}

// previousIcons are the icon handles WM_SETICON reported as replaced. Zero
// means the window had none of its own and showed its class icon.
type previousIcons struct {
	small, big uintptr
}

// applyWindowIcons gives the window the embedded icons and returns the ones it
// had before.
func applyWindowIcons(hwnd uintptr, dpi uint32) (previousIcons, bool) {
	smallSize, taskbarSize := iconSizesForDPI(dpi)
	small := loadIconResource(smallSize)
	big := loadIconResource(taskbarSize)
	if small == 0 || big == 0 {
		return previousIcons{}, false
	}

	var prev previousIcons
	prev.small, _, _ = procIconSendMessageW.Call(hwnd, wmSetIcon, iconSmall, small)
	prev.big, _, _ = procIconSendMessageW.Call(hwnd, wmSetIcon, iconBig, big)
	return prev, true
}

// restoreWindowIcons hands the window its own icons back. A zero handle is sent
// as it is: WM_SETICON with no icon makes the window fall back to its class
// icon, which is what a window that never had one of its own shows.
func restoreWindowIcons(hwnd uintptr, prev previousIcons) {
	procIconSendMessageW.Call(hwnd, wmSetIcon, iconSmall, prev.small)
	procIconSendMessageW.Call(hwnd, wmSetIcon, iconBig, prev.big)
}

func loadIconResource(size int) uintptr {
	module, _, _ := procIconGetModuleHandleW.Call(0)
	icon, _, _ := procIconLoadImageW.Call(
		module,
		appIconResource, // MAKEINTRESOURCE(1)
		imageIcon,
		uintptr(size),
		uintptr(size),
		lrShared,
	)
	return icon
}
