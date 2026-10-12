package mediainfo

import (
	"strings"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type reportDisplayLine struct {
	text        string
	fieldName   string
	fieldColumn int
	value       string
}

// reportTextView is a scrollable, dialog-themed report control. A ListBox
// cannot color only part of a row, so this view draws field names with a
// dimmed foreground and leaves values at the normal dialog text color.
type reportTextView struct {
	vtui.ScrollView
	lines []reportDisplayLine
}

func newReportTextView(x, y, width, height int, lines []string, styleFieldNames bool) *reportTextView {
	view := &reportTextView{lines: make([]reportDisplayLine, len(lines))}
	for index, line := range lines {
		view.lines[index] = parseReportDisplayLine(line, styleFieldNames)
	}
	view.SetCanFocus(true)
	view.ShowScrollBar = true
	view.InitScrollBar(view)
	view.ScrollBar.ColorIdx = vtui.ColDialogBox
	view.ItemCount = len(view.lines)
	view.SetPosition(x, y, x+width-1, y+height-1)
	return view
}

func (view *reportTextView) SetPosition(x1, y1, x2, y2 int) {
	view.ScrollView.SetPosition(x1, y1, x2, y2)
	if view.ItemCount == 0 {
		view.SelectPos = 0
		view.TopPos = 0
		return
	}
	if view.SelectPos >= view.ItemCount {
		view.SelectPos = view.ItemCount - 1
	}
	view.EnsureVisible()
	maxTop := view.ItemCount - view.ViewHeight
	if maxTop < 0 {
		maxTop = 0
	}
	if view.TopPos > maxTop {
		view.TopPos = maxTop
	}
}

func parseReportDisplayLine(line string, styleFieldName bool) reportDisplayLine {
	display := reportDisplayLine{text: line}
	if !styleFieldName {
		return display
	}
	delimiter := strings.Index(line, " : ")
	if delimiter <= 0 {
		return display
	}
	prefix := line[:delimiter]
	name := strings.TrimRight(prefix, " ")
	if name == "" {
		return display
	}
	display.fieldName = name
	display.fieldColumn = runewidth.StringWidth(prefix)
	display.value = line[delimiter:]
	return display
}

func (view *reportTextView) Show(screen *vtui.ScreenBuf) {
	view.ScreenObject.Show(screen)
	if !view.IsVisible() {
		return
	}

	base := view.GetStateAttr(vtui.ColDialogText, vtui.ColDialogText)
	cursor := view.GetStateAttr(vtui.ColDialogSelectedButton, vtui.ColDialogSelectedButton)
	// X2 is the dialog-border column. Content always stops before it; when
	// overflowing, DrawScrollBar replaces that border with the scroll track.
	contentWidth := view.X2 - view.X1
	visibleRows := view.Y2 - view.Y1 + 1
	for row := 0; row < visibleRows; row++ {
		y := view.Y1 + row
		lineIndex := view.TopPos + row
		attr := base
		if lineIndex < len(view.lines) && lineIndex == view.SelectPos && view.IsFocused() {
			attr = cursor
		}
		if contentWidth > 0 {
			screen.FillRect(view.X1, y, view.X2-1, y, ' ', attr)
		}
		if lineIndex >= len(view.lines) || contentWidth <= 0 {
			continue
		}
		view.drawLine(screen, y, contentWidth, view.lines[lineIndex], attr)
	}
	view.DrawScrollBar(screen)
}

func (view *reportTextView) drawLine(screen *vtui.ScreenBuf, y, width int, line reportDisplayLine, attr uint64) {
	if line.fieldName == "" {
		text := runewidth.Truncate(line.text, width, "")
		screen.Write(view.X1, y, vtui.StringToCharInfo(text, attr))
		return
	}

	name := runewidth.Truncate(line.fieldName, width, "")
	screen.Write(view.X1, y, vtui.StringToCharInfo(name, subduedReportFieldAttr(attr)))
	if line.fieldColumn >= width {
		return
	}
	value := runewidth.Truncate(line.value, width-line.fieldColumn, "")
	screen.Write(view.X1+line.fieldColumn, y, vtui.StringToCharInfo(value, attr))
}

// subduedReportFieldAttr keeps captions distinguishable without the 50% fade
// of vtui.DimColor, which moves the foreground half way to the background.
// True-color themes with a true-color background move it a quarter of the way
// (so the text gets dimmer whichever way round the colours are); with only the
// foreground in true color it keeps 75% of its intensity; indexed themes merely
// drop the bright flag.
func subduedReportFieldAttr(attr uint64) uint64 {
	if attr&vtui.IsFgRGB == 0 {
		return attr &^ vtui.ForegroundIntensity
	}
	fg := vtui.GetRGBFore(attr)
	fr, fgG, fb := (fg>>16)&0xff, (fg>>8)&0xff, fg&0xff
	if attr&vtui.IsBgRGB != 0 {
		bg := vtui.GetRGBBack(attr)
		br, bgG, bb := (bg>>16)&0xff, (bg>>8)&0xff, bg&0xff
		mix := func(f, b uint32) uint32 { return (f*3 + b) / 4 }
		return vtui.SetRGBFore(attr, mix(fr, br)<<16|mix(fgG, bgG)<<8|mix(fb, bb))
	}
	return vtui.SetRGBFore(attr, (fr*3/4)<<16|(fgG*3/4)<<8|(fb*3/4))
}

func (view *reportTextView) ProcessKey(event *vtinput.InputEvent) bool {
	if event == nil || !event.KeyDown || view.IsDisabled() {
		return false
	}
	return view.HandleKey(event)
}

func (view *reportTextView) ProcessMouse(event *vtinput.InputEvent) bool {
	if event == nil || view.IsDisabled() {
		return false
	}
	return view.HandleMouse(event)
}
