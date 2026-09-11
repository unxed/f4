package app

import (
	"github.com/unxed/f4/internal/settings"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/keymap"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestDialogTableUsesThemePalette(t *testing.T) {
	table := vtui.NewTable(0, 0, 20, 5, []vtui.TableColumn{{Title: "Value", Width: 20}})
	theme.UseTableColors(table)

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
	previous := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

	hm := keymap.NewHotkeyManager("")
	f := dialog.NewHotkeyAssignFrame(hm, "File.Attributes", "Shell", nil)
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

	left := dialog.NewHotkeyAssignFrame(hm, "File.Attributes", "Shell", nil)
	leftCtrlA := *rightCtrlA
	leftCtrlA.ControlKeyState = vtinput.LeftCtrlPressed
	if !left.ProcessKey(&leftCtrlA) {
		t.Fatal("Left Ctrl+A was not consumed by the assignment dialog")
	}
	if got := hm.Bindings["Shell"]["CtrlA"]; got != "File.Attributes" {
		t.Fatalf("captured Left Ctrl+A = %q, want File.Attributes under CtrlA", got)
	}
}

func TestHotkeyAssignFramePreservesShiftFromModifierEvent(t *testing.T) {
	hm := keymap.NewHotkeyManager("")
	f := dialog.NewHotkeyAssignFrame(hm, "File.Attributes", "Shell", nil)

	if !f.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_LSHIFT,
	}) {
		t.Fatal("Shift keydown was not consumed by the assignment dialog")
	}
	if !f.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_K,
	}) {
		t.Fatal("Shift+K was not consumed by the assignment dialog")
	}

	if got := hm.Bindings["Shell"]["ShiftK"]; got != "File.Attributes" {
		t.Fatalf("captured Shift+K = %q, want File.Attributes under ShiftK", got)
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
	previous := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

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
		switch testutil.GetCleanText(button) {
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
	previous := keymap.GlobalHotkeysMgr
	manager := keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr = manager
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

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
				switch testutil.GetCleanText(candidate) {
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
	testutil.ClickDialogButton(t, confirmation, "Cancel")
	if got := manager.GetAction("Shell", "F8"); got != "File.Delete" {
		t.Fatalf("cancelled confirmation changed live binding: got %q", got)
	}

	unbind.OnClick()
	confirmation, ok = vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatalf("confirmation frame = %T, want container", vtui.FrameManager.GetTopFrame())
	}
	testutil.ClickDialogButton(t, confirmation, "Ok")
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
	previous := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

	hm := keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr = hm
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
	previous := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

	hm := keymap.NewHotkeyManager("")
	keymap.GlobalHotkeysMgr = hm
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

func findEmbeddedHotkeys(t *testing.T, root vtui.Container) *hotkeyPage {
	t.Helper()
	for _, child := range root.GetChildren() {
		if page, ok := child.(*hotkeyPage); ok {
			return page
		}
		if group, ok := child.(vtui.Container); ok {
			if page := findEmbeddedHotkeys(t, group); page != nil {
				return page
			}
		}
	}
	return nil
}

func TestSettingsEmbeddedHotkeyConfiguratorDraft(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	manager := keymap.NewHotkeyManager(filepath.Join(t.TempDir(), "hotkeys.ini"))
	keymap.GlobalHotkeysMgr = manager
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(160, 40)
	vtui.FrameManager.Init(screen)
	settings.OpenAt("hotkeys", "", "", false)
	center, ok := vtui.FrameManager.GetTopFrame().(*settings.Center)
	if !ok {
		t.Fatalf("top frame = %T", vtui.FrameManager.GetTopFrame())
	}
	defer center.Close()
	page := findEmbeddedHotkeys(t, center)
	if page == nil {
		t.Fatal("missing embedded hotkey configurator")
	}
	if !page.table.Sortable || !page.table.QuickSearch || len(page.table.Columns) != 5 {
		t.Fatal("legacy table features missing")
	}
	for i, row := range page.table.Rows {
		r := row.(hotkeyRow)
		if r.Action == "File.Delete" && r.RawKey == "F8" {
			page.table.SetSelectPos(i)
			break
		}
	}
	page.unbind.OnClick()
	confirmation, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatal("no unbind confirmation")
	}
	testutil.ClickDialogButton(t, confirmation, "Ok")
	if manager.GetAction("Shell", "F8") != "File.Delete" {
		t.Fatal("draft changed live binding")
	}
	settings.OpenAt("appearance", "", "", false)
	settings.OpenAt("hotkeys", "", "", false)
	if findEmbeddedHotkeys(t, center) != page {
		t.Fatal("tab switch replaced the editing session")
	}
	testutil.ClickDialogButton(t, center, "Apply")
	if manager.GetAction("Shell", "F8") != "None" {
		t.Fatal("Apply did not persist removal of default binding")
	}

	// Edits after Apply must remain cancellable without reverting the applied removal.
	originalView := manager.GetAction("Shell", "F3")
	foundView := false
	for i, row := range page.table.Rows {
		r := row.(hotkeyRow)
		if r.Area == "Shell" && r.RawKey == "F3" && r.Editable {
			page.table.SetSelectPos(i)
			foundView = true
			break
		}
	}
	if !foundView {
		t.Fatal("F3 row missing")
	}
	page.unbind.OnClick()
	testutil.ClickDialogButton(t, vtui.FrameManager.GetTopFrame().(vtui.Container), "Ok")
	center.Close()
	reloaded := keymap.NewHotkeyManager(manager.IniPath)
	if manager.GetAction("Shell", "F3") != originalView || reloaded.GetAction("Shell", "F3") != originalView {
		t.Fatal("Cancel saved the post-Apply edit")
	}

	if reloaded.GetAction("Shell", "F8") != "None" {
		t.Fatal("removed binding returned after reload")
	}
}

func TestEmbeddedHotkeyPageUsesLiveDialogPalette(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	saved := append([]uint64(nil), vtui.Palette...)
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous; vtui.Palette = saved })
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	owner := vtui.NewCenteredDialog(80, 25, "Settings")
	page := (settingsHost{}).HotkeyPage(owner, nil).(*hotkeyPage)
	var rows []vtui.TableRow
	for i := 0; i < 40; i++ {
		rows = append(rows, hotkeyRow{Label: "Command", Key: "F8", Editable: true})
	}
	page.table.SetRows(rows)
	slots := []int{vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton, vtui.ColDialogBox}
	for _, width := range []int{40, 100} {
		page.SetPosition(1, 1, width, 18)
		for pass := 0; pass < 2; pass++ {
			for i, slot := range slots {
				vtui.Palette[slot] = vtui.SetRGBBoth(0, testutil.Uint32(0x101010+i*0x101+pass*0x100000), 0x020202)
			}
			for _, focus := range []bool{false, true} {
				page.SetFocus(focus)
				screen := vtui.NewSilentScreenBuf()
				screen.AllocBuf(120, 25)
				page.Show(screen)
				foundBox, foundText, foundSelection := false, false, false
				for y := 1; y <= 18; y++ {
					for x := 1; x <= width; x++ {
						attr := screen.GetCell(x, y).Attributes
						if attr == vtui.Palette[vtui.ColDialogText] {
							foundText = true
						}
						if attr == vtui.Palette[vtui.ColDialogSelectedButton] {
							foundSelection = true
						}
						if attr == vtui.Palette[vtui.ColDialogBox] {
							foundBox = true
						}
					}
				}
				if !foundBox || !foundText || (focus && !foundSelection) {
					t.Fatalf("live dialog palette: box=%v text=%v selected=%v focus=%v", foundBox, foundText, foundSelection, focus)
				}
			}
		}
	}
}

func TestSettingsHotkeyFirstRowUp(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager(filepath.Join(t.TempDir(), "hotkeys.ini"))
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(140, 35)
	vtui.FrameManager.Init(scr)
	settings.OpenAt("hotkeys", "", "", false)
	center := vtui.FrameManager.GetTopFrame().(*settings.Center)
	defer center.Close()
	page := findEmbeddedHotkeys(t, center)
	for _, child := range center.GetChildren() {
		if container, ok := child.(vtui.Container); ok {
			for _, nested := range container.GetChildren() {
				if nested == page {
					center.SetFocusedItem(child)
				}
			}
		}
	}
	page.SetFocusedItem(page.table)
	page.table.SetSelectPos(0)
	center.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_UP})
	center.Show(scr)
	if page.GetFocusedItem() != page.unbind {
		t.Fatal("Up at first row did not wrap inside the configurator")
	}
	center.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if page.GetFocusedItem() != page.table || page.table.SelectPos != 0 {
		t.Fatal("Down did not return to the first row")
	}
	page.table.SetSelectPos(page.table.ItemCount - 1)
	center.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if page.GetFocusedItem() != page.assign {
		t.Fatal("Down at last row did not reach Assign")
	}
	center.Show(scr)
}
