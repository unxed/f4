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
	finished   bool
	onFinish   func(bool, DriveBookmark)
}

func NewDriveBookmarkEditDialog(initial DriveBookmark, defaultPath string, onFinish func(bool, DriveBookmark)) *driveBookmarkEditDialog {
	const (
		width  = 64
		height = 14
	)
	titleKey := "DriveLink.CreateTitle"
	if initial.Name != "" || initial.Path != "" {
		titleKey = "DriveLink.EditTitle"
	}
	dialog := &driveBookmarkEditDialog{
		Window:     vtui.NewCenteredDialog(width, height, i18n.Msg(titleKey)),
		nameEdit:   vtui.NewEdit(0, 0, width-6, initial.Name),
		pathEdit:   vtui.NewEdit(0, 0, width-6, initial.Path),
		HotkeyEdit: &driveLinkHotkeyEdit{vtui.NewEdit(0, 0, max(1, vtui.StringWidth(initial.Hotkey)), initial.Hotkey)},
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

	dialog.AddItem(nameLabel)
	dialog.AddItem(dialog.nameEdit)
	dialog.AddItem(pathLabel)
	dialog.AddItem(dialog.pathEdit)
	dialog.AddItem(hotkeyLabel)
	dialog.AddItem(dialog.HotkeyEdit)
	dialog.AddItem(hotkeyHint)
	dialog.AddItem(createButton)
	dialog.AddItem(cancelButton)

	vbox := vtui.NewVBoxLayout(dialog.X1+2, dialog.Y1+2, width-4, height-4)
	vbox.Add(nameLabel, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(dialog.nameEdit, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(pathLabel, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(dialog.pathEdit, vtui.Margins{}, vtui.AlignFill)
	hotkeyRow := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hotkeyRow.Add(hotkeyLabel, vtui.Margins{Right: 1}, vtui.AlignLeft)
	hotkeyRow.Add(dialog.HotkeyEdit, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(hotkeyRow, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(hotkeyHint, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(createButton, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
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
		Name:   strings.TrimSpace(d.nameEdit.GetText()),
		Path:   strings.TrimSpace(d.pathEdit.GetText()),
		Hotkey: strings.TrimSpace(d.HotkeyEdit.GetText()),
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
