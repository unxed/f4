package panel

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Bookmarks dialog: ten fixed rows, one per slot, reachable from
// F9 → Commands → Bookmarks. Same layout and keys as far2l's
// BookmarksMenu (bookmarks/BookmarksMenu.cpp) — the port is of the UX,
// not of the code.

// bookmarksFrame wraps a vtui.VMenu so the dialog can draw a hotkey hint
// on its bottom border without extending vtui itself, exactly the way
// userMenuFrame does it for the F2 menu.
type bookmarksFrame struct {
	*vtui.VMenu
	bottomHint string
	onClose    func()
}

// IsDone doubles as the close hook. vtui has no OnClose on VMenu and Esc
// closes through the embedded menu's own SetExitCode, which a wrapper
// cannot intercept — but the frame manager drops a frame the moment
// IsDone reports true, so that is the point where "after the dialog goes
// away" work can be queued. Fires once.
func (b *bookmarksFrame) IsDone() bool {
	done := b.VMenu.IsDone()
	if done && b.onClose != nil {
		cb := b.onClose
		b.onClose = nil
		vtui.FrameManager.PostTask(cb)
	}
	return done
}

func (b *bookmarksFrame) Show(scr *vtui.ScreenBuf) {
	b.VMenu.Show(scr)
	if b.bottomHint == "" {
		return
	}
	x1, _, x2, y2 := b.GetPosition()
	vtui.NewPainter(scr).DrawTitle(x1, y2, x2, b.bottomHint, vtui.Palette[vtui.ColMenuTitle])
}

// bookmarksDialog owns the table as loaded from disk plus the menu that
// displays it. Every mutation writes straight back to file, so there is
// no "apply" step and nothing to undo on Esc.
type BookmarksDialog struct {
	Pf   *PanelsFrame
	File string
	Set  BookmarkSet
	menu *vtui.VMenu
}

// ShowBookmarksDialog is the entry point wired to appcmd.CmBookmarks.
func ShowBookmarksDialog(pf *PanelsFrame) {
	ShowBookmarksDialogAt(pf, 0, nil)
}

// ShowBookmarksDialogAt opens the dialog with the cursor on a given slot
// and runs onClose (may be nil) once the dialog is gone. The drive menu
// uses both: far2l's F4 there opens this dialog on the slot under the
// cursor and returns to the menu afterwards.
func ShowBookmarksDialogAt(pf *PanelsFrame, slot int, onClose func()) {
	d, err := NewBookmarksDialog(pf, BookmarksFilePath())
	if err != nil {
		vtui.ShowMessage(i18n.Msg("Bookmarks.Title"),
			fmt.Sprintf(i18n.Msg("Bookmarks.LoadError"), err),
			[]string{"&Ok"})
		if onClose != nil {
			onClose()
		}
		return
	}
	d.open(slot, onClose)
}

// newBookmarksDialog reads the table from path. The error is returned
// rather than displayed so the caller decides how to surface it — and so
// this half can be exercised without a live UI.
func NewBookmarksDialog(pf *PanelsFrame, path string) (*BookmarksDialog, error) {
	set, err := LoadBookmarks(path)
	if err != nil {
		return nil, err
	}
	return &BookmarksDialog{Pf: pf, File: path, Set: set}, nil
}

// open builds the menu and pushes it as a modal frame, cursor on slot.
func (d *BookmarksDialog) open(slot int, onClose func()) {
	// Empty rows carry appcmd.CmBookmarkEmptySlot, which is permanently disabled:
	// vtui then draws them dimmed and swallows Enter on them, which is
	// exactly the "empty slot is a no-op" behavior far2l has. No other
	// menu uses this command, so it never needs re-enabling.
	vtui.FrameManager.DisabledCommands.Disable(appcmd.CmBookmarkEmptySlot)

	d.menu = vtui.NewVMenu(i18n.Msg("Bookmarks.Title"))
	d.render()
	d.menu.SetSelectPos(slot)

	w, h := d.size()
	x := (d.Pf.LastW - w) / 2
	y := (d.Pf.LastH - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	d.menu.SetPosition(x, y, x+w-1, y+h-1)

	d.menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		if !e.KeyDown {
			return false
		}
		shift := e.ControlKeyState&vtinput.ShiftPressed != 0
		ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0

		slot := d.SlotAt(d.menu.SelectPos)

		// Ins stores the panel's directory, Del clears the slot, F4 edits
		// the path by hand. All three overwrite without asking, as far2l
		// does, and each one hits the disk immediately.
		if !ctrl && !shift && !alt {
			switch e.VirtualKeyCode {
			case vtinput.VK_INSERT:
				d.saveCurrentDir(slot)
				return true
			case vtinput.VK_DELETE:
				d.clearSlot(slot)
				return true
			case vtinput.VK_F4:
				d.editPath(slot)
				return true
			}
		}

		// Shift+Up / Shift+Down swap the slot with its neighbour and take
		// the cursor along, so it stays on the same bookmark.
		if shift && !ctrl && !alt {
			switch e.VirtualKeyCode {
			case vtinput.VK_UP:
				d.moveSlot(slot, -1)
				return true
			case vtinput.VK_DOWN:
				d.moveSlot(slot, +1)
				return true
			}
		}

		// Keys the top frame declines fall through to vtui's own global
		// handlers — F1 opens help, F9 activates the menu bar (which a
		// TypeMenu frame does not block), F12 opens the screen list.
		// None of that belongs on top of a modal dialog, so the whole
		// F-key range is swallowed, as the F2 user menu does. F10 is
		// left to vtui, which closes the menu with it.
		if !ctrl && !shift && !alt &&
			e.VirtualKeyCode >= vtinput.VK_F1 && e.VirtualKeyCode <= vtinput.VK_F12 &&
			e.VirtualKeyCode != vtinput.VK_F10 {
			return true
		}
		return false
	}

	// Enter (and a mouse click) on a populated row: vtui pops the menu on
	// its own once we return, so the navigation is posted for after the
	// frame stack has settled. Empty rows never get here — their command
	// is disabled, so vtui swallows the key first.
	d.menu.OnAction = func(uiIdx int) {
		slot := d.SlotAt(uiIdx)
		if slot < 0 || d.Set[slot].IsEmpty() {
			return
		}
		bookmark := d.Set[slot]
		pf := d.Pf
		vtui.FrameManager.PostTask(func() {
			if fsp := pf.GetActivePanel(); fsp != nil {
				pf.NavigateToBookmark(fsp, bookmark)
			}
		})
	}

	vtui.FrameManager.Push(&bookmarksFrame{
		VMenu:      d.menu,
		bottomHint: i18n.Msg("Bookmarks.BottomHint"),
		onClose:    onClose,
	})
}

// render rebuilds all ten rows in place, keeping the cursor where it was.
// The frame may already be on screen: vtui picks the new items up on the
// next redraw.
func (d *BookmarksDialog) render() {
	pos := d.menu.SelectPos
	d.menu.Items = nil
	d.menu.ItemCount = 0
	for i := range d.Set {
		item := vtui.MenuItem{Text: d.RowText(i), UserData: i}
		if d.Set[i].IsEmpty() {
			item.Command = appcmd.CmBookmarkEmptySlot
		}
		d.menu.AddItem(item)
	}
	d.menu.SetSelectPos(pos)
}

// rowText formats one row: the hotkey reminder far2l prints in its own
// dialog title, the slot digit, then the path (or the empty marker).
func (d *BookmarksDialog) RowText(slot int) string {
	path := d.Set[slot].Path
	if path == "" {
		path = i18n.Msg("Bookmarks.EmptySlot")
	}
	return fmt.Sprintf("%s %d   %s", i18n.Msg("Bookmarks.RowPrefix"), slot, dialog.EscapeAmpersand(path))
}

// size returns the menu box dimensions: wide enough for the longest row
// and for the bottom hint, tall enough for all ten slots, clamped to the
// console — same shape of arithmetic as menuSize in user_menu_ui.go.
func (d *BookmarksDialog) size() (int, int) {
	w := 60
	for i := range d.Set {
		if rw := runewidth.StringWidth(d.RowText(i)) + 4; rw > w {
			w = rw
		}
	}
	if minForHint := runewidth.StringWidth(i18n.Msg("Bookmarks.BottomHint")) + 2; w < minForHint {
		w = minForHint
	}
	if d.Pf.LastW > 0 && w > d.Pf.LastW-4 {
		w = d.Pf.LastW - 4
	}
	if w < 24 {
		w = 24
	}

	h := len(d.Set) + 2
	maxH := d.Pf.LastH - 6
	if maxH < 5 {
		maxH = 5
	}
	if h > maxH {
		h = maxH
	}
	return w, h
}

// saveCurrentDir records the active panel's directory in the slot.
func (d *BookmarksDialog) saveCurrentDir(slot int) {
	fsp := d.Pf.GetActivePanel()
	if fsp == nil || slot < 0 {
		return
	}
	d.Set.SetCurrentDir(slot, fsp.Vfs.GetPath())
	d.persist()
}

// clearSlot empties the slot. No confirmation, matching far2l.
func (d *BookmarksDialog) clearSlot(slot int) {
	if slot < 0 {
		return
	}
	d.Set.DeleteAtSlot(slot)
	d.persist()
}

// editPath asks for a path and stores it in the slot. Empty input counts
// as a cancel rather than "clear the slot" — that is what Del is for.
func (d *BookmarksDialog) editPath(slot int) {
	if slot < 0 || slot >= len(d.Set) {
		return
	}
	current := d.Set[slot].Path
	vtui.InputBox(i18n.Msg("Bookmarks.EditTitle"), i18n.Msg("Bookmarks.EditPrompt"), current, func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		d.Set.SetCurrentDir(slot, text)
		d.persist()
	})
}

// moveSlot swaps the slot with the neighbour delta rows away and moves
// the cursor with it. A move off either end is a no-op.
func (d *BookmarksDialog) moveSlot(slot, delta int) {
	target := slot + delta
	if slot < 0 || target < 0 || target >= len(d.Set) {
		return
	}
	d.Set.SwapSlots(slot, target)
	if d.persist() {
		d.menu.SetSelectPos(target)
	}
}

// persist writes the table back and re-renders. On a write failure the
// on-disk state wins: the in-memory copy is reloaded so the dialog never
// shows changes that were not saved.
func (d *BookmarksDialog) persist() bool {
	err := SaveBookmarks(d.File, d.Set)
	if err != nil {
		vtui.ShowMessage(i18n.Msg("Bookmarks.Title"),
			fmt.Sprintf(i18n.Msg("Bookmarks.SaveError"), err),
			[]string{"&Ok"})
		if reloaded, lerr := LoadBookmarks(d.File); lerr == nil {
			d.Set = reloaded
		}
	}
	d.render()
	vtui.FrameManager.Redraw()
	return err == nil
}

// deleteAtSlot clears the slot, leaving the rest of the table alone.
func (s *BookmarkSet) DeleteAtSlot(i int) {
	if i < 0 || i >= len(s) {
		return
	}
	s[i] = Bookmark{}
}

// swapSlots exchanges two slots. Out-of-range indices are ignored so
// callers can pass "cursor ± 1" without bounds-checking first.
func (s *BookmarkSet) SwapSlots(a, b int) {
	if a < 0 || a >= len(s) || b < 0 || b >= len(s) {
		return
	}
	s[a], s[b] = s[b], s[a]
}

// setCurrentDir stores path in the slot and clears the plugin fields:
// whoever edits a slot from the dialog is recording a filesystem
// directory, not a plugin location.
func (s *BookmarkSet) SetCurrentDir(i int, path string) {
	if i < 0 || i >= len(s) {
		return
	}
	s[i] = Bookmark{Path: path}
}

// slotAt maps a menu row to its slot index, or -1 when the row is out of
// range. Rows and slots are 1:1 today; going through UserData keeps that
// an implementation detail.
func (d *BookmarksDialog) SlotAt(uiPos int) int {
	if d.menu == nil || uiPos < 0 || uiPos >= len(d.menu.Items) {
		return -1
	}
	slot, ok := d.menu.Items[uiPos].UserData.(int)
	if !ok || slot < 0 || slot >= len(d.Set) {
		return -1
	}
	return slot
}
