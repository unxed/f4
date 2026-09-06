package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/unxed/f4/vfs"
)

// Folder comparison, as Far3 offers it in two places at once: the built-in
// "Compare folders" command, which marks what one panel has and the other
// does not, and the Advanced Compare dialog, which adds recursion, a
// marked-only scope and a real content comparison. f4 has a single command
// carrying the options of the second one, with the first one's behaviour
// (name plus time and size) as the default.
//
// Everything in this file is deliberately free of UI: the dialog and the
// progress reporting live in compare_folders_ui.go, so the comparison
// itself can be tested against a temporary directory.

// What "ignore" means when comparing contents.
const (
	// compareIgnoreEOL treats CRLF, CR and LF as the same line break, so a
	// file that has travelled through Windows still equals its Unix twin.
	compareIgnoreEOL = iota
	// compareIgnoreSpaces drops every whitespace byte, which also covers
	// line breaks: indentation changes stop counting as differences.
	compareIgnoreSpaces
)

const (
	// compareMaxDepthLimit is the nesting level Far3's dialog offers, and
	// the largest value the field accepts.
	compareMaxDepthLimit = 99
	// compareTimeSlack is Far's "two-second precision": FAT stores the
	// modification time in two-second steps, so a file copied from one is
	// routinely a second away from its source.
	compareTimeSlack = 2 * time.Second
	// compareZoneStep is the granularity of time zone offsets. Every zone
	// in use is a whole number of quarter hours away from UTC, so a
	// difference that is a multiple of one may be a zone artifact rather
	// than a real edit.
	compareZoneStep = 15 * time.Minute
	// compareZoneSpan bounds that rule. The inhabited zones run from
	// UTC-12 to UTC+14, so nothing further apart than their spread can be
	// explained by a zone — and without the bound, two files a year apart
	// to the second would pass as equal.
	compareZoneSpan = 26 * time.Hour
	// compareChunkSize is how much of a file is read at a time when
	// comparing contents.
	compareChunkSize = 64 * 1024
)

// compareOptions mirrors the Advanced Compare dialog field for field.
type compareOptions struct {
	// Recursive walks subfolders instead of comparing only what the two
	// panels show side by side.
	Recursive bool
	// LimitDepth caps that walk at MaxDepth levels below the panel folder.
	LimitDepth bool
	MaxDepth   int
	// MarkedOnly narrows the comparison to the items marked in each panel.
	MarkedOnly bool

	// ByTime, BySize and ByContent are the comparison criteria. A pair of
	// files differs as soon as one of the enabled criteria says so.
	ByTime bool
	// TimeSlack allows compareTimeSlack between two modification times.
	TimeSlack bool
	// IgnoreZones additionally ignores differences that are a whole
	// number of quarter hours, i.e. a file stamped in another time zone.
	IgnoreZones bool
	BySize      bool
	ByContent   bool

	// Ignore enables the content filter selected by IgnoreMode.
	Ignore     bool
	IgnoreMode int

	// ReportEqual asks for a message when the comparison found nothing.
	// Without it a comparison of two identical folders looks like a
	// command that did not run.
	ReportEqual bool
}

// defaultCompareOptions is Far's built-in "Compare folders": names, times
// and sizes, across the whole tree, and a word when nothing differs.
func defaultCompareOptions() compareOptions {
	return compareOptions{
		Recursive:   true,
		MaxDepth:    compareMaxDepthLimit,
		ByTime:      true,
		TimeSlack:   true,
		IgnoreZones: true,
		BySize:      true,
		IgnoreMode:  compareIgnoreEOL,
		ReportEqual: true,
	}
}

// normalize repairs values a hand-edited config may hold and reports the
// options actually usable. Nothing here silently turns a criterion on: a
// comparison with no criterion at all is refused by the dialog instead.
func (o compareOptions) normalize() compareOptions {
	if o.MaxDepth < 1 {
		o.MaxDepth = 1
	}
	if o.MaxDepth > compareMaxDepthLimit {
		o.MaxDepth = compareMaxDepthLimit
	}
	if o.IgnoreMode != compareIgnoreSpaces {
		o.IgnoreMode = compareIgnoreEOL
	}
	return o
}

// hasCriteria reports whether anything at all is being compared. Presence
// alone is not a criterion: two folders holding the same names would then
// always come back equal, whatever the files inside them look like.
func (o compareOptions) hasCriteria() bool {
	return o.ByTime || o.BySize || o.ByContent
}

// compareItem is one file or folder found below a panel's folder.
type compareItem struct {
	// rel is the path relative to the panel folder, always slash
	// separated so both sides compare as strings regardless of the file
	// system they came from.
	rel string
	// top is the first component of rel, i.e. the panel entry that gets
	// marked when this item turns out to differ.
	top string
	// full is the path in the item's own file system.
	full string
	item vfs.VFSItem
}

// compareOutcome is what a comparison marks, and what it looked at.
type compareOutcome struct {
	// left and right hold panel entry names, not relative paths: a
	// difference three folders down marks the top-level folder the user
	// can actually see, the way Far does.
	left  map[string]bool
	right map[string]bool
	// compared counts the pairs examined, differing the pairs (and lone
	// items) that turned out not to match.
	compared  int
	differing int
	// readErr is the first file that could not be read. Its pair is
	// treated as differing — an unreadable file is not a match — but the
	// comparison carries on and the error is reported once at the end.
	readErr error
}

func newCompareOutcome() *compareOutcome {
	return &compareOutcome{left: make(map[string]bool), right: make(map[string]bool)}
}

// markLeft and markRight record the panel entry to select.
func (o *compareOutcome) markLeft(item compareItem)  { o.left[item.top] = true }
func (o *compareOutcome) markRight(item compareItem) { o.right[item.top] = true }

// compareProgress is called while the tree is walked and while pairs are
// compared, so the dialog can show where the work currently is.
type compareProgress func(path string, done, total int)

// compareTimesEqual answers whether two modification times count as the
// same one under the current options.
func compareTimesEqual(a, b time.Time, opts compareOptions) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	slack := time.Duration(0)
	if opts.TimeSlack {
		slack = compareTimeSlack
	}
	if d <= slack {
		return true
	}
	if opts.IgnoreZones && d <= compareZoneSpan+slack {
		// A zone difference is a multiple of a quarter of an hour; the
		// slack applies around that multiple, not only around zero.
		rest := d % compareZoneStep
		if rest <= slack || compareZoneStep-rest <= slack {
			return true
		}
	}
	return false
}

// compareMetadata compares two files by everything that can be answered
// from the directory listing alone. It reports whether the metadata
// differs, whether that difference is one of time only, and which side is
// the newer one (1 left, -1 right, 0 neither).
func compareMetadata(a, b vfs.VFSItem, opts compareOptions) (differs, timeOnly bool, newer int) {
	sizeDiffers := opts.BySize && a.Size != b.Size
	timeDiffers := opts.ByTime && !compareTimesEqual(a.MTime, b.MTime, opts)
	if timeDiffers {
		switch {
		case a.MTime.After(b.MTime):
			newer = 1
		case b.MTime.After(a.MTime):
			newer = -1
		}
	}
	return sizeDiffers || timeDiffers, timeDiffers && !sizeDiffers, newer
}

// compareSizeKnown reports whether a listing gave a usable length. A
// remote object may not know its own size until it is opened, and VFSItem
// documents non-zero Size as always known.
func compareSizeKnown(item vfs.VFSItem) bool {
	return item.SizeKnown || item.Size != 0
}

// firstPathComponent returns the panel entry a relative path lives under.
func firstPathComponent(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// collectCompareSide walks one panel's folder and returns everything worth
// comparing, keyed by relative path. allow, when not nil, restricts the
// walk to those top-level names — this is the "only marked" scope.
//
// A subfolder that cannot be read is skipped rather than aborting the
// whole comparison: one unreadable folder should not cost the answer about
// every other one. An unreadable panel folder is fatal, because then there
// is nothing to compare at all.
func collectCompareSide(ctx context.Context, v vfs.VFS, root string, allow map[string]bool, opts compareOptions, progress func(string)) (map[string]compareItem, error) {
	if v == nil {
		return nil, errors.New("compare: no file system")
	}
	items := make(map[string]compareItem)
	type frame struct {
		full  string
		rel   string
		depth int
	}
	stack := []frame{{full: root}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if progress != nil {
			progress(cur.rel)
		}

		var children []vfs.VFSItem
		err := v.ReadDir(ctx, cur.full, func(chunk []vfs.VFSItem) {
			children = append(children, chunk...)
		})
		if err != nil {
			if cur.depth == 0 {
				return nil, err
			}
			continue
		}

		for _, child := range children {
			if child.Name == "." || child.Name == ".." || child.Name == "" {
				continue
			}
			if cur.depth == 0 && allow != nil && !allow[child.Name] {
				continue
			}
			if child.IsDir && !opts.Recursive {
				// Far's built-in comparison is about files. Without
				// recursion a folder carries no information beyond its
				// name, and marking it would promise a comparison that
				// did not happen.
				continue
			}
			rel := child.Name
			top := child.Name
			if cur.rel != "" {
				rel = cur.rel + "/" + child.Name
				top = firstPathComponent(cur.rel)
			}
			full := v.Join(cur.full, child.Name)
			items[rel] = compareItem{rel: rel, top: top, full: full, item: child}

			if !child.IsDir || child.IsSymlink {
				// A symlinked folder is a leaf: following it invites a
				// loop, and its target is compared where it really lives.
				continue
			}
			if opts.LimitDepth && cur.depth+1 > opts.MaxDepth {
				continue
			}
			stack = append(stack, frame{full: full, rel: rel, depth: cur.depth + 1})
		}
	}
	return items, nil
}

// compareSides is the comparison proper: it pairs the two collections by
// relative path and decides, for every pair, which side to mark.
func compareSides(ctx context.Context, leftFS, rightFS vfs.VFS, left, right map[string]compareItem, opts compareOptions, progress compareProgress) (*compareOutcome, error) {
	out := newCompareOutcome()

	keys := make([]string, 0, len(left)+len(right))
	for rel := range left {
		keys = append(keys, rel)
	}
	for rel := range right {
		if _, both := left[rel]; !both {
			keys = append(keys, rel)
		}
	}
	sort.Strings(keys)

	skip := compareSkipMode(opts)
	for i, rel := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if progress != nil {
			progress(rel, i, len(keys))
		}

		l, hasLeft := left[rel]
		r, hasRight := right[rel]
		switch {
		case !hasRight:
			out.differing++
			out.markLeft(l)
			continue
		case !hasLeft:
			out.differing++
			out.markRight(r)
			continue
		}

		if l.item.IsDir && r.item.IsDir {
			// Two folders of the same name are the container of the
			// comparison, not a subject of it.
			continue
		}
		out.compared++
		if l.item.IsDir != r.item.IsDir {
			// A file on one side and a folder on the other is the
			// starkest difference there is.
			out.differing++
			out.markLeft(l)
			out.markRight(r)
			continue
		}

		differs, timeOnly, newer := compareMetadata(l.item, r.item, opts)
		if opts.ByContent && !differs {
			equal, err := compareContents(ctx, leftFS, l, rightFS, r, skip)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}
				if out.readErr == nil {
					out.readErr = err
				}
				equal = false
			}
			if !equal {
				differs, timeOnly = true, false
			}
		}
		if !differs {
			continue
		}

		out.differing++
		// Far marks the newer file when the only difference is the time:
		// that is the one worth copying over. Any other difference says
		// nothing about direction, so both sides are marked.
		switch {
		case timeOnly && newer > 0:
			out.markLeft(l)
		case timeOnly && newer < 0:
			out.markRight(r)
		default:
			out.markLeft(l)
			out.markRight(r)
		}
	}
	return out, nil
}

// compareSkipMode turns the ignore options into the content filter mode,
// where -1 means "compare the bytes as they are".
func compareSkipMode(opts compareOptions) int {
	if !opts.Ignore {
		return -1
	}
	if opts.IgnoreMode == compareIgnoreSpaces {
		return compareIgnoreSpaces
	}
	return compareIgnoreEOL
}

// compareContents answers whether two files hold the same bytes, under the
// filter the options asked for.
func compareContents(ctx context.Context, leftFS vfs.VFS, left compareItem, rightFS vfs.VFS, right compareItem, skip int) (bool, error) {
	if skip < 0 && compareSizeKnown(left.item) && compareSizeKnown(right.item) && left.item.Size != right.item.Size {
		// Different lengths cannot hold the same bytes, and not opening
		// the files at all is the whole point of asking first.
		return false, nil
	}

	lf, err := leftFS.Open(ctx, left.full)
	if err != nil {
		return false, fmt.Errorf("%s: %w", left.full, err)
	}
	defer lf.Close()
	rf, err := rightFS.Open(ctx, right.full)
	if err != nil {
		return false, fmt.Errorf("%s: %w", right.full, err)
	}
	defer rf.Close()

	return compareStreams(newCompareStream(ctx, lf, skip), newCompareStream(ctx, rf, skip))
}

// compareStream reads a file in chunks and hands out the filtered bytes.
type compareStream struct {
	ctx  context.Context
	file vfs.ReadAtCloser
	// off is tracked here rather than relying on the sequential Read:
	// ReadAt is the call every VFS implements for the viewer, so it is
	// the one that can be relied on.
	off  int64
	skip int
	raw  []byte
	work []byte
	// buf is what has been read and filtered but not yet compared.
	buf []byte
	// pendingCR remembers a carriage return at the very end of a chunk,
	// so a CRLF split across two reads still collapses into one break.
	pendingCR bool
	eof       bool
}

func newCompareStream(ctx context.Context, file vfs.ReadAtCloser, skip int) *compareStream {
	return &compareStream{ctx: ctx, file: file, skip: skip, raw: make([]byte, compareChunkSize)}
}

// fill makes sure buf holds at least one byte, unless the file is over.
func (s *compareStream) fill() error {
	for len(s.buf) == 0 && !s.eof {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		n, err := s.file.ReadAt(s.ctx, s.raw, s.off)
		if n > 0 {
			s.off += int64(n)
			s.buf = s.normalize(s.raw[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				s.eof = true
				continue
			}
			return err
		}
		if n == 0 {
			s.eof = true
		}
	}
	return nil
}

// normalize applies the ignore filter to one chunk. Without a filter the
// chunk is handed on as it is, so the common case copies nothing.
func (s *compareStream) normalize(chunk []byte) []byte {
	switch s.skip {
	case compareIgnoreSpaces:
		out := s.work[:0]
		for _, b := range chunk {
			switch b {
			case ' ', '\t', '\r', '\n', '\v', '\f':
			default:
				out = append(out, b)
			}
		}
		s.work = out
		return out
	case compareIgnoreEOL:
		out := s.work[:0]
		for _, b := range chunk {
			switch b {
			case '\r':
				out = append(out, '\n')
				s.pendingCR = true
			case '\n':
				if s.pendingCR {
					s.pendingCR = false
					continue
				}
				out = append(out, '\n')
			default:
				s.pendingCR = false
				out = append(out, b)
			}
		}
		s.work = out
		return out
	default:
		return chunk
	}
}

// compareStreams walks both files side by side and stops at the first
// difference.
func compareStreams(a, b *compareStream) (bool, error) {
	for {
		if err := a.fill(); err != nil {
			return false, err
		}
		if err := b.fill(); err != nil {
			return false, err
		}
		if len(a.buf) == 0 || len(b.buf) == 0 {
			// One of them ran out: they match only if both did.
			return len(a.buf) == 0 && len(b.buf) == 0, nil
		}
		n := len(a.buf)
		if len(b.buf) < n {
			n = len(b.buf)
		}
		if !bytes.Equal(a.buf[:n], b.buf[:n]) {
			return false, nil
		}
		a.buf = a.buf[n:]
		b.buf = b.buf[n:]
	}
}
