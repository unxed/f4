package envman

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// managerWindow adds the two visual details that are specific to the profile
// manager: a divider between the profile list and the inline editor, and the
// key legend drawn into the bottom border.
type managerWindow struct {
	*vtui.Window
	bottomHint    string
	listHint      string
	editHint      string
	splitOffset   int
	profilesTitle string
	detailsTitle  string
	editingTitle  string
	controller    *managerController
}

func (window *managerWindow) Show(screen *vtui.ScreenBuf) {
	window.Window.Show(screen)
	x1, y1, x2, y2 := window.GetPosition()
	painter := vtui.NewPainter(screen)
	splitX := x1 + window.splitOffset
	editing := window.controller != nil && window.controller.editing
	window.recolorListBox(screen, !editing)
	if !editing {
		window.redrawReadablePreview(screen)
		recolorManagerControl(screen, window.controller.nameEdit, managerInputBackground(false))
		recolorManagerControl(screen, window.controller.variablesEdit, managerInputBackground(false))
	}
	if splitX > x1 && splitX < x2 {
		painter.Fill(splitX, y1+1, splitX, y2-1, '│', vtui.Palette[vtui.ColDialogBox])
	}
	drawManagerPaneHeader(screen, x1+1, splitX-1, y1+1, window.profilesTitle, !editing)
	rightTitle := window.detailsTitle
	if editing {
		rightTitle = window.editingTitle
	}
	drawManagerPaneHeader(screen, splitX+1, x2-1, y1+1, rightTitle, editing)
	if window.bottomHint != "" {
		drawManagerHint(screen, x1, x2, y2, window.bottomHint)
	}
}

func (window *managerWindow) redrawReadablePreview(screen *vtui.ScreenBuf) {
	if window.controller == nil || window.controller.nameEdit == nil {
		return
	}
	controls := []vtui.UIElement{
		window.controller.nameEdit,
		window.controller.enabledEdit,
		window.controller.variablesEdit,
	}
	for _, control := range controls {
		control.SetDisabled(false)
		control.Show(screen)
		control.SetDisabled(true)
	}
}

func recolorManagerRect(screen *vtui.ScreenBuf, left, top, right, bottom int, background uint64) {
	if left > right || top > bottom {
		return
	}
	for y := top; y <= bottom; y++ {
		row := make([]vtui.CharInfo, right-left+1)
		for x := left; x <= right; x++ {
			cell := screen.GetCell(x, y)
			cell.Attributes = managerHintWithBackground(cell.Attributes, background)
			row[x-left] = cell
		}
		screen.Write(left, y, row)
	}
}

func recolorManagerControl(screen *vtui.ScreenBuf, control vtui.UIElement, background uint64) {
	if control == nil {
		return
	}
	left, top, right, bottom := control.GetPosition()
	recolorManagerRect(screen, left, top, right, bottom, background)
}

func managerInputBackground(isActive bool) uint64 {
	base := vtui.Palette[vtui.ColDialogEdit]
	if isActive {
		return base
	}
	if base&vtui.IsBgRGB == 0 {
		return vtui.Palette[vtui.ColDialogEditSelected]
	}
	rgb := vtui.GetRGBBack(base)
	lighten := func(channel uint32) uint32 { return channel + (0xff-channel)*8/100 }
	red := lighten((rgb >> 16) & 0xff)
	green := lighten((rgb >> 8) & 0xff)
	blue := lighten(rgb & 0xff)
	return vtui.SetRGBBack(base, red<<16|green<<8|blue)
}

func (window *managerWindow) recolorListBox(screen *vtui.ScreenBuf, active bool) {
	if window.controller == nil || window.controller.list == nil {
		return
	}
	list := window.controller.list
	// Keep the row immediately below the pane title in the ordinary dialog
	// background; the input-like list surface begins on the list's first row.
	recolorManagerRect(screen, list.X1, list.Y1, list.X2, list.Y2, managerInputBackground(active))
	window.redrawListCursor(screen, active)
}

func (window *managerWindow) redrawListCursor(screen *vtui.ScreenBuf, active bool) {
	list := window.controller.list
	row := list.SelectPos - list.TopPos
	if row < 0 || row >= list.ViewHeight {
		return
	}
	y := list.Y1 + list.MarginTop + row
	background := inactiveManagerCursorAttr(vtui.Palette[vtui.ColDialogText])
	if active && list.IsFocused() {
		background = vtui.Palette[vtui.ColDialogSelectedButton]
	}
	right := list.X1 + list.GetContentWidth() - 1
	for x := list.X1; x <= right; x++ {
		cell := screen.GetCell(x, y)
		cell.Attributes = managerHintWithBackground(cell.Attributes, background)
		screen.Write(x, y, []vtui.CharInfo{cell})
	}
}

func drawManagerPaneHeader(screen *vtui.ScreenBuf, left, right, y int, title string, active bool) {
	if left > right {
		return
	}
	attr := vtui.Palette[vtui.ColDialogBoxTitle]
	if active {
		attr = vtui.Palette[vtui.ColDialogSelectedButton]
	}
	available := right - left + 1
	title = runewidth.Truncate(title, max(available-2, 0), "")
	plaqueWidth := runewidth.StringWidth(title) + 2
	x := left + (available-plaqueWidth)/2
	screen.FillRect(x, y, x+plaqueWidth-1, y, ' ', attr)
	screen.Write(x+1, y, vtui.StringToCharInfo(title, attr))
}

type managerModeButton struct {
	*vtui.Button
	modeVisible bool
}

func newManagerModeButton(button *vtui.Button) *managerModeButton {
	result := &managerModeButton{Button: button}
	result.SetVisible(false)
	return result
}

func (button *managerModeButton) setModeVisible(visible bool) {
	button.modeVisible = visible
	button.SetVisible(visible)
}

func (button *managerModeButton) Show(screen *vtui.ScreenBuf) {
	button.SetVisible(button.modeVisible)
	if button.modeVisible {
		button.Button.Show(screen)
	}
}

type managerHintPart struct {
	key   string
	label string
}

func parseManagerHint(hint string) []managerHintPart {
	segments := strings.Split(hint, "  ")
	parts := make([]managerHintPart, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		key, label, found := strings.Cut(segment, " ")
		if !found {
			parts = append(parts, managerHintPart{key: key})
			continue
		}
		parts = append(parts, managerHintPart{key: key, label: strings.TrimSpace(label)})
	}
	return parts
}

func drawManagerHint(screen *vtui.ScreenBuf, x1, x2, y int, hint string) {
	left, right := x1+1, x2-1
	if left > right {
		return
	}
	parts := parseManagerHint(hint)
	background := managerHintBackground()
	normal := managerHintWithBackground(vtui.Palette[vtui.ColDialogBoxTitle], background)
	active := managerHintWithBackground(vtui.Palette[vtui.ColDialogHighlightBoxTitle], background)

	contentWidth := managerHintWidth(parts)
	totalWidth := contentWidth + 2 // one background-only cell on each side
	available := right - left + 1
	renderWidth := totalWidth
	if renderWidth > available {
		renderWidth = available
	}
	x := left
	if totalWidth < available {
		x += (available - totalWidth) / 2
	}
	if renderWidth > 0 {
		screen.FillRect(x, y, x+renderWidth-1, y, ' ', normal)
	}
	contentRight := x + renderWidth - 1
	if renderWidth >= 2 {
		x++
		contentRight--
	}
	write := func(text string, attr uint64) {
		remaining := contentRight - x + 1
		if remaining <= 0 || text == "" {
			return
		}
		text = runewidth.Truncate(text, remaining, "")
		screen.Write(x, y, vtui.StringToCharInfo(text, attr))
		x += runewidth.StringWidth(text)
	}
	for index, part := range parts {
		if index > 0 {
			write("  ", normal)
		}
		write(part.key, active)
		if part.label != "" {
			write(" "+part.label, normal)
		}
		if x > right {
			break
		}
	}
}

func managerHintWidth(parts []managerHintPart) int {
	width := 0
	for index, part := range parts {
		if index > 0 {
			width += 2
		}
		width += runewidth.StringWidth(part.key)
		if part.label != "" {
			width += 1 + runewidth.StringWidth(part.label)
		}
	}
	return width
}

func managerHintBackground() uint64 {
	dialog := vtui.Palette[vtui.ColDialogText]
	if dialog&vtui.IsBgRGB == 0 {
		return vtui.Palette[vtui.ColKeyBarText]
	}
	rgb := vtui.GetRGBBack(dialog)
	lighten := func(channel uint32) uint32 { return channel + (0xff-channel)/8 }
	red := lighten((rgb >> 16) & 0xff)
	green := lighten((rgb >> 8) & 0xff)
	blue := lighten(rgb & 0xff)
	return vtui.SetRGBBack(dialog, red<<16|green<<8|blue)
}

func managerHintWithBackground(foreground, background uint64) uint64 {
	if background&vtui.IsBgRGB != 0 {
		return vtui.SetRGBBack(foreground, vtui.GetRGBBack(background))
	}
	return vtui.SetIndexBack(foreground, vtui.GetIndexBack(background))
}

func (window *managerWindow) ProcessKey(event *vtinput.InputEvent) bool {
	if event != nil && event.KeyDown && window.controller != nil {
		control := event.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
		alt := event.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
		shift := event.ControlKeyState&vtinput.ShiftPressed != 0
		editorFocused := window.controller.dialog.GetFocusedItem() != window.controller.list &&
			window.controller.nameEdit != nil && !window.controller.nameEdit.IsDisabled()
		if editorFocused && !control && !alt && !shift && event.VirtualKeyCode == vtinput.VK_ESCAPE {
			window.controller.cancelInlineEdit()
			return true
		}
		if editorFocused && control && !alt && !shift && event.VirtualKeyCode == vtinput.VK_RETURN {
			window.controller.saveInlineEdit()
			return true
		}
	}
	return window.Window.ProcessKey(event)
}

func (window *managerWindow) ProcessMouse(event *vtinput.InputEvent) bool {
	oldSelection := -1
	toggleIndex := -1
	if window.controller != nil && window.controller.list != nil {
		oldSelection = window.controller.list.SelectPos
		list := window.controller.list
		x1, y1, _, y2 := window.GetPosition()
		splitX := x1 + window.splitOffset
		if !window.controller.editing && event != nil && event.Type == vtinput.MouseEventType &&
			int(event.MouseX) > splitX && int(event.MouseY) >= y1+2 && int(event.MouseY) < y2 {
			return true
		}
		if !window.controller.editing && event != nil && event.Type == vtinput.MouseEventType && event.KeyDown &&
			event.ButtonState == vtinput.FromLeft1stButtonPressed && event.MouseEventFlags&vtinput.MouseMoved == 0 &&
			int(event.MouseX) >= list.X1 && int(event.MouseX) <= list.X1+2 {
			toggleIndex = list.GetClickIndex(int(event.MouseY))
		}
	}
	handled := window.Window.ProcessMouse(event)
	if toggleIndex >= 0 && toggleIndex < len(window.controller.config.Entries) {
		window.controller.toggle(toggleIndex, true, false)
		handled = true
	}
	if oldSelection >= 0 && window.controller.list.SelectPos != oldSelection {
		window.controller.selectionChanged()
	}
	return handled
}

type managerListRow struct {
	text     string
	inactive bool
	index    int
	list     *vtui.ListBox
}

func (row managerListRow) GetCellText(int) string { return row.text }

func (row managerListRow) GetCellAttr(_ int, defaultAttr uint64) uint64 {
	if row.list != nil && row.list.AlwaysShowCursor && row.index == row.list.SelectPos && !row.list.IsFocused() {
		defaultAttr = inactiveManagerCursorAttr(vtui.Palette[vtui.ColDialogText])
	}
	if !row.inactive {
		return defaultAttr
	}
	return subduedManagerRowAttr(defaultAttr)
}

// inactiveManagerCursorAttr keeps the current profile identifiable without
// looking like the keyboard cursor. Its neutral shade is derived from the
// live dialog palette, so it follows theme changes made after dialog creation.
func inactiveManagerCursorAttr(dialogAttr uint64) uint64 {
	if dialogAttr&(vtui.IsFgRGB|vtui.IsBgRGB) != vtui.IsFgRGB|vtui.IsBgRGB {
		return dialogAttr ^ vtui.BackgroundIntensity
	}
	foreground := vtui.GetRGBFore(dialogAttr)
	background := vtui.GetRGBBack(dialogAttr)
	gray := func(rgb uint32) uint32 {
		return (((rgb>>16)&0xff)*299 + ((rgb>>8)&0xff)*587 + (rgb&0xff)*114) / 1000
	}
	foregroundGray := gray(foreground)
	backgroundGray := gray(background)
	selectionGray := (backgroundGray*3 + foregroundGray) / 4
	neutralBackground := selectionGray<<16 | selectionGray<<8 | selectionGray
	return vtui.SetRGBBack(dialogAttr, neutralBackground)
}

// subduedManagerRowAttr is deliberately gentler than vtui.DimColor: disabled
// profiles remain readable while being visually distinct from enabled ones.
func subduedManagerRowAttr(attr uint64) uint64 {
	if attr&vtui.IsFgRGB == 0 {
		return attr &^ vtui.ForegroundIntensity
	}
	foreground := vtui.GetRGBFore(attr)
	red := ((foreground >> 16) & 0xff) * 3 / 4
	green := ((foreground >> 8) & 0xff) * 3 / 4
	blue := (foreground & 0xff) * 3 / 4
	return vtui.SetRGBFore(attr, red<<16|green<<8|blue)
}
