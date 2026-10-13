package cmdline

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
)

func TestConfirmedTextLeavesHistory(t *testing.T) {
	old := config.App.CommandLineAutoComplete
	config.App.CommandLineAutoComplete = false
	t.Cleanup(func() { config.App.CommandLineAutoComplete = old })
	cl := NewCommandLine("")
	cl.Edit.HistoryPos = 0
	cl.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '€', VirtualKeyCode: vtinput.VK_E,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed})
	if cl.Edit.GetText() != "€" || cl.Edit.HistoryPos != -1 {
		t.Fatalf("text=%q history=%d", cl.Edit.GetText(), cl.Edit.HistoryPos)
	}
}
