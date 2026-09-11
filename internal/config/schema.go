package config

// The domains of F4Config's fields: the enumerations a setting may take, the
// defaults it falls back to, and the parsers that read one out of an ini file.
// They live here rather than beside the feature that acts on them because
// F4Config names them, and F4Config's package may not import a package above
// it. Task 4 moved the field types here for the same reason; these are the same
// closure one level down.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/ini"
)

// ---- from history_dialog.go ----
const (
	HistoryTypeCommands = iota
	HistoryTypeFolders
	HistoryTypeViewEdit
	HistoryTypeCount
)

const (
	HistoryShowDateTime = iota
	HistoryShowDate
	HistoryShowNone
)

// ---- from mackeys.go ----
// Mac keyboard mode settings, as written to settings.ini.
const (
	// MacKeysAuto turns the mode on for macOS and leaves it off elsewhere.
	MacKeysAuto = "auto"
	// MacKeysOn asks for the Mac layout regardless of the platform, for an
	// Apple keyboard plugged into something else.
	MacKeysOn = "on"
	// MacKeysOff keeps the Far layout on macOS too.
	MacKeysOff = "off"
)

// ParseMacKeysMode normalizes a settings.ini value. Anything unrecognized is
// "auto": a typo must not silently change the keyboard.
func ParseMacKeysMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case MacKeysOn, "1", "yes", "true":
		return MacKeysOn
	case MacKeysOff, "0", "no", "false":
		return MacKeysOff
	default:
		return MacKeysAuto
	}
}

// ---- from colorer_settings.go ----
// Cross drawing modes. FarColorer calls the option "Show cross" and lets the
// scheme pick the axes through its parameters; those are not reachable through
// the WASM build, so the axes are chosen by hand instead.
const (
	ColorerCrossOff = iota
	ColorerCrossVertical
	ColorerCrossHorizontal
	ColorerCrossBoth
)

// ---- from image_slideshow.go ----
// DefaultSlideShowDelay is how many seconds a picture stays on screen when
// the configuration has nothing sensible to say about it.
const DefaultSlideShowDelay = 5

// ---- from ttyx_keys.go ----
// DefaultTTYXKeyList is what is asked for when the feature is switched on and
// nothing else is said. Every entry is a combination f4 binds and a plain TTY
// cannot distinguish from a simpler one, and nothing here is a combination a
// desktop is likely to want for itself.
//
// Ctrl+Shift+P is in the list for the same reason as the rest of it and not
// as a special case: it is the command palette, and Shift over a bare letter
// does not change the control byte a TTY sends, so it arrives as Ctrl+P — the
// passive-panel command — unless an extended keyboard protocol is running.
// vtinput asks every terminal for one at startup; VTE answers none of them,
// which is what issue #980 is. Where there is an X server the real chord is
// still there to be taken.
const DefaultTTYXKeyList = "Ctrl+Shift+Up, Ctrl+Shift+Down, Ctrl+Shift+Left, Ctrl+Shift+Right, " +
	"Ctrl+Enter, Shift+Enter, Ctrl+Shift+Enter, Ctrl+Tab, Ctrl+Shift+Tab, " +
	"Alt+Shift+F3, Alt+Shift+F4, Ctrl+Shift+P"

// ---- from gui_font.go ----
func DefaultGuiFontSize(goos string) int {
	if goos == "darwin" {
		return 17
	}
	return 16
}

// ---- from startup_backend.go ----
// NormalizeStartupGuiBackend canonicalizes a configured graphics backend
// name, returning "" for "use automatic selection". An unrecognized name also
// becomes "": a stale config naming a backend this build does not know must
// degrade to detection rather than to a failed start.
func NormalizeStartupGuiBackend(value string) string {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if lower == "" || lower == StartupAutoBackend {
		return ""
	}
	// External UI backends are passed through untouched; RunGui routes them
	// to plughost.RunExternalUIWithMapping, which owns their naming.
	if lower == "qt" || strings.HasPrefix(lower, "ext:") {
		return trimmed
	}
	switch lower {
	case "win32", "winapi", "gdi", "win32gui":
		return "win32"
	case "gogpu", "ebiten", "x11", "wayland":
		return lower
	}
	return ""
}

// NormalizeStartupTTYBackend canonicalizes a configured console backend name.
// "win32" is the documented alias of "winapi"; both spellings are understood
// downstream, so f4 stores only the canonical one.
func NormalizeStartupTTYBackend(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ansi":
		return "ansi"
	case "winapi", "win32":
		return "winapi"
	}
	return ""
}

// ---- from compare_folders_ui.go ----
// LoadCompareOptions reads the [Compare] section, falling back to Far's
// built-in comparison for a profile that has never opened the dialog.
func LoadCompareOptions(ini *ini.File) CompareOptions {
	defaults := DefaultCompareOptions()
	flag := func(key string, def bool) bool {
		fallback := "0"
		if def {
			fallback = "1"
		}
		return ini.GetString("Compare", key, fallback) == "1"
	}
	opts := CompareOptions{
		Recursive:   flag("Recursive", defaults.Recursive),
		LimitDepth:  flag("LimitDepth", defaults.LimitDepth),
		MaxDepth:    defaults.MaxDepth,
		MarkedOnly:  flag("MarkedOnly", defaults.MarkedOnly),
		ByTime:      flag("ByTime", defaults.ByTime),
		TimeSlack:   flag("TimeSlack", defaults.TimeSlack),
		IgnoreZones: flag("IgnoreZones", defaults.IgnoreZones),
		BySize:      flag("BySize", defaults.BySize),
		ByContent:   flag("ByContent", defaults.ByContent),
		Ignore:      flag("Ignore", defaults.Ignore),
		IgnoreMode:  defaults.IgnoreMode,
		ReportEqual: flag("ReportEqual", defaults.ReportEqual),
	}
	if depth, err := strconv.Atoi(ini.GetString("Compare", "MaxDepth", strconv.Itoa(defaults.MaxDepth))); err == nil {
		opts.MaxDepth = depth
	}
	if mode, err := strconv.Atoi(ini.GetString("Compare", "IgnoreMode", strconv.Itoa(defaults.IgnoreMode))); err == nil {
		opts.IgnoreMode = mode
	}
	return opts.Normalize()
}

// writeCompareOptions emits the [Compare] section body.
func writeCompareOptions(sb *strings.Builder, opts CompareOptions) {
	bit := func(on bool) int {
		if on {
			return 1
		}
		return 0
	}
	fmt.Fprintf(sb, "Recursive = %d\n", bit(opts.Recursive))
	fmt.Fprintf(sb, "LimitDepth = %d\n", bit(opts.LimitDepth))
	fmt.Fprintf(sb, "MaxDepth = %d\n", opts.MaxDepth)
	fmt.Fprintf(sb, "MarkedOnly = %d\n", bit(opts.MarkedOnly))
	fmt.Fprintf(sb, "ByTime = %d\n", bit(opts.ByTime))
	fmt.Fprintf(sb, "TimeSlack = %d\n", bit(opts.TimeSlack))
	fmt.Fprintf(sb, "IgnoreZones = %d\n", bit(opts.IgnoreZones))
	fmt.Fprintf(sb, "BySize = %d\n", bit(opts.BySize))
	fmt.Fprintf(sb, "ByContent = %d\n", bit(opts.ByContent))
	fmt.Fprintf(sb, "Ignore = %d\n", bit(opts.Ignore))
	fmt.Fprintf(sb, "IgnoreMode = %d\n", opts.IgnoreMode)
	fmt.Fprintf(sb, "ReportEqual = %d\n", bit(opts.ReportEqual))
}

// ---- from image_decode.go ----
// ParseImageDecoderPriorities reads the DecoderPriority setting: pairs of a
// decoder name and a number, separated by commas, semicolons or vertical
// bars. A pair that does not parse is dropped rather than turned into an
// error, because a typo in the settings file should not stop pictures from
// opening.
func ParseImageDecoderPriorities(spec string) map[string]int {
	out := make(map[string]int)
	for _, part := range strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	}) {
		colon := strings.LastIndex(part, ":")
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:colon])
		value, err := strconv.Atoi(strings.TrimSpace(part[colon+1:]))
		if name == "" || err != nil {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ---- from drive_menu_options.go ----
// These flags mirror the useful part of Far's Change Drive Menu Options.
// They are intentionally kept as one persisted bit field so old profiles can
// carry the setting without another configuration section.
const (
	DriveMenuShowType uint32 = 1 << iota
	DriveMenuShowLabel
	DriveMenuUseShellName
	DriveMenuShowFilesystem
	DriveMenuShowSize
	DriveMenuShowSizeFloat
	DriveMenuShowNetworkName
	DriveMenuShowPlugins
	DriveMenuSortPluginsByHotkey
	DriveMenuShowRemovable
	DriveMenuShowCD
	DriveMenuShowRemote
	DriveMenuDetectVirtual
	DriveMenuShowBookmarks
)

const DefaultDriveMenuOptions = DriveMenuShowType |
	DriveMenuShowLabel |
	DriveMenuShowFilesystem |
	DriveMenuShowSize |
	DriveMenuShowSizeFloat |
	DriveMenuShowPlugins |
	DriveMenuShowRemovable |
	DriveMenuShowCD |
	DriveMenuShowRemote |
	DriveMenuDetectVirtual |
	DriveMenuShowBookmarks

func ParseDriveMenuOptions(value string) uint32 {
	if strings.TrimSpace(value) == "" {
		return DefaultDriveMenuOptions
	}
	var options uint32
	if _, err := fmt.Sscanf(value, "%d", &options); err != nil {
		return DefaultDriveMenuOptions
	}
	return options
}

// ---- from portable.go ----
// PortableIniPath returns the ini GetF4ConfigDir reads for the given
// executable: <exe>.ini when it exists, otherwise f4.ini in the same
// directory. The second file need not exist; the caller decides whether a
// missing file matters.
func PortableIniPath(exe string) string {
	own := exe + ".ini"
	// #nosec G703 -- own is the executable path plus ".ini", not an untrusted path component.
	if _, err := os.Stat(own); err == nil {
		return own
	}
	return filepath.Join(filepath.Dir(exe), PortableIniName)
}

// expandProfileVars expands %NAME% (Far/Windows style) and $NAME / ${NAME}
// (Unix style) in a Profile= value. F4HOME always means exeDir, even when the
// process environment carries a different value, so a profile path in an ini
// that travels with the binary keeps pointing next to that binary.
func expandProfileVars(value, exeDir string) string {
	lookup := func(name string) string {
		if strings.EqualFold(name, "F4HOME") {
			return exeDir
		}
		return os.Getenv(name)
	}
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '%' {
			end := strings.IndexByte(value[i+1:], '%')
			if end > 0 {
				out.WriteString(lookup(value[i+1 : i+1+end]))
				i += end + 1
				continue
			}
		}
		out.WriteByte(value[i])
	}
	return os.Expand(out.String(), lookup)
}

// ---- from portable.go, startup_backend.go, image_external.go ----
// PortableIniName is the file name shared by every binary in the directory
// (f4, f4-gui, f4.exe, f4-gui.exe), as asked for in unxed/f4#274.
const PortableIniName = "f4.ini"

// StartupAutoBackend is the spelling that means "decide at startup". It is
// accepted both in settings.ini and on the command line, where it is the way
// to override a configured backend back to automatic selection for one run.
const StartupAutoBackend = "auto"

// DefaultImageExternalTimeout bounds one conversion, in seconds. A raw
// photograph on a slow machine is a few seconds; a converter that has
// gone to sleep on a malformed file is forever.
const DefaultImageExternalTimeout = 20

// The parsed form of App.ImageDecoderPriority. The raw string is the
// setting; this is what a reader of the setting actually needs, so it lives
// beside it rather than in the package that sorts decoders.
var (
	imageDecoderPrioMu sync.RWMutex
	imageDecoderPrio   map[string]int
)

// SetImageDecoderPriorities replaces the overrides. A nil or empty map means
// every decoder keeps the priority it registered with.
func SetImageDecoderPriorities(prio map[string]int) {
	imageDecoderPrioMu.Lock()
	defer imageDecoderPrioMu.Unlock()
	if len(prio) == 0 {
		imageDecoderPrio = nil
		return
	}
	imageDecoderPrio = make(map[string]int, len(prio))
	for name, value := range prio {
		imageDecoderPrio[name] = value
	}
}

// ImageDecoderPriorityOf returns the priority a decoder should be sorted by.
func ImageDecoderPriorityOf(name string, registered int) int {
	imageDecoderPrioMu.RLock()
	defer imageDecoderPrioMu.RUnlock()
	if value, ok := imageDecoderPrio[name]; ok {
		return value
	}
	return registered
}
