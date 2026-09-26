package app

// Editor tests whose subject is the action, not the editor: each fetches a
// registered action by name and runs its handler. The registry is filled by
// action_table.go's init in this package, so a test in internal/editor would
// look into an empty one and pass by finding nothing.

import (
	"github.com/unxed/f4/internal/keymap"
	"testing"

	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The same three-line constructor internal/editor's own tests use; a package
// cannot import another's tests.
func newActionTestEditor(t *testing.T, text string) *editor.EditorView {
	t.Helper()
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
		{"Editor", "CtrlDel", "Editor.DeleteSpacersForward"},
		{"Editor", "CtrlNumDel", "Editor.DeleteSpacersForward"},
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
}
