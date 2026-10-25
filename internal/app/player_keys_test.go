package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The keys of the player, pressed through the key router the way a user
// presses them (#2185). The player plays into a paneltest.FakeAudio, which
// says what it was told to play.

// playerFixture is a panels frame whose active panel, the right one, lists
// files of a temporary folder, with the cursor on the first of them, and the
// player opened from it by Ctrl+Shift+M: the player is on the left. The
// playlist lives in a profile of the test's own.
type playerFixture struct {
	t      *testing.T
	pf     *panel.PanelsFrame
	player *panel.PlayerPanel
	audio  *paneltest.FakeAudio
	dir    string
	scr    *vtui.ScreenBuf
}

func newPlayerFixture(t *testing.T, files ...string) *playerFixture {
	t.Helper()
	profile := t.TempDir()
	oldUserConfigDir := config.UserConfigDir
	config.UserConfigDir = func() (string, error) { return profile, nil }
	t.Cleanup(func() { config.UserConfigDir = oldUserConfigDir })
	config.ResetConfigDirForTest()
	t.Cleanup(config.ResetConfigDirForTest)
	engines := paneltest.UseFakeAudio(t)

	f := &playerFixture{t: t, scr: initFrameworkActionTestScreen(t), dir: t.TempDir()}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.pf = paneltest.SetupMockPanelsFrame(t)
	f.pf.ResizeConsole(100, 30)
	t.Cleanup(f.pf.Close)
	f.pf.ShowPanels = true
	vtui.FrameManager.Push(f.pf)
	fsp := f.pf.GetActivePanel()
	f.pf.SwitchToVFS(fsp, vfs.NewOSVFS(f.dir))
	paneltest.WaitForLoad(t, fsp)
	fsp.SetCursorIndex(1)

	f.press(playerKey(vtinput.VK_M, vtinput.LeftCtrlPressed|vtinput.ShiftPressed))
	player, ok := f.pf.AltPanels[1-f.pf.ActiveIdx].(*panel.PlayerPanel)
	if !ok {
		t.Fatalf("Ctrl+Shift+M opened %T in the passive slot, want the player", f.pf.AltPanels[1-f.pf.ActiveIdx])
	}
	t.Cleanup(player.Close)
	made := engines()
	if len(made) != 1 {
		t.Fatalf("the player made %d audio engines, want 1", len(made))
	}
	f.player, f.audio = player, made[0]
	return f
}

// press is a key pressed in the panels, and the screen drawn after it as f4
// draws it before the next key: drawing is what gives an alternative panel
// the focus after Tab.
func (f *playerFixture) press(e *vtinput.InputEvent) {
	f.t.Helper()
	pressKey(f.pf, e)
	f.settle()
	f.pf.Show(f.scr)
}

// pressIn is a key pressed in a dialog over the panels, which the frame
// manager drops from the screen once the key closed it.
func (f *playerFixture) pressIn(dlg vtui.Frame, e *vtinput.InputEvent) {
	f.t.Helper()
	pressKey(dlg, e)
	f.settle()
	f.pf.Show(f.scr)
}

// settle drops the frames a key closed, as the frame manager's loop does
// after each event; a test has no loop.
func (f *playerFixture) settle() {
	for top := vtui.FrameManager.GetTopFrame(); top != nil && top != f.pf && top.IsDone(); top = vtui.FrameManager.GetTopFrame() {
		vtui.FrameManager.Pop()
	}
}

// newFolder is F7 in the player and name typed into the dialog it opens.
func (f *playerFixture) newFolder(name string) {
	f.t.Helper()
	f.press(playerKey(vtinput.VK_F7, 0))
	dlg := vtui.FrameManager.GetTopFrame()
	if dlg == f.pf {
		f.t.Fatal("F7 in the player opened nothing")
	}
	for _, ch := range name {
		pressKey(dlg, playerChar(0, ch))
	}
	f.pressIn(dlg, playerKey(vtinput.VK_RETURN, 0))
}

func (f *playerFixture) path(name string) string { return filepath.Join(f.dir, name) }

func playerKey(vk uint16, mods vtinput.ControlKeyState) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: mods}
}

func playerChar(vk uint16, ch rune) *vtinput.InputEvent {
	return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, Char: ch}
}

// The report: music on the right, the player on the left with those files in
// its playlist, a track of the playlist playing. Tab to the player and back,
// and the file under the cursor of the file panel played instead -- on the
// next arrow there, which the player follows. A track of the playlist now
// stays until the user picks another.
func TestPlayerKeepsThePlaylistTrackWhenTheFocusComesBack(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3", "b.mp3")

	f.press(playerKey(vtinput.VK_F5, 0)) // a.mp3 into the playlist
	f.press(playerKey(vtinput.VK_TAB, 0))
	if !f.player.IsFocused() {
		t.Fatal("Tab did not move the focus to the player")
	}
	f.press(playerKey(vtinput.VK_RETURN, 0)) // play it from the playlist
	if got := f.audio.Path(); got != f.path("a.mp3") {
		t.Fatalf("Enter on the playlist track loaded %q, want a.mp3", got)
	}

	f.press(playerKey(vtinput.VK_TAB, 0))
	if f.player.IsFocused() {
		t.Fatal("the second Tab left the focus in the player")
	}
	f.press(playerKey(vtinput.VK_DOWN, 0))
	if got := f.pf.GetActivePanel().GetSelectedName(); got != "b.mp3" {
		t.Fatalf("Down put the cursor on %q, want b.mp3", got)
	}
	if got := f.audio.Path(); got != f.path("a.mp3") {
		t.Errorf("an arrow in the file panel swapped the playlist track for %q (loads %q)", got, f.audio.Loads)
	}
}

// Without a track of the playlist the player still follows the cursor of the
// file panel, as Far's AudioPlayer does: Enter plays a recording, and Down
// plays the next one (#380).
func TestPlayerFollowsTheFilePanelWhenItPlaysItsFiles(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3", "b.mp3")

	f.press(playerKey(vtinput.VK_RETURN, 0))
	if got := f.audio.Path(); got != f.path("a.mp3") {
		t.Fatalf("Enter on a recording loaded %q, want a.mp3", got)
	}
	f.press(playerKey(vtinput.VK_DOWN, 0))
	if got := f.audio.Path(); got != f.path("b.mp3") {
		t.Errorf("Down in the file panel loaded %q, want b.mp3", got)
	}
}

// Z X C V B are told by the key they are on: with the Russian layout on they
// type я ч с м и, and the player took them for nothing at all. A terminal
// sends only the character; the Windows console and the kitty protocol send
// the key too.
func TestPlayerLetterKeysWorkInTheRussianLayout(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3")
	f.press(playerKey(vtinput.VK_F5, 0))
	f.press(playerKey(vtinput.VK_TAB, 0))

	for _, how := range []struct {
		name    string
		withKey bool
	}{{"the character alone", false}, {"the key and the character", true}} {
		key := func(vk uint16, ch rune) *vtinput.InputEvent {
			if !how.withKey {
				vk = 0
			}
			return playerChar(vk, ch)
		}
		f.press(key(vtinput.VK_X, 'ч'))
		if got := f.audio.Path(); got != f.path("a.mp3") || !f.audio.IsPlaying() {
			t.Errorf("%s: ч (X, Play) left the engine with %q, playing=%v", how.name, got, f.audio.IsPlaying())
		}
		f.press(key(vtinput.VK_C, 'с'))
		if f.audio.IsPlaying() {
			t.Errorf("%s: с (C, Pause) did not pause", how.name)
		}
		f.press(key(vtinput.VK_V, 'м'))
		if f.audio.Path() != "" {
			t.Errorf("%s: м (V, Stop) did not stop", how.name)
		}
	}
	if !f.pf.CmdLine.IsEmpty() {
		t.Errorf("the letters went to the command line too: %q", f.pf.CmdLine.Edit.GetText())
	}
}

// F7 made a playlist folder only with the cursor in the playlist, and with
// the playlist empty the cursor never leaves the buttons, so the first folder
// could not be made at all. It works on the buttons too.
func TestPlayerF7MakesAFolderFromTheButtons(t *testing.T) {
	f := newPlayerFixture(t)
	f.press(playerKey(vtinput.VK_TAB, 0))

	f.newFolder("Album")
	if got := f.player.GetSelectedName(); got != "Album" {
		t.Errorf("after F7 the cursor of the player is on %q, want the new folder Album", got)
	}
}

// Ctrl with the arrows moved playlist items, and macOS keeps those keys for
// Mission Control. Shift with the arrows moves them now, on every system; Ctrl
// still works where it reaches f4.
func TestPlayerShiftArrowsMovePlaylistItems(t *testing.T) {
	for _, mod := range []struct {
		name string
		key  vtinput.ControlKeyState
	}{{"Shift", vtinput.ShiftPressed}, {"Ctrl", vtinput.LeftCtrlPressed}} {
		t.Run(mod.name, func(t *testing.T) {
			f := newPlayerFixture(t, "a.mp3", "b.mp3")
			selected := func() string { return f.player.GetSelectedName() }
			// Both files into the playlist: a, then b.
			f.press(playerKey(vtinput.VK_INSERT, 0))
			f.press(playerKey(vtinput.VK_INSERT, 0))
			f.press(playerKey(vtinput.VK_F5, 0))
			f.press(playerKey(vtinput.VK_TAB, 0))
			if got := selected(); got != "a" {
				t.Fatalf("the playlist starts at %q, want a", got)
			}

			f.press(playerKey(vtinput.VK_DOWN, mod.key))
			if got := selected(); got != "a" {
				t.Fatalf("%s+Down left the cursor on %q, want it to follow a", mod.name, got)
			}
			f.press(playerKey(vtinput.VK_UP, 0))
			if got := selected(); got != "b" {
				t.Fatalf("%s+Down did not move a below b: above a is %q", mod.name, got)
			}

			// A folder at the end, moved to the top: F, b, a.
			f.press(playerKey(vtinput.VK_UP, 0)) // the buttons
			f.newFolder("F")
			f.press(playerKey(vtinput.VK_UP, mod.key))
			f.press(playerKey(vtinput.VK_UP, mod.key))
			f.press(playerKey(vtinput.VK_DOWN, 0))
			if got := selected(); got != "b" {
				t.Fatalf("below the folder is %q, want b", got)
			}
			// b into the folder above it, and out again.
			f.press(playerKey(vtinput.VK_RIGHT, mod.key))
			f.press(playerKey(vtinput.VK_LEFT, 0)) // Left on a track in a folder goes to the folder
			if got := selected(); got != "F" {
				t.Fatalf("%s+Right did not put b into the folder: Left from it went to %q", mod.name, got)
			}
			f.press(playerKey(vtinput.VK_DOWN, 0))
			f.press(playerKey(vtinput.VK_LEFT, mod.key))
			f.press(playerKey(vtinput.VK_LEFT, 0)) // on the top level it stays
			if got := selected(); got != "b" {
				t.Errorf("%s+Left did not take b out of the folder: Left from it went to %q", mod.name, got)
			}
			if f.pf.WidthDecrement != 0 {
				t.Errorf("%s with the arrows moved the split between the panels by %d", mod.name, f.pf.WidthDecrement)
			}
		})
	}
}

// YouTube's keys next to WinAmp's (#2185): K plays and pauses, J and L move
// ten seconds, the digits jump to a tenth of the track, M mutes, Shift+N and
// Shift+P change the track. The letters by their key, so the Russian layout's
// л о д ь work as K J L M.
func TestPlayerYouTubeKeys(t *testing.T) {
	old := config.App.PlayerKeys
	t.Cleanup(func() { config.App.PlayerKeys = old })
	config.App.PlayerKeys = config.PlayerKeysBoth

	f := newPlayerFixture(t, "a.mp3", "b.mp3")
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_INSERT, 0))
	f.press(playerKey(vtinput.VK_F5, 0)) // a and b into the playlist
	f.press(playerKey(vtinput.VK_TAB, 0))

	f.press(playerChar(vtinput.VK_K, 'k'))
	if f.audio.Path() != f.path("a.mp3") || !f.audio.IsPlaying() {
		t.Fatalf("K with nothing playing: engine has %q, playing=%v; want a.mp3 playing", f.audio.Path(), f.audio.IsPlaying())
	}
	f.press(playerChar(0, 'л'))
	if f.audio.IsPlaying() {
		t.Error("л (K) did not pause")
	}
	f.press(playerChar(0, 'л'))
	if !f.audio.IsPlaying() {
		t.Error("л (K) again did not go on playing")
	}

	for _, step := range []struct {
		key  *vtinput.InputEvent
		name string
		want time.Duration
	}{
		{playerChar(vtinput.VK_L, 'l'), "L", 10 * time.Second},
		{playerChar(0, 'д'), "д (L)", 20 * time.Second},
		{playerChar(vtinput.VK_J, 'j'), "J", 10 * time.Second},
		{playerChar(0, '5'), "5", 90 * time.Second}, // half of the fake's three minutes
		{playerChar(0, '0'), "0", 0},
		{playerChar(0, 'о'), "о (J) at the start", 0},
	} {
		f.press(step.key)
		if got := f.audio.Position(); got != step.want {
			t.Errorf("%s left the track at %s, want %s", step.name, got, step.want)
		}
	}

	volume := f.audio.Volume()
	f.press(playerChar(0, 'ь'))
	if f.audio.Volume() != 0 {
		t.Errorf("ь (M) left the volume at %v, want it muted", f.audio.Volume())
	}
	f.press(playerChar(vtinput.VK_M, 'm'))
	if f.audio.Volume() != volume {
		t.Errorf("M again set the volume to %v, want %v back", f.audio.Volume(), volume)
	}

	shift := vtinput.ControlKeyState(vtinput.ShiftPressed)
	f.press(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_N, Char: 'N', ControlKeyState: shift})
	if got := f.audio.Path(); got != f.path("b.mp3") {
		t.Errorf("Shift+N played %q, want b.mp3", got)
	}
	f.press(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'З', ControlKeyState: shift})
	if got := f.audio.Path(); got != f.path("a.mp3") {
		t.Errorf("Shift+З (Shift+P) played %q, want a.mp3", got)
	}
	if !f.pf.CmdLine.IsEmpty() {
		t.Errorf("the keys went to the command line too: %q", f.pf.CmdLine.Edit.GetText())
	}
}

// The scheme is a setting: with WinAmp's alone K is no key of the player,
// with YouTube's alone X is not. The command palette runs the command, not
// its key, so Player: Stop stops whatever the scheme.
func TestPlayerKeySchemeSetting(t *testing.T) {
	old := config.App.PlayerKeys
	t.Cleanup(func() { config.App.PlayerKeys = old })

	for _, c := range []struct {
		scheme    string
		plays, no *vtinput.InputEvent
		name      string
	}{
		{config.PlayerKeysWinAmp, playerChar(vtinput.VK_X, 'x'), playerChar(vtinput.VK_K, 'k'), "WinAmp: X plays, K does not"},
		{config.PlayerKeysYouTube, playerChar(vtinput.VK_K, 'k'), playerChar(vtinput.VK_X, 'x'), "YouTube: K plays, X does not"},
	} {
		t.Run(c.scheme, func(t *testing.T) {
			config.App.PlayerKeys = c.scheme
			f := newPlayerFixture(t, "a.mp3")
			f.press(playerKey(vtinput.VK_F5, 0))
			f.press(playerKey(vtinput.VK_TAB, 0))

			f.press(c.no)
			if f.audio.Path() != "" {
				t.Errorf("%s: the key of the other scheme loaded %q", c.name, f.audio.Path())
			}
			f.pf.CmdLine.Clear() // where the other scheme's letter went
			f.press(c.plays)
			if f.audio.Path() != f.path("a.mp3") {
				t.Errorf("%s: the key of the scheme loaded %q", c.name, f.audio.Path())
			}
			if !RunAction("Panel.Player.Stop") || f.audio.Path() != "" {
				t.Errorf("%s: Player: Stop from the palette left %q loaded", c.name, f.audio.Path())
			}
		})
	}
}

// Left and Right on the buttons seeked while a track was loaded, so with
// something playing the cursor could not get from Play to Stop (#2185).
// They go from button to button whatever plays.
func TestPlayerArrowsGoBetweenButtonsWhileATrackPlays(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3")
	f.press(playerKey(vtinput.VK_F5, 0))
	f.press(playerKey(vtinput.VK_TAB, 0))
	f.press(playerKey(vtinput.VK_RETURN, 0)) // a.mp3 plays
	f.press(playerKey(vtinput.VK_HOME, vtinput.LeftCtrlPressed))

	f.press(playerKey(vtinput.VK_RIGHT, 0)) // Play to Pause
	f.press(playerKey(vtinput.VK_RIGHT, 0)) // Pause to Stop
	if got := f.audio.Position(); got != 0 {
		t.Errorf("Right on the buttons moved the track to %s", got)
	}
	f.press(playerKey(vtinput.VK_RETURN, 0))
	if got := f.audio.Path(); got != "" {
		t.Errorf("two Right from Play and Enter did not press Stop: %q still loaded", got)
	}
}

// The arrows seek on the playback bar, a row of its own above the buttons
// (Up from them), or on the clock with the bar off.
func TestPlayerSeeksOnThePlaybackBar(t *testing.T) {
	old := config.App.PlayerBar
	t.Cleanup(func() { config.App.PlayerBar = old })
	for _, bar := range []bool{true, false} {
		config.App.PlayerBar = bar
		f := newPlayerFixture(t, "a.mp3")
		f.press(playerKey(vtinput.VK_F5, 0))
		f.press(playerKey(vtinput.VK_TAB, 0))
		f.press(playerKey(vtinput.VK_RETURN, 0))
		f.press(playerKey(vtinput.VK_HOME, vtinput.LeftCtrlPressed))

		f.press(playerKey(vtinput.VK_UP, 0)) // onto the bar
		f.press(playerKey(vtinput.VK_RIGHT, 0))
		f.press(playerKey(vtinput.VK_RIGHT, 0))
		f.press(playerKey(vtinput.VK_LEFT, 0))
		if got := f.audio.Position(); got != 5*time.Second {
			t.Errorf("bar=%v: Right, Right, Left on the bar left the track at %s, want 5s", bar, got)
		}
		f.press(playerKey(vtinput.VK_DOWN, 0)) // back to the buttons
		f.press(playerKey(vtinput.VK_RIGHT, 0))
		if got := f.audio.Position(); got != 5*time.Second {
			t.Errorf("bar=%v: Right on the buttons after the bar moved the track to %s", bar, got)
		}
	}
}

// In the playlist Left and Right seeked too while something played, and a
// folder could not be folded then. They fold and unfold it whatever plays.
func TestPlayerArrowsFoldAFolderWhileATrackPlays(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3")
	f.press(playerKey(vtinput.VK_F5, 0))
	f.press(playerKey(vtinput.VK_TAB, 0))
	f.press(playerKey(vtinput.VK_RETURN, 0)) // a.mp3 plays
	f.press(playerKey(vtinput.VK_HOME, vtinput.LeftCtrlPressed))
	f.newFolder("F") // a, F
	f.press(playerKey(vtinput.VK_UP, vtinput.ShiftPressed))
	f.press(playerKey(vtinput.VK_DOWN, 0))
	f.press(playerKey(vtinput.VK_RIGHT, vtinput.ShiftPressed)) // F with a in it
	f.press(playerKey(vtinput.VK_UP, 0))
	if got := f.player.GetSelectedName(); got != "F" {
		t.Fatalf("the cursor is on %q, want the folder F", got)
	}

	f.press(playerKey(vtinput.VK_LEFT, 0)) // fold F
	f.press(playerKey(vtinput.VK_DOWN, 0))
	if got := f.player.GetSelectedName(); got != "F" {
		t.Errorf("Left did not fold F while a.mp3 played: Down went to %q", got)
	}
	if got := f.audio.Position(); got != 0 {
		t.Errorf("Left in the playlist moved the track to %s", got)
	}
}

// Ctrl+Alt+Left and Right seek from the file panel, without going to the
// player -- but the action looked for the player in the slot that has the
// focus, which is the file panel's, and never found it.
func TestPlayerSeeksFromTheFilePanel(t *testing.T) {
	f := newPlayerFixture(t, "a.mp3")
	f.press(playerKey(vtinput.VK_RETURN, 0))
	if f.player.IsFocused() || f.audio.Path() == "" {
		t.Fatalf("Enter on a recording: player focused=%v, loaded %q", f.player.IsFocused(), f.audio.Path())
	}
	ctrlAlt := vtinput.ControlKeyState(vtinput.LeftCtrlPressed | vtinput.LeftAltPressed)
	f.press(playerKey(vtinput.VK_RIGHT, ctrlAlt))
	f.press(playerKey(vtinput.VK_RIGHT, ctrlAlt))
	f.press(playerKey(vtinput.VK_LEFT, ctrlAlt))
	if got := f.audio.Position(); got != 5*time.Second {
		t.Errorf("two Ctrl+Alt+Right and a Ctrl+Alt+Left left the track at %s, want 5s", got)
	}
}
