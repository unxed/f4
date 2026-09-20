package app

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// screenshotHotkeyRows are the widest cells of the shortcut list from issue
// #1239: a long command name, the longest chord, the area and a long
// "when" condition.
func screenshotHotkeyRows() []hotkeyRow {
	return []hotkeyRow{
		{
			Label:     "Appearance and language settings dialog",
			Key:       "Ctrl+Alt+Shift+F12",
			Area:      "Terminal",
			Condition: "FrameworkNoTerminalCtrlNWorkspace",
			Desc:      "Open the settings on the page that holds this group of options",
		},
		{Label: "Close workspace", Key: "Ctrl+W", Area: "Common", Desc: "Close the current workspace"},
	}
}

func fixedColumnsWidth(columns []vtui.TableColumn) int {
	total := 0
	for _, c := range columns[:len(columns)-1] {
		total += c.Width
	}
	return total
}

// TestIssue1239KeyAndAreaKeepTheirWidthInAPane: when the list does not fit,
// the free-text columns (command, condition) have to give way. The key and the
// area are short, and a chord cut to "Ctrl+Alt+Sh" or an area cut to "Com" says
// nothing.
func TestIssue1239KeyAndAreaKeepTheirWidthInAPane(t *testing.T) {
	rows := screenshotHotkeyRows()
	const pane = 80
	columns := hotkeyTableColumns(rows, pane)

	for _, col := range []int{1, 2} {
		want := vtui.StringWidth(rows[0].GetCellText(col))
		if got := columns[col].Width; got < want {
			t.Errorf("column %q is %d wide, want all %d cells of its longest value", columns[col].Title, got, want)
		}
	}
}

// TestIssue1239DescriptionKeepsARoom: the description is the only column that
// is elastic, and it used to be left with just its header's width.
func TestIssue1239DescriptionKeepsARoom(t *testing.T) {
	rows := screenshotHotkeyRows()
	const pane = 80
	columns := hotkeyTableColumns(rows, pane)

	// The dialog spends 4 cells on padding and one on each column separator.
	room := pane - 4 - (len(columns) - 1) - fixedColumnsWidth(columns)
	if room < 16 {
		t.Errorf("description gets %d cells of the %d-wide pane, want at least 16", room, pane)
	}
}

// TestIssue1239NarrowPaneStillFits: a pane too narrow for everything must not
// be overflowed, and the chord must still be readable.
func TestIssue1239NarrowPaneStillFits(t *testing.T) {
	rows := screenshotHotkeyRows()
	const pane = 60
	columns := hotkeyTableColumns(rows, pane)

	room := pane - 4 - (len(columns) - 1) - fixedColumnsWidth(columns)
	if room < vtui.StringWidth(columns[len(columns)-1].Title) {
		t.Errorf("columns overflow the %d-wide pane: description has %d cells left", pane, room)
	}
	if got := columns[1].Width; got < 12 {
		t.Errorf("key column is %d wide, want at least 12", got)
	}
}

// dialogTexts collects the text of every static line in a frame.
func dialogTexts(frame vtui.Frame) string {
	var out []string
	var walk func(vtui.UIElement)
	walk = func(el vtui.UIElement) {
		if t, ok := el.(interface{ GetText() string }); ok {
			out = append(out, t.GetText())
		}
		if c, ok := el.(vtui.Container); ok {
			for _, child := range c.GetChildren() {
				walk(child)
			}
		}
	}
	if el, ok := frame.(vtui.UIElement); ok {
		walk(el)
	}
	return strings.Join(out, "\n")
}

// TestIssue1239F3ShowsTheWholeRow: a row of the hotkey list is cut to the
// width of its columns, so F3 has to show it in full, the way F3 shows a file
// in the other big dialogs.
func TestIssue1239F3ShowsTheWholeRow(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(140, 35)
	vtui.FrameManager.Init(scr)

	owner := vtui.NewCenteredDialog(100, 25, "Settings")
	vtui.FrameManager.Push(owner)
	page := (settingsHost{}).HotkeyPage(owner, nil).(*hotkeyPage)
	page.SetPosition(owner.X1+1, owner.Y1+1, owner.X1+60, owner.Y1+20)
	rows := []hotkeyRow{
		{Label: "Alpha", Key: "F8", Area: "Shell"},
		{
			Label:     "Appearance and language settings dialog",
			Key:       "Ctrl+Alt+Shift+F12",
			Area:      "Terminal",
			Condition: "FrameworkNoTerminalCtrlNWorkspace",
			Desc:      "Open the settings on the page that holds this group of options and then keep going until ENDOFDESCRIPTION",
			Editable:  true,
		},
	}
	page.table.SetRows([]vtui.TableRow{rows[0], rows[1]})
	page.table.SetSelectPos(1)
	page.SetFocusedItem(page.table)

	if !page.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3}) {
		t.Fatal("F3 in the hotkey list was not handled")
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == vtui.Frame(owner) {
		t.Fatal("F3 did not open anything over the settings dialog")
	}
	text := dialogTexts(top)
	for _, want := range []string{"Appearance and language settings dialog", "Ctrl+Alt+Shift+F12", "Terminal", "FrameworkNoTerminalCtrlNWorkspace", "ENDOFDESCRIPTION"} {
		if !strings.Contains(text, want) {
			t.Errorf("the details window lacks %q; it shows:\n%s", want, text)
		}
	}
}

// TestIssue1239ListKeysActOnTheSelectedRow: F4, Ins and Del in the hotkey
// list do what the Assign and Unbind buttons do, without leaving the list.
func TestIssue1239ListKeysActOnTheSelectedRow(t *testing.T) {
	previous := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager("")
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(140, 35)
	vtui.FrameManager.Init(scr)

	owner := vtui.NewCenteredDialog(100, 25, "Settings")
	vtui.FrameManager.Push(owner)
	page := (settingsHost{}).HotkeyPage(owner, nil).(*hotkeyPage)
	page.SetPosition(owner.X1+1, owner.Y1+1, owner.X1+60, owner.Y1+20)
	page.SetFocusedItem(page.table)

	var assigned, unbound int
	page.assign.OnClick = func() { assigned++ }
	page.unbind.OnClick = func() { unbound++ }
	press := func(vk uint16) bool {
		return page.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk})
	}

	if !press(vtinput.VK_F4) || assigned != 1 {
		t.Fatalf("F4: handled or not, Assign ran %d times, want 1", assigned)
	}
	if !press(vtinput.VK_INSERT) || assigned != 2 {
		t.Fatalf("Ins: Assign ran %d times, want 2", assigned)
	}
	if !press(vtinput.VK_DELETE) || unbound != 1 {
		t.Fatalf("Del: Unbind ran %d times, want 1", unbound)
	}

	// With a search typed, Del edits the search and leaves the binding alone.
	page.table.SetSearchText("ab")
	press(vtinput.VK_DELETE)
	if unbound != 1 {
		t.Fatalf("Del with a search typed unbound a row (%d)", unbound)
	}
}
