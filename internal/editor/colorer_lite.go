//go:build lite

package editor

// This file is the single point of truth for what the "lite" build tag does
// to syntax highlighting: it replaces every symbol the excluded
// colorer_*.go/colorer.go files export with an equivalent that never imports
// "github.com/unxed/colorer4go" (and therefore never links its WASM engine).
// Editors and viewers fall back to Chroma (see plugins/chroma) exactly the
// way they already do today when Colorer itself fails to start or has no
// scheme for a file — a lite build simply makes that the only path there is.
//
// Every exported name here mirrors one of the excluded files' signatures so
// that internal/app, internal/settings, internal/viewer and internal/panel
// need no changes at all for the lite tag: they keep calling
// editor.SchemasExist, editor.CurrentColorerSource, ev.ColorerPair, and so on,
// and get inert, Colorer-free behaviour instead.

import (
	"context"
	"errors"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// errColorerLiteUnavailable is what every Colorer operation reports in a lite
// build: there is no Colorer engine linked in to try.
var errColorerLiteUnavailable = errors.New("Colorer is not available in a lite build; use Chroma-based syntax highlighting instead")

// SchemasExist always reports false: a lite build has nowhere to install
// Colorer schemas and no engine that could load them, so the editor's
// highlighter switch (see view.go) never takes the Colorer branch and always
// falls back to Chroma.
func SchemasExist() bool { return false }

// ColorerDownloadURL and MaxColorerDownload are kept so callers that still
// mention them (settings screens) compile unchanged; DownloadColorerSchemas
// never actually fetches anything in a lite build.
var ColorerDownloadURL = "https://github.com/elfmz/far2l/archive/refs/tags/v_2.8.0.zip"

const MaxColorerDownload = 256 << 20

// DownloadColorerSchemas reports failure immediately: a lite build has no
// engine to use the schemas with.
func DownloadColorerSchemas(app vfs.App, onComplete func(success bool)) {
	if onComplete != nil {
		onComplete(false)
	}
}

// InstallColorerSchemas always fails in a lite build.
func InstallColorerSchemas(data []byte, destDir string, ctx context.Context) error {
	return errColorerLiteUnavailable
}

// ColorerSource names the same fields as the real Colorer configuration
// source, so callers that build one (settings screens) compile unchanged; a
// lite build never reads them.
type ColorerSource struct {
	ConfigsDir      string
	UserHRC         string
	UserHRD         string
	UserHRCSettings string
}

// CurrentColorerSource returns a zero value: no configuration to speak of.
func CurrentColorerSource() ColorerSource { return ColorerSource{} }

// ColorerConfigsDir and DefaultColorerConfigsDir return "": there is no
// Colorer configuration directory in a lite build.
func ColorerConfigsDir() string        { return "" }
func DefaultColorerConfigsDir() string { return "" }

// colorerLiteRegionDefine stands in for colorer4go's RegionDefine. It is
// never populated — ColorerGetRegionDefine always returns nil — so its shape
// only has to satisfy code that checks for nil before touching a field.
type colorerLiteRegionDefine struct {
	Fore      uint32
	Back      uint32
	Style     uint32
	IsForeSet bool
	IsBackSet bool
}

// ColorerGetRegionDefine always returns nil: there is no active Colorer style
// to read a region from.
func ColorerGetRegionDefine(region string) *colorerLiteRegionDefine { return nil }

// ColorerCheck mirrors the real diagnostic result so settings screens that
// display Err/Reports/Types compile unchanged.
type ColorerCheck struct {
	Err     error
	Reports []string
	Types   int
}

// Clean reports whether nothing failed and nothing was reported.
func (c ColorerCheck) Clean() bool { return c.Err == nil && len(c.Reports) == 0 }

// CheckColorerSource always reports that Colorer is unavailable.
func CheckColorerSource(ctx context.Context, src ColorerSource, scheme string, allTypes bool, progress func(done, total int, label string)) ColorerCheck {
	return ColorerCheck{Err: errColorerLiteUnavailable}
}

// IsColorerCheckCancelled mirrors the real helper's contract.
func IsColorerCheckCancelled(c ColorerCheck) bool {
	return errors.Is(c.Err, context.Canceled)
}

// ColorerScheme mirrors the real one field-for-field.
type ColorerScheme struct {
	Name        string
	Description string
}

func ListColorerSchemes() []ColorerScheme                     { return nil }
func ListColorerSchemesFor(src ColorerSource) []ColorerScheme { return nil }
func ResetColorerSchemesCache()                               {}

// ColorerSchemeLabel mirrors the real formatting rule.
func ColorerSchemeLabel(scheme ColorerScheme) string {
	if label := strings.TrimSpace(scheme.Description); label != "" {
		return label
	}
	return scheme.Name
}

func SetColorerScheme(name string)    {}
func ResetColorerScheme()             {}
func ColorerSchemeGeneration() uint64 { return 0 }

// ResetColorerRegions is a no-op in the real build too (the comment there
// says region graph scanning moved into the C++ engine); a lite build has no
// engine at all, so it stays a no-op.
func ResetColorerRegions() {}

// ResetColorerSessions has no pooled sessions to drop in a lite build.
func ResetColorerSessions() {}

// ReloadColorerEditors has no live Colorer editors to reload.
func ReloadColorerEditors() {}

// ColorerTypeParams and ColorerParam mirror the real per-file-type parameter
// model so the settings dialog that lists them compiles unchanged.
type ColorerTypeParams struct {
	Name        string
	Group       string
	Description string
	Params      []ColorerParam
}

type ColorerParam struct {
	Name        string
	Value       string
	Default     string
	Description string
	UserSet     bool
}

// LoadColorerTypeParams always fails: there is no Colorer session to read
// file types and parameters from.
func LoadColorerTypeParams(src ColorerSource) ([]ColorerTypeParams, error) {
	return nil, errColorerLiteUnavailable
}

// SaveColorerParams always fails: there is nothing to apply the change to.
func SaveColorerParams(changes map[string]map[string]*string) error {
	return errColorerLiteUnavailable
}

// ColorerParamDefaultText, ColorerParamChoices, ColorerParamText and
// ColorerParamEdit mirror the real formatting/editing rules on the same data,
// even though a lite build never has a real ColorerParam to show.
func ColorerParamDefaultText(p ColorerParam) string {
	return "<default-" + p.Default + ">"
}

func ColorerParamChoices(p ColorerParam) (choices []string, fixed bool) {
	return nil, false
}

func ColorerParamText(p ColorerParam) string {
	if p.UserSet {
		return p.Value
	}
	return ColorerParamDefaultText(p)
}

func ColorerParamEdit(p *ColorerParam, text string) (value *string, changed bool) {
	return nil, false
}

// ColorerHighlighter stands in for the real vtui.Highlighter Colorer
// implements. It is never assigned to an EditorView's Highlighter field in a
// lite build (startColorer below falls back to Chroma instead), so every
// `ev.Highlighter.(*ColorerHighlighter)` type assertion elsewhere in the
// package simply reports false; these methods exist only so that the dead
// branches guarded by that assertion still compile.
type ColorerHighlighter struct{}

// Highlight satisfies vtui.Highlighter, which is what ev.Highlighter's field
// type is: without it, the type assertions below would not just always fail,
// they would not compile at all ("impossible type assertion"), since Go
// requires the asserted concrete type to satisfy the interface being
// asserted from.
func (ch *ColorerHighlighter) Highlight(line string, prevState any, baseAttr uint64) ([]uint64, any) {
	return nil, nil
}

func (ch *ColorerHighlighter) beginColorerStartup()                {}
func (ch *ColorerHighlighter) noteLineCount(n int)                 {}
func (ch *ColorerHighlighter) DropFrom(idx int)                    {}
func (ch *ColorerHighlighter) DropAfterEdit(idx, lineCount int)    {}
func (ch *ColorerHighlighter) DropAfterReplace(idx, lineCount int) {}

func (ch *ColorerHighlighter) GetLineBackground(idx int, defaultAttr uint64) uint64 {
	return defaultAttr
}

func (ch *ColorerHighlighter) HighlightLine(idx int, line string, baseAttr uint64) []uint64 {
	return nil
}

func (ch *ColorerHighlighter) pairOverlay(cursorLine, cursorByte int, text string, first, last int) colorerPairOverlay {
	return nil
}

// colorerPairOverlay stands in for the real pair-highlight overlay; empty, it
// leaves attrs untouched.
type colorerPairOverlay map[int][]uint64

func (o colorerPairOverlay) apply(idx int, attrs []uint64) []uint64 { return attrs }

// ColorerPairAction and its constants mirror the real ones so action
// handlers (internal/app/actions_table.go) compile unchanged.
type ColorerPairAction int

const (
	ColorerMatchPair ColorerPairAction = iota
	ColorerSelectPair
	ColorerSelectBlock
)

// startColorer replaces the real Colorer startup: a lite build hands the
// editor straight to Chroma. It is unreachable in practice — view.go only
// takes this path when SchemasExist() is true, which it never is here — but
// it stays a real fallback rather than a panic in case that ever changes.
func (ev *EditorView) startColorer(path, filename, firstLine string) *ColorerHighlighter {
	ev.Highlighter = vtui.GetHighlighter(path, "")
	return nil
}

func (ev *EditorView) cancelColorer()             {}
func (ev *EditorView) restartColorerAfterReload() {}

// colorerBaseAttr mirrors the real one's result when no ColorerHighlighter is
// in charge (which is always, in a lite build): the editor's own text colour.
func (ev *EditorView) colorerBaseAttr() uint64 {
	return vtui.Palette[theme.ColEditorText]
}

// colorerCrossAxes mirrors the real one's result when no ColorerHighlighter
// is in charge.
func (ev *EditorView) colorerCrossAxes(horz, vert bool) (bool, bool) {
	if config.App.EditorCrossMode == config.ColorerCrossScheme {
		return false, false
	}
	return horz, vert
}

// The Colorer menu actions (internal/editor/base64.go's editor menu, and
// internal/app/actions_table.go's command palette entries) become no-ops:
// the menu item stays visible for consistency with the full build, but there
// is no Colorer session to act on.
func (ev *EditorView) ColorerChooseType()             {}
func (ev *EditorView) ColorerPair(ColorerPairAction)  {}
func (ev *EditorView) ColorerListOutline(errors bool) {}
func (ev *EditorView) ColorerSelectRegion()           {}
func (ev *EditorView) ColorerLocateFunction()         {}
func (ev *EditorView) ColorerUpdateHighlighting()     {}

// viewerHighlightAllows mirrors the real helper: whether the viewer
// highlighting setting covers this text.
func viewerHighlightAllows(quickView bool) bool {
	switch config.App.ViewerHighlighting {
	case config.ViewerHighlightQuickView:
		return quickView
	case config.ViewerHighlightAll:
		return true
	}
	return false
}

// colorerTextBatch mirrors the real batching size for the quick-view text
// colorizer below.
const colorerTextBatch = 200

// colorerTextColorizer is viewer.TextColorizer's concrete type here: the same
// shape as the real one, minus the Colorer branch.
type colorerTextColorizer struct {
	cancel context.CancelFunc
	attrs  [][]uint64
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

// NewTextColorizer is viewer.NewTextColorizer for a lite build: None stays
// None, everything else is Chroma. colorerSetups (colorer_setups.go, kept in
// every build as a plain goroutine counter) still tracks this goroutine so
// tests and shutdown code that wait for it behave the same as the full build.
func NewTextColorizer(path string, lines []string, base uint64, quickView bool, redraw func()) viewer.TextColorizer {
	if len(lines) == 0 || !viewerHighlightAllows(quickView) || vtui.FrameManager == nil {
		return nil
	}
	if strings.EqualFold(config.App.EditorHighlighter, "None") {
		return nil
	}
	frames := vtui.FrameManager
	ctx, cancel := context.WithCancel(context.Background())
	c := &colorerTextColorizer{cancel: cancel, attrs: make([][]uint64, len(lines))}
	snapshot := append([]string(nil), lines...)
	colorerSetups.start()
	go func() {
		defer colorerSetups.done()
		out := &textColorizerBatches{ctx: ctx, frames: frames, c: c, redraw: redraw}
		colourTextWithChroma(ctx, path, snapshot, base, out)
	}()
	return c
}

// textColorizerBatches hands a colorizer's colours to the UI thread in
// batches, in line order, exactly as the real one does.
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

// colourTextWithChroma is the real helper of the same name, unchanged: it
// never touched Colorer to begin with.
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

// windowJob, windowColored and windowColorizer are viewer.WindowColorizer's
// concrete type here: the same shape as the real one, minus the Colorer
// branch.
type windowJob struct {
	context []string
	lines   []viewer.WindowLine
}

type windowColored struct {
	text  string
	attrs []uint64
}

type windowColorizer struct {
	jobs    chan windowJob
	cancel  context.CancelFunc
	colors  map[int64]windowColored
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

// NewWindowColorizer is viewer.NewWindowColorizer for a lite build: always
// Chroma, never Colorer.
func NewWindowColorizer(path, firstLine string, base uint64, redraw func()) viewer.WindowColorizer {
	if !viewerHighlightAllows(false) || vtui.FrameManager == nil || strings.EqualFold(config.App.EditorHighlighter, "None") {
		return nil
	}
	frames := vtui.FrameManager
	ctx, cancel := context.WithCancel(context.Background())
	w := &windowColorizer{jobs: make(chan windowJob, 1), cancel: cancel}
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
		h := vtui.GetHighlighter(path, "")
		colorerSetups.done()
		if h == nil {
			return
		}
		if closer, ok := h.(interface{ Close() error }); ok {
			defer func() { _ = closer.Close() }()
		}
		for {
			var job windowJob
			select {
			case <-ctx.Done():
				return
			case job = <-w.jobs:
			}
			colors := make(map[int64]windowColored, len(job.lines))
			var state any
			for _, line := range job.context {
				_, state = h.Highlight(line, state, base)
			}
			for _, line := range job.lines {
				var attrs []uint64
				attrs, state = h.Highlight(line.Text, state, base)
				colors[line.Offset] = windowColored{text: line.Text, attrs: attrs}
			}
			if ctx.Err() != nil {
				return
			}
			post(colors, base)
		}
	}()
	return w
}
