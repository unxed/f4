package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

var (
	CachedF4ConfigDir string
	CachedF4Portable  bool
	ConfigDirOnce     sync.Once
)

// UserConfigDir is os.UserConfigDir behind a seam. os.UserConfigDir honors
// XDG_CONFIG_HOME only on Unix, not on darwin, so tests cannot redirect the
// config dir through the environment there; they override this variable
// instead of silently reading and polluting the developer's real profile.
var UserConfigDir = os.UserConfigDir

// Executable answers "where is this binary", which decides where the portable
// configuration is looked for. The real implementation lives in
// internal/update, one layer up, so the root assigns it — main.go, before the
// first GetF4ConfigDir.
//
// The default refuses rather than falling back to os.Executable, and that is
// the point. On the universal Linux build os.Executable returns the dynamic
// loader's path and returns it *successfully*, so a forgotten assignment would
// not fail: f4 would look for its ini next to ld.so, find none, and use a
// profile the user never chose. "Settings are not saved" is a much worse
// symptom than a refusal at startup.
var Executable = func() (string, error) {
	return "", errors.New("config: the executable resolver is not wired; the root must set config.Executable")
}

// LocalLangDir is the profile's lang/ directory for the loaders that honour
// UseLocalLanguageFiles, and "" when the setting is off. Empty is what
// i18n.SearchDirs takes to mean "skip the profile directory", so a caller
// passes this along without a branch of its own.
//
// The language *list* does not go through here: it enumerates what is
// installed, and a pack the profile carries stays visible in the dialog even
// while the loaders are told not to read it.
func LocalLangDir() string {
	if !App.UseLocalLanguageFiles {
		return ""
	}
	return filepath.Join(GetF4ConfigDir(), "lang")
}

func GetF4ConfigDir() string {
	ConfigDirOnce.Do(resolveConfigDir)
	// No branch of ResolveProfileDir returns "": the system branch yields at
	// least "f4", the portable branch at least "Profile". So an empty value
	// here means the resolver has not run — which a test leaves behind when it
	// swaps the cache and puts back the "" it found before the first call. Path
	// joins would silently become relative to the working directory.
	if CachedF4ConfigDir == "" {
		resolveConfigDir()
	}
	return CachedF4ConfigDir
}

func resolveConfigDir() {
	exe, err := Executable()
	if err != nil {
		exe = os.Args[0]
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	exeDir := filepath.Dir(exe)

	// F4HOME mirrors Far3's FARHOME: the directory the executable was
	// started from. It is exported so that Profile= in <exe>.ini,
	// user menu commands, macros and plugins can all refer to it, and
	// it is set before the ini is read so the ini can already use it.
	if os.Getenv("F4HOME") == "" {
		_ = os.Setenv("F4HOME", exeDir)
	}

	// Ищем f4.exe.ini (имя_бинарника.ini) или f4.ini в папке программы
	ini := ini.Load(PortableIniPath(exe))
	CachedF4ConfigDir, CachedF4Portable = ResolveProfileDir(exeDir, ini)
	if CachedF4Portable {
		_ = os.MkdirAll(CachedF4ConfigDir, 0700)
	}
}

// ResolveProfileDir picks the configuration directory from the executable
// directory and the parsed <exe>.ini, following Far3's Far.exe.ini rules:
//
//   - UseSystemProfiles missing or non-zero: the per-user directory
//     (%APPDATA%\f4, ~/.config/f4, ...) exactly as before.
//   - UseSystemProfiles=0: <exeDir>/Profile, unless [General] Profile= names
//     another directory. That value may use %F4HOME% (or $F4HOME) for the
//     executable directory and any other environment variable; a relative
//     path is taken relative to the executable directory.
//
// It is separated from GetF4ConfigDir so tests can exercise every branch
// without touching the process-wide cache.
func ResolveProfileDir(exeDir string, ini *ini.File) (dir string, portable bool) {
	if ini == nil || ini.GetString("General", "UseSystemProfiles", "1") != "0" {
		sysDir, _ := UserConfigDir()
		return filepath.Join(sysDir, "f4"), false
	}
	return PortableProfileDirFor(exeDir, ini), true
}

// PortableProfileDirFor is the directory a portable profile lands in for the
// given executable directory and ini, regardless of UseSystemProfiles.
func PortableProfileDirFor(exeDir string, ini *ini.File) string {
	custom := ""
	if ini != nil {
		custom = strings.TrimSpace(ini.GetString("General", "Profile", ""))
	}
	if custom == "" {
		return filepath.Join(exeDir, "Profile")
	}
	custom = expandProfileVars(custom, exeDir)
	if !filepath.IsAbs(custom) {
		custom = filepath.Join(exeDir, custom)
	}
	return filepath.Clean(custom)
}

// IsPortableProfile reports whether f4.ini selected the executable-local
// Profile directory. It shares GetF4ConfigDir's once-only detection so plugin
// initialization cannot disagree with the directory already in use.
func IsPortableProfile() bool {
	_ = GetF4ConfigDir()
	return CachedF4Portable
}

func parseHistoryShowTimes(value string) [HistoryTypeCount]int {
	result := [HistoryTypeCount]int{HistoryShowDateTime, HistoryShowDateTime, HistoryShowDateTime}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	for i := 0; i < len(parts) && i < len(result); i++ {
		mode, err := strconv.Atoi(parts[i])
		if err == nil && mode >= HistoryShowDateTime && mode <= HistoryShowNone {
			result[i] = mode
		}
	}
	return result
}

func ResetConfigDirForTest() {
	ConfigDirOnce = sync.Once{}
	CachedF4ConfigDir = ""
	CachedF4Portable = false
}

type PanelScrollbarMode int

const (
	PanelScrollbarOff PanelScrollbarMode = iota
	PanelScrollbarMinimal
	PanelScrollbarFull
)

func (m PanelScrollbarMode) String() string {
	switch m {
	case PanelScrollbarMinimal:
		return "minimal"
	case PanelScrollbarFull:
		return "full"
	default:
		return "off"
	}
}

type WorkspaceTabNumberingMode int

const (
	WorkspaceTabNumbersAlways WorkspaceTabNumberingMode = iota
	WorkspaceTabNumbersSession
	WorkspaceTabNumbersOrder
)

func (m WorkspaceTabNumberingMode) String() string {
	switch m {
	case WorkspaceTabNumbersSession:
		return "session"
	case WorkspaceTabNumbersOrder:
		return "order"
	default:
		return "always"
	}
}

func ParseWorkspaceTabNumberingMode(value string) WorkspaceTabNumberingMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "session":
		return WorkspaceTabNumbersSession
	case "order":
		return WorkspaceTabNumbersOrder
	default:
		return WorkspaceTabNumbersAlways
	}
}

// PanelNavigationMode controls how unmodified keyboard input is interpreted
// while file panels are visible.
type PanelNavigationMode int

const (
	NavigationClassic PanelNavigationMode = iota
	NavigationVim
	NavigationSearchFirst
)

func (m PanelNavigationMode) String() string {
	switch m {
	case NavigationVim:
		return "vim"
	case NavigationSearchFirst:
		return "search"
	default:
		return "classic"
	}
}

func ParsePanelNavigationMode(value string) PanelNavigationMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "vim":
		return NavigationVim
	case "search":
		return NavigationSearchFirst
	default:
		return NavigationClassic
	}
}

// StartupMode says which renderer family f4 starts in when nothing on the
// command line settles the question. It exists so that a user who always
// wants the same answer does not have to retype --gui or --tty on every run
// (issue #601).
type StartupMode int

const (
	// StartupModeAuto keeps the historical behavior: probe the environment
	// and pick a graphical window when one is available.
	StartupModeAuto StartupMode = iota
	// StartupModeTTY always starts in the terminal, even on a desktop where
	// automatic selection would have opened a window.
	StartupModeTTY
	// StartupModeGui always starts in a graphical window, including on the
	// platforms where automatic selection deliberately does not try one
	// (native Windows and Wine, see shouldTryGui).
	StartupModeGui
)

func (m StartupMode) String() string {
	switch m {
	case StartupModeTTY:
		return "tty"
	case StartupModeGui:
		return "gui"
	default:
		return "auto"
	}
}

// ParseStartupMode maps a settings.ini value to a mode. Anything unknown
// means "auto": a hand-edited or newer-version config must not be able to
// stop f4 from starting.
func ParseStartupMode(value string) StartupMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tty", "console", "terminal", "text":
		return StartupModeTTY
	case "gui", "graphics", "graphical", "window":
		return StartupModeGui
	default:
		return StartupModeAuto
	}
}

// What "ignore" means when comparing contents.
const (
	// CompareIgnoreEOL treats CRLF, CR and LF as the same line break, so a
	// file that has travelled through Windows still equals its Unix twin.
	CompareIgnoreEOL = iota
	// CompareIgnoreSpaces drops every whitespace byte, which also covers
	// line breaks: indentation changes stop counting as differences.
	CompareIgnoreSpaces
)

const (
	// CompareMaxDepthLimit is the nesting level Far3's dialog offers, and
	// the largest value the field accepts.
	CompareMaxDepthLimit = 99
)

// CompareOptions mirrors the Advanced Compare dialog field for field.
type CompareOptions struct {
	// Recursive walks subfolders instead of comparing only what the two
	// panels show side by side.
	Recursive bool
	// LimitDepth caps that walk at MaxDepth levels below the panel folder.
	LimitDepth bool
	MaxDepth   int
	// MarkedOnly narrows the comparison to the items marked in each panel.
	MarkedOnly bool

	// ByTime, BySize and ByContent are the comparison criteria. A pair of
	// files differs as soon as one of the enabled criteria says so.
	ByTime bool
	// TimeSlack allows compareTimeSlack between two modification times.
	TimeSlack bool
	// IgnoreZones additionally ignores differences that are a whole
	// number of quarter hours, i.e. a file stamped in another time zone.
	IgnoreZones bool
	BySize      bool
	ByContent   bool

	// Ignore enables the content filter selected by IgnoreMode.
	Ignore     bool
	IgnoreMode int

	// ReportEqual asks for a message when the comparison found nothing.
	// Without it a comparison of two identical folders looks like a
	// command that did not run.
	ReportEqual bool
}

// DefaultCompareOptions is Far's built-in "Compare folders": names, times
// and sizes, across the whole tree, and a word when nothing differs.
func DefaultCompareOptions() CompareOptions {
	return CompareOptions{
		Recursive:   true,
		MaxDepth:    CompareMaxDepthLimit,
		ByTime:      true,
		TimeSlack:   true,
		IgnoreZones: true,
		BySize:      true,
		IgnoreMode:  CompareIgnoreEOL,
		ReportEqual: true,
	}
}

// Normalize repairs values a hand-edited config may hold and reports the
// options actually usable. Nothing here silently turns a criterion on: a
// comparison with no criterion at all is refused by the dialog instead.
func (o CompareOptions) Normalize() CompareOptions {
	if o.MaxDepth < 1 {
		o.MaxDepth = 1
	}
	if o.MaxDepth > CompareMaxDepthLimit {
		o.MaxDepth = CompareMaxDepthLimit
	}
	if o.IgnoreMode != CompareIgnoreSpaces {
		o.IgnoreMode = CompareIgnoreEOL
	}
	return o
}

// HasCriteria reports whether anything at all is being compared. Presence
// alone is not a criterion: two folders holding the same names would then
// always come back equal, whatever the files inside them look like.
func (o CompareOptions) HasCriteria() bool {
	return o.ByTime || o.BySize || o.ByContent
}

func ParsePanelScrollbarMode(value string) PanelScrollbarMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minimal":
		return PanelScrollbarMinimal
	case "full":
		return PanelScrollbarFull
	default:
		return PanelScrollbarOff
	}
}

type F4Config struct {
	ColorStyle               string
	Language                 string
	FallbackLanguage         string
	HelpLanguage             string
	UseLocalLanguageFiles    bool
	AlwaysShowMenuBar        bool
	WorkspaceTabMode         int
	WorkspaceTabsOverlay     bool
	CtrlTabShowsMenu         bool
	AltNumberSwitchesTabs    bool
	RestoreWorkspaceTabs     bool
	WorkspaceTabNumbering    WorkspaceTabNumberingMode
	MacKeyboard              string
	ShowHiddenFiles          bool
	ShowDirPrefix            bool
	ShowHighlightMarks       bool
	SeparateFileExtensions   bool
	PanelScrollbarMode       PanelScrollbarMode
	ShowPanelFileInfo        bool
	SavePanelPaths           bool
	DriveMenuOptions         uint32 // display/filter flags for the Alt+F1/Alt+F2 menu
	InfoPanelBytes           bool   // Ctrl+L info panel: true = raw bytes, false = human (GiB/MiB…)
	InfoPanelCPUGPU          bool   // Ctrl+L info panel: show CPU and GPU sections (off by default)
	EscTogglePanels          bool   // ESC toggles panels visibility (Far ships this as a macro; on by default)
	TerminalCtrlNWorkspace   bool   // reserve Ctrl+N in terminal views for cloning panels to a workspace
	KeepTerminalCursor       bool
	ConsoleMode              string // "own" | "host" (default "own")
	ConsoleOverlayUI         bool   // Show f4 command line and keybar overlay on top of host console (default false)
	AnnounceKittyTerm        bool   // introduce the built-in terminal as kitty, so that image tools use the graphics protocol
	CommandLineAutoComplete  bool
	NavigationMode           PanelNavigationMode
	SearchCommandStayFocused bool
	SyncPanelLoad            bool
	SearchExactOnHit         bool // QuickSearch keeps only exact matches when at least one exists
	ApplyCommandParallelism  int  // 0 = unlimited; absent config defaults to runtime.NumCPU()
	EditorAutoComplete       bool
	EditorAutoCompleteMask   string
	EditorExpandTabs         int
	EditorAutoIndent         bool
	EditorCursorBeyondEOL    bool
	EditorTabSize            int
	EditorUseEditorConfig    bool
	EditorCrosshair          bool
	EditorMarkOccurrences    bool
	UseExternalEditor        bool
	ExternalEditorCommand    string
	ExternalEditorConsole    string
	ExternalEditorGUI        string
	EditorAutodetectCodePage bool
	EditorHighlighter        string
	EditorSyntaxAnimation    bool
	EditorColorerScheme      string
	EditorColorerBackground  bool
	EditorColorerSyntax      bool
	EditorColorerCatalog     string
	EditorCrossMode          int
	EditorDefaultCodePage    int
	// EditorMemoryMap lets the editor map a local file instead of reading it
	// in chunks. Off means every buffer takes the lazily fetched path, which
	// is the escape hatch for a file system where mapping misbehaves.
	EditorMemoryMap          bool
	ViewerAutodetectCodePage bool
	ViewerDefaultCodePage    int
	// SystemANSICodePage and SystemOEMCodePage pin what "ANSI" and "OEM"
	// mean on a system that cannot be asked. 0 keeps the codepage deduced
	// from the locale.
	SystemANSICodePage int
	SystemOEMCodePage  int
	// Wheel scroll speed (lines per notch) per area and direction.
	// 0 = follow the system setting.
	WheelPanelUp    int
	WheelPanelDown  int
	WheelEditorUp   int
	WheelEditorDown int
	WheelViewerUp   int
	WheelViewerDown int
	WheelMenuUp     int
	WheelMenuDown   int
	WheelTableUp    int
	WheelTableDown  int
	// Path hints (autocomplete in path inputs and the command line).
	PathHintTimeout     int  // seconds for a VFS ReadDir behind a hint
	PathHintFullPath    bool // show full paths in the hint, false = final element only
	PathHintSource      int  // 0 = active panel, 1 = passive panel, 2 = both
	PathHintMaxVisible  int  // visible rows cap in the hint list
	PathHintPerCategory bool // the cap applies per category (active/passive/history)
	DialogAutoComplete  bool // drop-down while typing in fields that have history
	// HistoryShowTimes controls the timestamp presentation in command, folder,
	// and viewer/editor history dialogs: date+time, date, or hidden.
	HistoryShowTimes       [HistoryTypeCount]int
	HistoryDirsPrefixLen   int // command-history directory prefix width
	SlideShowDelay         int
	ImageOverlay           bool
	VideoPauseOnFocusLoss  bool
	ImageX11OffsetX        int
	ImageX11OffsetY        int
	TTYXKeys               bool
	TTYXKeyList            string
	ImageExternalTimeout   int
	ImageDecoderPriority   string
	RegisteredPlugins      []string
	ConfirmCopy            bool
	ConfirmMove            bool
	ConfirmDelete          bool
	UseTrash               bool
	ConfirmExit            bool
	DeleteCancelFocused    bool
	AutoSaveSettings       bool
	AutoSaveDialogSettings bool
	AutoSavePanelSettings  bool
	AutoSaveCurrentPanel   bool
	AutoSaveGUIWindow      bool
	DefaultFileOpMode      int
	FileOpPathDisplay      int
	CopyAccessRights       int
	MacroRecordFormat      int
	GuiFont                string
	GuiUseSystemMonospace  bool
	GuiFontSize            int
	GuiCols                int
	GuiRows                int
	GuiPosX                int
	GuiPosY                int
	GuiPositionSaved       bool
	// StartupMode, GuiBackend and TTYBackend answer "what should plain `f4`
	// do?". They are only defaults: --gui/--tty still win on any single run.
	// An empty backend means automatic selection.
	StartupMode            StartupMode
	GuiBackend             string
	TTYBackend             string
	ConsoleTitleTemplate   string
	DisplayFullPathInTitle bool
	UpdateChannel          int // 0 = Stable, 1 = Nightly
	UpdateInterval         int // 0 = Never, 1 = Every start, 2 = Daily, 3 = Weekly
	EnforceColorCorrection bool
	HighlightPriority      int    // 0 = User wins, 1 = Theme wins
	LastUpdateCheck        int64  // Unix timestamp
	LastUpdateVersion      string // Version string or PublishedAt timestamp

	// [Proxy] applies to everything f4 sends out on its own: update checks
	// and downloads, the plugin ring, colorer schemes and netfox site
	// connections. A netfox connection may override it, see netproxy.
	ProxyMode int // netproxy.Mode*: 1 = system env (default), 2 = direct, 3 = HTTP, 4 = SOCKS5
	ProxyHost string
	ProxyPort string
	ProxyUser string
	ProxyPass string

	// [Layout] mirrors far2l's config.ini section of the same name so
	// a config shared with far2l keeps working in both. Adjusted by
	// Ctrl+Left/Right (width split) and Ctrl+Up/Down (panel/terminal
	// vertical split, applied symmetrically to both height fields).
	// Ctrl+Clear resets all three to 0.
	WidthDecrement       int
	LeftHeightDecrement  int
	RightHeightDecrement int

	// LayoutExtras is any [Layout] key we don't recognise (e.g. far2l's
	// FullscreenHelp, PanelsDisposition). Read at LoadConfig and written
	// back verbatim on SaveConfig so f4 doesn't strip far2l-only options
	// from a shared config file.
	LayoutExtras map[string]string

	// Compare keeps what the folder comparison dialog was last set to,
	// the way Far3's Advanced Compare remembers its own options.
	Compare CompareOptions
}

var App = F4Config{
	ColorStyle:               "Modern",
	Language:                 "en",
	FallbackLanguage:         "",
	HelpLanguage:             "en",
	UseLocalLanguageFiles:    false,
	AlwaysShowMenuBar:        false,
	WorkspaceTabMode:         int(vtui.WorkspaceTabsAlways),
	WorkspaceTabsOverlay:     true,
	CtrlTabShowsMenu:         false,
	AltNumberSwitchesTabs:    true,
	RestoreWorkspaceTabs:     true,
	WorkspaceTabNumbering:    WorkspaceTabNumbersAlways,
	MacKeyboard:              MacKeysAuto,
	ShowHiddenFiles:          true,
	ShowDirPrefix:            false,
	ShowHighlightMarks:       false,
	SeparateFileExtensions:   false,
	PanelScrollbarMode:       PanelScrollbarMinimal,
	ShowPanelFileInfo:        false,
	SavePanelPaths:           true,
	DriveMenuOptions:         DefaultDriveMenuOptions,
	InfoPanelBytes:           false,
	InfoPanelCPUGPU:          false,
	EscTogglePanels:          true,
	TerminalCtrlNWorkspace:   true,
	KeepTerminalCursor:       false,
	ConsoleMode:              "own",
	ConsoleOverlayUI:         false,
	AnnounceKittyTerm:        true,
	CommandLineAutoComplete:  true,
	NavigationMode:           NavigationClassic,
	SearchCommandStayFocused: false,
	SyncPanelLoad:            false,
	SearchExactOnHit:         false,
	ApplyCommandParallelism:  runtime.NumCPU(),
	EditorAutoComplete:       true,
	EditorAutoCompleteMask:   "*.go;*.c;*.cpp;*.h;*.hpp;*.py;*.js;*.ts;*.rs;*.java;*.sh;*.txt;*.md;*.html;*.css;*.json",
	EditorExpandTabs:         0,
	EditorAutoIndent:         true,
	EditorCursorBeyondEOL:    false,
	EditorTabSize:            4,
	EditorUseEditorConfig:    true,
	EditorCrosshair:          false,
	EditorMarkOccurrences:    true,
	UseExternalEditor:        false,
	ExternalEditorCommand:    "",
	ExternalEditorConsole:    "",
	ExternalEditorGUI:        "",
	EditorAutodetectCodePage: true,
	EditorHighlighter:        "Chroma",
	EditorSyntaxAnimation:    false,
	EditorColorerScheme:      "",
	EditorColorerBackground:  true,
	EditorColorerSyntax:      true,
	EditorColorerCatalog:     "",
	EditorCrossMode:          ColorerCrossBoth,
	EditorDefaultCodePage:    65001,
	EditorMemoryMap:          true,
	ViewerAutodetectCodePage: true,
	ViewerDefaultCodePage:    65001,
	WheelPanelUp:             0,
	WheelPanelDown:           0,
	WheelEditorUp:            0,
	WheelEditorDown:          0,
	WheelViewerUp:            0,
	WheelViewerDown:          0,
	WheelMenuUp:              0,
	WheelMenuDown:            0,
	WheelTableUp:             0,
	WheelTableDown:           0,
	PathHintTimeout:          2,
	PathHintFullPath:         false,
	PathHintSource:           2,
	PathHintMaxVisible:       5,
	PathHintPerCategory:      true,
	DialogAutoComplete:       true,
	HistoryShowTimes:         [HistoryTypeCount]int{HistoryShowDateTime, HistoryShowDateTime, HistoryShowDateTime},
	HistoryDirsPrefixLen:     24,
	SlideShowDelay:           DefaultSlideShowDelay,
	ImageOverlay:             true,
	TTYXKeys:                 true,
	TTYXKeyList:              DefaultTTYXKeyList,
	ImageExternalTimeout:     DefaultImageExternalTimeout,
	ImageDecoderPriority:     "",
	ConfirmCopy:              true,
	ConfirmMove:              true,
	ConfirmDelete:            true,
	UseTrash:                 false,
	ConfirmExit:              true,
	DeleteCancelFocused:      false,
	AutoSaveSettings:         true,
	AutoSaveDialogSettings:   true,
	AutoSavePanelSettings:    true,
	AutoSaveCurrentPanel:     true,
	AutoSaveGUIWindow:        true,
	DefaultFileOpMode:        0,
	FileOpPathDisplay:        0,
	CopyAccessRights:         0,
	GuiFont:                  "",
	GuiUseSystemMonospace:    true,
	GuiFontSize:              DefaultGuiFontSize(runtime.GOOS),
	GuiCols:                  100,
	GuiRows:                  30,
	GuiPosX:                  0,
	GuiPosY:                  0,
	GuiPositionSaved:         false,
	StartupMode:              StartupModeAuto,
	GuiBackend:               "",
	TTYBackend:               "",
	ConsoleTitleTemplate:     "f4 %Ver %Platform %Admin - %State",
	DisplayFullPathInTitle:   false,
	UpdateChannel:            0,
	ProxyMode:                netproxy.ModeSystem,
	UpdateInterval:           3, // Default to Weekly
	EnforceColorCorrection:   true,
	HighlightPriority:        0,
	LastUpdateCheck:          0,
	LastUpdateVersion:        "",
	Compare:                  DefaultCompareOptions(),
}

var GetUserConfigIniPath = func() string {
	return filepath.Join(GetF4ConfigDir(), "settings.ini")
}

var GetConfigIniPaths = func() []string {
	userPath := GetUserConfigIniPath()
	if IsPortableProfile() {
		// In portable mode (UseSystemProfiles=0) all config lives under
		// <exeDir>/Profile, so skip the machine-wide paths: otherwise system
		// settings from ProgramData (Windows) or /etc/f4 (Unix) would leak into
		// the isolated portable profile.
		return []string{userPath}
	}
	if runtime.GOOS == "windows" {
		progData := os.Getenv("ProgramData")
		if progData != "" {
			return []string{filepath.Join(progData, "f4", "settings.ini"), userPath}
		}
		return []string{userPath}
	}
	// For unix-like systems
	return []string{"/etc/f4/settings.ini", userPath}
}

// normalizeHighlighter maps an arbitrary config value to one of the engines
// the editor knows about, falling back to the default one.
func normalizeHighlighter(name string) string {
	for _, known := range []string{"Chroma", "Colorer", "None"} {
		if strings.EqualFold(name, known) {
			return known
		}
	}
	return "Chroma"
}

func LoadConfig() {
	paths := GetConfigIniPaths()
	merged := ini.New()

	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			vtui.DebugLog("CONFIG: Loading and merging config from %s", path)
			merged.Merge(ini.Load(path))
		}
	}

	App.ShowHiddenFiles = merged.GetString("Panel", "ShowHiddenFiles", "1") == "1"
	App.ColorStyle = merged.GetString("Interface", "ColorStyle", "Radiola")
	// "Far2l Dark" was an approximate port of the far2l theme "default dark".
	// It has been replaced by an exact one; carry existing configs over.
	if strings.EqualFold(App.ColorStyle, "Far2l Dark") {
		App.ColorStyle = "Default Dark"
	}
	App.Language = merged.GetString("Interface", "Language", "en")
	App.FallbackLanguage = merged.GetString("Interface", "FallbackLanguage", "")
	App.HelpLanguage = merged.GetString("Interface", "HelpLanguage", "en")
	App.UseLocalLanguageFiles = merged.GetString("Interface", "UseLocalLanguageFiles", "0") == "1"
	App.ConsoleTitleTemplate = merged.GetString("Interface", "ConsoleTitleTemplate", "f4 %Ver %Platform %Admin - %State")
	App.DisplayFullPathInTitle = merged.GetString("Interface", "DisplayFullPathInTitle", "0") == "1"
	App.AlwaysShowMenuBar = merged.GetString("Interface", "AlwaysShowMenuBar", "0") == "1"
	switch strings.ToLower(merged.GetString("Interface", "WorkspaceTabMode", "always")) {
	case "always":
		App.WorkspaceTabMode = int(vtui.WorkspaceTabsAlways)
	case "ctrl":
		App.WorkspaceTabMode = int(vtui.WorkspaceTabsOnCtrl)
	case "never":
		App.WorkspaceTabMode = int(vtui.WorkspaceTabsNever)
	default:
		App.WorkspaceTabMode = int(vtui.WorkspaceTabsMultiple)
	}
	App.WorkspaceTabsOverlay = merged.GetString("Interface", "WorkspaceTabsOverlay", "1") != "0"
	App.CtrlTabShowsMenu = strings.EqualFold(merged.GetString("Interface", "CtrlTabMode", "direct"), "menu")
	App.AltNumberSwitchesTabs = merged.GetString("Interface", "AltNumberSwitchesTabs", "1") != "0"
	App.RestoreWorkspaceTabs = merged.GetString("Interface", "RestoreWorkspaceTabs", "1") != "0"
	App.WorkspaceTabNumbering = ParseWorkspaceTabNumberingMode(merged.GetString("Interface", "WorkspaceTabNumbering", "always"))
	App.MacKeyboard = ParseMacKeysMode(merged.GetString("Interface", "MacKeyboard", MacKeysAuto))
	if App.ConsoleTitleTemplate == "f4 - %State" {
		App.ConsoleTitleTemplate = "f4 %Ver %Platform %Admin - %State"
	}
	App.ShowDirPrefix = merged.GetString("Panel", "ShowDirPrefix", "0") == "1"
	App.ShowHighlightMarks = merged.GetString("Panel", "ShowHighlightMarks", "0") == "1"
	App.SeparateFileExtensions = merged.GetString("Panel", "SeparateFileExtensions", "0") == "1"
	if mode := merged.GetString("Panel", "PanelScrollbarMode", ""); mode != "" {
		App.PanelScrollbarMode = ParsePanelScrollbarMode(mode)
	} else {
		// Migration from the short-lived boolean setting. When neither setting
		// exists, use the new default: the minimal scrollbar.
		switch merged.GetString("Panel", "ShowPanelScrollbars", "") {
		case "1":
			App.PanelScrollbarMode = PanelScrollbarFull
		case "0":
			App.PanelScrollbarMode = PanelScrollbarOff
		default:
			App.PanelScrollbarMode = PanelScrollbarMinimal
		}
	}
	App.ShowPanelFileInfo = merged.GetString("Panel", "ShowPanelFileInfo", "0") == "1"
	App.SavePanelPaths = merged.GetString("Panel", "SavePanelPaths", "1") == "1"
	App.DriveMenuOptions = ParseDriveMenuOptions(merged.GetString("Panel", "DriveMenuOptions", ""))
	App.InfoPanelBytes = merged.GetString("Panel", "InfoPanelBytes", "0") == "1"
	App.InfoPanelCPUGPU = merged.GetString("Panel", "InfoPanelCPUGPU", "0") == "1"
	App.EscTogglePanels = merged.GetString("Panel", "EscTogglePanels", "1") == "1"
	App.TerminalCtrlNWorkspace = merged.GetString("Panel", "TerminalCtrlNWorkspace", "1") == "1"
	App.KeepTerminalCursor = merged.GetString("Panel", "KeepTerminalCursor", "0") == "1"
	App.ConsoleMode = merged.GetString("Panel", "ConsoleMode", "own")
	App.ConsoleOverlayUI = merged.GetString("Panel", "ConsoleOverlayUI", "0") == "1"
	App.CommandLineAutoComplete = merged.GetString("Panel", "CommandLineAutoComplete", "1") == "1"
	if mode := merged.GetString("Panel", "NavigationMode", ""); mode != "" {
		App.NavigationMode = ParsePanelNavigationMode(mode)
	} else if merged.GetString("Panel", "VimHotkeys", "0") == "1" {
		// Migration from settings written before NavigationMode was introduced.
		App.NavigationMode = NavigationVim
	} else {
		App.NavigationMode = NavigationClassic
	}
	App.SearchCommandStayFocused = merged.GetString("Panel", "SearchCommandStayFocused", "0") == "1"
	App.SyncPanelLoad = merged.GetString("Panel", "SyncPanelLoad", "0") == "1"
	App.SearchExactOnHit = merged.GetString("Panel", "SearchExactOnHit", "0") == "1"
	App.ApplyCommandParallelism = runtime.NumCPU()
	_, _ = fmt.Sscanf(merged.GetString("Panel", "ApplyCommandParallelism", fmt.Sprintf("%d", runtime.NumCPU())), "%d", &App.ApplyCommandParallelism)
	if App.ApplyCommandParallelism < 0 {
		App.ApplyCommandParallelism = runtime.NumCPU()
	}
	_, _ = fmt.Sscanf(merged.GetString("Panel", "DefaultFileOpMode", "0"), "%d", &App.DefaultFileOpMode)
	App.ConfirmCopy = merged.GetString("System", "ConfirmCopy", "1") == "1"
	App.ConfirmMove = merged.GetString("System", "ConfirmMove", "1") == "1"
	App.ConfirmDelete = merged.GetString("System", "ConfirmDelete", "1") == "1"
	App.UseTrash = merged.GetString("System", "UseTrash", "0") == "1"
	App.ConfirmExit = merged.GetString("System", "ConfirmExit", "1") == "1"
	App.DeleteCancelFocused = merged.GetString("System", "DeleteCancelFocused", "0") == "1"
	legacyAutoSave := merged.GetString("System", "AutoSaveSettings", "1") != "0"
	App.AutoSaveSettings = legacyAutoSave
	autoSaveDefault := "0"
	if legacyAutoSave {
		autoSaveDefault = "1"
	}
	App.AutoSaveDialogSettings = merged.GetString("System", "AutoSaveDialogSettings", autoSaveDefault) != "0"
	App.AutoSavePanelSettings = merged.GetString("System", "AutoSavePanelSettings", autoSaveDefault) != "0"
	App.AutoSaveCurrentPanel = merged.GetString("System", "AutoSaveCurrentPanel", autoSaveDefault) != "0"
	App.AutoSaveGUIWindow = merged.GetString("System", "AutoSaveGUIWindow", autoSaveDefault) != "0"
	App.AnnounceKittyTerm = merged.GetString("System", "AnnounceKittyTerm", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("System", "MacroRecordFormat", "0"), "%d", &App.MacroRecordFormat)
	App.SystemANSICodePage = parseForcedCodePage(merged.GetString("System", "ANSICodePage", ""))
	App.SystemOEMCodePage = parseForcedCodePage(merged.GetString("System", "OEMCodePage", ""))
	applyForcedCodePages()
	_, _ = fmt.Sscanf(merged.GetString("Panel", "FileOpPathDisplay", "0"), "%d", &App.FileOpPathDisplay)
	_, _ = fmt.Sscanf(merged.GetString("Panel", "CopyAccessRights", "0"), "%d", &App.CopyAccessRights)
	if App.CopyAccessRights < 0 || App.CopyAccessRights > 2 {
		App.CopyAccessRights = 0
	}
	App.GuiFont = merged.GetString("Appearance", "GuiFont", "")
	App.GuiUseSystemMonospace = merged.GetString("Appearance", "GuiUseSystemMonospace", "1") == "1"
	defaultFontSize := DefaultGuiFontSize(runtime.GOOS)
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiFontSize", fmt.Sprintf("%d", defaultFontSize)), "%d", &App.GuiFontSize)
	if App.GuiFontSize <= 0 {
		App.GuiFontSize = defaultFontSize
	}
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiCols", "100"), "%d", &App.GuiCols)
	if App.GuiCols <= 0 {
		App.GuiCols = 100
	}
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiRows", "30"), "%d", &App.GuiRows)
	if App.GuiRows <= 0 {
		App.GuiRows = 30
	}
	guiPosX, xErr := strconv.Atoi(merged.GetString("Appearance", "GuiPosX", ""))
	guiPosY, yErr := strconv.Atoi(merged.GetString("Appearance", "GuiPosY", ""))
	App.GuiPositionSaved = xErr == nil && yErr == nil
	if App.GuiPositionSaved {
		App.GuiPosX = guiPosX
		App.GuiPosY = guiPosY
	} else {
		App.GuiPosX = 0
		App.GuiPosY = 0
	}
	App.StartupMode = ParseStartupMode(merged.GetString("Startup", "Mode", "auto"))
	App.GuiBackend = NormalizeStartupGuiBackend(merged.GetString("Startup", "GuiBackend", ""))
	App.TTYBackend = NormalizeStartupTTYBackend(merged.GetString("Startup", "TTYBackend", ""))
	App.EnforceColorCorrection = merged.GetString("Dialogs", "EnforceColorCorrection", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "HighlightPriority", "0"), "%d", &App.HighlightPriority)
	_, _ = fmt.Sscanf(merged.GetString("Update", "Channel", "0"), "%d", &App.UpdateChannel)
	_, _ = fmt.Sscanf(merged.GetString("Update", "Interval", "3"), "%d", &App.UpdateInterval)
	_, _ = fmt.Sscanf(merged.GetString("Update", "LastCheck", "0"), "%d", &App.LastUpdateCheck)
	App.LastUpdateVersion = merged.GetString("Update", "LastVersion", "")

	// The proxy password is stored obfuscated, exactly like netfox stores
	// site passwords; a hand-written plain one keeps working.
	_, _ = fmt.Sscanf(merged.GetString("Proxy", "Mode", "1"), "%d", &App.ProxyMode)
	App.ProxyHost = merged.GetString("Proxy", "Host", "")
	App.ProxyPort = merged.GetString("Proxy", "Port", "")
	App.ProxyUser = merged.GetString("Proxy", "User", "")
	App.ProxyPass = netproxy.DecodeSecret(merged.GetString("Proxy", "Password", ""))
	ApplyProxySettings()

	App.EditorAutoComplete = merged.GetString("Editor", "AutoComplete", "1") == "1"
	App.EditorAutoCompleteMask = merged.GetString("Editor", "AutoCompleteMask", "*.go;*.c;*.cpp;*.h;*.hpp;*.py;*.js;*.ts;*.rs;*.java;*.sh;*.txt;*.md;*.html;*.css;*.json")

	App.EditorExpandTabs = 0
	_, _ = fmt.Sscanf(merged.GetString("Editor", "ExpandTabs", "0"), "%d", &App.EditorExpandTabs)
	App.EditorAutoIndent = merged.GetString("Editor", "AutoIndent", "1") == "1"
	App.EditorCursorBeyondEOL = merged.GetString("Editor", "CursorBeyondEOL", "0") == "1"
	App.EditorUseEditorConfig = merged.GetString("Editor", "UseEditorConfig", "1") == "1"
	App.EditorCrosshair = merged.GetString("Editor", "Crosshair", "0") == "1"
	App.EditorMarkOccurrences = merged.GetString("Editor", "MarkOccurrences", "1") == "1"
	App.EditorAutodetectCodePage = merged.GetString("Editor", "AutodetectCodePage", "1") == "1"
	App.EditorMemoryMap = merged.GetString("Editor", "MemoryMap", "1") == "1"
	App.EditorHighlighter = normalizeHighlighter(merged.GetString("Editor", "Highlighter", "Chroma"))
	App.EditorSyntaxAnimation = merged.GetString("Editor", "SyntaxAnimation", "0") == "1"
	App.EditorColorerScheme = merged.GetString("Editor", "ColorerScheme", "")
	App.EditorColorerBackground = merged.GetString("Editor", "ColorerBackground", "1") == "1"
	App.EditorColorerSyntax = merged.GetString("Editor", "ColorerSyntax", "1") == "1"
	App.EditorColorerCatalog = merged.GetString("Editor", "ColorerCatalog", "")
	App.EditorCrossMode = ColorerCrossBoth
	_, _ = fmt.Sscanf(merged.GetString("Editor", "CrossMode", "3"), "%d", &App.EditorCrossMode)
	if App.EditorCrossMode < ColorerCrossOff || App.EditorCrossMode > ColorerCrossBoth {
		App.EditorCrossMode = ColorerCrossBoth
	}
	_, _ = fmt.Sscanf(merged.GetString("Editor", "DefaultCodePage", "65001"), "%d", &App.EditorDefaultCodePage)
	App.ViewerAutodetectCodePage = merged.GetString("Viewer", "AutodetectCodePage", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("Viewer", "DefaultCodePage", "65001"), "%d", &App.ViewerDefaultCodePage)

	// [Mouse] — wheel scroll speed (lines per notch), 0 = system default.
	App.WheelPanelUp = LoadWheelLines(merged, "PanelUp")
	App.WheelPanelDown = LoadWheelLines(merged, "PanelDown")
	App.WheelEditorUp = LoadWheelLines(merged, "EditorUp")
	App.WheelEditorDown = LoadWheelLines(merged, "EditorDown")
	App.WheelViewerUp = LoadWheelLines(merged, "ViewerUp")
	App.WheelViewerDown = LoadWheelLines(merged, "ViewerDown")
	App.WheelMenuUp = LoadWheelLines(merged, "MenuUp")
	App.WheelMenuDown = LoadWheelLines(merged, "MenuDown")
	App.WheelTableUp = LoadWheelLines(merged, "TableUp")
	App.WheelTableDown = LoadWheelLines(merged, "TableDown")

	// [PathHints]
	App.PathHintTimeout = 2
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "Timeout", "2"), "%d", &App.PathHintTimeout)
	if App.PathHintTimeout < 1 {
		App.PathHintTimeout = 1
	}
	App.PathHintFullPath = merged.GetString("PathHints", "FullPath", "0") == "1"
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "Source", "2"), "%d", &App.PathHintSource)
	App.PathHintMaxVisible = 5
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "MaxVisible", "5"), "%d", &App.PathHintMaxVisible)
	if App.PathHintMaxVisible < 1 {
		App.PathHintMaxVisible = 1
	}
	App.PathHintPerCategory = merged.GetString("PathHints", "PerCategory", "1") == "1"
	App.DialogAutoComplete = merged.GetString("PathHints", "DialogAutoComplete", "1") == "1"
	App.HistoryShowTimes = parseHistoryShowTimes(merged.GetString("History", "ShowTimes", "0,0,0"))
	if configured := merged.GetString("History", "HistoryShowTimes", ""); configured != "" {
		App.HistoryShowTimes = parseHistoryShowTimes(configured)
	}
	App.HistoryDirsPrefixLen = 24
	_, _ = fmt.Sscanf(merged.GetString("History", "DirsPrefixLen", "24"), "%d", &App.HistoryDirsPrefixLen)
	if configured := merged.GetString("History", "HistoryDirsPrefixLen", ""); configured != "" {
		_, _ = fmt.Sscanf(configured, "%d", &App.HistoryDirsPrefixLen)
	}
	if App.HistoryDirsPrefixLen < 4 {
		App.HistoryDirsPrefixLen = 4
	}
	App.SlideShowDelay = DefaultSlideShowDelay
	_, _ = fmt.Sscanf(merged.GetString("Images", "SlideShowDelay", "5"), "%d", &App.SlideShowDelay)
	if App.SlideShowDelay <= 0 {
		App.SlideShowDelay = DefaultSlideShowDelay
	}
	App.ImageExternalTimeout = DefaultImageExternalTimeout
	_, _ = fmt.Sscanf(merged.GetString("Images", "ExternalTimeout", "20"), "%d", &App.ImageExternalTimeout)
	if App.ImageExternalTimeout <= 0 {
		App.ImageExternalTimeout = DefaultImageExternalTimeout
	}
	App.ImageOverlay = OverlayEnabled(merged.GetString)
	App.VideoPauseOnFocusLoss = merged.GetString("Video", "PauseOnFocusLoss", "0") == "1"
	App.ImageX11OffsetX, App.ImageX11OffsetY = 0, 0
	_, _ = fmt.Sscanf(merged.GetString("Images", "X11OverlayOffsetX", "0"), "%d", &App.ImageX11OffsetX)
	_, _ = fmt.Sscanf(merged.GetString("Images", "X11OverlayOffsetY", "0"), "%d", &App.ImageX11OffsetY)
	App.TTYXKeys = merged.GetString("TTYXi", "Keys", "1") == "1"
	App.TTYXKeyList = merged.GetString("TTYXi", "KeyList", DefaultTTYXKeyList)
	App.Compare = LoadCompareOptions(merged)
	App.ImageDecoderPriority = merged.GetString("Images", "DecoderPriority", "")
	SetImageDecoderPriorities(ParseImageDecoderPriorities(App.ImageDecoderPriority))
	App.UseExternalEditor = merged.GetString("Editor", "UseExternalEditor", "0") == "1"
	App.ExternalEditorCommand = merged.GetString("Editor", "ExternalEditorCommand", "")
	App.ExternalEditorConsole = merged.GetString("Editor", "ExternalEditorCommandConsole", App.ExternalEditorCommand)
	App.ExternalEditorGUI = merged.GetString("Editor", "ExternalEditorCommandGUI", App.ExternalEditorCommand)
	plugStr := merged.GetString("Plugins", "List", "")
	if plugStr != "" {
		App.RegisteredPlugins = strings.Split(plugStr, "|")
	}
	App.EditorTabSize = 4
	_, _ = fmt.Sscanf(merged.GetString("Editor", "TabSize", "4"), "%d", &App.EditorTabSize)

	// [Layout] — three known keys plus round-trip storage for anything else.
	_, _ = fmt.Sscanf(merged.GetString("Layout", "WidthDecrement", "0"), "%d", &App.WidthDecrement)
	_, _ = fmt.Sscanf(merged.GetString("Layout", "LeftHeightDecrement", "0"), "%d", &App.LeftHeightDecrement)
	_, _ = fmt.Sscanf(merged.GetString("Layout", "RightHeightDecrement", "0"), "%d", &App.RightHeightDecrement)
	App.LayoutExtras = nil
	if layout, ok := merged.Sections()["Layout"]; ok {
		for k, v := range layout {
			switch k {
			case "WidthDecrement", "LeftHeightDecrement", "RightHeightDecrement":
				continue
			}
			if App.LayoutExtras == nil {
				App.LayoutExtras = make(map[string]string)
			}
			App.LayoutExtras[k] = v
		}
	}
}

// parseForcedCodePage reads [System] ANSICodePage / OEMCodePage. Empty, zero,
// or "auto" all mean "keep what the locale said"; anything unparsable means
// the same, because a typo here must not leave f4 decoding with a codepage
// nobody chose.
func parseForcedCodePage(value string) int {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "auto") {
		return 0
	}
	id, err := strconv.Atoi(value)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// applyForcedCodePages hands the two settings to vfs, which owns what ANSI and
// OEM mean. On Linux neither is a system property -- they are guessed from
// LC_ALL/LC_CTYPE/LANG -- and the guess is wrong on every machine whose locale
// says nothing about the legacy encodings its user actually meets, so far2l
// lets ~/.config/far2l/cp override it and f4 lets settings.ini do the same
// (#368).
func applyForcedCodePages() {
	if err := vfs.SetSystemCodepages(App.SystemANSICodePage, App.SystemOEMCodePage); err != nil {
		vtui.DebugLog("CONFIG: forced ANSI/OEM codepage ignored: %v", err)
	}
}

func SaveConfig() {
	SaveWithWindowSize(true)
}

// SaveWithWindowSize writes the application settings. When windowSize
// is false, the last persisted GUI dimensions are retained instead of the
// dimensions currently held in memory; this keeps the Shift+F9 groups
// independent.
func SaveWithWindowSize(windowSize bool) {
	ApplyProxySettings()
	cfg := App
	if !windowSize {
		cfg.GuiCols, cfg.GuiRows = persistedGuiWindowSize()
	}
	if err := WriteUserFileAtomically(GetUserConfigIniPath(), SerializeSettingsConfig(cfg), 0600); err != nil {
		vtui.DebugLog("CONFIG: Failed to save application settings: %v", err)
	}
}

// SerializeSettingsConfig encodes a snapshot without publishing it or applying runtime effects.
func SerializeSettingsConfig(cfg F4Config) []byte {
	guiCols, guiRows := cfg.GuiCols, cfg.GuiRows
	var sb strings.Builder
	sb.WriteString("[Interface]\n")
	fmt.Fprintf(&sb, "ColorStyle = %s\n", cfg.ColorStyle)
	fmt.Fprintf(&sb, "Language = %s\n", cfg.Language)
	fmt.Fprintf(&sb, "FallbackLanguage = %s\n", cfg.FallbackLanguage)
	fmt.Fprintf(&sb, "HelpLanguage = %s\n", cfg.HelpLanguage)
	fmt.Fprintf(&sb, "UseLocalLanguageFiles = %d\n", map[bool]int{true: 1, false: 0}[cfg.UseLocalLanguageFiles])
	fmt.Fprintf(&sb, "ConsoleTitleTemplate = %s\n", cfg.ConsoleTitleTemplate)
	fmt.Fprintf(&sb, "DisplayFullPathInTitle = %d\n", map[bool]int{true: 1, false: 0}[cfg.DisplayFullPathInTitle])
	fmt.Fprintf(&sb, "AlwaysShowMenuBar = %d\n", map[bool]int{true: 1, false: 0}[cfg.AlwaysShowMenuBar])
	workspaceTabMode := "multiple"
	if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsAlways) {
		workspaceTabMode = "always"
	} else if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsOnCtrl) {
		workspaceTabMode = "ctrl"
	} else if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsNever) {
		workspaceTabMode = "never"
	}
	ctrlTabMode := "direct"
	if cfg.CtrlTabShowsMenu {
		ctrlTabMode = "menu"
	}
	fmt.Fprintf(&sb, "WorkspaceTabMode = %s\n", workspaceTabMode)
	fmt.Fprintf(&sb, "WorkspaceTabsOverlay = %d\n", map[bool]int{true: 1, false: 0}[cfg.WorkspaceTabsOverlay])
	fmt.Fprintf(&sb, "CtrlTabMode = %s\n", ctrlTabMode)
	fmt.Fprintf(&sb, "AltNumberSwitchesTabs = %d\n", map[bool]int{true: 1, false: 0}[cfg.AltNumberSwitchesTabs])
	fmt.Fprintf(&sb, "RestoreWorkspaceTabs = %d\n", map[bool]int{true: 1, false: 0}[cfg.RestoreWorkspaceTabs])
	fmt.Fprintf(&sb, "WorkspaceTabNumbering = %s\n", cfg.WorkspaceTabNumbering.String())
	fmt.Fprintf(&sb, "MacKeyboard = %s\n\n", ParseMacKeysMode(cfg.MacKeyboard))
	sb.WriteString("[Panel]\n")
	fmt.Fprintf(&sb, "ShowHiddenFiles = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowHiddenFiles])
	fmt.Fprintf(&sb, "ShowDirPrefix = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowDirPrefix])
	fmt.Fprintf(&sb, "ShowHighlightMarks = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowHighlightMarks])
	fmt.Fprintf(&sb, "SeparateFileExtensions = %d\n", map[bool]int{true: 1, false: 0}[cfg.SeparateFileExtensions])
	fmt.Fprintf(&sb, "PanelScrollbarMode = %s\n", cfg.PanelScrollbarMode.String())
	fmt.Fprintf(&sb, "ShowPanelFileInfo = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowPanelFileInfo])
	fmt.Fprintf(&sb, "SavePanelPaths = %d\n", map[bool]int{true: 1, false: 0}[cfg.SavePanelPaths])
	fmt.Fprintf(&sb, "DriveMenuOptions = %d\n", cfg.DriveMenuOptions)
	fmt.Fprintf(&sb, "InfoPanelBytes = %d\n", map[bool]int{true: 1, false: 0}[cfg.InfoPanelBytes])
	fmt.Fprintf(&sb, "InfoPanelCPUGPU = %d\n", map[bool]int{true: 1, false: 0}[cfg.InfoPanelCPUGPU])
	fmt.Fprintf(&sb, "EscTogglePanels = %d\n", map[bool]int{true: 1, false: 0}[cfg.EscTogglePanels])
	fmt.Fprintf(&sb, "TerminalCtrlNWorkspace = %d\n", map[bool]int{true: 1, false: 0}[cfg.TerminalCtrlNWorkspace])
	fmt.Fprintf(&sb, "KeepTerminalCursor = %d\n", map[bool]int{true: 1, false: 0}[cfg.KeepTerminalCursor])
	fmt.Fprintf(&sb, "ConsoleMode = %s\n", cfg.ConsoleMode)
	fmt.Fprintf(&sb, "ConsoleOverlayUI = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConsoleOverlayUI])
	fmt.Fprintf(&sb, "CommandLineAutoComplete = %d\n", map[bool]int{true: 1, false: 0}[cfg.CommandLineAutoComplete])
	fmt.Fprintf(&sb, "NavigationMode = %s\n", cfg.NavigationMode.String())
	fmt.Fprintf(&sb, "SearchCommandStayFocused = %d\n", map[bool]int{true: 1, false: 0}[cfg.SearchCommandStayFocused])
	// Keep the legacy key synchronized for older f4 versions and shared configs.
	fmt.Fprintf(&sb, "VimHotkeys = %d\n", map[bool]int{true: 1, false: 0}[cfg.NavigationMode == NavigationVim])
	fmt.Fprintf(&sb, "SyncPanelLoad = %d\n", map[bool]int{true: 1, false: 0}[cfg.SyncPanelLoad])
	fmt.Fprintf(&sb, "SearchExactOnHit = %d\n", map[bool]int{true: 1, false: 0}[cfg.SearchExactOnHit])
	fmt.Fprintf(&sb, "ApplyCommandParallelism = %d\n", cfg.ApplyCommandParallelism)
	fmt.Fprintf(&sb, "DefaultFileOpMode = %d\n", cfg.DefaultFileOpMode)
	fmt.Fprintf(&sb, "FileOpPathDisplay = %d\n", cfg.FileOpPathDisplay)
	fmt.Fprintf(&sb, "CopyAccessRights = %d\n", cfg.CopyAccessRights)

	sb.WriteString("\n[System]\n")
	fmt.Fprintf(&sb, "ConfirmCopy = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConfirmCopy])
	fmt.Fprintf(&sb, "ConfirmMove = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConfirmMove])
	fmt.Fprintf(&sb, "ConfirmDelete = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConfirmDelete])
	fmt.Fprintf(&sb, "UseTrash = %d\n", map[bool]int{true: 1, false: 0}[cfg.UseTrash])
	fmt.Fprintf(&sb, "ConfirmExit = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConfirmExit])
	fmt.Fprintf(&sb, "DeleteCancelFocused = %d\n", map[bool]int{true: 1, false: 0}[cfg.DeleteCancelFocused])
	fmt.Fprintf(&sb, "AutoSaveSettings = %d\n", map[bool]int{true: 1, false: 0}[cfg.AutoSaveSettings])
	fmt.Fprintf(&sb, "AutoSaveDialogSettings = %d\n", map[bool]int{true: 1, false: 0}[cfg.AutoSaveDialogSettings])
	fmt.Fprintf(&sb, "AutoSavePanelSettings = %d\n", map[bool]int{true: 1, false: 0}[cfg.AutoSavePanelSettings])
	fmt.Fprintf(&sb, "AutoSaveCurrentPanel = %d\n", map[bool]int{true: 1, false: 0}[cfg.AutoSaveCurrentPanel])
	fmt.Fprintf(&sb, "AutoSaveGUIWindow = %d\n", map[bool]int{true: 1, false: 0}[cfg.AutoSaveGUIWindow])
	fmt.Fprintf(&sb, "AnnounceKittyTerm = %d\n", map[bool]int{true: 1, false: 0}[cfg.AnnounceKittyTerm])
	fmt.Fprintf(&sb, "MacroRecordFormat = %d\n", cfg.MacroRecordFormat)
	fmt.Fprintf(&sb, "ANSICodePage = %d\n", cfg.SystemANSICodePage)
	fmt.Fprintf(&sb, "OEMCodePage = %d\n", cfg.SystemOEMCodePage)

	sb.WriteString("\n[Dialogs]\n")
	fmt.Fprintf(&sb, "EnforceColorCorrection = %d\n", map[bool]int{true: 1, false: 0}[cfg.EnforceColorCorrection])

	sb.WriteString("\n[Appearance]\n")
	fmt.Fprintf(&sb, "GuiFont = %s\n", cfg.GuiFont)
	fmt.Fprintf(&sb, "GuiUseSystemMonospace = %d\n", map[bool]int{true: 1, false: 0}[cfg.GuiUseSystemMonospace])
	fmt.Fprintf(&sb, "GuiFontSize = %d\n", cfg.GuiFontSize)
	fmt.Fprintf(&sb, "GuiCols = %d\n", guiCols)
	fmt.Fprintf(&sb, "GuiRows = %d\n", guiRows)
	if cfg.GuiPositionSaved {
		fmt.Fprintf(&sb, "GuiPosX = %d\n", cfg.GuiPosX)
		fmt.Fprintf(&sb, "GuiPosY = %d\n", cfg.GuiPosY)
	}
	fmt.Fprintf(&sb, "HighlightPriority = %d\n", cfg.HighlightPriority)

	sb.WriteString("\n[Startup]\n")
	fmt.Fprintf(&sb, "Mode = %s\n", cfg.StartupMode.String())
	fmt.Fprintf(&sb, "GuiBackend = %s\n", cfg.GuiBackend)
	fmt.Fprintf(&sb, "TTYBackend = %s\n", cfg.TTYBackend)

	sb.WriteString("\n[Update]\n")
	fmt.Fprintf(&sb, "Channel = %d\n", cfg.UpdateChannel)
	fmt.Fprintf(&sb, "Interval = %d\n", cfg.UpdateInterval)
	fmt.Fprintf(&sb, "LastCheck = %d\n", cfg.LastUpdateCheck)
	fmt.Fprintf(&sb, "LastVersion = %s\n", cfg.LastUpdateVersion)

	sb.WriteString("\n[Proxy]\n")
	fmt.Fprintf(&sb, "Mode = %d\n", cfg.ProxyMode)
	fmt.Fprintf(&sb, "Host = %s\n", cfg.ProxyHost)
	fmt.Fprintf(&sb, "Port = %s\n", cfg.ProxyPort)
	fmt.Fprintf(&sb, "User = %s\n", cfg.ProxyUser)
	fmt.Fprintf(&sb, "Password = %s\n", netproxy.EncodeSecret(cfg.ProxyPass))
	sb.WriteString("\n[Editor]\n")
	fmt.Fprintf(&sb, "AutoComplete = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorAutoComplete])
	fmt.Fprintf(&sb, "AutoCompleteMask = %s\n", cfg.EditorAutoCompleteMask)

	fmt.Fprintf(&sb, "ExpandTabs = %d\n", cfg.EditorExpandTabs)
	fmt.Fprintf(&sb, "AutoIndent = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorAutoIndent])
	fmt.Fprintf(&sb, "CursorBeyondEOL = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorCursorBeyondEOL])
	fmt.Fprintf(&sb, "UseEditorConfig = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorUseEditorConfig])
	fmt.Fprintf(&sb, "Crosshair = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorCrosshair])
	fmt.Fprintf(&sb, "MarkOccurrences = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorMarkOccurrences])
	fmt.Fprintf(&sb, "TabSize = %d\n", cfg.EditorTabSize)
	fmt.Fprintf(&sb, "UseExternalEditor = %d\n", map[bool]int{true: 1, false: 0}[cfg.UseExternalEditor])
	legacyExternalEditorCommand := cfg.ExternalEditorConsole
	if legacyExternalEditorCommand == "" {
		legacyExternalEditorCommand = cfg.ExternalEditorCommand
	}
	fmt.Fprintf(&sb, "ExternalEditorCommand = %s\n", legacyExternalEditorCommand)
	fmt.Fprintf(&sb, "ExternalEditorCommandConsole = %s\n", cfg.ExternalEditorConsole)
	fmt.Fprintf(&sb, "ExternalEditorCommandGUI = %s\n", cfg.ExternalEditorGUI)
	fmt.Fprintf(&sb, "AutodetectCodePage = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorAutodetectCodePage])
	fmt.Fprintf(&sb, "MemoryMap = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorMemoryMap])
	fmt.Fprintf(&sb, "Highlighter = %s\n", cfg.EditorHighlighter)
	fmt.Fprintf(&sb, "SyntaxAnimation = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorSyntaxAnimation])
	fmt.Fprintf(&sb, "ColorerScheme = %s\n", cfg.EditorColorerScheme)
	fmt.Fprintf(&sb, "ColorerBackground = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorColorerBackground])
	fmt.Fprintf(&sb, "ColorerSyntax = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorColorerSyntax])
	fmt.Fprintf(&sb, "ColorerCatalog = %s\n", cfg.EditorColorerCatalog)
	fmt.Fprintf(&sb, "CrossMode = %d\n", cfg.EditorCrossMode)
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", cfg.EditorDefaultCodePage)

	sb.WriteString("\n[Viewer]\n")
	fmt.Fprintf(&sb, "AutodetectCodePage = %d\n", map[bool]int{true: 1, false: 0}[cfg.ViewerAutodetectCodePage])
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", cfg.ViewerDefaultCodePage)
	sb.WriteString("\n[Mouse]\n")
	fmt.Fprintf(&sb, "PanelUp = %d\n", cfg.WheelPanelUp)
	fmt.Fprintf(&sb, "PanelDown = %d\n", cfg.WheelPanelDown)
	fmt.Fprintf(&sb, "EditorUp = %d\n", cfg.WheelEditorUp)
	fmt.Fprintf(&sb, "EditorDown = %d\n", cfg.WheelEditorDown)
	fmt.Fprintf(&sb, "ViewerUp = %d\n", cfg.WheelViewerUp)
	fmt.Fprintf(&sb, "ViewerDown = %d\n", cfg.WheelViewerDown)
	fmt.Fprintf(&sb, "MenuUp = %d\n", cfg.WheelMenuUp)
	fmt.Fprintf(&sb, "MenuDown = %d\n", cfg.WheelMenuDown)
	fmt.Fprintf(&sb, "TableUp = %d\n", cfg.WheelTableUp)
	fmt.Fprintf(&sb, "TableDown = %d\n", cfg.WheelTableDown)
	sb.WriteString("\n[PathHints]\n")
	fmt.Fprintf(&sb, "Timeout = %d\n", cfg.PathHintTimeout)
	fmt.Fprintf(&sb, "FullPath = %d\n", map[bool]int{true: 1, false: 0}[cfg.PathHintFullPath])
	fmt.Fprintf(&sb, "Source = %d\n", cfg.PathHintSource)
	fmt.Fprintf(&sb, "MaxVisible = %d\n", cfg.PathHintMaxVisible)
	fmt.Fprintf(&sb, "PerCategory = %d\n", map[bool]int{true: 1, false: 0}[cfg.PathHintPerCategory])
	fmt.Fprintf(&sb, "DialogAutoComplete = %d\n", map[bool]int{true: 1, false: 0}[cfg.DialogAutoComplete])

	sb.WriteString("\n[History]\n")
	fmt.Fprintf(&sb, "ShowTimes = %d,%d,%d\n", cfg.HistoryShowTimes[0], cfg.HistoryShowTimes[1], cfg.HistoryShowTimes[2])
	fmt.Fprintf(&sb, "DirsPrefixLen = %d\n", cfg.HistoryDirsPrefixLen)
	sb.WriteString("\n[Images]\n")
	fmt.Fprintf(&sb, "SlideShowDelay = %d\n", cfg.SlideShowDelay)
	fmt.Fprintf(&sb, "ExternalTimeout = %d\n", cfg.ImageExternalTimeout)
	fmt.Fprintf(&sb, "DecoderPriority = %s\n", cfg.ImageDecoderPriority)
	sb.WriteString("\n[Compare]\n")
	writeCompareOptions(&sb, cfg.Compare)
	sb.WriteString("\n[Plugins]\n")
	fmt.Fprintf(&sb, "List = %s\n", strings.Join(cfg.RegisteredPlugins, "|"))

	// [Layout]: emit our three keys plus any unrecognised keys we loaded
	// (round-trip). Keys are written alphabetically to match far2l's
	// on-disk order, so a diff against far2l's config.ini stays minimal.
	layoutKeys := map[string]string{
		"WidthDecrement":       fmt.Sprintf("%d", cfg.WidthDecrement),
		"LeftHeightDecrement":  fmt.Sprintf("%d", cfg.LeftHeightDecrement),
		"RightHeightDecrement": fmt.Sprintf("%d", cfg.RightHeightDecrement),
	}
	for k, v := range cfg.LayoutExtras {
		if _, taken := layoutKeys[k]; taken {
			continue
		}
		layoutKeys[k] = v
	}
	names := make([]string, 0, len(layoutKeys))
	for k := range layoutKeys {
		names = append(names, k)
	}
	sort.Strings(names)
	sb.WriteString("\n[Layout]\n")
	for _, k := range names {
		fmt.Fprintf(&sb, "%s=%s\n", k, layoutKeys[k])
	}

	return []byte(sb.String())
}

func persistedGuiWindowSize() (int, int) {
	cols, rows := App.GuiCols, App.GuiRows
	merged := ini.New()
	for _, path := range GetConfigIniPaths() {
		if _, err := os.Stat(path); err == nil {
			merged.Merge(ini.Load(path))
		}
	}
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiCols", fmt.Sprintf("%d", cols)), "%d", &cols)
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiRows", fmt.Sprintf("%d", rows)), "%d", &rows)
	if cols <= 0 {
		cols = App.GuiCols
	}
	if rows <= 0 {
		rows = App.GuiRows
	}
	return cols, rows
}

// SaveGuiWindowSize updates only the persisted GUI dimensions. It preserves
// unrelated settings and unknown keys in an existing settings.ini.
func SaveGuiWindowSize() {
	path := GetUserConfigIniPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		vtui.DebugLog("CONFIG: Failed to create settings directory: %v", err)
		return
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		for _, source := range GetConfigIniPaths() {
			if source == path {
				continue
			}
			if inherited, readErr := os.ReadFile(source); readErr == nil {
				updated := UpdateIniValues(inherited, "Appearance", GuiWindowValues())
				if writeErr := os.WriteFile(path, updated, 0600); writeErr != nil {
					vtui.DebugLog("CONFIG: Failed to save GUI size: %v", writeErr)
				} else if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
					vtui.DebugLog("CONFIG: Failed to restrict GUI settings permissions: %v", chmodErr)
				}
				return
			}
		}
		data = []byte(fmt.Sprintf("[Appearance]\nGuiCols = %d\nGuiRows = %d\n", App.GuiCols, App.GuiRows))
		if App.GuiPositionSaved {
			data = append(data, []byte(fmt.Sprintf("GuiPosX = %d\nGuiPosY = %d\n", App.GuiPosX, App.GuiPosY))...)
		}
		if writeErr := os.WriteFile(path, data, 0600); writeErr != nil {
			vtui.DebugLog("CONFIG: Failed to save GUI size: %v", writeErr)
		} else if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
			vtui.DebugLog("CONFIG: Failed to restrict GUI settings permissions: %v", chmodErr)
		}
		return
	}
	if err != nil {
		vtui.DebugLog("CONFIG: Failed to read settings before saving GUI size: %v", err)
		return
	}
	updated := UpdateIniValues(data, "Appearance", GuiWindowValues())
	if err := os.WriteFile(path, updated, 0600); err != nil {
		vtui.DebugLog("CONFIG: Failed to save GUI size: %v", err)
	} else if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		vtui.DebugLog("CONFIG: Failed to restrict GUI settings permissions: %v", chmodErr)
	}
}

func GuiWindowValues() map[string]string {
	values := map[string]string{
		"GuiCols": strconv.Itoa(App.GuiCols),
		"GuiRows": strconv.Itoa(App.GuiRows),
	}
	if App.GuiPositionSaved {
		values["GuiPosX"] = strconv.Itoa(App.GuiPosX)
		values["GuiPosY"] = strconv.Itoa(App.GuiPosY)
	}
	return values
}

func SyncAutoSaveMaster() {
	App.AutoSaveSettings = App.AutoSaveDialogSettings ||
		App.AutoSavePanelSettings || App.AutoSaveCurrentPanel || App.AutoSaveGUIWindow
}

func UpdateIniValues(data []byte, section string, values map[string]string) []byte {
	lines := strings.SplitAfter(string(data), "\n")
	var out strings.Builder
	currentSection := ""
	seenSection := false
	seen := make(map[string]bool, len(values))
	appendMissing := func() {
		if currentSection != section {
			return
		}
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		for key, value := range values {
			if !seen[key] {
				fmt.Fprintf(&out, "%s = %s\n", key, value)
			}
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			appendMissing()
			currentSection = trimmed[1 : len(trimmed)-1]
			if currentSection == section {
				seenSection = true
			}
		}
		if currentSection == section {
			if idx := strings.Index(trimmed, "="); idx >= 0 {
				key := strings.TrimSpace(trimmed[:idx])
				if value, ok := values[key]; ok {
					lineEnding := "\n"
					if strings.HasSuffix(line, "\r\n") {
						lineEnding = "\r\n"
					} else if !strings.HasSuffix(line, "\n") {
						lineEnding = ""
					}
					fmt.Fprintf(&out, "%s = %s%s", key, value, lineEnding)
					seen[key] = true
					continue
				}
			}
		}
		out.WriteString(line)
	}
	appendMissing()
	if !seenSection {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
		out.WriteString("\n[" + section + "]\n")
		for key, value := range values {
			fmt.Fprintf(&out, "%s = %s\n", key, value)
		}
	}
	return []byte(out.String())
}

// RequestSaveConfig schedules a debounced SaveConfig call. Multiple calls
// within the debounce window collapse into a single write. Used by the
// panel-resize hotkeys, where holding Ctrl+Arrow can fire many times per
// second and we don't want to fsync on every keystroke. The final value
// still lands on disk when automatic saving is enabled at shutdown.
func RequestSaveConfig() {
	if !App.AutoSaveSettings || !App.AutoSaveDialogSettings {
		return
	}
	SaveConfigTimerMu.Lock()
	defer SaveConfigTimerMu.Unlock()
	if SaveConfigTimer != nil {
		SaveConfigTimer.Stop()
	}
	// Taken here, on the goroutine that arms the timer, rather than inside it.
	// The timer outlives whatever scheduled it -- half a second is a long time
	// in a test suite -- and reading the global from the callback races
	// anything that reassigns vtui.FrameManager in the meantime, which in the
	// tests is the next test to call swapFrameManager.
	frames := vtui.FrameManager
	SaveConfigTimer = time.AfterFunc(saveConfigDebounce, func() {
		// AfterFunc runs this on a goroutine of its own, and everything below
		// reads App -- the flags here, then SaveConfig and the proxy
		// settings it applies. App belongs to the UI goroutine, which
		// goes on editing it while the timer counts down, so reading it from
		// here is a race against whoever is changing settings. Hand the work
		// back to the UI instead; the delay is the debounce, not the thread.
		if frames == nil {
			return
		}
		frames.PostTask(func() {
			if App.AutoSaveSettings && App.AutoSaveDialogSettings {
				SaveConfig()
			}
		})
	})
}

var (
	SaveConfigTimerMu sync.Mutex
	SaveConfigTimer   *time.Timer
)

const saveConfigDebounce = 500 * time.Millisecond

// LoadWheelLines reads a [Mouse] wheel-speed key: lines per notch,
// 0 = system default. Negative values are treated as 0.
func LoadWheelLines(ini *ini.File, key string) int {
	n := 0
	_, _ = fmt.Sscanf(ini.GetString("Mouse", key, "0"), "%d", &n)
	if n < 0 {
		n = 0
	}
	return n
}

// WheelScrollLines resolves a configured wheel speed (0 = follow the system
// setting) into the number of lines to scroll per wheel notch.
func WheelScrollLines(cfg int) int {
	if cfg <= 0 {
		return vtui.WheelLinesPerNotch()
	}
	return cfg
}

// ApplyWheelSettings pushes the menu/table wheel speed overrides into vtui.
// Panels, editor and viewer are handled by f4 itself via WheelScrollLines.
func ApplyWheelSettings() {
	vtui.SetWheelAreaLines(vtui.WheelAreaMenu, App.WheelMenuUp, App.WheelMenuDown)
	vtui.SetWheelAreaLines(vtui.WheelAreaList, App.WheelTableUp, App.WheelTableDown)
}

func CreateDefaultHighlightIni(path string) {
	content := `# User highlight rules and sort groups.
#
# f4 applies file highlighting rules from both the active Color Style (Theme)
# and this file. By default, rules in this file have higher priority.
# The two sources are not merged field by field: f4 puts one complete rule
# list before the other. Change Appearance.HighlightPriority in settings.ini
# to 0 (user rules first, the default) or 1 (theme rules first).
# A matching rule normally stops processing even when it has no colour for
# the current state. Add ContinueProcessing = 1 when a later rule should be
# allowed to supply or merge the remaining colour components.
#
# You can add your custom highlight groups here (e.g. Mask = *.mp3).
# Default groups (Hidden, Executables, Directories) are already defined
# by the active Color Style, so you don't need to duplicate them unless
# you specifically want to override the theme's colors.
#
# A [Highlight_N] section matches an item by its Mask, attributes, size or
# date. Name is a label for you, not a matcher. The four color keys are
# selected independently:
#   NormalColor          - an ordinary, unselected item
#   SelectedColor        - a selected item
#   CursorColor          - an ordinary item under the cursor
#   SelectedCursorColor  - a selected item under the cursor
# The same four can be spelled the way Far Manager names them in its Files
# highlighting dialog, which is what a group copied from Far will use:
#   NormalFileName, SelectedFileName, FileNameUnderCursor,
#   FileNameSelectedUnderCursor
# The cursor-specific keys are also accepted as NormalColorUnderCursor and
# SelectedColorUnderCursor. If a specialized color is omitted, f4 falls back
# to the corresponding ordinary color. Every one of the four takes a
# foreground, a background, or both:
#   foreground:#FF00FF | background:#008080
# Other useful keys are IncludeAttributes/ExcludeAttributes (Directory,
# Hidden, Executable, ReadOnly, System, Archive, Symlink), SizeAbove,
# SizeBelow, DateType, DateAfter, DateBefore, Mark, and ContinueProcessing.
#
# Sections are tried in the order of their numbers and the first match wins,
# unless it sets ContinueProcessing = 1. A section without a Mask matches
# every name, so a rule meant for folders needs IncludeAttributes = Directory
# and a rule meant for files needs ExcludeAttributes = Directory -- a rule
# with neither repaints the whole panel and hides every rule below it.
#
# A comment takes a whole line. There are no trailing comments: '#' also
# opens a color literal, so anything after a value stays part of that value.
#
# Uncomment and adapt these complete examples to add custom rules. The
# sections are commented out deliberately, so they do not change the panel.
# [Highlight_100]
# Name = Archives
# Mask = *.zip, *.rar, *.7z
# ExcludeAttributes = Directory
# NormalColor = foreground:#FF00FF | background:#000000
# SelectedColor = foreground:#FFFF00 | background:#000000
# CursorColor = foreground:#FFFFFF | background:#008080
# SelectedCursorColor = foreground:#FFFF00 | background:#008080
#
# The same four colors for folders, written with the Far key names. Note the
# attribute: without it the section would color the files as well.
# [Highlight_101]
# Name = Directories
# IncludeAttributes = Directory
# NormalFileName = foreground:#FFFFFF | background:#000000
# SelectedFileName = foreground:#FFFF00 | background:#000000
# FileNameUnderCursor = foreground:#FFFFFF | background:#008080
# FileNameSelectedUnderCursor = foreground:#FFFF00 | background:#008080
#
# To use one coloured rule for sorting too, add Group to that Highlight
# section. The same mask and attributes then control both its colour and its
# position; sections with the same Group number form one cluster. Legacy
# [SortGroup_N] sections are still accepted for old profiles.

[SortGroup_1]
Name = Executables
Group = 0
IncludeAttributes = executable
ExcludeAttributes = directory

[SortGroup_2]
Name = Executables (by name)
Group = 0
Mask = *.exe, *.com, *.bat, *.cmd, *.ps1, *.sh

[SortGroup_3]
Name = Archives
Group = 1
Mask = *.zip, *.7z, *.rar, *.tar, *.tgz, *.gz, *.bz2, *.xz, *.zst

[SortGroup_4]
Name = Images
Group = 2
Mask = *.png, *.jpg, *.jpeg, *.gif, *.bmp, *.webp, *.svg, *.ico, *.tif, *.tiff

[SortGroup_5]
Name = Media
Group = 3
Mask = *.mp3, *.flac, *.ogg, *.wav, *.mp4, *.mkv, *.avi, *.webm, *.mov
`
	_ = os.WriteFile(path, []byte(content), 0600)
	_ = os.Chmod(path, 0600)
}

// ApplyProxySettings publishes the configured proxy to netproxy, which is
// where the updater, the plugin ring, the colorer downloader and netfox all
// read it from.
func ApplyProxySettings() {
	netproxy.SetGlobal(netproxy.Settings{
		Mode: App.ProxyMode,
		Host: App.ProxyHost,
		Port: App.ProxyPort,
		User: App.ProxyUser,
		Pass: App.ProxyPass,
	})
}
