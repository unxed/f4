package panel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/theme"
	id3 "github.com/unxed/id3-go"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// PlayerPanel is the Ctrl+Shift+M alternate panel: a WinAmp-shaped block of
// transport controls on top and a playlist underneath, separated by a rule.
// Files reach the playlist by F5 from the opposite file panel (see
// actionCopyMove); nothing is copied, the playlist keeps paths. F6 is
// refused so a slip of the finger cannot move music around on disk.
//
// There is a second way to use it, for going through a folder of
// recordings the way Far's AudioPlayer plugin does: with the player open,
// Enter on an audio file in the file panel plays that file right away
// without touching the playlist, and the panel stays where it was — so Ins
// selects, F8 deletes, Down and Enter listens to the next one. When such a
// file ends the next recording in the same panel plays, and |< >| step
// through the panel's files instead of the playlist (see PlayFile).
//
// Keyboard model, chosen so that Tab keeps its f4 meaning (switch panels):
// Up/Down walk one column that starts at the playback bar, goes on to the
// control row and continues into the playlist; Left/Right seek on the bar,
// pick a button on the control row, and collapse/expand a folder in the
// playlist. Enter and Space act on whatever has the cursor. The letter keys
// (WinAmp's or YouTube's) and +/- for volume work anywhere in the panel.
type PlayerPanel struct {
	vtui.ScreenObject
	// The session is what plays (player_session.go); the panel shows it
	// and has the cursor.
	*playerSession
	src     *FileSystemPanel
	frame   *vtui.BorderedFrame
	focused bool

	rows   []playlistRow // flattened view, rebuilt on every Show
	cursor int           // index into rows, or playerCursorButtons / playerCursorBar
	button int           // focused control when cursor == playerCursorButtons
	top    int           // first visible playlist row
	// followed is the cursor the view last scrolled to; the view follows
	// the cursor when it moves, and the wheel scrolls away from it.
	followed int

	// The mouse (player_mouse.go): where Show put what a click can hit,
	// what the button held since it went down drags, and where the knob of
	// the playback bar is while it is dragged, 0 to 1.
	layout   playerLayout
	drag     playerDrag
	dragSeek float64
}

type playlistItem struct {
	Name     string          `json:"name"`
	Path     string          `json:"path,omitempty"`
	Folder   bool            `json:"folder,omitempty"`
	Expanded bool            `json:"expanded,omitempty"`
	Children []*playlistItem `json:"children,omitempty"`
	parent   *playlistItem
}

type playlistRow struct {
	item  *playlistItem
	depth int
}

const (
	playerBtnPrev = iota
	playerBtnPlay
	playerBtnPause
	playerBtnStop
	playerBtnNext
	playerBtnVolume
	playerBtnCount
)

// controlRows is the height of the block above the playlist: the title; the
// playback bar and the stream facts under it, or with the bar off the clock
// and the facts on one row; the two rows of the spectrum when it is on; the
// buttons.
func (pp *PlayerPanel) controlRows() int {
	rows := 3
	if config.App.PlayerBar {
		rows++
	}
	if config.App.PlayerSpectrum {
		rows += 2
	}
	return rows
}

const playerSeekStep = 5 * time.Second

// The rows above the playlist the cursor stops on: the buttons, and above
// them the playback bar, where the arrows seek.
const (
	playerCursorButtons = -1
	playerCursorBar     = -2
)

func NewPlayerPanel(src *FileSystemPanel) *PlayerPanel {
	x1, y1, x2, y2 := src.GetPosition()
	pp := &PlayerPanel{
		playerSession: openPlayerSession(),
		src:           src,
		cursor:        playerCursorButtons,
		button:        playerBtnPlay,
		followed:      playerCursorButtons,
	}
	pp.SetVisible(true)
	pp.frame = vtui.NewBorderedFrame(x1, y1, x2, y2, vtui.SingleBox, i18n.Msg("Player.Title"))
	pp.frame.ColorBoxIdx = theme.ColPanelBox
	pp.frame.ColorTitleIdx = theme.ColPanelTitle
	pp.frame.ColorBackgroundIdx = theme.ColPanelText
	pp.SetPosition(x1, y1, x2, y2)
	// A player opened over music playing in the background shows it as it
	// is, with the cursor on the track that plays.
	if pp.current != nil {
		pp.cursorTo(pp.current)
	}
	return pp
}

func (pp *PlayerPanel) SetPosition(x1, y1, x2, y2 int) {
	pp.ScreenObject.SetPosition(x1, y1, x2, y2)
	if pp.frame != nil {
		pp.frame.SetPosition(x1, y1, x2, y2)
	}
}

// FocusedPlayer is the player when it has the focus in the panels, or nil.
// The Panel.Player.* actions act on it.
func (pf *PanelsFrame) FocusedPlayer() *PlayerPanel {
	if pf == nil || !pf.ShowPanels || pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.AltPanels) {
		return nil
	}
	if pp, ok := pf.AltPanels[pf.ActiveIdx].(*PlayerPanel); ok && pp.IsFocused() {
		return pp
	}
	return nil
}

func (pp *PlayerPanel) Source() *FileSystemPanel { return pp.src }
func (pp *PlayerPanel) Kind() string             { return "player" }
func (pp *PlayerPanel) SetFocus(f bool)          { pp.focused = f }
func (pp *PlayerPanel) IsFocused() bool          { return pp.focused }

// HelpTopic is the topic F1 opens while the player has the focus.
func (pp *PlayerPanel) HelpTopic() string { return "Player" }

func (pp *PlayerPanel) GetSelectedName() string {
	if pp.cursor >= 0 && pp.cursor < len(pp.rows) {
		return pp.rows[pp.cursor].item.Name
	}
	return ""
}

// Close is the panel going away. The music it played goes on in the
// background (playerSession); the question whether it should comes before
// the close (AskClose).
func (pp *PlayerPanel) Close() {
	if pp.playerSession != nil {
		pp.leave()
	}
}

// AskClose: closing the player while music plays asks whether to go on
// playing it in the background or to stop it; Cancel and Esc keep the player
// open. With nothing loaded, or another player still showing it, the player
// just closes.
func (pp *PlayerPanel) AskClose(proceed func()) {
	if pp.playerSession == nil || pp.views > 1 || !pp.Engine.IsLoaded() {
		proceed()
		return
	}
	dlg := vtui.ShowMessage(i18n.Msg("Player.Title"), i18n.Msg("Player.CloseWhilePlaying"),
		[]string{i18n.Msg("Player.PlayInBackground"), i18n.Msg("Player.StopPlaying"), i18n.Msg("vtui.Cancel")})
	dlg.OnResult = func(code int) {
		switch code {
		case 0:
			proceed()
		case 1:
			pp.stopPlaying()
			proceed()
		}
	}
}

// ---- playlist model ---------------------------------------------------

func marshalPlaylist(root *playlistItem) ([]byte, error) {
	return json.MarshalIndent(root.Children, "", " ")
}

func unmarshalPlaylist(data []byte, root *playlistItem) error {
	var items []*playlistItem
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	root.Children = items
	root.relink()
	return nil
}

func (it *playlistItem) relink() {
	for _, c := range it.Children {
		c.parent = it
		c.relink()
	}
}

// tracks lists every track in play order (depth first, folders included
// whether or not they are expanded on screen).
func (it *playlistItem) tracks(out []*playlistItem) []*playlistItem {
	for _, c := range it.Children {
		if c.Folder {
			out = c.tracks(out)
		} else {
			out = append(out, c)
		}
	}
	return out
}

func (it *playlistItem) indexInParent() int {
	if it.parent == nil {
		return -1
	}
	for i, c := range it.parent.Children {
		if c == it {
			return i
		}
	}
	return -1
}

func (it *playlistItem) detach() {
	if i := it.indexInParent(); i >= 0 {
		p := it.parent
		p.Children = append(p.Children[:i], p.Children[i+1:]...)
	}
	it.parent = nil
}

func (it *playlistItem) insertChild(at int, c *playlistItem) {
	if at < 0 || at > len(it.Children) {
		at = len(it.Children)
	}
	it.Children = append(it.Children, nil)
	copy(it.Children[at+1:], it.Children[at:])
	it.Children[at] = c
	c.parent = it
}

func (pp *PlayerPanel) rebuildRows() {
	pp.rows = pp.rows[:0]
	var walk func(it *playlistItem, depth int)
	walk = func(it *playlistItem, depth int) {
		for _, c := range it.Children {
			pp.rows = append(pp.rows, playlistRow{item: c, depth: depth})
			if c.Folder && c.Expanded {
				walk(c, depth+1)
			}
		}
	}
	walk(pp.root, 0)
	if pp.cursor >= len(pp.rows) {
		pp.cursor = len(pp.rows) - 1
	}
}

// AddPaths is what F5 calls. Directories are walked and become folders;
// anything that is not an audio file (see audioFormats) is ignored quietly
// — the user selected a directory of music, not a list of files to be
// argued about.
func (pp *PlayerPanel) AddPaths(paths []string) int {
	added := 0
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			folder := &playlistItem{Name: filepath.Base(p), Folder: true, Expanded: true}
			n := fillFolderFromDir(folder, p)
			if n > 0 {
				pp.root.insertChild(-1, folder)
				added += n
			}
			continue
		}
		if it := playlistItemForFile(p); it != nil {
			pp.root.insertChild(-1, it)
			added++
		}
	}
	if added > 0 {
		pp.savePlaylist()
		pp.rebuildRows()
		if pp.cursor < 0 && len(pp.rows) > 0 {
			pp.cursor = 0
		}
	}
	return added
}

func fillFolderFromDir(folder *playlistItem, dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			sub := &playlistItem{Name: e.Name(), Folder: true}
			if m := fillFolderFromDir(sub, full); m > 0 {
				folder.insertChild(-1, sub)
				n += m
			}
			continue
		}
		if it := playlistItemForFile(full); it != nil {
			folder.insertChild(-1, it)
			n++
		}
	}
	return n
}

func playlistItemForFile(path string) *playlistItem {
	if !media.IsAudioFile(path) {
		return nil
	}
	return &playlistItem{Name: trackDisplayName(path), Path: path}
}

// trackDisplayName is "Artist - Title" from ID3 when both are there, else
// the file name without extension. Only MP3 carries ID3 in practice; for
// anything else the name is the file name, which for a dictaphone
// recording is the date and time and exactly what one wants to see.
func trackDisplayName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if !strings.EqualFold(filepath.Ext(path), ".mp3") {
		return base
	}
	f, err := id3.Open(path)
	if err != nil {
		return base
	}
	defer f.Close()
	clean := func(s string) string { return strings.TrimSpace(strings.TrimRight(s, "\x00")) }
	title, artist := clean(f.Title()), clean(f.Artist())
	switch {
	case title != "" && artist != "":
		return artist + " - " + title
	case title != "":
		return title
	}
	return base
}

// ---- buttons ----------------------------------------------------------

func (pp *PlayerPanel) pressButton(b int) {
	switch b {
	case playerBtnPrev:
		pp.playRelative(-1)
	case playerBtnPlay:
		switch {
		case pp.Engine.IsLoaded() && !pp.Engine.IsPlaying():
			pp.Engine.Play()
		case pp.Engine.IsLoaded():
			// Play on a playing track restarts it, like WinAmp.
			pp.playItem(pp.current)
		case pp.cursor >= 0 && pp.cursor < len(pp.rows) && !pp.rows[pp.cursor].item.Folder:
			pp.playItem(pp.rows[pp.cursor].item)
		default:
			pp.playRelative(+1)
		}
	case playerBtnPause:
		pp.Engine.TogglePause()
	case playerBtnStop:
		pp.stopPlaying()
	case playerBtnNext:
		pp.playRelative(+1)
	case playerBtnVolume:
		// Enter on the volume slider does nothing; +/- and Left/Right adjust it.
	}
}

// PlayerCommand is something the player does on a key and from the command
// palette (Panel.Player.*). The palette asks for the command, not for its key:
// which keys work depends on config.App.PlayerKeys.
type PlayerCommand int

const (
	PlayerPrevious PlayerCommand = iota
	PlayerPlay
	PlayerPause
	PlayerStop
	PlayerNext
	PlayerPlayPause
	PlayerVolumeUp
	PlayerVolumeDown
	PlayerMute
	PlayerSeekBack10
	PlayerSeekForward10
	PlayerToggleKeyHints
)

// Do does cmd and reports whether it was one the player knows.
func (pp *PlayerPanel) Do(cmd PlayerCommand) bool {
	switch cmd {
	case PlayerPrevious:
		pp.pressButton(playerBtnPrev)
	case PlayerPlay:
		pp.pressButton(playerBtnPlay)
	case PlayerPause:
		pp.pressButton(playerBtnPause)
	case PlayerStop:
		pp.pressButton(playerBtnStop)
	case PlayerNext:
		pp.pressButton(playerBtnNext)
	case PlayerPlayPause:
		if pp.Engine.IsLoaded() {
			pp.Engine.TogglePause()
		} else {
			pp.pressButton(playerBtnPlay)
		}
	case PlayerVolumeUp:
		pp.adjustVolume(+0.05)
	case PlayerVolumeDown:
		pp.adjustVolume(-0.05)
	case PlayerMute:
		pp.toggleMute()
	case PlayerSeekBack10:
		pp.SeekBy(-10 * time.Second)
	case PlayerSeekForward10:
		pp.SeekBy(10 * time.Second)
	case PlayerToggleKeyHints:
		config.App.PlayerKeyHints = !config.App.PlayerKeyHints
		config.RequestSaveConfig()
	default:
		return false
	}
	return true
}

// ---- keys ------------------------------------------------------------

func (pp *PlayerPanel) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown || !pp.focused {
		return false
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	if alt {
		return false
	}
	handled := true
	switch {
	case !ctrl && pp.globalKey(e):
	case pp.cursor == playerCursorBar:
		handled = pp.barKey(e)
	case pp.cursor < 0:
		handled = pp.controlKey(e)
	default:
		handled = pp.playlistKey(e, ctrl, shift)
	}
	if !handled && !ctrl && isFilePanelOnlyKey(e.VirtualKeyCode) {
		// F3/F4/F5/F8 and their Shift variants, Ins, Del: with the player
		// in the slot there is no file under the cursor, and letting them
		// fall through would view, edit, copy or delete whatever the file
		// panel behind the player happens to remember (#902). Swallow
		// them; Tab, F1, F9, F10 and the Ctrl combinations still go on.
		return true
	}
	if handled {
		vtui.FrameManager.Redraw()
	}
	return handled
}

// OwnsKey: while the player has the focus it keeps (altPanelKeyOwner) the
// characters -- WinAmp's Z X C V B, + and - for the volume -- which must not
// go to the command line, Enter and Del, and Shift or Ctrl with the arrows,
// which move playlist items (docs/PLAYER.md) instead of marking files or the
// split between the panels, and Ctrl+Home. The file keys no focused
// alternative panel lets through (altPanelListsFiles, #380, #902).
func (pp *PlayerPanel) OwnsKey(e *vtinput.InputEvent) bool {
	if e.Char != 0 || e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_DELETE {
		return true
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	if alt || ctrl == shift {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_LEFT, vtinput.VK_RIGHT:
		return true
	case vtinput.VK_HOME:
		return ctrl
	}
	return false
}

// globalKey handles the letter keys of the scheme config.App.PlayerKeys
// names, the digits of YouTube's, and the volume keys, which work wherever
// the cursor is. The letters are told by the key they are on, not by what it
// types: with the Russian layout on, Z types я, and the player still has to
// go back a track (#2185).
func (pp *PlayerPanel) globalKey(e *vtinput.InputEvent) bool {
	if tenth, ok := playerDigit(e); ok && config.App.PlayerKeys != config.PlayerKeysWinAmp {
		pp.JumpToTenth(tenth)
		return true
	}
	cmd, ok := playerKeyCommand(e, config.App.PlayerKeys)
	if ok {
		pp.Do(cmd)
	}
	return ok
}

// playerKeyCommand is the command of a key in the focused player with the
// letter scheme scheme. WinAmp: Z X C V B. YouTube: K play or pause, J and L
// ten seconds back and forward, M mute, Shift+P and Shift+N the previous and
// the next track. The two share no key, so both can be on. H, with either,
// shows the keys on the player and hides them again.
func playerKeyCommand(e *vtinput.InputEvent, scheme string) (PlayerCommand, bool) {
	winamp := scheme != config.PlayerKeysYouTube
	youtube := scheme != config.PlayerKeysWinAmp
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	switch letter := playerLetterKey(e); {
	case letter == vtinput.VK_H:
		return PlayerToggleKeyHints, true
	case winamp && letter == vtinput.VK_Z:
		return PlayerPrevious, true
	case winamp && letter == vtinput.VK_X:
		return PlayerPlay, true
	case winamp && letter == vtinput.VK_C:
		return PlayerPause, true
	case winamp && letter == vtinput.VK_V:
		return PlayerStop, true
	case winamp && letter == vtinput.VK_B:
		return PlayerNext, true
	case youtube && letter == vtinput.VK_K:
		return PlayerPlayPause, true
	case youtube && letter == vtinput.VK_J:
		return PlayerSeekBack10, true
	case youtube && letter == vtinput.VK_L:
		return PlayerSeekForward10, true
	case youtube && letter == vtinput.VK_M:
		return PlayerMute, true
	case youtube && shift && letter == vtinput.VK_P:
		return PlayerPrevious, true
	case youtube && shift && letter == vtinput.VK_N:
		return PlayerNext, true
	case e.Char == '+' || e.Char == '=' || e.VirtualKeyCode == vtinput.VK_OEM_PLUS || e.VirtualKeyCode == vtinput.VK_ADD:
		return PlayerVolumeUp, true
	case e.Char == '-' || e.Char == '_' || e.VirtualKeyCode == vtinput.VK_OEM_MINUS || e.VirtualKeyCode == vtinput.VK_SUBTRACT:
		return PlayerVolumeDown, true
	}
	return 0, false
}

// playerDigit is the digit of a digit key pressed without Shift, which types
// the same digit in every layout.
func playerDigit(e *vtinput.InputEvent) (int, bool) {
	if e.ControlKeyState&vtinput.ShiftPressed != 0 {
		return 0, false
	}
	switch {
	case e.Char >= '0' && e.Char <= '9':
		return int(e.Char - '0'), true
	case e.Char == 0 && e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9:
		return int(e.VirtualKeyCode - vtinput.VK_0), true
	}
	return 0, false
}

// playerLetterKey is the Latin letter key e was typed on, or 0. That is the
// virtual key when the terminal or the window reported one; a terminal that
// sends only the character gives a letter of the layout that is on, and a
// Russian one is put back on its key.
func playerLetterKey(e *vtinput.InputEvent) uint16 {
	if vk := e.VirtualKeyCode; vk >= vtinput.VK_A && vk <= vtinput.VK_Z {
		return vk
	}
	if e.VirtualKeyCode != 0 || e.Char == 0 {
		return 0
	}
	if r := unicode.ToUpper(e.Char); r >= 'A' && r <= 'Z' {
		return uint16(r)
	}
	return keymap.LayoutLetterVK(e.Char)
}

// controlKey: on the buttons Left and Right go from one to the next, and on
// the volume slider make it louder or quieter, whether a track is loaded or
// not. They used to seek while one was, so nobody could get from Play to
// Stop with the arrows while it played (#2185); seeking is on the bar above.
func (pp *PlayerPanel) controlKey(e *vtinput.InputEvent) bool {
	switch e.VirtualKeyCode {
	case vtinput.VK_LEFT:
		if pp.button == playerBtnVolume {
			pp.adjustVolume(-0.05)
		} else if pp.button > 0 {
			pp.button--
		}
	case vtinput.VK_RIGHT:
		if pp.button == playerBtnVolume {
			pp.adjustVolume(+0.05)
		} else {
			pp.button++
		}
	case vtinput.VK_HOME:
		pp.button = 0
	case vtinput.VK_END:
		pp.button = playerBtnCount - 1
	case vtinput.VK_DOWN, vtinput.VK_NEXT:
		if len(pp.rows) > 0 {
			pp.cursor = 0
		}
	case vtinput.VK_RETURN, vtinput.VK_SPACE:
		pp.pressButton(pp.button)
	case vtinput.VK_UP:
		pp.cursor = playerCursorBar
	case vtinput.VK_F7:
		// On the buttons too, and with the playlist still empty, where the
		// cursor never leaves them: the folder goes to the end (#2185).
		pp.newFolder(nil)
	default:
		return false
	}
	return true
}

// barKey: the playback bar, or the clock with the bar off, is a row of the
// cursor's own above the buttons, and Left and Right there move the track
// five seconds. Up is the top of the column: nothing above, but the key is
// swallowed so the file panel behind the player does not take it.
func (pp *PlayerPanel) barKey(e *vtinput.InputEvent) bool {
	switch e.VirtualKeyCode {
	case vtinput.VK_LEFT:
		pp.SeekBy(-playerSeekStep)
	case vtinput.VK_RIGHT:
		pp.SeekBy(playerSeekStep)
	case vtinput.VK_DOWN, vtinput.VK_NEXT:
		pp.cursor = playerCursorButtons
	case vtinput.VK_UP, vtinput.VK_PRIOR:
	case vtinput.VK_RETURN, vtinput.VK_SPACE:
		pp.Do(PlayerPlayPause)
	case vtinput.VK_F7:
		pp.newFolder(nil)
	default:
		return false
	}
	return true
}

// playlistKey: Shift with an arrow moves the item under the cursor, and so
// does Ctrl, which is what the player had first. Shift is the one that works
// everywhere: macOS keeps Ctrl with the arrows for Mission Control and the
// switching of Spaces and never lets them reach the terminal (#2185).
func (pp *PlayerPanel) playlistKey(e *vtinput.InputEvent, ctrl, shift bool) bool {
	pp.rebuildRows()
	if pp.cursor >= len(pp.rows) {
		pp.cursor = len(pp.rows) - 1
	}
	if pp.cursor < 0 {
		pp.cursor = playerCursorButtons
		return pp.controlKey(e)
	}
	// Left and Right fold and unfold a folder, and Left takes a track to
	// its folder, also while something plays: they seeked then (#2185).
	move := ctrl || shift
	row := pp.rows[pp.cursor]
	it := row.item
	page := pp.playlistHeight()
	if page < 1 {
		page = 1
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_UP:
		if move {
			pp.moveItem(it, -1)
		} else if pp.cursor == 0 {
			pp.cursor = playerCursorButtons
		} else {
			pp.cursor--
		}
	case vtinput.VK_DOWN:
		if move {
			pp.moveItem(it, +1)
		} else if pp.cursor < len(pp.rows)-1 {
			pp.cursor++
		}
	case vtinput.VK_PRIOR:
		pp.cursor = max(0, pp.cursor-page)
	case vtinput.VK_NEXT:
		pp.cursor = min(len(pp.rows)-1, pp.cursor+page)
	case vtinput.VK_HOME:
		if ctrl {
			pp.cursor = playerCursorButtons
		} else {
			pp.cursor = 0
		}
	case vtinput.VK_END:
		pp.cursor = len(pp.rows) - 1
	case vtinput.VK_LEFT:
		if move {
			pp.moveOut(it)
		} else if it.Folder && it.Expanded {
			it.Expanded = false
			pp.savePlaylist()
		} else if it.parent != pp.root {
			pp.cursorTo(it.parent)
		}
	case vtinput.VK_RIGHT:
		if move {
			pp.moveInto(it)
		} else if it.Folder && !it.Expanded {
			it.Expanded = true
			pp.savePlaylist()
		}
	case vtinput.VK_RETURN:
		if it.Folder {
			it.Expanded = !it.Expanded
			pp.savePlaylist()
		} else {
			pp.playItem(it)
		}
	case vtinput.VK_SPACE:
		if pp.Engine.IsLoaded() {
			pp.Engine.TogglePause()
		} else if !it.Folder {
			pp.playItem(it)
		}
	case vtinput.VK_DELETE, vtinput.VK_F8:
		// F8 too, which deletes in the file panels: the key bar says Remove
		// over it while the player has the focus.
		if it == pp.current || (it.Folder && pp.currentInside(it)) {
			pp.Engine.Stop()
			pp.current = nil
		}
		it.detach()
		pp.savePlaylist()
	case vtinput.VK_F7:
		pp.newFolder(it)
	default:
		return false
	}
	pp.rebuildRows()
	return true
}

func (pp *PlayerPanel) currentInside(folder *playlistItem) bool {
	for _, t := range folder.tracks(nil) {
		if t == pp.current {
			return true
		}
	}
	return false
}

func (pp *PlayerPanel) cursorTo(it *playlistItem) {
	pp.rebuildRows()
	for i, r := range pp.rows {
		if r.item == it {
			pp.cursor = i
			return
		}
	}
}

// moveItem swaps it with its neighbour among its siblings.
func (pp *PlayerPanel) moveItem(it *playlistItem, delta int) {
	p := it.parent
	i := it.indexInParent()
	j := i + delta
	if p == nil || i < 0 || j < 0 || j >= len(p.Children) {
		return
	}
	p.Children[i], p.Children[j] = p.Children[j], p.Children[i]
	pp.savePlaylist()
	pp.cursorTo(it)
}

// moveInto drops it into the closest folder above it among its siblings.
func (pp *PlayerPanel) moveInto(it *playlistItem) {
	p := it.parent
	for i := it.indexInParent() - 1; i >= 0; i-- {
		if f := p.Children[i]; f.Folder && f != it {
			it.detach()
			f.insertChild(-1, it)
			f.Expanded = true
			pp.savePlaylist()
			pp.cursorTo(it)
			return
		}
	}
}

// moveOut lifts it one level up, right after the folder it was in.
func (pp *PlayerPanel) moveOut(it *playlistItem) {
	folder := it.parent
	if folder == nil || folder == pp.root {
		return
	}
	at := folder.indexInParent() + 1
	grand := folder.parent
	it.detach()
	grand.insertChild(at, it)
	pp.savePlaylist()
	pp.cursorTo(it)
}

func (pp *PlayerPanel) newFolder(after *playlistItem) {
	vtui.InputBox(i18n.Msg("Player.NewFolderTitle"), i18n.Msg("Player.NewFolderPrompt"), "", func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		f := &playlistItem{Name: name, Folder: true, Expanded: true}
		if after != nil && after.parent != nil {
			after.parent.insertChild(after.indexInParent()+1, f)
		} else {
			pp.root.insertChild(-1, f)
		}
		pp.savePlaylist()
		pp.cursorTo(f)
		vtui.FrameManager.Redraw()
	})
}

// ---- drawing ---------------------------------------------------------

func (pp *PlayerPanel) playlistHeight() int {
	return pp.Y2 - pp.Y1 - 2 - pp.controlRows() - 1
}

func fmtClock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

func (pp *PlayerPanel) Show(scr *vtui.ScreenBuf) {
	if pp.frame != nil {
		pp.frame.Show(scr)
	}
	w := pp.X2 - pp.X1 - 1
	if w < 8 || pp.Y2-pp.Y1 < 3 {
		return
	}
	text := vtui.Palette[theme.ColPanelText]
	hi := vtui.Palette[theme.ColPanelSelectedText]
	box := vtui.Palette[theme.ColPanelBox]
	x := pp.X1 + 1
	y := pp.Y1 + 1
	put := func(y int, s string, attr uint64) {
		s = runewidth.Truncate(s, w, "…")
		if pad := w - runewidth.StringWidth(s); pad > 0 {
			s += strings.Repeat(" ", pad)
		}
		scr.Write(x, y, vtui.StringToCharInfo(s, attr))
	}

	// Row 0: what is playing, scrolled when it does not fit.
	title := i18n.Msg("Player.Idle")
	if pp.status != "" {
		title = pp.status
	} else if pp.current != nil {
		title = "♪ " + pp.current.Name
		if pp.Engine.IsLoaded() && !pp.Engine.IsPlaying() && !pp.Engine.Finished() {
			title += " " + i18n.Msg("Player.PausedMark")
		}
	}
	if runewidth.StringWidth(title) > w && pp.Engine.IsPlaying() {
		title = marqueeSlice(title+"   ***   ", w, pp.marquee/2)
	}
	put(y, title, text)

	loaded := pp.Engine.IsLoaded()
	facts := ""
	if loaded {
		info := pp.Engine.Info()
		mode := i18n.Msg("Player.Stereo")
		if info.Mono {
			mode = i18n.Msg("Player.Mono")
		}
		facts = fmt.Sprintf("%dkbps %dkHz %s", info.BitrateKbps, info.SampleRate/1000, mode)
		if info.Codec != "" {
			facts = info.Codec + " " + facts
		}
	}
	// The bar, or the clock, is where the cursor stops to seek.
	barAttr := text
	if pp.focused && pp.cursor == playerCursorBar {
		barAttr = hi
	}
	row := y + 1
	pp.layout = playerLayout{barY: row}
	if config.App.PlayerBar {
		// Where the track is, on a bar between the two clocks, and the
		// stream facts under it. While the bar is dragged its knob is where
		// the mouse is; the track goes there when the button is let go.
		pos, dur := pp.Engine.Position(), pp.Engine.Duration()
		if pp.drag == playerDragBar && dur > 0 {
			pos = time.Duration(pp.dragSeek * float64(dur))
		}
		put(row, playbackBar(pos, dur, loaded, w), barAttr)
		if left, right := playbackClocks(pos, dur, loaded); loaded && dur > 0 {
			if start, width := playbackBarSpan(left, right, w); width >= 3 {
				pp.layout.barX, pp.layout.barW = x+start, width
			}
		}
		put(row+1, facts, text)
		row += 2
	} else {
		// The clock and the stream facts on one row.
		clock := "--:-- / --:--"
		if loaded {
			clock = fmtClock(pp.Engine.Position()) + " / " + fmtClock(pp.Engine.Duration())
		}
		line := clock
		if gap := w - runewidth.StringWidth(clock) - runewidth.StringWidth(facts); gap > 1 {
			line += strings.Repeat(" ", gap) + facts
		}
		put(row, line, barAttr)
		row++
	}

	// The spectrum, when it is on: two rows of eighth blocks, 16 levels a band.
	if config.App.PlayerSpectrum {
		bands := min(24, max(4, (w-2)/2))
		spec := pp.Engine.Spectrum(bands)
		var hiRow, loRow strings.Builder
		for _, v := range spec {
			if !pp.Engine.IsPlaying() {
				v = 0
			}
			lvl := int(v*16 + 0.5)
			hiRow.WriteString(eighthBlock(lvl-8) + " ")
			loRow.WriteString(eighthBlock(lvl) + " ")
		}
		put(row, hiRow.String(), text)
		put(row+1, loRow.String(), text)
		row += 2
	}

	// The last row: buttons and volume. While the keys are shown (H) each
	// button carries the letter that presses it.
	by := row
	labels := []string{"|<", " > ", "||", " # ", ">|"}
	if config.App.PlayerKeyHints {
		for i, key := range playerButtonKeys(config.App.PlayerKeys) {
			if key != "" {
				labels[i] = key + " " + strings.TrimSpace(labels[i])
			}
		}
	}
	bx := x
	pp.layout.buttonsY = by
	scr.FillRect(x, by, pp.X2-1, by, ' ', text)
	for i, l := range labels {
		attr := text
		if pp.focused && pp.cursor == playerCursorButtons && pp.button == i {
			attr = hi
		}
		cells := vtui.StringToCharInfo("["+l+"]", attr)
		scr.Write(bx, by, cells)
		pp.layout.buttons[i] = [2]int{bx, bx + len(cells) - 1}
		bx += len(cells) + 1
	}
	volW := w - (bx - x) - 5
	if volW >= 4 {
		pp.layout.volX, pp.layout.volW = bx, volW
		filled := int(pp.Engine.Volume()*float64(volW) + 0.5)
		bar := strings.Repeat("█", filled) + strings.Repeat("░", volW-filled)
		attr := text
		if pp.focused && pp.cursor == playerCursorButtons && pp.button == playerBtnVolume {
			attr = hi
		}
		scr.Write(bx, by, vtui.StringToCharInfo(bar, attr))
		scr.Write(bx+volW, by, vtui.StringToCharInfo(fmt.Sprintf("%3d%%", int(pp.Engine.Volume()*100+0.5)), text))
	}

	// Separator on the frame's own lines.
	sep := "├" + strings.Repeat("─", w) + "┤"
	scr.Write(pp.X1, by+1, vtui.StringToCharInfo(sep, box))
	pp.showKeyHints(scr, box)

	// Playlist.
	pp.rebuildRows()
	ph := pp.playlistHeight()
	py := by + 2
	if ph <= 0 {
		return
	}
	pp.layout.listY, pp.layout.listRows = py, ph
	// The view follows the cursor when it has moved; the wheel scrolls the
	// view away from a cursor that stays.
	if pp.cursor >= 0 && pp.cursor != pp.followed {
		if pp.cursor < pp.top {
			pp.top = pp.cursor
		}
		if pp.cursor >= pp.top+ph {
			pp.top = pp.cursor - ph + 1
		}
	}
	pp.followed = pp.cursor
	pp.top = max(0, min(pp.top, len(pp.rows)-ph))
	if len(pp.rows) == 0 {
		put(py, i18n.Msg("Player.EmptyPlaylist"), text)
		return
	}
	for i := 0; i < ph; i++ {
		ri := pp.top + i
		if ri >= len(pp.rows) {
			put(py+i, "", text)
			continue
		}
		r := pp.rows[ri]
		mark := "  "
		switch {
		case r.item.Folder && r.item.Expanded:
			mark = "▾ "
		case r.item.Folder:
			mark = "▸ "
		case r.item == pp.current:
			mark = "♪ "
		}
		s := strings.Repeat("  ", r.depth) + mark + r.item.Name
		attr := text
		if pp.focused && ri == pp.cursor {
			attr = hi
		}
		put(py+i, s, attr)
	}
}

// playbackClocks are the clocks at the two ends of the playback bar.
func playbackClocks(pos, dur time.Duration, loaded bool) (left, right string) {
	left, right = "--:--", "--:--"
	if loaded {
		left = fmtClock(pos)
		if dur > 0 {
			right = fmtClock(dur)
		}
	}
	return left, right
}

// playbackBarSpan is where the bar starts in its row, w cells wide, between
// the clocks left and right, and how wide it is; under 3 cells there is none.
func playbackBarSpan(left, right string, w int) (start, width int) {
	lw := runewidth.StringWidth(left)
	return lw + 1, w - lw - runewidth.StringWidth(right) - 2
}

// showKeyHints writes on the bottom border how to see the player's keys, or,
// while they are shown, the keys the buttons do not carry: as many as the
// border has room for, and always the one that hides them again. The full
// list is too long for a panel, and a player that showed it all the time
// would be a page of keys with a player in it (#2185).
func (pp *PlayerPanel) showKeyHints(scr *vtui.ScreenBuf, attr uint64) {
	room := pp.X2 - pp.X1 - 3
	line := i18n.Msg("Player.KeysHint")
	if config.App.PlayerKeyHints {
		key := "Player.KeysLine.Both"
		switch config.App.PlayerKeys {
		case config.PlayerKeysWinAmp:
			key = "Player.KeysLine.WinAmp"
		case config.PlayerKeysYouTube:
			key = "Player.KeysLine.YouTube"
		}
		line = fitHints(strings.Split(i18n.Msg(key), "|"), room-2)
	}
	if line = " " + line + " "; runewidth.StringWidth(line) <= room {
		scr.Write(pp.X1+2, pp.Y2, vtui.StringToCharInfo(line, attr))
	}
}

// fitHints joins as many of hints as fit in room, in their order, and the
// last of them whatever room is left.
func fitHints(hints []string, room int) string {
	const gap = "  "
	last := hints[len(hints)-1]
	width := runewidth.StringWidth(last)
	var shown []string
	for _, h := range hints[:len(hints)-1] {
		w := runewidth.StringWidth(h) + len(gap)
		if width+w > room {
			break
		}
		shown = append(shown, h)
		width += w
	}
	return strings.Join(append(shown, last), gap)
}

// playerButtonKeys are the letters the buttons carry while the keys are
// shown: WinAmp's when they are on, else YouTube's, which has no stop.
func playerButtonKeys(scheme string) [5]string {
	if scheme == config.PlayerKeysYouTube {
		return [5]string{"⇧P", "K", "K", "", "⇧N"}
	}
	return [5]string{"Z", "X", "C", "V", "B"}
}

// playbackBar is the row with the clocks at its two ends and a bar between
// them that fills as the track plays: "02:15 ━━━━━━●────── 04:31". A track
// whose length is not known leaves the bar empty.
func playbackBar(pos, dur time.Duration, loaded bool, w int) string {
	left, right := playbackClocks(pos, dur, loaded)
	_, barW := playbackBarSpan(left, right, w)
	if barW < 3 {
		return left + " / " + right
	}
	if !loaded || dur <= 0 {
		return left + " " + strings.Repeat("─", barW) + " " + right
	}
	knob := int(float64(barW-1)*min(1, max(0, float64(pos)/float64(dur))) + 0.5)
	return left + " " + strings.Repeat("━", knob) + "●" + strings.Repeat("─", barW-knob-1) + " " + right
}

func eighthBlock(level int) string {
	blocks := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	level = max(0, min(8, level))
	return blocks[level]
}

// marqueeSlice returns the w-wide window of s starting at rune offset,
// wrapping around so the text runs continuously.
func marqueeSlice(s string, w, offset int) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	offset %= len(r)
	var b strings.Builder
	width := 0
	for i := 0; width < w && i < len(r); i++ {
		ch := r[(offset+i)%len(r)]
		width += runewidth.RuneWidth(ch)
		b.WriteRune(ch)
	}
	return b.String()
}
