package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
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
// Up/Down walk one column that starts at the control row and continues into
// the playlist; Left/Right pick a button while the control row has the
// cursor, and collapse/expand a folder while the playlist has it. Enter and
// Space act on whatever has the cursor. WinAmp's Z X C V B keys and +/- for
// volume work anywhere in the panel.
type PlayerPanel struct {
	vtui.ScreenObject
	src     *FileSystemPanel
	frame   *vtui.BorderedFrame
	focused bool

	engine *audioEngine
	root   *playlistItem // invisible root; children are the top level
	rows   []playlistRow // flattened view, rebuilt on every Show
	cursor int           // index into rows; -1 = control row has the cursor
	button int           // focused control when cursor == -1
	top    int           // first visible playlist row

	current *playlistItem // the track loaded in the engine, if any
	marquee int

	// queue is the file panel's audio files when the current track came
	// from Enter in the panel rather than the playlist; queuePos is the
	// current one. Nil while the playlist drives playback.
	queue    []string
	queuePos int

	ffmpegWarned bool // the install message is shown once per panel
	//lastTick time.Time // fix linter error
	status string // one-line error/notice shown instead of the title

	stop chan struct{}
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

const playerControlRows = 5 // title, time/info, spectrum×2, buttons

func NewPlayerPanel(src *FileSystemPanel) *PlayerPanel {
	x1, y1, x2, y2 := src.GetPosition()
	pp := &PlayerPanel{
		src:    src,
		engine: newAudioEngine(),
		root:   &playlistItem{Folder: true, Expanded: true},
		cursor: -1,
		button: playerBtnPlay,
		stop:   make(chan struct{}),
	}
	pp.SetVisible(true)
	pp.frame = vtui.NewBorderedFrame(x1, y1, x2, y2, vtui.SingleBox, Msg("Player.Title"))
	pp.frame.ColorBoxIdx = ColPanelBox
	pp.frame.ColorTitleIdx = ColPanelTitle
	pp.frame.ColorBackgroundIdx = ColPanelText
	pp.SetPosition(x1, y1, x2, y2)
	pp.loadPlaylist()
	go pp.tick()
	return pp
}

func (pp *PlayerPanel) SetPosition(x1, y1, x2, y2 int) {
	pp.ScreenObject.SetPosition(x1, y1, x2, y2)
	if pp.frame != nil {
		pp.frame.SetPosition(x1, y1, x2, y2)
	}
}

func (pp *PlayerPanel) Source() *FileSystemPanel { return pp.src }
func (pp *PlayerPanel) Kind() string             { return "player" }
func (pp *PlayerPanel) SetFocus(f bool)          { pp.focused = f }
func (pp *PlayerPanel) IsFocused() bool          { return pp.focused }
func (pp *PlayerPanel) ProcessMouse(*vtinput.InputEvent) bool {
	return false
}

func (pp *PlayerPanel) GetSelectedName() string {
	if pp.cursor >= 0 && pp.cursor < len(pp.rows) {
		return pp.rows[pp.cursor].item.Name
	}
	return ""
}

// Close stops playback and the repaint ticker. Playback is tied to the
// panel on purpose: with no panel there is nothing to control it from.
func (pp *PlayerPanel) Close() {
	select {
	case <-pp.stop:
	default:
		close(pp.stop)
	}
	pp.engine.Close()
}

// tick repaints while something is playing so the clock, the marquee and
// the spectrum move, and advances to the next track when one ends. It
// never touches UI state itself; everything happens inside PostTask.
func (pp *PlayerPanel) tick() {
	t := time.NewTicker(150 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-pp.stop:
			return
		case <-t.C:
			if !pp.engine.IsLoaded() {
				continue
			}
			vtui.FrameManager.PostTask(func() {
				if pp.engine.Finished() {
					if !pp.playRelative(+1) {
						pp.engine.Stop()
						pp.current = nil
					}
				}
				pp.marquee++
				vtui.FrameManager.Redraw()
			})
		}
	}
}

// ---- playlist model ---------------------------------------------------

func playlistFile() string {
	return filepath.Join(GetF4ConfigDir(), "playlist.json")
}

func (pp *PlayerPanel) loadPlaylist() {
	data, err := os.ReadFile(playlistFile())
	if err != nil {
		return
	}
	_ = unmarshalPlaylist(data, pp.root)
}

func (pp *PlayerPanel) savePlaylist() {
	data, err := marshalPlaylist(pp.root)
	if err != nil {
		return
	}
	_ = os.WriteFile(playlistFile(), data, 0o600)
}

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
	if !IsAudioFile(path) {
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

// ---- playback ---------------------------------------------------------

func (pp *PlayerPanel) playItem(it *playlistItem) bool {
	if it == nil || it.Folder {
		return false
	}
	// A playlist track takes over from the file panel queue.
	if it.parent != nil {
		pp.queue = nil
	}
	pp.status = ""
	if err := pp.engine.Load(it.Path); err != nil {
		pp.status = err.Error()
		pp.current = nil
		if errors.Is(err, errNeedFFmpeg) {
			pp.status = fmt.Sprintf(Msg("Player.NeedFFmpeg"), filepath.Ext(it.Path))
			if !pp.ffmpegWarned {
				pp.ffmpegWarned = true
				vtui.ShowMessage(Msg("Player.Title"), toolFFmpeg.MissingMessage(), []string{Msg("vtui.Ok")})
			}
		}
		return false
	}
	pp.current = it
	pp.marquee = 0
	pp.engine.Play()
	return true
}

// PlayFile plays one file that is not in the playlist — the one under the
// cursor of the file panel — and remembers its neighbours so that playback
// can go on to the next recording, and |< >| move within the folder. The
// list is the panel's audio files in panel order; pos is the one to play.
func (pp *PlayerPanel) PlayFile(files []string, pos int) bool {
	if pos < 0 || pos >= len(files) {
		return false
	}
	it := &playlistItem{Name: trackDisplayName(files[pos]), Path: files[pos]}
	if !pp.playItem(it) {
		return false
	}
	pp.queue = append([]string(nil), files...)
	pp.queuePos = pos
	return true
}

// StopIfPlaying stops playback when the current track is one of paths.
// The delete action calls it before removing files: an open file cannot be
// deleted on Windows, and a player that carries on reading a file that is
// gone is not what anybody expects on the others. The queue forgets the
// files too so the player does not step onto them later.
func (pp *PlayerPanel) StopIfPlaying(paths []string) {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[filepath.Clean(p)] = true
	}
	if pp.current != nil && set[filepath.Clean(pp.current.Path)] {
		pp.engine.Stop()
		pp.current = nil
	}
	if pp.queue == nil {
		return
	}
	kept := pp.queue[:0]
	pos := pp.queuePos
	for i, p := range pp.queue {
		if set[filepath.Clean(p)] {
			if i < pp.queuePos {
				pos--
			}
			continue
		}
		kept = append(kept, p)
	}
	pp.queue = kept
	pp.queuePos = max(-1, min(pos, len(kept)-1))
}

// playQueueRelative steps through the file panel's files. Files that have
// gone since the list was taken (deleted from the panel meanwhile) are
// skipped.
func (pp *PlayerPanel) playQueueRelative(delta int) bool {
	for i := pp.queuePos + delta; i >= 0 && i < len(pp.queue); i += delta {
		if _, err := os.Stat(pp.queue[i]); err != nil {
			continue
		}
		if pp.PlayFile(pp.queue, i) {
			return true
		}
	}
	return false
}

// playRelative plays the track delta positions away from the current one
// in play order. With nothing current it starts from the ends.
func (pp *PlayerPanel) playRelative(delta int) bool {
	if pp.queue != nil {
		return pp.playQueueRelative(delta)
	}
	list := pp.root.tracks(nil)
	if len(list) == 0 {
		return false
	}
	idx := -1
	for i, t := range list {
		if t == pp.current {
			idx = i
			break
		}
	}
	if idx < 0 {
		if delta > 0 {
			idx = -1
		} else {
			idx = len(list)
		}
	}
	idx += delta
	if idx < 0 || idx >= len(list) {
		return false
	}
	return pp.playItem(list[idx])
}

func (pp *PlayerPanel) pressButton(b int) {
	switch b {
	case playerBtnPrev:
		pp.playRelative(-1)
	case playerBtnPlay:
		switch {
		case pp.engine.IsLoaded() && !pp.engine.IsPlaying():
			pp.engine.Play()
		case pp.engine.IsLoaded():
			// Play on a playing track restarts it, like WinAmp.
			pp.playItem(pp.current)
		case pp.cursor >= 0 && pp.cursor < len(pp.rows) && !pp.rows[pp.cursor].item.Folder:
			pp.playItem(pp.rows[pp.cursor].item)
		default:
			pp.playRelative(+1)
		}
	case playerBtnPause:
		pp.engine.TogglePause()
	case playerBtnStop:
		pp.engine.Stop()
		pp.current = nil
		pp.queue = nil
	case playerBtnNext:
		pp.playRelative(+1)
	case playerBtnVolume:
		// Enter on the volume slider does nothing; +/- and Left/Right adjust it.
	}
}

func (pp *PlayerPanel) adjustVolume(delta float64) {
	pp.engine.SetVolume(pp.engine.Volume() + delta)
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
	case !ctrl && !shift && e.Char != 0 && pp.globalChar(e.Char):
	case pp.cursor < 0:
		handled = pp.controlKey(e, ctrl)
	default:
		handled = pp.playlistKey(e, ctrl)
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

// isFilePanelOnlyKey lists the plain keys that only make sense with a
// file under the cursor, so the player refuses to pass them on.
func isFilePanelOnlyKey(vk uint16) bool {
	switch vk {
	case vtinput.VK_F3, vtinput.VK_F4, vtinput.VK_F5, vtinput.VK_F6,
		vtinput.VK_F7, vtinput.VK_F8, vtinput.VK_INSERT, vtinput.VK_DELETE:
		return true
	}
	return false
}

// globalChar handles the WinAmp letter keys and volume characters that
// work wherever the cursor is.
func (pp *PlayerPanel) globalChar(ch rune) bool {
	switch ch {
	case 'z', 'Z':
		pp.pressButton(playerBtnPrev)
	case 'x', 'X':
		pp.pressButton(playerBtnPlay)
	case 'c', 'C':
		pp.pressButton(playerBtnPause)
	case 'v', 'V':
		pp.pressButton(playerBtnStop)
	case 'b', 'B':
		pp.pressButton(playerBtnNext)
	case '+', '=':
		pp.adjustVolume(+0.05)
	case '-', '_':
		pp.adjustVolume(-0.05)
	default:
		return false
	}
	return true
}

func (pp *PlayerPanel) controlKey(e *vtinput.InputEvent, ctrl bool) bool {
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
		// Top of the column: nothing above, but swallow so the file
		// panel below the alt does not react.
	default:
		return false
	}
	return true
}

func (pp *PlayerPanel) playlistKey(e *vtinput.InputEvent, ctrl bool) bool {
	pp.rebuildRows()
	if pp.cursor >= len(pp.rows) {
		pp.cursor = len(pp.rows) - 1
	}
	if pp.cursor < 0 {
		return pp.controlKey(e, ctrl)
	}
	row := pp.rows[pp.cursor]
	it := row.item
	page := pp.playlistHeight()
	if page < 1 {
		page = 1
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_UP:
		if ctrl {
			pp.moveItem(it, -1)
		} else if pp.cursor == 0 {
			pp.cursor = -1
		} else {
			pp.cursor--
		}
	case vtinput.VK_DOWN:
		if ctrl {
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
			pp.cursor = -1
		} else {
			pp.cursor = 0
		}
	case vtinput.VK_END:
		pp.cursor = len(pp.rows) - 1
	case vtinput.VK_LEFT:
		if ctrl {
			pp.moveOut(it)
		} else if it.Folder && it.Expanded {
			it.Expanded = false
			pp.savePlaylist()
		} else if it.parent != pp.root {
			pp.cursorTo(it.parent)
		}
	case vtinput.VK_RIGHT:
		if ctrl {
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
		if pp.engine.IsLoaded() {
			pp.engine.TogglePause()
		} else if !it.Folder {
			pp.playItem(it)
		}
	case vtinput.VK_DELETE:
		if it == pp.current || (it.Folder && pp.currentInside(it)) {
			pp.engine.Stop()
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
	vtui.InputBox(Msg("Player.NewFolderTitle"), Msg("Player.NewFolderPrompt"), "", func(name string) {
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
	return pp.Y2 - pp.Y1 - 2 - playerControlRows - 1
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
	text := vtui.Palette[ColPanelText]
	hi := vtui.Palette[ColPanelSelectedText]
	box := vtui.Palette[ColPanelBox]
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
	title := Msg("Player.Idle")
	if pp.status != "" {
		title = pp.status
	} else if pp.current != nil {
		title = "♪ " + pp.current.Name
		if pp.engine.IsLoaded() && !pp.engine.IsPlaying() && !pp.engine.Finished() {
			title += " " + Msg("Player.PausedMark")
		}
	}
	if runewidth.StringWidth(title) > w && pp.engine.IsPlaying() {
		title = marqueeSlice(title+"   ***   ", w, pp.marquee/2)
	}
	put(y, title, text)

	// Row 1: clock and stream facts.
	clock := "--:-- / --:--"
	facts := ""
	if pp.engine.IsLoaded() {
		clock = fmtClock(pp.engine.Position()) + " / " + fmtClock(pp.engine.Duration())
		info := pp.engine.Info()
		mode := Msg("Player.Stereo")
		if info.Mono {
			mode = Msg("Player.Mono")
		}
		facts = fmt.Sprintf("%dkbps %dkHz %s", info.BitrateKbps, info.SampleRate/1000, mode)
		if info.Codec != "" {
			facts = info.Codec + " " + facts
		}
	}
	line := clock
	if gap := w - runewidth.StringWidth(clock) - runewidth.StringWidth(facts); gap > 1 {
		line += strings.Repeat(" ", gap) + facts
	}
	put(y+1, line, text)

	// Rows 2-3: spectrum, two rows of eighth blocks = 16 levels per band.
	bands := min(24, max(4, (w-2)/2))
	spec := pp.engine.Spectrum(bands)
	if !pp.engine.IsPlaying() {
		for i := range spec {
			spec[i] = 0
		}
	}
	var hiRow, loRow strings.Builder
	for _, v := range spec {
		lvl := int(v*16 + 0.5)
		hiRow.WriteString(eighthBlock(lvl-8) + " ")
		loRow.WriteString(eighthBlock(lvl) + " ")
	}
	put(y+2, hiRow.String(), text)
	put(y+3, loRow.String(), text)

	// Row 4: buttons and volume.
	labels := []string{"|<", " > ", "||", " # ", ">|"}
	bx := x
	scr.FillRect(x, y+4, pp.X2-1, y+4, ' ', text)
	for i, l := range labels {
		attr := text
		if pp.focused && pp.cursor < 0 && pp.button == i {
			attr = hi
		}
		cells := vtui.StringToCharInfo("["+l+"]", attr)
		scr.Write(bx, y+4, cells)
		bx += len(cells) + 1
	}
	volW := w - (bx - x) - 5
	if volW >= 4 {
		filled := int(pp.engine.Volume()*float64(volW) + 0.5)
		bar := strings.Repeat("█", filled) + strings.Repeat("░", volW-filled)
		attr := text
		if pp.focused && pp.cursor < 0 && pp.button == playerBtnVolume {
			attr = hi
		}
		scr.Write(bx, y+4, vtui.StringToCharInfo(bar, attr))
		scr.Write(bx+volW, y+4, vtui.StringToCharInfo(fmt.Sprintf("%3d%%", int(pp.engine.Volume()*100+0.5)), text))
	}

	// Separator on the frame's own lines.
	sep := "├" + strings.Repeat("─", w) + "┤"
	scr.Write(pp.X1, y+playerControlRows, vtui.StringToCharInfo(sep, box))

	// Playlist.
	pp.rebuildRows()
	ph := pp.playlistHeight()
	py := y + playerControlRows + 1
	if ph <= 0 {
		return
	}
	if pp.cursor >= 0 {
		if pp.cursor < pp.top {
			pp.top = pp.cursor
		}
		if pp.cursor >= pp.top+ph {
			pp.top = pp.cursor - ph + 1
		}
	}
	if pp.top > max(0, len(pp.rows)-ph) {
		pp.top = max(0, len(pp.rows)-ph)
	}
	if len(pp.rows) == 0 {
		put(py, Msg("Player.EmptyPlaylist"), text)
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
