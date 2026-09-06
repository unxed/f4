package main

import (
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestDialogTableUsesThemePalette(t *testing.T) {
	table := vtui.NewTable(0, 0, 20, 5, []vtui.TableColumn{{Title: "Value", Width: 20}})
	useDialogTableColors(table)

	if table.ColorTextIdx != vtui.ColDialogText ||
		table.ColorSelectedTextIdx != vtui.ColDialogSelectedButton ||
		table.ColorItemSelectTextIdx != vtui.ColDialogHighlightText ||
		table.ColorItemSelectCursorIdx != vtui.ColDialogHighlightSelectedButton ||
		table.ColorTitleIdx != vtui.ColDialogHighlightText ||
		table.ColorBoxIdx != vtui.ColDialogBox {
		t.Fatal("dialog table does not use the dialog theme palette")
	}
}

func TestHotkeyRow(t *testing.T) {
	row := hotkeyRow{
		Action:    "Test.Action",
		Label:     "Test Label",
		Area:      "Common",
		Key:       "F12",
		Condition: "EmptyCommandLine",
		Desc:      "Description",
	}

	if row.GetCellText(0) != "Test Label" {
		t.Errorf("Expected Test Label")
	}
	if row.GetCellText(1) != "F12" {
		t.Errorf("Expected F12")
	}
	if row.GetCellText(2) != "Common" {
		t.Errorf("Expected Common")
	}
	if row.GetCellText(3) != "EmptyCommandLine" {
		t.Errorf("Expected EmptyCommandLine")
	}
	if row.GetCellText(4) != "Description" {
		t.Errorf("Expected Description")
	}
}

func TestHotkeyAssignFramePreservesRightCtrl(t *testing.T) {
	previous := GlobalHotkeysMgr
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	hm := NewHotkeyManager("")
	f := NewHotkeyAssignFrame(hm, "File.Attributes", "Shell", nil)
	ctrlABefore, ctrlAExists := hm.Bindings["Shell"]["CtrlA"]

	rightCtrlA := &vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_A,
		ControlKeyState: vtinput.RightCtrlPressed,
	}
	if !f.ProcessKey(rightCtrlA) {
		t.Fatal("Right Ctrl+A was not consumed by the assignment dialog")
	}
	if got := hm.Bindings["Shell"]["RCtrlA"]; got != "File.Attributes" {
		t.Fatalf("captured Right Ctrl+A = %q, want File.Attributes under RCtrlA", got)
	}
	if got, exists := hm.Bindings["Shell"]["CtrlA"]; exists != ctrlAExists || (exists && got != ctrlABefore) {
		t.Fatalf("Right Ctrl+A changed the normalized CtrlA binding from %q to %q", ctrlABefore, got)
	}

	left := NewHotkeyAssignFrame(hm, "File.Attributes", "Shell", nil)
	leftCtrlA := *rightCtrlA
	leftCtrlA.ControlKeyState = vtinput.LeftCtrlPressed
	if !left.ProcessKey(&leftCtrlA) {
		t.Fatal("Left Ctrl+A was not consumed by the assignment dialog")
	}
	if got := hm.Bindings["Shell"]["CtrlA"]; got != "File.Attributes" {
		t.Fatalf("captured Left Ctrl+A = %q, want File.Attributes under CtrlA", got)
	}
}

func TestHotkeyDialogSizeForScreen(t *testing.T) {
	if gotW, gotH := hotkeyDialogSizeForScreen(200, 60); gotW != 196 || gotH != 58 {
		t.Fatalf("large screen size = %dx%d, want 196x58", gotW, gotH)
	}
	if gotW, gotH := hotkeyDialogSizeForScreen(80, 25); gotW != 76 || gotH != 23 {
		t.Fatalf("small screen size = %dx%d, want 76x23", gotW, gotH)
	}
}

func TestHotkeyTableColumnsFitContentWhenSpaceAllows(t *testing.T) {
	rows := []hotkeyRow{{
		Label:     "A long localized command label",
		Key:       "Ctrl+Shift+PgDn",
		Area:      "Terminal",
		Condition: "CommandLineNotEmpty",
		Desc:      "A long localized description that remains in the flexible column",
	}}
	columns := hotkeyTableColumns(rows, 160)
	for col := 0; col < 4; col++ {
		if got, want := columns[col].Width, vtui.StringWidth(rows[0].GetCellText(col)); got < want {
			t.Errorf("column %d width = %d, want at least %d", col, got, want)
		}
	}
}

func TestSelectedHotkeyRowUsesDisplayedTablePosition(t *testing.T) {
	rows := []hotkeyRow{
		{Label: "Zulu action"},
		{Label: "Alpha action"},
		{Label: "Middle action"},
	}
	table := vtui.NewTable(0, 0, 80, 10, []vtui.TableColumn{{Title: "Command", Width: 40}})
	table.Sortable = true
	table.SetRows([]vtui.TableRow{rows[0], rows[1], rows[2]})
	table.SetSort(0, true)

	row, ok := selectedHotkeyRow(table, rows)
	if !ok || row.Label != "Alpha action" {
		t.Fatalf("sorted display row selected %q, want Alpha action", row.Label)
	}

	table.QuickSearch = true
	table.SetSearchText("middle")
	table.SelectPos = 0
	row, ok = selectedHotkeyRow(table, rows)
	if !ok || row.Label != "Middle action" {
		t.Fatalf("filtered display row selected %q, want Middle action", row.Label)
	}
}

func TestSelectedHotkeyRowAtUsesActionPosition(t *testing.T) {
	rows := []hotkeyRow{
		{Label: "Zulu action"},
		{Label: "Alpha action"},
		{Label: "Middle action"},
	}
	table := vtui.NewTable(0, 0, 80, 10, []vtui.TableColumn{{Title: "Command", Width: 40}})
	table.Sortable = true
	table.SetRows([]vtui.TableRow{rows[0], rows[1], rows[2]})
	table.SetSort(0, true)

	// Simulate Enter dispatching an explicit display position while the
	// table's current selection contains a different value.
	table.SelectPos = 2
	row, ok := selectedHotkeyRowAt(table, rows, 0)
	if !ok || row.Label != "Alpha action" {
		t.Fatalf("action-position row selected %q, want Alpha action", row.Label)
	}
}

func TestNativeHotkeyInventory(t *testing.T) {
	selectAction, ok := GetAction("Panel.SelectNavigation")
	if !ok {
		t.Fatal("Panel.SelectNavigation is not registered")
	}
	for _, key := range []string{"Ins", "ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight"} {
		found := false
		for _, native := range selectAction.NativeKeys {
			if native == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Panel.SelectNavigation is missing native key %q", key)
		}
	}

	focusAction, ok := GetAction("Panel.ToggleCommandLineFocus")
	if !ok {
		t.Fatal("Panel.ToggleCommandLineFocus is not registered")
	}
	if len(focusAction.NativeKeys) == 0 || focusAction.NativeKeys[0] != "VK_C0:SearchFirst" {
		t.Errorf("command-line focus native keys = %v", focusAction.NativeKeys)
	}
}

func TestActionHotkeyConfigBuildsNativeRowsAndFitsScreen(t *testing.T) {
	previous := GlobalHotkeysMgr
	GlobalHotkeysMgr = NewHotkeyManager("")
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(160, 40)
	vtui.FrameManager.Init(screen)
	actionHotkeyConfig(nil)

	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want hotkey dialog", vtui.FrameManager.GetTopFrame())
	}
	defer dlg.Close()

	var table *vtui.Table
	for _, child := range dlg.GetChildren() {
		if candidate, ok := child.(*vtui.Table); ok {
			table = candidate
			break
		}
	}
	if table == nil {
		t.Fatal("hotkey dialog has no table")
	}

	selectInsert := false
	toggleFocus := false
	for _, row := range table.Rows {
		hotkey, ok := row.(hotkeyRow)
		if !ok {
			continue
		}
		if hotkey.Action == "Panel.SelectNavigation" && hotkey.Key == "Ins" && !hotkey.Editable {
			selectInsert = true
		}
		if hotkey.Action == "Panel.ToggleCommandLineFocus" && hotkey.Key == "`" && hotkey.Condition == "SearchFirst" && !hotkey.Editable {
			toggleFocus = true
		}
	}
	if !selectInsert || !toggleFocus {
		t.Fatalf("native rows missing: select=%v toggle-focus=%v", selectInsert, toggleFocus)
	}

	var hasSave, hasCancel bool
	for _, child := range dlg.GetChildren() {
		button, ok := child.(*vtui.Button)
		if !ok {
			continue
		}
		switch getCleanText(button) {
		case "Save":
			hasSave = true
		case "Cancel":
			hasCancel = true
		}
	}
	if !hasSave || !hasCancel {
		t.Fatalf("hotkey dialog must expose transactional buttons: save=%v cancel=%v", hasSave, hasCancel)
	}

	x1, _, x2, _ := dlg.GetPosition()
	if got, want := x2-x1+1, 156; got != want {
		t.Errorf("dialog width = %d, want %d", got, want)
	}
	_, y1, _, y2 := dlg.GetPosition()
	if got, want := y2-y1+1, 38; got != want {
		t.Errorf("dialog height = %d, want %d", got, want)
	}
}

func TestActionHotkeyConfigUnbindUsesDraftUntilSave(t *testing.T) {
	previous := GlobalHotkeysMgr
	manager := NewHotkeyManager("")
	GlobalHotkeysMgr = manager
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(160, 40)
	vtui.FrameManager.Init(screen)
	actionHotkeyConfig(nil)

	openDialog := func() (*vtui.Window, *vtui.Table, *vtui.Button, *vtui.Button) {
		top, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if !ok {
			t.Fatalf("top frame = %T, want hotkey dialog", vtui.FrameManager.GetTopFrame())
		}
		var table *vtui.Table
		var unbind, save *vtui.Button
		for _, child := range top.GetChildren() {
			switch candidate := child.(type) {
			case *vtui.Table:
				table = candidate
			case *vtui.Button:
				switch getCleanText(candidate) {
				case "Unbind":
					unbind = candidate
				case "Save":
					save = candidate
				}
			}
		}
		if table == nil || unbind == nil || save == nil {
			t.Fatalf("hotkey dialog controls missing: table=%v unbind=%v save=%v", table != nil, unbind != nil, save != nil)
		}
		for i, row := range table.Rows {
			hotkey, ok := row.(hotkeyRow)
			if ok && hotkey.Action == "File.Delete" && hotkey.RawKey == "F8" {
				table.SelectPos = i
				return top, table, unbind, save
			}
		}
		t.Fatal("File.Delete/F8 row not found")
		return nil, nil, nil, nil
	}

	mainDialog, _, unbind, save := openDialog()
	unbind.OnClick()
	confirmation, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatalf("confirmation frame = %T, want container", vtui.FrameManager.GetTopFrame())
	}
	clickDialogButton(t, confirmation, "Cancel")
	if got := manager.GetAction("Shell", "F8"); got != "File.Delete" {
		t.Fatalf("cancelled confirmation changed live binding: got %q", got)
	}

	unbind.OnClick()
	confirmation, ok = vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatalf("confirmation frame = %T, want container", vtui.FrameManager.GetTopFrame())
	}
	clickDialogButton(t, confirmation, "Ok")
	if got := manager.GetAction("Shell", "F8"); got != "File.Delete" {
		t.Fatalf("accepted draft removal changed live binding before Save: got %q", got)
	}
	if mainDialog.IsDone() {
		t.Fatal("unbind confirmation closed the main dialog")
	}
	save.OnClick()
	if got := manager.GetAction("Shell", "F8"); got != "None" {
		t.Fatalf("saved removal = %q, want None", got)
	}
}

// TestNativeOnlyActionsStayAssignable guards issue #72: a user whose host
// swallows a framework-owned chord must still be able to put the action on a
// key of their own from the hotkey settings dialog.
func TestNativeOnlyActionsStayAssignable(t *testing.T) {
	previous := GlobalHotkeysMgr
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	hm := NewHotkeyManager("")
	GlobalHotkeysMgr = hm
	rows := buildHotkeyRows(hm)

	for _, tc := range []struct {
		action    string
		nativeKey string
	}{
		{"Workspace.Next", "Ctrl+Tab"},
		{"Workspace.Previous", "Ctrl+Shift+Tab"},
		{"Workspace.List", "F12"},
	} {
		var native, assignable int
		for _, row := range rows {
			if row.Action != tc.action {
				continue
			}
			switch {
			case row.Key == tc.nativeKey && !row.Editable:
				native++
			case row.Key == "" && row.Editable:
				assignable++
			default:
				t.Errorf("%s: unexpected row %+v", tc.action, row)
			}
		}
		if native != 1 {
			t.Errorf("%s: read-only %s rows = %d, want 1", tc.action, tc.nativeKey, native)
		}
		if assignable != 1 {
			t.Errorf("%s: assignable rows = %d, want 1", tc.action, assignable)
		}
	}
}

// TestConfiguredBindingReplacesTheAssignableRow keeps the ordinary case
// unchanged: once an action carries a configurable binding it no longer needs
// the empty row.
func TestConfiguredBindingReplacesTheAssignableRow(t *testing.T) {
	previous := GlobalHotkeysMgr
	t.Cleanup(func() { GlobalHotkeysMgr = previous })

	hm := NewHotkeyManager("")
	GlobalHotkeysMgr = hm
	hm.Bind("Common", "CtrlShiftT", "Workspace.Next")

	var empty, bound int
	for _, row := range buildHotkeyRows(hm) {
		if row.Action != "Workspace.Next" {
			continue
		}
		if row.Key == "" {
			empty++
		}
		if row.Key == "Ctrl+Shift+T" && row.Editable && row.Area == "Common" {
			bound++
		}
	}
	if bound != 1 {
		t.Errorf("configured Ctrl+Shift+T rows = %d, want 1", bound)
	}
	if empty != 0 {
		t.Errorf("empty rows = %d, want 0 once the action is bound", empty)
	}
}
