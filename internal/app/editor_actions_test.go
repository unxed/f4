package app

// Editor tests whose subject is the action, not the editor: each fetches a
// registered action by name and runs its handler. The registry is filled by
// action_table.go's init in this package, so a test in internal/editor would
// look into an empty one and pass by finding nothing.

import (
	"time"

	"github.com/unxed/f4/internal/keymap"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The same three-line constructor internal/editor's own tests use; a package
// cannot import another's tests.
func newActionTestEditor(t *testing.T, text string) *editor.EditorView {
	t.Helper()
	discardStaleUITasks()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	ev := editor.NewEditorView(piecetable.New([]byte(text)), nil, "test.txt")
	t.Cleanup(ev.Close)
	ev.SetPosition(0, 0, 79, 24)
	return ev
}

func TestEditorBase64ActionsExposeF11AndEditCommands(t *testing.T) {
	menu, ok := GetAction("Editor.Base64Menu")
	if !ok || menu.Area != "Editor" || len(menu.DefaultKeys) != 1 || menu.DefaultKeys[0] != "F11" {
		t.Fatalf("Base64 menu action = %#v, present=%t", menu, ok)
	}
	for _, name := range []string{"Editor.Base64Encode", "Editor.Base64Decode"} {
		action, ok := GetAction(name)
		if !ok || action.Area != "Editor" || action.MenuPath != "Edit" || action.Handler == nil {
			t.Fatalf("Base64 action %s = %#v, present=%t", name, action, ok)
		}
	}
}

func TestEditorCalculateExpressionActionExposesEditCommand(t *testing.T) {
	calc, ok := GetAction("Editor.CalculateExpression")
	if !ok || calc.Area != "Editor" || calc.MenuPath != "Edit" || calc.Handler == nil {
		t.Fatalf("CalculateExpression action = %#v, present=%t", calc, ok)
	}
}

func TestEditorCalculateExpressionActionReplacesSelectionWithResult(t *testing.T) {
	ev := newActionTestEditor(t, "2 + 2 * 3")
	vtui.FrameManager.Push(ev)
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorLine = 0
	ev.CursorPos = len("2 + 2 * 3")

	if !RunAction("Editor.CalculateExpression") {
		t.Fatal("Editor.CalculateExpression did not run on the editor")
	}

	if got, want := ev.GetText(), "8"; got != want {
		t.Fatalf("text after Editor.CalculateExpression = %q, want %q", got, want)
	}
}

func TestEditorCopyPasteActionsWorkWhileEditMenuIsOpen(t *testing.T) {
	ev := newActionTestEditor(t, "abc")
	vtui.FrameManager.Push(ev)

	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorPos = len("abc")
	menu := vtui.NewVMenu("Edit")
	vtui.FrameManager.Push(menu)
	t.Cleanup(menu.Close)

	if !RunAction("Editor.Copy") {
		t.Fatal("Editor.Copy did not resolve the editor below the menu")
	}
	terminal.WaitForAsyncClipboard()
	if got := vtui.GetClipboard(); got != "abc" {
		t.Fatalf("menu copy clipboard = %q, want %q", got, "abc")
	}

	ev.SelActive = false
	ev.CursorPos = 0
	if !RunAction("Editor.Paste") {
		t.Fatal("Editor.Paste did not resolve the editor below the menu")
	}
	if got, want := ev.GetText(), "abcabc"; got != want {
		t.Fatalf("menu paste text = %q, want %q", got, want)
	}
}

// f4#1463 (DkmS1953): the words go to the clipboard and the number stays.
func TestEditorAmountInWordsActionCopiesTheWords(t *testing.T) {
	ev := newActionTestEditor(t, "5 руб")
	vtui.FrameManager.Push(ev)
	ev.SelActive = true
	ev.SelAnchorOffset = 0
	ev.CursorLine = 0
	ev.CursorPos = len("5 руб")

	if !RunAction("Editor.AmountInWords") {
		t.Fatal("Editor.AmountInWords did not run on the editor")
	}
	if got, want := ev.GetText(), "5 руб"; got != want {
		t.Fatalf("text after Editor.AmountInWords = %q, want %q", got, want)
	}
	if got, want := vtui.GetClipboard(), "пять рублей"; got != want {
		t.Fatalf("clipboard after Editor.AmountInWords = %q, want %q", got, want)
	}
}

func TestEditorSortLinesAction(t *testing.T) {
	sortAction, ok := GetAction("Editor.SortLines")
	if !ok {
		t.Fatal("Editor.SortLines is not registered")
	}
	if sortAction.Area != "Editor" || sortAction.MenuPath != "Edit" ||
		!sortAction.MenuSeparatorBefore || sortAction.Handler == nil {
		t.Fatalf("sort action = %+v", sortAction)
	}
}

func TestEditor_DuplicateLine_Hotkey(t *testing.T) {
	action, ok := GetAction("Editor.DuplicateLine")
	if !ok {
		t.Fatal("Editor.DuplicateLine is not registered")
	}
	if action.Area != "Editor" || action.MenuPath != "Edit" ||
		len(action.DefaultKeys) != 1 || action.DefaultKeys[0] != "CtrlShiftD" {
		t.Fatalf("registration = %+v; want Editor/Edit bound to CtrlShiftD", action)
	}

	ev := newActionTestEditor(t, "line1\nline2")
	ev.CursorLine = 0
	ev.CursorPos = 0

	pressKey(ev, &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_D,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	if got, want := ev.Pt.String(), "line1\nline1\nline2"; got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
}

func TestEditor_MoveLine_Hotkeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		action  string
		key     uint16
		defKey  string
		initial string
		want    string
		line    int
	}{
		{"up", "Editor.MoveLineUp", vtinput.VK_UP, "CtrlShiftUp", "one\ntwo", "two\none", 1},
		{"down", "Editor.MoveLineDown", vtinput.VK_DOWN, "CtrlShiftDown", "one\ntwo", "two\none", 0},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			action, ok := GetAction(tc.action)
			if !ok {
				t.Fatalf("%s is not registered", tc.action)
			}
			if action.Area != "Editor" || action.MenuPath != "Edit" ||
				len(action.DefaultKeys) != 1 || action.DefaultKeys[0] != tc.defKey {
				t.Fatalf("registration = %+v; want Editor/Edit bound to %s", action, tc.defKey)
			}

			ev := newActionTestEditor(t, tc.initial)
			ev.CursorLine = tc.line
			ev.CursorPos = 0

			pressKey(ev, &vtinput.InputEvent{
				Type:            vtinput.KeyEventType,
				KeyDown:         true,
				VirtualKeyCode:  tc.key,
				ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
			})

			if got := ev.Pt.String(); got != tc.want {
				t.Fatalf("buffer = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEditor_MultiCursor_OccurrenceActionsAreRegistered(t *testing.T) {
	for name, key := range map[string]string{
		"Editor.AddCursorAtNextOccurrence": "CtrlShiftN",
		"Editor.SelectAllOccurrences":      "CtrlShiftL",
	} {
		action, ok := GetAction(name)
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if action.Area != "Editor" || action.MenuPath != "Edit" ||
			len(action.DefaultKeys) != 1 || action.DefaultKeys[0] != key {
			t.Errorf("%s = %+v; want Editor/Edit bound to %s", name, action, key)
		}
	}
}

// Every name the backends can produce for a Del key must resolve to the same
// action as its counterpart, so a rebound key keeps working too.
func TestHotkeyDelAliasesResolve(t *testing.T) {
	hm := keymap.NewHotkeyManager("")

	cases := []struct{ area, key, want string }{
		{"Editor", "ShiftDel", "Editor.Cut"},
		{"Editor", "ShiftNumDel", "Editor.Cut"},
	}
	for _, c := range cases {
		if got := hm.GetAction(c.area, c.key); got != c.want {
			t.Errorf("GetAction(%q, %q) = %q, want %q", c.area, c.key, got, c.want)
		}
	}

	// An explicit binding still wins over the alias.
	hm.Bind("Editor", "ShiftNumDel", "Editor.Copy")
	if got := hm.GetAction("Editor", "ShiftNumDel"); got != "Editor.Copy" {
		t.Errorf("explicit binding overridden by alias: got %q", got)
	}
	for _, key := range []string{"CtrlDel", "CtrlNumDel"} {
		if got := hm.GetAction("Editor", key); got != "Editor.DeleteWordForward" {
			t.Errorf("GetAction(%q, %q) = %q, want Editor.DeleteWordForward", "Editor", key, got)
		}
	}
}

func TestEditorFAR3HotkeysResolve(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	hm.InitDefaults()
	cases := map[string]string{
		"CtrlN":          "Editor.MoveToScreenTop",
		"CtrlE":          "Editor.MoveToScreenBottom",
		"CtrlK":          "Editor.DeleteToLineEnd",
		"AltD":           "Editor.DeleteToLineEnd",
		"CtrlBS":         "Editor.DeleteWordBackward",
		"CtrlDel":        "Editor.DeleteWordForward",
		"CtrlT":          "Editor.DeleteSpacersForward",
		"CtrlD":          "Editor.DeleteBlock",
		"CtrlP":          "Editor.CopyBlockToCursor",
		"CtrlM":          "Editor.MoveBlockToCursor",
		"AltU":           "Editor.ShiftBlockLeft",
		"AltI":           "Editor.ShiftBlockRight",
		"CtrlShiftEnter": "Editor.InsertPassivePanelFileName",
		"AltF7":          "Editor.SearchPrevious",
		"AltF8":          "Editor.GoTo",
		"CtrlO":          "Panel.ToggleWorkspace",
		"ShiftF10":       "Editor.SaveAndQuit",
	}
	for key, want := range cases {
		if got := hm.GetAction("Editor", key); got != want {
			t.Errorf("Editor/%s = %q, want %q", key, got, want)
		}
	}
}

func TestEditorCtrlOOpensSwitchableTerminalWorkspace(t *testing.T) {
	fm := vtui.FrameManager
	ev := newActionTestEditor(t, "text")

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeOwn
	fm.Push(pf)
	fm.Push(ev)

	if !RunAction("Panel.ToggleWorkspace") {
		t.Fatal("Ctrl+O workspace action was not handled from the editor")
	}
	if len(fm.Screens) != 2 {
		t.Fatalf("Ctrl+O created %d workspaces, want a separate terminal workspace", len(fm.Screens))
	}
	clone, ok := fm.GetTopFrame().(*panel.PanelsFrame)
	if !ok || clone == pf {
		t.Fatalf("active workspace frame = %T, want a forked terminal PanelsFrame", fm.GetTopFrame())
	}
	defer clone.Close()
	if clone.ShowPanels {
		t.Error("new terminal workspace still shows panels")
	}
	if !pf.ShowPanels {
		t.Error("opening the terminal workspace hid the original panels")
	}
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, clone.Panels[1].(*panel.FileSystemPanel))
}

// discardStaleUITasks drops the UI tasks earlier tests left queued: Init does
// not empty TaskChan, and a macro test's assign dialog or " Macro " message
// would otherwise be pushed over this test's editor the first time a helper
// pumps the queue (f4 train #1995, darwin/arm64). TaskChan is fed by a pump
// goroutine, so an empty select with default can miss a task that is queued
// but not offered yet: the queue counts as empty after a short quiet spell.
func discardStaleUITasks() {
	for {
		select {
		case <-vtui.FrameManager.TaskChan:
		case <-time.After(20 * time.Millisecond):
			return
		}
	}
}

// newEditorConsoleWorkspaces is the layout F4 produces: the panels in one
// workspace and the editor opened from them in a workspace of its own.
func newEditorConsoleWorkspaces(t *testing.T) (*panel.PanelsFrame, *editor.EditorView) {
	t.Helper()
	ev := newActionTestEditor(t, "text")
	pf := panel.NewPanelsFrame()
	t.Cleanup(pf.Close)
	pf.ResizeConsole(80, 25)
	pf.ShellMode = terminal.ShellModeOwn
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.AddScreen(ev)
	paneltest.WaitForLoad(t, pf.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, pf.Panels[1].(*panel.FileSystemPanel))
	if top := vtui.FrameManager.GetTopFrame(); top != vtui.Frame(ev) {
		title := ""
		if top != nil {
			title = top.GetTitle()
		}
		t.Fatalf("setup: top frame %T %q, want the editor", top, title)
	}
	if fm := vtui.FrameManager; len(fm.Screens) != 2 || fm.ActiveIdx != 1 {
		t.Fatalf("setup: %d workspaces, active %d; want the editor in its own workspace, active", len(fm.Screens), fm.ActiveIdx)
	}
	return pf, ev
}

// f4#1973: Ctrl+O in the editor peeks at the console of the workspace the
// editor was opened from, and Ctrl+O there returns to the editor with the
// panels shown again.
func TestEditorCtrlOShowsConsoleOfOriginWorkspaceAndReturns(t *testing.T) {
	fm := vtui.FrameManager
	pf, _ := newEditorConsoleWorkspaces(t)

	if !RunAction("Panel.ToggleWorkspace") {
		t.Fatal("Ctrl+O workspace action was not handled from the editor")
	}
	if len(fm.Screens) != 2 {
		t.Fatalf("Ctrl+O created a workspace: %d workspaces, want the two there were", len(fm.Screens))
	}
	if fm.ActiveIdx != 0 || fm.GetTopFrame() != vtui.Frame(pf) {
		t.Fatalf("after Ctrl+O the active workspace is %d (top %T), want the panels' workspace 0", fm.ActiveIdx, fm.GetTopFrame())
	}
	if pf.ShowPanels {
		t.Fatal("Ctrl+O from the editor left the origin workspace showing panels, not its console")
	}

	if !RunAction("Panel.Toggle") {
		t.Fatal("Ctrl+O was not handled in the console")
	}
	if fm.ActiveIdx != 1 {
		t.Fatalf("second Ctrl+O switched to workspace %d, want back to the editor (1)", fm.ActiveIdx)
	}
	if !pf.ShowPanels {
		t.Error("second Ctrl+O did not give the panels back")
	}
}

// Ctrl+Tab to the editor and Ctrl+O again must not forget that the panels were
// visible before the first Ctrl+O.
func TestEditorCtrlOAfterCtrlTabStillRestoresPanels(t *testing.T) {
	fm := vtui.FrameManager
	pf, _ := newEditorConsoleWorkspaces(t)

	RunAction("Panel.ToggleWorkspace")
	fm.SwitchScreen(1) // Ctrl+Tab back to the editor, console still open
	RunAction("Panel.ToggleWorkspace")
	if fm.ActiveIdx != 0 || pf.ShowPanels {
		t.Fatalf("second Ctrl+O from the editor: workspace %d, ShowPanels=%t; want the console of workspace 0", fm.ActiveIdx, pf.ShowPanels)
	}
	RunAction("Panel.Toggle")
	if fm.ActiveIdx != 1 || !pf.ShowPanels {
		t.Fatalf("return to the editor: workspace %d, ShowPanels=%t; want the editor with the panels restored", fm.ActiveIdx, pf.ShowPanels)
	}
}

// The editor closed while its console peek was still open: Ctrl+O is then the
// ordinary panels toggle again.
func TestConsoleCtrlOWithoutEditorTogglesPanels(t *testing.T) {
	fm := vtui.FrameManager
	pf, _ := newEditorConsoleWorkspaces(t)

	RunAction("Panel.ToggleWorkspace")
	fm.CloseScreen(1)
	fm.SwitchScreen(0)
	if pf.ShowPanels {
		t.Fatal("setup: panels are visible, want the console")
	}
	RunAction("Panel.Toggle")
	if !pf.ShowPanels || fm.ActiveIdx != 0 {
		t.Fatalf("Ctrl+O after the editor closed: ShowPanels=%t, workspace %d; want the panels back in workspace 0", pf.ShowPanels, fm.ActiveIdx)
	}
}
