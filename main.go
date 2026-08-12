//go:generate go -C tools/icons run .

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func main() {
	vtui.AppName = "f4"
	installConsoleCtrlHandler()
	var sudoDispatcher string

	// Initialize SudoClient immediately for all process types
	execPath, err := os.Executable()
	if err != nil {
		execPath = os.Args[0]
	}
	absExecPath, _ := filepath.Abs(execPath)
	vfs.InitSudoClient(absExecPath, "")

	if os.Getenv("F4_ASKPASS_PARENT") != "" {
		vfs.RunSudoAskpass()
		return
	}

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--sudo-dispatcher" {
			if i+1 < len(os.Args) {
				sudoDispatcher = os.Args[i+1]
			}
			break
		} else if strings.HasPrefix(arg, "--sudo-dispatcher=") {
			sudoDispatcher = arg[len("--sudo-dispatcher="):]
			break
		}
	}
	if sudoDispatcher != "" {
		vfs.RunSudoDispatcher(sudoDispatcher)
		return
	}

	// Setup crash/stderr location before any logging starts; in portable mode
	// this keeps crash reports inside <configDir>\crashes (Profile\crashes).
	vtui.CrashDirFull = filepath.Join(GetF4ConfigDir(), "crashes")

	vtui.SetupStderrLog()
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
	var ttyMode bool
	var version bool
	var attachedMode bool

	exeName := filepath.Base(absExecPath)
	if strings.Contains(strings.ToLower(exeName), "gui") {
		guiMode = true
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
		case "-v", "--version":
			version = true
		case "--debug":
			os.Setenv("VTUI_DEBUG", "1")
		case "--gui":
			guiMode = true
			if flagVal != "" {
				guiBackend = flagVal
			} else if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				guiBackend = os.Args[i+1]
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
				i++
			}
			os.Exit(RunNewPlugin(pluginName, os.Stdout, os.Stderr))
		case "-test-plugins":
			vtui.ConfigDiskLogging(true)
			vtui.DebugLog("--- PLUGIN TEST MODE ---")
			pm := NewPluginManager()
			pm.LoadAll()
			pm.CloseAll()
			return
		case "--tty":
			ttyMode = true
		case "--attached":
			attachedMode = true
		case "--sudo-dispatcher":
			if flagVal != "" {
				sudoDispatcher = flagVal
			} else if i+1 < len(os.Args) {
				sudoDispatcher = os.Args[i+1]
				i++
			}
		}
	}

	if version {
		fmt.Println(getFormattedVersionInfo())
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
		runClient(clientPath)
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

	if ttyMode {
		ManageSessions()
		return
	}

	if guiMode {
		checkAndDetach(attachedMode)
		if guiBackend != "" {
			if err := RunGui(guiBackend); err != nil {
				fmt.Fprintf(os.Stderr, "\n[f4] FATAL GUI ERROR: %v\n", err)
				os.Exit(1)
			}
		} else {
			if err := tryRunDefaultGui(); err != nil {
				fmt.Fprintf(os.Stderr, "\n[f4] FATAL GUI ERROR: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	// Default auto-detect mode (neither --gui nor --tty specified)
	if shouldTryGui() {
		checkAndDetach(attachedMode)
		if err := tryRunDefaultGui(); err != nil {
			vtui.DebugLog("MAIN: GUI auto-detect failed after detach: %v", err)
			os.Exit(1)
		}
		return
	}

	vtui.DebugLog("MAIN: Falling back to console mode")
	ManageSessions()
}

func shouldTryGui() bool {
	if runtime.GOOS == "windows" {
		// On Windows, we compile separate binaries for console (f4.exe) and GUI (f4-gui.exe).
		// We do not auto-detect GUI mode; it must be requested via filename or --gui flag.
		return false
	}
	if runtime.GOOS == "darwin" {
		return true
	}
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

func tryRunDefaultGui() error {
	var errs []string
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		vtui.DebugLog("GUI_AUTO: Trying gogpu...")
		if err := RunGui("gogpu"); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("gogpu: %v", err))
		}
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
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	scr := vtui.NewScreenBuf()
	scr.AllocBuf(width, height)

	vtui.FrameManager.Init(scr)

	SetupUI()

	vtui.DebugLog("CORE: Initialization complete")
	return scr
}

func SetupUI() {
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
	}

	// CrashDirFull задаётся рано (см. main()); здесь только повторная
	// синхронизация для vfs, чтобы конфиг портативного режима был единым.
	vfs.CustomConfigDir = configDir

	os.MkdirAll(configDir, 0755)
	GlobalHotkeysMgr = NewHotkeyManager(filepath.Join(configDir, "hotkeys.ini"))
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
		if previousEventFilter != nil && previousEventFilter(e) {
			return true
		}
		if handlePanelPathEditHotkey(e) {
			return true
		}
		return handleHelpSearchHotkey(e)
	}

	vtui.FrameManager.MenuBar = panels.menuBar
	vtui.FrameManager.KeyBar = panels.keyBar
	vtui.FrameManager.OnRender = func(scr *vtui.ScreenBuf) {
		if AppConfig.WorkspaceTabNumbering == WorkspaceTabNumbersOrder {
			renumberWorkspaceScreens()
		}
		UpdateWindowTitle(scr)
		renderHelpSearch(scr)
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

var getSessionIniPath = func() string {
	return filepath.Join(GetF4ConfigDir(), "session.ini")
}

func LoadSession() {
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

	// Восстанавливаем состояние левой панели
	LastLeftPath = ini.GetString("Panel/Left", "Folder", "")
	LastLeftCursor = ini.GetString("Panel/Left", "CurFile", "")
	fmt.Sscanf(ini.GetString("Panel/Left", "ViewMode", "0"), "%d", &LastLeftViewMode)
	fmt.Sscanf(ini.GetString("Panel/Left", "SortMode", "0"), "%d", &LastLeftSortMode)
	LastLeftSortRev = ini.GetString("Panel/Left", "SortReverse", "0") == "1"

	// Восстанавливаем состояние правой панели
	LastRightPath = ini.GetString("Panel/Right", "Folder", "")
	LastRightCursor = ini.GetString("Panel/Right", "CurFile", "")
	fmt.Sscanf(ini.GetString("Panel/Right", "ViewMode", "0"), "%d", &LastRightViewMode)
	fmt.Sscanf(ini.GetString("Panel/Right", "SortMode", "0"), "%d", &LastRightSortMode)
	LastRightSortRev = ini.GetString("Panel/Right", "SortReverse", "0") == "1"

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
	path := getSessionIniPath()
	os.MkdirAll(filepath.Dir(path), 0755)

	if vtui.FrameManager != nil {
		w := vtui.FrameManager.GetScreenSize()
		h := vtui.FrameManager.GetScreenHeight()
		if w > 0 && h > 0 {
			if AppConfig.GuiCols != w || AppConfig.GuiRows != h {
				AppConfig.GuiCols = w
				AppConfig.GuiRows = h
				SaveConfig()
			}
		}
	}

	if vtui.FrameManager != nil {
		if states, active := captureWorkspaceSessions(); len(states) > 0 {
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
	sb.WriteString(fmt.Sprintf("Pattern = %s\n", LastEditorSearch))
	sb.WriteString(fmt.Sprintf("Replace = %s\n", LastEditorReplace))
	sb.WriteString(fmt.Sprintf("CaseSensitive = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchCase]))
	sb.WriteString(fmt.Sprintf("Reverse = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchReverse]))
	sb.WriteString(fmt.Sprintf("Regexp = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchRegexp]))
	sb.WriteString(fmt.Sprintf("WholeWord = %d\n", map[bool]int{true: 1, false: 0}[LastEditorSearchWholeWord]))

	sb.WriteString("\n[FindFile]\n")
	sb.WriteString(fmt.Sprintf("Mask = %s\n", LastFindFileMask))
	sb.WriteString(fmt.Sprintf("Text = %s\n", LastFindFileText))

	sb.WriteString("\n[Session]\n")
	sb.WriteString(fmt.Sprintf("ActivePanel = %d\n", LastActivePanel))
	sb.WriteString(fmt.Sprintf("WidePanel = %d\n", LastWidePanel))
	sb.WriteString(fmt.Sprintf("ShowPanels = %d\n", map[bool]int{true: 1, false: 0}[LastShowPanels]))
	sb.WriteString(fmt.Sprintf("ShowLeft = %d\n", map[bool]int{true: 1, false: 0}[LastShowLeft]))
	sb.WriteString(fmt.Sprintf("ShowRight = %d\n", map[bool]int{true: 1, false: 0}[LastShowRight]))

	sb.WriteString("\n[Panel/Left]\n")
	sb.WriteString(fmt.Sprintf("Folder = %s\n", LastLeftPath))
	sb.WriteString(fmt.Sprintf("CurFile = %s\n", LastLeftCursor))
	sb.WriteString(fmt.Sprintf("ViewMode = %d\n", LastLeftViewMode))
	sb.WriteString(fmt.Sprintf("SortMode = %d\n", LastLeftSortMode))
	sb.WriteString(fmt.Sprintf("SortReverse = %d\n", map[bool]int{true: 1, false: 0}[LastLeftSortRev]))

	sb.WriteString("\n[Panel/Right]\n")
	sb.WriteString(fmt.Sprintf("Folder = %s\n", LastRightPath))
	sb.WriteString(fmt.Sprintf("CurFile = %s\n", LastRightCursor))
	sb.WriteString(fmt.Sprintf("ViewMode = %d\n", LastRightViewMode))
	sb.WriteString(fmt.Sprintf("SortMode = %d\n", LastRightSortMode))
	sb.WriteString(fmt.Sprintf("SortReverse = %d\n", map[bool]int{true: 1, false: 0}[LastRightSortRev]))
	writeWorkspaceSessions(&sb, LastWorkspaceSessions, LastActiveWorkspace)

	err := os.WriteFile(path, []byte(sb.String()), 0644)
	if err != nil {
		vtui.DebugLog("SESSION: Failed to save state: %v", err)
		return
	}

	vtui.DebugLog("SESSION: Saved state to %s", path)
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
