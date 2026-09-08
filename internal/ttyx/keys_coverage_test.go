package ttyx

import (
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/unxed/vtinput"
)

func TestModifierXMask(t *testing.T) {
	cases := []struct {
		name string
		mods Modifier
		want uint16
	}{
		{name: "none", want: 0},
		{name: "shift", mods: ModShift, want: xproto.ModMaskShift},
		{name: "control", mods: ModCtrl, want: xproto.ModMaskControl},
		{name: "alt", mods: ModAlt, want: xproto.ModMask1},
		{name: "all", mods: ModShift | ModCtrl | ModAlt, want: xproto.ModMaskShift | xproto.ModMaskControl | xproto.ModMask1},
		{name: "unknown bits ignored", mods: Modifier(1 << 15), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.mods.xmask(); got != tc.want {
				t.Fatalf("xmask(%#x) = %#x, want %#x", tc.mods, got, tc.want)
			}
		})
	}
}

func TestModsFromState(t *testing.T) {
	cases := []struct {
		name  string
		state uint16
		want  vtinput.ControlKeyState
	}{
		{name: "none"},
		{name: "shift", state: xproto.ModMaskShift, want: vtinput.ShiftPressed},
		{name: "control", state: xproto.ModMaskControl, want: vtinput.LeftCtrlPressed},
		{name: "alt", state: xproto.ModMask1, want: vtinput.LeftAltPressed},
		{name: "locks", state: xproto.ModMaskLock | xproto.ModMask2, want: vtinput.CapsLockOn | vtinput.NumLockOn},
		{name: "all", state: xproto.ModMaskShift | xproto.ModMaskControl | xproto.ModMask1 | xproto.ModMaskLock | xproto.ModMask2, want: vtinput.ShiftPressed | vtinput.LeftCtrlPressed | vtinput.LeftAltPressed | vtinput.CapsLockOn | vtinput.NumLockOn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modsFromState(tc.state); got != tc.want {
				t.Fatalf("modsFromState(%#x) = %#x, want %#x", tc.state, got, tc.want)
			}
		})
	}
}

func TestKeyStateAccessors(t *testing.T) {
	var empty Session
	if empty.Keys() != nil || empty.GrabReport() != nil || empty.Dropped() != 0 || empty.grabsHeld() {
		t.Fatal("an unconfigured session must expose empty key state")
	}

	events := make(chan *vtinput.InputEvent, 1)
	original := []GrabResult{{Keysym: 42, Mods: ModCtrl, Code: 24}}
	s := Session{keys: &keyState{events: events, dropped: 3, held: true, report: original}}
	if s.Keys() != events {
		t.Fatal("Keys must return the configured event stream")
	}
	if got := s.Dropped(); got != 3 {
		t.Fatalf("Dropped = %d, want 3", got)
	}
	if !s.grabsHeld() {
		t.Fatal("grabsHeld must report the key state")
	}
	report := s.GrabReport()
	if len(report) != 1 || report[0] != original[0] {
		t.Fatalf("GrabReport = %#v, want %#v", report, original)
	}
	report[0].Mods = ModAlt
	if original[0].Mods != ModCtrl {
		t.Fatal("GrabReport must return a copy")
	}
}
