//go:build !lite

package editor

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

// colorerTextBatch is how many lines the text colorizer posts at a time: few
// enough that the first screen arrives quickly, many enough not to redraw per
// line.
const colorerTextBatch = 200

// colorerTextColorizer colours a text from its first line on one goroutine,
// with Colorer or Chroma, and hands the colours to the UI thread in batches.
// FarViewer parses what the viewer shows with the lines before it as context;
// a quick view shows the head of the file, so parsing from the first line
// gives the highlighter the whole context there is.
type colorerTextColorizer struct {
	cancel context.CancelFunc
	attrs  [][]uint64 // UI-owned: the colours of each line, nil until known
}

func (c *colorerTextColorizer) LineAttrs(idx int) []uint64 {
	if c == nil || idx < 0 || idx >= len(c.attrs) {
		return nil
	}
	return c.attrs[idx]
}

func (c *colorerTextColorizer) Close() {
	if c != nil && c.cancel != nil {
		c.cancel()
	}
}

// viewerHighlightAllows reports whether the viewer highlighting setting
// covers this text.
func viewerHighlightAllows(quickView bool) bool {
	switch config.App.ViewerHighlighting {
	case config.ViewerHighlightQuickView:
		return quickView
	case config.ViewerHighlightAll:
		return true
	}
	return false
}

// NewTextColorizer is viewer.NewTextColorizer, highlighting with the editor's
// highlighter by the editor's rules: none for None; Colorer when it is chosen
// and its schemas are installed, handing over to Chroma when Colorer cannot
// start or knows no type for the file; Chroma otherwise.
func NewTextColorizer(path string, lines []string, base uint64, quickView bool, redraw func()) viewer.TextColorizer {
	if len(lines) == 0 || !viewerHighlightAllows(quickView) || vtui.FrameManager == nil {
		return nil
	}
	if strings.EqualFold(config.App.EditorHighlighter, "None") {
		return nil
	}
	useColorer := strings.EqualFold(config.App.EditorHighlighter, "Colorer") && SchemasExist()
	if useColorer && !config.App.EditorColorerSyntax {
		return nil // Colorer in charge with its syntax colours off colours nothing
	}
	frames := vtui.FrameManager
	ctx, cancel := context.WithCancel(context.Background())
	c := &colorerTextColorizer{cancel: cancel, attrs: make([][]uint64, len(lines))}
	snapshot := append([]string(nil), lines...)
	src := CurrentColorerSource()
	scheme := config.App.EditorColorerScheme
	colorerBase := ColorerEditorBaseAttr(base)
	colorerSetups.start()
	go func() {
		defer colorerSetups.done()
		out := &textColorizerBatches{ctx: ctx, frames: frames, c: c, redraw: redraw}
		if useColorer && colourTextWithColorer(ctx, src, scheme, path, snapshot, colorerBase, out) {
			return
		}
		colourTextWithChroma(ctx, path, snapshot, base, out)
	}()
	return c
}

// textColorizerBatches hands a colorizer's colours to the UI thread in
// batches, in line order.
type textColorizerBatches struct {
	ctx    context.Context
	frames interface{ PostTask(func()) }
	c      *colorerTextColorizer
	redraw func()
	start  int
	batch  [][]uint64
}

func (b *textColorizerBatches) add(attrs []uint64) {
	b.batch = append(b.batch, attrs)
	if len(b.batch) == colorerTextBatch {
		b.flush()
	}
}

func (b *textColorizerBatches) flush() {
	if len(b.batch) == 0 {
		return
	}
	first, done := b.start, b.batch
	b.start += len(done)
	b.batch = nil
	ctx, c, redraw := b.ctx, b.c, b.redraw
	b.frames.PostTask(func() {
		if ctx.Err() != nil {
			return
		}
		copy(c.attrs[first:], done)
		if redraw != nil {
			redraw()
		}
	})
}

// colourTextWithColorer colours lines with a Colorer session of its own. It
// reports false, having coloured nothing, when Colorer cannot start or knows
// no type for the file, so the caller can hand over to Chroma; true otherwise,
// including when it stops part way.
func colourTextWithColorer(ctx context.Context, src ColorerSource, scheme, path string, lines []string, base uint64, out *textColorizerBatches) bool {
	session, err := acquireCancelableColorerSession(ctx, src)
	if err != nil {
		vtui.DebugLog("COLORER: viewer highlighting falls back to Chroma, session failed: %v", err)
		return false
	}
	defer session.Close()
	if scheme == "" {
		scheme = "default"
	}
	if err := session.SetHRD("rgb", scheme); err != nil {
		vtui.DebugLog("COLORER: viewer highlighting falls back to Chroma, colour style %q: %v", scheme, err)
		return false
	}
	if ok, err := session.SelectType(filepath.Base(path), lines[0]); err != nil || !ok {
		return false
	}
	settings := readColorerTypeSettings(session)
	lineBase := settings.baseAttr(base)
	for _, line := range lines {
		if ctx.Err() != nil {
			return true
		}
		line = settings.truncate(line)
		regions, err := session.ParseLine(line)
		if err != nil {
			vtui.DebugLog("COLORER: viewer highlighting stopped: %v", err)
			break
		}
		attrs, _ := colorerAttrs(line, regions, lineBase, true, settings.plainEOL)
		out.add(attrs)
	}
	out.flush()
	return true
}

// colourTextWithChroma colours lines with the Chroma highlighter the editor
// would pick for the file, carrying its state from line to line.
func colourTextWithChroma(ctx context.Context, path string, lines []string, base uint64, out *textColorizerBatches) {
	h := vtui.GetHighlighter(path, "")
	if h == nil {
		return
	}
	if closer, ok := h.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}
	var state any
	for _, line := range lines {
		if ctx.Err() != nil {
			return
		}
		var attrs []uint64
		attrs, state = h.Highlight(line, state, base)
		out.add(attrs)
	}
	out.flush()
}
