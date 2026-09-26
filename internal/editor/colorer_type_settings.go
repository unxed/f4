//go:build !lite

package editor

import (
	"strconv"
	"strings"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// colorerTypeSettings are the file type parameters FarEditor::reloadTypeSettings
// applies to an editor, as far as f4 acts on them.
type colorerTypeSettings struct {
	maxLineLength int    // maxlinelength: parse at most this many characters of a line; 0 for all
	plainEOL      bool   // fullback=no: a region running to the end of the line stops at the text
	showCross     string // show-cross: none, vertical, horizontal, both; "" when unset
	// default-fore, default-back: RGB, meaningful only when the matching
	// flag is set. The flags are what says "unset", not a sentinel value:
	// this struct is held zeroed until a session has been read — while the
	// editor already draws through baseAttr — and a zero that meant
	// "RGB 000000" painted the whole editor black on black until the
	// Colorer worker's first round trip landed.
	fore, back       int
	foreSet, backSet bool
}

// readColorerTypeSettings is FarEditor::reloadTypeSettings: the "default"
// type's parameters, then those of the type the session has on top. As
// there, a type cannot turn fullback back on once "default" has turned it
// off.
func readColorerTypeSettings(session *colorer.Session) colorerTypeSettings {
	var s colorerTypeSettings
	current, err := session.FileType()
	if err != nil {
		return s
	}
	for _, typeName := range []string{"default", current} {
		if typeName == "" {
			continue
		}
		param := func(name string) (string, bool) {
			v, ok, err := session.FileTypeParam(typeName, name)
			return v, ok && err == nil
		}
		if v, ok := param("maxlinelength"); ok {
			s.maxLineLength = colorerParamInt(v, s.maxLineLength)
		}
		if v, ok := param("default-fore"); ok {
			if rgb, valid := colorerParamHexValue(v); valid {
				s.fore, s.foreSet = rgb, true
			}
		}
		if v, ok := param("default-back"); ok {
			if rgb, valid := colorerParamHexValue(v); valid {
				s.back, s.backSet = rgb, true
			}
		}
		if v, ok := param("fullback"); ok && v == "no" {
			s.plainEOL = true
		}
		if v, ok := param("show-cross"); ok {
			switch v {
			case "none", "vertical", "horizontal", "both":
				s.showCross = v
			}
		}
	}
	return s
}

// colorerParamInt is FileType::getParamValueInt: std::stoi of the value, or
// def when it is empty or does not start with a number.
func colorerParamInt(value string, def int) int {
	value = strings.TrimLeft(value, " \t\n\v\f\r")
	end := 0
	if end < len(value) && (value[end] == '+' || value[end] == '-') {
		end++
	}
	digits := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == digits {
		return def
	}
	n, err := strconv.Atoi(value[:end])
	if err != nil {
		return def
	}
	return n
}

// colorerParamHex is FileType::getParamValueHex: an optional '#', then
// std::stoul in base 16, or def when that does not parse.
func colorerParamHex(value string, def int) int {
	if rgb, ok := colorerParamHexValue(value); ok {
		return rgb
	}
	return def
}

// colorerParamHexValue is colorerParamHex with the "did it parse" answer kept
// separate, so a colour that was never given stays unset instead of becoming
// a real black.
func colorerParamHexValue(value string) (int, bool) {
	value = strings.TrimPrefix(value, "#")
	value = strings.TrimLeft(value, " \t\n\v\f\r")
	if len(value) > 2 && value[0] == '0' && (value[1] == 'x' || value[1] == 'X') {
		value = value[2:]
	}
	end := 0
	for end < len(value) && strings.IndexByte("0123456789abcdefABCDEF", value[end]) >= 0 {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(value[:end], 16, 32)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// truncate cuts a line to maxlinelength characters, as FarEditor::getLine
// does before Colorer sees it.
func (s colorerTypeSettings) truncate(line string) string {
	if s.maxLineLength <= 0 || len(line) <= s.maxLineLength {
		return line
	}
	n := 0
	for i := range line {
		if n == s.maxLineLength {
			return line[:i]
		}
		n++
	}
	return line
}

// baseAttr puts default-fore and default-back into the editor's base colour,
// which FarEditor::convert gives every region that sets no colour of its own.
func (s colorerTypeSettings) baseAttr(attr uint64) uint64 {
	if s.foreSet {
		attr = vtui.SetRGBFore(attr, uint32(s.fore))
	}
	if s.backSet {
		attr = vtui.SetRGBBack(attr, uint32(s.back))
	}
	return attr
}

// crossAxes is show-cross as FarEditor reads it in its "if the scheme says"
// cross mode.
func (s colorerTypeSettings) crossAxes() (horz, vert bool) {
	switch s.showCross {
	case "vertical":
		return false, true
	case "horizontal":
		return true, false
	case "both":
		return true, true
	}
	return false, false
}

// colorerBaseAttr is the editor's base colour for syntax: the Colorer style's
// def:Text when it supplies one, with the file type's default colours on top.
func (ev *EditorView) colorerBaseAttr() uint64 {
	attr := ColorerEditorBaseAttr(vtui.Palette[theme.ColEditorText])
	if ch, ok := ev.Highlighter.(*ColorerHighlighter); ok {
		attr = ch.typeSettings.baseAttr(attr)
	}
	return attr
}

// colorerCrossAxes narrows the crosshair to what the file type's show-cross
// says, when the cross mode leaves it to the file type.
func (ev *EditorView) colorerCrossAxes(horz, vert bool) (bool, bool) {
	if config.App.EditorCrossMode != config.ColorerCrossScheme {
		return horz, vert
	}
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok {
		return false, false
	}
	h, v := ch.typeSettings.crossAxes()
	return horz && h, vert && v
}

// adoptTypeSettings takes settings the worker read after giving its session a
// type. Different settings change the colours already computed, so they are
// computed again.
func (ch *ColorerHighlighter) adoptTypeSettings(s colorerTypeSettings) {
	if ch.closed || ch.typeSettings == s {
		return
	}
	ch.typeSettings = s
	ch.DropFrom(0)
	if ch.redraw != nil {
		ch.redraw()
	}
}
