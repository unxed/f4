package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/fusefs"
	"github.com/unxed/f4/internal/gui"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"golang.org/x/term"
)

// startupDirEnv and startupDirRightEnv carry the panel directories of this
// start to the process that draws them. That process cannot work them out
// itself: the GUI restarts detached, the daemon is spawned by the client, and a
// Dock start would answer with the bundle's own directory. The right-hand one
// is set only when the command line named a directory, which is also what tells
// the panels that the left one was asked for rather than merely inherited.
const (
	startupDirEnv      = "F4_STARTUP_DIR"
	startupDirRightEnv = "F4_STARTUP_DIR_RIGHT"
)

// startupDirsFor resolves what `f4`, `f4 dir` and `f4 dir1 dir2` mean, in the
// order the panels are drawn: left first. One directory leaves the right panel
// in the current one, matching mc. Relative paths are resolved here, while cwd
// is still the shell's; a third argument and beyond has no panel to go to.
func startupDirsFor(cwd string, args []string) (left, right string) {
	abs := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(cwd, path)
	}
	switch len(args) {
	case 0:
		return cwd, ""
	case 1:
		return abs(args[0]), cwd
	default:
		return abs(args[0]), abs(args[1])
	}
}

// plainStartOpensCwd says what `f4` with no folders, started from a terminal,
// shows when the "open the current folder at start" setting is on: the current
// directory in both panels, the way mc does (issue #822), or the panels the
// session restored. With the setting off (the default, far2l's way, issue #495)
// a plain start always restores the session; see farStartupDirs.
//
// Outside Windows a terminal on stdin is what proves that a shell chose that
// directory; a Dock or desktop start has no terminal and names nothing. On
// Windows it proves nothing. A console program started from Explorer or a
// shortcut is given a console of its own, so stdin is a terminal all the same,
// while its working directory is the executable's folder or the shortcut's
// "Start in" -- not a place anyone asked to see. There a plain start keeps the
// restored panels, as it has since the change made for #823.
//
// That change applied to every platform, and `cd dir && f4` on macOS went back
// to showing the previous session instead of dir (issue #1152).
const plainStartOpensCwd = runtime.GOOS != "windows"

// startupDirsOverride says what this start names for the panels, and whether it
// names anything at all. Folders on the command line always do. A plain start
// names the current directory when plainOpensCwd is set, and otherwise leaves
// the panels to the restored session.
func startupDirsOverride(cwd string, args []string, plainOpensCwd bool) (left, right string, ok bool) {
	if len(args) == 0 && !plainOpensCwd {
		return "", "", false
	}
	left, right = startupDirsFor(cwd, args)
	return left, right, true
}

// farStartupDirs is what far2l and Far make of the command line: no folders
// leaves the panels as the last session left them, a first folder replaces the
// panel it names, a second replaces the other one. With a single folder the
// other panel is not touched, which panel.StartupKeepPanel says in place of a
// path. f4 has always named the panels by side, so the first folder goes to the
// left one, and takes the focus with it (panel.ApplyStartupDirs).
//
// far2l is the reference: `far2l path1 path2` opens path1 in the active panel
// and path2 in the passive one, and without paths the panels come from the saved
// setup (far2l/src/main.cpp, Opt.strLeftFolder and friends).
func farStartupDirs(cwd string, args []string) (left, right string, ok bool) {
	abs := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(cwd, path)
	}
	switch len(args) {
	case 0:
		return "", "", false
	case 1:
		return abs(args[0]), panel.StartupKeepPanel, true
	default:
		return abs(args[0]), abs(args[1]), true
	}
}

// startupDirsChoice picks between the two ways to read a start from a
// terminal: mc's (Settings: open the current folder at start), which
// startupDirsOverride implements, and far2l's, which is the default.
func startupDirsChoice(cwd string, args []string, currentFolderStyle bool) (left, right string, ok bool) {
	if currentFolderStyle {
		return startupDirsOverride(cwd, args, plainStartOpensCwd)
	}
	return farStartupDirs(cwd, args)
}

// startupDirArgs picks the panel directories out of a command line: the words
// before the first switch, plus everything after a "--" separator. --gui and
// --tty take their backend as a separate word, so a word after a switch could
// be either that backend or a directory; f4 does not guess between them.
func startupDirArgs(args []string) []string {
	var dirs []string
	beforeSwitches := true
	for i, arg := range args {
		switch {
		case arg == "--":
			return append(dirs, args[i+1:]...)
		case strings.HasPrefix(arg, "-"):
			beforeSwitches = false
		case beforeSwitches:
			dirs = append(dirs, arg)
		}
	}
	return dirs
}

// rememberStartupDirs records those directories. Explicit command-line paths
// must also be carried by a GUI start without a terminal (for example
// `f4-gui.exe path1 path2`); a plain GUI start still leaves the restored
// session alone. It must run before checkAndDetach and before the daemon is
// spawned: both hand the next process /dev/null on stdin. Values inherited
// from the parent win, they are the answer that process already worked out.
func rememberStartupDirs(args []string) {
	if os.Getenv(startupDirEnv) != "" {
		return
	}
	if len(args) == 0 && !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	left, right, ok := startupDirsChoice(cwd, args, config.App.StartInCurrentFolder)
	if !ok {
		return
	}
	_ = os.Setenv(startupDirEnv, left)
	if right != "" {
		_ = os.Setenv(startupDirRightEnv, right)
	}
}

// startupDirs are those directories; an empty left one means the panels keep
// the restored paths, and an empty right one means both take the left.
func startupDirs() (left, right string) {
	return os.Getenv(startupDirEnv), os.Getenv(startupDirRightEnv)
}

// editFilePath holds the -e flag's target, if given, made absolute right after
// the command line is read -- opened in the editor once InitCore() has the
// panels frame ready. Package-level because the
// flag is parsed in main() but the hook point (right after the panels
// frame is pushed) lives in InitCore(), a separate function.
var editFilePath string

// viewFilePaths are the files among the paths named before the switches
// (issue #991): `f4 file` opens file in the viewer, as F3 on it would, while
// the panel goes to its folder (see panel.ApplyStartupDirs). Absolute, and
// package-level for the same reason editFilePath is.
var viewFilePaths []string

// startupViewFiles picks the files out of the startup paths: every word that
// names something other than a folder, resolved against cwd the way
// startupDirsFor resolves it. A folder, and a word that names nothing, stay
// panel paths only.
func startupViewFiles(cwd string, args []string) []string {
	var files []string
	for _, arg := range args {
		if path := resolveStartupPath(cwd, arg); panel.IsStartupFile(path) {
			files = append(files, path)
		}
	}
	return files
}

// resolveStartupPath makes a path from the command line absolute against cwd,
// the directory the command was typed in. It has to happen in the process that
// parsed the command line: on Unix the files are opened by the session daemon,
// whose working directory is the one it was first started from, and a client
// attaching to it from elsewhere used to have `f4 -e notes.txt` open that
// directory's notes.txt.
func resolveStartupPath(cwd, path string) string {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	return path
}

// openStartupFilesIfRequested opens what the command line named for viewing and
// for editing (see openStartupFilesOnPanels). Called from every entry
// point where SetupUI() (or InitCore(), which calls it) already ran in the one
// process that's actually going to render -- every GUI backend, and the
// tty path on Windows (session_windows.go). The Unix tty path is the odd
// one out: it daemonizes, so SetupUI() there runs inside a not-yet-
// attached background process with nothing to draw to; session_unix.go's
// terminal.RunServer() hands the files to ClientAttached instead, timed to the
// actual client attach, not to this function.
func openStartupFilesIfRequested() {
	openStartupFilesOnPanels(viewFilePaths, editFilePath)
}

// openStartupFilesOnPanels opens the files on the panels frame: the top frame,
// or, when a dialog is open over the panels -- in a running session a client
// attaches to, the user may have left one open, or an update prompt may have
// come up -- the panels under it. The viewer and the editor open as screens of
// their own, so the dialog stays where it is. The panels are not moved to the
// startup directories in that case, as a dialog may be acting on them.
func openStartupFilesOnPanels(viewPaths []string, editPath string) {
	if editPath == "" && len(viewPaths) == 0 {
		return
	}
	pf := panel.FindPanelsFrame()
	if pf == nil {
		vtui.DebugLog("MAIN: -e %q, view %q: no panels frame to open them from (top frame %T)",
			editPath, viewPaths, vtui.FrameManager.GetTopFrame())
		return
	}
	openStartupFilesIn(pf, viewPaths, editPath)
}

// openStartupFilesIn opens each view path in the viewer and then editPath, if
// any, in the editor.
func openStartupFilesIn(pf *panel.PanelsFrame, viewPaths []string, editPath string) {
	for _, path := range viewPaths {
		openViewFileIn(pf, path)
	}
	if editPath != "" {
		openEditFileIn(pf, editPath)
	}
}

// openViewFileIn opens path in pf's viewer through actionOpenViewer, the path
// F3 takes after its file associations: the same "already viewed?" dialog,
// history entry, and choice between the image viewer, the video player and the
// text viewer.
func openViewFileIn(pf *panel.PanelsFrame, path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		vtui.DebugLog("MAIN: view %q: filepath.Abs failed: %v", path, err)
		return
	}
	actionOpenViewer(pf, vfs.NewOSVFS(filepath.Dir(abs)), abs)
}

// openEditFileIn resolves path to an absolute path and opens it in pf's
// editor via the normal action_registry path (the same one F4/double-click
// use), so -e behaves identically to a user opening the file by hand --
// same "already open?" dialog, same history entry, same everything.
func openEditFileIn(pf *panel.PanelsFrame, path string) {
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

// Main is f4's entry point. cmd/f4's main() is a call to it and nothing else:
// what this function does — read the flags, pick a startup mode, wire every
// subsystem's seam and run the loop — is the composition root's work, and the
// composition root is this package.
//
// It exits through os.Exit at seven points and returns nothing, the shape it
// has always had. Turning it into a New/Run pair that returns an error is a
// redesign of the startup path, not a move, and no test covers it; see the
// deviation recorded for Task 36.
func Main() {
	vtui.AppName = "f4"
	setProcessName()
	// Before anything asks where the configuration lives: internal/config is a
	// layer-0 leaf and cannot reach internal/update for the answer.
	config.Executable = update.Executable
	keymap.Suspended = keyRemapSuspended
	configureF4DebugLogPath(config.GetF4ConfigDir())
	if archivePath, archiveKind, found, err := update.ParseHelperArgs(os.Args[1:]); found {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := update.RunHelper(archivePath, archiveKind); err != nil {
			fmt.Fprintf(os.Stderr, "f4 update helper failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if backupPath, found, err := update.ParseRestoreHelperArgs(os.Args[1:]); found {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := update.RunRestoreHelper(backupPath); err != nil {
			fmt.Fprintf(os.Stderr, "f4 restore helper failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	installConsoleCtrlHandler()
	var sudoDispatcher string

	// Initialize SudoClient immediately for all process types
	execPath, err := update.Executable()
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
	vtui.CrashDirFull = filepath.Join(config.GetF4ConfigDir(), "crashes")
	installHangDumpHandler()

	vtui.SetupStderrLog()
	// The configuration is read before the detached copy's stderr fix, not
	// after, because that fix is one of the two places f4 uses libwinescape
	// and UseWinescape decides whether it may. Reading settings.ini is a
	// handful of os.Stat/os.ReadFile calls that do not touch the host file
	// layer, so nothing here can freeze the personality before the setting
	// is applied.
	config.LoadConfig() // Load config early to apply GUI font settings
	hostmode.SetAllowed(config.App.UseWinescape)
	redirectDetachedStdout()
	vtui.DebugLog("MAIN: Starting with args: %v", os.Args)

	defer func() {
		SaveSession() // Гарантирует сохранение размеров и путей при любом выходе
		if plughost.GlobalPluginManager != nil {
			plughost.GlobalPluginManager.CloseAll()
		}
		panel.ShutdownProcessEnvironmentRuntime()
		if fileops.GlobalFileState != nil {
			fileops.GlobalFileState.Flush()
		}
		if r := recover(); r != nil {
			vtui.DebugLog("FATAL PANIC IN MAIN: %v", r)
			crashPath := vtui.RecordCrash(r, nil)
			vtui.Suspend()
			// We print to os.Stdout here because os.Stderr is redirected to the log file!
			_, _ = fmt.Fprintf(os.Stdout, "\n[f4] FATAL PANIC IN MAIN: %v\n", r)
			if crashPath != "" {
				_, _ = fmt.Fprintf(os.Stdout, "[f4] Crash report saved to: %s\n", crashPath)
			}
			vtui.CleanupStderrLog()
			cleanupWineStderrLog()
			os.Exit(2)
		}
		vtui.CleanupStderrLog()
		cleanupWineStderrLog()
	}()
	// Defer disk logging to prevent launcher processes from polluting rotation queue.
	// Logging will be enabled in InitCore() for workers and standalone sessions.
	vtui.ConfigDiskLogging(false)
	var serverPath, clientPath string
	var cpuprofile string
	var diagFlags diagnosticFlags
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
	var updateRequested bool
	var updateChannelArg string

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
			_ = os.Setenv("VTUI_DEBUG", "1")
		case "--update":
			updateRequested = true
			if flagVal != "" {
				updateChannelArg = flagVal
			} else if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				updateChannelArg = os.Args[i+1]
				i++
			}
		case "-gui", "--gui":
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
				_ = os.Setenv("VTUI_DEBUG", flagVal)
			} else if i+1 < len(os.Args) {
				_ = os.Setenv("VTUI_DEBUG", os.Args[i+1])
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
		case "--trace", "--stall-watchdog":
			consumed, err := diagFlags.apply(flagName, flagVal, argAfter(os.Args, i))
			if err != nil {
				// stdout, like --version and --help: f4 has already taken stderr
				// over for its own log by the time a switch is read.
				fmt.Printf("%s: %v\n", flagName, err)
				os.Exit(2)
			}
			i += consumed
		case "--new-plugin":
			pluginName := flagVal
			if pluginName == "" && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				pluginName = os.Args[i+1]
			}
			os.Exit(plughost.RunNewPlugin(pluginName, os.Stdout, os.Stderr))
		case "-test-plugins":
			configureF4DebugLogPath(config.GetF4ConfigDir())
			vtui.ConfigDiskLogging(true)
			vtui.DebugLog("--- PLUGIN TEST MODE ---")
			pm := plughost.NewPluginManager(&coreAPI{})
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
		case "-e", "--edit":
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
	rememberStartupDirs(startupDirArgs(os.Args[1:]))
	if cwd, err := os.Getwd(); err == nil {
		viewFilePaths = startupViewFiles(cwd, startupDirArgs(os.Args[1:]))
		if editFilePath != "" {
			editFilePath = resolveStartupPath(cwd, editFilePath)
		}
	}
	configureF4DebugLogPath(config.GetF4ConfigDir())

	if version {
		fmt.Println(getFormattedVersionInfo())
		return
	}
	if print_help {
		fmt.Printf(`f4 version: %s
f4 is efficient and cozy two-panel file manager in go
Usage: f4 [path1 [path2]] [switches]
Paths come before the switches, or after a "--" separator. Without them both
panels open the current directory (on Windows they keep the folders of the last
session); path1 alone opens in the left panel and leaves the right one on the
current directory. A path that names a file opens that file in the viewer, as
F3 would, and its panel shows the file's folder with the cursor on it.
The following switches may be used in the command line:
 -h, -?, --help         This help and exit
 -v, --version          Displays the current version and exit
 --attached             Force run in Attached-mode
 --client [clientPath]
 --cpuprofile [cpuprofile]
 --trace [file]         Write a runtime execution trace, which records GC
                         pauses, blocking syscalls and scheduling as well as
                         CPU; read it with "go tool trace"
 --stall-watchdog [d]   Write every goroutine's stack into the profile's
                         crashes folder whenever one UI frame takes longer
                         than d (default 250ms). Answers what a freeze was
                         waiting on, which a CPU profile cannot.
 --debug                Log to profile logs/debug.log (equivalent to --log=1)
 --dump-screen-after N  Auto-run Debug.ScreenDump N seconds after startup
                         (bypasses hotkeys entirely -- useful under Wine
                         tty mode, where complex combos like CtrlAltP can
                         fail to arrive through native console input)
 -e, --edit [filename]  Open filename directly in the editor on startup
                         (far2l-compatible; useful for scripted/headless
                         testing where interactive navigation is unreliable)
 -gui, --gui [Backend]  Force run in GUI-mode
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
 --update [Channel]     Download and install the newest build, then exit;
                         [Channel] values: "stable" (or "latest"), "nightly";
                         if Channel omited, the configured update channel is
                         used ([Update] Channel, Options > Auto update), and
                         a named channel becomes the configured one
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

	// Updating is a command, not a way to start the file manager: no panels
	// and no session come up here. os.Exit skips the deferred SaveSession on
	// purpose, this run never touched the session.
	if updateRequested {
		os.Exit(update.RunCLI(updateChannelArg, updateSettings(), currentBuild(), applyUpdateSettings))
	}

	for _, arg := range os.Args {
		if arg == "--askpass" {
			vfs.RunSudoAskpass()
			return
		}
	}

	// Before the daemon and client branches below, so that a daemon started
	// with these switches measures itself (#884).
	if cpuprofile != "" {
		// #nosec G703 -- cpuprofile is the path the user typed after
		// --cpuprofile; writing where they asked is the whole feature.
		f, err := os.Create(cpuprofile)
		if err != nil {
			panic(err)
		}
		_ = pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if diagFlags.wanted() {
		stopDiagnostics := diagFlags.arm(filepath.Join(config.GetF4ConfigDir(), "crashes"))
		defer stopDiagnostics()
	}
	terminal.ServerDiagnosticArgs = serverDiagnosticArgs(cpuprofile, diagFlags)

	if serverPath != "" {
		terminal.RunServer(serverPath)
		return
	}
	if clientPath != "" {
		terminal.RunClient(clientPath, 0)
		return
	}

	// Settings.ini supplies whatever this run did not (issue #601). The
	// startup mode and the two backends are independent: the mode says which
	// renderer family f4 opens, each backend says which renderer that family
	// uses once opened.
	if !startupChoiceGiven {
		switch config.App.StartupMode {
		case config.StartupModeTTY:
			ttyMode = true
		case config.StartupModeGui:
			guiMode = true
		}
	}
	guiBackendFromConfig := !guiBackendGiven && config.NormalizeStartupGuiBackend(config.App.GuiBackend) != ""
	guiBackend = resolveStartupBackend(guiBackend, guiBackendGiven, config.App.GuiBackend, config.NormalizeStartupGuiBackend)
	ttyBackend = resolveStartupBackend(ttyBackend, ttyBackendGiven, config.App.TTYBackend, config.NormalizeStartupTTYBackend)
	vtui.DebugLog("MAIN: startup mode=%s guiMode=%v ttyMode=%v guiBackend=%q ttyBackend=%q",
		config.App.StartupMode, guiMode, ttyMode, guiBackend, ttyBackend)

	if ttyBackend != "" {
		terminal.SelectedTTYBackend = ttyBackend
	} else {
		terminal.SelectedTTYBackend = vtui.DefaultConsoleBackend()
	}
	configureNestedInputMode()

	// The probe runs after backend selection (so it can report what f4 would
	// really use) and before any renderer exists (so it cannot disturb the
	// console it is describing).
	if wineProbe {
		terminal.RunWineProbe()
		return
	}

	if dumpScreenAfter > 0 {
		delay := time.Duration(dumpScreenAfter * float64(time.Second))
		time.AfterFunc(delay, func() {
			actionScreenDump()
		})
	}

	if ttyMode {
		redirectConsoleWineStderr()
		terminal.ManageSessions()
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
	redirectConsoleWineStderr()
	terminal.ManageSessions()
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
	err := gui.RunGui(backend, setupGuiUI)
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
	if liteBuild {
		// A lite build has no GUI backend to try (internal/gui/run_lite.go
		// makes gui.RunGui always fail); go straight to console mode instead
		// of attempting one and hard-failing when a display happens to be set.
		return false
	}
	if runtime.GOOS == "windows" {
		// Windows ships separate binaries for console (f4.exe) and GUI
		// (f4-gui.exe). GUI mode is not auto-detected; it must be requested
		// via the filename or the --gui flag. Wine gets exactly the same
		// rules (issue #474).
		return false
	}
	// A terminal launch must stay in console mode even when the shell has a
	// display environment (for example, an SSH session into a desktop or a
	// terminal opened under X11). The desktop launcher has no host TTY and can
	// still select the GUI from the display variables below.
	if terminal.ProbeHostTTY() {
		return false
	}
	if runtime.GOOS == "darwin" {
		return true
	}
	return os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
}

// setupGuiUI builds the interface inside a freshly opened GUI window. The files
// named on the command line are opened here rather than before RunGui, because
// they need the frames that SetupUI creates.
func setupGuiUI() {
	SetupUI()
	openStartupFilesIfRequested()
}

func tryRunDefaultGui() error {
	// Wine follows the Windows order below unchanged (issue #474): it used
	// to try win32 once on its own first and, if that failed, drop the error
	// and try win32 a second time here.
	var errs []string
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {

		// Windows: try native win32 (GDI) first as the primary, lightweight, cgo-free backend.
		if runtime.GOOS == "windows" {
			vtui.DebugLog("GUI_AUTO: Trying win32...")
			if err := gui.RunGui("win32", setupGuiUI); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("win32: %v", err))
			}

			vtui.DebugLog("GUI_AUTO: Trying ebiten...")
			if err := gui.RunGui("ebiten", setupGuiUI); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("ebiten: %v", err))
			}
		}

		// Try gogpu (macOS default; Windows fallback)
		vtui.DebugLog("GUI_AUTO: Trying gogpu...")
		if err := gui.RunGui("gogpu", setupGuiUI); err == nil {
			return nil
		} else {
			errs = append(errs, fmt.Sprintf("gogpu: %v", err))
		}

		// Fallback to X11 if DISPLAY environment variable is set
		if os.Getenv("DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying x11...")
			if err := gui.RunGui("x11", setupGuiUI); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("x11: %v", err))
			}
		}
	} else {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying wayland...")
			if err := gui.RunGui("wayland", setupGuiUI); err == nil {
				return nil
			} else {
				errs = append(errs, fmt.Sprintf("wayland: %v", err))
			}
		}
		if os.Getenv("DISPLAY") != "" {
			vtui.DebugLog("GUI_AUTO: Trying x11...")
			if err := gui.RunGui("x11", setupGuiUI); err == nil {
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
	if p := terminal.ProbeConsole(); true {
		vtui.DebugLog("ENV: wine=%v backend=%q size=%dx%d consoleBuffer=%v window=%dx%d",
			vtui.IsWine(), terminal.SelectedTTYBackend, width, height, p.OK, p.WinCols(), p.WinRows())
	}
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	scr := vtui.NewScreenBuf()
	if terminal.SelectedTTYBackend == "winapi" || terminal.SelectedTTYBackend == "win32" {
		scr.Renderer = vtui.NewWin32ConsoleRenderer(scr)
	}
	scr.AllocBuf(width, height)

	vtui.FrameManager.Init(scr)

	// Only this console path may pick the first-start style from the console:
	// the GUI window draws in true colour whatever the process's console is.
	setupUI(consoleFirstRunColorStyle)

	vtui.DebugLog("CORE: Initialization complete")
	return scr
}

// applyAndRememberStartupDirs applies explicit command-line paths and mirrors
// the resulting panel state into the legacy session fields. The live panels
// remain the source of truth during a normal save, but the legacy snapshot is
// the fallback used if a GUI backend tears down its screens before Main's
// deferred SaveSession runs.
func applyAndRememberStartupDirs(panels *panel.PanelsFrame, left, right string) {
	panel.ApplyStartupDirs(panels, left, right)
	if left == "" {
		return
	}
	panel.SetLegacyWorkspaceSession(panel.CaptureWorkspaceSession(panels))
}

func SetupUI() {
	setupUI(nil)
}

// setupUI is SetupUI with one hook: firstRunStyle, when set, may name the
// colour style to start with in place of the built-in default. It is asked only
// when no settings.ini has chosen a style (issue #513).
func setupUI(firstRunStyle func() (string, bool)) {
	configureUnicodeInput()
	vtui.ConfigDiskLogging(os.Getenv("VTUI_DEBUG") != "")
	vtui.DebugLog("=== F4 STARTUP [%s] PID:%d ===", getFormattedVersionInfo(), os.Getpid())

	theme.SetDefaultF4Palette()
	config.LoadConfig()
	config.ApplyWheelSettings()
	config.ApplyMenuSettings()
	vtui.PathHintProvider = panel.PathHintProvider
	panel.ApplyPathHintSettings()
	ctrlTabMode := vtui.WorkspaceCtrlTabDirect
	if config.App.CtrlTabShowsMenu {
		ctrlTabMode = vtui.WorkspaceCtrlTabMenu
	}
	vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabMode(config.App.WorkspaceTabMode), ctrlTabMode)
	vtui.FrameManager.ConfigureWorkspaceTabOverlay(config.App.WorkspaceTabsOverlay)
	vtui.FrameManager.ConfigureWorkspaceAltNumberSwitch(config.App.AltNumberSwitchesTabs)
	initLang()
	applyFirstRunColorStyle(firstRunStyle)
	if err := theme.ApplyColorStyle(config.App.ColorStyle); err != nil {
		vtui.DebugLog("COLORS: %v; falling back to Modern", err)
		config.App.ColorStyle = "Modern"
		_ = theme.ApplyColorStyle(config.App.ColorStyle)
	}
	vtui.GlobalHistoryProvider = history.NewF4HistoryProvider(config.GetF4ConfigDir())
	history.SamePath = panel.SameFolderHistoryPath
	fileops.GlobalFileState = fileops.NewF4FileStateProvider()
	// A file operation sent to the background keeps its progress dialog; the
	// dialog needs a workspace behind it, and a copy is what lets the user go
	// on working in the original.
	fileops.BackgroundWorkspace = func() vtui.Frame {
		pf := panel.FindPanelsFrame()
		if pf == nil {
			return nil
		}
		return pf.Clone()
	}
	fileops.StartQueueWorker()
	// A copy that ignores errors ends with a summary whose "View log" opens the
	// log in f4's own viewer (#722).
	fileops.OpenLog = func(path string) {
		if pf := panel.FindPanelsFrame(); pf != nil {
			actionOpenViewer(pf, vfs.NewOSVFS(filepath.Dir(path)), path)
		}
	}
	// The registry is a leaf and cannot reach the message catalogue; the root
	// hands it the lookup. Moves to internal/i18n's i18n.Msg when that package exists.
	action.Localize = i18n.Msg
	// internal/editor declares what it needs from above; this is the root
	// filling it in. Each is one call site inside the editor.
	editor.RunAction = RunAction
	editor.LookupHotkey = func(e *vtinput.InputEvent) bool { return macroLookupHotkey(macro.MacroMgr, e) }
	editor.MenuBarItems = BuildMenuBarItems
	editor.CrossAttrs = EditorCrossAttrs
	viewer.NewTextColorizer = editor.NewTextColorizer
	viewer.NewWindowColorizer = editor.NewWindowColorizer
	editor.KeyBarLabels = keymap.KeyBarLabelsForArea
	editor.HotkeyAction = func(area, key string) string {
		if keymap.GlobalHotkeysMgr == nil {
			return ""
		}
		return keymap.GlobalHotkeysMgr.GetAction(area, key)
	}
	editor.RememberEdited = func(v vfs.VFS, path string) { rememberViewerEditorHistory(v, path, historyModeEdit) }
	editor.SaveSession = SaveSession
	editor.HandleWorkspaceFork = handleWorkspaceForkCommand
	editor.SwitchToViewer = actionSwitchEditorToViewer
	// internal/panel declares what it needs from the application above it; this
	// is the root filling it in. Every default is inert, so an unwired panel
	// declines the command rather than doing the wrong thing.
	panel.AppCommand = handlePanelsAppCommand
	panel.RunAction = RunAction
	panel.BuildMenuBarItems = BuildMenuBarItems
	panel.SaveSession = SaveSession
	panel.OpenEditor = actionOpenEditor
	panel.OpenViewer = actionOpenViewer
	panel.OpenViewerInternal = openViewerInternal
	panel.OpenEditFileIn = openEditFileIn
	panel.ShowViewer = showViewer
	panel.ShowEditor = showEditor
	panel.FindOpenedEditor = findOpenedEditor
	panel.Execute = actionExecute
	panel.SortMenuForPanel = actionSortMenuForPanel
	panel.WorkspaceClose = actionWorkspaceClose
	panel.Arkanoid = actionArkanoid
	panel.CurrentArea = macroCurrentArea
	panel.MacroHotkey = func(e *vtinput.InputEvent) bool { return macroLookupHotkey(macro.MacroMgr, e) }
	panel.KeyFilter = func(e *vtinput.InputEvent) bool { return macroFilter(macro.MacroMgr, e) }
	panel.AISetViewMode = aiSetViewMode
	vtinput.Logger = vtui.DebugLog // Pipe vtinput logs to vtui's debug logger
	vtui.GlobalClipboardAccessManager = terminal.NewF4ClipboardAuth()
	// sysinfo.RegisterDrive("Null VFS", func() vfs.VFS { return vfs.NewNullVFS(50 * 1024 * 1024) }) // 50 MB/s

	configDir := config.GetF4ConfigDir()

	// Initialize File Highlighting
	highlightPath := filepath.Join(configDir, "highlight.ini")
	if _, err := os.Stat(highlightPath); os.IsNotExist(err) {
		config.CreateDefaultHighlightIni(highlightPath)
	}
	if _, err := os.Stat(highlightPath); err == nil {
		loadHighlightIni(ini.Load(highlightPath))
	}

	// CrashDirFull задаётся рано (см. main()); здесь только повторная
	// синхронизация для vfs, чтобы конфиг портативного режима был единым.
	vfs.CustomConfigDir = configDir

	_ = os.MkdirAll(configDir, 0755)
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager(filepath.Join(configDir, "hotkeys.ini"))
	keymapPath := filepath.Join(configDir, "keymap.ini")
	if _, err := os.Stat(keymapPath); os.IsNotExist(err) {
		// The file is the documentation: a user fighting a multiplexer has to
		// find it in the profile without knowing it exists first.
		keymap.CreateDefaultKeymapIni(keymapPath)
	}
	keymap.GlobalKeyRemap = keymap.NewKeyRemap(keymapPath)
	macro.MacroMgr = macro.NewMacroManager(filepath.Join(configDir, "key_macros.ini"))
	macro.MacroMgr.LoadLuaMacros(f4MacroHost{}, filepath.Join(configDir, "Macros", "scripts"))
	// Help is initialized after the hotkey manager: key binding topics
	// are generated from the action registry and must reflect the
	// user's overrides from hotkeys.ini.
	InitHelpSystem()
	vtui.FrameManager.EventFilter = func(e *vtinput.InputEvent) bool {
		return macroFilter(macro.MacroMgr, e)
	}

	pluginsDisabled := false
	for _, arg := range os.Args {
		if arg == "--no-plugins" {
			pluginsDisabled = true
			break
		}
	}
	if !pluginsDisabled {
		plughost.GlobalPluginManager = plughost.NewPluginManager(&coreAPI{})
		// Built-ins only register local capabilities and must be ready before
		// LoadSession restores provider-owned visual panel paths.
		plughost.GlobalPluginManager.LoadInternal()
	} else {
		plughost.GlobalPluginManager = nil
		vtui.DebugLog("CORE: Plugins disabled by --no-plugins flag")
	}

	LoadSession()
	config.ApplyCursorSettings()
	vtui.FrameManager.Push(vtui.NewDesktop())

	width := vtui.FrameManager.GetScreenSize()
	height := vtui.FrameManager.GetScreenHeight()

	panels := panel.NewPanelsFrame()
	panels.ResizeConsole(width, height)
	states, activeWorkspace := panel.WorkspaceSessionsForRestore(
		panel.LastWorkspaceSessions, panel.LastActiveWorkspace, config.App.RestoreWorkspaceTabs,
	)
	if len(states) == 0 && config.App.SavePanelPaths {
		states = []panel.WorkspaceSessionState{panel.LegacyWorkspaceSession()}
	}
	if len(states) > 0 {
		panel.ApplyWorkspaceSession(panels, states[0], width, height, config.App.SavePanelPaths)
	}
	// The startup directories outrank the restored paths. A client attaching to
	// a running daemon brings its own instead -- see attachPayload.
	startLeft, startRight := startupDirs()
	applyAndRememberStartupDirs(panels, startLeft, startRight)
	vtui.FrameManager.Push(panels)
	if len(states) > 1 {
		// AddScreenBackground inserts immediately after the active workspace;
		// restore from right to left to preserve the saved tab order.
		for i := len(states) - 1; i >= 1; i-- {
			state := states[i]
			extra := panel.NewPanelsFrame()
			panel.ApplyWorkspaceSession(extra, state, width, height, config.App.SavePanelPaths)
			vtui.FrameManager.AddScreenBackground(extra)
		}
	}
	if len(states) > 0 {
		if config.App.WorkspaceTabNumbering == config.WorkspaceTabNumbersAlways {
			numbers := make([]int, len(states))
			for i, state := range states {
				numbers[i] = state.Number
			}
			vtui.FrameManager.RestoreScreenNumbers(numbers)
		} else {
			panel.RenumberWorkspaceScreens()
		}
		if activeWorkspace > 0 && activeWorkspace < len(vtui.FrameManager.Screens) {
			vtui.FrameManager.SwitchScreen(activeWorkspace)
		}
	}
	previousEventFilter := vtui.FrameManager.EventFilter
	vtui.FrameManager.EventFilter = func(e *vtinput.InputEvent) bool {
		if panel.HandleTranslatorMouseEvent(e) {
			return true
		}
		if previousEventFilter != nil && previousEventFilter(e) {
			return true
		}
		if handleForcedMouseSelectionEvent(e) {
			return true
		}
		if history.HandleMenuHistoryEvent(e) {
			return true
		}
		if panel.HandlePanelPathEditHotkey(e) {
			return true
		}
		if dialog.HandleHelpSearchHotkey(e) {
			return true
		}
		if panels.ShellMode == terminal.ShellModeSimpleInline && panels.ConsoleViewActive() && panels.IsTopFrame() {
			if e.Type == vtinput.KeyEventType && e.KeyDown {
				vtui.FrameManager.PostTask(func() {
					panels.DrawConsoleOverlay()
				})
			}
		}
		return false
	}

	vtui.FrameManager.MenuBar = panels.MenuBar
	vtui.FrameManager.KeyBar = panels.KeyBar
	// consoleOverlayOwnedScreen tracks whether panels was the top frame the
	// last time OnRender checked, so a foreign frame (editor/viewer/dialog
	// opened via F3/F4 or similar while the console view is showing) giving
	// the screen back to panels can be told apart from panels having simply
	// stayed on top the whole time. Only the former needs a full background
	// repaint: panels.Busy stays true across that whole round trip (it is
	// what keeps the console view free of a full panels/keybar flush while
	// some other frame owns the screen), so once panels is back on top,
	// renderPhase()'s busy gate blocks any further full redraw -- without an
	// explicit terminal.ClearConsoleViewBackground() here, whatever the foreign frame
	// last drew stays frozen under nothing but the two freshly-painted
	// overlay rows.
	consoleOverlayOwnedScreen := true
	// Help's hint, query and match highlights belong to the Help window, so
	// they are painted with it, in stack order (#378). The optional dialog
	// outer border (#1399) chains after it for the same reason: each frame
	// decoration belongs with the frame it decorates, painted right after it.
	vtui.FrameManager.AfterFrameShow = func(scr *vtui.ScreenBuf, frame vtui.Frame) {
		dialog.RenderHelpFrame(scr, frame)
		RenderDialogOuterBorder(scr, frame)
	}
	vtui.FrameManager.OnRender = func(scr *vtui.ScreenBuf) {
		if config.App.WorkspaceTabNumbering == config.WorkspaceTabNumbersOrder {
			panel.RenumberWorkspaceScreens()
		}
		UpdateWindowTitle(scr)
		dialog.FinishHelpRender()
		if panels.ShellMode == terminal.ShellModeSimpleInline && panels.ConsoleViewActive() {
			onTop := panels.IsTopFrame()
			if onTop {
				if !consoleOverlayOwnedScreen {
					terminal.ClearConsoleViewBackground(panels.LastW, panels.LastH)
				}
				panels.DrawConsoleOverlay()
			}
			consoleOverlayOwnedScreen = onTop
		}
	}

	// External plugins may post a permission dialog or call Host.RunAction
	// during Init. Start them only after session restoration and initial frame
	// construction, and never wait for them before the UI event loop starts.
	if plughost.GlobalPluginManager != nil {
		plughost.GlobalPluginManager.StartExternal()
	}

	// Background update check
	if config.App.UpdateInterval > 0 {
		go CheckForUpdates(panels, false)
		go plughost.CheckForPluginUpdates()
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
		// The reader parses bytes; the console host only sends them once it
		// has been asked to. See prepareNestedConsoleInput.
		prepareNestedConsoleInput()
	}
}

// loadHighlightIni hands highlight.ini to the file highlighter and to the sort
// groups. Sort groups share the file (and the rule syntax) with highlighting,
// the way far keeps both in one dialog. Themes may not define them.
func loadHighlightIni(file *ini.File) {
	theme.GlobalFileHighlighter.LoadFromIni(file)
	// A Group key inside a coloured [Highlight_N] section puts the files that
	// rule matches into that group: one matcher both paints and places (#413).
	panel.GlobalSortGroups.LoadFromIni(file, theme.GlobalFileHighlighter.UserRules)
}

var getSessionIniPath = func() string {
	return filepath.Join(config.GetF4ConfigDir(), "session.ini")
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
	ini := ini.Load(path)

	editor.LastEditorSearch = ini.GetString("EditorSearch", "Pattern", "")
	editor.LastEditorReplace = ini.GetString("EditorSearch", "Replace", "")
	editor.LastEditorSearchCase = ini.GetString("EditorSearch", "CaseSensitive", "0") == "1"
	editor.LastEditorSearchReverse = ini.GetString("EditorSearch", "Reverse", "0") == "1"
	editor.LastEditorSearchRegexp = ini.GetString("EditorSearch", "Regexp", "0") == "1"
	editor.LastEditorSearchWholeWord = ini.GetString("EditorSearch", "WholeWord", "0") == "1"

	LastFindFileMask = ini.GetString("FindFile", "Mask", "*")
	LastFindFileText = ini.GetString("FindFile", "Text", "")
	LastFindFileCaseSensitive = ini.GetString("FindFile", "CaseSensitive", "0") == "1"
	LastFindFileWholeWords = ini.GetString("FindFile", "WholeWords", "0") == "1"
	LastFindFileRegexp = ini.GetString("FindFile", "Regexp", "0") == "1"
	LastFindFileNotContaining = ini.GetString("FindFile", "NotContaining", "0") == "1"
	LastFindFileFolders = ini.GetString("FindFile", "Folders", "0") == "1"
	LastFindFileSymlinks = ini.GetString("FindFile", "Symlinks", "0") == "1"

	// Восстанавливаем состояние левой панели
	panel.LastLeftPath = ini.GetString("Panel/Left", "Folder", "")
	panel.LastLeftCursor = ini.GetString("Panel/Left", "CurFile", "")
	_, _ = fmt.Sscanf(ini.GetString("Panel/Left", "ViewMode", "0"), "%d", &panel.LastLeftViewMode)
	_, _ = fmt.Sscanf(ini.GetString("Panel/Left", "SortMode", "0"), "%d", &panel.LastLeftSortMode)
	panel.LastLeftSortRev = ini.GetString("Panel/Left", "SortReverse", "0") == "1"
	panel.LastLeftSortGroups = ini.GetString("Panel/Left", "UseSortGroups", "0") == "1"
	if _, err := fmt.Sscanf(ini.GetString("Panel/Left", "GroupBy", "0"), "%d", &panel.LastLeftGroupBy); err != nil {
		panel.LastLeftGroupBy = panel.GroupNone
	}
	panel.LastLeftGroupBy = panel.ValidGroupMode(panel.LastLeftGroupBy)
	panel.LastLeftGroupReverse = ini.GetString("Panel/Left", "GroupReverse", "0") == "1"
	panel.LastLeftGroupFoldersSeparately = ini.GetString("Panel/Left", "GroupFoldersSeparately", "1") == "1"

	// Восстанавливаем состояние правой панели
	panel.LastRightPath = ini.GetString("Panel/Right", "Folder", "")
	panel.LastRightCursor = ini.GetString("Panel/Right", "CurFile", "")
	_, _ = fmt.Sscanf(ini.GetString("Panel/Right", "ViewMode", "0"), "%d", &panel.LastRightViewMode)
	_, _ = fmt.Sscanf(ini.GetString("Panel/Right", "SortMode", "0"), "%d", &panel.LastRightSortMode)
	panel.LastRightSortRev = ini.GetString("Panel/Right", "SortReverse", "0") == "1"
	panel.LastRightSortGroups = ini.GetString("Panel/Right", "UseSortGroups", "0") == "1"
	if _, err := fmt.Sscanf(ini.GetString("Panel/Right", "GroupBy", "0"), "%d", &panel.LastRightGroupBy); err != nil {
		panel.LastRightGroupBy = panel.GroupNone
	}
	panel.LastRightGroupBy = panel.ValidGroupMode(panel.LastRightGroupBy)
	panel.LastRightGroupReverse = ini.GetString("Panel/Right", "GroupReverse", "0") == "1"
	panel.LastRightGroupFoldersSeparately = ini.GetString("Panel/Right", "GroupFoldersSeparately", "1") == "1"

	// Восстанавливаем глобальное состояние сессии
	activeStr := ini.GetString("Session", "ActivePanel", "1")
	_, _ = fmt.Sscanf(activeStr, "%d", &panel.LastActivePanel)
	panel.LastWidePanel = -1
	_, _ = fmt.Sscanf(ini.GetString("Session", "WidePanel", "-1"), "%d", &panel.LastWidePanel)
	if panel.LastWidePanel < -1 || panel.LastWidePanel > 1 {
		panel.LastWidePanel = -1
	}
	panel.LastShowPanels = ini.GetString("Session", "ShowPanels", "1") == "1"
	panel.LastShowLeft = ini.GetString("Session", "ShowLeft", "1") == "1"
	panel.LastShowRight = ini.GetString("Session", "ShowRight", "1") == "1"
	panel.LastWorkspaceSessions, panel.LastActiveWorkspace = panel.LoadWorkspaceSessions(ini)

	vtui.DebugLog("SESSION: Loaded state from %s", path)
}

func SaveSession() {
	if !sessionLoaded {
		vtui.DebugLog("SESSION: State was never loaded, nothing to save")
		return
	}
	if !config.App.AutoSaveSettings {
		vtui.DebugLog("SESSION: Automatic saving is disabled")
		return
	}
	saveSessionWithOptions(config.App.AutoSavePanelSettings, config.App.AutoSaveCurrentPanel, config.App.AutoSaveGUIWindow)
}

func saveSessionWithOptions(savePanelSettings, saveCurrentPanel, saveGUIWindow bool) {
	if !savePanelSettings && !saveCurrentPanel && !saveGUIWindow && !config.App.AutoSaveDialogSettings {
		return
	}
	path := getSessionIniPath()
	if saveGUIWindow {
		windowChanged := captureCurrentWindowSize()
		positionChanged := captureCurrentWindowPosition()
		if windowChanged || positionChanged {
			if config.App.AutoSaveDialogSettings {
				config.SaveWithWindowSize(true)
			} else {
				config.SaveGuiWindowSize()
			}
		}
	} else if config.App.AutoSaveDialogSettings {
		// Flush a pending settings-dialog change at shutdown without replacing
		// the last GUI geometry when that group is disabled.
		config.SaveWithWindowSize(false)
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
	if w <= 0 || h <= 0 || (config.App.GuiCols == w && config.App.GuiRows == h) {
		return false
	}
	config.App.GuiCols = w
	config.App.GuiRows = h
	return true
}

func captureCurrentWindowPosition() bool {
	if !shouldPersistGUIWindowSize(vtui.ActiveBackend()) || vtui.FrameManager == nil {
		return false
	}
	x, y, ok := vtui.GetWindowPosition()
	if !ok || (config.App.GuiPositionSaved && config.App.GuiPosX == x && config.App.GuiPosY == y) {
		return false
	}
	config.App.GuiPosX = x
	config.App.GuiPosY = y
	config.App.GuiPositionSaved = true
	return true
}

func mergeWorkspaceSessionSave(previous []panel.WorkspaceSessionState, previousActive int, current []panel.WorkspaceSessionState, currentActive int, savePanelSettings, saveCurrentPanel bool) ([]panel.WorkspaceSessionState, int) {
	if len(previous) == 0 || (savePanelSettings && saveCurrentPanel) {
		return current, currentActive
	}
	merged := append([]panel.WorkspaceSessionState(nil), previous...)
	findPrevious := func(state panel.WorkspaceSessionState, index int) int {
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
	if err := saveSessionFileError(path, savePanelSettings, saveCurrentPanel); err != nil {
		vtui.DebugLog("SESSION: %v", err)
	}
}
func saveSessionFileError(path string, savePanelSettings, saveCurrentPanel bool) error {

	if vtui.FrameManager != nil {
		if states, active := panel.CaptureWorkspaceSessions(); len(states) > 0 {
			states, active = mergeWorkspaceSessionSave(panel.LastWorkspaceSessions, panel.LastActiveWorkspace, states, active, savePanelSettings, saveCurrentPanel)
			if !config.App.SavePanelPaths {
				for i := range states {
					states[i].Left.Path, states[i].Right.Path = "", ""
					states[i].Left.Cursor, states[i].Right.Cursor = "", ""
				}
			}
			panel.LastWorkspaceSessions, panel.LastActiveWorkspace = states, active
			if config.App.SavePanelPaths {
				panel.SetLegacyWorkspaceSession(states[0])
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("[EditorSearch]\n")
	fmt.Fprintf(&sb, "Pattern = %s\n", editor.LastEditorSearch)
	fmt.Fprintf(&sb, "Replace = %s\n", editor.LastEditorReplace)
	fmt.Fprintf(&sb, "CaseSensitive = %d\n", map[bool]int{true: 1, false: 0}[editor.LastEditorSearchCase])
	fmt.Fprintf(&sb, "Reverse = %d\n", map[bool]int{true: 1, false: 0}[editor.LastEditorSearchReverse])
	fmt.Fprintf(&sb, "Regexp = %d\n", map[bool]int{true: 1, false: 0}[editor.LastEditorSearchRegexp])
	fmt.Fprintf(&sb, "WholeWord = %d\n", map[bool]int{true: 1, false: 0}[editor.LastEditorSearchWholeWord])

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
	fmt.Fprintf(&sb, "ActivePanel = %d\n", panel.LastActivePanel)
	fmt.Fprintf(&sb, "WidePanel = %d\n", panel.LastWidePanel)
	fmt.Fprintf(&sb, "ShowPanels = %d\n", map[bool]int{true: 1, false: 0}[panel.LastShowPanels])
	fmt.Fprintf(&sb, "ShowLeft = %d\n", map[bool]int{true: 1, false: 0}[panel.LastShowLeft])
	fmt.Fprintf(&sb, "ShowRight = %d\n", map[bool]int{true: 1, false: 0}[panel.LastShowRight])

	sb.WriteString("\n[Panel/Left]\n")
	fmt.Fprintf(&sb, "Folder = %s\n", panel.LastLeftPath)
	fmt.Fprintf(&sb, "CurFile = %s\n", panel.LastLeftCursor)
	fmt.Fprintf(&sb, "ViewMode = %d\n", panel.LastLeftViewMode)
	fmt.Fprintf(&sb, "SortMode = %d\n", panel.LastLeftSortMode)
	fmt.Fprintf(&sb, "SortReverse = %d\n", map[bool]int{true: 1, false: 0}[panel.LastLeftSortRev])
	fmt.Fprintf(&sb, "UseSortGroups = %d\n", map[bool]int{true: 1, false: 0}[panel.LastLeftSortGroups])
	fmt.Fprintf(&sb, "GroupBy = %d\nGroupReverse = %d\nGroupFoldersSeparately = %d\n", panel.LastLeftGroupBy, map[bool]int{true: 1}[panel.LastLeftGroupReverse], map[bool]int{true: 1}[panel.LastLeftGroupFoldersSeparately])

	sb.WriteString("\n[Panel/Right]\n")
	fmt.Fprintf(&sb, "Folder = %s\n", panel.LastRightPath)
	fmt.Fprintf(&sb, "CurFile = %s\n", panel.LastRightCursor)
	fmt.Fprintf(&sb, "ViewMode = %d\n", panel.LastRightViewMode)
	fmt.Fprintf(&sb, "SortMode = %d\n", panel.LastRightSortMode)
	fmt.Fprintf(&sb, "SortReverse = %d\n", map[bool]int{true: 1, false: 0}[panel.LastRightSortRev])
	fmt.Fprintf(&sb, "UseSortGroups = %d\n", map[bool]int{true: 1, false: 0}[panel.LastRightSortGroups])
	fmt.Fprintf(&sb, "GroupBy = %d\nGroupReverse = %d\nGroupFoldersSeparately = %d\n", panel.LastRightGroupBy, map[bool]int{true: 1}[panel.LastRightGroupReverse], map[bool]int{true: 1}[panel.LastRightGroupFoldersSeparately])
	panel.WriteWorkspaceSessions(&sb, panel.LastWorkspaceSessions, panel.LastActiveWorkspace)

	return config.WriteUserFileAtomically(path, []byte(sb.String()), 0600)
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
	plughost.GlobalPluginManager = plughost.NewPluginManager(&coreAPI{})
	plughost.GlobalPluginManager.LoadInternal()
	return fusefs.RunCLI(os.Args)
}
