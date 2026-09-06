package vfs

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charlievieth/strcase"
	"github.com/coregx/coregex"
	"github.com/unxed/f4/vfs/hostfs"
	"github.com/unxed/f4/vfs/hostpath"
)

// FindFiles is the local fast path for Find File. ReadDir is deliberately a
// rich panel-listing API: it stats every entry so a panel can render size,
// timestamps, permissions, and physical size. A search only needs names and
// directory bits for rejected entries, so doing that work for every item made
// a large local tree pay one stat per entry before the actual match decision.
// This walk keeps metadata lazy and stats only hits, which is the same shape
// as the native find implementations used by Far.
func (v *OSVFS) FindFiles(ctx context.Context, dir string, q FindQuery) ([]FoundEntry, error) {
	masks := q.Masks
	if len(masks) == 0 {
		masks = []string{"*"}
	}
	matcher, err := newFindQueryMatcher(q)
	if err != nil {
		return nil, err
	}

	var found []FoundEntry
	var scanned int64
	var lastProgress time.Time
	report := func(path string, force bool) {
		if q.Progress == nil {
			return
		}
		now := time.Now()
		if !force && now.Sub(lastProgress) < 100*time.Millisecond {
			return
		}
		lastProgress = now
		q.Progress(FindProgress{Scanned: scanned, Found: int64(len(found)), Path: path})
	}

	var walk func(string) error
	walk = func(current string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		report(current, false)
		entries, err := hostfs.ReadDir(prepareOSPath(current))
		if err != nil {
			// The root error is useful to the caller. A child that becomes
			// inaccessible during a long search is treated like Far's scan:
			// skip it and continue with the rest of the tree.
			if current == dir {
				return err
			}
			return nil
		}

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if q.Limit > 0 && len(found) >= q.Limit {
				return nil
			}
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			scanned++
			child := hostpath.Join(current, name)
			isSymlink := entry.Type()&os.ModeSymlink != 0
			isDir := entry.IsDir()
			if isSymlink {
				if !q.FindSymlinks {
					continue
				}
				// Symlinks are leaves even when their target is a directory.
				// Following them is both surprising in a search and a common
				// source of recursive loops in home directories.
				isDir = false
			}

			if isDir {
				if q.FindFolders && q.Text == "" && findMaskMatches(name, masks) {
					if item, statErr := v.Stat(ctx, child); statErr == nil {
						found = append(found, FoundEntry{Path: child, Item: item})
						report(child, true)
					}
				}
				if err := walk(child); err != nil {
					return err
				}
				continue
			}

			if !findMaskMatches(name, masks) {
				continue
			}
			if matcher != nil {
				contains, matchErr := v.findFileContains(ctx, child, matcher)
				if matchErr != nil || !contains {
					continue
				}
			}
			item, statErr := v.Stat(ctx, child)
			if statErr != nil {
				continue
			}
			found = append(found, FoundEntry{Path: child, Item: item})
			report(child, true)
		}
		return nil
	}

	if err := walk(dir); err != nil {
		return found, err
	}
	report(dir, true)
	return found, nil
}

func findMaskMatches(name string, masks []string) bool {
	for _, mask := range masks {
		if mask == "" {
			continue
		}
		if matched, _ := filepath.Match(mask, name); matched {
			return true
		}
	}
	return false
}

type findQueryMatcher struct {
	query  FindQuery
	needle string
	regex  *coregex.Regex
}

// newFindQueryMatcher builds the content filter for one FindFiles call. The
// regular expression case goes through coregex, which is the engine the
// viewer and the editor search with, so the local fast path agrees with what
// the user sees after opening a hit. A literal query never reaches the engine:
// strcase folds case while scanning, so a search does not pay for a compiled
// pattern it does not need.
func newFindQueryMatcher(q FindQuery) (*findQueryMatcher, error) {
	if q.Text == "" {
		return nil, nil
	}
	m := &findQueryMatcher{query: q, needle: q.Text}
	if q.Regex {
		pattern := q.Text
		if q.IgnoreCase {
			pattern = "(?i:" + pattern + ")"
		}
		re, err := coregex.Compile(pattern)
		if err != nil {
			return nil, err
		}
		m.regex = re
	}
	return m, nil
}

func (m *findQueryMatcher) hasMatch(data []byte) bool {
	if m == nil {
		return false
	}
	if m.regex != nil {
		// coregex matches bytes, so the chunk stays as it was read. Without
		// whole-word filtering the first hit settles the file, and Match
		// stops there rather than enumerating every occurrence in it.
		if !m.query.WholeWords {
			return m.regex.Match(data)
		}
		for _, span := range m.regex.FindAllIndex(data, -1) {
			if findWholeWord(data, span[0], span[1]) {
				return true
			}
		}
		return false
	}
	// Literal search. The offsets must index the chunk itself for the
	// whole-word check to see the right neighbours, so the chunk cannot be
	// lowercased first: folding can change byte lengths ("İ" becomes two
	// runes) and shift every offset behind it. strcase folds while scanning
	// and reports offsets into the original text, and CutPrefix measures how
	// much of it the match actually covered.
	haystack := string(data)
	index := strings.Index
	if m.query.IgnoreCase {
		index = strcase.Index
	}
	for from := 0; from <= len(haystack); {
		at := index(haystack[from:], m.needle)
		if at < 0 {
			return false
		}
		at += from
		end := at + len(m.needle)
		if m.query.IgnoreCase {
			rest, ok := strcase.CutPrefix(haystack[at:], m.needle)
			if !ok {
				from = at + 1
				continue
			}
			end = len(haystack) - len(rest)
		}
		if !m.query.WholeWords || findWholeWord(data, at, end) {
			return true
		}
		from = at + 1
	}
	return false
}

// findWholeWord reports whether the match spanning [start, end) is delimited
// by non-word runes. The check is hand-rolled rather than a \b wrapper on the
// pattern because Go's \b is defined over the ASCII \w class: a Cyrillic or
// Greek word has no boundary in it, so \b would reject every non-Latin
// whole-word query.
func findWholeWord(data []byte, start, end int) bool {
	return !findWordBefore(data, start) && !findWordAfter(data, end)
}

func findWordBefore(data []byte, at int) bool {
	if at <= 0 {
		return false
	}
	r, _ := utf8.DecodeLastRune(data[:at])
	return isFindWordRune(r)
}

func findWordAfter(data []byte, at int) bool {
	if at >= len(data) {
		return false
	}
	r, _ := utf8.DecodeRune(data[at:])
	return isFindWordRune(r)
}

func isFindWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (v *OSVFS) findFileContains(ctx context.Context, path string, matcher *findQueryMatcher) (bool, error) {
	f, err := v.Open(ctx, path)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = f.Close() // Search opens the handle only for reading.
	}()

	overlap := len(matcher.needle) + 4
	if matcher.regex != nil && overlap < 4096 {
		overlap = 4096
	}
	if overlap < 1 {
		overlap = 1
	}
	buf := make([]byte, 128*1024)
	var carry []byte
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n, readErr := f.Read(ctx, buf)
		if n > 0 {
			data := make([]byte, 0, len(carry)+n)
			data = append(data, carry...)
			data = append(data, buf[:n]...)
			if matcher.hasMatch(data) {
				return !matcher.query.NotContaining, nil
			}
			if len(data) > overlap {
				carry = append(carry[:0], data[len(data)-overlap:]...)
			} else {
				carry = append(carry[:0], data...)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return matcher.query.NotContaining, nil
			}
			return false, readErr
		}
	}
}
