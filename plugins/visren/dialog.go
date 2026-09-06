package visren

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var nameTemplates = []string{
	"[N]", "[N#-#]", "[C#+#]", "────────",
	"[L]", "[U]", "[F]", "[T]", "[M]", "────────",
	"[#]", "[t]", "[a]", "[l]", "[y]", "[g]", "────────",
	"[c]", "[m]", "[d]", "[r]", "────────",
	"[DM]", "[TM]", "[TL]", "[TR]", "[R]", "[V]",
}

var extTemplates = []string{
	"[E]", "[E#-#]", "[C#+#]", "────────", "[L]", "[U]", "[F]", "[T]",
}

type previewList struct {
	vtui.ScreenObject
	rows                 []Preview
	cursor, top, divider int
	srcOffset, dstOffset int
	scrollBar            *vtui.ScrollBar
}

type tokenButton struct {
	vtui.ScreenObject
	onClick func()
}

func newTokenButton(onClick func()) *tokenButton {
	b := &tokenButton{onClick: onClick}
	b.SetCanFocus(true)
	b.SetText("[+]")
	return b
}

func (b *tokenButton) Show(scr *vtui.ScreenBuf) {
	b.ScreenObject.Show(scr)
	attr := vtui.Palette[vtui.ColDialogText]
	if b.IsFocused() {
		attr = vtui.Palette[vtui.ColDialogSelectedButton]
	}
	scr.Write(b.X1, b.Y1, vtui.StringToCharInfo("[+]", attr))
}

func (b *tokenButton) fire() bool {
	if b.IsDisabled() {
		return false
	}
	if b.onClick != nil {
		b.onClick()
	}
	return true
}

func (b *tokenButton) ProcessKey(e *vtinput.InputEvent) bool {
	if e.KeyDown && (e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_SPACE) {
		return b.fire()
	}
	return false
}

func (b *tokenButton) ProcessMouse(e *vtinput.InputEvent) bool {
	return e.KeyDown && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && b.fire()
}

func newPreviewList(x, y, w, h int) *previewList {
	p := &previewList{}
	p.scrollBar = vtui.NewScrollBar(0, 0, 0)
	p.scrollBar.SetOwner(p)
	p.scrollBar.SetVisible(true)
	p.scrollBar.OnScroll = func(value int) {
		p.top = value
		vtui.FrameManager.Redraw()
	}
	p.scrollBar.OnStep = func(step int) {
		delta := 1
		if step < 0 {
			delta = -1
		}
		if step == -2 || step == 2 {
			delta *= maxInt(1, p.visibleHeight())
		}
		p.setTop(p.top + delta)
	}
	p.SetPosition(x, y, x+w-1, y+h-1)
	p.divider = p.contentWidth() / 2
	p.SetCanFocus(true)
	return p
}

func copyBackground(attr, background uint64) uint64 {
	if background&vtui.IsBgRGB != 0 {
		attr = vtui.SetRGBBack(attr, vtui.GetRGBBack(background))
	} else {
		attr = vtui.SetIndexBack(attr, vtui.GetIndexBack(background))
	}
	return (attr &^ vtui.BackgroundIntensity) | (background & vtui.BackgroundIntensity)
}

func (p *previewList) SetPosition(x1, y1, x2, y2 int) {
	p.ScreenObject.SetPosition(x1, y1, x2, y2)
	if p.scrollBar != nil {
		p.scrollBar.SetPosition(x2, y1, x2, y2)
	}
	p.syncScrollBar()
}

func (p *previewList) visibleHeight() int {
	return maxInt(0, p.Y2-p.Y1+1)
}

func (p *previewList) scrollbarNeeded() bool {
	return p.visibleHeight() > 2 && len(p.rows) > p.visibleHeight()
}

func (p *previewList) contentWidth() int {
	width := maxInt(0, p.X2-p.X1+1)
	if p.scrollbarNeeded() {
		width--
	}
	return width
}

func (p *previewList) syncScrollBar() {
	if p.scrollBar == nil {
		return
	}
	height := p.visibleHeight()
	maxTop := maxInt(0, len(p.rows)-height)
	if p.top > maxTop {
		p.top = maxTop
	}
	if p.top < 0 {
		p.top = 0
	}
	p.scrollBar.SetPosition(p.X2, p.Y1, p.X2, p.Y2)
	p.scrollBar.PgStep = maxInt(1, height)
	p.scrollBar.SetParams(p.top, 0, maxTop)
}

func (p *previewList) setTop(top int) {
	maxTop := maxInt(0, len(p.rows)-p.visibleHeight())
	p.top = maxInt(0, minInt(maxTop, top))
	p.syncScrollBar()
	vtui.FrameManager.Redraw()
}

func (p *previewList) setRows(rows []Preview) {
	hadScrollbar := p.scrollbarNeeded()
	p.rows = rows
	if len(rows) == 0 {
		p.cursor, p.top = 0, 0
		p.syncScrollBar()
		return
	}
	if p.cursor >= len(rows) {
		p.cursor = len(rows) - 1
	}
	p.ensureVisible()
	if hadScrollbar != p.scrollbarNeeded() {
		p.divider = p.contentWidth() / 2
	}
	p.syncScrollBar()
}

func (p *previewList) ensureVisible() {
	height := p.Y2 - p.Y1 + 1
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+height {
		p.top = p.cursor - height + 1
	}
	if p.top < 0 {
		p.top = 0
	}
	p.syncScrollBar()
}

func (p *previewList) Show(scr *vtui.ScreenBuf) {
	p.ScreenObject.Show(scr)
	width := p.contentWidth()
	if p.divider < 5 || p.divider > width-6 {
		p.divider = width / 2
	}
	for line, y := 0, p.Y1; y <= p.Y2; line, y = line+1, y+1 {
		idx := p.top + line
		attr := vtui.Palette[vtui.ColDialogText]
		if p.IsFocused() && idx == p.cursor && idx < len(p.rows) {
			attr = vtui.Palette[vtui.ColDialogSelectedButton]
		}
		source, destination := "", ""
		var sourceMatches, replacementMatches []TextRange
		if idx < len(p.rows) {
			source, destination = p.rows[idx].Item.Source, p.rows[idx].Destination
			sourceMatches = p.rows[idx].Matches
			replacementMatches = p.rows[idx].ReplacementMatches
		}
		left := cropPadHighlighted(source, p.srcOffset, p.divider, attr, p.matchAttr(attr), sourceMatches)
		right := cropPadHighlighted(destination, p.dstOffset, width-p.divider-1, attr, p.matchAttr(attr), replacementMatches)
		scr.Write(p.X1, y, left)
		scr.Write(p.X1+p.divider, y, vtui.StringToCharInfo("│", attr))
		scr.Write(p.X1+p.divider+1, y, right)
	}
	if p.scrollbarNeeded() {
		p.syncScrollBar()
		vtui.DrawScrollBar(scr, p.X2, p.Y1, p.visibleHeight(), p.top, len(p.rows), vtui.Palette[vtui.ColDialogBox])
	}
}

func (p *previewList) matchAttr(base uint64) uint64 {
	highlight := vtui.Palette[vtui.ColDialogHighlightText]
	if highlight&vtui.IsFgRGB != 0 {
		return vtui.SetRGBFore(base, vtui.GetRGBFore(highlight))
	}
	return vtui.SetIndexFore(base, vtui.GetIndexFore(highlight))
}

func cropPadHighlighted(value string, offset, width int, baseAttr, matchAttr uint64, ranges []TextRange) []vtui.CharInfo {
	isMatch := func(pos int) bool {
		for _, match := range ranges {
			if pos >= match.Start && pos < match.End {
				return true
			}
		}
		return false
	}

	runes := []rune(value)
	if offset < 0 {
		offset = 0
	}
	if offset > len(runes) {
		offset = len(runes)
	}
	cells := make([]vtui.CharInfo, 0, width)
	used := 0
	for pos := offset; pos < len(runes); pos++ {
		cellWidth := runewidth.RuneWidth(runes[pos])
		if used+cellWidth > width {
			break
		}
		attr := baseAttr
		if isMatch(pos) {
			attr = matchAttr
		}
		cells = append(cells, vtui.CharInfo{Char: uint64(runes[pos]), Attributes: attr})
		for filler := 1; filler < cellWidth; filler++ {
			cells = append(cells, vtui.CharInfo{Char: vtui.WideCharFiller, Attributes: attr})
		}
		used += cellWidth
	}
	for used < width {
		cells = append(cells, vtui.CharInfo{Char: ' ', Attributes: baseAttr})
		used++
	}
	return cells
}

func (p *previewList) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown || len(p.rows) == 0 {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_UP:
		if p.cursor > 0 {
			p.cursor--
		}
	case vtinput.VK_DOWN:
		if p.cursor+1 < len(p.rows) {
			p.cursor++
		}
	case vtinput.VK_HOME:
		p.cursor = 0
	case vtinput.VK_END:
		p.cursor = len(p.rows) - 1
	case vtinput.VK_PRIOR:
		p.cursor = maxInt(0, p.cursor-(p.Y2-p.Y1+1))
	case vtinput.VK_NEXT:
		p.cursor = minInt(len(p.rows)-1, p.cursor+(p.Y2-p.Y1+1))
	default:
		return false
	}
	p.ensureVisible()
	vtui.FrameManager.Redraw()
	return true
}

func (p *previewList) ProcessMouse(e *vtinput.InputEvent) bool {
	if p.scrollBar != nil {
		if p.scrollBar.ProcessMouse(e) {
			return true
		}
	}
	if e.Type != vtinput.MouseEventType || !p.HitTest(int(e.MouseX), int(e.MouseY)) {
		return false
	}
	if e.WheelDirection != 0 {
		delta := 3
		if e.WheelDirection > 0 {
			delta = -delta
		}
		p.setTop(p.top + delta)
		return true
	}
	if e.KeyDown && (e.ButtonState&vtinput.FromLeft1stButtonPressed) != 0 {
		idx := p.top + int(e.MouseY) - p.Y1
		if idx >= 0 && idx < len(p.rows) {
			p.cursor = idx
			p.ensureVisible()
			vtui.FrameManager.Redraw()
		}
		return true
	}
	return false
}

type Dialog struct {
	*vtui.Window
	host   PanelHost
	plugin *Plugin
	fs     *vfs.OSVFS
	dir    string
	engine Engine
	rows   []Preview

	nameEdit, extEdit, searchEdit, replaceEdit *vtui.Edit
	nameCombo, extCombo                        *vtui.ComboBox
	namePlus, extPlus                          *tokenButton
	caseCheck, regexCheck                      *vtui.Checkbox
	preview                                    *previewList
	logLine                                    *vtui.Text
	renameButton, undoButton, editorButton     *vtui.Button
	cancelButton                               *vtui.Button
	separators                                 []*vtui.Text

	logging      bool
	undoMode     bool
	maximized    bool
	lastW, lastH int
	wordDiv      string
	dragRow      int
	logMarkX     int
}

type visRenHelpView struct {
	*vtui.HelpView
}

func newVisRenHelpView(w, h int) *visRenHelpView {
	view := &visRenHelpView{HelpView: vtui.NewHelpView(vtui.GlobalHelpEngine, "VisRen")}
	view.ResizeConsole(w, h)
	return view
}

func (view *visRenHelpView) ResizeConsole(w, h int) {
	// Let HelpView establish the correct height first. It uses a fixed width,
	// so learn BaseWindow's internal size offset and then expand that width.
	view.HelpView.ResizeConsole(w, h)
	x1, y1, x2, y2 := view.GetPosition()
	probeW, probeH := x2-x1+2, y2-y1+2
	view.ChangeSize(probeW, probeH)
	x1, y1, x2, y2 = view.GetPosition()
	sizeOffsetW := x2 - x1 + 1 - probeW
	sizeOffsetH := y2 - y1 + 1 - probeH

	width := minInt(120, w-4)
	if width < 20 {
		width = 20
	}
	height := h - 4
	if height < 5 {
		height = 5
	}
	view.ChangeSize(width-sizeOffsetW, height-sizeOffsetH)
	view.Center(w, h)
}

func showDialog(host PanelHost, plugin *Plugin, fs *vfs.OSVFS, dir string, items []*Item, undo bool) {
	d := newDialog(host, plugin, fs, dir, items)
	if undo {
		d.loadUndoView()
	}
	vtui.FrameManager.Push(d)
}

func (d *Dialog) Show(scr *vtui.ScreenBuf) {
	d.Window.Show(scr)
	matchArrowBackground := func(x, y int) {
		arrowAttr := scr.GetCell(x, y).Attributes
		fieldAttr := scr.GetCell(x-1, y).Attributes
		scr.Write(x, y, vtui.StringToCharInfo("↓", copyBackground(arrowAttr, fieldAttr)))
	}
	for _, combo := range []*vtui.ComboBox{d.nameCombo, d.extCombo} {
		if combo == nil || !combo.IsVisible() || combo.X2 <= combo.X1 {
			continue
		}
		matchArrowBackground(combo.X2, combo.Y1)
	}
	for _, edit := range []*vtui.Edit{d.nameEdit, d.extEdit, d.searchEdit, d.replaceEdit} {
		if edit == nil || !edit.IsVisible() || !edit.ShowHistoryButton || edit.X2 <= edit.X1 {
			continue
		}
		matchArrowBackground(edit.X2, edit.Y1)
	}
}

func newDialog(host PanelHost, plugin *Plugin, fs *vfs.OSVFS, dir string, items []*Item) *Dialog {
	w := vtui.NewCenteredDialog(78, 23, " "+tr("VisRen.Title", "Visual File Renamer")+" ")
	w.ShowZoom = true
	w.SetHelp("VisRen")
	d := &Dialog{Window: w, host: host, plugin: plugin, fs: fs, dir: dir, engine: Engine{Items: items}, logging: true, dragRow: -1}
	d.lastW = vtui.FrameManager.GetScreenSize()
	d.lastH = vtui.FrameManager.GetScreenHeight()
	d.wordDiv = loadConfig().WordDiv

	d.nameEdit = vtui.NewEdit(0, 0, 37, "[N]")
	d.extEdit = vtui.NewEdit(0, 0, 35, "[E]")
	d.searchEdit = vtui.NewEdit(0, 0, 24, "")
	d.replaceEdit = vtui.NewEdit(0, 0, 24, "")
	for edit, id := range map[*vtui.Edit]string{
		d.nameEdit: "VisRenMaskName", d.extEdit: "VisRenMaskExt", d.searchEdit: "VisRenSearch", d.replaceEdit: "VisRenReplace",
	} {
		edit.HistoryID, edit.ShowHistoryButton = id, true
		edit.OnTextChange = func(string) { d.refreshPreview() }
	}
	d.nameCombo = vtui.NewComboBox(0, 0, 30, nameTemplates)
	d.extCombo = vtui.NewComboBox(0, 0, 30, extTemplates)
	d.nameCombo.DropdownOnly, d.extCombo.DropdownOnly = true, true
	d.nameCombo.Edit.SetText(nameTemplates[0])
	d.extCombo.Edit.SetText(extTemplates[0])
	d.nameCombo.Menu.OnAction = func(idx int) {
		if !strings.HasPrefix(nameTemplates[idx], "─") {
			d.nameCombo.Edit.SetText(nameTemplates[idx])
		}
	}
	d.extCombo.Menu.OnAction = func(idx int) {
		if !strings.HasPrefix(extTemplates[idx], "─") {
			d.extCombo.Edit.SetText(extTemplates[idx])
		}
	}
	d.namePlus = newTokenButton(func() {
		d.insertSelectedTemplate(d.nameCombo, d.nameEdit, nameTemplates, false)
		d.SetFocusedItem(d.nameEdit)
	})
	d.extPlus = newTokenButton(func() {
		d.insertSelectedTemplate(d.extCombo, d.extEdit, extTemplates, true)
		d.SetFocusedItem(d.extEdit)
	})
	d.caseCheck = vtui.NewCheckbox(0, 0, tr("VisRen.CaseSensitive", "&Case sensitive"), false)
	d.caseCheck.State = 1
	d.regexCheck = vtui.NewCheckbox(0, 0, tr("VisRen.Regex", "Re&gular expression"), false)
	d.caseCheck.OnChange = func(int) { d.refreshPreview() }
	d.regexCheck.OnChange = func(int) { d.refreshPreview() }
	d.preview = newPreviewList(0, 0, 74, 11)
	d.logLine = vtui.NewText(0, 0, "", vtui.Palette[vtui.ColDialogText])
	d.renameButton = vtui.NewButton(0, 0, tr("VisRen.Rename", "&Rename"))
	d.renameButton.IsDefault = true
	d.undoButton = vtui.NewButton(0, 0, tr("VisRen.Undo", "F6 &Undo"))
	d.editorButton = vtui.NewButton(0, 0, tr("VisRen.Editor", "F4 &In editor"))
	d.cancelButton = vtui.NewButton(0, 0, tr("VisRen.Cancel", "Cancel"))

	d.AddItem(d.logLine)
	for _, item := range []vtui.UIElement{d.nameEdit, d.extEdit, d.nameCombo, d.namePlus, d.extCombo, d.extPlus, d.searchEdit, d.replaceEdit, d.caseCheck, d.regexCheck, d.preview, d.renameButton, d.undoButton, d.editorButton, d.cancelButton} {
		d.AddItem(item)
	}
	d.renameButton.OnClick = d.onRename
	d.undoButton.OnClick = d.loadUndoView
	d.editorButton.OnClick = d.askEditorColumns
	d.cancelButton.OnClick = d.Close
	d.SetFocusedItem(d.searchEdit)
	d.layout()
	d.refreshPreview()
	return d
}

func (d *Dialog) addText(text string) *vtui.Text {
	t := vtui.NewText(0, 0, text, vtui.Palette[vtui.ColDialogText])
	d.AddItem(t)
	return t
}

func centeredRule(title string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(title)+2 > width {
		return runewidth.Truncate(title, width, "")
	}
	remaining := width - runewidth.StringWidth(title) - 2
	left := remaining / 2
	return strings.Repeat("─", left) + " " + title + " " + strings.Repeat("─", remaining-left)
}

func (d *Dialog) layout() {
	x, y := d.X1, d.Y1
	w, h := d.X2-d.X1+1, d.Y2-d.Y1+1
	inner := w - 4
	rightX := x + w/2 + 2

	if len(d.separators) == 0 {
		d.separators = []*vtui.Text{
			d.addText(tr("VisRen.Name", "&Name")), d.addText(tr("VisRen.Extension", "&Extension")),
			d.addText(tr("VisRen.Search", "&Search for")), d.addText(tr("VisRen.Replace", "Re&place with")),
			d.addText(tr("VisRen.NameTemplates", "Name &templates")), d.addText(tr("VisRen.ExtTemplates", "Extension temp&lates")),
			d.addText(""), d.addText(""),
		}
		d.separators[0].FocusLink = d.nameEdit
		d.separators[1].FocusLink = d.extEdit
		d.separators[2].FocusLink = d.searchEdit
		d.separators[3].FocusLink = d.replaceEdit
		d.separators[4].FocusLink = d.nameCombo
		d.separators[5].FocusLink = d.extCombo
	}
	d.separators[0].SetPosition(x+2, y+2, x+10, y+2)
	d.separators[1].SetPosition(rightX, y+2, rightX+12, y+2)
	d.nameEdit.SetPosition(x+2, y+3, rightX-3, y+3)
	d.extEdit.SetPosition(rightX, y+3, x+w-3, y+3)
	d.separators[4].SetPosition(x+2, y+4, rightX-3, y+4)
	d.separators[5].SetPosition(rightX, y+4, x+w-3, y+4)
	d.nameCombo.SetPosition(x+2, y+5, rightX-10, y+5)
	d.namePlus.SetPosition(rightX-7, y+5, rightX-5, y+5)
	d.extCombo.SetPosition(rightX, y+5, x+w-8, y+5)
	d.extPlus.SetPosition(x+w-5, y+5, x+w-3, y+5)
	d.separators[6].SetText(strings.Repeat("─", inner))
	d.separators[6].SetPosition(x+2, y+6, x+w-3, y+6)
	d.separators[2].SetPosition(x+2, y+7, x+13, y+7)
	d.searchEdit.SetPosition(x+15, y+7, rightX-3, y+7)
	d.caseCheck.SetPosition(rightX, y+7, rightX+23, y+7)
	d.separators[3].SetPosition(x+2, y+8, x+13, y+8)
	d.replaceEdit.SetPosition(x+15, y+8, rightX-3, y+8)
	d.regexCheck.SetPosition(rightX, y+8, rightX+25, y+8)
	d.separators[7].SetText(centeredRule(tr("VisRen.BeforeAfter", "name before - name after"), inner))
	d.separators[7].SetPosition(x+2, y+9, x+w-3, y+9)
	d.preview.SetPosition(x+2, y+10, x+w-3, y+h-5)
	d.preview.divider = d.preview.contentWidth() / 2
	d.logLine.SetPosition(x+2, y+h-4, x+w-3, y+h-4)
	d.updateLogLine()
	buttonWidths := []int{d.renameButton.X2 - d.renameButton.X1 + 1, d.undoButton.X2 - d.undoButton.X1 + 1, d.editorButton.X2 - d.editorButton.X1 + 1, d.cancelButton.X2 - d.cancelButton.X1 + 1}
	total := buttonWidths[0] + buttonWidths[1] + buttonWidths[2] + buttonWidths[3] + 6
	bx := x + (w-total)/2
	for idx, button := range []*vtui.Button{d.renameButton, d.undoButton, d.editorButton, d.cancelButton} {
		button.SetPosition(bx, y+h-3, bx+buttonWidths[idx]-1, y+h-3)
		bx += buttonWidths[idx] + 2
	}
}

func (d *Dialog) options() Options {
	return Options{NameMask: d.nameEdit.GetText(), ExtMask: d.extEdit.GetText(), Search: d.searchEdit.GetText(), Replace: d.replaceEdit.GetText(), CaseSensitive: d.caseCheck.State == 1, Regex: d.regexCheck.State == 1, WordDiv: d.wordDiv}
}

func (d *Dialog) refreshPreview() {
	if d.preview == nil {
		return
	}
	if d.undoMode {
		_, log := d.plugin.getUndo()
		d.rows = d.rows[:0]
		for idx := len(log) - 1; idx >= 0; idx-- {
			d.rows = append(d.rows, Preview{Item: &Item{Source: log[idx].New}, Destination: log[idx].Old})
		}
	} else {
		d.rows, _ = d.engine.Build(d.options())
	}
	d.preview.setRows(d.rows)
	hasError := false
	for _, row := range d.rows {
		hasError = hasError || row.Err != nil
	}
	d.renameButton.SetDisabled(hasError || len(d.rows) == 0)
	d.editorButton.SetDisabled(hasError || len(d.rows) == 0 || d.undoMode)
	vtui.FrameManager.Redraw()
}

func (d *Dialog) insertTemplate(edit *vtui.Edit, template string, extension bool) {
	if strings.HasPrefix(template, "─") {
		return
	}
	baseToken := "N"
	maxLen := 1
	if extension {
		baseToken = "E"
		for _, item := range d.engine.Items {
			_, ext := SplitName(item.Source)
			maxLen = maxInt(maxLen, len([]rune(ext)))
		}
	} else {
		for _, item := range d.engine.Items {
			base, _ := SplitName(item.Source)
			maxLen = maxInt(maxLen, len([]rune(base)))
		}
	}
	template = strings.ReplaceAll(template, baseToken+"#-#", fmt.Sprintf("%s1-%d", baseToken, maxLen))
	template = strings.ReplaceAll(template, "C#+#", "C1+1")
	edit.InsertString(template)
	d.refreshPreview()
}

func (d *Dialog) insertSelectedTemplate(combo *vtui.ComboBox, edit *vtui.Edit, templates []string, extension bool) bool {
	idx := combo.Menu.SelectPos
	if idx < 0 || idx >= len(templates) || strings.HasPrefix(templates[idx], "─") {
		return false
	}
	combo.Edit.SetText(templates[idx])
	d.insertTemplate(edit, templates[idx], extension)
	return true
}

func (d *Dialog) updateLogLine() {
	mark := " "
	if d.logging {
		mark = "√"
	}
	_, log := d.plugin.getUndo()
	star := ""
	if len(log) > 0 {
		star = "*"
	}
	width := d.logLine.X2 - d.logLine.X1 + 1
	caption := fmt.Sprintf("%s %s%s", mark, tr("VisRen.Log", "Log renaming"), star)
	captionWidth := runewidth.StringWidth(caption)
	if captionWidth+2 > width {
		// centeredRule truncates an oversized caption without side rules.
		d.logMarkX = d.logLine.X1
	} else {
		left := (width - captionWidth - 2) / 2
		d.logMarkX = d.logLine.X1 + left + 1
	}
	d.logLine.SetText(centeredRule(caption, width))
}

func (d *Dialog) setControlsDisabled(disabled bool) {
	for _, item := range []vtui.UIElement{d.nameEdit, d.extEdit, d.nameCombo, d.namePlus, d.extCombo, d.extPlus, d.searchEdit, d.replaceEdit, d.caseCheck, d.regexCheck} {
		item.SetDisabled(disabled)
	}
}

func (d *Dialog) loadUndoView() {
	dir, log := d.plugin.getUndo()
	if len(log) == 0 {
		vtui.ShowMessageOn(d, " "+tr("VisRen.Undo", "Undo")+" ", tr("VisRen.NothingToUndo", "There is no rename log."), []string{"&Ok"})
		return
	}
	d.undoMode, d.dir = true, dir
	d.SetTitle(" " + dir + " ")
	d.setControlsDisabled(true)
	d.editorButton.SetDisabled(true)
	d.refreshPreview()
	d.SetFocusedItem(d.preview)
}

func (d *Dialog) restoreNormalView() {
	d.undoMode = false
	d.dir = d.fs.GetPath()
	d.SetTitle(" " + tr("VisRen.Title", "Visual File Renamer") + " ")
	d.setControlsDisabled(false)
	d.refreshPreview()
	d.SetFocusedItem(d.nameEdit)
}

func (d *Dialog) onRename() {
	if d.undoMode {
		confirm := vtui.ShowMessageOn(d, " "+tr("VisRen.Undo", "Undo")+" ", tr("VisRen.UndoQuestion", "Undo the recorded rename operation?"), []string{"&Yes", "&No", "Cancel"})
		// Destructive — renames files back — render on the WarnDialog
		// palette so the confirmation reads as an alarm. See #379.
		confirm.IsWarning = true
		confirm.OnResult = func(choice int) {
			switch choice {
			case 0:
				d.runUndo()
			case 1:
				d.restoreNormalView()
			}
		}
		return
	}
	d.saveHistories()
	d.runRows(append([]Preview(nil), d.rows...))
}

func (d *Dialog) saveHistories() {
	for _, edit := range []*vtui.Edit{d.nameEdit, d.extEdit, d.searchEdit, d.replaceEdit} {
		if edit.HistoryID != "" && edit.History == nil && vtui.GlobalHistoryProvider != nil {
			edit.History = vtui.GlobalHistoryProvider.LoadHistory(edit.HistoryID)
		}
		edit.AddHistory(edit.GetText())
	}
}

func (d *Dialog) runRows(rows []Preview) {
	d.Close()
	var result RenameResult
	var mu sync.Mutex
	d.host.RunProgressTask(tr("VisRen.Title", "Visual File Renamer"), tr("VisRen.Renaming", "Renaming files..."), false, func(ctx context.Context, update func(string, int)) error {
		wrapped := make([]Preview, len(rows))
		copy(wrapped, rows)
		result = ExecuteRenames(ctx, d.fs, d.dir, wrapped, func(source, destination string, err error) ErrorAction {
			return errorHandler(d.host)(source, destination, err)
		})
		mu.Lock()
		defer mu.Unlock()
		update(tr("VisRen.Done", "Done"), 100)
		return nil
	}, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if d.logging {
			d.plugin.setUndo(d.dir, result.Succeeded)
		} else {
			d.plugin.setUndo("", nil)
		}
		d.host.ReplaceMarkedNames(result.Pending)
		d.host.RefreshAll()
		if len(result.Succeeded) > 0 {
			vtui.ShowMessage(" "+tr("VisRen.Title", "Visual File Renamer")+" ", fmt.Sprintf(tr("VisRen.Summary", "Renamed %d of %d item(s)."), len(result.Succeeded), len(rows)), []string{"&Ok"})
		}
	})
}

func (d *Dialog) runUndo() {
	d.Close()
	dir, log := d.plugin.getUndo()
	_ = d.fs.SetPath(dir)
	var result RenameResult
	d.host.RunProgressTask(tr("VisRen.Undo", "Undo"), tr("VisRen.Undoing", "Undoing rename..."), false, func(ctx context.Context, update func(string, int)) error {
		result = ExecuteUndo(ctx, d.fs, dir, log, errorHandler(d.host))
		update(tr("VisRen.Done", "Done"), 100)
		return nil
	}, func(error) {
		d.plugin.setUndo("", nil)
		d.host.ReplaceMarkedNames(result.Pending)
		d.host.RefreshAll()
	})
}

func (d *Dialog) showDetails() {
	if len(d.rows) == 0 || d.preview.cursor >= len(d.rows) {
		return
	}
	row := d.rows[d.preview.cursor]
	destination := row.Destination
	if row.Err != nil {
		destination = row.Err.Error()
	}
	details := vtui.NewCenteredDialog(71, 15, " "+tr("VisRen.FullName", "Full name")+" ")
	oldBox := vtui.NewGroupBox(details.X1+2, details.Y1+1, details.X1+68, details.Y1+6, " "+tr("VisRen.OldName", "Old name")+" ")
	newBox := vtui.NewGroupBox(details.X1+2, details.Y1+7, details.X1+68, details.Y1+12, " "+tr("VisRen.NewName", "New name")+" ")
	details.AddItem(oldBox)
	details.AddItem(newBox)
	for idx, line := range fixedLines(row.Item.Source, 65, 4) {
		text := vtui.NewText(details.X1+3, details.Y1+2+idx, line, vtui.Palette[vtui.ColDialogText])
		details.AddItem(text)
	}
	for idx, line := range fixedLines(destination, 65, 4) {
		text := vtui.NewText(details.X1+3, details.Y1+8+idx, line, vtui.Palette[vtui.ColDialogText])
		details.AddItem(text)
	}
	ok := vtui.NewButton(details.X1+33, details.Y1+13, vtui.Msg("vtui.Ok"))
	ok.IsDefault = true
	ok.OnClick = details.Close
	details.AddItem(ok)
	details.SetFocusedItem(ok)
	vtui.FrameManager.Push(details)
}

func fixedLines(value string, width, count int) []string {
	runes := []rune(value)
	if len(runes) > width*count {
		runes = runes[:width*count]
	}
	lines := make([]string, count)
	for idx := 0; idx < count && len(runes) > 0; idx++ {
		end := minInt(width, len(runes))
		lines[idx] = string(runes[:end])
		runes = runes[end:]
	}
	return lines
}

func (d *Dialog) editWordDiv() {
	d.host.InputBox(tr("VisRen.WordDivTitle", "Word delimiters"), tr("VisRen.WordDivPrompt", "Characters separating words (maximum 18):"), d.wordDiv, func(value string) {
		runes := []rune(value)
		if len(runes) > 18 {
			runes = runes[:18]
		}
		if len(runes) == 0 {
			return
		}
		d.wordDiv = string(runes)
		if err := saveConfig(config{WordDiv: d.wordDiv}); err != nil {
			vtui.ShowMessageOn(d, " "+tr("VisRen.WordDivTitle", "Word delimiters")+" ", err.Error(), []string{"&Ok"})
		}
		d.refreshPreview()
	})
}

func (d *Dialog) selectedTemplateIsTitle() bool {
	if d.GetFocusedItem() == d.nameCombo {
		return d.nameCombo.Menu.SelectPos == 7 || d.nameCombo.Edit.GetText() == "[T]"
	}
	if d.GetFocusedItem() == d.extCombo {
		return d.extCombo.Menu.SelectPos == 7 || d.extCombo.Edit.GetText() == "[T]"
	}
	return false
}

func (d *Dialog) clearUndoLog() {
	_, log := d.plugin.getUndo()
	if len(log) == 0 {
		return
	}
	confirm := vtui.ShowMessageOn(d, " "+tr("VisRen.Undo", "Undo")+" ", tr("VisRen.ClearLog", "Clear the rename log?"), []string{"&Yes", "&No"})
	// Destructive — wipes the undo history, taking away the ability to
	// undo the last rename. Render on the WarnDialog palette. See #379.
	confirm.IsWarning = true
	confirm.OnResult = func(code int) {
		if code == 0 {
			d.plugin.setUndo("", nil)
			d.updateLogLine()
			if d.undoMode {
				d.restoreNormalView()
			}
		}
	}
}

func (d *Dialog) askEditorColumns() {
	if d.editorButton.IsDisabled() {
		return
	}
	d.openEditor(loadConfig().EditorFormat == editorFormatSourceTarget, nil, 0)
}

func editorColumnChoices() []string {
	return []string{
		tr("VisRen.EditorSourceTarget", "&Source + target"),
		tr("VisRen.EditorTargetsOnly", "Targets &only"),
		tr("VisRen.Cancel", "Cancel"),
	}
}

func (d *Dialog) openEditor(twoColumns bool, content []byte, cursor int) {
	rows := append([]Preview(nil), d.rows...)
	if content == nil {
		content = renderEditorList(rows, twoColumns)
	}
	original := append([]byte(nil), content...)
	d.Close()
	err := d.host.OpenVisRenEditor(EditorRequest{Title: tr("VisRen.EditorTitle", "Rename list of files"), Content: content, CursorLine: cursor, CursorCol: 0, OnClose: func(data []byte, err error) {
		if err != nil {
			vtui.ShowMessage(" "+tr("VisRen.Editor", "In editor")+" ", err.Error(), []string{"&Ok"})
			return
		}
		destinations, badLine, parseErr := parseEditorList(data, rows, twoColumns)
		if parseErr != nil {
			msg := vtui.ShowMessage(" "+tr("VisRen.Editor", "In editor")+" ", parseErr.Error(), []string{"&Edit again", "Cancel"})
			msg.OnResult = func(code int) {
				if code == 0 {
					d.openEditor(twoColumns, data, badLine)
				}
			}
			return
		}
		for idx := range rows {
			rows[idx].Destination = destinations[idx]
			rows[idx].Err = nil
		}
		if string(data) == string(original) {
			changed := false
			for _, row := range rows {
				changed = changed || row.Item.Source != row.Destination
			}
			if changed {
				confirm := vtui.ShowMessage(" "+tr("VisRen.Title", "Visual File Renamer")+" ", tr("VisRen.RenameQuestion", "Rename files using this list?"), []string{"&Yes", "&No"})
				confirm.OnResult = func(code int) {
					if code == 0 {
						d.runRows(rows)
					}
				}
			}
			return
		}
		d.runRows(rows)
	}})
	if err != nil {
		vtui.ShowMessage(" "+tr("VisRen.Editor", "In editor")+" ", err.Error(), []string{"&Ok"})
	}
}

func (d *Dialog) navigateToCurrent() {
	if len(d.rows) == 0 || d.preview.cursor >= len(d.rows) {
		return
	}
	d.host.SetPendingSelection(d.rows[d.preview.cursor].Item.Source)
	d.Close()
	d.host.RefreshAll()
}

func (d *Dialog) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return d.Window.ProcessKey(e)
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	if alt && !ctrl && e.Char != 0 {
		key := unicode.ToLower(e.Char)
		translated := unicode.ToLower(vtui.GlobalXlator.Translate(e.Char))
		for idx, target := range []vtui.UIElement{d.nameEdit, d.extEdit, d.searchEdit, d.replaceEdit, d.nameCombo, d.extCombo} {
			hotkey := d.separators[idx].GetHotkey()
			if hotkey != 0 && (hotkey == key || hotkey == translated) {
				if !target.IsDisabled() {
					d.SetFocusedItem(target)
				}
				return true
			}
		}
	}
	if ctrl && alt && e.VirtualKeyCode == vtinput.VK_F {
		return true
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_F1:
		if vtui.GlobalHelpEngine != nil {
			vtui.FrameManager.Push(newVisRenHelpView(d.lastW, d.lastH))
		}
		return true
	case vtinput.VK_F2:
		d.logging = !d.logging
		d.updateLogLine()
		vtui.FrameManager.Redraw()
		return true
	case vtinput.VK_F3:
		d.showDetails()
		return true
	case vtinput.VK_F4:
		if d.selectedTemplateIsTitle() {
			d.editWordDiv()
			return true
		}
		d.askEditorColumns()
		return true
	case vtinput.VK_F5:
		d.maximized = !d.maximized
		d.ResizeConsole(d.lastW, d.lastH)
		return true
	case vtinput.VK_F6:
		d.loadUndoView()
		return true
	case vtinput.VK_F8:
		d.clearUndoLog()
		return true
	case vtinput.VK_F12:
		if d.GetFocusedItem() == d.preview {
			d.SetFocusedItem(d.nameEdit)
		} else {
			d.SetFocusedItem(d.preview)
		}
		return true
	case vtinput.VK_DELETE:
		if d.GetFocusedItem() == d.preview && !d.undoMode && len(d.engine.Items) > 0 {
			idx := d.preview.cursor
			d.engine.Items = append(d.engine.Items[:idx], d.engine.Items[idx+1:]...)
			d.refreshPreview()
			return true
		}
	case vtinput.VK_UP, vtinput.VK_DOWN:
		if ctrl && d.GetFocusedItem() == d.preview && !d.undoMode && len(d.engine.Items) > 1 {
			from := d.preview.cursor
			to := from - 1
			if e.VirtualKeyCode == vtinput.VK_DOWN {
				to = from + 1
			}
			if to >= 0 && to < len(d.engine.Items) {
				d.engine.Items[from], d.engine.Items[to] = d.engine.Items[to], d.engine.Items[from]
				d.preview.cursor = to
				d.refreshPreview()
			}
			return true
		}
	case vtinput.VK_LEFT, vtinput.VK_RIGHT:
		if d.GetFocusedItem() == d.preview {
			delta := -1
			if e.VirtualKeyCode == vtinput.VK_RIGHT {
				delta = 1
			}
			if ctrl {
				d.preview.divider = maxInt(5, minInt(d.preview.X2-d.preview.X1-5, d.preview.divider+delta))
			} else if alt {
				d.preview.srcOffset = maxInt(0, d.preview.srcOffset+delta)
			} else {
				d.preview.dstOffset = maxInt(0, d.preview.dstOffset+delta)
			}
			vtui.FrameManager.Redraw()
			return true
		}
	case vtinput.VK_NUMPAD5:
		if ctrl && d.GetFocusedItem() == d.preview {
			d.preview.divider = (d.preview.X2 - d.preview.X1 + 1) / 2
			d.preview.srcOffset, d.preview.dstOffset = 0, 0
			vtui.FrameManager.Redraw()
			return true
		}
	case vtinput.VK_PRIOR:
		if ctrl && d.GetFocusedItem() == d.preview {
			d.navigateToCurrent()
			return true
		}
	case vtinput.VK_INSERT:
		if d.GetFocusedItem() == d.nameCombo {
			d.insertSelectedTemplate(d.nameCombo, d.nameEdit, nameTemplates, false)
			return true
		}
		if d.GetFocusedItem() == d.extCombo {
			d.insertSelectedTemplate(d.extCombo, d.extEdit, extTemplates, true)
			return true
		}
	}
	return d.Window.ProcessKey(e)
}

func (d *Dialog) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.KeyDown && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && int(e.MouseY) == d.Y1 && int(e.MouseX) >= d.X2-4 && int(e.MouseX) <= d.X2-2 {
		d.maximized = !d.maximized
		d.ResizeConsole(d.lastW, d.lastH)
		return true
	}
	if e.KeyDown && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && int(e.MouseY) == d.logLine.Y1 {
		if int(e.MouseX) == d.logMarkX {
			d.logging = !d.logging
			d.updateLogLine()
			vtui.FrameManager.Redraw()
		} else {
			d.clearUndoLog()
		}
		return true
	}
	if d.preview.HitTest(int(e.MouseX), int(e.MouseY)) {
		idx := d.preview.top + int(e.MouseY) - d.preview.Y1
		if idx >= 0 && idx < len(d.rows) {
			if e.MouseEventFlags&vtinput.DoubleClick != 0 && e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 {
				d.preview.cursor = idx
				d.navigateToCurrent()
				return true
			}
			if e.ButtonState&vtinput.RightmostButtonPressed != 0 && e.KeyDown {
				d.preview.cursor = idx
				d.showDetails()
				return true
			}
			if e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && e.KeyDown {
				if e.MouseEventFlags&vtinput.MouseMoved != 0 && d.dragRow >= 0 && !d.undoMode && d.dragRow != idx {
					item := d.engine.Items[d.dragRow]
					d.engine.Items = append(d.engine.Items[:d.dragRow], d.engine.Items[d.dragRow+1:]...)
					d.engine.Items = append(d.engine.Items, nil)
					copy(d.engine.Items[idx+1:], d.engine.Items[idx:])
					d.engine.Items[idx] = item
					d.dragRow = idx
					d.preview.cursor = idx
					d.refreshPreview()
					return true
				}
				d.dragRow = idx
			}
			if e.ButtonState == 0 {
				d.dragRow = -1
			}
		}
	}
	return d.Window.ProcessMouse(e)
}

func (d *Dialog) ResizeConsole(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	d.lastW, d.lastH = w, h
	targetW, targetH := minInt(78, maxInt(20, w-2)), minInt(23, maxInt(12, h-2))
	x, y := (w-targetW)/2, (h-targetH)/2
	if d.maximized {
		targetW, targetH, x, y = maxInt(20, w-2), maxInt(12, h-2), 1, 1
	}
	d.ChangeSize(targetW, targetH)
	d.MoveRelative(x-d.X1, y-d.Y1)
	d.layout()
	vtui.FrameManager.Redraw()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
