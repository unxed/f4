package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type settingsRow struct {
	field              f4settings.Field
	session            *settingsSession
	control            vtui.UIElement
	label              []string
	y, height          int
	heading            bool
	match              bool
	controlHeight      int
	controlX, controlY int
	controlWidth       int
	gap                int
	read               func() string
	write              func(string)
	values             func() map[string]string
	matchFunc          func() bool
	unavailableReason  string
}

// Keep the familiar checkbox label on the control, wrapping only when needed.
type settingsCheckbox struct {
	*vtui.Checkbox
	lines []string
}

func (b *settingsCheckbox) Show(scr *vtui.ScreenBuf) {
	b.Checkbox.Show(scr)
	n, _ := b.GetStateAttrs(vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton)
	for i := 1; i < len(b.lines); i++ {
		scr.Write(b.X1+4, b.Y1+i, vtui.StringToCharInfo(b.lines[i], n))
	}
}

// settingsViewport clips only the page, leaving the window chrome fixed.
// It remains a Group/FocusContainer so native focus and UI inspection work.
type settingsViewport struct {
	fullPage vtui.UIElement
	*vtui.Group
	rows          []*settingsRow
	scroll, total int
	bar           *vtui.ScrollBar
	onFocus       func(*settingsRow)
	boxes         []settingsGroupBox
	groupLabel    func(string) string
}

type settingsGroupBox struct {
	title       string
	top, bottom int
	rows        []*settingsRow
}

func newSettingsViewport() *settingsViewport {
	v := &settingsViewport{Group: vtui.NewGroup(0, 0, 1, 1), bar: vtui.NewScrollBar(0, 0, 1)}
	v.bar.ColorIdx = vtui.ColDialogBox
	v.bar.OnScroll = func(n int) { v.scroll = n; v.positionRows() }
	return v
}
func (v *settingsViewport) SetPosition(x1, y1, x2, y2 int) {
	v.Group.SetPosition(x1, y1, x2, y2)
	if v.fullPage != nil {
		v.fullPage.SetPosition(x1, y1, x2, y2)
		v.total = 0
		v.scroll = 0
		v.boxes = nil
		return
	}
	v.bar.SetPosition(x2, y1, x2, y2)
	v.total = 0
	v.boxes = nil
	closeBox := func() {
		box := &v.boxes[len(v.boxes)-1]
		if len(box.rows) > 0 {
			last := box.rows[len(box.rows)-1]
			if !last.heading {
				last.height -= last.gap
				v.total -= last.gap
				last.gap = 0
			}
		}
		box.bottom = v.total
	}
	group := ""
	for _, r := range v.rows {
		if len(v.boxes) == 0 || group != r.field.Group {
			if len(v.boxes) > 0 {
				closeBox()
				v.total += 2
			}
			group = r.field.Group
			title := group
			if v.groupLabel != nil {
				title = v.groupLabel(group)
			}
			if r.heading {
				title = r.field.Label.Resolve(config.App.Language, i18n.Msg)
			}
			v.boxes = append(v.boxes, settingsGroupBox{title: title, top: v.total})
			v.total++
		}
		box := &v.boxes[len(v.boxes)-1]
		box.rows = append(box.rows, r)
		if r.heading {
			r.y, r.height = box.top, 1
			continue
		}
		r.label = settingsWrap(r.field.Label.Resolve(config.App.Language, i18n.Msg), max(1, x2-x1-5))
		r.controlX, r.gap = 0, 1
		r.controlWidth = 0
		// Action controls already name themselves; keep their metadata for
		// search and explanations without repeating it above the control.
		switch r.control.(type) {
		case *vtui.Button, *settingsButtonRow:
			r.label = nil
			r.gap = 0
		case *vtui.ComboBox:
			width := max(1, x2-x1-5)
			label := r.field.Label.Resolve(config.App.Language, i18n.Msg)
			labelWidth := min(vtui.StringWidth(label), max(1, width/2))
			r.label = settingsWrap(label, labelWidth)
			r.controlX = labelWidth + 1
			r.gap = 0
		case *settingsRadios:
			radios := r.control.(*settingsRadios)
			width := max(1, x2-x1-5)
			label := r.field.Label.Resolve(config.App.Language, i18n.Msg)
			labelWidth := vtui.StringWidth(label)
			if labelWidth+1+radios.inlineWidth() <= width {
				r.label = []string{label}
				r.controlX = labelWidth + 1
			}
			r.controlHeight = radios.layout(width - r.controlX)
			r.gap = 0
		case *vtui.Table:
			if r.field.Label.Resolve(config.App.Language, i18n.Msg) == box.title {
				r.label = nil
			}
		case *settingsEdit:
			width := r.field.InputWidth
			if width == 0 && r.field.Kind == f4settings.Integer {
				width = 10
			}
			if width > 0 {
				available := max(3, x2-x1-5)
				r.controlWidth = min(width, max(1, available/2))
				label := r.field.Label.Resolve(config.App.Language, i18n.Msg)
				labelWidth := min(vtui.StringWidth(label), max(1, available-r.controlWidth-1))
				r.label = settingsWrap(label, labelWidth)
				r.controlX = labelWidth + 1
				r.gap = 0
			}
		}
		if b, ok := r.control.(*settingsCheckbox); ok {
			b.lines = settingsWrap(r.field.Label.Resolve(config.App.Language, i18n.Msg), max(1, x2-x1-9))
			b.SetText(b.lines[0])
			r.label = nil
			r.controlHeight = len(b.lines)
			r.gap = 0
		}
		r.y = v.total
		r.controlY = len(r.label)
		if r.controlX > 0 {
			r.controlY = 0
		}
		r.height = len(r.label)
		if r.control != nil {
			r.height = max(r.height, r.controlY+max(1, r.controlHeight))
		}
		r.height += r.gap
		v.total += r.height
	}
	if len(v.boxes) > 0 {
		closeBox()
		v.total++
	}
	v.scroll = min(v.scroll, max(0, v.total-(y2-y1+1)))
	v.positionRows()
}
func (v *settingsViewport) positionRows() {
	for _, r := range v.rows {
		if r.control != nil {
			y := v.Y1 + r.y + r.controlY - v.scroll
			x := v.X1 + 2 + r.controlX
			right := v.X2 - 4
			if r.controlWidth > 0 {
				right = min(right, x+r.controlWidth-1)
			}
			r.control.SetPosition(x, y, right, y+max(1, r.controlHeight)-1)
		}
	}
	v.bar.PgStep = max(1, v.Y2-v.Y1+1)
	v.bar.SetParams(v.scroll, 0, max(0, v.total-v.bar.PgStep))
}
func (v *settingsViewport) Show(scr *vtui.ScreenBuf) {
	if v.fullPage != nil {
		v.Group.Show(scr)
		return
	}
	v.ScreenObject.Show(scr)
	scr.PushClipRect(v.X1, v.Y1, v.X2, v.Y2)
	defer scr.PopClipRect()
	scr.FillRect(v.X1, v.Y1, v.X2, v.Y2, ' ', vtui.Palette[vtui.ColDialogText])
	painter := vtui.NewPainter(scr)
	for _, box := range v.boxes {
		matched := false
		for _, row := range box.rows {
			matched = matched || row.match
		}
		border, title := vtui.Palette[vtui.ColDialogBox], vtui.Palette[vtui.ColDialogBoxTitle]
		if !matched {
			border, title = vtui.DimColor(border), vtui.DimColor(title)
		}
		top, bottom := v.Y1+box.top-v.scroll, v.Y1+box.bottom-v.scroll
		painter.DrawBox(v.X1, top, v.X2-2, bottom, border, vtui.SingleBox)
		painter.DrawTitle(v.X1, top, v.X2-2, vtui.TruncateString(box.title, max(1, v.X2-v.X1-5), "…"), title)
	}
	for _, r := range v.rows {
		if r.control != nil {
			r.control.SetFocus(v.IsFocused() && v.GetFocusedItem() == r.control)
		}
		if r.heading {
			continue
		}
		y := v.Y1 + r.y - v.scroll
		if y+r.height <= v.Y1 || y > v.Y2 {
			continue
		}
		attr := vtui.Palette[vtui.ColDialogText]
		if r.heading {
			attr = vtui.Palette[vtui.ColDialogHighlightText]
		}
		if !r.match {
			attr = vtui.DimColor(attr)
		}
		for j, line := range r.label {
			scr.Write(v.X1+2, y+j, vtui.StringToCharInfo(line, attr))
		}
		if r.control != nil {
			r.control.Show(scr)
			if !r.match {
				x1, y1, x2, y2 := r.control.GetPosition()
				settingsDimRect(scr, x1, max(v.Y1, y1), x2, min(v.Y2, y2))
			}
		}
	}
	if v.total > v.Y2-v.Y1+1 {
		v.bar.Show(scr)
	}
}
func settingsDimRect(scr *vtui.ScreenBuf, x1, y1, x2, y2 int) {
	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			c := scr.GetCell(x, y)
			c.Attributes = vtui.DimColor(c.Attributes)
			scr.Write(x, y, []vtui.CharInfo{c})
		}
	}
}
func (v *settingsViewport) notifyFocus() {
	focused := v.GetFocusedItem()
	for _, r := range v.rows {
		if r.control == focused {
			top, bottom := r.y, r.y+r.height
			if radios, ok := r.control.(*settingsRadios); ok {
				if button, ok := radios.GetFocusedItem().(*settingsRadioOption); ok {
					top = r.y + r.controlY + button.dy
					bottom = top + len(button.lines)
				}
			}
			if top < v.scroll {
				v.scroll = top
			}
			if bottom > v.scroll+v.Y2-v.Y1+1 {
				v.scroll = bottom - (v.Y2 - v.Y1 + 1)
			}
			v.positionRows()
			if v.onFocus != nil {
				v.onFocus(r)
			}
			break
		}
	}
}
func (v *settingsViewport) ProcessKey(e *vtinput.InputEvent) bool {
	// Embedded pages own navigation; wrapping the single child can re-enter
	// that same group while it is preparing a backwards focus transition.
	if v.fullPage != nil {
		return v.fullPage.ProcessKey(e)
	}
	v.WrapFocus = true
	handled := v.Group.ProcessKey(e)
	v.notifyFocus()
	if e.KeyDown && (e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_DOWN || e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) {
		return true
	}
	return handled
}
func (v *settingsViewport) ProcessMouse(e *vtinput.InputEvent) bool {
	if v.fullPage != nil {
		return v.Group.ProcessMouse(e)
	}
	if v.bar.IsMouseCaptured() {
		v.bar.ProcessMouse(e)
		return true
	}
	if v.IsMouseCaptured() {
		return v.Group.ProcessMouse(e)
	}
	if !v.HitTest(int(e.MouseX), int(e.MouseY)) {
		return false
	}
	if e.WheelDirection != 0 {
		delta := e.WheelDirection
		step := 3
		if delta > 0 {
			step = -step
		}
		v.scroll = max(0, min(v.bar.Max, v.scroll+step))
		v.positionRows()
		return true
	}
	if int(e.MouseX) == v.X2 && v.bar.ProcessMouse(e) {
		return true
	}
	handled := v.Group.ProcessMouse(e)
	for _, r := range v.rows {
		if int(e.MouseY) >= v.Y1+r.y-v.scroll && int(e.MouseY) < v.Y1+r.y+r.height-v.scroll && v.onFocus != nil {
			v.onFocus(r)
			break
		}
	}
	return handled
}

type settingsHelp struct {
	*vtui.Group
	text string
	top  int
	bar  *vtui.ScrollBar
}

func newSettingsHelp() *settingsHelp {
	h := &settingsHelp{Group: vtui.NewGroup(0, 0, 1, 1), bar: vtui.NewScrollBar(0, 0, 1)}
	h.bar.ColorIdx = vtui.ColDialogBox
	h.bar.OnScroll = func(n int) { h.top = n }
	return h
}
func (h *settingsHelp) CanFocus() bool { return !h.IsDisabled() }
func (h *settingsHelp) Show(scr *vtui.ScreenBuf) {
	if h.IsDisabled() {
		return
	}
	h.ScreenObject.Show(scr)
	scr.PushClipRect(h.X1, h.Y1, h.X2, h.Y2)
	defer scr.PopClipRect()
	scr.FillRect(h.X1, h.Y1, h.X2, h.Y2, ' ', vtui.Palette[vtui.ColDialogText])
	lines := settingsWrap(h.text, max(1, h.X2-h.X1))
	height := h.Y2 - h.Y1 + 1
	h.top = min(h.top, max(0, len(lines)-height))
	for i := h.top; i < len(lines) && i-h.top < height; i++ {
		scr.Write(h.X1, h.Y1+i-h.top, vtui.StringToCharInfo(lines[i], vtui.Palette[vtui.ColDialogText]))
	}
	h.bar.SetPosition(h.X2, h.Y1, h.X2, h.Y2)
	h.bar.PgStep = height
	h.bar.SetParams(h.top, 0, max(0, len(lines)-height))
	if len(lines) > height {
		h.bar.Show(scr)
	}
}
func (h *settingsHelp) ProcessKey(e *vtinput.InputEvent) bool {
	if h.IsDisabled() {
		return false
	}
	if !e.KeyDown {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_DOWN:
		h.top++
	case vtinput.VK_UP:
		h.top = max(0, h.top-1)
	case vtinput.VK_NEXT:
		h.top += max(1, h.Y2-h.Y1)
	case vtinput.VK_PRIOR:
		h.top = max(0, h.top-(h.Y2-h.Y1))
	default:
		return false
	}
	return true
}
func (h *settingsHelp) ProcessMouse(e *vtinput.InputEvent) bool {
	if h.IsDisabled() {
		return false
	}
	if h.bar.IsMouseCaptured() {
		h.bar.ProcessMouse(e)
		return true
	}
	if !h.HitTest(int(e.MouseX), int(e.MouseY)) {
		return false
	}
	if e.WheelDirection != 0 {
		if e.WheelDirection > 0 {
			h.top = max(0, h.top-3)
		} else {
			h.top += 3
		}
		return true
	}
	return h.bar.ProcessMouse(e)
}

func settingsWrap(text string, width int) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for vtui.StringWidth(word) > width {
				if line != "" {
					lines = append(lines, line)
					line = ""
				}
				part := ""
				for _, r := range word {
					if vtui.StringWidth(part+string(r)) > width {
						break
					}
					part += string(r)
				}
				if part == "" {
					part = string([]rune(word)[0])
				}
				lines = append(lines, part)
				word = strings.TrimPrefix(word, part)
			}
			if line != "" && vtui.StringWidth(line+" "+word) > width {
				lines = append(lines, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		lines = append(lines, line)
	}
	return lines
}

type settingsCategoryRow struct {
	center   *settingsCenter
	category f4settings.Category
}

func (r settingsCategoryRow) GetCellText(int) string {
	label := r.category.Label.Resolve(config.App.Language, i18n.Msg)
	if strings.TrimSpace(r.center.query) != "" {
		label += fmt.Sprintf(" (%d)", r.center.categoryMatches(r.category.ID))
	}
	return label
}
func (r settingsCategoryRow) GetCellAttr(_ int, attr uint64) uint64 {
	inactiveCursor := r.center.category == r.category.ID && !r.center.sidebar.IsFocused()
	if inactiveCursor {
		attr = settingsInactiveCategoryAttr(vtui.Palette[vtui.ColDialogText])
	}
	if strings.TrimSpace(r.center.query) != "" && r.center.categoryMatches(r.category.ID) == 0 {
		if r.center.category == r.category.ID {
			// Keep the same dark text on both active and inactive cursor surfaces.
			_, background := theme.GetColorRGBBoth(settingsInactiveCategoryAttr(vtui.Palette[vtui.ColDialogText]))
			dark := ((background>>16&255)/4)<<16 | ((background>>8&255)/4)<<8 | (background&255)/4
			return vtui.SetRGBFore(attr, dark)
		}
		return vtui.DimColor(attr)
	}
	return attr
}

// Match Environment Manager's neutral, theme-derived inactive cursor.
func settingsInactiveCategoryAttr(attr uint64) uint64 {
	if attr&(vtui.IsFgRGB|vtui.IsBgRGB) != vtui.IsFgRGB|vtui.IsBgRGB {
		return attr ^ vtui.BackgroundIntensity
	}
	gray := func(rgb uint32) uint32 {
		return (((rgb>>16)&0xff)*299 + ((rgb>>8)&0xff)*587 + (rgb&0xff)*114) / 1000
	}
	shade := (gray(vtui.GetRGBBack(attr))*3 + gray(vtui.GetRGBFore(attr))) / 4
	return vtui.SetRGBBack(attr, shade<<16|shade<<8|shade)
}

var lastSettingsCategory string
var lastSettingsOffsets = map[string]int{}

type settingsCenter struct {
	hotkeyPage                            vtui.UIElement
	searchCacheQuery, searchCacheLanguage string
	categoryMatchCache                    map[string]int
	recordMatchCache                      map[settingsRecordMatchKey]bool

	*vtui.Window
	sessions                []*settingsSession
	categories              []f4settings.Category
	sidebar                 *vtui.Table
	search                  *vtui.Edit
	page                    *settingsViewport
	help                    *settingsHelp
	apply, ok, cancel       *vtui.Button
	previous, next          *settingsSearchButton
	clearSearch             *settingsClearButton
	category, query, status string
	choiceHelpRow           *settingsRow
	offsets                 map[string]int
	closed                  bool
	running                 *vtui.TaskContext
	closePending            bool
	screenW, screenH        int
	positioned              bool
	resizing                bool
	layoutBounds            [4]int
}

func settingsDialogTable(t *vtui.Table) {
	t.ColorTextIdx = vtui.ColDialogText
	t.ColorSelectedTextIdx = vtui.ColDialogSelectedButton
	t.ColorItemSelectTextIdx = vtui.ColDialogHighlightText
	t.ColorItemSelectCursorIdx = vtui.ColDialogHighlightSelectedButton
	t.ColorTitleIdx = vtui.ColDialogHighlightText
	t.ColorBoxIdx = vtui.ColDialogBox
	t.ColorHighlightIdx = vtui.ColDialogHighlightText
	t.ScrollBar.ColorIdx = vtui.ColDialogBox
	t.ShowScrollBar = true
	t.QuickSearch = false
}

func newSettingsCenter(sessions []*settingsSession) *settingsCenter {
	c := &settingsCenter{Window: vtui.NewDialog(0, 0, 79, 24, settingsText("Title", "Settings")), sessions: sessions, offsets: map[string]int{}}
	for k, v := range lastSettingsOffsets {
		c.offsets[k] = v
	}
	c.ShowClose = true
	c.ShowZoom = true
	c.SetId("settings-center")
	c.SetHelp("SettingsCenter")
	seen := map[string]bool{}
	for _, s := range sessions {
		for _, cat := range s.catalog.Categories {
			if !seen[cat.ID] {
				c.categories = append(c.categories, cat)
				seen[cat.ID] = true
			}
		}
	}
	c.search = vtui.NewEdit(0, 0, 30, "")
	c.search.SetId("settings-search")
	c.search.OnTextChange = func(s string) {
		c.query = s
		c.updateMatches()
		c.status = ""
		c.layoutWindow()
	}
	c.sidebar = vtui.NewTable(0, 0, 20, 10, []vtui.TableColumn{{Width: 0}})
	c.sidebar.ShowHeader = false
	c.sidebar.ShowSeparators = false
	settingsDialogTable(c.sidebar)
	c.sidebar.SetId("settings-categories")
	var rows []vtui.TableRow
	for _, cat := range c.categories {
		rows = append(rows, settingsCategoryRow{c, cat})
	}
	c.sidebar.SetRows(rows)
	c.page = newSettingsViewport()
	c.page.groupLabel = c.groupLabel
	c.page.SetId("settings-page")
	c.page.onFocus = c.describe
	c.help = newSettingsHelp()
	c.help.SetId("settings-description")
	c.apply = vtui.NewButton(0, 0, settingsText("Apply", "&Apply"))
	c.ok = vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	c.cancel = vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	c.previous = &settingsSearchButton{vtui.NewButton(0, 0, settingsText("Previous", "Previous match"))}
	c.next = &settingsSearchButton{vtui.NewButton(0, 0, settingsText("Next", "Next match"))}
	c.previous.ScreenObject.SetText("[←]")
	c.next.ScreenObject.SetText("[→]")
	c.clearSearch = &settingsClearButton{vtui.NewButton(0, 0, settingsText("ClearSearch", "Clear search"))}
	c.clearSearch.SetId("settings-search-clear")
	c.clearSearch.OnClick = func() {
		c.search.SetText("")
		c.search.OnTextChange("")
		c.SetFocusedItem(c.search)
	}

	c.apply.OnClick = func() { c.commit(false) }
	c.ok.OnClick = func() { c.commit(true) }
	c.cancel.OnClick = func() { c.Close() }
	c.previous.OnClick = func() { c.nextMatch(-1) }
	c.next.OnClick = func() { c.nextMatch(1) }
	for _, el := range []vtui.UIElement{c.search, c.sidebar, c.page, c.help, c.clearSearch, c.previous, c.next, c.apply, c.ok, c.cancel} {
		c.AddItem(el)
	}
	c.sidebar.OnSelect = func(i int) {
		if i >= 0 && i < len(c.categories) {
			c.selectCategory(c.categories[i].ID)
		}
	}
	c.OnResult = func(int) {
		if !c.closed {
			c.closed = true
			lastSettingsCategory = c.category
			c.offsets[c.category] = c.page.scroll
			lastSettingsOffsets = map[string]int{}
			for k, v := range c.offsets {
				lastSettingsOffsets[k] = v
			}
			for _, s := range c.sessions {
				if s.contributed && !settingsProviderAlive(s.provider) {
					s.draft.CloseFunc = nil
				}
				s.draft.Close()
			}
		}
	}
	if len(c.categories) > 0 {
		c.selectCategory(c.categories[0].ID)
	}
	c.layoutWindow()
	c.MinW, c.MinH = 72, 22
	c.SetFocusedItem(c.search)
	return c
}

func (c *settingsCenter) ResizeConsole(w, h int) {
	c.screenW, c.screenH = max(1, w), max(1, h)
	if !c.positioned {
		dw, dh := min(w, max(72, w/2)), min(h, max(22, h*3/4))
		c.SetPosition((w-dw)/2, (h-dh)/2, (w+dw)/2-1, (h+dh)/2-1)
		c.positioned = true
	} else if c.SavedBounds != nil {
		c.SetPosition(0, 0, w-1, h-1)
	} else {
		c.fitBounds()
	}
}

func (c *settingsCenter) SetPosition(x1, y1, x2, y2 int) {
	c.Window.SetPosition(x1, y1, x2, y2)
	if c.search != nil {
		c.layoutWindow()
	}
}

func (c *settingsCenter) fitBounds() {
	w, h := min(c.screenW, c.X2-c.X1+1), min(c.screenH, c.Y2-c.Y1+1)
	x, y := max(0, min(c.X1, c.screenW-w)), max(0, min(c.Y1, c.screenH-h))
	c.SetPosition(x, y, x+w-1, y+h-1)
}

func (c *settingsCenter) layoutWindow() {
	c.layoutBounds = [4]int{c.X1, c.Y1, c.X2, c.Y2}
	w, h := c.X2-c.X1+1, c.Y2-c.Y1+1
	x0, y0 := c.X1, c.Y1
	side := c.categorySidebarWidth() + 1
	bottom := c.contentBottom()
	c.sidebar.SetPosition(x0+2, y0+4, x0+side, bottom)
	c.layoutSearch()
	px := x0 + side + 2
	c.help.SetVisible(c.category != "hotkeys")
	c.help.SetDisabled(c.category == "hotkeys")
	if c.category == "hotkeys" {
		c.page.SetPosition(px, y0+3, x0+w-3, bottom)
	} else if w >= 110 {
		helpWidth := max(28, w/4)
		c.page.SetPosition(px, y0+3, x0+w-helpWidth-4, bottom)
		c.help.SetPosition(x0+w-helpWidth-2, y0+1, x0+w-3, bottom)
	} else {
		helpHeight := min(6, max(3, h/5))
		c.page.SetPosition(px, y0+3, x0+w-3, bottom-helpHeight-1)
		c.help.SetPosition(px, bottom-helpHeight+1, x0+w-3, bottom)
	}
	x := c.X2 - 1
	for _, b := range []*vtui.Button{c.cancel, c.ok, c.apply} {
		bw := vtui.StringWidth(b.GetCaption()) + 4
		b.SetPosition(x-bw, y0+h-2, x-1, y0+h-2)
		x -= bw + 1
	}
	c.layoutPage()
}
func (c *settingsCenter) Show(scr *vtui.ScreenBuf) {
	if c.sidebar.X2-c.sidebar.X1+1 != c.categorySidebarWidth() || c.sidebar.Y2 != c.contentBottom() {
		c.layoutWindow()
	}
	c.refreshAvailability()
	c.refreshChoiceHelp()
	c.page.SetFocus(c.GetFocusedItem() == c.page)
	c.BaseWindow.Show(scr)
	attr := vtui.Palette[vtui.ColDialogBox]
	for y := c.Y1 + 1; y <= c.contentBottom(); y++ {
		scr.Write(c.sidebar.X2+1, y, vtui.StringToCharInfo("│", attr))
		if c.category != "hotkeys" && c.help.X1 > c.page.X2 {
			scr.Write(c.help.X1-1, y, vtui.StringToCharInfo("│", attr))
		}
	}
	title := vtui.TruncateString(c.categoryLabel(c.category), max(1, c.page.X2-c.page.X1+1), "…")
	titleX := c.page.X1 + (c.page.X2-c.page.X1+1-vtui.StringWidth(title))/2
	titleAttr := vtui.Palette[vtui.ColDialogBoxTitle]
	if strings.TrimSpace(c.query) != "" && c.categoryMatches(c.category) == 0 {
		titleAttr = vtui.DimColor(titleAttr)
	}
	scr.Write(titleX, c.Y1+1, vtui.StringToCharInfo(title, titleAttr))
	if c.category != "hotkeys" && c.help.X1 == c.page.X1 {
		for x := c.page.X1; x <= c.help.X2; x++ {
			scr.Write(x, c.help.Y1-1, vtui.StringToCharInfo("─", attr))
		}
	}
	scr.Write(c.sidebar.X1, c.Y1+1, vtui.StringToCharInfo(settingsText("Search", "Search:"), vtui.Palette[vtui.ColDialogText]))
	separatorY := c.Y1 + 3
	scr.FillRect(c.sidebar.X1, separatorY, c.sidebar.X2, separatorY, '─', attr)
	if strings.TrimSpace(c.query) != "" {
		count := 0
		for _, cat := range c.categories {
			count += c.categoryMatches(cat.ID)
		}
		label := " " + fmt.Sprintf(settingsText("Matches", "Matches: %d"), count) + " "
		label = vtui.TruncateString(label, c.sidebar.X2-c.sidebar.X1+1, "…")
		x := c.sidebar.X1 + (c.sidebar.X2-c.sidebar.X1+1-vtui.StringWidth(label))/2
		scr.Write(x, separatorY, vtui.StringToCharInfo(label, vtui.Palette[vtui.ColDialogBoxTitle]))
	}
	c.paintContentBackground(scr)
	if c.status != "" {
		scr.Write(c.X1+2, c.apply.Y1, vtui.StringToCharInfo(vtui.TruncateString(c.status, max(0, c.apply.X1-c.X1-3), "…"), vtui.Palette[vtui.ColDialogHighlightText]))
	}
}

func (c *settingsCenter) categorySidebarWidth() int {
	width := 1
	for _, category := range c.categories {
		label := category.Label.Resolve(config.App.Language, i18n.Msg)
		width = max(width, vtui.StringWidth(label))
	}
	// Leave room for the sidebar scrollbar and a usable content column.
	w := c.X2 - c.X1 + 1
	available := w - 30
	if w >= 110 {
		available -= max(28, w/4) + 1
	}
	// Reserve " (99)" regardless of the query, plus the scrollbar column.
	return min(width+5+1, max(10, available))
}
func (c *settingsCenter) ProcessKey(e *vtinput.InputEvent) bool {
	if c.category == "hotkeys" && c.GetFocusedItem() == c.page && c.hotkeyPage != nil {
		if e.KeyDown && (e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_TAB) {
			if c.hotkeyPage.ProcessKey(e) {
				return true
			}
		}
	}

	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_ESCAPE {
		c.Close()
		return true
	}
	if c.running != nil {
		return true
	}
	if e.KeyDown && e.VirtualKeyCode == vtinput.VK_F && (e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0 {
		c.SetFocusedItem(c.search)
		return true
	}
	if e.KeyDown && e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed) == 0 {
		panes := []vtui.UIElement{c.search}
		if !c.clearSearch.IsDisabled() {
			panes = append(panes, c.clearSearch)
		}
		if !c.previous.IsDisabled() {
			panes = append(panes, c.previous, c.next)
		}
		panes = append(panes, c.sidebar, c.page, c.apply, c.ok, c.cancel)
		focused := c.GetFocusedItem()
		if e.VirtualKeyCode == vtinput.VK_TAB {
			direction := 1
			if e.ControlKeyState&vtinput.ShiftPressed != 0 {
				direction = -1
			}
			c.movePaneFocus(panes, focused, direction)
			return true
		}
		arrow := e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_DOWN || e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT
		if arrow {
			switch focused {
			case c.sidebar:
				if e.VirtualKeyCode == vtinput.VK_RIGHT {
					if c.page.CanFocus() {
						c.SetFocusedItem(c.page)
						c.page.notifyFocus()
					}
				} else if e.VirtualKeyCode == vtinput.VK_UP && c.sidebar.SelectPos == 0 {
					c.SetFocusedItem(c.search)
				} else {
					c.sidebar.ProcessKey(e)
				}
				return true
			case c.search:
				if e.VirtualKeyCode == vtinput.VK_DOWN {
					c.SetFocusedItem(c.sidebar)
				} else {
					c.search.ProcessKey(e)
				}
				return true
			case c.page:
				if e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT {
					// Text cursors and compound controls retain their editing keys.
					if item := c.page.GetFocusedItem(); item != nil && item.ProcessKey(e) {
						return true
					}
					if e.VirtualKeyCode == vtinput.VK_LEFT {
						c.SetFocusedItem(c.sidebar)
					}
					return true
				}
				c.page.ProcessKey(e)
				return true
			case c.help:
				c.help.ProcessKey(e)
				return true
			default:
				direction := 1
				if e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_LEFT {
					direction = -1
				}
				if focused == c.clearSearch || focused == c.previous || focused == c.next {
					c.movePaneFocus([]vtui.UIElement{c.clearSearch, c.previous, c.next}, focused, direction)
				} else {
					c.movePaneFocus([]vtui.UIElement{c.apply, c.ok, c.cancel}, focused, direction)
				}
				return true
			}
		}
	}
	handled := c.BaseWindow.ProcessKey(e)
	c.syncWindowBounds()
	return handled
}

func (c *settingsCenter) movePaneFocus(items []vtui.UIElement, focused vtui.UIElement, direction int) {
	index := -1
	for i, item := range items {
		if item == focused {
			index = i
			break
		}
	}
	for step := 1; step <= len(items); step++ {
		i := (index + direction*step + len(items)*2) % len(items)
		if items[i].CanFocus() && !items[i].IsDisabled() {
			c.SetFocusedItem(items[i])
			if items[i] == c.page {
				c.page.notifyFocus()
			}
			return
		}
	}
}
func (c *settingsCenter) categoryLabel(id string) string {
	for _, cat := range c.categories {
		if cat.ID == id {
			return cat.Label.Resolve(config.App.Language, i18n.Msg)
		}
	}
	return id
}
func (c *settingsCenter) groupLabel(id string) string {
	for _, s := range c.sessions {
		for _, g := range s.catalog.Groups {
			if g.ID == id {
				return g.Label.Resolve(config.App.Language, i18n.Msg)
			}
		}
	}
	return settingsText(settingsGroupKey(id), id)
}
func settingsError(format string, args ...any) error { return f4settings.Error(format, args...) }
func settingsErrorText(err error) string {
	if localized, ok := err.(interface {
		Localized(string, func(string) string) string
	}); ok {
		return localized.Localized(config.App.Language, i18n.Msg)
	}
	return Phrase(err.Error())
}

func Phrase(english string) string {
	return (f4settings.Text{English: english}).Resolve(config.App.Language, i18n.Msg)
}

func settingsText(key, fallback string) string {
	return (f4settings.Text{English: fallback, Key: "SettingsCenter." + key}).Resolve(config.App.Language, i18n.Msg)
}
func (c *settingsCenter) matches(f f4settings.Field) bool {
	if strings.TrimSpace(c.query) == "" {
		return true
	}
	for _, cat := range c.categories {
		if cat.ID == f.Category {
			f.Aliases = append(append([]string(nil), f.Aliases...), cat.Label.English)
			break
		}
	}
	f.Aliases = append(append([]string(nil), f.Aliases...), c.groupLabel(f.Group))
	return f4settings.Matches(c.query, f, c.categoryLabel(f.Category), config.App.Language, i18n.Msg)
}
func (c *settingsCenter) categoryMatches(id string) int {
	c.ensureSearchCache()
	if count, ok := c.categoryMatchCache[id]; ok {
		return count
	}
	n := 0
	for _, s := range c.sessions {
		for _, f := range s.catalog.Fields {
			if f.Category == id && c.matches(f) {
				n++
			}
		}
		for _, col := range s.catalog.Collections {
			if col.Category == id && settingsCollectionMatches(c, s, col) {
				n++
			}
		}
		for _, cmd := range s.catalog.Commands {
			if cmd.Category == id && c.matches(f4settings.Field{Category: id, Group: cmd.Group, Label: cmd.Label, Description: cmd.Description}) {
				n++
			}
		}
	}
	c.categoryMatchCache[id] = n
	return n
}
func (c *settingsCenter) updateMatches() {
	// Draft edits and category rebuilds may change searchable record names or metadata.
	c.categoryMatchCache = nil
	c.recordMatchCache = nil
	c.layoutSearch()
	for _, r := range c.page.rows {
		r.match = c.matches(r.field)
		if r.matchFunc != nil {
			r.match = r.matchFunc()
		}
		if r.session != nil {
			for _, col := range r.session.catalog.Collections {
				if col.ID == r.field.ID {
					r.match = settingsCollectionMatches(c, r.session, col)
				}
			}
		}
	}
	for _, r := range c.page.rows {
		if r.heading {
			r.match = false
			for _, other := range c.page.rows {
				if !other.heading && other.field.Group == r.field.Group && other.match {
					r.match = true
					break
				}
			}
		}
	}
}
func (c *settingsCenter) describe(r *settingsRow) {
	text := r.field.Label.Resolve(config.App.Language, i18n.Msg) + "\n\n" + r.field.Description.Resolve(config.App.Language, i18n.Msg)
	if radios, ok := r.control.(*settingsRadios); ok {
		i := radios.helpIndex()
		if i >= 0 && i < len(r.field.Choices) {
			choice := r.field.Choices[i]
			text = r.field.Label.Resolve(config.App.Language, i18n.Msg) + " — " + choice.Label.Resolve(config.App.Language, i18n.Msg) + "\n\n" + choice.Description.Resolve(config.App.Language, i18n.Msg)
		}
	}
	if r.field.Timing != "" {
		text += "\n\n" + fmt.Sprintf(Phrase("Takes effect: %s"), Phrase(r.field.Timing))
	}
	if r.unavailableReason != "" {
		text += "\n\n" + fmt.Sprintf(Phrase("Unavailable: %s"), Phrase(r.unavailableReason))
	}
	if text != c.help.text {
		c.help.text = text
		c.help.top = 0
	}
}
func (c *settingsCenter) selectCategory(id string) {
	if id == c.category {
		return
	}
	if c.category != "" {
		c.offsets[c.category] = c.page.scroll
	}
	c.category = id
	c.page.Group = vtui.NewGroup(c.page.X1, c.page.Y1, c.page.X2-c.page.X1+1, c.page.Y2-c.page.Y1+1)
	c.page.SetOwner(c.Window)
	c.page.rows = nil
	c.page.fullPage = nil
	if id == "hotkeys" {
		if h, ok := host.(HotkeyPageHost); ok {
			if c.hotkeyPage == nil {
				c.hotkeyPage = h.HotkeyPage(c.Window, func(hm *keymap.HotkeyManager) {
					for _, session := range c.sessions {
						if session.catalog.ID != "hotkeys" {
							continue
						}
						var records []f4settings.Record
						for i, r := range settingsHotkeyRows(hm) {
							records = append(records, f4settings.Record{ID: fmt.Sprintf("binding:%d", i), Values: map[string]string{"binding.Action": r.Action, "binding.Key": r.RawKey, "binding.Area": r.Area, "binding.Condition": r.Condition}})
						}
						session.draft.Records["bindings"] = records
					}
					c.status = ""
					c.updateMatches()
				})
			}
			c.page.fullPage = c.hotkeyPage
			c.page.AddItem(c.hotkeyPage)
			c.page.SetFocusedItem(c.hotkeyPage)
			c.layoutWindow()
			c.updateMatches()
			return
		}
	}
	group := ""
	for _, s := range c.sessions {
		for _, f := range s.catalog.Fields {
			if f.Category != id {
				continue
			}
			if f.Group != group {
				group = f.Group
				c.page.rows = append(c.page.rows, &settingsRow{field: f4settings.Field{Category: id, Group: group, Label: f4settings.Text{English: c.groupLabel(group)}}, heading: true, match: true})
			}
			r := &settingsRow{field: f, session: s, match: true}
			r.control = c.makeControl(r)
			r.control.SetId("setting:" + f.ID)
			if h, ok := r.control.(interface{ SetHelp(string) }); ok {
				h.SetHelp("Setting." + f.ID)
			}
			c.page.AddItem(r.control)
			c.page.rows = append(c.page.rows, r)
		}
	}
	c.addCollections(id)
	c.addCommands(id)
	c.layoutWindow()
	c.page.scroll = c.offsets[id]
	c.layoutPage()
	c.updateMatches()
	c.help.text = settingsText("Select", "Select a setting to read what it does.")
	c.help.top = 0
}
func (c *settingsCenter) layoutPage() { c.page.SetPosition(c.page.X1, c.page.Y1, c.page.X2, c.page.Y2) }
func (c *settingsCenter) makeControl(r *settingsRow) vtui.UIElement {
	f := r.field
	d := r.session.draft
	value := d.Values[f.ID]
	if r.read != nil {
		value = r.read()
	}
	change := func(v string) {
		if r.session.contributed && !settingsProviderAlive(r.session.provider) {
			c.status = Phrase("Provider is no longer loaded.")
			return
		}
		if r.write != nil {
			r.write(v)
		} else {
			d.Values[f.ID] = v
		}
		if f.Timing == "preview" && d.PreviewFunc != nil {
			if err := d.PreviewFunc(d); err != nil {
				c.status = settingsErrorText(err)
			}
		}
		c.describe(r)
		c.updateMatches()
	}
	var control vtui.UIElement
	switch f.Kind {
	case f4settings.Chord:
		control = newSettingsChord(value, change)
	case f4settings.Multiline:
		e := vtui.NewMultiLineEdit(0, 0, 20, 4, value)
		e.OnTextChange = change
		r.controlHeight = 4
		control = e
	case f4settings.Boolean:
		b := &settingsCheckbox{Checkbox: vtui.NewCheckbox(0, 0, f.Label.Resolve(config.App.Language, i18n.Msg), false)}
		if value == "true" {
			b.State = 1
		}
		b.OnChange = func(n int) { change(strconv.FormatBool(n == 1)) }
		control = b
	case f4settings.ChoiceKind:
		f = settingsFieldChoiceHelp(f)
		r.field = f
		choices := append([]f4settings.Choice(nil), f.Choices...)
		selected := -1
		var labels []string
		for i, ch := range choices {
			labels = append(labels, ch.Label.Resolve(config.App.Language, i18n.Msg))
			if ch.Value == value {
				selected = i
			}
		}
		if selected < 0 {
			selected = len(choices)
			choices = append(choices, f4settings.Choice{Value: value, Label: f4settings.Text{English: value + " " + Phrase("(saved value)"), Literal: true}, Description: f.Description})
			labels = append(labels, choices[selected].Label.English)
		}
		r.field.Choices = choices
		if settingsUseRadios(f) {
			radios := newSettingsRadios(labels, selected, func(i int) { change(choices[i].Value) })
			radios.onExplain = func() { c.describe(r) }
			control = radios
			break
		}
		b := vtui.NewComboBox(0, 0, 20, labels)
		b.DropdownOnly = !f.AllowCustom
		b.Menu.SetSelectPos(selected)
		b.Edit.SetText(labels[selected])
		if f.AllowCustom {
			b.Edit.OnTextChange = func(text string) {
				for i, label := range labels {
					if text == label {
						change(choices[i].Value)
						return
					}
				}
				change(text)
			}
		}
		b.Menu.OnAction = func(i int) {
			if i >= 0 && i < len(choices) {
				b.Edit.SetText(labels[i])
				change(choices[i].Value)
			}
		}
		control = b
	default:
		var e *vtui.Edit
		if f.Kind == f4settings.Secret {
			e = vtui.NewPasswordEdit(0, 0, 20, value)
		} else {
			e = vtui.NewEdit(0, 0, 20, value)
		}
		e.OnTextChange = change
		control = &settingsEdit{Edit: e}
	}
	control.SetDisabled(f.Unavailable != "")
	return control
}

func (c *settingsCenter) commit(closeAfter bool) {
	if c.running != nil {
		return
	}
	for _, s := range c.sessions {
		if len(s.draft.Changed()) == 0 {
			continue
		}
		if s.contributed && !settingsProviderAlive(s.provider) {
			c.status = Phrase("A settings provider was unloaded; pending edits were not saved.")
			return
		}
		for id, err := range s.draft.Validate() {
			c.status = id + ": " + settingsErrorText(err)
			return
		}
	}
	var commitNext func(int)
	commitNext = func(index int) {
		if c.closePending {
			return
		}
		if index >= len(c.sessions) {
			c.status = Phrase("Settings applied.")
			c.rebuildCategory()
			if closeAfter {
				c.Close()
			}
			return
		}
		s := c.sessions[index]
		if s.contributed && !settingsProviderAlive(s.provider) {
			c.status = Phrase("Provider unloaded; pending edits retained.")
			return
		}
		if len(s.draft.Changed()) == 0 {
			commitNext(index + 1)
			return
		}
		finish := func(r f4settings.Result) {
			s.draft.Accept(r)
			if c.closePending {
				return
			}
			if len(r.Errors) > 0 {
				for id, err := range r.Errors {
					c.status = id + ": " + settingsErrorText(err)
					break
				}
				c.rebuildCategory()
				return
			}
			commitNext(index + 1)
		}
		if s.catalog.Background {
			var result f4settings.Result
			c.runBackground(func(ctx context.Context) error { result = s.draft.CommitFunc(ctx, s.draft); return nil }, func(error) { finish(result) })
		} else {
			finish(s.draft.CommitFunc(context.Background(), s.draft))
		}
	}
	commitNext(0)
}

func (c *settingsCenter) runBackground(worker func(context.Context) error, done func(error)) {
	if c.running != nil {
		return
	}
	c.search.SetDisabled(true)
	c.sidebar.SetDisabled(true)
	c.page.SetDisabled(true)
	c.apply.SetDisabled(true)
	c.ok.SetDisabled(true)
	c.running = vtui.RunAsync(func(task *vtui.TaskContext) {
		err := worker(task)
		task.RunOnUI(func() {
			c.running = nil
			c.search.SetDisabled(false)
			c.sidebar.SetDisabled(false)
			c.apply.SetDisabled(false)
			c.ok.SetDisabled(false)
			c.rebuildCategory()
			if err != nil {
				c.status = settingsErrorText(err)
			}
			if done != nil {
				done(err)
			}
			if c.closePending && c.running == nil {
				c.Window.Close()
			}
		})
	})
}
func (c *settingsCenter) Close() {
	if c.running != nil {
		c.closePending = true
		c.running.Cancel()
		return
	}
	c.Window.Close()
}
func (c *settingsCenter) ProcessMouse(e *vtinput.InputEvent) bool {
	if c.running != nil {
		if c.cancel.HitTest(int(e.MouseX), int(e.MouseY)) {
			return c.cancel.ProcessMouse(e)
		}
		return true
	}
	if c.resizing {
		if vtui.IsMouseRelease(e) {
			c.resizing = false
		} else {
			c.ChangeSize(int(e.MouseX)-c.X1+1, int(e.MouseY)-c.Y1+1)
			c.syncWindowBounds()
		}
		return true
	}
	if int(e.MouseX) == c.X2 && int(e.MouseY) == c.Y2 && vtui.IsMousePress(e) && e.ButtonState == vtinput.FromLeft1stButtonPressed {
		c.resizing = true
		c.SavedBounds = nil
		return true
	}
	handled := c.BaseWindow.ProcessMouse(e)
	c.syncWindowBounds()
	return handled
}

func (c *settingsCenter) syncWindowBounds() {
	if c.layoutBounds == [4]int{c.X1, c.Y1, c.X2, c.Y2} {
		return
	}
	if c.screenW > 0 {
		c.fitBounds()
	} else {
		c.layoutWindow()
	}
}

func (c *settingsCenter) nextMatch(direction int) {
	if strings.TrimSpace(c.query) == "" {
		return
	}
	type target struct {
		cat, id    string
		record     int
		collection string
	}
	var targets []target
	for _, cat := range c.categories {
		for _, s := range c.sessions {
			for _, f := range s.catalog.Fields {
				if f.Category == cat.ID && c.matches(f) {
					targets = append(targets, target{cat.ID, "setting:" + f.ID, -1, ""})
				}
			}
			for _, col := range s.catalog.Collections {
				if col.Category != cat.ID {
					continue
				}
				if settingsCollectionMatches(c, s, col) {
					targets = append(targets, target{cat.ID, "collection:" + col.ID, -1, col.ID})
				}
				for i, rec := range s.draft.Records[col.ID] {
					for _, field := range col.Fields {
						f := field
						f.Category = cat.ID
						f.Group = col.Group
						f.Aliases = append(append([]string(nil), f.Aliases...), rec.Values[col.NameField])
						if c.matches(f) {
							targets = append(targets, target{cat.ID, "record-field:" + col.ID + ":" + f.ID, i, col.ID})
						}
					}
				}
			}
			for _, cmd := range s.catalog.Commands {
				if cmd.Category == cat.ID && c.matches(f4settings.Field{Category: cat.ID, Group: cmd.Group, Label: cmd.Label, Description: cmd.Description}) {
					targets = append(targets, target{cat.ID, "settings-command:" + cmd.ID, -1, ""})
				}
			}
		}
	}
	if len(targets) == 0 {
		c.status = Phrase("No matching settings.")
		return
	}
	current := ""
	if item := c.page.GetFocusedItem(); item != nil {
		current = item.GetId()
	}
	index := -1
	for i, t := range targets {
		if t.id == current && (t.record < 0 || c.offsets["record:"+t.collection] == t.record) {
			index = i
			break
		}
	}
	if index < 0 {
		if direction > 0 {
			index = 0
		} else {
			index = len(targets) - 1
		}
	} else {
		index = (index + direction + len(targets)) % len(targets)
	}
	t := targets[index]
	if t.record >= 0 {
		c.offsets["record:"+t.collection] = t.record
	}
	if c.category == t.cat {
		c.rebuildCategory()
	} else {
		c.selectCategory(t.cat)
	}
	for i, cat := range c.categories {
		if cat.ID == t.cat {
			c.sidebar.SetSelectPos(i)
		}
	}
	c.SetFocusedItem(c.page)
	for _, r := range c.page.rows {
		if r.control != nil && r.control.GetId() == t.id {
			if !r.control.IsDisabled() {
				c.page.SetFocusedItem(r.control)
			}
			c.page.scroll = r.y
			c.page.positionRows()
			c.describe(r)
			break
		}
	}
	c.status = ""
}

func Open(category string) bool { return OpenAt(category, "", "", false) }
func OpenAt(category, collection, record string, create bool) bool {
	if vtui.FrameManager == nil {
		return false
	}
	if current, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter); ok {
		if current.running == nil {
			current.navigate(category, collection, record, create)
		}
		return true
	}
	sessions, err := beginSettingsSessions(context.Background())
	if err != nil {
		vtui.ShowMessage(Phrase("Settings"), err.Error(), []string{i18n.Msg("vtui.Ok")})
		return true
	}
	return showSettingsCenter(sessions, category, collection, record, create)
}
func showSettingsCenter(sessions []*settingsSession, category, collection, record string, create bool) bool {
	c := newSettingsCenter(sessions)
	c.navigate(category, collection, record, create)
	c.ResizeConsole(vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight())
	vtui.FrameManager.Push(c)
	c.refreshSchemeChoices()
	return true
}

// Catalogs can live on an unavailable network share. Enumerate their labels
// outside the UI thread and ignore results after the editing session closes.
func (c *settingsCenter) refreshSchemeChoices() {
	directory := editor.ColorerConfigsDir()
	vtui.RunAsync(func(task *vtui.TaskContext) {
		schemes := settingsColorerSchemesAt(directory)
		task.RunOnUI(func() {
			if c.closed || directory != editor.ColorerConfigsDir() {
				return
			}
			for _, session := range c.sessions {
				if session.catalog.ID != "core" {
					continue
				}
				for i := range session.catalog.Fields {
					field := &session.catalog.Fields[i]
					if field.ID == "EditorColorerScheme" {
						field.Choices = settingsChoices(":Built-in default")
						for _, scheme := range schemes {
							field.Choices = append(field.Choices, f4settings.Choice{Value: scheme.Name, Label: f4settings.Text{English: editor.ColorerSchemeLabel(scheme), Literal: true}})
						}
					}
				}
			}
			if c.running == nil && c.category == "syntax" {
				c.rebuildCategory()
			}
		})
	})
}
func (c *settingsCenter) navigate(category, collection, record string, create bool) {
	if category == "" {
		category = lastSettingsCategory
	}
	validCategory := false
	for _, cat := range c.categories {
		if cat.ID == category {
			validCategory = true
			break
		}
	}
	if validCategory {
		c.selectCategory(category)
		for i, cat := range c.categories {
			if cat.ID == category {
				c.sidebar.SetSelectPos(i)
			}
		}
	}
	if collection != "" {
		for _, session := range c.sessions {
			for _, col := range session.catalog.Collections {
				if col.ID != collection {
					continue
				}
				for i, r := range session.draft.Records[col.ID] {
					if r.ID == record || r.Values[col.NameField] == record || r.Values["__id"] == record {
						c.offsets["record:"+col.ID] = i
						break
					}
				}
				c.rebuildCategory()
				if create && !col.Fixed {
					for _, row := range c.page.rows {
						if row.control != nil && row.control.GetId() == "collection-actions:"+col.ID {
							if bar, ok := row.control.(*settingsButtonRow); ok && len(bar.buttons) > 0 {
								bar.buttons[0].OnClick()
							}
							break
						}
					}
				}
				for _, row := range c.page.rows {
					if row.control != nil && row.control.GetId() == "collection:"+col.ID {
						c.page.scroll = row.y
						c.page.positionRows()
						c.SetFocusedItem(c.page)
						c.page.SetFocusedItem(row.control)
						c.describe(row)
						break
					}
				}
			}
		}
	}
}

func settingsGroupKey(id string) string {
	var b strings.Builder
	b.WriteString("Group.")
	for _, r := range strings.ToLower(id) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *settingsCenter) refreshAvailability() {
	for _, r := range c.page.rows {
		if r.control == nil {
			continue
		}
		reason := r.field.Unavailable
		if r.session != nil {
			if r.session.contributed && !settingsProviderAlive(r.session.provider) {
				reason = "Provider is no longer loaded."
			} else if reason == "" && r.field.Enabled != nil {
				values := r.session.draft.Values
				if r.values != nil {
					values = r.values()
				}
				reason = r.field.Enabled(values)
			}
		}
		r.unavailableReason = reason
		r.control.SetDisabled(c.running != nil || reason != "")
	}
}
func (c *settingsCenter) commandReason(requires []string) string {
	for _, s := range c.sessions {
		for _, id := range s.draft.Changed() {
			for _, require := range requires {
				if require == "*" || id == require || strings.HasSuffix(require, ".*") && strings.HasPrefix(id, strings.TrimSuffix(require, "*")) {
					return fmt.Sprintf(Phrase("Apply changes to %s before running this operation."), id)
				}
			}
		}
	}
	return ""
}

func (c *settingsCenter) layoutSearch() {
	if c.previous == nil || c.sidebar == nil {
		return
	}
	searching := strings.TrimSpace(c.query) != ""
	c.previous.SetVisible(searching)
	c.previous.SetDisabled(!searching)
	c.next.SetDisabled(!searching)
	c.next.SetVisible(searching)
	right := c.sidebar.X2
	if searching {
		c.next.SetPosition(right-2, c.Y1+2, right, c.Y1+2)
		c.previous.SetPosition(right-6, c.Y1+2, right-4, c.Y1+2)
		right -= 8
	} else if c.GetFocusedItem() == c.previous || c.GetFocusedItem() == c.next {
		c.SetFocusedItem(c.search)
	}
	filled := c.query != ""
	c.clearSearch.SetVisible(filled)
	c.clearSearch.SetDisabled(!filled)
	if filled {
		c.clearSearch.SetPosition(right-2, c.Y1+2, right, c.Y1+2)
		right -= 3
	} else if c.GetFocusedItem() == c.clearSearch {
		c.SetFocusedItem(c.search)
	}
	c.search.SetPosition(c.sidebar.X1, c.Y1+2, max(c.sidebar.X1, right), c.Y1+2)
}

// Keep controls' semantic foreground and focus colors; replace only the dialog
// surface behind the content. Inputs retain their separate input surface.
func (c *settingsCenter) paintContentBackground(scr *vtui.ScreenBuf) {
	background := vtui.Palette[theme.ColDialogSettingsBackground]
	if background == 0 {
		return
	}
	_, normalBG := theme.GetColorRGBBoth(vtui.Palette[vtui.ColDialogText])
	for y := c.Y1 + 1; y <= c.page.Y2; y++ {
		for x := c.page.X1; x <= c.page.X2; x++ {
			cell := scr.GetCell(x, y)
			_, bg := theme.GetColorRGBBoth(cell.Attributes)
			if bg != normalBG {
				continue
			}
			if background&vtui.IsBgRGB != 0 {
				cell.Attributes = vtui.SetRGBBack(cell.Attributes, vtui.GetRGBBack(background))
			} else {
				cell.Attributes = vtui.SetIndexBack(cell.Attributes, vtui.GetIndexBack(background))
			}
			scr.Write(x, y, []vtui.CharInfo{cell})
		}
	}
}

type settingsSearchButton struct{ *vtui.Button }

func (b *settingsSearchButton) Show(scr *vtui.ScreenBuf) {
	if !b.IsDisabled() {
		b.Button.Show(scr)
	}
}

func (c *settingsCenter) contentBottom() int {
	return c.Y2 - 2 // Status shares the action row, so content height stays fixed.
}

// The clear button occupies the trailing three cells of the search surface.
type settingsClearButton struct{ *vtui.Button }

func (b *settingsClearButton) Show(scr *vtui.ScreenBuf) {
	if b.IsDisabled() {
		return
	}
	b.ScreenObject.Show(scr)
	normal, _ := b.GetStateAttrs(vtui.ColDialogEdit, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton)
	scr.Write(b.X1, b.Y1, vtui.StringToCharInfo(" × ", normal))
}

func (c *settingsCenter) ensureSearchCache() {
	if c.categoryMatchCache != nil && c.searchCacheQuery == c.query && c.searchCacheLanguage == config.App.Language {
		return
	}
	c.searchCacheQuery, c.searchCacheLanguage = c.query, config.App.Language
	c.categoryMatchCache = make(map[string]int)
	c.recordMatchCache = make(map[settingsRecordMatchKey]bool)
}

// SetRecordDefault seeds the newest record opened by a contextual command.
func SetRecordDefault(collection, field, value string) {
	if vtui.FrameManager == nil {
		return
	}
	if c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter); ok {
		for _, s := range c.sessions {
			rows := s.draft.Records[collection]
			if len(rows) > 0 {
				rows[len(rows)-1].Values[field] = value
			}
		}
		c.rebuildCategory()
	}
}

// Center is the canonical settings dialog returned to application integrations.
type Center = settingsCenter

func (c *settingsCenter) Category() string { return c.category }
