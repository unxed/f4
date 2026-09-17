package visren

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestVisRenDialogSmallRenderingHelpers(t *testing.T) {
	if got := centeredRule("title", 0); got != "" {
		t.Fatalf("centeredRule zero width = %q", got)
	}
	if got := centeredRule("long title", 5); got != "long " {
		t.Fatalf("centeredRule truncation = %q", got)
	}
	if got := centeredRule("x", 7); got != "── x ──" {
		t.Fatalf("centeredRule centered = %q", got)
	}

	if got := fixedLines("abcdef", 2, 4); !reflect.DeepEqual(got, []string{"ab", "cd", "ef", ""}) {
		t.Fatalf("fixedLines = %#v", got)
	}
	if got := fixedLines("abcdef", 2, 2); !reflect.DeepEqual(got, []string{"ab", "cd"}) {
		t.Fatalf("fixedLines truncation = %#v", got)
	}

	base := uint64(0)
	indexed := vtui.SetIndexBack(base, 3)
	if got := copyBackground(base, indexed); vtui.GetIndexBack(got) != 3 {
		t.Fatalf("copyBackground indexed background = %#x", got)
	}
	rgb := vtui.SetRGBBack(vtui.IsBgRGB, 0x112233)
	if got := copyBackground(0, rgb); got&vtui.IsBgRGB == 0 || vtui.GetRGBBack(got) != 0x112233 {
		t.Fatalf("copyBackground RGB background = %#x", got)
	}

	baseAttr, matchAttr := uint64(11), uint64(22)
	got := cropPadHighlighted("界ab", -2, 3, baseAttr, matchAttr, []TextRange{{1, 3}})
	if len(got) != 3 || got[0].Char != '界' || got[1].Char != vtui.WideCharFiller || got[2].Char != 'a' || got[2].Attributes != matchAttr {
		t.Fatalf("cropPadHighlighted = %#v", got)
	}
	if got := cropPadHighlighted("abc", 20, 4, baseAttr, matchAttr, nil); len(got) != 4 || got[0].Char != ' ' {
		t.Fatalf("cropPadHighlighted past end = %#v", got)
	}
}

func TestVisRenPreviewListNavigationAndBounds(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(30, 12)
	p := newPreviewList(1, 1, 12, 4)
	if p.visibleHeight() != 4 || p.scrollbarNeeded() {
		t.Fatalf("initial preview geometry height=%d scrollbar=%v", p.visibleHeight(), p.scrollbarNeeded())
	}
	if p.contentWidth() != 12 {
		t.Fatalf("initial content width=%d", p.contentWidth())
	}

	rows := make([]Preview, 7)
	for i := range rows {
		rows[i] = Preview{Item: &Item{Source: "source"}, Destination: "destination"}
	}
	p.setRows(rows)
	if !p.scrollbarNeeded() || p.contentWidth() != 11 {
		t.Fatalf("overflow geometry scrollbar=%v width=%d", p.scrollbarNeeded(), p.contentWidth())
	}
	p.cursor = len(rows) - 1
	p.ensureVisible()
	if p.top != len(rows)-p.visibleHeight() {
		t.Fatalf("ensureVisible top=%d", p.top)
	}
	p.setTop(100)
	if p.top != len(rows)-p.visibleHeight() {
		t.Fatalf("setTop upper clamp=%d", p.top)
	}
	p.setTop(-100)
	if p.top != 0 {
		t.Fatalf("setTop lower clamp=%d", p.top)
	}

	for _, event := range []*vtinput.InputEvent{
		{KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_HOME},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_END},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT},
	} {
		event.Type = vtinput.KeyEventType
		if !p.ProcessKey(event) {
			t.Fatalf("preview key %#v was not handled", event)
		}
	}
	if p.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_UP}) {
		t.Fatal("preview key-up was handled")
	}
	if p.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F1}) {
		t.Fatal("unrelated preview key was handled")
	}

	if !p.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: 2, MouseY: 2, WheelDirection: 1}) {
		t.Fatal("preview wheel was not handled")
	}
	if !p.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: 2, MouseY: 2, ButtonState: vtinput.FromLeft1stButtonPressed}) {
		t.Fatal("preview click was not handled")
	}
	if p.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: 29, MouseY: 11}) {
		t.Fatal("outside preview mouse event was handled")
	}
	p.Show(scr)

	p.setRows(nil)
	if p.cursor != 0 || p.top != 0 || p.scrollbarNeeded() {
		t.Fatalf("empty rows state cursor=%d top=%d scrollbar=%v", p.cursor, p.top, p.scrollbarNeeded())
	}
	if p.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}) {
		t.Fatal("empty preview handled a key")
	}
}

func TestVisRenDialogUndoAndKeyboardContracts(t *testing.T) {
	d := setupDialogTest(t)
	d.host = &inputBoxHostStub{}
	d.plugin.setUndo(d.dir, []RenamePair{{Old: "old.txt", New: "new.txt"}})
	d.updateLogLine()
	if !strings.Contains(d.logLine.GetText(), "*") {
		t.Fatalf("log marker does not show undo state: %q", d.logLine.GetText())
	}
	d.logging = false
	d.updateLogLine()
	if !strings.Contains(d.logLine.GetText(), "Log") {
		t.Fatalf("disabled log line = %q", d.logLine.GetText())
	}

	d.loadUndoView()
	if !d.undoMode || d.GetFocusedItem() != d.preview || len(d.rows) != 1 || d.rows[0].Destination != "old.txt" {
		t.Fatalf("undo view mode=%v focus=%T rows=%#v", d.undoMode, d.GetFocusedItem(), d.rows)
	}
	d.restoreNormalView()
	if d.undoMode || d.GetFocusedItem() != d.nameEdit {
		t.Fatalf("normal view mode=%v focus=%T", d.undoMode, d.GetFocusedItem())
	}

	d.SetFocusedItem(d.nameCombo)
	d.nameCombo.Menu.SetSelectPos(7)
	if !d.selectedTemplateIsTitle() {
		t.Fatal("name title template was not recognized")
	}
	d.nameCombo.Menu.SetSelectPos(0)
	d.nameCombo.Edit.SetText("[T]")
	if !d.selectedTemplateIsTitle() {
		t.Fatal("name title text was not recognized")
	}
	d.SetFocusedItem(d.extCombo)
	d.extCombo.Menu.SetSelectPos(7)
	if !d.selectedTemplateIsTitle() {
		t.Fatal("extension title template was not recognized")
	}
	d.SetFocusedItem(d.searchEdit)
	if d.selectedTemplateIsTitle() {
		t.Fatal("unrelated focused item was treated as title template")
	}

	d.SetFocusedItem(d.searchEdit)
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F2})
	if !d.logging {
		t.Fatal("F2 did not toggle logging")
	}
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F12})
	if d.GetFocusedItem() != d.preview {
		t.Fatal("F12 did not focus preview")
	}
	d.preview.cursor = 0
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.LeftCtrlPressed})
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NUMPAD5, ControlKeyState: vtinput.LeftCtrlPressed})
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR, ControlKeyState: vtinput.LeftCtrlPressed})

	// Reinitialize the frame manager after the navigation key closes the dialog.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	d = setupDialogTest(t)
	d.engine.Items = []*Item{testItem("one.txt"), testItem("two.txt")}
	d.refreshPreview()
	d.SetFocusedItem(d.preview)
	d.preview.cursor = 0
	if !d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE}) {
		t.Fatal("Delete did not remove preview row")
	}
	if len(d.engine.Items) != 1 {
		t.Fatalf("rows after Delete=%d", len(d.engine.Items))
	}
	d.engine.Items = []*Item{testItem("one.txt"), testItem("two.txt")}
	d.refreshPreview()
	d.SetFocusedItem(d.preview)
	d.preview.cursor = 0
	d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN, ControlKeyState: vtinput.LeftCtrlPressed})
	if d.preview.cursor != 1 || d.engine.Items[1].Source != "one.txt" {
		t.Fatalf("Ctrl+Down did not reorder preview: cursor=%d items=%q,%q", d.preview.cursor, d.engine.Items[0].Source, d.engine.Items[1].Source)
	}

	if d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: false, VirtualKeyCode: vtinput.VK_F2}) {
		t.Fatal("dialog key-up unexpectedly handled")
	}
	d.ResizeConsole(0, 0)
}

func TestVisRenDialogUndoDetailsAndMouseContracts(t *testing.T) {
	d := setupDialogTest(t)
	d.plugin.setUndo(d.dir, []RenamePair{{Old: "old.txt", New: "new.txt"}})
	d.refreshPreview()
	d.preview.cursor = 0
	d.showDetails()
	if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); !ok {
		t.Fatalf("details frame = %T", vtui.FrameManager.GetTopFrame())
	}
	vtui.FrameManager.Pop()

	d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: int16(d.X2 - 3), MouseY: int16(d.Y1), ButtonState: vtinput.FromLeft1stButtonPressed})           // #nosec G115 -- test dialog coordinates fit int16.
	d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: int16(d.logMarkX), MouseY: int16(d.logLine.Y1), ButtonState: vtinput.FromLeft1stButtonPressed}) // #nosec G115 -- test dialog coordinates fit int16.
	d.preview.cursor = 0
	d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: visrenMouseCoordinate(d.preview.X1), MouseY: visrenMouseCoordinate(d.preview.Y1), ButtonState: vtinput.FromLeft1stButtonPressed})
	d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true, MouseX: visrenMouseCoordinate(d.preview.X1 + 1), MouseY: visrenMouseCoordinate(d.preview.Y1), ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved})
	d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: visrenMouseCoordinate(d.preview.X1 + 1), MouseY: visrenMouseCoordinate(d.preview.Y1), ButtonState: 0})
	if d.dragRow != -1 {
		t.Fatalf("drag row after release=%d", d.dragRow)
	}
	if d.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: 100, MouseY: 100}) {
		t.Fatal("outside dialog mouse event was handled")
	}

	// Exercise the empty undo branch as well; it should show an informational
	// dialog and leave the normal editor state untouched.
	d.plugin.setUndo("", nil)
	d.loadUndoView()
	if d.undoMode {
		t.Fatal("empty undo log entered undo mode")
	}
}
