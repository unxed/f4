package keymap

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ttyx"
)

func TestParseTTYXCombos(t *testing.T) {
	got, bad := parseTTYXCombos("Ctrl+Shift+Up, Alt+Shift+F3, Ctrl+Enter")
	if len(bad) != 0 {
		t.Fatalf("nothing here is wrong, but %v was rejected", bad)
	}
	want := []ttyx.Combo{
		{Keysym: 0xFF52, Mods: ttyx.ModCtrl | ttyx.ModShift},
		{Keysym: 0xFFC0, Mods: ttyx.ModAlt | ttyx.ModShift},
		{Keysym: 0xFF0D, Mods: ttyx.ModCtrl},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d combinations, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("combination %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A letter or a digit is its own keysym in the Latin block, so a binding can
// name one without the table having to list every key on the keyboard.
func TestParseTTYXCombosTakesPlainCharacters(t *testing.T) {
	got, bad := parseTTYXCombos("Ctrl+Shift+c, Alt+7")
	if len(bad) != 0 || len(got) != 2 {
		t.Fatalf("got %+v, rejected %v", got, bad)
	}
	if got[0].Keysym != 'c' || got[0].Mods != ttyx.ModCtrl|ttyx.ModShift {
		t.Errorf("Ctrl+Shift+c came out as %+v", got[0])
	}
	if got[1].Keysym != '7' || got[1].Mods != ttyx.ModAlt {
		t.Errorf("Alt+7 came out as %+v", got[1])
	}
}

// One typo must not cost the user the rest of the list.
func TestParseTTYXCombosSkipsWhatItCannotRead(t *testing.T) {
	got, bad := parseTTYXCombos("Ctrl+Shift+Up, Hyper+Nonsense, Ctrl+Enter")
	if len(got) != 2 {
		t.Errorf("the readable entries must survive: %+v", got)
	}
	if len(bad) != 1 || bad[0] != "Hyper+Nonsense" {
		t.Errorf("the unreadable entry must be reported: %v", bad)
	}
}

// A combination with no modifier is a plain key the terminal delivers
// perfectly well. Grabbing it would take it from the desktop for nothing.
func TestParseTTYXCombosRefusesBareKeys(t *testing.T) {
	got, bad := parseTTYXCombos("F5, Up, a")
	if len(got) != 0 {
		t.Errorf("bare keys must not be grabbed: %+v", got)
	}
	if len(bad) != 3 {
		t.Errorf("and they must be reported: %v", bad)
	}
}

func TestParseTTYXCombosIgnoresBlanks(t *testing.T) {
	got, bad := parseTTYXCombos("  ,, Ctrl+Enter ,  ")
	if len(got) != 1 || len(bad) != 0 {
		t.Errorf("got %+v, rejected %v", got, bad)
	}
}

// The default list has to be readable by the parser that will read it, and
// every entry in it has to be a real combination.
func TestDefaultTTYXKeyListParses(t *testing.T) {
	got, bad := parseTTYXCombos(config.DefaultTTYXKeyList)
	if len(bad) != 0 {
		t.Errorf("the built-in list must parse cleanly: %v", bad)
	}
	if len(got) == 0 {
		t.Error("the built-in list must not be empty")
	}
	for _, c := range got {
		if c.Mods == 0 || c.Keysym == 0 {
			t.Errorf("the built-in list contains a broken entry: %+v", c)
		}
	}
}

// Switching it off has to switch it off: it is the way out for whoever
// disagrees about which combinations are worth taking from the desktop.
func TestTTYXKeyboardCanBeSwitchedOff(t *testing.T) {
	saved := config.App.TTYXKeys
	config.App.TTYXKeys = false
	defer func() { config.App.TTYXKeys = saved }()

	if k := StartTTYXKeyboard(nil); k != nil {
		k.Close()
		t.Error("Keys=0 must stop it starting at all")
	}
}

// It is on unless it is turned off, because a Ctrl+Enter that only works after
// the user has found a setting does not work.
func TestTTYXKeyboardOnByDefault(t *testing.T) {
	if !config.App.TTYXKeys {
		t.Error("the default must be on")
	}
}

// Ctrl+Enter is the combination this exists for, so it has to be in the list
// that is asked for when nobody says otherwise.
func TestDefaultTTYXKeyListHasCtrlEnter(t *testing.T) {
	got, _ := parseTTYXCombos(config.DefaultTTYXKeyList)
	for _, c := range got {
		if c.Keysym == 0xFF0D && c.Mods == ttyx.ModCtrl {
			return
		}
	}
	t.Errorf("Ctrl+Enter is missing from the built-in list: %+v", got)
}

// Ctrl+Shift+P is the command palette, and on a terminal with no extended
// keyboard protocol it is delivered as a bare Ctrl+P: Shift over a letter does
// not change the control byte. VTE implements none of the protocols vtinput
// asks for, so on GNOME Terminal the X server is the only place the real chord
// can be had (issue #980).
func TestDefaultTTYXKeyListHasCommandPalette(t *testing.T) {
	got, _ := parseTTYXCombos(config.DefaultTTYXKeyList)
	for _, c := range got {
		if c.Keysym == 'p' && c.Mods == (ttyx.ModCtrl|ttyx.ModShift) {
			return
		}
	}
	t.Errorf("Ctrl+Shift+P is missing from the built-in list: %+v", got)
}

// Every method has to survive being called on a nil keyboard, because that is
// what "not available here" looks like to the session loop.
func TestTTYXKeyboardNilIsSafe(t *testing.T) {
	var k *ttyxKeyboard
	k.Close()
	k.Close()
}
