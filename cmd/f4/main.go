//go:generate go -C ../../tools/icons run .

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/fusefs"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// SelectedTTYBackend holds the user-chosen or auto-detected console renderer name ("ansi" or "winapi").
var SelectedTTYBackend string

// editFilePath holds the -e flag's target, if given -- opened in the editor
// once InitCore() has the panels frame ready. Package-level because the
// flag is parsed in main() but the hook point (right after the panels
// frame is pushed) lives in InitCore(), a separate function.
var editFilePath string

// openDashEFileIfRequested opens -e's target file in the editor on the
// current top PanelsFrame, if -e was given. Called from every entry point
// where SetupUI() (or InitCore(), which calls it) already ran in the one
// process that's actually going to render -- every GUI backend, and the
// tty path on Windows (session_windows.go). The Unix tty path is the odd
// one out: it daemonizes, so SetupUI() there runs inside a not-yet-
// attached background process with nothing to draw to; session_unix.go's
// runServer() calls openEditFileIn directly instead, timed to the actual
// client attach, not to this function.
func openDashEFileIfRequested() {
	if editFilePath == "" {
		return
	}
	if top := vtui.FrameManager.GetTopFrame(); top != nil {
		if pf, ok := top.(*PanelsFrame); ok && pf != nil {
			openEditFileIn(pf, editFilePath)
		}
	}
}

// openEditFileIn resolves path to an absolute path and opens it in pf's
// editor via the normal action_registry path (the same one F4/double-click
// use), so -e behaves identically to a user opening the file by hand --
// same "already open?" dialog, same history entry, same everything.
func openEditFileIn(pf *PanelsFrame, path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		vtui.DebugLog("MAIN: -e %q: filepath.Abs failed: %v", path, err)
		return
	}
	actionOpenEditor(pf, vfs.NewOSVFS(filepath.Dir(abs)), abs)
}

func sudoDispatcherPath(args []string) string {
	for i, arg := range args {
		if arg == "--sudo-dispatcher" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		if strings.HasPrefix(arg, "--sudo-dispatcher=") {
			return arg[len("--sudo-dispatcher="):]
		}
	}
	return ""
}

func sudoStartupMode(args []string, askpassParent bool) (dispatcher string, askpass bool) {
	if dispatcher = sudoDispatcherPath(args); dispatcher != "" {
		return dispatcher, false
	}
	return "", askpassParent
}

func main() {
	vtui.AppName = "f4"
	configureF4DebugLogPath(GetF4ConfigDir())
	if archivePath, archiveKind, found, err := parseUpdateHelperArgs(os.Args[1:]); found {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := runUpdateHelper(archivePath, archiveKind); err != nil {
			fmt.Fprintf(os.Stderr, "f4 update helper failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	installConsoleCtrlHandler()
	var sudoDispatcher string

	// Initialize SudoClient immediately for all process types
	execPath, err := f4Executable()
	if err != nil {
		execPath = os.Args[0]
	}
	absExecPath, _ := filepath.Abs(execPath)
	vfs.InitSudoClient(absExecPath, "")

	// The elevated dispatcher inherits F4_ASKPASS_PARENT from the client that
	// started sudo. Dispatcher mode must win over askpass mode; otherwise the
	// post-authentication root process asks for a password again instead of
	// creating its IPC socket, leaving the original operation waiting forever.
	var askpassParent bool
	sudoDispatcher, askpassParent = sudoStartupMode(os.Args[1:], os.Getenv("F4_ASKPASS_PARENT") != "")
	if sudoDispatcher != "" {
		vfs.RunSudoDispatcher(sudoDispatcher)
		return
	}

	if askpassParent {
		vfs.RunSudoAskpass()
		return
	}

	// --mount, --umount and --list-mounts are answered here and nowhere
	// else: they are a command, not a way to start the file manager. RunCLI
	// reports handled=false for every argument vector that says nothing
	// about mounting, so normal startup carries on untouched.
	//
	// The built-in plugins are loaded first, because they are what registers
	// the VFS providers: without them the registry is empty, an archive
	// resolves to a plain file and sftp:// resolves to a directory called
	// "sftp:". Nothing else of the UI is started — a mount command must not
	// build panels it will never draw.
	if code, handled := runMountCLI(); handled {
		os.Exit(code)
	}

	// Setup crash/stderr location before any logging starts; in portable mode
	// this keeps crash reports inside <configDir>\crashes (Profile\crashes).
	vtui.CrashDirFull = filepath.Join(GetF4ConfigDir(), "crashes")
	installHangDumpHandler()

	vtui.SetupStderrLog()
	redirectDetachedStdout()
	vtui.DebugLog("MAIN: Starting with args: %v", os.Args)
	LoadConfig() // Load config early to apply GUI font settings

	defer func() {
		SaveSession() // Гарантирует сохранение размеров и путей при любом выходе
		if GlobalPluginManager != nil {
			GlobalPluginManager.CloseAll()
		}
		shutdownProcessEnvironmentRuntime()
		if GlobalFileState != nil {
			GlobalFileState.Flush()
		}
		if r := recover(); r != nil {
			vtui.DebugLog("FATAL PANIC IN MAIN: %v", r)
			crashPath := vtui.RecordCrash(r, nil)
			vtui.Suspend()
			// We print to os.Stdout here because os.Stderr is redirected to the log file!
			fmt.Fprintf(os.Stdout, "\n[f4] FATAL PANIC IN MAIN: %v\n", r)
			if crashPath != "" {
				fmt.Fprintf(os.Stdout, "[f4] Crash report saved to: %s\n", crashPath)
			}
			vtui.CleanupStderrLog()
			os.Exit(2)
		}
		vtui.CleanupStderrLog()
	}()
	// Defer disk logging to prevent launcher processes from polluting rotation queue.
	// Logging will be enabled in InitCore() for workers and standalone sessions.
	vtui.ConfigDiskLogging(false)
	var serverPath, clientPath string
	var cpuprofile string
	var guiMode bool
	var guiBackend string
	var guiBackendGiven bool
	var ttyMode bool
	var ttyBackend string
	var ttyBackendGiven bool
	// startupChoiceGiven records that this run already said which renderer
	// family to use -- through --gui, --tty, or a GUI-named executable. The
	// configured startup mode only applies when it did not.
	var startupChoiceGiven bool
	var version bool
	var print_help bool
	var attachedMode bool
	var wineProbe bool
	var dumpScreenAfter float64

	exeName := filepath.Base(absExecPath)
	if strings.Contains(strings.ToLower(exeName), "gui") {
		guiMode = true
		startupChoiceGiven = true
	}

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]

		// Handle --flag=value format
		flagName := arg
		flagVal := ""
		if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
			flagName = arg[:eqIdx]
			flagVal = arg[eqIdx+1:]
		}

		switch flagName {
		case "-h", "-?", "--help":
			print_help = true
		case "-v", "--version":
			version = true
		case "--debug":
			os.Setenv("VTUI_DEBUG", "1")
		case "--gui":
			guiMode = true
			startupChoiceGiven = true
			if flagVal != "" {
				guiBackend = flagVal
				guiBackendGiven = true
			} else if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				guiBackend = os.Args[i+1]
				guiBackendGiven = true
				i++
			}
		case "--log":
			if flagVal != "" {
				os.Setenv("VTUI_DEBUG", flagVal)
			} else if i+1 < len(os.Args) {
				os.Setenv("VTUI_DEBUG", os.Args[i+1])
				i++
			}
		case "--server":
			if flagVal != "" {
				serverPath = flagVal
			} else if i+1 < len(os.Args) {
				serverPath = os.Args[i+1]
				i++
			}
		case "--client":
			if flagVal != "" {
				clientPath = flagVal
			} else if i+1 < len(os.Args) {
				clientPath = os.Args[i+1]
				i++
			}
		case "--input":
			if flagVal != "" {
				vtinput.InputMode = flagVal
			} else if i+1 < len(os.Args) {
				vtinput.InputMode = os.Args[i+1]
				i++
			}
		case "--cpuprofile":
			if flagVal != "" {
				cpuprofile = flagVal
			} else if i+1 < len(os.Args) {
				cpuprofile = os.Args[i+1]
				i++
			}
		case "--new-plugin":
			pluginName := flagVal
			if pluginName == "" && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				pluginName = os.Args[i+1]
			}
			os.Exit(RunNewPlugin(pluginName, os.Stdout, os.Stderr))
		case "-test-plugins":
			configureF4DebugLogPath(GetF4ConfigDir())
			vtui.ConfigDiskLogging(true)
			vtui.DebugLog("--- PLUGIN TEST MODE ---")
			pm := NewPluginManager()
			pm.LoadAll()
			pm.CloseAll()
			return
		case "--tty":
			ttyMode = true
			startupChoiceGiven = true
			if flagVal != "" {
				ttyBackend = flagVal
				ttyBackendGiven = true
			} else if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				ttyBackend = os.Args[i+1]
				ttyBackendGiven = true
				i++
			}
		case "--attached":
			attachedMode = true
		case "--wine-probe":
			wineProbe = true
		case "--dump-screen-after":
			// Wine's native console-input translation can drop complex
			// modifier combos (issue #536 testing: CtrlAltP for
			// Debug.ScreenDump never arrives under `wine f4.exe` tty mode,
			// though it works fine under --gui=win32). This sidesteps
			// keyboard input entirely: schedule one automatic screen dump
			// N seconds after startup instead of waiting for a hotkey that
			// may never arrive.
			val := flagVal
			if val == "" && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				val = os.Args[i+1]
				i++
			}
			if secs, err := strconv.ParseFloat(val, 64); err == nil && secs > 0 {
				dumpScreenAfter = secs
			}
		case "-e":
			// far2l-compatible: `-e [filename]` opens filename directly in
			// the editor. far2l also accepts `-e<line>[:<pos>]`, which this
			// does not implement yet -- only the filename form. Primarily
			// useful for exactly what it was added for: scripted/headless
			// testing under Wine, where interactive keyboard navigation to
			// reach a specific file can be unreliable (issue #536
			// investigation -- CtrlAltP already showed Wine's native
			// console input isn't trustworthy for automation).
			if flagVal != "" {
				editFilePath = flagVal
			} else if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				editFilePath = os.Args[i+1]
				i++
			}
		case "--sudo-dispatcher":
			// Handled before regular argument parsing; still consume its
			// separate value here so it is not interpreted as another flag.
			if flagVal == "" && i+1 < len(os.Args) {
				i++
			}
		}
	}
	configureF4DebugLogPath(GetF4ConfigDir())

	if version {
		fmt.Println(getFormattedVersionInfo())
		return
	}
	if print_help {
		fmt.Printf(`f4 version: %s
f4 is efficient and cozy two-panel file manager in go
Usage: f4 [switches]
The following switches may be used in the command line:
 -h, -?, --help         This help and exit
 -v, --version          Displays the current version and exit
 --attached             Force run in Attached-mode
 --client [clientPath]
 --cpuprofile [cpuprofile]
 --debug                Log to profile logs/debug.log (equivalent to --log=1)
 --dump-screen-after N  Auto-run Debug.ScreenDump N seconds after startup
                         (bypasses hotkeys entirely -- useful under Wine
                         tty mode, where complex combos like CtrlAltP can
                         fail to arrive through native console input)
 -e [filename]          Open filename directly in the editor on startup
                         (far2l-compatible; useful for scripted/headless
                         testing where interactive navigation is unreliable)
 --gui [Backend]        Force run in GUI-mode
                         [Backend] values: "win32" (or "winapi", "gdi"),
                         "gogpu", "ebiten", "x11", "wayland", "auto",
                         if Backend omited, the configured default is used
                         ([Startup] GuiBackend), or the most suitable one;
                         "auto" ignores the configured default for this run
 --input [InputMode]    Defines the preferred vtinput parser method;
                         [InputMode] values: "", "ansi", "ConPTY"
 --log [logfile]        If =1 or =true uses profile logs/debug.log,
                         otherwise logfile
 --new-plugin [pluginName]
 --server [serverPath]
 -test-plugins          Plugin test mode
 --tty [Backend]        Force run in TTY-mode
                         [Backend] values: "ansi", "winapi" (or "win32"),
                         "auto"; if Backend omited, the configured default
                         is used ([Startup] TTYBackend)
 --wine-probe           Print console/terminal environment facts and exit
                         (renderer backend, console geometry, shell mode)

Without --gui and --tty, f4 starts in the mode set by [Startup] Mode in
settings.ini ("auto", "tty" or "gui"); see Options > Startup settings.

Details see in build-in help (via key F1 inside f4)
and in project home: https://github.com/unxed/f4

If you want to report a problem with the program, please create bugreport
at https://github.com/unxed/f4/issues

Details about valid values of [Backend] and [logtype]
see in vtui project: https://github.com/unxed/vtui

Details about valid values of [InputMode]
see in vtinput project: https://github.com/unxed/vtinput
`,
			getFormattedVersionInfo())
		return
	}

	for _, arg := range os.Args {
		if arg == "--askpass" {
			vfs.RunSudoAskpass()
			return
		}
	}

	if serverPath != "" {
		runServer(serverPath)
		return
	}
	if clientPath != "" {
		runClient(clientPath, 0)
		return
	}
	if cpuprofile != "" {
		f, err := os.Create(cpuprofile)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	// Settings.ini supplies whatever this run did not (issue #601). The
	// startup mode and the two backends are independent: the mode says which
	// renderer family f4 opens, each backend says which renderer that family
	// uses once opened.
	if !startupChoiceGiven {
		switch AppConfig.StartupMode {
		case StartupModeTTY:
			ttyMode = true
		case StartupModeGui:
			guiMode = true
		}
	}
	guiBackendFromConfig := !guiBackendGiven && normalizeStartupGuiBackend(AppConfig.GuiBackend) != ""
	guiBackend = resolveStartupBackend(guiBackend, guiBackendGiven, AppConfig.GuiBackend, normalizeStartupGuiBackend)
	ttyBackend = resolveStartupBackend(ttyBackend, ttyBackendGiven, AppConfig.TTYBackend, normalizeStartupTTYBackend)
	vtui.DebugLog("MAIN: startup mode=%s guiMode=%v ttyMode=%v guiBackend=%q ttyBackend=%q",
		AppConfig.StartupMode, guiMode, ttyMode, guiBackend, ttyBackend)

	if ttyBackend != "" {
		SelectedTTYBackend = ttyBackend
	} else {
		SelectedTTYBackend = vtui.DefaultConsoleBackend()
	}
	configureNestedInputMode()

	// The probe runs after backend selection (so it can report what f4 would
	// really use) and before any renderer exists (so it cannot disturb the
	// console it is describing).
	if wineProbe {
		runWineProbe()
		return
	}

	if dumpScreenAfter > 0 {
		delay := time.Duration(dumpScreenAfter * float64(time.Second))
		time.AfterFunc(delay, func() {
			actionScreenDump()
		})
	}

	if ttyMode {
		ManageSessions()
		return
	}

	if guiMode {
		checkAndDetach(attachedMode)
		if err := runGuiBackend(guiBackend, guiBackendFromConfig); err != nil {
			fmt.Fprintf(os.Stderr, "\n[f4] FATAL GUI ERROR: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Default auto-detect mode (neither --gui nor --tty specified)
	if shouldTryGui() {
		checkAndDetach(attachedMode)
		if err := runGuiBackend(guiBackend, guiBackendFromConfig); err != nil {
			vtui.DebugLog("MAIN: GUI auto-detect failed after detach: %v", err)
			os.Exit(1)
		}
		return
	}

	vtui.DebugLog("MAIN: Falling back to console mode")
	ManageSessions()
}

// runGuiBackend starts the GUI on a named backend, or on the best available
// one when the name is empty.
//
// A backend that came from settings.ini is a preference, not an instruction:
// when it fails -- a static build without FFI, a machine whose GPU stack is
// gone, a display server that is not running -- f4 falls back to automatic
// selection rather than refusing to start over a setting the user may have
// saved on a different machine. A backend named on the command line keeps the
// strict behavior, because there the user asked for that one and is watching.
func runGuiBackend(backend string, fromConfig bool) error {
	if backend == "" {
		return tryRunDefaultGui()
	}
	err := RunGui(backend)
	if err == nil || !fromConfig {
		return err
	}
	vtui.DebugLog("MAIN: configured GUI backend %q failed (%v), falling back to automatic selection", backend, err)
	if fallbackErr := tryRunDefaultGui(); fallbackErr != nil {
		return fmt.Errorf("configured GUI backend %q failed: %v; automatic selection also failed: %v", backend, err, fallbackErr)
	}
	return nil
}

func shouldTryGui() bool {
	if vtui.IsWine() {
		// Under Wine, default to console mode (wineconsole / terminal).
		// Win32 GUI mode is available via --gui=win32, --gui, or f4-gui.exe.
		return false
	}
	if runtime.GOOS == "windows" {
		// On native Windows, we compile separate binaries for console (f4.exe) and GUI (f4-gui.exe).
		// We do not auto-detect GUI mode; it must be requested via filename or --gui flag.
		return false
	}
	if runtime.GOOS == "darwin" {
		return true
	}
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

func tryRunDefaultGui() error {
	if vtui.IsWine() {
		vtui.DebugLog("GUI_AUTO: Under Wine, trying win32 GUI backend...")
		if err := RunGui("win32"); err == nil {
			return nil
		}
	}
	var errs []string
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {

		// Windows: try native win32 (GDI) first as the primary, lightweight, cgo-free backend.
		if runtime.GOOS == "windows" {
			vtui.DebugLog("GUI_AUTO: Trying win32...")
			if err := RunGui("win32"); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("win32: %v", err))
			}

			vtui.DebugLog("GUI_AUTO: Trying ebiten...")
			if err := RunGui("ebiten"); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("ebiten: %v", err))
			}
		}

		// Try gogpu (macOS default; Windows fallback)
		vtui.DebugLog("GUI_AUTO: Trying gogpu...")
		if err := RunGui("gogpu"); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("gogpu: %v", err))
		}

		// Fallback to X11 if DISPLAY environment variable is set
		if os.Getenv("DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying x11...")
			if err := RunGui("x11"); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("x11: %v", err))
			}
		}
	} else {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying wayland...")
			if err := RunGui("wayland"); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("wayland: %v", err))
			}
		}
		if os.Getenv("DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying x11...")
			if err := RunGui("x11"); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("x11: %v", err))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("all GUI backends failed: %s", strings.Join(errs, "; "))
	}
	return fmt.Errorf("no suitable GUI environment detected")
}

func InitCore() *vtui.ScreenBuf {
	// Environment Diagnostics
	vtui.DebugLog("ENV: OS=%s ARCH=%s", runtime.GOOS, runtime.GOARCH)
	if wt := os.Getenv("WT_SESSION"); wt != "" {
		vtui.DebugLog("ENV: Running inside Windows Terminal (WT_SESSION set)")
	}
	if term := os.Getenv("TERM"); term != "" {
		vtui.DebugLog("ENV: TERM=%s", term)
	}
	width, height, err := vtui.GetTerminalSize()
	if err != nil {
		vtui.DebugLog("CORE: term.GetSize(0) failed: %v", err)
	}
	if p := probeConsole(); true {
		vtui.DebugLog("ENV: wine=%v backend=%q size=%dx%d consoleBuffer=%v window=%dx%d",
			vtui.IsWine(), SelectedTTYBackend, width, height, p.OK, p.WinCols(), p.WinRows())
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	scr := vtui.NewScreenBuf()
	if SelectedTTYBackend == "winapi" || SelectedTTYBackend == "win32" {
		scr.Renderer = vtui.NewWin32ConsoleRenderer(scr)
	}
	scr.AllocBuf(width, height)

	vtui.FrameManager.Init(scr)

	SetupUI()

	vtui.DebugLog("CORE: Initialization complete")
	return scr
}

func SetupUI() {
	configureUnicodeInput()
	vtui.ConfigDiskLogging(os.Getenv("VTUI_DEBUG") != "")
	vtui.DebugLog("=== F4 STARTUP [%s] PID:%d ===", getFormattedVersionInfo(), os.Getpid())

	SetDefaultF4Palette()
	LoadConfig()
	applyWheelSettings()
	vtui.PathHintProvider = pathHintProvider
	applyPathHintSettings()
	ctrlTabMode := vtui.WorkspaceCtrlTabDirect
	if AppConfig.CtrlTabShowsMenu {
		ctrlTabMode = vtui.WorkspaceCtrlTabMenu
	}
	vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabMode(AppConfig.WorkspaceTabMode), ctrlTabMode)
	vtui.FrameManager.ConfigureWorkspaceTabOverlay(AppConfig.WorkspaceTabsOverlay)
	vtui.FrameManager.ConfigureWorkspaceAltNumberSwitch(AppConfig.AltNumberSwitchesTabs)
	InitLang()
	if err := ApplyColorStyle(AppConfig.ColorStyle); err != nil {
		vtui.DebugLog("COLORS: %v; falling back to Modern", err)
		AppConfig.ColorStyle = "Modern"
		_ = ApplyColorStyle(AppConfig.ColorStyle)
	}
	vtui.GlobalHistoryProvider = NewF4HistoryProvider()
	GlobalFileState = NewF4FileStateProvider()
	vtinput.Logger = vtui.DebugLog // Pipe vtinput logs to vtui's debug logger
	vtui.GlobalClipboardAccessManager = NewF4ClipboardAuth()
	// RegisterDrive("Null VFS", func() vfs.VFS { return vfs.NewNullVFS(50 * 1024 * 1024) }) // 50 MB/s

	configDir := GetF4ConfigDir()

	// Initialize File Highlighting
	highlightPath := filepath.Join(configDir, "highlight.ini")
	if _, err := os.Stat(highlightPath); os.IsNotExist(err) {
		createDefaultHighlightIni(highlightPath)
	}
	if _, err := os.Stat(highlightPath); err == nil {
		highlightIni := LoadIni(highlightPath)
		GlobalFileHighlighter.LoadFromIni(highlightIni)
		// Sort groups share the file (and the rule syntax) with highlighting,
		// the way far keeps both in one dialog. Themes may not define them.
		GlobalSortGroups.LoadFromIni(highlightIni)
	}

	// CrashDirFull задаётся рано (см. main()); здесь только повторная
	// синхронизация для vfs, чтобы конфиг портативного режима был единым.
	vfs.CustomConfigDir = configDir

	os.MkdirAll(configDir, 0755)
	GlobalHotkeysMgr = NewHotkeyManager(filepath.Join(configDir, "hotkeys.ini"))
	keymapPath := filepath.Join(configDir, "keymap.ini")
	if _, err := os.Stat(keymapPath); os.IsNotExist(err) {
		// The file is the documentation: a user fighting a multiplexer has to
		// find it in the profile without knowing it exists first.
		createDefaultKeymapIni(keymapPath)
	}
	GlobalKeyRemap = NewKeyRemap(keymapPath)
	MacroMgr = NewMacroManager(filepath.Join(configDir, "key_macros.ini"))
	MacroMgr.LoadLuaMacros(filepath.Join(configDir, "Macros", "scripts"))
	// Help is initialized after the hotkey manager: key binding topics
	// are generated from the action registry and must reflect the
	// user's overrides from hotkeys.ini.
	InitHelpSystem()
	vtui.FrameManager.EventFilter = MacroMgr.Filter

	pluginsDisabled := false
	for _, arg := range os.Args {
		if arg == "--no-plugins" {
			pluginsDisabled = true
			break
		}
	}
	if !pluginsDisabled {
		GlobalPluginManager = NewPluginManager()
		// Built-ins only register local capabilities and must be ready before
		// LoadSession restores provider-owned visual panel paths.
		GlobalPluginManager.LoadInternal()
	} else {
		GlobalPluginManager = nil
		vtui.DebugLog("CORE: Plugins disabled by --no-plugins flag")
	}

	LoadSession()
	vtui.ManageCursorStyle = !AppConfig.KeepTerminalCursor
	vtui.FrameManager.Push(vtui.NewDesktop())

	width := vtui.FrameManager.GetScreenSize()
	height := vtui.FrameManager.GetScreenHeight()

	panels := NewPanelsFrame()
	panels.ResizeConsole(width, height)
	states, activeWorkspace := workspaceSessionsForRestore(
		LastWorkspaceSessions, LastActiveWorkspace, AppConfig.RestoreWorkspaceTabs,
	)
	if len(states) == 0 && AppConfig.SavePanelPaths {
		states = []workspaceSessionState{legacyWorkspaceSession()}
	}
	if len(states) > 0 {
		applyWorkspaceSession(panels, states[0], width, height, AppConfig.SavePanelPaths)
	}
	vtui.FrameManager.Push(panels)
	if len(states) > 1 {
		// AddScreenBackground inserts immediately after the active workspace;
		// restore from right to left to preserve the saved tab order.
		for i := len(states) - 1; i >= 1; i-- {
			state := states[i]
			extra := NewPanelsFrame()
			applyWorkspaceSession(extra, state, width, height, AppConfig.SavePanelPaths)
			vtui.FrameManager.AddScreenBackground(extra)
		}
	}
	if len(states) > 0 {
		if AppConfig.WorkspaceTabNumbering == WorkspaceTabNumbersAlways {
			numbers := make([]int, len(states))
			for i, state := range states {
				numbers[i] = state.Number
			}
			vtui.FrameManager.RestoreScreenNumbers(numbers)
		} else {
			renumberWorkspaceScreens()
		}
		if activeWorkspace > 0 && activeWorkspace < len(vtui.FrameManager.Screens) {
			vtui.FrameManager.SwitchScreen(activeWorkspace)
		}
	}
	previousEventFilter := vtui.FrameManager.EventFilter
	vtui.FrameManager.EventFilter = func(e *vtinput.InputEvent) bool {
		if handleTranslatorMouseEvent(e) {
			return true
		}
		if previousEventFilter != nil && previousEventFilter(e) {
			return true
		}
		if handleForcedMouseSelectionEvent(e) {
			return true
		}
		if handleMenuHistoryEvent(e) {
			return true
		}
		if handlePanelPathEditHotkey(e) {
			return true
		}
		if handleHelpSearchHotkey(e) {
			return true
		}
		if panels.shellMode == ShellModeSimpleInline && panels.consoleViewActive() && panels.isTopFrame() {
			if e.Type == vtinput.KeyEventType && e.KeyDown {
				vtui.FrameManager.PostTask(func() {
					panels.drawConsoleOverlay()
				})
			}
		}
		return false
	}

	vtui.FrameManager.MenuBar = panels.menuBar
	vtui.FrameManager.KeyBar = panels.keyBar
	// consoleOverlayOwnedScreen tracks whether panels was the top frame the
	// last time OnRender checked, so a foreign frame (editor/viewer/dialog
	// opened via F3/F4 or similar while the console view is showing) giving
	// the screen back to panels can be told apart from panels having simply
	// stayed on top the whole time. Only the former needs a full background
	// repaint: panels.Busy stays true across that whole round trip (it is
	// what keeps the console view free of a full panels/keybar flush while
	// some other frame owns the screen), so once panels is back on top,
	// renderPhase()'s busy gate blocks any further full redraw -- without an
	// explicit clearConsoleViewBackground() here, whatever the foreign frame
	// last drew stays frozen under nothing but the two freshly-painted
	// overlay rows.
	consoleOverlayOwnedScreen := true
	vtui.FrameManager.OnRender = func(scr *vtui.ScreenBuf) {
		if AppConfig.WorkspaceTabNumbering == WorkspaceTabNumbersOrder {
			renumberWorkspaceScreens()
		}
		UpdateWindowTitle(scr)
		renderHelpSearch(scr)
		if panels.shellMode == ShellModeSimpleInline && panels.consoleViewActive() {
			onTop := panels.isTopFrame()
			if onTop {
				if !consoleOverlayOwnedScreen {
					clearConsoleViewBackground(panels.lastW, panels.lastH)
				}
				panels.drawConsoleOverlay()
			}
			consoleOverlayOwnedScreen = onTop
		}
	}

	// External plugins may post a permission dialog or call Host.RunAction
	// during Init. Start them only after session restoration and initial frame
	// construction, and never wait for them before the UI event loop starts.
	if GlobalPluginManager != nil {
		GlobalPluginManager.StartExternal()
	}

	// Background update check
	if AppConfig.UpdateInterval > 0 {
		go CheckForUpdates(panels, false)
		go CheckForPluginUpdates()
	}
}

// configureUnicodeInput enables the full grapheme-aware visual caret mode
// for every f4 input surface. zoin-bot keeps this explicit at the application
// boundary so vtui remains backwards-compatible for other applications.
func configureUnicodeInput() {
	vtui.DefaultBidiMode = vtui.BidiFull
}

// nestedInputMode selects the reader for f4 launched inside its own terminal.
// Windows ConPTY converts SGR mouse bytes sent to a native console reader into
// lossy MOUSE_EVENT records, so a nested f4 must parse the bytes directly.
// An explicit --input choice always wins, and Unix keeps its normal reader.
func nestedInputMode(explicit string, nested, windows bool) string {
	if explicit != "" || !nested || !windows {
		return explicit
	}
	return "ansi"
}

func configureNestedInputMode() {
	nested := os.Getenv("F4_NESTED") != ""
	vtinput.InputMode = nestedInputMode(vtinput.InputMode, nested, runtime.GOOS == "windows")
	if nested && runtime.GOOS == "windows" && vtinput.InputMode == "ansi" {
		vtui.DebugLog("INPUT: nested f4 uses ANSI reader to preserve ConPTY mouse buttons")
	}
}

var getSessionIniPath = func() string {
	return filepath.Join(GetF4ConfigDir(), "session.ini")
}

// sessionLoaded marks the process that read the session and may write it back.
// LoadSession runs only from SetupUI, but main's defer reaches SaveSession from
// processes with no UI at all -- the session client, --version, --help -- whose
// Last* globals are defaults that would overwrite the daemon's file.
var sessionLoaded bool

func LoadSession() {
	sessionLoaded = true
	path := getSessionIniPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}
	ini := LoadIni(path)

	LastEditorSearch = ini.GetString("EditorSearch", "Pattern", "")
	LastEditorReplace = ini.GetString("EditorSearch", "Replace", "")
	LastEditorSearchCase = ini.GetString("EditorSearch", "CaseSensitive", "0") == "1"
	LastEditorSearchReverse = ini.GetString("EditorSearch", "Reverse", "0") == "1"
	LastEditorSearchRegexp = ini.GetString("EditorSearch", "Regexp", "0") == "1"
	LastEditorSearchWholeWord = ini.GetString("EditorSearch", "WholeWord", "0") == "1"

	LastFindFileMask = ini.GetString("FindFile", "Mask", "*")
	LastFindFileText = ini.GetString("FindFile", "Text", "")
	LastFindFileCaseSensitive = ini.GetString("FindFile", "CaseSensitive", "0") == "1"
	LastFindFileWholeWords = ini.GetString("FindFile", "WholeWords", "0") == "1"
	LastFindFileRegexp = ini.GetString("FindFile", "Regexp", "0") == "1"
	LastFindFileNotContaining = ini.GetString("FindFile", "NotContaining", "0") == "1"
	LastFindFileFolders = ini.GetString("FindFile", "Folders", "0") == "1"
	LastFindFileSymlinks = ini.GetString("FindFile", "Symlinks", "0") == "1"

	// Восстанавливаем состояние левой панели
	LastLeftPath = ini.GetString("Panel/Left", "Folder", "")
	LastLeftCursor = ini.GetString("Panel/Left", "CurFile", "")
	fmt.Sscanf(ini.GetString("Panel/Left", "ViewMode", "0"), "%d", &LastLeftViewMode)
	fmt.Sscanf(ini.GetString("Panel/Left", "SortMode", "0"), "%d", &LastLeftSortMode)
	LastLeftSortRev = ini.GetString("Panel/Left", "SortReverse", "0") == "1"
	LastLeftSortGroups = ini.GetString("Panel/Left", "UseSortGroups", "0") == "1"

	// Восстанавливаем состояние правой панели
	LastRightPath = ini.GetString("Panel/Right", "Folder", "")
	LastRightCursor = ini.GetString("Panel/Right", "CurFile", "")
	fmt.Sscanf(ini.GetString("Panel/Right", "ViewMode", "0"), "%d", &LastRightViewMode)
	fmt.Sscanf(ini.GetString("Panel/Right", "SortMode", "0"), "%d", &LastRightSortMode)
	LastRightSortRev = ini.GetString("Panel/Right", "SortReverse", "0") == "1"
	LastRightSortGroups = ini.GetString("Panel/Right", "UseSortGroups", "0") == "1"

	// Восстанавливаем глобальное состояние сессии
	activeStr := ini.GetString("Session", "ActivePanel", "1")
	fmt.Sscanf(activeStr, "%d", &LastActivePanel)
	LastWidePanel = -1
	fmt.Sscanf(ini.GetString("Session", "WidePanel", "-1"), "%d", &LastWidePanel)
	if LastWidePanel < -1 || LastWidePanel > 1 {
		LastWidePanel = -1
	}
	LastShowPanels = ini.GetString("Session", "ShowPanels", "1") == "1"
	LastShowLeft = ini.GetString("Session", "ShowLeft", "1") == "1"
	LastShowRight = ini.GetString("Session", "ShowRight", "1") == "1"
	LastWorkspaceSessions, LastActiveWorkspace = loadWorkspaceSessions(ini)

	vtui.DebugLog("SESSION: Loaded state from %s", path)
}

func SaveSession() {
	if !sessionLoaded {
		vtui.DebugLog("SESSION: State was never loaded, nothing to save")
		return
	}
	if !AppConfig.AutoSaveSettings {
		vtui.DebugLog("SESSION: Automatic saving is disabled")
		return
	}
	saveSessionWithOptions(AppConfig.AutoSavePanelSettings, AppConfig.AutoSaveCurrentPanel, AppConfig.AutoSaveGUIWindow)
}

func saveSessionWithOptions(savePanelSettings, saveCurrentPanel, saveGUIWindow bool) {
	if !savePanelSettings && !saveCurrentPanel && !saveGUIWindow && !AppConfig.AutoSaveDialogSettings {
		return
	}
	path := getSessionIniPath()
	if saveGUIWindow {
		windowChanged := captureCurrentWindowSize()
		positionChanged := captureCurrentWindowPosition()
		if windowChanged || positionChanged {
			if AppConfig.AutoSaveDialogSettings {
				saveConfigWithWindowSize(true)
			} else {
				saveGuiWindowSize()
			}
		}
	} else if AppConfig.AutoSaveDialogSettings {
		// Flush a pending settings-dialog change at shutdown without replacing
		// the last GUI geometry when that group is disabled.
		saveConfigWithWindowSize(false)
	}

	if savePanelSettings || saveCurrentPanel {
		saveSessionFileWithOptions(path, savePanelSettings, saveCurrentPanel)
	}
}

func captureCurrentWindowSize() bool {
	if !shouldPersistGUIWindowSize(vtui.ActiveBackend()) || vtui.FrameManager == nil {
		return false
	}
	w := vtui.FrameManager.GetScreenSize()
	h := vtui.FrameManager.GetScreenHeight()
	if w <= 0 || h <= 0 || (AppConfig.GuiCols == w && AppConfig.GuiRows == h) {
		return false
	}
	AppConfig.GuiCols = w
	AppConfig.GuiRows = h
	return true
}

func captureCurrentWindowPosition() bool {
	if !shouldPersistGUIWindowSize(vtui.ActiveBackend()) || vtui.FrameManager == nil {
		return false
	}
	x, y, ok := vtui.GetWindowPosition()
	if !ok || (AppConfig.GuiPositionSaved && AppConfig.GuiPosX == x && AppConfig.GuiPosY == y) {
		return false
	}
	AppConfig.GuiPosX = x
	AppConfig.GuiPosY = y
	AppConfig.GuiPositionSaved = true
	return true
}

func mergeWorkspaceSessionSave(previous []workspaceSessionState, previousActive int, current []workspaceSessionState, currentActive int, savePanelSettings, saveCurrentPanel bool) ([]workspaceSessionState, int) {
	if len(previous) == 0 || (savePanelSettings && saveCurrentPanel) {
		return current, currentActive
	}
	merged := append([]workspaceSessionState(nil), previous...)
	findPrevious := func(state workspaceSessionState, index int) int {
		for i := range merged {
			if state.Number != 0 && merged[i].Number == state.Number {
				return i
			}
		}
		if index < len(merged) {
			return index
		}
		return -1
	}
	for index, state := range current {
		previousIndex := findPrevious(state, index)
		if previousIndex < 0 {
			if savePanelSettings {
				merged = append(merged, state)
			}
			continue
		}
		if savePanelSettings {
			paths := merged[previousIndex].Left.Path
			leftCursor := merged[previousIndex].Left.Cursor
			rightPath := merged[previousIndex].Right.Path
			rightCursor := merged[previousIndex].Right.Cursor
			merged[previousIndex] = state
			merged[previousIndex].Left.Path = paths
			merged[previousIndex].Left.Cursor = leftCursor
			merged[previousIndex].Right.Path = rightPath
			merged[previousIndex].Right.Cursor = rightCursor
		}
		if saveCurrentPanel {
			merged[previousIndex].Left.Path = state.Left.Path
			merged[previousIndex].Left.Cursor = state.Left.Cursor
			merged[previousIndex].Right.Path = state.Right.Path
			merged[previousIndex].Right.Cursor = state.Right.Cursor
		}
	}
	if savePanelSettings {
		return merged, currentActive
	}
	return merged, previousActive
}

func saveSessionFile(path string) {
	saveSessionFileWithOptions(path, true, true)
}

func saveSessionFileWithOptions(path string, savePanelSettings, saveCurrentPanel bool) {
	os.MkdirAll(filepath.Dir(path), 0755)

	if vtui.FrameManager != nil {
		if states, active := captureWorkspaceSessions(); len(states) > 0 {
			states, active = mergeWorkspaceSessionSave(LastWorkspaceSessions, LastActiveWorkspace, states, active, savePanelSettings, saveCurrentPanel)
			if !AppConfig.SavePanelPaths {
				for i := range states {
					states[i].Left.Path, states[i].Right.Path = "", ""
					states[i].Left.Cursor, states[i].Right.Cursor = "", ""
				}
			}
			LastWorkspaceSessions, LastActiveWorkspace = states, active
			if AppConfig.SavePanelPaths {
				setLegacyWorkspaceSession(states[0])
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("[EditorSearch]\n")
	fmt.Fprintf(&sb, "Pattern = %s\n", LastEditorSearch)
	fmt.Fprintf(&sb, "Replace = %s\n", LastEditorReplace)
	fmt.Fprintf(&sb, "CaseSensitive = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchCase])
	fmt.Fprintf(&sb, "Reverse = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchReverse])
	fmt.Fprintf(&sb, "Regexp = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchRegexp])
	fmt.Fprintf(&sb, "WholeWord = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchWholeWord])

	sb.WriteString("\n[FindFile]\n")
	fmt.Fprintf(&sb, "Mask = %s\n", LastFindFileMask)
	fmt.Fprintf(&sb, "Text = %s\n", LastFindFileText)
	fmt.Fprintf(&sb, "CaseSensitive = %d\n", boolToCheckboxState(LastFindFileCaseSensitive))
	fmt.Fprintf(&sb, "WholeWords = %d\n", boolToCheckboxState(LastFindFileWholeWords))
	fmt.Fprintf(&sb, "Regexp = %d\n", boolToCheckboxState(LastFindFileRegexp))
	fmt.Fprintf(&sb, "NotContaining = %d\n", boolToCheckboxState(LastFindFileNotContaining))
	fmt.Fprintf(&sb, "Folders = %d\n", boolToCheckboxState(LastFindFileFolders))
	fmt.Fprintf(&sb, "Symlinks = %d\n", boolToCheckboxState(LastFindFileSymlinks))

	sb.WriteString("\n[Session]\n")
	fmt.Fprintf(&sb, "ActivePanel = %d\n", LastActivePanel)
	fmt.Fprintf(&sb, "WidePanel = %d\n", LastWidePanel)
	fmt.Fprintf(&sb, "ShowPanels = %d\n", map[bool]int{true: 1, false: 0}[LastShowPanels])
	fmt.Fprintf(&sb, "ShowLeft = %d\n", map[bool]int{true: 1, false: 0}[LastShowLeft])
	fmt.Fprintf(&sb, "ShowRight = %d\n", map[bool]int{true: 1, false: 0}[LastShowRight])

	sb.WriteString("\n[Panel/Left]\n")
	fmt.Fprintf(&sb, "Folder = %s\n", LastLeftPath)
	fmt.Fprintf(&sb, "CurFile = %s\n", LastLeftCursor)
	fmt.Fprintf(&sb, "ViewMode = %d\n", LastLeftViewMode)
	fmt.Fprintf(&sb, "SortMode = %d\n", LastLeftSortMode)
	fmt.Fprintf(&sb, "SortReverse = %d\n", map[bool]int{true: 1, false: 0}[LastLeftSortRev])
	fmt.Fprintf(&sb, "UseSortGroups = %d\n", map[bool]int{true: 1, false: 0}[LastLeftSortGroups])

	sb.WriteString("\n[Panel/Right]\n")
	fmt.Fprintf(&sb, "Folder = %s\n", LastRightPath)
	fmt.Fprintf(&sb, "CurFile = %s\n", LastRightCursor)
	fmt.Fprintf(&sb, "ViewMode = %d\n", LastRightViewMode)
	fmt.Fprintf(&sb, "SortMode = %d\n", LastRightSortMode)
	fmt.Fprintf(&sb, "SortReverse = %d\n", map[bool]int{true: 1, false: 0}[LastRightSortRev])
	fmt.Fprintf(&sb, "UseSortGroups = %d\n", map[bool]int{true: 1, false: 0}[LastRightSortGroups])
	writeWorkspaceSessions(&sb, LastWorkspaceSessions, LastActiveWorkspace)

	err := os.WriteFile(path, []byte(sb.String()), 0600)
	if err != nil {
		vtui.DebugLog("SESSION: Failed to save state: %v", err)
		return
	}
	_ = os.Chmod(path, 0600)

	vtui.DebugLog("SESSION: Saved state to %s", path)
}

func shouldPersistGUIWindowSize(backend string) bool {
	return backend != ""
}

func getFormattedVersionInfo() string {
	return getLongVersionInfo()
}

func formatVersionSHA(v string) string {
	runes := []rune(v)
	var res []rune
	i := 0
	for i < len(runes) {
		if i+8 <= len(runes) && isHexSequence(runes[i:i+8]) {
			isStandalone := true
			if i > 0 && isHexChar(runes[i-1]) {
				isStandalone = false
			}
			if i+8 < len(runes) && isHexChar(runes[i+8]) {
				isStandalone = false
			}
			if isStandalone {
				res = append(res, runes[i:i+7]...)
				i += 8
				continue
			}
		}
		res = append(res, runes[i])
		i++
	}
	return string(res)
}

func isHexSequence(s []rune) bool {
	for _, r := range s {
		if !isHexChar(r) {
			return false
		}
	}
	return true
}

func isHexChar(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

// runMountCLI answers the mount command line, with the VFS providers loaded
// and nothing else. It is separate from InitCore because a command needs the
// providers and none of the terminal, the session or the panels.
func runMountCLI() (int, bool) {
	if cmd, _, _ := fusefs.ParseArgs(os.Args); cmd == fusefs.CmdNone {
		return 0, false
	}
	GlobalPluginManager = NewPluginManager()
	GlobalPluginManager.LoadInternal()
	return fusefs.RunCLI(os.Args)
}
