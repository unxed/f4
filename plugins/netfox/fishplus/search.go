package fishplus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultGrepLimit caps how many matches one request brings back. A search
// across a large log can match millions of times, and nobody scrolls through
// millions of hits; the caller asks again with a narrower pattern instead.
const DefaultGrepLimit = 10000

// ErrNoGrep is returned when the remote host lacks the tools the search is
// built from. The caller is expected to fall back to reading the file.
var ErrNoGrep = errors.New("fishplus: the remote host cannot search files")

// GrepOptions selects how the pattern is interpreted and how much comes back.
type GrepOptions struct {
	// Fixed treats the pattern as a plain string rather than as an extended
	// regular expression.
	Fixed bool
	// IgnoreCase folds case the way the remote grep does it, which for a
	// non-ASCII pattern is the remote locale's idea of case.
	IgnoreCase bool
	// Limit caps the number of matches; zero means DefaultGrepLimit.
	Limit int
}

func (o GrepOptions) mode() string {
	m := "e"
	if o.Fixed {
		m = "f"
	}
	if o.IgnoreCase {
		m += "i"
	}
	return m
}

// CanGrep reports whether the remote host announced the tools the search
// needs. A host without them is not an error, it is a fallback to reading.
func (c *Client) CanGrep() bool {
	feats := c.sess.Features()
	return feats.Has("grep") && feats.Has("awk")
}

// Grep runs the search on the remote host and returns the byte offset of
// every match, in the order the file has them. Only the offsets travel: the
// matched text stays where it is, which is the whole reason for searching
// remotely instead of downloading the file.
func (c *Client) Grep(ctx context.Context, p, pattern string, opts GrepOptions) ([]int64, error) {
	if pattern == "" {
		return nil, errors.New("fishplus: empty search pattern")
	}
	if !c.CanGrep() {
		return nil, ErrNoGrep
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultGrepLimit
	}
	resp, err := c.sess.ExecPaths(ctx, "grep", []string{pattern, p}, opts.mode(), strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	if err := resp.Err("grep " + p); err != nil {
		return nil, err
	}
	offsets := make([]int64, 0, len(resp.Lines))
	for _, line := range resp.Lines {
		if line == "" {
			continue
		}
		off, convErr := strconv.ParseInt(line, 10, 64)
		if convErr != nil {
			// A diagnostic line from a remote tool must not cost the caller
			// the matches that did parse.
			continue
		}
		offsets = append(offsets, off)
	}
	if len(offsets) > limit {
		return nil, fmt.Errorf("fishplus: grep %q returned %d offsets for a limit of %d", p, len(offsets), limit)
	}
	return offsets, nil
}

// MaxFindResults caps how many hits one tree search brings back. Nobody
// scrolls through more, and the remote host stops walking once it has them.
const MaxFindResults = 10000

// ErrNoFind is returned when the remote host has no listing backend at all,
// so it cannot describe what it found even if it found it.
var ErrNoFind = errors.New("fishplus: the remote host cannot search a tree")

// FindOptions describes a tree search: which names to match, and optionally
// what the file has to contain.
type FindOptions struct {
	// OnFound publishes each parsed entry on the calling goroutine. It must
	// not block or call into the same session. Find still returns all hits.
	OnFound func(Entry)

	// Masks are shell globs matched against the file name, as find's -name
	// does it. At least one is required.
	Masks []string
	// Text, when set, keeps only files containing it.
	Text string
	// Fixed treats Text as a plain string rather than a regular expression.
	Fixed bool
	// IgnoreCase folds case for Text.
	IgnoreCase bool
	// Limit caps the number of hits; zero means MaxFindResults.
	Limit int
	// Progress, when non-nil, is called periodically while the search
	// runs — currently only when the remote helper drives the search as
	// a background job (Windows peers with the "ffindjob" feature). It
	// reports how many entries the walk has visited and where the head
	// currently is, so a dialog can show "scanning X, N found so far."
	// Fired on the goroutine that runs Find; the callback must not block.
	Progress func(FindProgress)
}

// FindProgress is a checkpoint reported by an in-flight tree search.
type FindProgress struct {
	// Scanned is the number of entries the walk has visited so far.
	Scanned int64
	// Found is the number of entries that have matched so far.
	Found int64
	// Path is the last path the walk looked at, in wire format.
	Path string
}

// CanFind reports whether the remote host can walk a tree for us. Anything
// that can list a directory can, since the walking is find's job.
func (c *Client) CanFind() bool { return c.sess.Features().ListingMode() != "" }

// Find walks a whole tree on the remote host and returns one entry per hit,
// with the full path in Entry.Name. A content search runs as a remote grep
// inside the same find, so a candidate is never downloaded just to be
// rejected — which is the difference between searching a remote source tree
// and waiting for it to arrive.
//
// Two paths, picked from the peer's feature banner: a helper that announced
// "ffindjob" drives the search as a background job — cancel via jdrop when
// the caller's context is done, live progress via P lines, and the panel
// session stays free for other requests meanwhile. A helper without that
// feature falls through to the synchronous ffind wire command, which is
// what the POSIX helper still speaks.
func (c *Client) Find(ctx context.Context, dir string, opts FindOptions) ([]Entry, error) {
	masks := make([]string, 0, len(opts.Masks))
	for _, m := range opts.Masks {
		if m != "" {
			masks = append(masks, m)
		}
	}
	if len(masks) == 0 {
		return nil, errors.New("fishplus: find needs at least one mask")
	}
	if !c.CanFind() {
		return nil, ErrNoFind
	}
	if opts.Text != "" && !c.CanGrep() {
		return nil, ErrNoGrep
	}
	limit := opts.Limit
	if limit <= 0 || limit > MaxFindResults {
		limit = MaxFindResults
	}
	gmode := "-"
	if opts.Text != "" {
		gmode = GrepOptions{Fixed: opts.Fixed, IgnoreCase: opts.IgnoreCase}.mode()
	}

	if c.sess.Features().Has("ffindjob") && c.CanRunJobs() && os.Getenv("F4_NO_FFINDJOB") == "" {
		return c.findViaJob(ctx, dir, masks, opts.Text, gmode, limit, opts.Progress, opts.OnFound)
	}

	paths := append([]string{dir}, masks...)
	if opts.Text != "" {
		paths = append(paths, opts.Text)
	}

	if opts.OnFound != nil {
		var entries []Entry
		mode := ""
		resp, err := c.sess.ExecPathsLines(ctx, "ffind", paths, func(line string) {
			if strings.HasPrefix(line, "M ") {
				mode = strings.TrimSpace(strings.TrimPrefix(line, "M "))
				return
			}
			if mode == "" || strings.HasPrefix(line, "T ") {
				return
			}
			e, parseErr := parseFoundEntry(line, mode)
			if parseErr != nil {
				return
			}
			entries = append(entries, e)
			opts.OnFound(e)
		}, strconv.Itoa(limit), strconv.Itoa(len(masks)), gmode)
		if err != nil {
			return entries, err
		}
		if err := resp.Err("ffind " + dir); err != nil {
			return entries, err
		}
		if mode == "" {
			return entries, fmt.Errorf("fishplus: listing without a mode marker")
		}
		return entries, nil
	}
	resp, err := c.sess.ExecPaths(ctx, "ffind", paths,
		strconv.Itoa(limit), strconv.Itoa(len(masks)), gmode)
	if err != nil {
		return nil, err
	}
	if err := resp.Err("ffind " + dir); err != nil {
		return nil, err
	}
	_, entries, err := ParseFoundListing(resp.Lines)
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// findViaJob drives the tree walk as a background job. It gets cancel and
// progress that the sync path cannot offer, at the cost of the same
// poll-with-backoff dance FollowScan uses. On ctx cancel the job is
// dropped on a fresh context, matching how Scan cleans up.
func (c *Client) findViaJob(ctx context.Context, dir string, masks []string, text, gmode string, limit int, progress func(FindProgress), onFound ...func(Entry)) ([]Entry, error) {
	paths := make([]string, 0, 1+len(masks)+1)
	paths = append(paths, dir)
	paths = append(paths, masks...)
	if text != "" {
		paths = append(paths, text)
	}
	id, err := c.StartJob(ctx, "ffind", paths,
		strconv.Itoa(limit), strconv.Itoa(len(masks)), gmode)
	if err != nil {
		return nil, err
	}
	entries, err := c.followFind(ctx, id, dir, progress, onFound...)
	c.dropJobQuietly(id)
	return entries, err
}

// followFind polls a ffind job until it ends, parses hit lines into
// Entry records, and forwards P lines to progress if set. Poll cadence
// backs off between empty polls exactly like FollowScan, so a big walk
// does not spend more round trips than a small one.
func (c *Client) followFind(ctx context.Context, id int, dir string, progress func(FindProgress), onFound ...func(Entry)) ([]Entry, error) {
	reqCtx := context.WithoutCancel(ctx)
	var entries []Entry
	mode := ""
	wait := jobPollMin
	for {
		pollCtx, cancel := context.WithTimeout(reqCtx, jobDropTimeout)
		st, err := c.PollJob(pollCtx, id, DefaultPollLines)
		cancel()
		if err != nil {
			return entries, err
		}
		fresh := false
		for _, line := range st.Lines {
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "M ") {
				mode = strings.TrimSpace(strings.TrimPrefix(line, "M "))
				continue
			}
			if strings.HasPrefix(line, "P ") {
				if progress != nil {
					if p, ok := parseFindProgress(line); ok {
						progress(p)
					}
				}
				fresh = true
				continue
			}
			if strings.HasPrefix(line, "T ") {
				// Total marker — nothing to do beyond noting we saw it;
				// the entries live in the lines that came before.
				continue
			}
			// Try to parse as a stat-shaped entry with the full path in
			// its name field. We can only do that once we have seen the
			// mode marker; anything before is a diagnostic from a remote
			// tool and gets ignored.
			if mode == "" {
				continue
			}
			e, perr := parseFoundEntry(line, mode)
			if perr != nil {
				// A stray diagnostic must not cost the caller the
				// entries that did parse.
				continue
			}
			entries = append(entries, e)
			if len(onFound) > 0 && onFound[0] != nil {
				onFound[0](e)
			}
			fresh = true
		}
		if err := ctx.Err(); err != nil {
			return entries, err
		}
		if st.More {
			continue
		}
		switch st.State {
		case JobKilled:
			return entries, ErrJobKilled
		case JobDone:
			if st.Exit != 0 {
				msg := st.Msg
				if msg == "" {
					msg = "the remote find failed with status " + strconv.Itoa(st.Exit)
				}
				return entries, &RemoteError{Cmd: "ffind " + dir, Msg: msg}
			}
			return entries, nil
		}
		if fresh {
			wait = jobPollMin
		}
		if err := sleepCtx(ctx, wait); err != nil {
			return entries, err
		}
		wait *= 2
		if wait > jobPollMax {
			wait = jobPollMax
		}
	}
}

// parseFindProgress reads a "P <scanned> <found> <path>" line. A malformed
// one is ignored rather than fatal: it is a hint, not a payload.
func parseFindProgress(line string) (FindProgress, bool) {
	f := strings.SplitN(strings.TrimPrefix(line, "P "), " ", 3)
	if len(f) < 2 {
		return FindProgress{}, false
	}
	scanned, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return FindProgress{}, false
	}
	found, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return FindProgress{}, false
	}
	p := FindProgress{Scanned: scanned, Found: found}
	if len(f) == 3 {
		p.Path = f[2]
	}
	return p, true
}

// parseFoundEntry reads one entry line from a ffind job's output. Same
// backend selection as ParseFoundListing; kept separate because the job
// stream interleaves M/P/T/entry lines and the caller needs to pick.
func parseFoundEntry(line, mode string) (Entry, error) {
	m, variant, _ := strings.Cut(mode, " ")
	switch m {
	case "find":
		return parseFindEntry(line)
	case "stat":
		return parseStatLike(line, 16, "stat listing", true)
	case "statbsd":
		return parseStatLike(line, 8, "bsd stat listing", true)
	case "ls":
		return parseLsEntry(line, variant, true)
	default:
		return Entry{}, fmt.Errorf("fishplus: unknown ffind listing mode %q", mode)
	}
}

// LineIndex is what the remote host knows about the line structure of a file
// after one pass over it: where the requested lines start, and how many
// there are in total.
type LineIndex struct {
	// First is the one-based number of the line Offsets[0] belongs to.
	First int64
	// Offsets holds the byte offset of each line start, in file order. It is
	// shorter than requested when the file ends first.
	Offsets []int64
	// Total is the number of lines in the file.
	Total int64
}

// MaxLineIndexCount caps one index request. The offsets come back as text,
// so asking for millions of them would cost more than reading the file.
const MaxLineIndexCount = 100000

// CanIndexLines reports whether the remote host can do the counting.
func (c *Client) CanIndexLines() bool { return c.sess.Features().Has("awk") }

// Lines returns where the given range of lines starts, counting from line 1,
// together with the number of lines in the file. The remote host walks the
// file; what crosses the network is a few numbers, which is what lets a
// viewer jump to the end of a multi-gigabyte log over ssh.
func (c *Client) Lines(ctx context.Context, p string, first, count int64) (LineIndex, error) {
	if first < 1 {
		return LineIndex{}, fmt.Errorf("fishplus: lines %q: first line %d is below 1", p, first)
	}
	if count < 0 || count > MaxLineIndexCount {
		return LineIndex{}, fmt.Errorf("fishplus: lines %q: count %d outside 0..%d", p, count, MaxLineIndexCount)
	}
	if !c.CanIndexLines() {
		return LineIndex{}, ErrNoGrep
	}
	resp, err := c.sess.ExecPath(ctx, "lidx", p,
		strconv.FormatInt(first, 10), strconv.FormatInt(count, 10))
	if err != nil {
		return LineIndex{}, err
	}
	if err := resp.Err("lidx " + p); err != nil {
		return LineIndex{}, err
	}
	idx := LineIndex{First: first, Total: -1}
	for _, line := range resp.Lines {
		if strings.HasPrefix(line, "T ") {
			total, convErr := strconv.ParseInt(strings.TrimSpace(line[2:]), 10, 64)
			if convErr != nil {
				return LineIndex{}, fmt.Errorf("fishplus: bad line total %q", line)
			}
			idx.Total = total
			continue
		}
		off, convErr := strconv.ParseInt(line, 10, 64)
		if convErr != nil {
			continue
		}
		idx.Offsets = append(idx.Offsets, off)
	}
	if idx.Total < 0 {
		return LineIndex{}, errors.New("fishplus: line index without a total")
	}
	return idx, nil
}
