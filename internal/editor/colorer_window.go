//go:build !lite

package editor

import (
	"context"
	"path/filepath"
	"strings"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtui"
)

// windowJob is one viewer window to highlight.
type windowJob struct {
	context []string
	lines   []viewer.WindowLine
}

// windowColored is a highlighted line of a viewer window.
type windowColored struct {
	text  string
	attrs []uint64
}

// windowColorizer highlights viewer windows on one goroutine, with the
// editor's highlighter by the editor's rules, as NewTextColorizer does for
// the quick view. Each window is highlighted from its context lines on: the
// viewer shows an arbitrary part of the file, so, as with FarViewer and the
// editor's anchor, a construct opened above the context is not seen.
type windowColorizer struct {
	jobs   chan windowJob
	cancel context.CancelFunc
	colors map[int64]windowColored // UI-owned: the last window highlighted
	// UI-owned: what the lines of that window are drawn over, and whether
	// a window has arrived yet.
	base    uint64
	baseSet bool
}

func (w *windowColorizer) Request(context []string, lines []viewer.WindowLine) {
	job := windowJob{context: append([]string(nil), context...), lines: append([]viewer.WindowLine(nil), lines...)}
	for {
		select {
		case w.jobs <- job:
			return
		default:
		}
		// A request not started yet is out of date: replace it.
		select {
		case <-w.jobs:
		default:
		}
	}
}

func (w *windowColorizer) LineAttrs(offset int64, text string) []uint64 {
	if c, ok := w.colors[offset]; ok && c.text == text {
		return c.attrs
	}
	return nil
}

func (w *windowColorizer) BaseAttr() (uint64, bool) {
	return w.base, w.baseSet
}

func (w *windowColorizer) Close() {
	w.cancel()
}

// NewWindowColorizer is viewer.NewWindowColorizer: nil unless viewer
// highlighting covers every viewer, then Chroma or Colorer by the editor's
// rules.
func NewWindowColorizer(path, firstLine string, base uint64, redraw func()) viewer.WindowColorizer {
	if !viewerHighlightAllows(false) || vtui.FrameManager == nil || strings.EqualFold(config.App.EditorHighlighter, "None") {
		return nil
	}
	useColorer := strings.EqualFold(config.App.EditorHighlighter, "Colorer") && SchemasExist()
	if useColorer && !config.App.EditorColorerSyntax {
		return nil
	}
	frames := vtui.FrameManager
	ctx, cancel := context.WithCancel(context.Background())
	w := &windowColorizer{jobs: make(chan windowJob, 1), cancel: cancel}
	src := CurrentColorerSource()
	scheme := config.App.EditorColorerScheme
	colorerBase := ColorerEditorBaseAttr(base)
	post := func(colors map[int64]windowColored, lineBase uint64) {
		frames.PostTask(func() {
			if ctx.Err() != nil {
				return
			}
			w.colors = colors
			w.base, w.baseSet = lineBase, true
			if redraw != nil {
				redraw()
			}
		})
	}
	colorerSetups.start()
	go func() {
		var session *colorer.Session
		var settings colorerTypeSettings
		if useColorer {
			session, settings = startWindowColorer(ctx, src, scheme, path, firstLine)
			if session != nil {
				defer session.Close()
			}
		}
		// The rest of this goroutine serves the window until it is
		// closed; only the session setup is counted.
		colorerSetups.done()
		// What the lines are drawn over: Colorer's are on its style's
		// def:Text and the file type's colours, as colourWindowWithColorer
		// draws them; Chroma's are on the viewer's base.
		lineBase := base
		if session != nil {
			lineBase = settings.baseAttr(colorerBase)
		}
		var h vtui.Highlighter
		if session == nil {
			if h = vtui.GetHighlighter(path, ""); h == nil {
				return
			}
			if closer, ok := h.(interface{ Close() error }); ok {
				defer func() { _ = closer.Close() }()
			}
		}
		for {
			var job windowJob
			select {
			case <-ctx.Done():
				return
			case job = <-w.jobs:
			}
			colors := make(map[int64]windowColored, len(job.lines))
			if session != nil {
				if !colourWindowWithColorer(ctx, session, settings, path, firstLine, colorerBase, job, colors) {
					return
				}
			} else {
				var state any
				for _, line := range job.context {
					_, state = h.Highlight(line, state, base)
				}
				for _, line := range job.lines {
					var attrs []uint64
					attrs, state = h.Highlight(line.Text, state, base)
					colors[line.Offset] = windowColored{text: line.Text, attrs: attrs}
				}
			}
			if ctx.Err() != nil {
				return
			}
			post(colors, lineBase)
		}
	}()
	return w
}

// startWindowColorer opens a Colorer session for a viewer, or returns nil
// when Colorer cannot start or knows no type for the file, so the caller
// hands over to Chroma.
func startWindowColorer(ctx context.Context, src ColorerSource, scheme, path, firstLine string) (*colorer.Session, colorerTypeSettings) {
	session, err := acquireCancelableColorerSession(ctx, src)
	if err != nil {
		vtui.DebugLog("COLORER: viewer highlighting falls back to Chroma, session failed: %v", err)
		return nil, colorerTypeSettings{}
	}
	if scheme == "" {
		scheme = "default"
	}
	if err := session.SetHRD("rgb", scheme); err != nil {
		vtui.DebugLog("COLORER: viewer highlighting falls back to Chroma, colour style %q: %v", scheme, err)
		session.Close()
		return nil, colorerTypeSettings{}
	}
	if ok, err := session.SelectType(filepath.Base(path), firstLine); err != nil || !ok {
		session.Close()
		return nil, colorerTypeSettings{}
	}
	return session, readColorerTypeSettings(session)
}

// colourWindowWithColorer parses a window from its context on, after a
// reset; it reports false when the session failed.
func colourWindowWithColorer(ctx context.Context, session *colorer.Session, settings colorerTypeSettings, path, firstLine string, base uint64, job windowJob, colors map[int64]windowColored) bool {
	session.Reset()
	if _, err := session.SelectType(filepath.Base(path), firstLine); err != nil {
		vtui.DebugLog("COLORER: viewer highlighting stopped: %v", err)
		return false
	}
	lineBase := settings.baseAttr(base)
	for _, line := range job.context {
		if ctx.Err() != nil {
			return false
		}
		if _, err := session.ParseLine(settings.truncate(line)); err != nil {
			vtui.DebugLog("COLORER: viewer highlighting stopped: %v", err)
			return false
		}
	}
	for _, line := range job.lines {
		if ctx.Err() != nil {
			return false
		}
		text := settings.truncate(line.Text)
		regions, err := session.ParseLine(text)
		if err != nil {
			vtui.DebugLog("COLORER: viewer highlighting stopped: %v", err)
			return false
		}
		attrs, _ := colorerAttrs(text, regions, lineBase, true, settings.plainEOL)
		colors[line.Offset] = windowColored{text: line.Text, attrs: attrs}
	}
	return true
}
