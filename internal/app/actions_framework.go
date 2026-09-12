package app

import (
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/vtui"
)

// actionContextHelp resolves the same contextual topic as FrameManager's F1
// fallback. It deliberately does not synthesize another F1 event: frames may
// route synthesized keys back through configured hotkeys, where a user binding
// of F1 to term.App.Help would recursively invoke this action.
func actionContextHelp() bool {
	if !contextHelpActionAvailable() {
		return false
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		return false
	}

	topic := top.GetHelp()
	if container, ok := top.(vtui.FocusContainer); ok {
		if focused := container.GetFocusedItem(); focused != nil && focused.GetHelp() != "" {
			topic = focused.GetHelp()
		}
	}
	if topic == "" {
		topic = "Contents"
	}
	if vtui.GlobalHelpEngine == nil {
		return false
	}
	vtui.FrameManager.Push(vtui.NewHelpView(vtui.GlobalHelpEngine, topic))
	return true
}

func contextHelpActionAvailable() bool {
	if vtui.FrameManager == nil {
		return false
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		return false
	}
	_, alreadyHelp := top.(commandPaletteHelpFrame)
	return !alreadyHelp
}

func mainMenuActionAvailable() bool {
	if vtui.FrameManager == nil {
		return false
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil || vtui.FrameManager.GetActiveMenuBar() == nil {
		return false
	}
	return !top.IsModal() || top.GetType() == vtui.TypeMenu || top.GetMenuBar() != nil
}

// actionActivateMainMenu implements the same eligibility rules as the native
// F9 fallback. The palette executes it only after its own dialog has gone away,
// so GetTopFrame refers to the screen the user was working in.
func actionActivateMainMenu() bool {
	return activateMainMenuAt(-1, false)
}

// activateMainMenuAt raises the active menu bar. A non-negative position is an
// explicit top-level menu selected by a caller such as Shift+F10; -1 keeps the
// ordinary F9/palette behavior. openSubMenu drops the selected menu down right
// away, which is what far2l's ShellOptions(1) does for Shift+F10 and what
// ShellOptions(0) deliberately does not do for F9.
func activateMainMenuAt(requestedPos int, openSubMenu bool) bool {
	if !mainMenuActionAvailable() {
		return false
	}
	top := vtui.FrameManager.GetTopFrame()
	menu := vtui.FrameManager.GetActiveMenuBar()
	if menu.Active {
		return true
	}

	menu.Active = true
	if len(menu.Items) > 0 {
		selectPos := requestedPos
		if selectPos < 0 {
			selectPos = menu.SelectPos
		}
		if requestedPos < 0 {
			if panels, ok := top.(*panel.PanelsFrame); ok {
				// panel.PanelsFrame owns F9 before vtui's fallback and always opens the
				// fixed-side menu belonging to the active panel. Do not retain the
				// previously visited menu when the command comes from the palette.
				selectPos = 0
				if panels.ActiveIdx == 1 {
					selectPos = 4
				}
				if selectPos >= len(menu.Items) {
					selectPos = 0
				}
			}
		}
		if selectPos < 0 || selectPos >= len(menu.Items) {
			selectPos = 0
		}
		menu.SelectPos = selectPos
		if openSubMenu {
			menu.ActivateSubMenu(selectPos)
		}
	}
	vtui.FrameManager.Redraw()
	return true
}

func multipleWorkspacesAvailable() bool {
	return vtui.FrameManager != nil && len(vtui.FrameManager.Screens) > 1
}

func actionWorkspaceNew() bool {
	if vtui.FrameManager == nil {
		return false
	}
	if vtui.FrameManager.HandleSemanticAction(map[string]any{
		"action": "workspace.new",
		"target": "workspace-new",
	}) {
		return true
	}

	// Full-screen editor, viewer, image and queue workspaces do not keep the
	// panel.PanelsFrame in their own frame stack, so vtui's active-stack CmResize
	// broadcast cannot reach the frame that knows how to clone panels. Reuse
	// the same MRU-aware resolver used by the rest of f4 and ask that concrete
	// panels frame to perform its ordinary, well-tested fork operation.
	return forkNearestPanelsFrame()
}

// forkNearestPanelsFrame clones the panels the current workspace was opened
// from into a new workspace, wherever those panels currently live. Returns
// false only when no workspace holds a panel.PanelsFrame at all, which leaves the
// caller free to report the request as unhandled.
func forkNearestPanelsFrame() bool {
	panels := panel.FindPanelsFrameAnyScreen()
	return panels != nil && panels.HandleCommand(vtui.CmResize, "fork")
}

// handleWorkspaceForkCommand serves vtui's fork request -- CmResize carrying
// the string "fork", as emitted by FrameManager's native Ctrl+N fallback and
// by a click on the "+" workspace tab. Only panel.PanelsFrame implements that
// command, and FrameManager routes it down the *active* screen's frame stack
// only, so on a workspace that holds no panels of its own the request used to
// die unhandled after the framework had already flashed the screen for it
// (issue #528). Full-screen frames call this from HandleCommand so the chord
// forks the panels they were opened from instead.
//
// It forks those panels directly rather than calling actionWorkspaceNew:
// that action first hands the request back to
// FrameManager.HandleSemanticAction, which re-emits this very command into
// this very frame stack, and the two would recurse.
func handleWorkspaceForkCommand(cmd int, args any) bool {
	if cmd != vtui.CmResize {
		return false
	}
	if name, ok := args.(string); !ok || name != "fork" {
		return false
	}
	return forkNearestPanelsFrame()
}

func activeWorkspaceNumber() (int, bool) {
	if vtui.FrameManager == nil {
		return 0, false
	}
	index := vtui.FrameManager.ActiveIdx
	if index < 0 || index >= len(vtui.FrameManager.Screens) {
		return 0, false
	}
	return vtui.FrameManager.Screens[index].Number, true
}

func workspaceSemanticTarget(number int) string {
	return fmt.Sprintf("workspace-tab-%d", number)
}

func actionActivateWorkspaceNumber(number int) bool {
	if vtui.FrameManager == nil || number < 1 {
		return false
	}
	// Resolve the stable display number at execution time. Workspace order may
	// change while a palette is open; a captured slice index could then activate
	// the wrong tab.
	for _, screen := range vtui.FrameManager.Screens {
		if screen.Number != number {
			continue
		}
		return vtui.FrameManager.HandleSemanticAction(map[string]any{
			"action": "workspace.activate",
			"target": workspaceSemanticTarget(number),
		})
	}
	return false
}

func actionWorkspaceClose() bool {
	number, ok := activeWorkspaceNumber()
	return ok && actionWorkspaceCloseNumber(number)
}

func workspaceByNumber(number int) *vtui.AppScreen {
	if vtui.FrameManager == nil || number < 1 {
		return nil
	}
	for _, screen := range vtui.FrameManager.Screens {
		if screen != nil && screen.Number == number {
			return screen
		}
	}
	return nil
}

func queueFrameInWorkspace(screen *vtui.AppScreen) *fileops.QueueFrame {
	if screen == nil {
		return nil
	}
	for index := len(screen.Frames) - 1; index >= 0; index-- {
		if queue, ok := screen.Frames[index].(*fileops.QueueFrame); ok {
			return queue
		}
	}
	return nil
}

// actionWorkspaceCloseNumber resolves the stable workspace number at
// execution time. Queue may be underneath contextual Help, or the target may
// be a background workspace selected by a dynamic palette entry; both cases
// must preserve fileops.QueueFrame's active-operation veto before semantic close.
func actionWorkspaceCloseNumber(number int) bool {
	screen := workspaceByNumber(number)
	if screen == nil {
		return false
	}
	// Keep f4 alive while the only workspace containing file panels exists.
	// Closing it while editor/viewer workspaces remain leaves the next last
	// workspace without panel.PanelsFrame, so its final Ctrl+W emits CmQuit directly
	// and bypasses panel.PanelsFrame's exit-confirmation policy (issue #531).
	if panel.IsOnlyPanelsWorkspace(screen) {
		return true
	}
	if queue := queueFrameInWorkspace(screen); queue != nil && queue.VetoCloseWhileActive() {
		return true
	}
	return vtui.FrameManager.HandleSemanticAction(map[string]any{
		"action": "workspace.close",
		"target": workspaceSemanticTarget(number),
	})
}

func actionWorkspaceOffset(offset int) bool {
	if vtui.FrameManager == nil || len(vtui.FrameManager.Screens) < 2 {
		return false
	}
	index := vtui.FrameManager.ActiveIdx + offset
	if index < 0 {
		index = len(vtui.FrameManager.Screens) - 1
	} else if index >= len(vtui.FrameManager.Screens) {
		index = 0
	}
	return actionActivateWorkspaceNumber(vtui.FrameManager.Screens[index].Number)
}

func actionWorkspaceNext() bool {
	return actionWorkspaceOffset(1)
}

func actionWorkspacePrevious() bool {
	return actionWorkspaceOffset(-1)
}

func actionWorkspaceList() bool {
	if vtui.FrameManager == nil || len(vtui.FrameManager.Screens) == 0 {
		return false
	}
	return vtui.FrameManager.HandleSemanticAction(map[string]any{
		"action": "workspace.menu",
		"target": "workspace-counter",
	})
}

func dumpScreenTo(path string) error {
	if vtui.FrameManager == nil || vtui.FrameManager.Screen() == nil {
		return fmt.Errorf("screen buffer is not initialized")
	}
	File, err := os.Create(path)
	if err != nil {
		return err
	}
	vtui.FrameManager.Screen().Dump(File)
	return File.Close()
}

// screenDumpCandidateDirs lists, in priority order, where actionScreenDump
// tries to write vtui.screen.log.
//
// os.UserHomeDir() alone is not enough under Wine: on the "windows" build of
// f4 (which is what a Wine tty session runs), it follows Windows semantics —
// %USERPROFILE%, or %HOMEDRIVE%+%HOMEPATH% — which resolve inside the
// wineprefix (e.g. `C:\users\<name>`, i.e.
// `<WINEPREFIX>/drive_c/users/<name>` on the Unix side), not the real Unix
// $HOME the user is used to looking in. Nothing about that is broken, but
// it is exactly where issue #536 testing tripped: the file was written
// (or the write silently failed) somewhere other than where it was searched
// for. The executable's own directory is added first because it is the one
// location a Wine user unambiguously knows without having to think about
// prefix layout — they just ran the .exe from there.
func screenDumpCandidateDirs() []string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dirs = append(dirs, filepath.Dir(exe))
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		dirs = append(dirs, home)
	}
	dirs = append(dirs, os.TempDir())
	return dirs
}

func actionScreenDump() bool {
	var attempts []string
	for _, dir := range screenDumpCandidateDirs() {
		path := filepath.Join(dir, "vtui.screen.log")
		if err := dumpScreenTo(path); err != nil {
			attempts = append(attempts, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		vtui.DebugLog("FM: Screen dump saved to %s", path)
		return true
	}
	// Every candidate failed — this used to return false with zero trace of
	// why. Record every attempted path and its error in the in-memory log
	// ring (GetCurrentLogs()) so it survives even without VTUI_DEBUG set,
	// which is the only way anyone would have known this action ran at all.
	vtui.DebugLog("FM: Screen dump failed, tried: %s", strings.Join(attempts, " | "))
	return false
}
