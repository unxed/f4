package findfile

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charlievieth/strcase"
	"github.com/coregx/coregex"
	"github.com/unxed/f4/internal/filemask"
	"github.com/unxed/f4/vfs"
)

// Options describes the portable filename and content search switches.
type Options struct {
	// SelectedFolders restricts the search to these directory trees when nonempty.
	SelectedFolders []string
	CaseSensitive   bool
	WholeWords      bool
	Regex           bool
	NotContaining   bool
	FindFolders     bool
	FindSymlinks    bool
}

func (o Options) usesDefaultSearchEngine() bool {
	return !o.WholeWords && !o.NotContaining && !o.FindFolders && !o.FindSymlinks
}

// splitFindMasks separates the ordinary and excluded name masks accepted by
// Find File. Excluded masks are deliberately kept client-side: not every VFS
// FileFinder can express directory pruning, while the generic VFS walk works
// for both local and remote providers.
func splitFindMasks(mask string) (includes, excludes []string, err error) {
	if strings.Count(mask, "|") > 1 {
		return nil, nil, fmt.Errorf("find file mask must contain at most one '|' separator")
	}

	parts := strings.SplitN(mask, "|", 2)
	normalize := func(value string) []string {
		fields := strings.Split(value, ",")
		masks := make([]string, 0, len(fields))
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			// Far compatibility: *.* translates to * in filepath.Match logic.
			masks = append(masks, strings.ReplaceAll(field, "*.*", "*"))
		}
		return masks
	}

	includes = normalize(parts[0])
	if len(includes) == 0 {
		includes = []string{"*"}
	}
	if len(parts) == 2 {
		excludes = normalize(parts[1])
		if len(excludes) == 0 {
			return nil, nil, fmt.Errorf("find file exclusion masks must not be empty")
		}
	}
	for _, group := range [][]string{includes, excludes} {
		for _, pattern := range group {
			if _, matchErr := filepath.Match(pattern, ""); matchErr != nil {
				return nil, nil, fmt.Errorf("invalid name mask %q: %w", pattern, matchErr)
			}
		}
	}
	return includes, excludes, nil
}

func findFileMaskMatches(name string, masks []string, ignoreCase bool) bool {
	for _, mask := range masks {
		if filemask.Match(name, mask, ignoreCase) {
			return true
		}
	}
	return false
}

type findTextMatcher struct {
	options Options
	needle  string
	regex   *coregex.Regex
}

// newFindTextMatcher prepares the content filter for one Find File run. A
// regular expression is handed to coregex, the engine the viewer and the
// editor already search with, so a pattern that selects a file here selects
// the same text once that file is opened. A literal pattern stays out of the
// engine: strcase folds case while it scans, which is the same split the
// editor makes between its regex and its plain-text search paths.
func newFindTextMatcher(pattern string, options Options) (*findTextMatcher, error) {
	if pattern == "" {
		return nil, nil
	}
	m := &findTextMatcher{options: options, needle: pattern}
	if options.Regex {
		expression := pattern
		if !options.CaseSensitive {
			expression = "(?i:" + expression + ")"
		}
		compiled, err := coregex.Compile(expression)
		if err != nil {
			return nil, err
		}
		m.regex = compiled
	}
	return m, nil
}

func (m *findTextMatcher) matches(data []byte) bool {
	if m == nil {
		return true
	}
	return m.hasMatch(data) != m.options.NotContaining
}

func (m *findTextMatcher) hasMatch(data []byte) bool {
	if m == nil {
		return false
	}
	if m.regex != nil {
		// coregex matches bytes, so the chunk does not have to be copied
		// into a string first. Without whole-word filtering the first hit
		// already answers the question and Match stops there instead of
		// walking the rest of a 128K chunk collecting offsets nobody reads.
		if !m.options.WholeWords {
			return m.regex.Match(data)
		}
		for _, span := range m.regex.FindAllIndex(data, -1) {
			if findTextWholeWord(data, span[0], span[1]) {
				return true
			}
		}
		return false
	}
	// Literal search. Offsets have to index the chunk itself, otherwise the
	// whole-word check inspects the wrong neighbours — which rules out
	// lowercasing the chunk first, since folding can change byte lengths
	// ("İ" becomes two runes) and shift everything behind it. strcase folds
	// as it scans and reports offsets into the original text; CutPrefix then
	// gives back the length the match really occupied there.
	haystack := string(data)
	index := strings.Index
	if !m.options.CaseSensitive {
		index = strcase.Index
	}
	for from := 0; from <= len(haystack); {
		at := index(haystack[from:], m.needle)
		if at < 0 {
			return false
		}
		at += from
		end := at + len(m.needle)
		if !m.options.CaseSensitive {
			rest, ok := strcase.CutPrefix(haystack[at:], m.needle)
			if !ok {
				from = at + 1
				continue
			}
			end = len(haystack) - len(rest)
		}
		if !m.options.WholeWords || findTextWholeWord(data, at, end) {
			return true
		}
		from = at + 1
	}
	return false
}

// findTextWholeWord reports whether the match spanning [start, end) is
// delimited by non-word runes. This stays a hand-rolled check instead of
// wrapping the pattern in \b: Go's \b is defined over the ASCII \w class, so
// a Cyrillic or Greek word has no boundary anywhere in it and whole-word
// search would answer "no match" for every non-Latin query.
func findTextWholeWord(data []byte, start, end int) bool {
	wordBefore := false
	if start > 0 {
		r, _ := utf8.DecodeLastRune(data[:start])
		wordBefore = isFindTextWordRune(r)
	}
	wordAfter := false
	if end < len(data) {
		r, _ := utf8.DecodeRune(data[end:])
		wordAfter = isFindTextWordRune(r)
	}
	return !wordBefore && !wordAfter
}

func isFindTextWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func fileContainsText(ctx context.Context, v vfs.VFS, path string, textLower string) bool {
	matcher, err := newFindTextMatcher(textLower, Options{})
	if err != nil {
		return false
	}
	return fileContainsTextWithMatcher(ctx, v, path, matcher)
}

// fileContainsTextWithMatcher scans in chunks while retaining enough of the
// previous chunk to catch matches crossing a read boundary. Regex searches use
// a larger carry window because a useful expression may consume more than one
// literal token around the boundary.
func fileContainsTextWithMatcher(ctx context.Context, v vfs.VFS, path string, matcher *findTextMatcher) bool {
	if matcher == nil {
		return true
	}
	f, err := v.Open(ctx, path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // Read-only handle; closing cannot change the match.

	overlap := len(matcher.needle) + 4
	if matcher.regex != nil && overlap < 4096 {
		overlap = 4096
	}
	if overlap < 1 {
		overlap = 1
	}
	buf := make([]byte, 128*1024)
	var tail []byte
	for {
		if ctx.Err() != nil {
			return false
		}
		n, readErr := f.Read(ctx, buf)
		if n > 0 {
			data := make([]byte, 0, len(tail)+n)
			data = append(data, tail...)
			data = append(data, buf[:n]...)
			if matcher.hasMatch(data) {
				return !matcher.options.NotContaining
			}
			if len(data) > overlap {
				tail = append(tail[:0], data[len(data)-overlap:]...)
			} else {
				tail = append(tail[:0], data...)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return matcher.options.NotContaining
			}
			return false
		}
	}
}
