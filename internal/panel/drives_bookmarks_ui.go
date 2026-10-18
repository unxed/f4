package panel

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// DriveMenuFrame adds the Far-style operation hint to a drive menu while
// keeping the embedded VMenu available to the menu's existing callers.
type DriveMenuFrame struct {
	*vtui.VMenu
	bottomHint string
}

func (f *DriveMenuFrame) Show(scr *vtui.ScreenBuf) {
	f.VMenu.Show(scr)
	if f.bottomHint == "" {
		return
	}
	x1, _, x2, y2 := f.GetPosition()
	vtui.NewPainter(scr).DrawTitle(x1, y2, x2, f.bottomHint, vtui.Palette[vtui.ColMenuTitle])
}

func (f *DriveMenuFrame) ProcessKey(e *vtinput.InputEvent) bool {
	handled := f.VMenu.ProcessKey(e)
	// VMenu checks whether it is the exact top frame before reporting that
	// Esc/F10 was consumed. The wrapper is the top frame in this menu, so
	// finish that part of the contract here.
	if e != nil && e.KeyDown && f.IsDone() &&
		(e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10) {
		return true
	}
	return handled
}

// driveLinkHotkeyEdit keeps the caret on the captured character, rather than
// scrolling it out of a one-cell field to display the insertion point after it.
// Existing multi-character bindings remain intact until explicitly replaced.
type driveLinkHotkeyEdit struct{ *vtui.Edit }

func (e *driveLinkHotkeyEdit) Show(scr *vtui.ScreenBuf) {
	e.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_HOME})
	e.Edit.Show(scr)
}

type driveBookmarkEditDialog struct {
	*vtui.Window
	nameEdit   *vtui.Edit
	pathEdit   *vtui.Edit
	HotkeyEdit *driveLinkHotkeyEdit
	quickCall  *vtui.Checkbox
	finished   bool
	onFinish   func(bool, DriveBookmark)
}

// driveBookmarkDialogMinWidth is the floor for the dialog's computed width:
// half the program window would otherwise squeeze the fields on a narrow
// terminal.
const driveBookmarkDialogMinWidth = 64

// driveBookmarkDialogWidth returns the add/edit link dialog's width for the
// given screen width: half the program window, the same proportion the file
// dialogs use (see FileDialogWidth, #891), clamped to a sane minimum and to
// the screen itself. The dialog's fields are laid out once at construction
// time and never reflow afterwards, so f4#1148 asked to size it from the
// screen instead of letting the user stretch it and leave the fields behind.
func driveBookmarkDialogWidth(screenWidth int) int {
	if screenWidth <= 0 {
		return driveBookmarkDialogMinWidth
	}
	w := screenWidth / 2
	if w < driveBookmarkDialogMinWidth {
		w = driveBookmarkDialogMinWidth
	}
	if w > screenWidth {
		w = screenWidth
	}
	return w
}

func NewDriveBookmarkEditDialog(initial DriveBookmark, defaultPath string, onFinish func(bool, DriveBookmark)) *driveBookmarkEditDialog {
	// Six rows of fields, the rule and the buttons; the only blank row is the
	// one between the path and the hot key, none under the top border or
	// above the bottom one (mitek1977-coder, f4#1148).
	const height = 11
	screenW := 0
	if vtui.FrameManager != nil {
		screenW = vtui.FrameManager.GetScreenSize()
	}
	width := driveBookmarkDialogWidth(screenW)
	titleKey := "DriveLink.CreateTitle"
	if initial.Name != "" || initial.Path != "" {
		titleKey = "DriveLink.EditTitle"
	}
	dialog := &driveBookmarkEditDialog{
		Window:     vtui.NewCenteredDialog(width, height, i18n.Msg(titleKey)),
		nameEdit:   vtui.NewEdit(0, 0, width-6, initial.Name),
		pathEdit:   vtui.NewEdit(0, 0, width-6, initial.Path),
		HotkeyEdit: &driveLinkHotkeyEdit{vtui.NewEdit(0, 0, max(1, vtui.StringWidth(initial.Hotkey)), initial.Hotkey)},
		quickCall:  vtui.NewCheckbox(0, 0, i18n.Msg("DriveLink.QuickCall"), initial.QuickCall),
		onFinish:   onFinish,
	}
	if dialog.pathEdit.GetText() == "" {
		dialog.pathEdit.SetText(defaultPath)
	}

	nameLabel := vtui.NewLabel(0, 0, i18n.Msg("DriveLink.Name"), dialog.nameEdit)
	pathLabel := vtui.NewLabel(0, 0, i18n.Msg("DriveLink.Path"), dialog.pathEdit)
	hotkeyLabel := vtui.NewLabel(0, 0, i18n.Msg("DriveLink.Hotkey"), dialog.HotkeyEdit)
	hotkeyHint := vtui.NewText(0, 0, i18n.Msg("DriveLink.HotkeyHint"), vtui.Palette[vtui.ColDialogText])
	createText := i18n.Msg("DriveLink.Create")
	if initial.Name != "" || initial.Path != "" {
		createText = i18n.Msg("DriveLink.Save")
	}
	createButton := vtui.NewButton(0, 0, createText)
	createButton.IsDefault = true
	cancelButton := vtui.NewButton(0, 0, i18n.Msg("DriveLink.Cancel"))
	createButton.OnClick = func() { dialog.submit() }
	cancelButton.OnClick = func() { dialog.finish(false, DriveBookmark{}) }
	separator := vtui.NewSeparator(0, 0, width, true, true)

	dialog.AddItem(nameLabel)
	dialog.AddItem(dialog.nameEdit)
	dialog.AddItem(pathLabel)
	dialog.AddItem(dialog.pathEdit)
	dialog.AddItem(hotkeyLabel)
	dialog.AddItem(dialog.HotkeyEdit)
	dialog.AddItem(dialog.quickCall)
	dialog.AddItem(hotkeyHint)
	dialog.AddItem(separator)
	dialog.AddItem(createButton)
	dialog.AddItem(cancelButton)

	vbox := vtui.NewVBoxLayout(dialog.X1+2, dialog.Y1+1, width-4, height-2)
	vbox.Add(nameLabel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(dialog.nameEdit, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(pathLabel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(dialog.pathEdit, vtui.Margins{}, vtui.AlignFill)
	hotkeyRow := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hotkeyRow.Spacing = 0
	hotkeyRow.Add(hotkeyLabel, vtui.Margins{}, vtui.AlignLeft)
	hotkeyRow.Add(dialog.HotkeyEdit, vtui.Margins{Right: 1}, vtui.AlignLeft)
	hotkeyRow.Add(dialog.quickCall, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(hotkeyRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(hotkeyHint, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(createButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(separator, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
	vbox.Add(buttons, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	dialog.SetFocusedItem(dialog.nameEdit)
	dialog.OnResult = func(code int) {
		if !dialog.finished {
			dialog.finished = true
			if dialog.onFinish != nil {
				dialog.onFinish(false, DriveBookmark{})
			}
		}
	}
	return dialog
}

func (d *driveBookmarkEditDialog) finish(saved bool, bookmark DriveBookmark) {
	if d.finished {
		return
	}
	d.finished = true
	d.SetExitCode(1)
	if d.onFinish != nil {
		d.onFinish(saved, bookmark)
	}
}

func (d *driveBookmarkEditDialog) submit() {
	bookmark := DriveBookmark{
		Name:      strings.TrimSpace(d.nameEdit.GetText()),
		Path:      strings.TrimSpace(d.pathEdit.GetText()),
		Hotkey:    strings.TrimSpace(d.HotkeyEdit.GetText()),
		QuickCall: d.quickCall.State == 1,
	}
	if bookmark.Name == "" {
		vtui.ShowMessageOn(d, i18n.Msg("DriveLink.ErrorTitle"), i18n.Msg("DriveLink.NameRequired"), []string{"&Ok"})
		d.SetFocusedItem(d.nameEdit)
		return
	}
	if bookmark.Path == "" {
		vtui.ShowMessageOn(d, i18n.Msg("DriveLink.ErrorTitle"), i18n.Msg("DriveLink.PathRequired"), []string{"&Ok"})
		d.SetFocusedItem(d.pathEdit)
		return
	}
	d.finish(true, bookmark)
}

// ProcessMouse blocks the corner-drag resize gesture vtui.Window otherwise
// offers on every modal dialog. The dialog's width already tracks half the
// program window (f4#1148); the fields and buttons are laid out once at
// construction and never reflow on resize, so letting the user stretch the
// dialog further just left them behind, off to one side.
func (d *driveBookmarkEditDialog) ProcessMouse(e *vtinput.InputEvent) bool {
	if e != nil && e.ButtonState == vtinput.FromLeft1stButtonPressed && e.KeyDown &&
		int(e.MouseX) == d.X2 && int(e.MouseY) == d.Y2 {
		return true
	}
	return d.Window.ProcessMouse(e)
}

func (d *driveBookmarkEditDialog) ProcessKey(e *vtinput.InputEvent) bool {
	if e != nil && e.KeyDown && d.GetFocusedItem() == d.HotkeyEdit {
		if e.VirtualKeyCode == vtinput.VK_TAB || e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_RETURN {
			return d.Window.ProcessKey(e)
		}
		if e.VirtualKeyCode == vtinput.VK_DELETE {
			d.HotkeyEdit.SetText("")
			return true
		}
		key := keymap.EventToHotkeyString(e)
		if len([]rune(key)) == 1 {
			d.HotkeyEdit.SetText(key)
		}
		return true
	}
	return d.Window.ProcessKey(e)
}

func (pf *PanelsFrame) driveBookmarkDefaultPath(panelIdx int) string {
	if panelIdx < 0 || panelIdx >= len(pf.Panels) {
		return ""
	}
	fsp, ok := pf.Panels[panelIdx].(*FileSystemPanel)
	if !ok || fsp.Vfs == nil {
		return ""
	}
	return fsp.Vfs.GetPath()
}

func (pf *PanelsFrame) openDriveBookmarkEditor(panelIdx int, menu *vtui.VMenu, bookmarks []DriveBookmark, index int, reopen func()) {
	var initial DriveBookmark
	if index >= 0 && index < len(bookmarks) {
		initial = bookmarks[index]
	}
	if menu != nil {
		menu.Close()
	}
	vtui.FrameManager.PostTask(func() {
		dialog := NewDriveBookmarkEditDialog(initial, pf.driveBookmarkDefaultPath(panelIdx), func(saved bool, bookmark DriveBookmark) {
			if !saved {
				vtui.FrameManager.PostTask(reopen)
				return
			}
			current, err := LoadDriveBookmarks(DriveBookmarksFilePath())
			if err == nil {
				if index >= 0 && index < len(current) {
					current[index] = bookmark
				} else {
					current = append(current, bookmark)
				}
				err = SaveDriveBookmarks(DriveBookmarksFilePath(), current)
			}
			if err != nil {
				message := vtui.ShowMessage(i18n.Msg("DriveLink.ErrorTitle"), fmt.Sprintf(i18n.Msg("DriveLink.SaveError"), err), []string{"&Ok"})
				message.OnResult = func(int) { vtui.FrameManager.PostTask(reopen) }
				return
			}
			vtui.FrameManager.PostTask(reopen)
		})
		vtui.FrameManager.Push(dialog)
	})
}

func (pf *PanelsFrame) deleteDriveBookmark(menu *vtui.VMenu, bookmarks []DriveBookmark, index int, reopen func()) {
	if index < 0 || index >= len(bookmarks) {
		return
	}
	question := fmt.Sprintf(i18n.Msg("DriveLink.DeleteQuestion"), bookmarks[index].Name)
	vtui.ShowMessageOn(menu, i18n.Msg("DriveLink.DeleteTitle"), question, []string{"&Delete", i18n.Msg("DriveLink.Cancel")}).OnResult = func(choice int) {
		if choice != 0 {
			return
		}
		current, err := LoadDriveBookmarks(DriveBookmarksFilePath())
		if err == nil && index >= 0 && index < len(current) {
			current = append(current[:index], current[index+1:]...)
			err = SaveDriveBookmarks(DriveBookmarksFilePath(), current)
		}
		if err != nil {
			message := vtui.ShowMessage(i18n.Msg("DriveLink.ErrorTitle"), fmt.Sprintf(i18n.Msg("DriveLink.SaveError"), err), []string{"&Ok"})
			message.OnResult = func(int) { menu.Close(); vtui.FrameManager.PostTask(reopen) }
			return
		}
		menu.Close()
		vtui.FrameManager.PostTask(reopen)
	}
}

// moveDriveBookmark returns the list with the link at index moved by delta
// places (-1 up, 1 down), and whether anything moved: the first link cannot go
// up nor the last one down. The list itself is left as it was.
func moveDriveBookmark(bookmarks []DriveBookmark, index, delta int) ([]DriveBookmark, bool) {
	target := index + delta
	if index < 0 || index >= len(bookmarks) || target < 0 || target >= len(bookmarks) || delta == 0 {
		return bookmarks, false
	}
	moved := append([]DriveBookmark(nil), bookmarks...)
	moved[index], moved[target] = moved[target], moved[index]
	return moved, true
}

// moveBookmarkSlotInFile swaps a slot of the folder bookmarks with the nearest
// assigned slot in the direction of delta (the drive menu lists assigned slots
// only, so that is the row the user sees it pass) and saves the table. It
// reports false when there is no such slot.
func moveBookmarkSlotInFile(slot, delta int) (bool, error) {
	set, err := LoadBookmarks(BookmarksFilePath())
	if err != nil {
		return false, err
	}
	if slot < 0 || slot >= len(set) {
		return false, nil
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for next := slot + step; next >= 0 && next < len(set); next += step {
		if set[next].IsEmpty() {
			continue
		}
		set.SwapSlots(slot, next)
		return true, SaveBookmarks(BookmarksFilePath(), set)
	}
	return false, nil
}

// moveDriveBookmarkInFile moves one link of the saved list by delta places
// and saves the new order, so that it is remembered.
func moveDriveBookmarkInFile(index, delta int) (bool, error) {
	current, err := LoadDriveBookmarks(DriveBookmarksFilePath())
	if err != nil {
		return false, err
	}
	moved, ok := moveDriveBookmark(current, index, delta)
	if !ok {
		return false, nil
	}
	return true, SaveDriveBookmarks(DriveBookmarksFilePath(), moved)
}
