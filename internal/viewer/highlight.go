package viewer

import (
	"bytes"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// WindowLine is one logical line a viewer shows: where it starts in the text
// and what it holds, without its line break.
type WindowLine struct {
	Offset int64
	Text   string
}

// WindowColorizer highlights what a viewer shows, off the UI thread. The
// viewer hands it the logical lines on screen with the lines before them as
// context, as FarColorer's FarViewer takes them, and draws each line in its
// colours once they arrive.
type WindowColorizer interface {
	// Request asks for lines highlighted after context. It returns at once;
	// a newer request replaces one not started yet.
	Request(context []string, lines []WindowLine)
	// LineAttrs returns the attribute of each rune of the line starting at
	// offset, or nil while it is not highlighted with this text. The slice is
	// shared and must not be modified.
	LineAttrs(offset int64, text string) []uint64
	// BaseAttr returns the attribute the highlighted lines are drawn over —
	// the base the colorizer was given, or a Colorer style's own background
	// on top of it — once the first lines arrived; ok is false until then.
	BaseAttr() (attr uint64, ok bool)
	// Close stops the work and releases what it holds.
	Close()
}

// NewWindowColorizer, when set, returns a colorizer for a viewer of path, whose
// first line is firstLine, drawn over base; or nil when the viewer is not to
// be highlighted. redraw runs on the UI thread when colours arrive. It is the
// viewer's counterpart of NewTextColorizer, set by the application.
var NewWindowColorizer func(path, firstLine string, base uint64, redraw func()) WindowColorizer

const (
	// How many lines above the screen a viewer hands over as context, and
	// how far it reads to find one line. A construct opened further up is
	// not seen, as with the editor's anchor.
	viewerHighlightContextLines = 100
	viewerHighlightMaxBytes     = 64 * 1024
)

// RefreshHighlighting drops the colorizer this window built on first use, so
// the next redraw asks NewWindowColorizer again and picks up a setting
// changed since then (f4 #1413: toggling ViewerHighlighting for a viewer
// that is already open).
func (vv *ViewerView) RefreshHighlighting() {
	if vv.highlight != nil {
		vv.highlight.Close()
	}
	vv.highlight = nil
	vv.highlightTried = false
	// renderText only re-requests a window when the lines on screen hash
	// differently from the last request it made (highlightKey), so it does
	// not spam the colorizer every frame while nothing has scrolled. That
	// hash says nothing about which colorizer made the request, though: left
	// as is, toggling highlighting back on without scrolling built a fresh,
	// empty colorizer that never got a Request, because the screen still
	// hashed to the same key as the one already highlighted before this
	// toggle turned it off (f4 #1413 — "выключает подсветку сразу, а
	// включает с нескольких попыток"). Resetting it here makes the next
	// renderText call send a request regardless of the lines on screen.
	vv.highlightKey = 0
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

// windowColorizer is the viewer's colorizer, created on first use.
func (vv *ViewerView) windowColorizer() WindowColorizer {
	if !vv.highlightTried {
		vv.highlightTried = true
		if NewWindowColorizer != nil && vv.Backend != nil {
			first, _ := vv.highlightLineAt(0)
			vv.highlight = NewWindowColorizer(vv.Path, first, vtui.Palette[theme.ColViewerText], func() {
				if vtui.FrameManager != nil {
					vtui.FrameManager.Redraw()
				}
			})
		}
	}
	return vv.highlight
}

// textAttr is what the text view is drawn over: Viewer.Text, or, once the
// colorizer says what it draws highlighted lines over, that, so the rest of
// the view does not show as strips of another background around the lines,
// as it does when Colorer paints them on its style's own background (#1232).
// The hex and decode views are not highlighted and stay on Viewer.Text.
func (vv *ViewerView) textAttr() uint64 {
	attr := vtui.Palette[theme.ColViewerText]
	if vv.HexMode || vv.DecodeMode || vv.highlight == nil {
		return attr
	}
	if base, ok := vv.highlight.BaseAttr(); ok {
		return base
	}
	return attr
}

// highlightLineStart finds where the logical line holding off begins, looking
// back at most viewerHighlightMaxBytes; a longer line is taken to begin at off.
func (vv *ViewerView) highlightLineStart(off int64) (int64, bool) {
	if off <= 0 {
		return 0, true
	}
	start := max(off-viewerHighlightMaxBytes, 0)
	data, err := vv.Backend.ReadAt(start, int(off-start))
	if err != nil {
		return 0, false
	}
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		return start + int64(i) + 1, true
	}
	if start == 0 {
		return 0, true
	}
	return off, true
}

// highlightLineAt reads the logical line starting at off, without its break,
// at most viewerHighlightMaxBytes of it.
func (vv *ViewerView) highlightLineAt(off int64) (string, bool) {
	// Never past the end: the backend reports a range beyond the file as
	// still loading.
	n := min(int64(viewerHighlightMaxBytes), vv.Backend.Size()-off)
	if n <= 0 {
		return "", true
	}
	data, err := vv.Backend.ReadAt(off, int(n))
	if err != nil {
		return "", false
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		data = data[:i]
	}
	return strings.TrimSuffix(string(data), "\r"), true
}

// highlightLinesBefore returns up to n logical lines ending just before off,
// in order, read from at most viewerHighlightMaxBytes back.
func (vv *ViewerView) highlightLinesBefore(off int64, n int) []string {
	if off <= 0 {
		return nil
	}
	start := max(off-viewerHighlightMaxBytes, 0)
	data, err := vv.Backend.ReadAt(start, int(off-start))
	if err != nil || len(data) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // cut by the read
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// highlightWindowKey identifies the lines a request was made for.
func highlightWindowKey(lines []WindowLine) uint64 {
	h := fnv.New64a()
	for _, l := range lines {
		_, _ = h.Write([]byte(strconv.FormatInt(l.Offset, 10)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(l.Text))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// applyViewerHighlight paints a row's cells with its logical line's colours.
// rowText is the row's text, cellByteOffsets where each cell's text begins in
// it, and rowStart where the row begins within lineText.
func applyViewerHighlight(cells []vtui.CharInfo, rowText string, cellByteOffsets []int, lineText string, rowStart int, attrs []uint64) {
	if rowStart < 0 {
		return
	}
	base := utf8.RuneCountInString(lineText[:min(rowStart, len(lineText))])
	// runeAt[b] is the index of the rune byte b of rowText belongs to.
	runeAt := make([]int, len(rowText)+1)
	idx := -1
	for i := 0; i <= len(rowText); i++ {
		if i == len(rowText) || utf8.RuneStart(rowText[i]) {
			idx++
		}
		runeAt[i] = idx
	}
	for i := range cells {
		if i >= len(cellByteOffsets) {
			break
		}
		b := cellByteOffsets[i]
		if b < 0 || b >= len(runeAt) {
			continue
		}
		if r := base + runeAt[b]; r < len(attrs) {
			cells[i].Attributes = attrs[r]
		}
	}
}
