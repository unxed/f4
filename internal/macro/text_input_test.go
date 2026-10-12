package macro

import (
	"encoding/base64"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestConfirmedTextMacroAssignmentFields(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	frame := NewMacroAssignFrame(&MacroManager{})
	event := vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, TextInput: true,
		Char: '@', VirtualKeyCode: vtinput.VK_Q, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed}
	original := event
	if !frame.ProcessKey(&event) || frame.Key() != "" || frame.GetFocusedItem() != frame.keyField || frame.IsDone() {
		t.Fatal("committed text captured a key or left the capture field")
	}
	frame.SetFocusedItem(frame.desc)
	if !frame.ProcessKey(&event) || frame.desc.GetText() != "@" || frame.IsDone() {
		t.Fatalf("description rejected committed text: %q", frame.desc.GetText())
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("assignment changed source event")
	}
	frame.SetFocusedItem(frame.keyField)
	event.VirtualKeyCode = vtinput.VK_ESCAPE
	if !frame.ProcessKey(&event) || frame.IsDone() || frame.Key() != "" {
		t.Fatal("physical Escape metadata cancelled a text commit")
	}
	event.TextInput = false
	if !frame.ProcessKey(&event) || !frame.IsDone() {
		t.Fatal("ordinary Escape no longer cancels assignment")
	}
}

func recordedTextEvents() []*vtinput.InputEvent {
	var events []*vtinput.InputEvent
	for _, ch := range "@€Ą~😀 " {
		events = append(events, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true,
			TextInput: true, Char: ch, VirtualKeyCode: vtinput.VK_Q, VirtualScanCode: 16,
			UnshiftedChar: 'q', ControlKeyState: vtinput.LeftCtrlPressed | vtinput.RightAltPressed,
			RepeatCount: 2, InputSource: "win32"})
	}
	return events
}

func TestConfirmedTextMacroRejectsInvalidTokens(t *testing.T) {
	for _, payload := range []string{`{"char":0}`, `{"char":55296}`, `{"char":1114112}`, `{"char":64,"unshifted":-1}`, `{`} {
		token := textEventPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload))
		if decodeMacroEvent(token) != nil {
			t.Errorf("accepted %s", payload)
		}
	}
	if decodeMacroEvent("f4-text-v1:!") != nil {
		t.Fatal("accepted invalid base64")
	}
	if decodeMacroEvent("f4-text-v2:abc") != nil {
		t.Fatal("accepted unknown text codec version")
	}
}

func TestConfirmedTextCannotTriggerLuaMacro(t *testing.T) {
	host := newFakeMacroHost()
	engine := newTestMacroEngine(t, host, `Macro { area = "Shell"; key = "CtrlAltQ"; action = function() Keys("F10") end; }`)
	event := recordedTextEvents()[0]
	if engine.Trigger("Shell", event) {
		t.Fatal("committed text triggered the physical-key macro")
	}
	if !fireMacro(t, engine, "CtrlAltQ") {
		t.Fatal("ordinary shortcut stopped triggering the macro")
	}
}

func TestConfirmedTextMacroDiskRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "macros.ini")
	m := NewMacroManager(path)
	events := recordedTextEvents()
	m.Macros["Shell"] = map[string][]*vtinput.InputEvent{"F6": events}
	m.Save()
	got := NewMacroManager(path).Macros["Shell"]["F6"]
	if !reflect.DeepEqual(got, events) {
		t.Fatalf("recorded text changed after save/load: got %+v, want %+v", got, events)
	}
}

func TestConfirmedTextMacroLuaRoundTrip(t *testing.T) {
	events := recordedTextEvents()
	dir := t.TempDir()
	m := NewMacroManager("")
	if err := m.SaveRecordedMacro(dir, "Shell", "F6", "text", events); err != nil {
		t.Fatal(err)
	}
	host := newFakeMacroHost()
	engine := newTestMacroEngine(t, host, "")
	if err := engine.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if !fireMacro(t, engine, "F6") {
		t.Fatal("recorded macro did not trigger")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if !reflect.DeepEqual(host.injected, events) {
		t.Fatalf("exported text changed on replay: got %+v, want %+v", host.injected, events)
	}
}
