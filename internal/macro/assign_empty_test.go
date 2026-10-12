package macro

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// f4#1870 (DkmS1953): with nothing recorded, the assign dialog says before a
// key is pressed that the key's macro will be removed.
func TestAssignDialogWarnsAboutAnEmptyRecording(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	text := func(f *MacroAssignFrame) string {
		var b strings.Builder
		for _, child := range f.GetChildren() {
			if tx, ok := child.(*vtui.Text); ok {
				b.WriteString(tx.GetText())
				b.WriteString(" ")
			}
		}
		return b.String()
	}
	firstWord := strings.Fields(i18n.Msg("Macro.AssignEmpty"))[0]

	empty := NewMacroAssignFrame(&MacroManager{})
	if !strings.Contains(text(empty), firstWord) {
		t.Errorf("no warning in the dialog for an empty recording: %q", text(empty))
	}
	for _, e := range vtui.ValidateLayout(empty.Window) {
		t.Errorf("layout: %v", e)
	}

	recorded := NewMacroAssignFrame(&MacroManager{Buffer: []*vtinput.InputEvent{{Type: vtinput.KeyEventType, KeyDown: true, Char: 'a'}}})
	if strings.Contains(text(recorded), firstWord) {
		t.Errorf("the warning shows although keys were recorded: %q", text(recorded))
	}
}

// f4#1870 (DkmS1953): the assign dialog is far2's -- a description, a field
// that takes the pressed key, OK and Cancel; the description is kept with the
// macro.
func TestAssignDialogKeyFieldAndDescription(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	path := t.TempDir() + "/macros.ini"
	mgr := NewMacroManager(path)
	mgr.StartArea = "Shell"
	mgr.Buffer = []*vtinput.InputEvent{{Type: vtinput.KeyEventType, KeyDown: true, Char: 'a'}}
	f := NewMacroAssignFrame(mgr)
	for _, e := range vtui.ValidateLayout(f.Window) {
		t.Errorf("layout: %v", e)
	}
	enter := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}

	// The key field has the focus: Enter is the key, not OK.
	f.SetDescriptionText("про тесто")
	f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F5, ControlKeyState: vtinput.LeftCtrlPressed})
	if f.Key() != "CtrlF5" || f.IsDone() {
		t.Fatalf("key field holds %q, done %v; want CtrlF5 and the dialog open", f.Key(), f.IsDone())
	}
	// Now OK has the focus.
	f.ProcessKey(enter)
	if !f.IsDone() {
		t.Fatal("Enter after the key did not confirm")
	}
	if _, ok := mgr.Macros["Shell"]["CtrlF5"]; !ok {
		t.Fatal("the macro was not assigned to CtrlF5")
	}

	loaded := NewMacroManager(path)
	loaded.Load()
	if got := loaded.Description("Shell", "CtrlF5"); got != "про тесто" {
		t.Errorf("description after reload = %q", got)
	}
}
