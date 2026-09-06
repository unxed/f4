package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

var (
	cachedF4ConfigDir string
	cachedF4Portable  bool
	configDirOnce     sync.Once
)

// userConfigDir is os.UserConfigDir behind a seam. os.UserConfigDir honors
// XDG_CONFIG_HOME only on Unix, not on darwin, so tests cannot redirect the
// config dir through the environment there; they override this variable
// instead of silently reading and polluting the developer's real profile.
var userConfigDir = os.UserConfigDir

func GetF4ConfigDir() string {
	configDirOnce.Do(func() {
		exe, err := osExecutable()
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
		ini := LoadIni(portableIniPath(exe))
		cachedF4ConfigDir, cachedF4Portable = resolveProfileDir(exeDir, ini)
		if cachedF4Portable {
			_ = os.MkdirAll(cachedF4ConfigDir, 0700)
		}
	})
	return cachedF4ConfigDir
}

// resolveProfileDir picks the configuration directory from the executable
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
func resolveProfileDir(exeDir string, ini *IniFile) (dir string, portable bool) {
	if ini == nil || ini.GetString("General", "UseSystemProfiles", "1") != "0" {
		sysDir, _ := userConfigDir()
		return filepath.Join(sysDir, "f4"), false
	}
	return portableProfileDirFor(exeDir, ini), true
}

// portableProfileDirFor is the directory a portable profile lands in for the
// given executable directory and ini, regardless of UseSystemProfiles.
func portableProfileDirFor(exeDir string, ini *IniFile) string {
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
	return cachedF4Portable
}

func parseHistoryShowTimes(value string) [historyTypeCount]int {
	result := [historyTypeCount]int{historyShowDateTime, historyShowDateTime, historyShowDateTime}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	for i := 0; i < len(parts) && i < len(result); i++ {
		mode, err := strconv.Atoi(parts[i])
		if err == nil && mode >= historyShowDateTime && mode <= historyShowNone {
			result[i] = mode
		}
	}
	return result
}

func resetConfigDirForTest() {
	configDirOnce = sync.Once{}
	cachedF4ConfigDir = ""
	cachedF4Portable = false
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
	AlwaysShowMenuBar        bool
	WorkspaceTabMode         int
	WorkspaceTabsOverlay     bool
	CtrlTabShowsMenu         bool
	AltNumberSwitchesTabs    bool
	RestoreWorkspaceTabs     bool
	WorkspaceTabNumbering    WorkspaceTabNumberingMode
	ShowHiddenFiles          bool
	ShowDirPrefix            bool
	ShowHighlightMarks       bool
	SeparateFileExtensions   bool
	PanelScrollbarMode       PanelScrollbarMode
	ShowPanelFileInfo        bool
	SavePanelPaths           bool
	InfoPanelBytes           bool // Ctrl+L info panel: true = raw bytes, false = human (GiB/MiB…)
	InfoPanelCPUGPU          bool // Ctrl+L info panel: show CPU and GPU sections (off by default)
	EscTogglePanels          bool // ESC toggles panels visibility (Far ships this as a macro; on by default)
	TerminalCtrlNWorkspace   bool // reserve Ctrl+N in terminal views for cloning panels to a workspace
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
	UseExternalEditor        bool
	ExternalEditorCommand    string
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
	HistoryShowTimes       [historyTypeCount]int
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
}

var AppConfig = F4Config{
	ColorStyle:               "Modern",
	Language:                 "en",
	FallbackLanguage:         "",
	HelpLanguage:             "en",
	AlwaysShowMenuBar:        false,
	WorkspaceTabMode:         int(vtui.WorkspaceTabsAlways),
	WorkspaceTabsOverlay:     true,
	CtrlTabShowsMenu:         false,
	AltNumberSwitchesTabs:    true,
	RestoreWorkspaceTabs:     true,
	WorkspaceTabNumbering:    WorkspaceTabNumbersAlways,
	ShowHiddenFiles:          true,
	ShowDirPrefix:            false,
	ShowHighlightMarks:       false,
	SeparateFileExtensions:   false,
	PanelScrollbarMode:       PanelScrollbarMinimal,
	ShowPanelFileInfo:        false,
	SavePanelPaths:           true,
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
	UseExternalEditor:        false,
	ExternalEditorCommand:    "",
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
	HistoryShowTimes:         [historyTypeCount]int{historyShowDateTime, historyShowDateTime, historyShowDateTime},
	HistoryDirsPrefixLen:     24,
	SlideShowDelay:           defaultSlideShowDelay,
	ImageOverlay:             true,
	TTYXKeys:                 true,
	TTYXKeyList:              defaultTTYXKeyList,
	ImageExternalTimeout:     defaultImageExternalTimeout,
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
	GuiFont:                  "",
	GuiUseSystemMonospace:    true,
	GuiFontSize:              defaultGuiFontSize(runtime.GOOS),
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
}

var getUserConfigIniPath = func() string {
	return filepath.Join(GetF4ConfigDir(), "settings.ini")
}

var getConfigIniPaths = func() []string {
	userPath := getUserConfigIniPath()
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
	paths := getConfigIniPaths()
	ini := &IniFile{data: make(map[string]map[string]string)}

	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			vtui.DebugLog("CONFIG: Loading and merging config from %s", path)
			partialIni := LoadIni(path)
			ini.Merge(partialIni)
		}
	}

	AppConfig.ShowHiddenFiles = ini.GetString("Panel", "ShowHiddenFiles", "1") == "1"
	AppConfig.ColorStyle = ini.GetString("Interface", "ColorStyle", "Radiola")
	// "Far2l Dark" was an approximate port of the far2l theme "default dark".
	// It has been replaced by an exact one; carry existing configs over.
	if strings.EqualFold(AppConfig.ColorStyle, "Far2l Dark") {
		AppConfig.ColorStyle = "Default Dark"
	}
	AppConfig.Language = ini.GetString("Interface", "Language", "en")
	AppConfig.FallbackLanguage = ini.GetString("Interface", "FallbackLanguage", "")
	AppConfig.HelpLanguage = ini.GetString("Interface", "HelpLanguage", "en")
	AppConfig.ConsoleTitleTemplate = ini.GetString("Interface", "ConsoleTitleTemplate", "f4 %Ver %Platform %Admin - %State")
	AppConfig.DisplayFullPathInTitle = ini.GetString("Interface", "DisplayFullPathInTitle", "0") == "1"
	AppConfig.AlwaysShowMenuBar = ini.GetString("Interface", "AlwaysShowMenuBar", "0") == "1"
	switch strings.ToLower(ini.GetString("Interface", "WorkspaceTabMode", "always")) {
	case "always":
		AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsAlways)
	case "ctrl":
		AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsOnCtrl)
	case "never":
		AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsNever)
	default:
		AppConfig.WorkspaceTabMode = int(vtui.WorkspaceTabsMultiple)
	}
	AppConfig.WorkspaceTabsOverlay = ini.GetString("Interface", "WorkspaceTabsOverlay", "1") != "0"
	AppConfig.CtrlTabShowsMenu = strings.EqualFold(ini.GetString("Interface", "CtrlTabMode", "direct"), "menu")
	AppConfig.AltNumberSwitchesTabs = ini.GetString("Interface", "AltNumberSwitchesTabs", "1") != "0"
	AppConfig.RestoreWorkspaceTabs = ini.GetString("Interface", "RestoreWorkspaceTabs", "1") != "0"
	AppConfig.WorkspaceTabNumbering = ParseWorkspaceTabNumberingMode(ini.GetString("Interface", "WorkspaceTabNumbering", "always"))
	if AppConfig.ConsoleTitleTemplate == "f4 - %State" {
		AppConfig.ConsoleTitleTemplate = "f4 %Ver %Platform %Admin - %State"
	}
	AppConfig.ShowDirPrefix = ini.GetString("Panel", "ShowDirPrefix", "0") == "1"
	AppConfig.ShowHighlightMarks = ini.GetString("Panel", "ShowHighlightMarks", "0") == "1"
	AppConfig.SeparateFileExtensions = ini.GetString("Panel", "SeparateFileExtensions", "0") == "1"
	if mode := ini.GetString("Panel", "PanelScrollbarMode", ""); mode != "" {
		AppConfig.PanelScrollbarMode = ParsePanelScrollbarMode(mode)
	} else {
		// Migration from the short-lived boolean setting. When neither setting
		// exists, use the new default: the minimal scrollbar.
		switch ini.GetString("Panel", "ShowPanelScrollbars", "") {
		case "1":
			AppConfig.PanelScrollbarMode = PanelScrollbarFull
		case "0":
			AppConfig.PanelScrollbarMode = PanelScrollbarOff
		default:
			AppConfig.PanelScrollbarMode = PanelScrollbarMinimal
		}
	}
	AppConfig.ShowPanelFileInfo = ini.GetString("Panel", "ShowPanelFileInfo", "0") == "1"
	AppConfig.SavePanelPaths = ini.GetString("Panel", "SavePanelPaths", "1") == "1"
	AppConfig.InfoPanelBytes = ini.GetString("Panel", "InfoPanelBytes", "0") == "1"
	AppConfig.InfoPanelCPUGPU = ini.GetString("Panel", "InfoPanelCPUGPU", "0") == "1"
	AppConfig.EscTogglePanels = ini.GetString("Panel", "EscTogglePanels", "1") == "1"
	AppConfig.TerminalCtrlNWorkspace = ini.GetString("Panel", "TerminalCtrlNWorkspace", "1") == "1"
	AppConfig.KeepTerminalCursor = ini.GetString("Panel", "KeepTerminalCursor", "0") == "1"
	AppConfig.ConsoleMode = ini.GetString("Panel", "ConsoleMode", "own")
	AppConfig.ConsoleOverlayUI = ini.GetString("Panel", "ConsoleOverlayUI", "0") == "1"
	AppConfig.CommandLineAutoComplete = ini.GetString("Panel", "CommandLineAutoComplete", "1") == "1"
	if mode := ini.GetString("Panel", "NavigationMode", ""); mode != "" {
		AppConfig.NavigationMode = ParsePanelNavigationMode(mode)
	} else if ini.GetString("Panel", "VimHotkeys", "0") == "1" {
		// Migration from settings written before NavigationMode was introduced.
		AppConfig.NavigationMode = NavigationVim
	} else {
		AppConfig.NavigationMode = NavigationClassic
	}
	AppConfig.SearchCommandStayFocused = ini.GetString("Panel", "SearchCommandStayFocused", "0") == "1"
	AppConfig.SyncPanelLoad = ini.GetString("Panel", "SyncPanelLoad", "0") == "1"
	AppConfig.SearchExactOnHit = ini.GetString("Panel", "SearchExactOnHit", "0") == "1"
	AppConfig.ApplyCommandParallelism = runtime.NumCPU()
	fmt.Sscanf(ini.GetString("Panel", "ApplyCommandParallelism", fmt.Sprintf("%d", runtime.NumCPU())), "%d", &AppConfig.ApplyCommandParallelism)
	if AppConfig.ApplyCommandParallelism < 0 {
		AppConfig.ApplyCommandParallelism = runtime.NumCPU()
	}
	fmt.Sscanf(ini.GetString("Panel", "DefaultFileOpMode", "0"), "%d", &AppConfig.DefaultFileOpMode)
	AppConfig.ConfirmCopy = ini.GetString("System", "ConfirmCopy", "1") == "1"
	AppConfig.ConfirmMove = ini.GetString("System", "ConfirmMove", "1") == "1"
	AppConfig.ConfirmDelete = ini.GetString("System", "ConfirmDelete", "1") == "1"
	AppConfig.UseTrash = ini.GetString("System", "UseTrash", "0") == "1"
	AppConfig.ConfirmExit = ini.GetString("System", "ConfirmExit", "1") == "1"
	AppConfig.DeleteCancelFocused = ini.GetString("System", "DeleteCancelFocused", "0") == "1"
	legacyAutoSave := ini.GetString("System", "AutoSaveSettings", "1") != "0"
	AppConfig.AutoSaveSettings = legacyAutoSave
	autoSaveDefault := "0"
	if legacyAutoSave {
		autoSaveDefault = "1"
	}
	AppConfig.AutoSaveDialogSettings = ini.GetString("System", "AutoSaveDialogSettings", autoSaveDefault) != "0"
	AppConfig.AutoSavePanelSettings = ini.GetString("System", "AutoSavePanelSettings", autoSaveDefault) != "0"
	AppConfig.AutoSaveCurrentPanel = ini.GetString("System", "AutoSaveCurrentPanel", autoSaveDefault) != "0"
	AppConfig.AutoSaveGUIWindow = ini.GetString("System", "AutoSaveGUIWindow", autoSaveDefault) != "0"
	AppConfig.AnnounceKittyTerm = ini.GetString("System", "AnnounceKittyTerm", "1") == "1"
	fmt.Sscanf(ini.GetString("System", "MacroRecordFormat", "0"), "%d", &AppConfig.MacroRecordFormat)
	AppConfig.SystemANSICodePage = parseForcedCodePage(ini.GetString("System", "ANSICodePage", ""))
	AppConfig.SystemOEMCodePage = parseForcedCodePage(ini.GetString("System", "OEMCodePage", ""))
	applyForcedCodePages()
	fmt.Sscanf(ini.GetString("Panel", "FileOpPathDisplay", "0"), "%d", &AppConfig.FileOpPathDisplay)
	AppConfig.GuiFont = ini.GetString("Appearance", "GuiFont", "")
	AppConfig.GuiUseSystemMonospace = ini.GetString("Appearance", "GuiUseSystemMonospace", "1") == "1"
	defaultFontSize := defaultGuiFontSize(runtime.GOOS)
	fmt.Sscanf(ini.GetString("Appearance", "GuiFontSize", fmt.Sprintf("%d", defaultFontSize)), "%d", &AppConfig.GuiFontSize)
	if AppConfig.GuiFontSize <= 0 {
		AppConfig.GuiFontSize = defaultFontSize
	}
	fmt.Sscanf(ini.GetString("Appearance", "GuiCols", "100"), "%d", &AppConfig.GuiCols)
	if AppConfig.GuiCols <= 0 {
		AppConfig.GuiCols = 100
	}
	fmt.Sscanf(ini.GetString("Appearance", "GuiRows", "30"), "%d", &AppConfig.GuiRows)
	if AppConfig.GuiRows <= 0 {
		AppConfig.GuiRows = 30
	}
	guiPosX, xErr := strconv.Atoi(ini.GetString("Appearance", "GuiPosX", ""))
	guiPosY, yErr := strconv.Atoi(ini.GetString("Appearance", "GuiPosY", ""))
	AppConfig.GuiPositionSaved = xErr == nil && yErr == nil
	if AppConfig.GuiPositionSaved {
		AppConfig.GuiPosX = guiPosX
		AppConfig.GuiPosY = guiPosY
	} else {
		AppConfig.GuiPosX = 0
		AppConfig.GuiPosY = 0
	}
	AppConfig.StartupMode = ParseStartupMode(ini.GetString("Startup", "Mode", "auto"))
	AppConfig.GuiBackend = normalizeStartupGuiBackend(ini.GetString("Startup", "GuiBackend", ""))
	AppConfig.TTYBackend = normalizeStartupTTYBackend(ini.GetString("Startup", "TTYBackend", ""))
	AppConfig.EnforceColorCorrection = ini.GetString("Dialogs", "EnforceColorCorrection", "1") == "1"
	fmt.Sscanf(ini.GetString("Appearance", "HighlightPriority", "0"), "%d", &AppConfig.HighlightPriority)
	fmt.Sscanf(ini.GetString("Update", "Channel", "0"), "%d", &AppConfig.UpdateChannel)
	fmt.Sscanf(ini.GetString("Update", "Interval", "3"), "%d", &AppConfig.UpdateInterval)
	fmt.Sscanf(ini.GetString("Update", "LastCheck", "0"), "%d", &AppConfig.LastUpdateCheck)
	AppConfig.LastUpdateVersion = ini.GetString("Update", "LastVersion", "")

	// The proxy password is stored obfuscated, exactly like netfox stores
	// site passwords; a hand-written plain one keeps working.
	fmt.Sscanf(ini.GetString("Proxy", "Mode", "1"), "%d", &AppConfig.ProxyMode)
	AppConfig.ProxyHost = ini.GetString("Proxy", "Host", "")
	AppConfig.ProxyPort = ini.GetString("Proxy", "Port", "")
	AppConfig.ProxyUser = ini.GetString("Proxy", "User", "")
	AppConfig.ProxyPass = netproxy.DecodeSecret(ini.GetString("Proxy", "Password", ""))
	ApplyProxySettings()

	AppConfig.EditorAutoComplete = ini.GetString("Editor", "AutoComplete", "1") == "1"
	AppConfig.EditorAutoCompleteMask = ini.GetString("Editor", "AutoCompleteMask", "*.go;*.c;*.cpp;*.h;*.hpp;*.py;*.js;*.ts;*.rs;*.java;*.sh;*.txt;*.md;*.html;*.css;*.json")

	AppConfig.EditorExpandTabs = 0
	fmt.Sscanf(ini.GetString("Editor", "ExpandTabs", "0"), "%d", &AppConfig.EditorExpandTabs)
	AppConfig.EditorAutoIndent = ini.GetString("Editor", "AutoIndent", "1") == "1"
	AppConfig.EditorCursorBeyondEOL = ini.GetString("Editor", "CursorBeyondEOL", "0") == "1"
	AppConfig.EditorUseEditorConfig = ini.GetString("Editor", "UseEditorConfig", "1") == "1"
	AppConfig.EditorCrosshair = ini.GetString("Editor", "Crosshair", "0") == "1"
	AppConfig.EditorAutodetectCodePage = ini.GetString("Editor", "AutodetectCodePage", "1") == "1"
	AppConfig.EditorMemoryMap = ini.GetString("Editor", "MemoryMap", "1") == "1"
	AppConfig.EditorHighlighter = normalizeHighlighter(ini.GetString("Editor", "Highlighter", "Chroma"))
	AppConfig.EditorSyntaxAnimation = ini.GetString("Editor", "SyntaxAnimation", "0") == "1"
	AppConfig.EditorColorerScheme = ini.GetString("Editor", "ColorerScheme", "")
	AppConfig.EditorColorerBackground = ini.GetString("Editor", "ColorerBackground", "1") == "1"
	AppConfig.EditorColorerSyntax = ini.GetString("Editor", "ColorerSyntax", "1") == "1"
	AppConfig.EditorColorerCatalog = ini.GetString("Editor", "ColorerCatalog", "")
	AppConfig.EditorCrossMode = ColorerCrossBoth
	fmt.Sscanf(ini.GetString("Editor", "CrossMode", "3"), "%d", &AppConfig.EditorCrossMode)
	if AppConfig.EditorCrossMode < ColorerCrossOff || AppConfig.EditorCrossMode > ColorerCrossBoth {
		AppConfig.EditorCrossMode = ColorerCrossBoth
	}
	fmt.Sscanf(ini.GetString("Editor", "DefaultCodePage", "65001"), "%d", &AppConfig.EditorDefaultCodePage)
	AppConfig.ViewerAutodetectCodePage = ini.GetString("Viewer", "AutodetectCodePage", "1") == "1"
	fmt.Sscanf(ini.GetString("Viewer", "DefaultCodePage", "65001"), "%d", &AppConfig.ViewerDefaultCodePage)

	// [Mouse] — wheel scroll speed (lines per notch), 0 = system default.
	AppConfig.WheelPanelUp = loadWheelLines(ini, "PanelUp")
	AppConfig.WheelPanelDown = loadWheelLines(ini, "PanelDown")
	AppConfig.WheelEditorUp = loadWheelLines(ini, "EditorUp")
	AppConfig.WheelEditorDown = loadWheelLines(ini, "EditorDown")
	AppConfig.WheelViewerUp = loadWheelLines(ini, "ViewerUp")
	AppConfig.WheelViewerDown = loadWheelLines(ini, "ViewerDown")
	AppConfig.WheelMenuUp = loadWheelLines(ini, "MenuUp")
	AppConfig.WheelMenuDown = loadWheelLines(ini, "MenuDown")
	AppConfig.WheelTableUp = loadWheelLines(ini, "TableUp")
	AppConfig.WheelTableDown = loadWheelLines(ini, "TableDown")

	// [PathHints]
	AppConfig.PathHintTimeout = 2
	fmt.Sscanf(ini.GetString("PathHints", "Timeout", "2"), "%d", &AppConfig.PathHintTimeout)
	if AppConfig.PathHintTimeout < 1 {
		AppConfig.PathHintTimeout = 1
	}
	AppConfig.PathHintFullPath = ini.GetString("PathHints", "FullPath", "0") == "1"
	fmt.Sscanf(ini.GetString("PathHints", "Source", "2"), "%d", &AppConfig.PathHintSource)
	AppConfig.PathHintMaxVisible = 5
	fmt.Sscanf(ini.GetString("PathHints", "MaxVisible", "5"), "%d", &AppConfig.PathHintMaxVisible)
	if AppConfig.PathHintMaxVisible < 1 {
		AppConfig.PathHintMaxVisible = 1
	}
	AppConfig.PathHintPerCategory = ini.GetString("PathHints", "PerCategory", "1") == "1"
	AppConfig.DialogAutoComplete = ini.GetString("PathHints", "DialogAutoComplete", "1") == "1"
	AppConfig.HistoryShowTimes = parseHistoryShowTimes(ini.GetString("History", "ShowTimes", "0,0,0"))
	if configured := ini.GetString("History", "HistoryShowTimes", ""); configured != "" {
		AppConfig.HistoryShowTimes = parseHistoryShowTimes(configured)
	}
	AppConfig.HistoryDirsPrefixLen = 24
	fmt.Sscanf(ini.GetString("History", "DirsPrefixLen", "24"), "%d", &AppConfig.HistoryDirsPrefixLen)
	if configured := ini.GetString("History", "HistoryDirsPrefixLen", ""); configured != "" {
		fmt.Sscanf(configured, "%d", &AppConfig.HistoryDirsPrefixLen)
	}
	if AppConfig.HistoryDirsPrefixLen < 4 {
		AppConfig.HistoryDirsPrefixLen = 4
	}
	AppConfig.SlideShowDelay = defaultSlideShowDelay
	fmt.Sscanf(ini.GetString("Images", "SlideShowDelay", "5"), "%d", &AppConfig.SlideShowDelay)
	if AppConfig.SlideShowDelay <= 0 {
		AppConfig.SlideShowDelay = defaultSlideShowDelay
	}
	AppConfig.ImageExternalTimeout = defaultImageExternalTimeout
	fmt.Sscanf(ini.GetString("Images", "ExternalTimeout", "20"), "%d", &AppConfig.ImageExternalTimeout)
	if AppConfig.ImageExternalTimeout <= 0 {
		AppConfig.ImageExternalTimeout = defaultImageExternalTimeout
	}
	AppConfig.ImageOverlay = overlayEnabled(ini.GetString)
	AppConfig.VideoPauseOnFocusLoss = ini.GetString("Video", "PauseOnFocusLoss", "0") == "1"
	AppConfig.ImageX11OffsetX, AppConfig.ImageX11OffsetY = 0, 0
	fmt.Sscanf(ini.GetString("Images", "X11OverlayOffsetX", "0"), "%d", &AppConfig.ImageX11OffsetX)
	fmt.Sscanf(ini.GetString("Images", "X11OverlayOffsetY", "0"), "%d", &AppConfig.ImageX11OffsetY)
	AppConfig.TTYXKeys = ini.GetString("TTYXi", "Keys", "1") == "1"
	AppConfig.TTYXKeyList = ini.GetString("TTYXi", "KeyList", defaultTTYXKeyList)
	AppConfig.ImageDecoderPriority = ini.GetString("Images", "DecoderPriority", "")
	SetImageDecoderPriorities(ParseImageDecoderPriorities(AppConfig.ImageDecoderPriority))
	AppConfig.UseExternalEditor = ini.GetString("Editor", "UseExternalEditor", "0") == "1"
	AppConfig.ExternalEditorCommand = ini.GetString("Editor", "ExternalEditorCommand", "")
	plugStr := ini.GetString("Plugins", "List", "")
	if plugStr != "" {
		AppConfig.RegisteredPlugins = strings.Split(plugStr, "|")
	}
	AppConfig.EditorTabSize = 4
	fmt.Sscanf(ini.GetString("Editor", "TabSize", "4"), "%d", &AppConfig.EditorTabSize)

	// [Layout] — three known keys plus round-trip storage for anything else.
	fmt.Sscanf(ini.GetString("Layout", "WidthDecrement", "0"), "%d", &AppConfig.WidthDecrement)
	fmt.Sscanf(ini.GetString("Layout", "LeftHeightDecrement", "0"), "%d", &AppConfig.LeftHeightDecrement)
	fmt.Sscanf(ini.GetString("Layout", "RightHeightDecrement", "0"), "%d", &AppConfig.RightHeightDecrement)
	AppConfig.LayoutExtras = nil
	if layout, ok := ini.data["Layout"]; ok {
		for k, v := range layout {
			switch k {
			case "WidthDecrement", "LeftHeightDecrement", "RightHeightDecrement":
				continue
			}
			if AppConfig.LayoutExtras == nil {
				AppConfig.LayoutExtras = make(map[string]string)
			}
			AppConfig.LayoutExtras[k] = v
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
	if err := vfs.SetSystemCodepages(AppConfig.SystemANSICodePage, AppConfig.SystemOEMCodePage); err != nil {
		vtui.DebugLog("CONFIG: forced ANSI/OEM codepage ignored: %v", err)
	}
}

func SaveConfig() {
	saveConfigWithWindowSize(true)
}

// saveConfigWithWindowSize writes the application settings. When windowSize
// is false, the last persisted GUI dimensions are retained instead of the
// dimensions currently held in memory; this keeps the Shift+F9 groups
// independent.
func saveConfigWithWindowSize(windowSize bool) {
	// Settings dialogs write into AppConfig and call SaveConfig; publishing
	// here means a proxy change takes effect without a restart.
	ApplyProxySettings()

	path := getUserConfigIniPath()
	guiCols, guiRows := AppConfig.GuiCols, AppConfig.GuiRows
	if !windowSize {
		guiCols, guiRows = persistedGuiWindowSize()
	}

	var sb strings.Builder
	sb.WriteString("[Interface]\n")
	fmt.Fprintf(&sb, "ColorStyle = %s\n", AppConfig.ColorStyle)
	fmt.Fprintf(&sb, "Language = %s\n", AppConfig.Language)
	fmt.Fprintf(&sb, "FallbackLanguage = %s\n", AppConfig.FallbackLanguage)
	fmt.Fprintf(&sb, "HelpLanguage = %s\n", AppConfig.HelpLanguage)
	fmt.Fprintf(&sb, "ConsoleTitleTemplate = %s\n", AppConfig.ConsoleTitleTemplate)
	fmt.Fprintf(&sb, "DisplayFullPathInTitle = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.DisplayFullPathInTitle])
	fmt.Fprintf(&sb, "AlwaysShowMenuBar = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AlwaysShowMenuBar])
	workspaceTabMode := "multiple"
	if AppConfig.WorkspaceTabMode == int(vtui.WorkspaceTabsAlways) {
		workspaceTabMode = "always"
	} else if AppConfig.WorkspaceTabMode == int(vtui.WorkspaceTabsOnCtrl) {
		workspaceTabMode = "ctrl"
	} else if AppConfig.WorkspaceTabMode == int(vtui.WorkspaceTabsNever) {
		workspaceTabMode = "never"
	}
	ctrlTabMode := "direct"
	if AppConfig.CtrlTabShowsMenu {
		ctrlTabMode = "menu"
	}
	fmt.Fprintf(&sb, "WorkspaceTabMode = %s\n", workspaceTabMode)
	fmt.Fprintf(&sb, "WorkspaceTabsOverlay = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.WorkspaceTabsOverlay])
	fmt.Fprintf(&sb, "CtrlTabMode = %s\n", ctrlTabMode)
	fmt.Fprintf(&sb, "AltNumberSwitchesTabs = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AltNumberSwitchesTabs])
	fmt.Fprintf(&sb, "RestoreWorkspaceTabs = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.RestoreWorkspaceTabs])
	fmt.Fprintf(&sb, "WorkspaceTabNumbering = %s\n\n", AppConfig.WorkspaceTabNumbering.String())
	sb.WriteString("[Panel]\n")
	fmt.Fprintf(&sb, "ShowHiddenFiles = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ShowHiddenFiles])
	fmt.Fprintf(&sb, "ShowDirPrefix = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ShowDirPrefix])
	fmt.Fprintf(&sb, "ShowHighlightMarks = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ShowHighlightMarks])
	fmt.Fprintf(&sb, "SeparateFileExtensions = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.SeparateFileExtensions])
	fmt.Fprintf(&sb, "PanelScrollbarMode = %s\n", AppConfig.PanelScrollbarMode.String())
	fmt.Fprintf(&sb, "ShowPanelFileInfo = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ShowPanelFileInfo])
	fmt.Fprintf(&sb, "SavePanelPaths = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.SavePanelPaths])
	fmt.Fprintf(&sb, "InfoPanelBytes = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.InfoPanelBytes])
	fmt.Fprintf(&sb, "InfoPanelCPUGPU = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.InfoPanelCPUGPU])
	fmt.Fprintf(&sb, "EscTogglePanels = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EscTogglePanels])
	fmt.Fprintf(&sb, "TerminalCtrlNWorkspace = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.TerminalCtrlNWorkspace])
	fmt.Fprintf(&sb, "KeepTerminalCursor = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.KeepTerminalCursor])
	fmt.Fprintf(&sb, "ConsoleMode = %s\n", AppConfig.ConsoleMode)
	fmt.Fprintf(&sb, "ConsoleOverlayUI = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ConsoleOverlayUI])
	fmt.Fprintf(&sb, "CommandLineAutoComplete = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.CommandLineAutoComplete])
	fmt.Fprintf(&sb, "NavigationMode = %s\n", AppConfig.NavigationMode.String())
	fmt.Fprintf(&sb, "SearchCommandStayFocused = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.SearchCommandStayFocused])
	// Keep the legacy key synchronized for older f4 versions and shared configs.
	fmt.Fprintf(&sb, "VimHotkeys = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.NavigationMode == NavigationVim])
	fmt.Fprintf(&sb, "SyncPanelLoad = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.SyncPanelLoad])
	fmt.Fprintf(&sb, "SearchExactOnHit = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.SearchExactOnHit])
	fmt.Fprintf(&sb, "ApplyCommandParallelism = %d\n", AppConfig.ApplyCommandParallelism)
	fmt.Fprintf(&sb, "DefaultFileOpMode = %d\n", AppConfig.DefaultFileOpMode)
	fmt.Fprintf(&sb, "FileOpPathDisplay = %d\n", AppConfig.FileOpPathDisplay)

	sb.WriteString("\n[System]\n")
	fmt.Fprintf(&sb, "ConfirmCopy = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ConfirmCopy])
	fmt.Fprintf(&sb, "ConfirmMove = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ConfirmMove])
	fmt.Fprintf(&sb, "ConfirmDelete = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ConfirmDelete])
	fmt.Fprintf(&sb, "UseTrash = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.UseTrash])
	fmt.Fprintf(&sb, "ConfirmExit = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ConfirmExit])
	fmt.Fprintf(&sb, "DeleteCancelFocused = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.DeleteCancelFocused])
	fmt.Fprintf(&sb, "AutoSaveSettings = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AutoSaveSettings])
	fmt.Fprintf(&sb, "AutoSaveDialogSettings = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AutoSaveDialogSettings])
	fmt.Fprintf(&sb, "AutoSavePanelSettings = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AutoSavePanelSettings])
	fmt.Fprintf(&sb, "AutoSaveCurrentPanel = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AutoSaveCurrentPanel])
	fmt.Fprintf(&sb, "AutoSaveGUIWindow = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AutoSaveGUIWindow])
	fmt.Fprintf(&sb, "AnnounceKittyTerm = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.AnnounceKittyTerm])
	fmt.Fprintf(&sb, "MacroRecordFormat = %d\n", AppConfig.MacroRecordFormat)
	fmt.Fprintf(&sb, "ANSICodePage = %d\n", AppConfig.SystemANSICodePage)
	fmt.Fprintf(&sb, "OEMCodePage = %d\n", AppConfig.SystemOEMCodePage)

	sb.WriteString("\n[Dialogs]\n")
	fmt.Fprintf(&sb, "EnforceColorCorrection = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EnforceColorCorrection])

	sb.WriteString("\n[Appearance]\n")
	fmt.Fprintf(&sb, "GuiFont = %s\n", AppConfig.GuiFont)
	fmt.Fprintf(&sb, "GuiUseSystemMonospace = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.GuiUseSystemMonospace])
	fmt.Fprintf(&sb, "GuiFontSize = %d\n", AppConfig.GuiFontSize)
	fmt.Fprintf(&sb, "GuiCols = %d\n", guiCols)
	fmt.Fprintf(&sb, "GuiRows = %d\n", guiRows)
	if AppConfig.GuiPositionSaved {
		fmt.Fprintf(&sb, "GuiPosX = %d\n", AppConfig.GuiPosX)
		fmt.Fprintf(&sb, "GuiPosY = %d\n", AppConfig.GuiPosY)
	}
	fmt.Fprintf(&sb, "HighlightPriority = %d\n", AppConfig.HighlightPriority)

	sb.WriteString("\n[Startup]\n")
	fmt.Fprintf(&sb, "Mode = %s\n", AppConfig.StartupMode.String())
	fmt.Fprintf(&sb, "GuiBackend = %s\n", AppConfig.GuiBackend)
	fmt.Fprintf(&sb, "TTYBackend = %s\n", AppConfig.TTYBackend)

	sb.WriteString("\n[Update]\n")
	fmt.Fprintf(&sb, "Channel = %d\n", AppConfig.UpdateChannel)
	fmt.Fprintf(&sb, "Interval = %d\n", AppConfig.UpdateInterval)
	fmt.Fprintf(&sb, "LastCheck = %d\n", AppConfig.LastUpdateCheck)
	fmt.Fprintf(&sb, "LastVersion = %s\n", AppConfig.LastUpdateVersion)

	sb.WriteString("\n[Proxy]\n")
	fmt.Fprintf(&sb, "Mode = %d\n", AppConfig.ProxyMode)
	fmt.Fprintf(&sb, "Host = %s\n", AppConfig.ProxyHost)
	fmt.Fprintf(&sb, "Port = %s\n", AppConfig.ProxyPort)
	fmt.Fprintf(&sb, "User = %s\n", AppConfig.ProxyUser)
	fmt.Fprintf(&sb, "Password = %s\n", netproxy.EncodeSecret(AppConfig.ProxyPass))
	sb.WriteString("\n[Editor]\n")
	fmt.Fprintf(&sb, "AutoComplete = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorAutoComplete])
	fmt.Fprintf(&sb, "AutoCompleteMask = %s\n", AppConfig.EditorAutoCompleteMask)

	fmt.Fprintf(&sb, "ExpandTabs = %d\n", AppConfig.EditorExpandTabs)
	fmt.Fprintf(&sb, "AutoIndent = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorAutoIndent])
	fmt.Fprintf(&sb, "CursorBeyondEOL = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorCursorBeyondEOL])
	fmt.Fprintf(&sb, "UseEditorConfig = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorUseEditorConfig])
	fmt.Fprintf(&sb, "Crosshair = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorCrosshair])
	fmt.Fprintf(&sb, "TabSize = %d\n", AppConfig.EditorTabSize)
	fmt.Fprintf(&sb, "UseExternalEditor = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.UseExternalEditor])
	fmt.Fprintf(&sb, "ExternalEditorCommand = %s\n", AppConfig.ExternalEditorCommand)
	fmt.Fprintf(&sb, "AutodetectCodePage = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorAutodetectCodePage])
	fmt.Fprintf(&sb, "MemoryMap = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorMemoryMap])
	fmt.Fprintf(&sb, "Highlighter = %s\n", AppConfig.EditorHighlighter)
	fmt.Fprintf(&sb, "SyntaxAnimation = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorSyntaxAnimation])
	fmt.Fprintf(&sb, "ColorerScheme = %s\n", AppConfig.EditorColorerScheme)
	fmt.Fprintf(&sb, "ColorerBackground = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorColorerBackground])
	fmt.Fprintf(&sb, "ColorerSyntax = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.EditorColorerSyntax])
	fmt.Fprintf(&sb, "ColorerCatalog = %s\n", AppConfig.EditorColorerCatalog)
	fmt.Fprintf(&sb, "CrossMode = %d\n", AppConfig.EditorCrossMode)
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", AppConfig.EditorDefaultCodePage)

	sb.WriteString("\n[Viewer]\n")
	fmt.Fprintf(&sb, "AutodetectCodePage = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.ViewerAutodetectCodePage])
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", AppConfig.ViewerDefaultCodePage)
	sb.WriteString("\n[Mouse]\n")
	fmt.Fprintf(&sb, "PanelUp = %d\n", AppConfig.WheelPanelUp)
	fmt.Fprintf(&sb, "PanelDown = %d\n", AppConfig.WheelPanelDown)
	fmt.Fprintf(&sb, "EditorUp = %d\n", AppConfig.WheelEditorUp)
	fmt.Fprintf(&sb, "EditorDown = %d\n", AppConfig.WheelEditorDown)
	fmt.Fprintf(&sb, "ViewerUp = %d\n", AppConfig.WheelViewerUp)
	fmt.Fprintf(&sb, "ViewerDown = %d\n", AppConfig.WheelViewerDown)
	fmt.Fprintf(&sb, "MenuUp = %d\n", AppConfig.WheelMenuUp)
	fmt.Fprintf(&sb, "MenuDown = %d\n", AppConfig.WheelMenuDown)
	fmt.Fprintf(&sb, "TableUp = %d\n", AppConfig.WheelTableUp)
	fmt.Fprintf(&sb, "TableDown = %d\n", AppConfig.WheelTableDown)
	sb.WriteString("\n[PathHints]\n")
	fmt.Fprintf(&sb, "Timeout = %d\n", AppConfig.PathHintTimeout)
	fmt.Fprintf(&sb, "FullPath = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.PathHintFullPath])
	fmt.Fprintf(&sb, "Source = %d\n", AppConfig.PathHintSource)
	fmt.Fprintf(&sb, "MaxVisible = %d\n", AppConfig.PathHintMaxVisible)
	fmt.Fprintf(&sb, "PerCategory = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.PathHintPerCategory])
	fmt.Fprintf(&sb, "DialogAutoComplete = %d\n", map[bool]int{true: 1, false: 0}[AppConfig.DialogAutoComplete])

	sb.WriteString("\n[History]\n")
	fmt.Fprintf(&sb, "ShowTimes = %d,%d,%d\n", AppConfig.HistoryShowTimes[0], AppConfig.HistoryShowTimes[1], AppConfig.HistoryShowTimes[2])
	fmt.Fprintf(&sb, "DirsPrefixLen = %d\n", AppConfig.HistoryDirsPrefixLen)
	sb.WriteString("\n[Images]\n")
	fmt.Fprintf(&sb, "SlideShowDelay = %d\n", AppConfig.SlideShowDelay)
	fmt.Fprintf(&sb, "ExternalTimeout = %d\n", AppConfig.ImageExternalTimeout)
	fmt.Fprintf(&sb, "DecoderPriority = %s\n", AppConfig.ImageDecoderPriority)
	sb.WriteString("\n[Plugins]\n")
	fmt.Fprintf(&sb, "List = %s\n", strings.Join(AppConfig.RegisteredPlugins, "|"))

	// [Layout]: emit our three keys plus any unrecognised keys we loaded
	// (round-trip). Keys are written alphabetically to match far2l's
	// on-disk order, so a diff against far2l's config.ini stays minimal.
	layoutKeys := map[string]string{
		"WidthDecrement":       fmt.Sprintf("%d", AppConfig.WidthDecrement),
		"LeftHeightDecrement":  fmt.Sprintf("%d", AppConfig.LeftHeightDecrement),
		"RightHeightDecrement": fmt.Sprintf("%d", AppConfig.RightHeightDecrement),
	}
	for k, v := range AppConfig.LayoutExtras {
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

	err := writeFileAtomically(path, []byte(sb.String()), 0600)
	if err != nil {
		vtui.DebugLog("CONFIG: Failed to save application settings: %v", err)
		return
	}

	vtui.DebugLog("CONFIG: Saved application settings to %s", path)
}

func persistedGuiWindowSize() (int, int) {
	cols, rows := AppConfig.GuiCols, AppConfig.GuiRows
	ini := newIniFile()
	for _, path := range getConfigIniPaths() {
		if _, err := os.Stat(path); err == nil {
			ini.Merge(LoadIni(path))
		}
	}
	fmt.Sscanf(ini.GetString("Appearance", "GuiCols", fmt.Sprintf("%d", cols)), "%d", &cols)
	fmt.Sscanf(ini.GetString("Appearance", "GuiRows", fmt.Sprintf("%d", rows)), "%d", &rows)
	if cols <= 0 {
		cols = AppConfig.GuiCols
	}
	if rows <= 0 {
		rows = AppConfig.GuiRows
	}
	return cols, rows
}

// saveGuiWindowSize updates only the persisted GUI dimensions. It preserves
// unrelated settings and unknown keys in an existing settings.ini.
func saveGuiWindowSize() {
	path := getUserConfigIniPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		vtui.DebugLog("CONFIG: Failed to create settings directory: %v", err)
		return
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		for _, source := range getConfigIniPaths() {
			if source == path {
				continue
			}
			if inherited, readErr := os.ReadFile(source); readErr == nil {
				updated := updateIniValues(inherited, "Appearance", guiWindowValues())
				if writeErr := os.WriteFile(path, updated, 0600); writeErr != nil {
					vtui.DebugLog("CONFIG: Failed to save GUI size: %v", writeErr)
				} else if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
					vtui.DebugLog("CONFIG: Failed to restrict GUI settings permissions: %v", chmodErr)
				}
				return
			}
		}
		data = []byte(fmt.Sprintf("[Appearance]\nGuiCols = %d\nGuiRows = %d\n", AppConfig.GuiCols, AppConfig.GuiRows))
		if AppConfig.GuiPositionSaved {
			data = append(data, []byte(fmt.Sprintf("GuiPosX = %d\nGuiPosY = %d\n", AppConfig.GuiPosX, AppConfig.GuiPosY))...)
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
	updated := updateIniValues(data, "Appearance", guiWindowValues())
	if err := os.WriteFile(path, updated, 0600); err != nil {
		vtui.DebugLog("CONFIG: Failed to save GUI size: %v", err)
	} else if chmodErr := os.Chmod(path, 0600); chmodErr != nil {
		vtui.DebugLog("CONFIG: Failed to restrict GUI settings permissions: %v", chmodErr)
	}
}

func guiWindowValues() map[string]string {
	values := map[string]string{
		"GuiCols": strconv.Itoa(AppConfig.GuiCols),
		"GuiRows": strconv.Itoa(AppConfig.GuiRows),
	}
	if AppConfig.GuiPositionSaved {
		values["GuiPosX"] = strconv.Itoa(AppConfig.GuiPosX)
		values["GuiPosY"] = strconv.Itoa(AppConfig.GuiPosY)
	}
	return values
}

func saveSettingsGroups(general, panel, window bool) {
	if !general && !panel && !window {
		return
	}
	if window {
		captureCurrentWindowSize()
		captureCurrentWindowPosition()
	}
	if general {
		saveConfigWithWindowSize(window)
	} else if window {
		saveGuiWindowSize()
	}
	if panel {
		saveSessionFile(getSessionIniPath())
	}
}

func syncAutoSaveMaster() {
	AppConfig.AutoSaveSettings = AppConfig.AutoSaveDialogSettings ||
		AppConfig.AutoSavePanelSettings || AppConfig.AutoSaveCurrentPanel || AppConfig.AutoSaveGUIWindow
}

func updateIniValues(data []byte, section string, values map[string]string) []byte {
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
	if !AppConfig.AutoSaveSettings || !AppConfig.AutoSaveDialogSettings {
		return
	}
	saveConfigTimerMu.Lock()
	defer saveConfigTimerMu.Unlock()
	if saveConfigTimer != nil {
		saveConfigTimer.Stop()
	}
	// Taken here, on the goroutine that arms the timer, rather than inside it.
	// The timer outlives whatever scheduled it -- half a second is a long time
	// in a test suite -- and reading the global from the callback races
	// anything that reassigns vtui.FrameManager in the meantime, which in the
	// tests is the next test to call swapFrameManager.
	frames := vtui.FrameManager
	saveConfigTimer = time.AfterFunc(saveConfigDebounce, func() {
		// AfterFunc runs this on a goroutine of its own, and everything below
		// reads AppConfig -- the flags here, then SaveConfig and the proxy
		// settings it applies. AppConfig belongs to the UI goroutine, which
		// goes on editing it while the timer counts down, so reading it from
		// here is a race against whoever is changing settings. Hand the work
		// back to the UI instead; the delay is the debounce, not the thread.
		if frames == nil {
			return
		}
		frames.PostTask(func() {
			if AppConfig.AutoSaveSettings && AppConfig.AutoSaveDialogSettings {
				SaveConfig()
			}
		})
	})
}

var (
	saveConfigTimerMu sync.Mutex
	saveConfigTimer   *time.Timer
)

const saveConfigDebounce = 500 * time.Millisecond

// loadWheelLines reads a [Mouse] wheel-speed key: lines per notch,
// 0 = system default. Negative values are treated as 0.
func loadWheelLines(ini *IniFile, key string) int {
	n := 0
	fmt.Sscanf(ini.GetString("Mouse", key, "0"), "%d", &n)
	if n < 0 {
		n = 0
	}
	return n
}

// wheelScrollLines resolves a configured wheel speed (0 = follow the system
// setting) into the number of lines to scroll per wheel notch.
func wheelScrollLines(cfg int) int {
	if cfg <= 0 {
		return vtui.WheelLinesPerNotch()
	}
	return cfg
}

// applyWheelSettings pushes the menu/table wheel speed overrides into vtui.
// Panels, editor and viewer are handled by f4 itself via wheelScrollLines.
func applyWheelSettings() {
	vtui.SetWheelAreaLines(vtui.WheelAreaMenu, AppConfig.WheelMenuUp, AppConfig.WheelMenuDown)
	vtui.SetWheelAreaLines(vtui.WheelAreaList, AppConfig.WheelTableUp, AppConfig.WheelTableDown)
}

func createDefaultHighlightIni(path string) {
	content := `# User highlight rules and sort groups.
#
# f4 applies file highlighting rules from both the active Color Style (Theme)
# and this file. By default, rules in this file have higher priority.
#
# You can add your custom highlight groups here (e.g. Mask = *.mp3).
# Default groups (Hidden, Executables, Directories) are already defined
# by the active Color Style, so you don't need to duplicate them unless
# you specifically want to override the theme's colors.
#
# [SortGroup_N] sections below define sort groups. They accept the same
# matching keys as a highlight rule (Mask, IncludeAttributes,
# ExcludeAttributes, SizeAbove/SizeBelow, DateAfter/DateBefore) and are
# used only when a panel has "Use sort groups" switched on: files are then
# clustered by group first and sorted by the current sort mode inside each
# group. Group decides where a cluster goes; sections that share a number
# form one group, and files matching no group land after all of them.

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
		Mode: AppConfig.ProxyMode,
		Host: AppConfig.ProxyHost,
		Port: AppConfig.ProxyPort,
		User: AppConfig.ProxyUser,
		Pass: AppConfig.ProxyPass,
	})
}
