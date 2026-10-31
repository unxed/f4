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
	"sync/atomic"
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

// SyncDefaultMask is what the mask field of the synchronize dialog starts
// out with: everything, the way Total Commander's own field does.
const SyncDefaultMask = "*"

// SyncOptions mirrors the option row of Total Commander's "Synchronize
// dirs" window, which is the reference this feature follows.
type SyncOptions struct {
	// Asymmetric makes the right folder a mirror of the left one:
	// anything missing or older on the right is copied over it, and
	// anything the left folder does not have is deleted from the right.
	// Without it the two folders are peers and each side's newer file
	// wins.
	Asymmetric bool
	// Subdirs compares the whole tree instead of the two folders' own
	// files.
	Subdirs bool
	// ByContent reads the files whose size and time already match, to
	// find the ones that only look equal.
	ByContent bool
	// IgnoreDate takes name and size as the whole truth. Total
	// Commander documents the consequence: such a comparison can only
	// answer "equal" or "not equal", so the copying direction is left
	// to the user.
	IgnoreDate bool
	// Mask is the far2l-style file mask the comparison is limited to,
	// including the "|" exclude section.
	Mask string
}

// DefaultSyncOptions is Total Commander's own starting position: the whole
// tree, everything in it, times and sizes decide.
func DefaultSyncOptions() SyncOptions {
	return SyncOptions{
		Subdirs: true,
		Mask:    SyncDefaultMask,
	}
}

// Normalize repairs values a hand-edited config may hold.
func (o SyncOptions) Normalize() SyncOptions {
	if strings.TrimSpace(o.Mask) == "" {
		o.Mask = SyncDefaultMask
	}
	return o
}

// CompareOptions is the comparison these sync options ask for, so that the
// synchronize window and the Advanced Compare dialog answer the same
// question the same way instead of growing two comparison engines.
//
// The two-second slack is always on: Total Commander treats a FAT
// timestamp that is one second away from its source as the same time, and
// without it every file copied to a memory card comes back as differing.
func (o SyncOptions) CompareOptions() CompareOptions {
	return CompareOptions{
		Recursive:  o.Subdirs,
		MaxDepth:   CompareMaxDepthLimit,
		ByTime:     !o.IgnoreDate,
		TimeSlack:  true,
		BySize:     true,
		ByContent:  o.ByContent,
		IgnoreMode: CompareIgnoreEOL,
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
	UseLocalLanguageFiles    bool
	AlwaysShowMenuBar        bool
	DialogOuterBorder        bool   // leave an empty border outside dialog/UserMenu/drive-menu/plugins-menu borders instead of a second frame: three cells on the sides, one above and below for dialogs, two cells on the sides, one above and below for menus, far2l/Far3 style (default off)
	SettingsMaximized        bool   // Settings Center always opens maximized (f4#1239, default off)
	GlyphStyle               string // box, checkbox and radio glyphs of graphical windows: "classic" or "rounded" (f4#285)
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
	ShowSymlinkArrow         bool
	SeparateFileExtensions   bool
	PanelScrollbarMode       PanelScrollbarMode
	ShowPanelFileInfo        bool
	SavePanelPaths           bool
	DriveMenuOptions         uint32 // display/filter flags for the Alt+F1/Alt+F2 menu
	InfoPanelBytes           bool   // Ctrl+L info panel: true = raw bytes, false = human (GiB/MiB…)
	InfoPanelCPUGPU          bool   // Ctrl+L info panel: show CPU and GPU sections (off by default)
	TreeRootWholeVolume      bool   // Ctrl+T tree panel root: true = whole current volume (far2l), false = source panel's current directory
	TreeExplorerMode         bool   // Ctrl+T tree like Explorer's: the panel that opened it follows the tree's cursor (f4#1602)
	DirSizeCountsFolders     bool   // folder size (F3, quick view) also counts the size file systems give the folders themselves, as far2l does (f4#2005)
	PlayerBar                bool   // the audio player (Ctrl+Shift+M) draws a playback bar between its clocks (f4#2185)
	PlayerSpectrum           bool   // the audio player draws the spectrum of what plays, on by default as it always was (f4#2185)
	PlayerKeys               string // the player's letter keys: PlayerKeysBoth, PlayerKeysWinAmp or PlayerKeysYouTube (f4#2185)
	PlayerKeyHints           bool   // the player shows its keys under its buttons and in its border; H flips it (f4#2185)
	EscTogglePanels          bool   // ESC toggles panels visibility (Far ships this as a macro; on by default)
	TerminalCtrlNWorkspace   bool   // reserve Ctrl+N in terminal views for cloning panels to a workspace
	KeepTerminalCursor       bool
	CursorInsertShape        string // caret while typing: "underline" | "bar" | "block" (f4 #1154)
	CursorOvertypeShape      string // caret in overtype mode, same names
	CursorBlink              bool
	ConsoleMode              string // "own" | "host" (default "own")
	PluginDefaultHotkeysOff  string // semicolon-separated hotkeys (e.g. "ShiftF1;ShiftF2") whose plugin defaults the user removed with F4/Del in the plugin menu (f4:config)
	DragOutModifier          string // "" | "ctrl" | "alt" | "shift": a drag out of a panel starts only while this key is held (default "", f4:config)
	DragOutHoldMs            int    // without DragOutModifier: how long (ms) a left button is held on a file before a move starts a drag out instead of moving the cursor; 0 starts at once, -1 only outside the rows (default 250, f4:config)
	DropOnFolder             string // "current" | "folder" | "ask": where files dropped over a folder row go when the backend could not show the target during the drag (default "current", f4:config, #2049)
	ConsoleOverlayUI         bool   // Show f4 command line and keybar overlay on top of host console (default false)
	HostConsoleDefaultColors bool   // Host modes: draw the console mirror shown beside a hidden panel in the terminal's own default colours (default false, f4:config)
	UseWinescape             bool   // Windows only: let the file layer use libwinescape where it is available (default true)
	AnnounceKittyTerm        bool   // introduce the built-in terminal as kitty, so that image tools use the graphics protocol
	CommandLineAutoComplete  bool
	UsePromptFormat          bool
	PromptFormat             string
	// The 16 basic ANSI colours as #RRGGBB, in palette order (f4#2071).
	AnsiColorBlack           string
	AnsiColorRed             string
	AnsiColorGreen           string
	AnsiColorYellow          string
	AnsiColorBlue            string
	AnsiColorMagenta         string
	AnsiColorCyan            string
	AnsiColorWhite           string
	AnsiColorBrightBlack     string
	AnsiColorBrightRed       string
	AnsiColorBrightGreen     string
	AnsiColorBrightYellow    string
	AnsiColorBrightBlue      string
	AnsiColorBrightMagenta   string
	AnsiColorBrightCyan      string
	AnsiColorBrightWhite     string
	TerminalForeground       string // this and the next four: the embedded terminal's own colours, #RRGGBB or empty for the theme's (f4#2071)
	TerminalBackground       string
	TerminalCursorColor      string
	TerminalSelectionText    string
	TerminalSelectionColor   string
	NavigationMode           PanelNavigationMode
	PanelAutoFilter          bool // a lone Alt press opens the panel autofilter (hides non-matching rows); Alt+letter stays the quick search
	PanelStrictAutoFilter    bool // panel quick search/autofilter requires an exact match instead of tolerating one typo
	PanelQuickSearchXLat     bool // a quick search/autofilter text that finds nothing is tried in the other keyboard layout, as far2l's XLat.EnableForFastFileFind
	PanelGroupSmallMiB       int
	PanelGroupMediumMiB      int
	PanelGroupLargeMiB       int
	SearchCommandStayFocused bool
	SyncPanelLoad            bool
	SearchExactOnHit         bool // QuickSearch keeps only exact matches when at least one exists
	ApplyCommandParallelism  int  // 0 = unlimited; absent config defaults to runtime.NumCPU()
	EditorAutoComplete       bool
	EditorAutoCompleteMask   string
	// ArchiveEnterExcludeMask names the files Enter must not open as an
	// archive even when their content is one. It is a far2l file mask, so
	// "|" still carves an exception out of it.
	ArchiveEnterExcludeMask string
	// ObserverEnterExcludeMask is ArchiveEnterExcludeMask's counterpart for
	// plugins/observer (f4#1563): files Enter must not open as an Observer
	// container (an ISO image, for the one module wired up so far) even
	// when their content is one. Ctrl+PgDn keeps opening them either way.
	// Empty by default -- unlike office documents, which are ZIP containers
	// far2l already lists, nothing yet is known to collide with the formats
	// Observer modules cover.
	ObserverEnterExcludeMask string
	// ArchiveTarIndexCache keeps the file index of an opened tar archive in the
	// cache so that opening it again is instant. Off rebuilds the index every
	// time, which is slower and never out of date (#1187).
	ArchiveTarIndexCache bool
	// WatchDirectories refreshes a panel showing a local directory when the
	// files in it change on disk, via inotify where available and periodic
	// comparison elsewhere (f4#1668). Off leaves refreshing to the user.
	WatchDirectories bool
	// ArchiveUseRatarmountIfAvailable opts in to using the user's own
	// external `ratarmount` (https://github.com/mxmlnkn/ratarmount), when it
	// is found on PATH, as a faster tar-index backend instead of f4's own
	// internal/tarindexcache (#251). This first part only stores the
	// preference and lets plugins/archive.RatarmountAvailable() detect the
	// binary; it does not change how archives are opened yet -- see that
	// function's doc comment for the plan.
	ArchiveUseRatarmountIfAvailable bool
	EditorExpandTabs                int
	EditorAutoIndent                bool
	EditorCursorBeyondEOL           bool
	EditorPersistentBlocks          bool
	EditorTabSize                   int
	EditorUseEditorConfig           bool
	EditorCrosshair                 bool
	EditorShowControlChars          bool
	EditorMarkOccurrences           bool
	UseExternalEditor               bool
	ExternalEditorCommand           string
	ExternalEditorConsole           string
	ExternalEditorGUI               string
	EditorAutodetectCodePage        bool
	EditorHighlighter               string
	EditorSyntaxAnimation           bool
	EditorColorerScheme             string
	EditorColorerBackground         bool
	EditorColorerSyntax             bool
	EditorColorerCatalog            string
	EditorColorerPairs              bool   // draw the pair under the cursor, FarColorer's PairsDraw
	EditorColorerOldOutline         bool   // list lines, not labels, in the outliner: FarColorer's OldOutlineView
	EditorColorerUserHrc            string // user schemes, FarColorer's UserHrcPath
	EditorColorerUserHrd            string // user colour styles, FarColorer's UserHrdPath
	EditorColorerHrcSettings        string // user HRC settings, FarColorer's UserHrcSettingsPath
	EditorCrossMode                 int
	ViewerHighlighting              int // where viewers highlight syntax; see ViewerHighlightOff
	EditorDefaultCodePage           int
	// EditorMemoryMap lets the editor map a local file instead of reading it
	// in chunks. Off means every buffer takes the lazily fetched path, which
	// is the escape hatch for a file system where mapping misbehaves.
	EditorMemoryMap          bool
	ViewerAutodetectCodePage bool
	ViewerDefaultCodePage    int
	// ViewerOpenAsSupportedType sends a picture to the image viewer, a
	// video to the video player, and a Markdown file to the formatted
	// view (issue #991, then #1625) when a file is opened for viewing.
	// Off, every file opens in the text and hex viewer.
	ViewerOpenAsSupportedType bool
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
	// Wheel acceleration: how many lines the very fastest notch queues on
	// top of the rows it scrolls at once, WheelAccelerationMin (the ramp
	// never queues) to WheelAccelerationMax. The spin window, the coast
	// step and its delay are tuned in code, see internal/wheel.
	WheelAcceleration int
	// ElevationIdleSeconds: how long the elevated helper of Windows (UAC) may
	// stay without requests before f4 closes it and asks again next time;
	// 0 keeps administrator rights until f4 exits (f4#1768).
	ElevationIdleSeconds int
	// Path hints (autocomplete in path inputs and the command line).
	PathHintTimeout     int  // seconds for a VFS ReadDir behind a hint
	PathHintFullPath    bool // show full paths in the hint, false = final element only
	PathHintSource      int  // 0 = active panel, 1 = passive panel, 2 = both
	PathHintMaxVisible  int  // visible rows cap in the hint list
	PathHintPerCategory bool // the cap applies per category (active/passive/history)
	DialogAutoComplete  bool // drop-down while typing in fields that have history
	// HistoryShowTimes controls the timestamp presentation in command, folder,
	// and viewer/editor history dialogs: date+time, date, or hidden.
	HistoryShowTimes             [HistoryTypeCount]int
	HistoryDirsPrefixLen         int // command-history directory prefix width
	SlideShowDelay               int
	ImageOverlay                 bool
	VideoPauseOnFocusLoss        bool
	ImageX11OffsetX              int
	ImageX11OffsetY              int
	TTYXKeys                     bool
	TTYXKeyList                  string
	ImageExternalTimeout         int
	ImageDecoderPriority         string
	RegisteredPlugins            []string
	ConfirmCopy                  bool
	ConfirmMove                  bool
	ConfirmDelete                bool
	UseTrash                     bool
	ConfirmExit                  bool
	DeleteCancelFocused          bool
	AutoSaveSettings             bool
	AutoSaveDialogSettings       bool
	AutoSavePanelSettings        bool
	AutoSaveCurrentPanel         bool
	AutoSaveGUIWindow            bool
	DefaultFileOpMode            int
	QueueSmallOpsBypass          bool // small files in the queue need not wait for a long operation (f4#1894)
	CopyThreads                  int  // files copied at once between local disks, 1..MaxCopyThreads; 1 is the single-file path (f4#1894)
	FileOpPathDisplay            int
	ClipboardImageFormat         string
	ClipboardImagePNGCompression string
	ClipboardImageJPEGQuality    int
	ClipboardImagePrefix         string
	ClipboardImageTemplate       string
	ClipboardImageDigitFormat    string
	CopyAccessRights             int
	MacroRecordFormat            int
	GuiFont                      string
	GuiUseSystemMonospace        bool
	GuiFontSize                  int
	GuiCols                      int
	GuiRows                      int
	GuiPosX                      int
	GuiPosY                      int
	GuiPositionSaved             bool
	// StartupMode, GuiBackend and TTYBackend answer "what should plain `f4`
	// do?". They are only defaults: --gui/--tty still win on any single run.
	// An empty backend means automatic selection.
	StartupMode StartupMode
	GuiBackend  string
	TTYBackend  string
	// StartInCurrentFolder picks what a start from a terminal does with the
	// panels. Off is far2l's and Far's way: `f4` restores the panels of the
	// last session, and a folder on the command line replaces only its own
	// panel. On is mc's way: `f4` opens the current folder in both panels
	// (issues #822, #495).
	StartInCurrentFolder   bool
	ConsoleTitleTemplate   string
	DisplayFullPathInTitle bool
	UpdateChannel          int // 0 = Stable, 1 = Nightly
	UpdateInterval         int // 0 = Never, 1 = Every start, 2 = Daily, 3 = Weekly
	EnforceColorCorrection bool
	MenuLoopScroll         bool   // far2l Opt.VMenu.MenuLoopScroll, [VMenu] MenuStopWrapOnEdge
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

	// Sync keeps what the synchronize dialog was last set to, the way
	// Total Commander saves its own sync options.
	Sync SyncOptions
}

var App = F4Config{
	ColorStyle:               "Modern",
	Language:                 "en",
	FallbackLanguage:         "",
	HelpLanguage:             "en",
	UseLocalLanguageFiles:    false,
	AlwaysShowMenuBar:        false,
	DialogOuterBorder:        false,
	SettingsMaximized:        false,
	GlyphStyle:               GlyphStyleClassic,
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
	ShowSymlinkArrow:         false,
	SeparateFileExtensions:   false,
	PanelScrollbarMode:       PanelScrollbarMinimal,
	ShowPanelFileInfo:        false,
	SavePanelPaths:           true,
	DriveMenuOptions:         DefaultDriveMenuOptions,
	InfoPanelBytes:           false,
	InfoPanelCPUGPU:          false,
	TreeRootWholeVolume:      true,
	TreeExplorerMode:         false,
	DirSizeCountsFolders:     false,
	PlayerBar:                true,
	PlayerSpectrum:           true,
	PlayerKeys:               PlayerKeysBoth,
	PlayerKeyHints:           false,
	EscTogglePanels:          true,
	TerminalCtrlNWorkspace:   true,
	KeepTerminalCursor:       false,
	CursorInsertShape:        "underline",
	CursorOvertypeShape:      "block",
	CursorBlink:              true,
	ConsoleMode:              "own",
	ConsoleOverlayUI:         false,
	PluginDefaultHotkeysOff:  "",
	DragOutModifier:          "",
	DragOutHoldMs:            DefaultDragOutHoldMs,
	DropOnFolder:             DropOnFolderCurrent,
	HostConsoleDefaultColors: false,
	UseWinescape:             true,
	AnnounceKittyTerm:        true,
	CommandLineAutoComplete:  true,
	UsePromptFormat:          false,
	PromptFormat:             "$u@$n:$p$# ",
	NavigationMode:           NavigationClassic,
	PanelAutoFilter:          false,
	PanelStrictAutoFilter:    false,
	PanelQuickSearchXLat:     true,
	PanelGroupSmallMiB:       5,
	PanelGroupMediumMiB:      10,
	PanelGroupLargeMiB:       100,
	SearchCommandStayFocused: false,
	SyncPanelLoad:            false,
	SearchExactOnHit:         false,
	ApplyCommandParallelism:  runtime.NumCPU(),
	EditorAutoComplete:       true,
	EditorAutoCompleteMask:   "*.go;*.c;*.cpp;*.h;*.hpp;*.py;*.js;*.ts;*.rs;*.java;*.sh;*.txt;*.md;*.html;*.css;*.json",
	// far2l's KnownDocumentTypes (multiarc/src/MultiArc.cpp), the list it
	// refuses to sink into on Enter "even while its really archive", plus
	// .epub, which f4 issue #1184 named and far2l's list does not.
	ArchiveTarIndexCache:            true,
	WatchDirectories:                true,
	ArchiveUseRatarmountIfAvailable: false,
	ArchiveEnterExcludeMask:         "*.docx,*.docm,*.dotx,*.dotm,*.xlsx,*.xlsm,*.xltx,*.xltm,*.xlsb,*.xlam,*.pptx,*.pptm,*.potx,*.potm,*.ppam,*.ppsx,*.ppsm,*.sldx,*.sldm,*.thmx,*.odt,*.ods,*.odp,*.epub",
	ObserverEnterExcludeMask:        "",
	EditorExpandTabs:                0,
	EditorAutoIndent:                true,
	EditorCursorBeyondEOL:           false,
	EditorPersistentBlocks:          false,
	EditorTabSize:                   4,
	EditorUseEditorConfig:           true,
	EditorCrosshair:                 false,
	EditorShowControlChars:          false,
	EditorMarkOccurrences:           true,
	UseExternalEditor:               false,
	ExternalEditorCommand:           "",
	ExternalEditorConsole:           "",
	ExternalEditorGUI:               "",
	EditorAutodetectCodePage:        true,
	EditorHighlighter:               "Chroma",
	EditorSyntaxAnimation:           false,
	EditorColorerScheme:             "",
	EditorColorerBackground:         true,
	EditorColorerSyntax:             true,
	EditorColorerCatalog:            "",
	EditorColorerPairs:              true,
	EditorColorerOldOutline:         true,
	EditorColorerUserHrc:            "",
	EditorColorerUserHrd:            "",
	EditorColorerHrcSettings:        "",
	EditorCrossMode:                 ColorerCrossBoth,
	ViewerHighlighting:              ViewerHighlightOff,
	EditorDefaultCodePage:           65001,
	EditorMemoryMap:                 true,
	ViewerAutodetectCodePage:        true,
	ViewerDefaultCodePage:           65001,
	WheelPanelUp:                    0,
	WheelPanelDown:                  0,
	WheelEditorUp:                   0,
	WheelEditorDown:                 0,
	WheelViewerUp:                   0,
	WheelViewerDown:                 0,
	WheelMenuUp:                     0,
	WheelMenuDown:                   0,
	WheelTableUp:                    0,
	WheelTableDown:                  0,
	WheelAcceleration:               WheelAccelerationDefault,
	ElevationIdleSeconds:            DefaultElevationIdleSeconds,
	PathHintTimeout:                 2,
	PathHintFullPath:                false,
	PathHintSource:                  2,
	PathHintMaxVisible:              5,
	PathHintPerCategory:             true,
	DialogAutoComplete:              true,
	HistoryShowTimes:                [HistoryTypeCount]int{HistoryShowDateTime, HistoryShowDateTime, HistoryShowDateTime},
	HistoryDirsPrefixLen:            24,
	SlideShowDelay:                  DefaultSlideShowDelay,
	ImageOverlay:                    true,
	TTYXKeys:                        true,
	TTYXKeyList:                     DefaultTTYXKeyList,
	ImageExternalTimeout:            DefaultImageExternalTimeout,
	ImageDecoderPriority:            "",
	ConfirmCopy:                     true,
	ConfirmMove:                     true,
	ConfirmDelete:                   true,
	UseTrash:                        false,
	ConfirmExit:                     true,
	DeleteCancelFocused:             false,
	AutoSaveSettings:                true,
	AutoSaveDialogSettings:          true,
	AutoSavePanelSettings:           true,
	AutoSaveCurrentPanel:            true,
	AutoSaveGUIWindow:               true,
	DefaultFileOpMode:               0,
	QueueSmallOpsBypass:             true,
	CopyThreads:                     1,
	FileOpPathDisplay:               0,
	ClipboardImageFormat:            "png",
	ClipboardImagePNGCompression:    "default",
	ClipboardImageJPEGQuality:       90,
	ClipboardImagePrefix:            "screenshot",
	ClipboardImageTemplate:          "!{prefix}!!{seq}!",
	ClipboardImageDigitFormat:       "000",
	CopyAccessRights:                0,
	GuiFont:                         "",
	GuiUseSystemMonospace:           true,
	GuiFontSize:                     DefaultGuiFontSize(runtime.GOOS),
	GuiCols:                         100,
	GuiRows:                         30,
	GuiPosX:                         0,
	GuiPosY:                         0,
	GuiPositionSaved:                false,
	StartupMode:                     StartupModeAuto,
	StartInCurrentFolder:            false,
	GuiBackend:                      "",
	TTYBackend:                      "",
	ConsoleTitleTemplate:            "f4 %Ver %Platform %Admin - %State",
	DisplayFullPathInTitle:          false,
	UpdateChannel:                   0,
	ProxyMode:                       netproxy.ModeSystem,
	UpdateInterval:                  3, // Default to Weekly
	EnforceColorCorrection:          true,
	MenuLoopScroll:                  true,
	HighlightPriority:               0,
	LastUpdateCheck:                 0,
	LastUpdateVersion:               "",
	Compare:                         DefaultCompareOptions(),
	Sync:                            DefaultSyncOptions(),

	// Pictures and video open in their own viewers (issue #991).
	ViewerOpenAsSupportedType: true,
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

// colorStyleConfigured records whether any settings.ini that LoadConfig read
// names a ColorStyle. It is the only way to tell "the user chose Radiola" from
// "nobody chose anything, so Radiola is the fallback", which is what lets a
// first start pick a style that suits the console (see app.firstRunColorStyle).
var colorStyleConfigured atomic.Bool

// ColorStyleConfigured reports whether the last LoadConfig found a ColorStyle
// in a settings.ini. False means the value in App.ColorStyle is only the
// built-in default.
func ColorStyleConfigured() bool {
	return colorStyleConfigured.Load()
}

func LoadConfig() {
	merged := loadSettingsIni()
	parseConfigInto(&App, merged)
	colorStyleConfigured.Store(merged.GetString("Interface", "ColorStyle", "") != "")
	// What was read takes effect only here. parseConfigInto itself touches
	// nothing outside the struct it fills, which is what lets f4:config work
	// out defaults and try an edit without disturbing the running f4.
	applyForcedCodePages()
	ApplyProxySettings()
	SetImageDecoderPriorities(ParseImageDecoderPriorities(App.ImageDecoderPriority))
}

// loadSettingsIni merges every settings.ini f4 reads, the machine-wide one
// first, so that the user's own file wins.
func loadSettingsIni() *ini.File {
	paths := GetConfigIniPaths()
	merged := ini.New()

	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			vtui.DebugLog("CONFIG: Loading and merging config from %s", path)
			merged.Merge(ini.Load(path))
		}
	}
	return merged
}

// parseConfigInto reads merged into cfg. A field it does not read keeps the
// value cfg already had.
func parseConfigInto(cfg *F4Config, merged *ini.File) {
	cfg.ShowHiddenFiles = merged.GetString("Panel", "ShowHiddenFiles", "1") == "1"
	cfg.ColorStyle = merged.GetString("Interface", "ColorStyle", "Radiola")
	// "Far2l Dark" was an approximate port of the far2l theme "default dark".
	// It has been replaced by an exact one; carry existing configs over.
	if strings.EqualFold(cfg.ColorStyle, "Far2l Dark") {
		cfg.ColorStyle = "Default Dark"
	}
	cfg.Language = merged.GetString("Interface", "Language", "en")
	cfg.FallbackLanguage = merged.GetString("Interface", "FallbackLanguage", "")
	cfg.HelpLanguage = merged.GetString("Interface", "HelpLanguage", "en")
	cfg.UseLocalLanguageFiles = merged.GetString("Interface", "UseLocalLanguageFiles", "0") == "1"
	cfg.ConsoleTitleTemplate = merged.GetString("Interface", "ConsoleTitleTemplate", "f4 %Ver %Platform %Admin - %State")
	cfg.DisplayFullPathInTitle = merged.GetString("Interface", "DisplayFullPathInTitle", "0") == "1"
	cfg.AlwaysShowMenuBar = merged.GetString("Interface", "AlwaysShowMenuBar", "0") == "1"
	cfg.DialogOuterBorder = merged.GetString("Interface", "DialogOuterBorder", "0") == "1"
	cfg.SettingsMaximized = merged.GetString("Interface", "SettingsMaximized", "0") == "1"
	cfg.GlyphStyle = NormalizeGlyphStyle(merged.GetString("Interface", "GlyphStyle", GlyphStyleClassic))
	switch strings.ToLower(merged.GetString("Interface", "WorkspaceTabMode", "always")) {
	case "always":
		cfg.WorkspaceTabMode = int(vtui.WorkspaceTabsAlways)
	case "ctrl":
		cfg.WorkspaceTabMode = int(vtui.WorkspaceTabsOnCtrl)
	case "never":
		cfg.WorkspaceTabMode = int(vtui.WorkspaceTabsNever)
	case "whilectrl":
		// Shown for as long as a plain Ctrl is held (f4#1870); "ctrl" is
		// the Ctrl+Tab mode vtui#496 made of the old one.
		cfg.WorkspaceTabMode = int(vtui.WorkspaceTabsWhileCtrl)
	default:
		cfg.WorkspaceTabMode = int(vtui.WorkspaceTabsMultiple)
	}
	cfg.WorkspaceTabsOverlay = merged.GetString("Interface", "WorkspaceTabsOverlay", "1") != "0"
	cfg.CtrlTabShowsMenu = strings.EqualFold(merged.GetString("Interface", "CtrlTabMode", "direct"), "menu")
	cfg.AltNumberSwitchesTabs = merged.GetString("Interface", "AltNumberSwitchesTabs", "1") != "0"
	cfg.RestoreWorkspaceTabs = merged.GetString("Interface", "RestoreWorkspaceTabs", "1") != "0"
	cfg.WorkspaceTabNumbering = ParseWorkspaceTabNumberingMode(merged.GetString("Interface", "WorkspaceTabNumbering", "always"))
	cfg.MacKeyboard = ParseMacKeysMode(merged.GetString("Interface", "MacKeyboard", MacKeysAuto))
	if cfg.ConsoleTitleTemplate == "f4 - %State" {
		cfg.ConsoleTitleTemplate = "f4 %Ver %Platform %Admin - %State"
	}
	cfg.ShowDirPrefix = merged.GetString("Panel", "ShowDirPrefix", "0") == "1"
	cfg.ShowHighlightMarks = merged.GetString("Panel", "ShowHighlightMarks", "0") == "1"
	cfg.ShowSymlinkArrow = merged.GetString("Panel", "ShowSymlinkArrow", "0") == "1"
	cfg.SeparateFileExtensions = merged.GetString("Panel", "SeparateFileExtensions", "0") == "1"
	if mode := merged.GetString("Panel", "PanelScrollbarMode", ""); mode != "" {
		cfg.PanelScrollbarMode = ParsePanelScrollbarMode(mode)
	} else {
		// Migration from the short-lived boolean setting. When neither setting
		// exists, use the new default: the minimal scrollbar.
		switch merged.GetString("Panel", "ShowPanelScrollbars", "") {
		case "1":
			cfg.PanelScrollbarMode = PanelScrollbarFull
		case "0":
			cfg.PanelScrollbarMode = PanelScrollbarOff
		default:
			cfg.PanelScrollbarMode = PanelScrollbarMinimal
		}
	}
	cfg.ShowPanelFileInfo = merged.GetString("Panel", "ShowPanelFileInfo", "0") == "1"
	cfg.SavePanelPaths = merged.GetString("Panel", "SavePanelPaths", "1") == "1"
	cfg.DriveMenuOptions = ParseDriveMenuOptions(merged.GetString("Panel", "DriveMenuOptions", ""))
	cfg.InfoPanelBytes = merged.GetString("Panel", "InfoPanelBytes", "0") == "1"
	cfg.InfoPanelCPUGPU = merged.GetString("Panel", "InfoPanelCPUGPU", "0") == "1"
	cfg.TreeRootWholeVolume = merged.GetString("Panel", "TreeRootWholeVolume", "1") == "1"
	cfg.TreeExplorerMode = merged.GetString("Panel", "TreeExplorerMode", "0") == "1"
	cfg.DirSizeCountsFolders = merged.GetString("Panel", "DirSizeCountsFolders", "0") == "1"
	cfg.PlayerBar = merged.GetString("Panel", "PlayerBar", "1") != "0"
	cfg.PlayerSpectrum = merged.GetString("Panel", "PlayerSpectrum", "1") != "0"
	cfg.PlayerKeys = ParsePlayerKeys(merged.GetString("Panel", "PlayerKeys", PlayerKeysBoth))
	cfg.PlayerKeyHints = merged.GetString("Panel", "PlayerKeyHints", "0") == "1"
	cfg.EscTogglePanels = merged.GetString("Panel", "EscTogglePanels", "1") == "1"
	cfg.TerminalCtrlNWorkspace = merged.GetString("Panel", "TerminalCtrlNWorkspace", "1") == "1"
	cfg.KeepTerminalCursor = merged.GetString("Panel", "KeepTerminalCursor", "0") == "1"
	cfg.CursorInsertShape = NormalizeCursorShape(merged.GetString("Panel", "CursorInsertShape", ""), "underline")
	cfg.CursorOvertypeShape = NormalizeCursorShape(merged.GetString("Panel", "CursorOvertypeShape", ""), "block")
	cfg.CursorBlink = merged.GetString("Panel", "CursorBlink", "1") != "0"
	cfg.ConsoleMode = merged.GetString("Panel", "ConsoleMode", "own")
	cfg.ConsoleOverlayUI = merged.GetString("Panel", "ConsoleOverlayUI", "0") == "1"
	cfg.PluginDefaultHotkeysOff = strings.TrimSpace(merged.GetString("Panel", "PluginDefaultHotkeysOff", ""))
	cfg.DragOutModifier = NormalizeDragOutModifier(merged.GetString("Panel", "DragOutModifier", ""))
	cfg.DragOutHoldMs = NormalizeDragOutHoldMs(merged.GetString("Panel", "DragOutHoldMs", ""))
	cfg.DropOnFolder = NormalizeDropOnFolder(merged.GetString("Panel", "DropOnFolder", ""))
	cfg.HostConsoleDefaultColors = merged.GetString("Panel", "HostConsoleDefaultColors", "0") == "1"
	cfg.UseWinescape = merged.GetString("Panel", "UseWinescape", "1") != "0"
	cfg.CommandLineAutoComplete = merged.GetString("Panel", "CommandLineAutoComplete", "1") == "1"
	cfg.UsePromptFormat = merged.GetString("Panel", "UsePromptFormat", "0") == "1"
	cfg.PromptFormat = merged.GetString("Panel", "PromptFormat", "$u@$n:$p$# ")
	loadAnsiColors(cfg, func(key, def string) string { return merged.GetString("Panel", key, def) })
	loadTerminalColors(cfg, func(key, def string) string { return merged.GetString("Panel", key, def) })
	if mode := merged.GetString("Panel", "NavigationMode", ""); mode != "" {
		cfg.NavigationMode = ParsePanelNavigationMode(mode)
	} else if merged.GetString("Panel", "VimHotkeys", "0") == "1" {
		// Migration from settings written before NavigationMode was introduced.
		cfg.NavigationMode = NavigationVim
	} else {
		cfg.NavigationMode = NavigationClassic
	}
	cfg.PanelAutoFilter = merged.GetString("Panel", "PanelAutoFilter", "0") == "1"
	cfg.PanelStrictAutoFilter = merged.GetString("Panel", "PanelStrictAutoFilter", "0") == "1"
	cfg.PanelQuickSearchXLat = merged.GetString("Panel", "PanelQuickSearchXLat", "1") == "1"
	cfg.PanelGroupSmallMiB = parseGroupLimit(merged.GetString("Panel", "PanelGroupSmallMiB", "5"))
	cfg.PanelGroupMediumMiB = parseGroupLimit(merged.GetString("Panel", "PanelGroupMediumMiB", "10"))
	cfg.PanelGroupLargeMiB = parseGroupLimit(merged.GetString("Panel", "PanelGroupLargeMiB", "100"))
	if !ValidPanelGroupLimits(cfg.PanelGroupSmallMiB, cfg.PanelGroupMediumMiB, cfg.PanelGroupLargeMiB) {
		cfg.PanelGroupSmallMiB, cfg.PanelGroupMediumMiB, cfg.PanelGroupLargeMiB = 5, 10, 100
	}
	cfg.SearchCommandStayFocused = merged.GetString("Panel", "SearchCommandStayFocused", "0") == "1"
	cfg.SyncPanelLoad = merged.GetString("Panel", "SyncPanelLoad", "0") == "1"
	cfg.SearchExactOnHit = merged.GetString("Panel", "SearchExactOnHit", "0") == "1"
	cfg.ApplyCommandParallelism = runtime.NumCPU()
	_, _ = fmt.Sscanf(merged.GetString("Panel", "ApplyCommandParallelism", fmt.Sprintf("%d", runtime.NumCPU())), "%d", &cfg.ApplyCommandParallelism)
	if cfg.ApplyCommandParallelism < 0 {
		cfg.ApplyCommandParallelism = runtime.NumCPU()
	}
	_, _ = fmt.Sscanf(merged.GetString("Panel", "DefaultFileOpMode", "0"), "%d", &cfg.DefaultFileOpMode)
	cfg.QueueSmallOpsBypass = merged.GetString("Panel", "QueueSmallOpsBypass", "1") != "0"
	cfg.CopyThreads = 1
	_, _ = fmt.Sscanf(merged.GetString("Panel", "CopyThreads", "1"), "%d", &cfg.CopyThreads)
	cfg.CopyThreads = ClampCopyThreads(cfg.CopyThreads)
	cfg.ConfirmCopy = merged.GetString("System", "ConfirmCopy", "1") == "1"
	cfg.ConfirmMove = merged.GetString("System", "ConfirmMove", "1") == "1"
	cfg.ConfirmDelete = merged.GetString("System", "ConfirmDelete", "1") == "1"
	cfg.UseTrash = merged.GetString("System", "UseTrash", "0") == "1"
	cfg.ConfirmExit = merged.GetString("System", "ConfirmExit", "1") == "1"
	cfg.DeleteCancelFocused = merged.GetString("System", "DeleteCancelFocused", "0") == "1"
	legacyAutoSave := merged.GetString("System", "AutoSaveSettings", "1") != "0"
	cfg.AutoSaveSettings = legacyAutoSave
	autoSaveDefault := "0"
	if legacyAutoSave {
		autoSaveDefault = "1"
	}
	cfg.AutoSaveDialogSettings = merged.GetString("System", "AutoSaveDialogSettings", autoSaveDefault) != "0"
	cfg.AutoSavePanelSettings = merged.GetString("System", "AutoSavePanelSettings", autoSaveDefault) != "0"
	cfg.AutoSaveCurrentPanel = merged.GetString("System", "AutoSaveCurrentPanel", autoSaveDefault) != "0"
	cfg.AutoSaveGUIWindow = merged.GetString("System", "AutoSaveGUIWindow", autoSaveDefault) != "0"
	cfg.AnnounceKittyTerm = merged.GetString("System", "AnnounceKittyTerm", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("System", "MacroRecordFormat", "0"), "%d", &cfg.MacroRecordFormat)
	cfg.SystemANSICodePage = parseForcedCodePage(merged.GetString("System", "ANSICodePage", ""))
	cfg.SystemOEMCodePage = parseForcedCodePage(merged.GetString("System", "OEMCodePage", ""))
	_, _ = fmt.Sscanf(merged.GetString("Panel", "FileOpPathDisplay", "0"), "%d", &cfg.FileOpPathDisplay)
	cfg.ClipboardImageFormat = merged.GetString("ClipboardImages", "Format", "png")
	cfg.ClipboardImagePNGCompression = merged.GetString("ClipboardImages", "PNGCompression", "default")
	cfg.ClipboardImageJPEGQuality = 90
	_, _ = fmt.Sscanf(merged.GetString("ClipboardImages", "JPEGQuality", "90"), "%d", &cfg.ClipboardImageJPEGQuality)
	cfg.ClipboardImagePrefix = merged.GetString("ClipboardImages", "Prefix", "screenshot")
	cfg.ClipboardImageTemplate = merged.GetString("ClipboardImages", "Template", "!{prefix}!!{seq}!")
	cfg.ClipboardImageDigitFormat = merged.GetString("ClipboardImages", "DigitFormat", "000")
	_, _ = fmt.Sscanf(merged.GetString("Panel", "CopyAccessRights", "0"), "%d", &cfg.CopyAccessRights)
	if cfg.CopyAccessRights < 0 || cfg.CopyAccessRights > 2 {
		cfg.CopyAccessRights = 0
	}
	cfg.GuiFont = merged.GetString("Appearance", "GuiFont", "")
	cfg.GuiUseSystemMonospace = merged.GetString("Appearance", "GuiUseSystemMonospace", "1") == "1"
	defaultFontSize := DefaultGuiFontSize(runtime.GOOS)
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiFontSize", fmt.Sprintf("%d", defaultFontSize)), "%d", &cfg.GuiFontSize)
	if cfg.GuiFontSize <= 0 {
		cfg.GuiFontSize = defaultFontSize
	}
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiCols", "100"), "%d", &cfg.GuiCols)
	if cfg.GuiCols <= 0 {
		cfg.GuiCols = 100
	}
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "GuiRows", "30"), "%d", &cfg.GuiRows)
	if cfg.GuiRows <= 0 {
		cfg.GuiRows = 30
	}
	guiPosX, xErr := strconv.Atoi(merged.GetString("Appearance", "GuiPosX", ""))
	guiPosY, yErr := strconv.Atoi(merged.GetString("Appearance", "GuiPosY", ""))
	cfg.GuiPositionSaved = xErr == nil && yErr == nil
	if cfg.GuiPositionSaved {
		cfg.GuiPosX = guiPosX
		cfg.GuiPosY = guiPosY
	} else {
		cfg.GuiPosX = 0
		cfg.GuiPosY = 0
	}
	cfg.StartupMode = ParseStartupMode(merged.GetString("Startup", "Mode", "auto"))
	cfg.GuiBackend = NormalizeStartupGuiBackend(merged.GetString("Startup", "GuiBackend", ""))
	cfg.TTYBackend = NormalizeStartupTTYBackend(merged.GetString("Startup", "TTYBackend", ""))
	cfg.StartInCurrentFolder = merged.GetString("Startup", "StartInCurrentFolder", "0") == "1"
	cfg.EnforceColorCorrection = merged.GetString("Dialogs", "EnforceColorCorrection", "1") == "1"
	cfg.MenuLoopScroll = merged.GetString("VMenu", "MenuStopWrapOnEdge", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("Appearance", "HighlightPriority", "0"), "%d", &cfg.HighlightPriority)
	_, _ = fmt.Sscanf(merged.GetString("Update", "Channel", "0"), "%d", &cfg.UpdateChannel)
	_, _ = fmt.Sscanf(merged.GetString("Update", "Interval", "3"), "%d", &cfg.UpdateInterval)
	_, _ = fmt.Sscanf(merged.GetString("Update", "LastCheck", "0"), "%d", &cfg.LastUpdateCheck)
	cfg.LastUpdateVersion = merged.GetString("Update", "LastVersion", "")

	// The proxy password is stored obfuscated, exactly like netfox stores
	// site passwords; a hand-written plain one keeps working.
	_, _ = fmt.Sscanf(merged.GetString("Proxy", "Mode", "1"), "%d", &cfg.ProxyMode)
	cfg.ProxyHost = merged.GetString("Proxy", "Host", "")
	cfg.ProxyPort = merged.GetString("Proxy", "Port", "")
	cfg.ProxyUser = merged.GetString("Proxy", "User", "")
	cfg.ProxyPass = netproxy.DecodeSecret(merged.GetString("Proxy", "Password", ""))

	cfg.EditorAutoComplete = merged.GetString("Editor", "AutoComplete", "1") == "1"
	cfg.EditorAutoCompleteMask = merged.GetString("Editor", "AutoCompleteMask", "*.go;*.c;*.cpp;*.h;*.hpp;*.py;*.js;*.ts;*.rs;*.java;*.sh;*.txt;*.md;*.html;*.css;*.json")
	cfg.ArchiveTarIndexCache = merged.GetString("Panel", "ArchiveTarIndexCache", "1") == "1"
	cfg.WatchDirectories = merged.GetString("Panel", "WatchDirectories", "1") == "1"
	cfg.ArchiveUseRatarmountIfAvailable = merged.GetString("Panel", "ArchiveUseRatarmountIfAvailable", "0") == "1"
	cfg.ArchiveEnterExcludeMask = merged.GetString("Panel", "ArchiveEnterExcludeMask", "*.docx,*.docm,*.dotx,*.dotm,*.xlsx,*.xlsm,*.xltx,*.xltm,*.xlsb,*.xlam,*.pptx,*.pptm,*.potx,*.potm,*.ppam,*.ppsx,*.ppsm,*.sldx,*.sldm,*.thmx,*.odt,*.ods,*.odp,*.epub")
	cfg.ObserverEnterExcludeMask = merged.GetString("Panel", "ObserverEnterExcludeMask", "")

	cfg.EditorExpandTabs = 0
	_, _ = fmt.Sscanf(merged.GetString("Editor", "ExpandTabs", "0"), "%d", &cfg.EditorExpandTabs)
	cfg.EditorAutoIndent = merged.GetString("Editor", "AutoIndent", "1") == "1"
	cfg.EditorCursorBeyondEOL = merged.GetString("Editor", "CursorBeyondEOL", "0") == "1"
	cfg.EditorPersistentBlocks = merged.GetString("Editor", "PersistentBlocks", "0") == "1"
	cfg.EditorUseEditorConfig = merged.GetString("Editor", "UseEditorConfig", "1") == "1"
	cfg.EditorCrosshair = merged.GetString("Editor", "Crosshair", "0") == "1"
	cfg.EditorShowControlChars = merged.GetString("Editor", "ShowControlChars", "0") == "1"
	cfg.EditorMarkOccurrences = merged.GetString("Editor", "MarkOccurrences", "1") == "1"
	cfg.EditorAutodetectCodePage = merged.GetString("Editor", "AutodetectCodePage", "1") == "1"
	cfg.EditorMemoryMap = merged.GetString("Editor", "MemoryMap", "1") == "1"
	cfg.EditorHighlighter = normalizeHighlighter(merged.GetString("Editor", "Highlighter", "Chroma"))
	cfg.EditorSyntaxAnimation = merged.GetString("Editor", "SyntaxAnimation", "0") == "1"
	cfg.EditorColorerScheme = merged.GetString("Editor", "ColorerScheme", "")
	cfg.EditorColorerBackground = merged.GetString("Editor", "ColorerBackground", "1") == "1"
	cfg.EditorColorerSyntax = merged.GetString("Editor", "ColorerSyntax", "1") == "1"
	cfg.EditorColorerCatalog = merged.GetString("Editor", "ColorerCatalog", "")
	cfg.EditorColorerPairs = merged.GetString("Editor", "ColorerPairs", "1") == "1"
	cfg.EditorColorerOldOutline = merged.GetString("Editor", "ColorerOldOutline", "1") == "1"
	cfg.EditorColorerUserHrc = merged.GetString("Editor", "ColorerUserHrc", "")
	cfg.EditorColorerUserHrd = merged.GetString("Editor", "ColorerUserHrd", "")
	cfg.EditorColorerHrcSettings = merged.GetString("Editor", "ColorerHrcSettings", "")
	cfg.EditorCrossMode = ColorerCrossBoth
	_, _ = fmt.Sscanf(merged.GetString("Editor", "CrossMode", "3"), "%d", &cfg.EditorCrossMode)
	if cfg.EditorCrossMode < ColorerCrossOff || cfg.EditorCrossMode > ColorerCrossScheme {
		cfg.EditorCrossMode = ColorerCrossBoth
	}
	cfg.ViewerHighlighting = ViewerHighlightOff
	_, _ = fmt.Sscanf(merged.GetString("Viewer", "Highlighting", "0"), "%d", &cfg.ViewerHighlighting)
	if cfg.ViewerHighlighting < ViewerHighlightOff || cfg.ViewerHighlighting > ViewerHighlightAll {
		cfg.ViewerHighlighting = ViewerHighlightOff
	}
	_, _ = fmt.Sscanf(merged.GetString("Editor", "DefaultCodePage", "65001"), "%d", &cfg.EditorDefaultCodePage)
	cfg.ViewerAutodetectCodePage = merged.GetString("Viewer", "AutodetectCodePage", "1") == "1"
	_, _ = fmt.Sscanf(merged.GetString("Viewer", "DefaultCodePage", "65001"), "%d", &cfg.ViewerDefaultCodePage)
	cfg.ViewerOpenAsSupportedType = merged.GetString("Viewer", "OpenAsSupportedType", "1") == "1"

	// [Mouse] — wheel scroll speed (lines per notch), 0 = system default.
	cfg.WheelPanelUp = LoadWheelLines(merged, "PanelUp")
	cfg.WheelPanelDown = LoadWheelLines(merged, "PanelDown")
	cfg.WheelEditorUp = LoadWheelLines(merged, "EditorUp")
	cfg.WheelEditorDown = LoadWheelLines(merged, "EditorDown")
	cfg.WheelViewerUp = LoadWheelLines(merged, "ViewerUp")
	cfg.WheelViewerDown = LoadWheelLines(merged, "ViewerDown")
	cfg.WheelMenuUp = LoadWheelLines(merged, "MenuUp")
	cfg.WheelMenuDown = LoadWheelLines(merged, "MenuDown")
	cfg.WheelTableUp = LoadWheelLines(merged, "TableUp")
	cfg.WheelTableDown = LoadWheelLines(merged, "TableDown")
	// [Mouse] — the strength of the fast-spin ramp; its shape is tuned in
	// code (see internal/wheel), and the value is clamped into its range.
	cfg.WheelAcceleration = loadWheelInt(merged, "Acceleration", WheelAccelerationDefault, WheelAccelerationMin, WheelAccelerationMax)

	// [Elevation]
	cfg.ElevationIdleSeconds = DefaultElevationIdleSeconds
	_, _ = fmt.Sscanf(merged.GetString("Elevation", "IdleSeconds", strconv.Itoa(DefaultElevationIdleSeconds)), "%d", &cfg.ElevationIdleSeconds)
	if cfg.ElevationIdleSeconds < 0 {
		cfg.ElevationIdleSeconds = 0
	}

	// [PathHints]
	cfg.PathHintTimeout = 2
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "Timeout", "2"), "%d", &cfg.PathHintTimeout)
	if cfg.PathHintTimeout < 1 {
		cfg.PathHintTimeout = 1
	}
	cfg.PathHintFullPath = merged.GetString("PathHints", "FullPath", "0") == "1"
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "Source", "2"), "%d", &cfg.PathHintSource)
	cfg.PathHintMaxVisible = 5
	_, _ = fmt.Sscanf(merged.GetString("PathHints", "MaxVisible", "5"), "%d", &cfg.PathHintMaxVisible)
	if cfg.PathHintMaxVisible < 1 {
		cfg.PathHintMaxVisible = 1
	}
	cfg.PathHintPerCategory = merged.GetString("PathHints", "PerCategory", "1") == "1"
	cfg.DialogAutoComplete = merged.GetString("PathHints", "DialogAutoComplete", "1") == "1"
	cfg.HistoryShowTimes = parseHistoryShowTimes(merged.GetString("History", "ShowTimes", "0,0,0"))
	if configured := merged.GetString("History", "HistoryShowTimes", ""); configured != "" {
		cfg.HistoryShowTimes = parseHistoryShowTimes(configured)
	}
	cfg.HistoryDirsPrefixLen = 24
	_, _ = fmt.Sscanf(merged.GetString("History", "DirsPrefixLen", "24"), "%d", &cfg.HistoryDirsPrefixLen)
	if configured := merged.GetString("History", "HistoryDirsPrefixLen", ""); configured != "" {
		_, _ = fmt.Sscanf(configured, "%d", &cfg.HistoryDirsPrefixLen)
	}
	if cfg.HistoryDirsPrefixLen < 4 {
		cfg.HistoryDirsPrefixLen = 4
	}
	cfg.SlideShowDelay = DefaultSlideShowDelay
	_, _ = fmt.Sscanf(merged.GetString("Images", "SlideShowDelay", "5"), "%d", &cfg.SlideShowDelay)
	if cfg.SlideShowDelay <= 0 {
		cfg.SlideShowDelay = DefaultSlideShowDelay
	}
	cfg.ImageExternalTimeout = DefaultImageExternalTimeout
	_, _ = fmt.Sscanf(merged.GetString("Images", "ExternalTimeout", "20"), "%d", &cfg.ImageExternalTimeout)
	if cfg.ImageExternalTimeout <= 0 {
		cfg.ImageExternalTimeout = DefaultImageExternalTimeout
	}
	cfg.ImageOverlay = OverlayEnabled(merged.GetString)
	cfg.VideoPauseOnFocusLoss = merged.GetString("Video", "PauseOnFocusLoss", "0") == "1"
	cfg.ImageX11OffsetX, cfg.ImageX11OffsetY = 0, 0
	_, _ = fmt.Sscanf(merged.GetString("Images", "X11OverlayOffsetX", "0"), "%d", &cfg.ImageX11OffsetX)
	_, _ = fmt.Sscanf(merged.GetString("Images", "X11OverlayOffsetY", "0"), "%d", &cfg.ImageX11OffsetY)
	cfg.TTYXKeys = merged.GetString("TTYXi", "Keys", "1") == "1"
	cfg.TTYXKeyList = merged.GetString("TTYXi", "KeyList", DefaultTTYXKeyList)
	cfg.Compare = LoadCompareOptions(merged)
	cfg.Sync = LoadSyncOptions(merged)
	cfg.ImageDecoderPriority = merged.GetString("Images", "DecoderPriority", "")
	cfg.UseExternalEditor = merged.GetString("Editor", "UseExternalEditor", "0") == "1"
	cfg.ExternalEditorCommand = merged.GetString("Editor", "ExternalEditorCommand", "")
	cfg.ExternalEditorConsole = merged.GetString("Editor", "ExternalEditorCommandConsole", cfg.ExternalEditorCommand)
	cfg.ExternalEditorGUI = merged.GetString("Editor", "ExternalEditorCommandGUI", cfg.ExternalEditorCommand)
	plugStr := merged.GetString("Plugins", "List", "")
	if plugStr != "" {
		cfg.RegisteredPlugins = strings.Split(plugStr, "|")
	}
	cfg.EditorTabSize = 4
	_, _ = fmt.Sscanf(merged.GetString("Editor", "TabSize", "4"), "%d", &cfg.EditorTabSize)

	// [Layout] — three known keys plus round-trip storage for anything else.
	_, _ = fmt.Sscanf(merged.GetString("Layout", "WidthDecrement", "0"), "%d", &cfg.WidthDecrement)
	_, _ = fmt.Sscanf(merged.GetString("Layout", "LeftHeightDecrement", "0"), "%d", &cfg.LeftHeightDecrement)
	_, _ = fmt.Sscanf(merged.GetString("Layout", "RightHeightDecrement", "0"), "%d", &cfg.RightHeightDecrement)
	cfg.LayoutExtras = nil
	if layout, ok := merged.Sections()["Layout"]; ok {
		for k, v := range layout {
			switch k {
			case "WidthDecrement", "LeftHeightDecrement", "RightHeightDecrement":
				continue
			}
			if cfg.LayoutExtras == nil {
				cfg.LayoutExtras = make(map[string]string)
			}
			cfg.LayoutExtras[k] = v
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
	fmt.Fprintf(&sb, "DialogOuterBorder = %d\n", map[bool]int{true: 1, false: 0}[cfg.DialogOuterBorder])
	fmt.Fprintf(&sb, "SettingsMaximized = %d\n", map[bool]int{true: 1, false: 0}[cfg.SettingsMaximized])
	fmt.Fprintf(&sb, "GlyphStyle = %s\n", NormalizeGlyphStyle(cfg.GlyphStyle))
	workspaceTabMode := "multiple"
	if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsAlways) {
		workspaceTabMode = "always"
	} else if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsOnCtrl) {
		workspaceTabMode = "ctrl"
	} else if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsNever) {
		workspaceTabMode = "never"
	} else if cfg.WorkspaceTabMode == int(vtui.WorkspaceTabsWhileCtrl) {
		workspaceTabMode = "whilectrl"
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
	fmt.Fprintf(&sb, "ArchiveEnterExcludeMask = %s\n", cfg.ArchiveEnterExcludeMask)
	fmt.Fprintf(&sb, "ObserverEnterExcludeMask = %s\n", cfg.ObserverEnterExcludeMask)
	fmt.Fprintf(&sb, "ArchiveTarIndexCache = %d\n", map[bool]int{true: 1, false: 0}[cfg.ArchiveTarIndexCache])
	fmt.Fprintf(&sb, "WatchDirectories = %d\n", map[bool]int{true: 1, false: 0}[cfg.WatchDirectories])
	fmt.Fprintf(&sb, "ArchiveUseRatarmountIfAvailable = %d\n", map[bool]int{true: 1, false: 0}[cfg.ArchiveUseRatarmountIfAvailable])
	fmt.Fprintf(&sb, "ShowHiddenFiles = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowHiddenFiles])
	fmt.Fprintf(&sb, "ShowDirPrefix = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowDirPrefix])
	fmt.Fprintf(&sb, "ShowHighlightMarks = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowHighlightMarks])
	fmt.Fprintf(&sb, "ShowSymlinkArrow = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowSymlinkArrow])
	fmt.Fprintf(&sb, "SeparateFileExtensions = %d\n", map[bool]int{true: 1, false: 0}[cfg.SeparateFileExtensions])
	fmt.Fprintf(&sb, "PanelScrollbarMode = %s\n", cfg.PanelScrollbarMode.String())
	fmt.Fprintf(&sb, "ShowPanelFileInfo = %d\n", map[bool]int{true: 1, false: 0}[cfg.ShowPanelFileInfo])
	fmt.Fprintf(&sb, "SavePanelPaths = %d\n", map[bool]int{true: 1, false: 0}[cfg.SavePanelPaths])
	fmt.Fprintf(&sb, "DriveMenuOptions = %d\n", cfg.DriveMenuOptions)
	fmt.Fprintf(&sb, "InfoPanelBytes = %d\n", map[bool]int{true: 1, false: 0}[cfg.InfoPanelBytes])
	fmt.Fprintf(&sb, "InfoPanelCPUGPU = %d\n", map[bool]int{true: 1, false: 0}[cfg.InfoPanelCPUGPU])
	fmt.Fprintf(&sb, "TreeRootWholeVolume = %d\n", map[bool]int{true: 1, false: 0}[cfg.TreeRootWholeVolume])
	fmt.Fprintf(&sb, "TreeExplorerMode = %d\n", map[bool]int{true: 1, false: 0}[cfg.TreeExplorerMode])
	fmt.Fprintf(&sb, "DirSizeCountsFolders = %d\n", map[bool]int{true: 1, false: 0}[cfg.DirSizeCountsFolders])
	fmt.Fprintf(&sb, "PlayerBar = %d\n", map[bool]int{true: 1, false: 0}[cfg.PlayerBar])
	fmt.Fprintf(&sb, "PlayerSpectrum = %d\n", map[bool]int{true: 1, false: 0}[cfg.PlayerSpectrum])
	fmt.Fprintf(&sb, "PlayerKeys = %s\n", ParsePlayerKeys(cfg.PlayerKeys))
	fmt.Fprintf(&sb, "PlayerKeyHints = %d\n", map[bool]int{true: 1, false: 0}[cfg.PlayerKeyHints])
	fmt.Fprintf(&sb, "EscTogglePanels = %d\n", map[bool]int{true: 1, false: 0}[cfg.EscTogglePanels])
	fmt.Fprintf(&sb, "TerminalCtrlNWorkspace = %d\n", map[bool]int{true: 1, false: 0}[cfg.TerminalCtrlNWorkspace])
	fmt.Fprintf(&sb, "KeepTerminalCursor = %d\n", map[bool]int{true: 1, false: 0}[cfg.KeepTerminalCursor])
	fmt.Fprintf(&sb, "CursorInsertShape = %s\n", NormalizeCursorShape(cfg.CursorInsertShape, "underline"))
	fmt.Fprintf(&sb, "CursorOvertypeShape = %s\n", NormalizeCursorShape(cfg.CursorOvertypeShape, "block"))
	fmt.Fprintf(&sb, "CursorBlink = %d\n", map[bool]int{true: 1, false: 0}[cfg.CursorBlink])
	fmt.Fprintf(&sb, "ConsoleMode = %s\n", cfg.ConsoleMode)
	fmt.Fprintf(&sb, "PluginDefaultHotkeysOff = %s\n", strings.TrimSpace(cfg.PluginDefaultHotkeysOff))
	fmt.Fprintf(&sb, "DragOutModifier = %s\n", NormalizeDragOutModifier(cfg.DragOutModifier))
	fmt.Fprintf(&sb, "DragOutHoldMs = %d\n", clampDragOutHoldMs(cfg.DragOutHoldMs))
	fmt.Fprintf(&sb, "DropOnFolder = %s\n", NormalizeDropOnFolder(cfg.DropOnFolder))
	fmt.Fprintf(&sb, "ConsoleOverlayUI = %d\n", map[bool]int{true: 1, false: 0}[cfg.ConsoleOverlayUI])
	fmt.Fprintf(&sb, "HostConsoleDefaultColors = %d\n", map[bool]int{true: 1, false: 0}[cfg.HostConsoleDefaultColors])
	fmt.Fprintf(&sb, "UseWinescape = %d\n", map[bool]int{true: 1, false: 0}[cfg.UseWinescape])
	fmt.Fprintf(&sb, "CommandLineAutoComplete = %d\n", map[bool]int{true: 1, false: 0}[cfg.CommandLineAutoComplete])
	fmt.Fprintf(&sb, "UsePromptFormat = %d\n", map[bool]int{true: 1, false: 0}[cfg.UsePromptFormat])
	fmt.Fprintf(&sb, "PromptFormat = %s\n", cfg.PromptFormat)
	writeAnsiColors(&sb, cfg)
	writeTerminalColors(&sb, cfg)
	fmt.Fprintf(&sb, "NavigationMode = %s\n", cfg.NavigationMode.String())
	fmt.Fprintf(&sb, "PanelAutoFilter = %d\n", map[bool]int{true: 1, false: 0}[cfg.PanelAutoFilter])
	fmt.Fprintf(&sb, "PanelStrictAutoFilter = %d\n", map[bool]int{true: 1, false: 0}[cfg.PanelStrictAutoFilter])
	fmt.Fprintf(&sb, "PanelQuickSearchXLat = %d\n", map[bool]int{true: 1, false: 0}[cfg.PanelQuickSearchXLat])
	fmt.Fprintf(&sb, "PanelGroupSmallMiB = %d\nPanelGroupMediumMiB = %d\nPanelGroupLargeMiB = %d\n", cfg.PanelGroupSmallMiB, cfg.PanelGroupMediumMiB, cfg.PanelGroupLargeMiB)
	fmt.Fprintf(&sb, "SearchCommandStayFocused = %d\n", map[bool]int{true: 1, false: 0}[cfg.SearchCommandStayFocused])
	// Keep the legacy key synchronized for older f4 versions and shared configs.
	fmt.Fprintf(&sb, "VimHotkeys = %d\n", map[bool]int{true: 1, false: 0}[cfg.NavigationMode == NavigationVim])
	fmt.Fprintf(&sb, "SyncPanelLoad = %d\n", map[bool]int{true: 1, false: 0}[cfg.SyncPanelLoad])
	fmt.Fprintf(&sb, "SearchExactOnHit = %d\n", map[bool]int{true: 1, false: 0}[cfg.SearchExactOnHit])
	fmt.Fprintf(&sb, "ApplyCommandParallelism = %d\n", cfg.ApplyCommandParallelism)
	fmt.Fprintf(&sb, "DefaultFileOpMode = %d\n", cfg.DefaultFileOpMode)
	fmt.Fprintf(&sb, "QueueSmallOpsBypass = %d\n", map[bool]int{true: 1, false: 0}[cfg.QueueSmallOpsBypass])
	fmt.Fprintf(&sb, "CopyThreads = %d\n", cfg.CopyThreads)
	fmt.Fprintf(&sb, "FileOpPathDisplay = %d\n", cfg.FileOpPathDisplay)
	fmt.Fprintf(&sb, "CopyAccessRights = %d\n", cfg.CopyAccessRights)

	sb.WriteString("\n[ClipboardImages]\n")
	fmt.Fprintf(&sb, "Format = %s\nPNGCompression = %s\nJPEGQuality = %d\nPrefix = %s\nTemplate = %s\nDigitFormat = %s\n", cfg.ClipboardImageFormat, cfg.ClipboardImagePNGCompression, cfg.ClipboardImageJPEGQuality, cfg.ClipboardImagePrefix, cfg.ClipboardImageTemplate, cfg.ClipboardImageDigitFormat)
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

	sb.WriteString("\n[VMenu]\n")
	fmt.Fprintf(&sb, "MenuStopWrapOnEdge = %d\n", map[bool]int{true: 1, false: 0}[cfg.MenuLoopScroll])

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
	fmt.Fprintf(&sb, "StartInCurrentFolder = %d\n", map[bool]int{true: 1, false: 0}[cfg.StartInCurrentFolder])

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
	fmt.Fprintf(&sb, "PersistentBlocks = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorPersistentBlocks])
	fmt.Fprintf(&sb, "UseEditorConfig = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorUseEditorConfig])
	fmt.Fprintf(&sb, "Crosshair = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorCrosshair])
	fmt.Fprintf(&sb, "ShowControlChars = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorShowControlChars])
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
	fmt.Fprintf(&sb, "ColorerPairs = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorColorerPairs])
	fmt.Fprintf(&sb, "ColorerOldOutline = %d\n", map[bool]int{true: 1, false: 0}[cfg.EditorColorerOldOutline])
	fmt.Fprintf(&sb, "ColorerUserHrc = %s\n", cfg.EditorColorerUserHrc)
	fmt.Fprintf(&sb, "ColorerUserHrd = %s\n", cfg.EditorColorerUserHrd)
	fmt.Fprintf(&sb, "ColorerHrcSettings = %s\n", cfg.EditorColorerHrcSettings)
	fmt.Fprintf(&sb, "CrossMode = %d\n", cfg.EditorCrossMode)
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", cfg.EditorDefaultCodePage)

	sb.WriteString("\n[Viewer]\n")
	fmt.Fprintf(&sb, "AutodetectCodePage = %d\n", map[bool]int{true: 1, false: 0}[cfg.ViewerAutodetectCodePage])
	fmt.Fprintf(&sb, "DefaultCodePage = %d\n", cfg.ViewerDefaultCodePage)
	fmt.Fprintf(&sb, "OpenAsSupportedType = %d\n", map[bool]int{true: 1, false: 0}[cfg.ViewerOpenAsSupportedType])
	fmt.Fprintf(&sb, "Highlighting = %d\n", cfg.ViewerHighlighting)
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
	fmt.Fprintf(&sb, "Acceleration = %d\n", cfg.WheelAcceleration)
	sb.WriteString("\n[Elevation]\n")
	fmt.Fprintf(&sb, "IdleSeconds = %d\n", cfg.ElevationIdleSeconds)
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
	fmt.Fprintf(&sb, "Overlay = %d\n", map[bool]int{true: 1, false: 0}[cfg.ImageOverlay])
	fmt.Fprintf(&sb, "X11OverlayOffsetX = %d\n", cfg.ImageX11OffsetX)
	fmt.Fprintf(&sb, "X11OverlayOffsetY = %d\n", cfg.ImageX11OffsetY)
	// [Video] and [TTYXi] have no dialog, only settings.ini, and SaveConfig
	// replaces the whole file: a key it does not write is gone after the
	// first save.
	sb.WriteString("\n[Video]\n")
	fmt.Fprintf(&sb, "PauseOnFocusLoss = %d\n", map[bool]int{true: 1, false: 0}[cfg.VideoPauseOnFocusLoss])
	sb.WriteString("\n[TTYXi]\n")
	fmt.Fprintf(&sb, "Keys = %d\n", map[bool]int{true: 1, false: 0}[cfg.TTYXKeys])
	// The default list is left out, so that a profile keeps following it:
	// it has grown before (Ctrl+Shift+P, #980), and a written copy would
	// have frozen every existing profile at the old one.
	if cfg.TTYXKeyList != DefaultTTYXKeyList {
		fmt.Fprintf(&sb, "KeyList = %s\n", cfg.TTYXKeyList)
	}
	sb.WriteString("\n[Compare]\n")
	writeCompareOptions(&sb, cfg.Compare)
	sb.WriteString("\n[Sync]\n")
	writeSyncOptions(&sb, cfg.Sync)
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

// Bounds and default of the [Mouse] Acceleration key: a settings file that
// does not mention it ends up with the default, and so does a compiled-in
// App before any file was read. The weakest value never queues anything, so
// it is the ramp switched off; see internal/wheel for the ramp itself.
const (
	WheelAccelerationMin     = 1
	WheelAccelerationMax     = 10
	WheelAccelerationDefault = 5
)

// loadWheelInt reads an integer [Mouse] key: def when the key is missing or
// unparsable, then clamped into [min, max]. settings.ini is user text, and
// no value in it may be able to stop the panel from scrolling or make it
// scroll at nonsense speed.
func loadWheelInt(ini *ini.File, key string, def, min, max int) int {
	n := def
	_, _ = fmt.Sscanf(ini.GetString("Mouse", key, strconv.Itoa(def)), "%d", &n)
	if n < min {
		n = min
	}
	if n > max {
		n = max
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

// ApplyMenuSettings pushes the menu behaviour options into vtui.
func ApplyMenuSettings() {
	vtui.SetMenuLoopScroll(App.MenuLoopScroll)
}

// NormalizeCursorShape returns name when it is one of vtui's caret shape
// names ("underline", "bar", "block", in any letter case) and fallback
// otherwise, so a hand-edited settings.ini cannot store a shape that the
// Settings Center would then refuse as an unknown choice.
func NormalizeCursorShape(name, fallback string) string {
	if shape, ok := vtui.ParseCursorShape(strings.ToLower(strings.TrimSpace(name))); ok {
		return shape.String()
	}
	return fallback
}

// ApplyCursorSettings pushes the caret options into vtui: whether f4 manages
// the terminal cursor style at all (KeepTerminalCursor), the caret shapes for
// insert and overtype text entry, and blinking (f4 #1154).
func ApplyCursorSettings() {
	vtui.ManageCursorStyle = !App.KeepTerminalCursor
	insert, _ := vtui.ParseCursorShape(NormalizeCursorShape(App.CursorInsertShape, "underline"))
	overtype, _ := vtui.ParseCursorShape(NormalizeCursorShape(App.CursorOvertypeShape, "block"))
	vtui.SetCursorStyle(insert, overtype, App.CursorBlink)
}

// defaultHighlightIni is the highlight.ini a new profile gets: the comments that
// describe every key, and the sort groups. Everything before the first section
// is the header, which RefreshHighlightIniHeader keeps up to date in files that
// already exist (f4#912).
const defaultHighlightIni = `# User highlight rules and sort groups.
#
# f4 applies the rules of this file together with those of the active Color
# Style (Theme). Sections are tried in the order of their numbers and the first
# match wins, unless it sets ContinueProcessing = 1. Rules of this file go first;
# set Appearance.HighlightPriority in settings.ini to 1 to put the theme first.
#
# A [Highlight_N] section matches an item by its Mask, attributes, size or date.
# Name is a label, not a matcher. A section without a Mask matches every name,
# so a rule for folders needs IncludeAttributes = Directory and a rule for files
# needs ExcludeAttributes = Directory. Attributes: Directory, Hidden, Executable,
# ReadOnly, System, Archive, Symlink, Junction. Other keys: SizeAbove, SizeBelow,
# DateType, DateAfter, DateBefore, Mark.
#
# The four colors are set independently, each as a foreground, a background or
# both. A color that is omitted leaves the panel's own color for that state:
#   NormalFileName              - an ordinary item
#   SelectedFileName            - a selected item
#   FileNameUnderCursor         - an ordinary item under the cursor
#   FileNameSelectedUnderCursor - a selected item under the cursor
#
# UseDefaults = 1 turns a section into a change of the built-in colors of its
# attribute: its place in the file does not matter, it keeps the rest of the
# theme's rules, and only the colors it names are replaced. When an item has
# several attributes, the first of these decides: Junction, Symlink, Hidden or
# System, Directory. So a Directory section does not recolor symlinks or hidden
# folders, and a Symlink section does not recolor junctions.
#
# A comment takes a whole line. There are no trailing comments: '#' also opens
# a color literal, so anything after a value stays part of that value.
#
# Sort groups put files of one kind together on a panel that has "Use sort
# groups" switched on (Left/Right menu). There are two ways to define them, and
# both may be used in one file:
#
# 1. Add "Group = N" to a coloured [Highlight_N] section: its mask and
#    attributes then decide both the colour and the position, and sections
#    with the same Group number form one cluster. Remember that the first
#    matching Highlight section wins, so a Highlight section written only for
#    sorting hides the colours of the sections below it unless it also says
#    ContinueProcessing = 1.
# 2. A [SortGroup_N] section is a rule that only sorts and colours nothing;
#    the examples below are of this kind. It has the same Mask and attribute
#    keys and does not interfere with the colours.
#
# Sort groups are switched on by default for new panels. To turn them off for
# a panel, clear Left/Right menu -> "Use sort groups"; f4 remembers the choice.
# They only take effect while at least one Group is defined in this file.
#
# f4 reads this file at start: restart it after editing.
#
# Uncomment and adapt these examples. They are commented out deliberately, so
# they do not change the panel.
#
# [Highlight_100]
# Name = Archives
# Mask = *.zip, *.rar, *.7z
# ExcludeAttributes = Directory
# NormalFileName = foreground:#FF00FF | background:#000080
# SelectedFileName = foreground:#FFFF00 | background:#000080
# FileNameUnderCursor = foreground:#FF00FF | background:#008080
# FileNameSelectedUnderCursor = foreground:#FFFF00 | background:#008080
#
# [Highlight_101]
# UseDefaults = 1
# Name = Directories
# IncludeAttributes = Directory
# NormalFileName = foreground:#FF00FF | background:#000080
# SelectedFileName = foreground:#FFFF00 | background:#000080
# FileNameUnderCursor = foreground:#FF00FF | background:#008080
# FileNameSelectedUnderCursor = foreground:#FFFF00 | background:#008080
#
# [Highlight_102]
# Name = Documents (a colour and a sort group at once)
# Mask = *.pdf, *.doc, *.docx, *.odt
# ExcludeAttributes = Directory
# Group = 4
# NormalFileName = foreground:#00FFFF
#
# [SortGroup_6]
# Name = Sources (a sort group only, no colour)
# Group = 5
# Mask = *.go, *.c, *.h, *.cpp, *.py, *.pas
#
`

// CreateDefaultHighlightIni writes the stock highlight.ini.
func CreateDefaultHighlightIni(path string) {
	content := defaultHighlightIni
	_ = os.WriteFile(path, []byte(content), 0600)
	_ = os.Chmod(path, 0600)
}

// highlightIniHeader splits a highlight.ini at its first section: the lines
// before it are the header (comments and blank lines), the rest is the user's
// rules. ok is false for a file with no section at all.
func highlightIniHeader(text string) (header, rest string, ok bool) {
	pos := 0
	for pos < len(text) {
		end := strings.IndexByte(text[pos:], '\n')
		line := text[pos:]
		next := len(text)
		if end >= 0 {
			line = text[pos : pos+end]
			next = pos + end + 1
		}
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return text[:pos], text[pos:], true
		}
		pos = next
	}
	return text, "", false
}

// RefreshHighlightIniHeader rewrites the comments at the top of an existing
// highlight.ini with those of the stock file, so that a file made by an older f4
// describes the keys the current one reads (f4#912). Only the lines before the
// first section are replaced; the rules stay as they are. It reports whether the
// file was changed. A file with no section, or one it cannot write, is left alone.
func RefreshHighlightIniHeader(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	oldHeader, rest, ok := highlightIniHeader(text)
	if !ok {
		return false
	}
	newHeader, _, _ := highlightIniHeader(defaultHighlightIni)
	if oldHeader == newHeader {
		return false
	}
	// #nosec G703 -- path is the highlight.ini inside the profile directory, built by the caller.
	if err := os.WriteFile(path, []byte(newHeader+rest), 0600); err != nil {
		return false
	}
	return true
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

// NormalizeDragOutModifier maps a settings value to one of "", "ctrl", "alt"
// and "shift"; anything else means no modifier is required.
func NormalizeDragOutModifier(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case "ctrl", "alt", "shift":
		return v
	}
	return ""
}

// DropOnFolder values: where files dropped over a folder row go when the
// backend could not show the target while the files were dragged (#2049).
const (
	DropOnFolderCurrent = "current" // into the folder the panel shows
	DropOnFolderFolder  = "folder"  // into the folder under the pointer
	DropOnFolderAsk     = "ask"     // ask which of the two
)

// NormalizeDropOnFolder maps a settings value to one of the DropOnFolder
// values; anything else is the default, the folder the panel shows.
func NormalizeDropOnFolder(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case DropOnFolderFolder, DropOnFolderAsk:
		return v
	}
	return DropOnFolderCurrent
}

// DefaultDragOutHoldMs is how long the left button must stay down on a file
// before a move starts dragging it out of the panel (unxed/f4#1604).
const DefaultDragOutHoldMs = 250

// MaxDragOutHoldMs bounds DragOutHoldMs; a longer hold is no longer a hold.
const MaxDragOutHoldMs = 5000

// NormalizeDragOutHoldMs reads a DragOutHoldMs settings value: a number of
// milliseconds, 0 for "start on the first move", a negative number for "only
// once the pointer leaves the rows"; empty or unreadable means the default.
func NormalizeDragOutHoldMs(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return DefaultDragOutHoldMs
	}
	return clampDragOutHoldMs(n)
}

func clampDragOutHoldMs(n int) int {
	switch {
	case n < 0:
		return -1
	case n > MaxDragOutHoldMs:
		return MaxDragOutHoldMs
	}
	return n
}

// The values of F4Config.GlyphStyle.
const (
	GlyphStyleClassic = "classic"
	GlyphStyleRounded = "rounded"
)

// NormalizeGlyphStyle maps whatever the ini file holds to a known style; an
// unknown or empty value is the classic one.
func NormalizeGlyphStyle(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), GlyphStyleRounded) {
		return GlyphStyleRounded
	}
	return GlyphStyleClassic
}
