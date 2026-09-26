//go:build !lite

package editor

import (
	"context"
	"time"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

// colorerJob contains only immutable data captured on the UI thread. In
// particular, the worker never calls EditorView.lineTextForHighlight while
// the document can be edited.
type colorerJob struct {
	id           uint64
	generation   uint64
	target       int
	lines        []string
	contextStart int
	context      []string
	reset        bool
	baseAttr     uint64
	syntax       bool
	total        int
	// fileType is the type the user picked for the file, "" to choose by
	// file name.
	fileType string
}

type colorerLineAttrs struct {
	attrs      []uint64
	background uint64
	pairs      []colorer.Pair
	outline    []colorerOutlineEntry
	regions    []colorerRegionSpan
}

type colorerResult struct {
	job   colorerJob
	lines []colorerLineAttrs
	// partial marks a batch cut short by a parse error on a prefetched line:
	// the attributes up to the bad line are good, but the session's position
	// is unknown, so the next job must re-anchor.
	partial bool
	err     error
}

// startWorker transfers ownership of session calls to one goroutine. A
// Colorer Session is stateful, so a pool of concurrent calls would be wrong;
// one worker also gives cancellation a single well-defined owner.
func (ch *ColorerHighlighter) startWorker(session *colorer.Session) {
	if ch.closed {
		session.Close()
		return
	}
	if ch.sessionCtx == nil {
		ch.sessionCtx, ch.sessionCancel = context.WithCancel(context.Background())
	}

	ch.workerJobs = make(chan colorerJob, 1)
	ch.workerDone = make(chan struct{})
	go ch.runWorker(ch.sessionCtx, session)
}

func (ch *ColorerHighlighter) runWorker(ctx context.Context, session *colorer.Session) {
	defer close(ch.workerDone)
	defer session.Close()

	parsedIdx := 0
	forgottenUpTo := 0
	forgetDisabled := false
	var baseAttr uint64
	var schemeGen uint64
	baseKnown := false

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-ch.workerJobs:
			if err := ch.prepareWorkerSession(ctx, session, job, &parsedIdx, &forgottenUpTo); err != nil {
				ch.postColorerResult(colorerResult{job: job, err: err})
				return
			}

			generation := ColorerSchemeGeneration()
			if !baseKnown || baseAttr != job.baseAttr || schemeGen != generation {
				activeScheme := currentColorerSchemeName()
				if err := session.SetHRD("rgb", activeScheme); err != nil {
					ch.postColorerResult(colorerResult{job: job, err: err})
					return
				}
				baseAttr = job.baseAttr
				baseKnown = true
				schemeGen = generation
			}

			for i, contextLine := range job.context {
				if ctx.Err() != nil {
					return
				}
				if _, err := session.ParseLine(ch.workerTypeSettings.truncate(contextLine)); err != nil {
					vtui.DebugLog("COLORER: ParseLine failed in context at line %d: %v", job.contextStart+i, err)
					ch.postColorerResult(colorerResult{job: job, err: err})
					return
				}
				parsedIdx++
				if i == len(job.context)-1 || i%16 == 0 {
					ch.postColorerProgress(job.id, job.total, i+1)
				}
			}

			if parsedIdx != job.target {
				err := colorerWorkerStateError{want: job.target, got: parsedIdx}
				ch.postColorerResult(colorerResult{job: job, err: err})
				return
			}

			// No progress posts inside the batch: the attributes only land on
			// screen with the final result, so a mid-batch redraw would paint
			// nothing new — the per-line render cost the batch exists to avoid.
			lineResults := make([]colorerLineAttrs, 0, len(job.lines))
			partial := false
			for i, lineText := range job.lines {
				if ctx.Err() != nil {
					return
				}
				lineText = ch.workerTypeSettings.truncate(lineText)
				regions, pairs, err := session.ParseLinePairs(lineText)
				var outline []colorer.OutlineItem
				if err == nil {
					outline, err = session.LineOutline()
				}
				if err != nil {
					vtui.DebugLog("COLORER: ParseLine failed at line %d: %v", job.target+i, err)
					if len(lineResults) == 0 {
						// The line the viewport actually asked for cannot be
						// parsed; nothing to salvage.
						ch.postColorerResult(colorerResult{job: job, err: err})
						return
					}
					// A prefetched line failed: keep the finished attributes
					// instead of throwing the whole batch away. Whether the
					// session consumed the bad line is unknown, so its
					// position is unknown too — the next job must re-anchor.
					partial = true
					break
				}
				parsedIdx++
				attrs, background := ch.attrsForSyntax(lineText, regions, job.baseAttr, job.syntax)
				lineResults = append(lineResults, colorerLineAttrs{attrs: attrs, background: background, pairs: pairs, outline: colorerOutlineEntries(job.target+i, lineText, outline), regions: colorerRegionSpans(regions)})
			}
			if ctx.Err() != nil {
				return
			}
			if partial {
				parsedIdx = -1 // never matches a contextStart: forces a reset
			} else if !forgetDisabled {
				if keepFrom, do := colorerForgetPlan(parsedIdx, forgottenUpTo); do {
					if err := session.ForgetBefore(keepFrom); err != nil {
						vtui.DebugLog("COLORER: ForgetBefore unsupported, disabling for this session: %v", err)
						forgetDisabled = true
					} else {
						forgottenUpTo = keepFrom
					}
				}
			}
			ch.postColorerResult(colorerResult{
				job:     job,
				lines:   lineResults,
				partial: partial,
			})
		}
	}
}

type colorerWorkerStateError struct {
	want int
	got  int
}

func (e colorerWorkerStateError) Error() string {
	return "colorer worker state mismatch"
}

func currentColorerSchemeName() string {
	schemeMu.Lock()
	name := schemeName
	schemeMu.Unlock()
	if name == "" {
		return "default"
	}
	return name
}

func (ch *ColorerHighlighter) prepareWorkerSession(ctx context.Context, session *colorer.Session, job colorerJob, parsedIdx, forgottenUpTo *int) error {
	typeChanged := job.fileType != ch.workerFileType
	needReset := job.reset || *parsedIdx != job.contextStart || typeChanged
	if needReset {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		session.Reset()
		chosen := false
		if job.fileType != "" {
			ok, err := session.SetFileType(job.fileType)
			if err != nil {
				return err
			}
			chosen = ok
		}
		if !chosen {
			if _, err := session.SelectType(ch.filename, ch.firstLine); err != nil {
				return err
			}
		}
		ch.workerFileType = job.fileType
		if typeChanged {
			// The type's own parameters come with it.
			if settings := readColorerTypeSettings(session); settings != ch.workerTypeSettings {
				ch.workerTypeSettings = settings
				if ch.postTask != nil {
					ch.postTask(func() { ch.adoptTypeSettings(settings) })
				}
			}
		}
		*parsedIdx = job.contextStart
		*forgottenUpTo = job.contextStart
	}
	return nil
}

// queueLine is called by the UI render path. It may read the document, but it
// never calls into Colorer. A jump is re-anchored to at most
// hlColorerContext lines, and ordinary scrolling snapshots only the missing
// forward lines.
//
// One job covers the whole uncoloured run starting at idx, up to
// hlColorerBatchLines: colouring a viewport in one round trip is what keeps
// the screen from filling in visibly line by line, one redraw per line.
func (ch *ColorerHighlighter) queueLine(idx int, line string, baseAttr uint64) {
	if ch.pending || ch.workerJobs == nil || ch.lineAt == nil {
		return
	}

	start, reset := colorerContextPlan(ch.parsedIdx, idx)
	// Do not make a large document read on the UI thread merely because the
	// parser could theoretically be fed forward. A jump gets the same bounded
	// re-anchor as the worker's reset path.
	if !reset && idx-start > hlColorerContext {
		reset = true
		start = idx - hlColorerContext
		if start < 0 {
			start = 0
		}
	}
	if ch.forceReset {
		reset = true
		start = idx - hlColorerContext
		if start < 0 {
			start = 0
		}
	}

	contextLines := make([]string, 0, idx-start)
	for lineIdx := start; lineIdx < idx; lineIdx++ {
		contextLine, ok := ch.lineAt(lineIdx)
		if !ok {
			return
		}
		contextLines = append(contextLines, contextLine)
	}

	// The batch stops at the first line that is already coloured or whose
	// text is not available yet — the next frame picks up from there — and
	// at the byte budget, which keeps this snapshot loop from copying
	// megabytes inside a frame when lines are huge.
	batch := make([]string, 0, hlColorerBatchLines)
	batch = append(batch, line)
	batchBytes := len(line)
	for lineIdx := idx + 1; len(batch) < hlColorerBatchLines && batchBytes < hlColorerBatchBytes; lineIdx++ {
		if _, cached := ch.attrCache[lineIdx]; cached {
			break
		}
		text, ok := ch.lineAt(lineIdx)
		if !ok {
			break
		}
		batch = append(batch, text)
		batchBytes += len(text)
	}

	ch.workGeneration++
	job := colorerJob{
		id:           ch.workGeneration,
		generation:   ch.workGeneration,
		target:       idx,
		lines:        batch,
		contextStart: start,
		context:      contextLines,
		reset:        reset,
		baseAttr:     baseAttr,
		syntax:       config.App.EditorColorerSyntax,
		total:        len(contextLines) + len(batch),
		fileType:     ch.fileTypeOverride,
	}
	ch.forceReset = false
	ch.pending = true
	ch.beginColorerWork(job.id, job.total)

	select {
	case ch.workerJobs <- job:
	default:
		ch.pending = false
	}
}

// postColorerProgress takes the job's id and total, not the job: the queued
// closure would otherwise keep the whole line snapshot — up to hundreds of
// lines of text — alive until the UI thread gets around to it.
func (ch *ColorerHighlighter) postColorerProgress(jobID uint64, total, done int) {
	if ch.postTask == nil || ch.owner == nil {
		return
	}
	postTask := ch.postTask
	redraw := ch.redraw
	owner := ch.owner
	postTask(func() {
		if ch.closed || owner.Highlighter != vtui.Highlighter(ch) || owner.colorerWorkID != jobID {
			return
		}
		owner.colorerProgress = done
		owner.colorerTotal = total
		if redraw != nil {
			redraw()
		}
	})
}

func (ch *ColorerHighlighter) postColorerResult(result colorerResult) {
	if ch.postTask == nil || ch.owner == nil {
		return
	}
	postTask := ch.postTask
	redraw := ch.redraw
	owner := ch.owner
	postTask(func() {
		if ch.closed || owner.Highlighter != vtui.Highlighter(ch) {
			return
		}
		if ch.workGeneration != result.job.generation {
			ch.pending = false
			if redraw != nil {
				redraw()
			}
			return
		}
		ch.pending = false
		if result.err != nil {
			ch.disabled = true
			vtui.DebugLog("COLORER: background highlighting stopped: %v", result.err)
		} else {
			ch.parsedIdx = result.job.target + len(result.lines)
			for i, lineAttrs := range result.lines {
				ch.storeAttrs(result.job.target+i, lineAttrs.attrs, lineAttrs.background, lineAttrs.pairs)
				ch.storeOutline(result.job.target+i, lineAttrs.outline)
				ch.storeRegions(result.job.target+i, lineAttrs.regions)
			}
			if result.partial {
				// The worker lost the session's position on the failed line;
				// forward-feeding from here would cold-start the parser with
				// no context. The next job re-anchors instead.
				ch.forceReset = true
			}
		}
		owner.finishColorerWork(result.job.id)
		ch.continuePairSearch()
		ch.continueOutline()
		if redraw != nil {
			redraw()
		}
	})
}

func (ch *ColorerHighlighter) beginColorerWork(id uint64, total int) {
	if ch.owner == nil || ch.owner.Highlighter != vtui.Highlighter(ch) {
		return
	}
	ch.owner.colorerWorkID = id
	ch.owner.colorerIndexing = true
	ch.owner.colorerProgress = 0
	ch.owner.colorerTotal = total
	ch.owner.colorerCancel = ch.Cancel
}

func (ch *ColorerHighlighter) beginColorerStartup() {
	if ch.owner == nil || ch.owner.Highlighter != vtui.Highlighter(ch) {
		return
	}
	ch.workGeneration++
	ch.startupWorkID = ch.workGeneration
	ch.owner.colorerWorkID = ch.startupWorkID
	ch.owner.colorerIndexing = true
	ch.owner.colorerProgress = 0
	ch.owner.colorerTotal = 1
	ch.owner.colorerCancel = ch.Cancel
}

func (ch *ColorerHighlighter) Cancel() {
	ch.disabled = true
	ch.pending = false
	ch.workGeneration++
	ch.forceReset = false
	if ch.sessionCancel != nil {
		ch.sessionCancel()
	}
}

func (ch *ColorerHighlighter) stopWorker() {
	if ch.workerDone == nil {
		return
	}
	select {
	case <-ch.workerDone:
	case <-time.After(250 * time.Millisecond):
		// The session owns the remaining call and will close itself when it
		// observes context cancellation. The editor must not wait forever for
		// a parser while it is closing.
	}
}

func (ev *EditorView) finishColorerWork(id uint64) {
	if ev.colorerWorkID != id {
		return
	}
	ev.colorerIndexing = false
	ev.colorerProgress = 0
	ev.colorerTotal = 0
	ev.colorerCancel = nil
}

func (ev *EditorView) cancelColorer() {
	if ev.colorerCancel != nil {
		cancel := ev.colorerCancel
		ev.colorerCancel = nil
		cancel()
	} else if ch, ok := ev.Highlighter.(*ColorerHighlighter); ok {
		ch.Cancel()
	}
	ev.colorerWorkID++
	ev.colorerIndexing = false
	ev.colorerProgress = 0
	ev.colorerTotal = 0
}
