package panel

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func newDialogTestManager(t *testing.T) *keymap.HotkeyManager {
	t.Helper()
	return &keymap.HotkeyManager{
		Bindings: map[string]map[string]string{"Shell": {"Del": "DeleteFiles"}},
		Defaults: map[string]map[string]string{"Shell": {"Del": "DeleteFiles"}},
		IniPath:  filepath.Join(t.TempDir(), "hotkeys.ini"),
	}
}

func TestPluginHotkeyDialogChoice(t *testing.T) {
	old := config.App.PluginDefaultHotkeysOff
	t.Cleanup(func() { config.App.PluginDefaultHotkeysOff = old })
	config.App.PluginDefaultHotkeysOff = ""
	hm := newDialogTestManager(t)
	const action = "Plugin.Legacy.0"

	// A letter is assigned; the same letter again is no change.
	if changed, ok := applyPluginHotkeyChoice(hm, action, "", "", "q"); !ok || !changed || hm.Bindings["Shell"]["Q"] != action {
		t.Fatalf("assign q: changed=%v ok=%v bindings=%v", changed, ok, hm.Bindings["Shell"])
	}
	if changed, ok := applyPluginHotkeyChoice(hm, action, "", "", "Q"); !ok || changed {
		t.Fatalf("the same letter: changed=%v ok=%v, want no change", changed, ok)
	}
	if got := currentPluginHotkeyText(hm, action, "", ""); got != "Q" {
		t.Fatalf("field starts with %q, want Q", got)
	}

	// Anything that is not one letter or digit is refused and changes nothing.
	for _, bad := range []string{"F4", "ab", "-", "Del"} {
		if changed, ok := applyPluginHotkeyChoice(hm, action, "", "", bad); ok || changed {
			t.Errorf("%q: changed=%v ok=%v, want it refused", bad, changed, ok)
		}
	}
	if hm.Bindings["Shell"]["Q"] != action || hm.Bindings["Shell"]["Del"] != "DeleteFiles" {
		t.Fatalf("a refused choice moved the bindings: %v", hm.Bindings["Shell"])
	}

	// An empty field takes the assigned key back.
	if changed, ok := applyPluginHotkeyChoice(hm, action, "", "", ""); !ok || !changed {
		t.Fatalf("empty field: changed=%v ok=%v", changed, ok)
	}
	if _, still := hm.Bindings["Shell"]["Q"]; still {
		t.Fatal("the assigned hotkey survived an empty field")
	}
}

func TestPluginHotkeyDialogEmptyFieldSwitchesOffThePluginsDefault(t *testing.T) {
	old := config.App.PluginDefaultHotkeysOff
	t.Cleanup(func() { config.App.PluginDefaultHotkeysOff = old })
	config.App.PluginDefaultHotkeysOff = ""
	hm := newDialogTestManager(t)

	if got := currentPluginHotkeyText(hm, "Plugin.Legacy.0", "", "ShiftF1"); got != "" {
		t.Fatalf("a chord default does not fit a one-character field, field = %q", got)
	}
	if changed, ok := applyPluginHotkeyChoice(hm, "Plugin.Legacy.0", "", "ShiftF1", ""); !ok || !changed {
		t.Fatalf("empty field over a default: changed=%v ok=%v", changed, ok)
	}
	if !PluginDefaultKeyOff("ShiftF1") {
		t.Fatal("the default was not switched off")
	}
	if changed, _ := applyPluginHotkeyChoice(hm, "Plugin.Legacy.0", "", "ShiftF1", ""); changed {
		t.Fatal("removing an already removed default must not report a change")
	}
}

// The row "V Visual File Renamer" of the ticket has its letter from an
// ampersand in the label: the field must show it, and an empty field must
// switch it off like any other default (f4#918).
func TestPluginHotkeyDialogShowsAndRemovesTheLabelLetter(t *testing.T) {
	old := config.App.PluginDefaultHotkeysOff
	t.Cleanup(func() { config.App.PluginDefaultHotkeysOff = old })
	config.App.PluginDefaultHotkeysOff = ""
	hm := newDialogTestManager(t)
	const action = "Plugin.Legacy.0"
	const label = "&Visual File Renamer"

	if got := currentPluginHotkeyText(hm, action, label, ""); got != "V" {
		t.Fatalf("the field shows %q, want the label's V", got)
	}
	if changed, ok := applyPluginHotkeyChoice(hm, action, label, "", ""); !ok || !changed {
		t.Fatalf("empty field over a label letter: changed=%v ok=%v", changed, ok)
	}
	if got := currentPluginHotkeyText(hm, action, label, ""); got != "" {
		t.Fatalf("after clearing the field shows %q", got)
	}
	entries := []PluginMenuEntry{{Label: label, ActionName: action}}
	RefreshPluginMenuEntries(entries)
	if entries[0].Hotkey != "" {
		t.Fatalf("the menu still gives the row the letter %q", entries[0].Hotkey)
	}
	if changed, _ := applyPluginHotkeyChoice(hm, action, label, "", ""); changed {
		t.Fatal("clearing an already cleared field must not report a change")
	}
}

func TestPluginHotkeyEditHoldsOneCharacter(t *testing.T) {
	edit := &pluginHotkeyEdit{vtui.NewEdit(0, 0, 1, "")}
	type_ := func(c rune) {
		edit.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: c})
	}
	type_('a')
	type_('b')
	if got := edit.GetText(); got != "B" {
		t.Fatalf("after a and b the field holds %q, want B", got)
	}
	type_('-')
	if got := edit.GetText(); got != "B" {
		t.Fatalf("a non letter changed the field to %q", got)
	}
	edit.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})
	if got := edit.GetText(); got != "" {
		t.Fatalf("Delete left %q", got)
	}
	type_('ф')
	if got := edit.GetText(); got != "Ф" {
		t.Fatalf("a Cyrillic letter gave %q", got)
	}
}

func TestMenuHeightLimit(t *testing.T) {
	if pluginMenuBottomHint != " Del F4 F9 " {
		t.Fatalf("plugin menu hint = %q, want Del, F4 and F9", pluginMenuBottomHint)
	}
	if got := menuHeightLimit("", 50); got != 15 {
		t.Errorf("a generic menu on 50 rows: %d, want 15", got)
	}
	if got := menuHeightLimit(pluginMenuBottomHint, 50); got != 50 {
		t.Errorf("the plugin menu on 50 rows: %d, want the whole window", got)
	}
	if got := menuHeightLimit(pluginMenuBottomHint, 10); got != 10 {
		t.Errorf("the plugin menu on 10 rows: %d", got)
	}
	if got := menuHeightLimit("", 0); got != 15 {
		t.Errorf("unknown screen: %d", got)
	}
}

// The hot key window follows the example in the ticket (f4#918): the prompt,
// the one-cell field with the name of the entry beside it, the hint, a rule
// and the buttons as the last row inside the frame, with no blank row.
//
// Screen Dump (80x25, the whole window):
//
//	╔══════════ Assign plugin hot key ═══════════[×]╗
//	║ Enter hot key (letter or digit)               ║
//	║ V  Visual File Renamer                        ║
//	║ Type a character, or Del to clear             ║
//	║───────────────────────────────────────────────║
//	║             [ Ok ]  [ Cancel ]                ║
//	╚═══════════════════════════════════════════════╝
func TestPluginHotkeyDialogLayout(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	theme.SetDefaultF4Palette()
	old := config.App.PluginDefaultHotkeysOff
	t.Cleanup(func() { config.App.PluginDefaultHotkeysOff = old })
	config.App.PluginDefaultHotkeysOff = ""

	showPluginHotkeyDialog(newDialogTestManager(t), "Plugin.Legacy.0", "&Visual File Renamer", nil)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatal("the hot key window is not on top")
	}
	if dlg.ShowClose {
		t.Fatal("the hot key dialog still shows a close button")
	}
	if got, want := dlg.X2-dlg.X1+1, pluginHotkeyDialogWidth("Visual File Renamer"); got != want {
		t.Fatalf("the hot key window width = %d, want content width %d", got, want)
	}
	t.Cleanup(func() { vtui.FrameManager.Pop() })
	rules := vtui.DefaultLayoutRules
	rules.FrameClearanceY = 0
	vtui.AssertLayoutWithRules(t, dlg, rules)

	dlg.Show(screen)
	var buf bytes.Buffer
	screen.Dump(&buf)
	lines := strings.Split(buf.String(), "\n")
	var rows []string
	for y := dlg.Y1; y <= dlg.Y2; y++ {
		cells := []rune(lines[2+y])
		rows = append(rows, string(cells[dlg.X1:dlg.X2+1]))
	}
	dump := strings.Join(rows, "\n")
	if len(rows) != 7 {
		t.Fatalf("the window has %d rows, want 7:\n%s", len(rows), dump)
	}
	for i, want := range map[int]string{1: "Enter hot key (letter or digit)", 2: "V  Visual File Renamer", 3: "Type a character, or Del to clear", 5: "Ok"} {
		if !strings.Contains(rows[i], want) {
			t.Errorf("row %d should hold %q:\n%s", i, want, dump)
		}
	}
	for _, c := range []rune(rows[4])[1 : len([]rune(rows[4]))-1] {
		if c == ' ' {
			t.Errorf("row 4 is not a rule:\n%s", dump)
			break
		}
	}
}
