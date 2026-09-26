package app

import (
	"errors"
	"github.com/unxed/f4/internal/panel"
	"os"
	"testing"
	"time"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/fusefs"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// pressKey is testutil.PressKey with this package's macro filter, which is
// where action hotkeys are dispatched. The managers are created on demand
// because most tests never touch them.
func pressKey(f vtui.Frame, e *vtinput.InputEvent) bool {
	if keymap.GlobalHotkeysMgr == nil {
		keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	}
	if macro.MacroMgr == nil {
		macro.MacroMgr = macro.NewMacroManager("")
	}
	return testutil.PressKey(f, e, func(e *vtinput.InputEvent) bool {
		return macroFilter(macro.MacroMgr, e)
	})
}

// preserveActionRegistry keeps tests that register synthetic actions from
// leaking them into later tests or the next -count iteration. The copy itself
// lives in internal/action, which is the only package that can reach the maps.
func preserveActionRegistry(t *testing.T) {
	t.Helper()
	t.Cleanup(action.Snapshot())
}

// runAsF4Env makes this test binary run f4 itself rather than the tests. A test
// that needs the whole startup path -- a terminal on stdin, the session daemon,
// the restored session -- starts the binary it is already running as instead of
// building ./cmd/f4 a second time. The daemon f4 spawns re-executes the same
// binary with --server and inherits the variable, so it runs as f4 too. None of
// the test seams below are installed there: that process is the application.
const runAsF4Env = "F4_TEST_RUN_AS_F4"

// setProcessNameTestOutEnv makes this test binary call setProcessName and
// report the comm it produced, instead of running the tests. It is checked
// before anything else in TestMain for the same reason runAsF4Env's check
// is: this runs on the process's leader OS thread the way Main's own call
// does, which m.Run()'s test scheduling no longer guarantees once it starts
// (f4 #1390 — see procname_linux.go).
const setProcessNameTestOutEnv = "F4_TEST_SET_PROCESS_NAME_OUT"

func TestMain(m *testing.M) {
	if out := os.Getenv(setProcessNameTestOutEnv); out != "" {
		setProcessName()
		comm, err := os.ReadFile("/proc/self/comm")
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(out, comm, 0600); err != nil { // #nosec G703 -- out is a t.TempDir() path the test itself built, not untrusted input.
			os.Exit(3)
		}
		os.Exit(0)
	}
	if os.Getenv(runAsF4Env) != "" {
		Main()
		os.Exit(0)
	}
	os.Exit(testutil.Main(m, installTestSeams, unmountTestFilesystems))
}

// installTestSeams points this package's escape hatches somewhere harmless for
// the duration of the run.
func installTestSeams() {
	// f4 extends vtui's palette past its last index, and any widget drawn with
	// one of the extra colours indexes past the end of the default one. Which
	// test draws first depends on the shuffle seed, so the palette is sized
	// here rather than left to whichever test happens to grow it.
	theme.SetDefaultF4Palette()

	vfs.InitSudoClient("/usr/bin/f4", "")

	// main() wires this before it reads the config directory; without it here
	// the tests would exercise the "resolver not wired" fallback instead of the
	// path a running f4 takes.
	config.Executable = update.Executable
	keymap.Suspended = keyRemapSuspended

	// SetupUI installs this in production; the test binary never runs it, and
	// without it every action label falls back to its English spelling.
	action.Localize = i18n.Msg
	// internal/editor declares what it needs from above; this is the root
	// filling it in. Each is one call site inside the editor.
	editor.RunAction = RunAction
	editor.LookupHotkey = func(e *vtinput.InputEvent) bool { return macroLookupHotkey(macro.MacroMgr, e) }
	editor.MenuBarItems = BuildMenuBarItems
	editor.CrossAttrs = EditorCrossAttrs
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

	// Unit tests must never hand control to the user's desktop. Individual
	// tests that exercise these routes install per-dialog/per-frame recorders.
	panel.DefaultExternalUICommandRunner = func(string, []string, string) error { return nil }
	dialog.DefaultNativePropertiesOpener = func(string) error { return nil }

	// Frames must not fork the user's shell during unit tests; the few
	// tests that exercise the term.PTY path construct one explicitly.
	panel.SpawnLocalShellPTY = false

	// Toast behavior is still exercised through vtui's real asynchronous
	// setup and expiry paths, but unit tests do not need production-length
	// display times. Keep a small observable window: tests may observe another
	// effect of the same UI task (for example, clipboard contents) before they
	// pump the nested toast task, and a 1 ms toast can expire in that gap.
	toast.DurationOverride = func(time.Duration) time.Duration {
		const minimumObservableToastDuration = 100 * time.Millisecond
		return minimumObservableToastDuration
	}
	fileops.QueueShowToast = func(string, time.Duration) {}

	// os.UserConfigDir ignores XDG_CONFIG_HOME and APPDATA on darwin, so the
	// seam is what actually isolates the suite from the developer's profile.
	if dir := testutil.ConfigDir(); dir != "" {
		config.UserConfigDir = func() (string, error) { return dir, nil }
		config.ResetConfigDirForTest()
	}
}

func unmountTestFilesystems() error {
	return errors.Join(fusefs.UnmountAll()...)
}
