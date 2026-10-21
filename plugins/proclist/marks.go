//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Selecting processes (f4#2130) uses the file panels' own gestures, so F8,
// Shift+F1/F2 and Ctrl+F8 (actions.go) can act on several processes at once:
//
//   - Ins toggles the process under the cursor and moves down.
//   - Shift with Up/Down/PgUp/PgDn/Home/End selects or deselects on the way,
//     in far's run form: the first key of a run decides from the row under
//     the cursor whether the run selects or deselects, and every following
//     key of the run does the same. Up/Down take the row they leave,
//     PgUp/PgDn/Home/End the whole stretch they cross
//     (FileSystemPanel.processKey in internal/panel/list.go is the model).
//   - The right mouse button toggles a row, and dragging with it carries
//     the state the first row got over every row the pointer passes.
//   - The group keys are the file panels' own actions, looked up in the
//     user's keymap rather than spelled out here, so every alias and
//     rebinding of them works too: select or deselect by a name mask (Gray
//     +/-), every process named like the one under the cursor (Ctrl+Gray
//     +/-: a process has no extension, its name is the nearest thing), invert
//     (Gray *) and restore the previous selection (Ctrl+M). Those actions
//     stand down while a panel plugin has the keyboard (vfs.PanelKeyProvider),
//     which is how the key reaches ProcessKey instead of the hidden file panel.
//   - Shift+Gray +/- select and deselect everything, as they do in far.
//
// The group keys work on the rows the table shows: all of them, or only the
// QuickSearch matches while a search is typed, the way the file panels'
// group selection leaves rows the filter hides alone. A selection belongs
// to a process (procKey), not to a row or a bare pid: it stays through
// refreshes and re-sorting, and goes away when the process exits.

// markSet is the set of selected processes. Every procRow carries the
// panel's own set, so the panel changes it in place and never replaces it.
type markSet map[procKey]struct{}

func (m markSet) has(k procKey) bool {
	_, ok := m[k]
	return ok
}

func (m markSet) set(k procKey, on bool) {
	if on {
		m[k] = struct{}{}
	} else {
		delete(m, k)
	}
}

func (m markSet) clone() markSet {
	c := make(markSet, len(m))
	for k := range m {
		c[k] = struct{}{}
	}
	return c
}

// prune drops every process listed does not have: one that exited, or a pid
// the OS has since given to another process.
func (m markSet) prune(listed map[procKey]bool) {
	for k := range m {
		if !listed[k] {
			delete(m, k)
		}
	}
}

// shellArea is the keymap area the file panels' actions are bound in, the
// one internal/app's key router resolves them in while the panels are up.
const shellArea = "Shell"

// panelMarkActions are the file panels' selection actions this panel answers
// in their stead, by lowercase name.
var panelMarkActions = map[string]func(*procListPanel){
	"panel.selectgroup":              func(p *procListPanel) { p.askMarkMask(true) },
	"panel.deselectgroup":            func(p *procListPanel) { p.askMarkMask(false) },
	"panel.selectcurrentextension":   func(p *procListPanel) { p.markSameName(true) },
	"panel.deselectcurrentextension": func(p *procListPanel) { p.markSameName(false) },
	"panel.invertselection":          (*procListPanel).invertMarks,
	"panel.restoreselection":         (*procListPanel).restoreMarks,
}

// boundPanelMarkAction returns the selection action e is bound to in the
// file panels, or nil. Without a keymap (a host that never loaded one) no key
// is bound to anything.
func boundPanelMarkAction(e *vtinput.InputEvent) func(*procListPanel) {
	hm := keymap.GlobalHotkeysMgr
	if hm == nil {
		return nil
	}
	name := keymap.ConfiguredHotkeyAction(hm, shellArea, keymap.EventToHotkeyString(e))
	return panelMarkActions[strings.ToLower(name)]
}

// isShiftMarkKey reports a Shift+navigation key, the keys a selecting run
// is made of. Left/Right are not among them: with no stripes to move
// between, the table gives them to the QuickSearch line.
func isShiftMarkKey(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	mods := e.ControlKeyState
	if mods&vtinput.ShiftPressed == 0 || mods&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0 {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
		return true
	}
	return false
}

// processMarkKey handles the selection keys. The keymap goes first, so a key
// the user bound to one of the panels' selection actions means that action
// here too, whatever else it would have meant.
func (p *procListPanel) processMarkKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	if run := boundPanelMarkAction(e); run != nil {
		run(p)
		return true
	}
	mods := e.ControlKeyState
	ctrlAlt := mods&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := mods&vtinput.ShiftPressed != 0
	switch {
	case e.VirtualKeyCode == vtinput.VK_INSERT && !ctrlAlt && !shift:
		p.toggleMarkAndAdvance()
		return true
	case isShiftMarkKey(e):
		p.shiftMarkStep(e)
		return true
	case (e.VirtualKeyCode == vtinput.VK_ADD || e.VirtualKeyCode == vtinput.VK_SUBTRACT) && shift && !ctrlAlt:
		p.markAll(e.VirtualKeyCode == vtinput.VK_ADD)
		return true
	}
	return false
}

// toggleMarkAndAdvance is Ins.
func (p *procListPanel) toggleMarkAndAdvance() {
	s, ok := p.selectedSample()
	if !ok {
		return
	}
	p.marks.set(s.key(), !p.marks.has(s.key()))
	p.table.MoveSelection(1)
}

// shiftMarkStep is one key of a Shift+navigation run. The run's direction
// is decided by its first key alone; ProcessKey and every declared key end
// the run (shiftSession), so the next one decides afresh.
func (p *procListPanel) shiftMarkStep(e *vtinput.InputEvent) {
	start := p.table.SelectPos
	s, ok := p.selectedSample()
	if !ok {
		return
	}
	if !p.shiftSession {
		p.shiftSession = true
		p.shiftMark = !p.marks.has(s.key())
	}
	p.marks.set(s.key(), p.shiftMark)
	if !p.table.ProcessKey(e) {
		return
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_PRIOR, vtinput.VK_NEXT, vtinput.VK_HOME, vtinput.VK_END:
		p.markRange(start, p.table.SelectPos, p.shiftMark)
	}
}

// markRange sets every shown row from position a to position b, both
// included, in either order.
func (p *procListPanel) markRange(a, b int, on bool) {
	if a > b {
		a, b = b, a
	}
	for pos := a; pos <= b; pos++ {
		if s, ok := p.sampleAt(pos); ok {
			p.marks.set(s.key(), on)
		}
	}
}

// processMarkMouse is the right mouse button: a press toggles the row under
// the pointer and moves the cursor there, and moving with the button held
// gives every row passed the state the first one got. It claims nothing
// else, so the left button, the wheel and header clicks reach the table.
func (p *procListPanel) processMarkMouse(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.MouseEventType {
		return false
	}
	moving := e.MouseEventFlags&vtinput.MouseMoved != 0
	right := e.ButtonState&vtinput.RightmostButtonPressed != 0
	if p.rightDrag {
		if !right || (!moving && !e.KeyDown) {
			// The release ends the drag and is ours. A move with the button
			// already up means the release never arrived: end the drag too,
			// and let the table have the move.
			p.rightDrag = false
			return !moving
		}
		if pos := p.table.GetClickIndex(int(e.MouseY)); pos >= 0 {
			p.markRange(p.rightDragPos, pos, p.rightMark)
			p.rightDragPos = pos
			p.table.SetSelectPos(pos)
		}
		// Off the rows (over the header, below the last process) the drag
		// stays captured and resumes when the pointer comes back.
		return true
	}
	if !right || moving || !e.KeyDown || !p.table.HitTest(int(e.MouseX), int(e.MouseY)) {
		return false
	}
	pos := p.table.GetClickIndex(int(e.MouseY))
	s, ok := p.sampleAt(pos)
	if !ok {
		return false
	}
	p.rightDrag = true
	p.rightDragPos = pos
	p.rightMark = !p.marks.has(s.key())
	p.marks.set(s.key(), p.rightMark)
	p.table.SetSelectPos(pos)
	return true
}

// saveMarks remembers the selection before a group change, for Ctrl+M.
func (p *procListPanel) saveMarks() { p.prevMarks = p.marks.clone() }

// askMarkMask is Gray +/-: the file panels' own mask dialog, with the same
// shared mask history, matched against process names case-insensitively in
// f4's mask syntax (vfs.MatchFileMask: "chrome*,firefox*", "*|*helper*",
// "/regexp/").
func (p *procListPanel) askMarkMask(on bool) {
	title := "Select.Title"
	if !on {
		title = "Deselect.Title"
	}
	var edit *vtui.Edit
	dlg := vtui.InputBox(i18n.Msg(title), i18n.Msg("Select.Mask"), "*", func(mask string) {
		history.CommitHistory(edit, mask)
		p.markByMask(mask, on)
	})
	edit = history.AttachHistory(history.InputBoxEdit(dlg), history.FileMasksHistoryID)
}

func (p *procListPanel) markByMask(mask string, on bool) {
	if strings.TrimSpace(mask) == "" {
		return
	}
	p.saveMarks()
	p.eachShown(func(_ int, s sample) {
		if vfs.MatchFileMask(s.name, mask, true) {
			p.marks.set(s.key(), on)
		}
	})
	p.redraw()
}

// markSameName is Ctrl+Gray +/-: every shown process named like the one
// under the cursor, all twenty helpers of a browser in one key.
func (p *procListPanel) markSameName(on bool) {
	current, ok := p.selectedSample()
	if !ok {
		return
	}
	p.saveMarks()
	p.eachShown(func(_ int, s sample) {
		if strings.EqualFold(s.name, current.name) {
			p.marks.set(s.key(), on)
		}
	})
	p.redraw()
}

// markAll is Shift+Gray +/-.
func (p *procListPanel) markAll(on bool) {
	p.saveMarks()
	p.eachShown(func(_ int, s sample) { p.marks.set(s.key(), on) })
	p.redraw()
}

// invertMarks is Gray *.
func (p *procListPanel) invertMarks() {
	p.saveMarks()
	p.eachShown(func(_ int, s sample) { p.marks.set(s.key(), !p.marks.has(s.key())) })
	p.redraw()
}

// restoreMarks is Ctrl+M: it swaps the selection with the one saved before
// the last group change, as far2l's FileList::RestoreSelection does, so a
// second Ctrl+M undoes the first. Processes that exited meanwhile stay gone.
func (p *procListPanel) restoreMarks() {
	listed := p.listedKeys()
	current := p.marks.clone()
	clear(p.marks)
	for k := range p.prevMarks {
		if listed[k] {
			p.marks[k] = struct{}{}
		}
	}
	p.prevMarks = current
	p.redraw()
}

// listedKeys is every process in the current snapshot, shown or not.
func (p *procListPanel) listedKeys() map[procKey]bool {
	listed := make(map[procKey]bool, len(p.table.Rows))
	for _, r := range p.table.Rows {
		if pr, ok := r.(procRow); ok {
			listed[pr.s.key()] = true
		}
	}
	return listed
}

// selectionSummary is the line far puts on a file panel's bottom border
// while files are selected, here for processes: how many are selected and
// how much memory they hold, or only how many when that is all width fits.
func (p *procListPanel) selectionSummary(width int) string {
	count := 0
	var kib uint64
	for _, r := range p.table.Rows {
		if pr, ok := r.(procRow); ok && p.marks.has(pr.s.key()) {
			count++
			kib += pr.s.rssKiB
		}
	}
	if count == 0 {
		return ""
	}
	for _, text := range []string{
		" " + fmt.Sprintf(i18n.Msg("ProcList.SelectedInfo"), count, formatRSSKiB(kib)) + " ",
		" " + fmt.Sprintf(i18n.Msg("ProcList.SelectedInfoShort"), count) + " ",
	} {
		if vtui.StringWidth(text) < width {
			return text
		}
	}
	return ""
}

// drawSelectionSummary centres selectionSummary on the bottom border, in
// the colour the file panels give the same line.
func (p *procListPanel) drawSelectionSummary(scr *vtui.ScreenBuf) {
	x1, _, x2, y2 := p.frame.GetPosition()
	width := x2 - x1 - 1
	text := p.selectionSummary(width)
	if text == "" {
		return
	}
	x := x1 + 1 + (width-vtui.StringWidth(text))/2
	vtui.NewPainter(scr).DrawString(x, y2, text, vtui.Palette[theme.ColPanelSelectedInfo])
}
