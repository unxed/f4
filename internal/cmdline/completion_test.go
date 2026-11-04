package cmdline

import (
	"testing"

	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type completionCommandFrame struct {
	vtui.BaseFrame
	executions int
}

func (frame *completionCommandFrame) ProcessKey(event *vtinput.InputEvent) bool {
	if event.Type == vtinput.FocusEventType {
		frame.SetFocus(event.SetFocus)
	}
	if event.Type == vtinput.KeyEventType && event.KeyDown && event.VirtualKeyCode == vtinput.VK_RETURN {
		frame.executions++
	}
	return true
}

func (frame *completionCommandFrame) Show(*vtui.ScreenBuf)                  {}
func (frame *completionCommandFrame) GetType() vtui.FrameType               { return vtui.TypeUser }
func (frame *completionCommandFrame) ProcessMouse(*vtinput.InputEvent) bool { return false }

// Ctrl+Y with the completion list open closes the list and clears the line in
// one press, as in Far (unxed/f4#2187): the list used to hand the key to
// vtui's edit control, which has no Ctrl+Y, and the line kept its text.
func TestCompletionCtrlYClosesTheListAndClearsTheLine(t *testing.T) {
	previousManager, previousPalette := vtui.FrameManager, vtui.Palette
	t.Cleanup(func() {
		vtui.FrameManager, vtui.Palette = previousManager, previousPalette
	})
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	vtui.FrameManager = vtui.NewFrameManager()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)
	frame := &completionCommandFrame{}
	vtui.FrameManager.Push(frame)
	cl := NewCommandLine(">")
	cl.SetPosition(0, 23, 79, 23)
	cl.Edit.History = []string{"echo first", "echo second"}
	cl.Edit.SetText("echo")
	cl.Edit.HistoryPos = 1
	menu := NewCompletionMenu(cl.Edit)
	if !menu.HasMatches() {
		t.Fatal("missing suggestions")
	}
	vtui.FrameManager.Push(menu)

	if !menu.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_Y,
		ControlKeyState: vtinput.LeftCtrlPressed,
	}) {
		t.Fatal("the list did not take Ctrl+Y")
	}
	for range 4 {
		vtui.FrameManager.Step(0)
	}
	if got := cl.Edit.GetText(); got != "" {
		t.Errorf("Ctrl+Y left %q in the command line", got)
	}
	if cl.Edit.HistoryPos != -1 {
		t.Errorf("Ctrl+Y left the history position at %d", cl.Edit.HistoryPos)
	}
	if !menu.IsDone() {
		t.Error("Ctrl+Y left the completion list open")
	}
	if frame.executions != 0 {
		t.Errorf("Ctrl+Y ran %d commands", frame.executions)
	}
}

func TestCompletionEnterPolicy(t *testing.T) {
	previousManager, previousPalette := vtui.FrameManager, vtui.Palette
	t.Cleanup(func() {
		vtui.FrameManager, vtui.Palette = previousManager, previousPalette
	})
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	for _, selected := range []bool{false, true} {
		for _, key := range []vtinput.InputEvent{
			{},
			{ControlKeyState: vtinput.ShiftPressed},
			{ControlKeyState: vtinput.LeftCtrlPressed},
			{ControlKeyState: vtinput.RightCtrlPressed},
			{ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed},
		} {
			modifier := key.ControlKeyState
			vtui.FrameManager = vtui.NewFrameManager()
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 25)
			vtui.FrameManager.Init(scr)
			frame := &completionCommandFrame{}
			vtui.FrameManager.Push(frame)
			cl := NewCommandLine(">")
			cl.SetPosition(0, 23, 79, 23)
			cl.Edit.History = []string{"echo first", "echo second"}
			cl.Edit.SetText("echo")
			menu := NewCompletionMenu(cl.Edit)
			if !menu.HasMatches() {
				t.Fatal("missing suggestions")
			}
			vtui.FrameManager.Push(menu)
			want := "echo first"
			if selected {
				menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
				menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
				want = "echo second"
			}
			ctrl := modifier&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
			if !selected && !ctrl {
				want = "echo"
			}
			wantExecutions := 0
			if modifier == 0 {
				wantExecutions = 1
			}
			menu.ProcessKey(&vtinput.InputEvent{
				Type: vtinput.KeyEventType, KeyDown: true,
				VirtualKeyCode: vtinput.VK_RETURN, ControlKeyState: modifier,
			})
			for range 4 {
				vtui.FrameManager.Step(0)
			}
			if got := cl.Edit.GetText(); got != want {
				t.Errorf("selected=%v modifier=%d: text=%q, want %q", selected, modifier, got, want)
			}
			if frame.executions != wantExecutions {
				t.Errorf("selected=%v modifier=%d: executed %d commands, want %d", selected, modifier, frame.executions, wantExecutions)
			}
			if !menu.IsDone() {
				t.Error("accepted suggestion did not close the menu")
			}
		}
	}
}
