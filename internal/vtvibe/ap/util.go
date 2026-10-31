package ap

import (
	"regexp"
	"strings"
)

// splitLinesUniversal mirrors Python's str.splitlines() applied to text that
// has already gone through universal-newline translation (which is what
// Python's text-mode file reading does before ap.py ever sees the string):
// split on '\n', and do not emit a trailing empty element for a trailing
// newline.
func splitLinesUniversal(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func stripBOM(s string) string {
	return strings.TrimPrefix(s, "\ufeff")
}

// cleanLines removes trailing spaces/tabs from each line of s. A nil input
// stays nil (mirrors `if s is None: return None`).
func cleanLines(s *string) *string {
	if s == nil {
		return nil
	}
	lines := strings.Split(*s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := strings.Join(lines, "\n")
	return &out
}

// normalizeBlock normalizes a block of text the same way the search
// algorithm does: strip the whole block, split into lines, drop blank
// lines, strip each remaining line, and rejoin with '\n'.
func normalizeBlock(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var kept []string
	for _, l := range splitLinesUniversal(text) {
		t := strings.TrimSpace(l)
		if t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, "\n")
}

// pySliceBounds turns Python-style (possibly negative) slice start/end
// indices for a sequence of the given length into clamped, non-negative Go
// slice bounds.
func pySliceBounds(length, start, end int) (int, int) {
	if start < 0 {
		start += length
		if start < 0 {
			start = 0
		}
	}
	if end < 0 {
		end += length
		if end < 0 {
			end = 0
		}
	}
	if start > length {
		start = length
	}
	if end > length {
		end = length
	}
	if end < start {
		end = start
	}
	return start, end
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// normalizeCreateCompare normalizes text the way CREATE's idempotency check
// does: strip the whole block, then strip each line, but (unlike
// normalizeBlock) keep blank lines - two files that differ only in
// insignificant whitespace, not in which lines are blank, count as equal.
func normalizeCreateCompare(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	lines := splitLinesUniversal(trimmed)
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}

// normalizeToLF mirrors Python's universal-newline text-mode file reading:
// both '\r\n' and lone '\r' become '\n'.
func normalizeToLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func leadingWhitespace(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func indentWidth(line string) int {
	return len(leadingWhitespace(line))
}

// indentedBlock is normalizeBlock that keeps each line's leading whitespace:
// blank lines are dropped and trailing whitespace is trimmed, indentation is
// compared exactly.
func indentedBlock(text string) string {
	var kept []string
	for _, l := range splitLinesUniversal(text) {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, strings.TrimRight(l, " \t\r\f\v"))
		}
	}
	return strings.Join(kept, "\n")
}

// whitespaceOnlyReplacement decides a REPLACE whose content equals the
// matched region up to whitespace (port of upstream ap's
// whitespace_only_replacement).
//
// normalizeBlock ignores indentation, so on its own it cannot tell an
// already-applied REPLACE from one whose whole point IS the indentation (a
// dedent or indent of a block). It returns ok=false when the region already
// reads as the patch wants it (skip as already applied), otherwise the exact
// text to write in place of the region.
//
// old is the patch's own snippet when it covers the whole region, else nil.
func whitespaceOnlyReplacement(region string, old *string, newContent string, reindent func(string) string) (string, bool) {
	if old != nil && indentedBlock(*old) == indentedBlock(newContent) {
		// The patch itself changes nothing (old == new, even indentation-wise).
		return "", false
	}
	oldIndented := false
	if old != nil {
		for _, l := range splitLinesUniversal(*old) {
			if strings.TrimSpace(l) != "" {
				oldIndented = leadingWhitespace(l) != ""
				break
			}
		}
	}
	target := newContent
	if !oldIndented {
		// The model dropped the base indentation (or gave no full old
		// text): restore it as for any other REPLACE.
		target = reindent(newContent)
	}
	// Otherwise the old text was written WITH its indentation, so a content
	// indented differently is the intended change; re-indenting it would undo
	// the very edit requested.
	if indentedBlock(region) == indentedBlock(target) {
		return "", false
	}
	return target, true
}

// isSpaceByte reports whether b is whitespace as Python's \s treats it
// (excluding '\n', which never appears inside a single already-split line).
func isSpaceByte(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\f', '\v':
		return true
	}
	return false
}

// isIDLike decides whether candidate may be a drifted patch ID: either a
// fresh 8-hex-char token, or a same-length alphanumeric/underscore token
// (covers a model that keeps using its own non-hex "semantic" ID
// consistently). Deliberately conservative: it runs on every line, so a
// loose rule would let payload text hijack the directive prefix.
func isIDLike(candidate, reference string) bool {
	if hexIDRE.MatchString(candidate) {
		return true
	}
	return len(candidate) == len(reference) && idLikeGenericRE.MatchString(candidate)
}

var fenceRE = regexp.MustCompile("^\\s*(?:`{3,}|~{3,})\\s*[\\w+.#-]*\\s*$")
var headerRE = regexp.MustCompile(`(?i)^(\S+)\s+AP\s+v?(\d+(?:\.\d+)?)\s*$`)
var hexIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}$`)
var idLikeGenericRE = regexp.MustCompile(`^[0-9A-Za-z_]+$`)
var decorationPrefixRE = regexp.MustCompile(`^[\s#/*>+\-•·\[\]().\d]*$`)

var driftPattern = regexp.MustCompile(`^(\S+)\s+(` +
	`(?:RECREATE|REPLACE|INSERT_AFTER|INSERT_BEFORE|DELETE|CREATE|snippet|anchor|content|snippet_tail|RENAME|END)` +
	`|(?:FILE(?:\s+\S.*)?)` +
	`|(?:(?:include_leading_blank_lines|include_trailing_blank_lines|scope_end)(?:\s+\d+)?)` +
	`)$`)

var headerlessActionRE = regexp.MustCompile(`^([0-9a-fA-F]{8})\s+(?:FILE|REPLACE|INSERT_AFTER|INSERT_BEFORE|DELETE|CREATE|RECREATE|RENAME)\b`)

// stripMarkdownFences removes the markdown code fences that wrapped the
// patch in a chat answer. Without this, the closing ``` would be silently
// appended to the last `content` block and end up inside the patched
// source file.
func stripMarkdownFences(lines []string) []string {
	first := -1
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			first = i
			break
		}
	}
	if first == -1 || !fenceRE.MatchString(lines[first]) {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	out[first] = ""
	for j := len(out) - 1; j > first; j-- {
		if strings.TrimSpace(out[j]) == "" {
			continue
		}
		if fenceRE.MatchString(out[j]) {
			out[j] = ""
		}
		break
	}
	return out
}

// resolveDirectiveKey maps a directive keyword to its canonical form,
// forgiving case/colon/synonym drift in non-strict mode. warn may be nil.
func resolveDirectiveKey(rawKey string, strict bool, warn func(string)) (string, bool) {
	if canonicalKeys[rawKey] {
		return rawKey, true
	}
	if strict {
		return "", false
	}

	// The reference does key.rstrip(':').strip(): strip ALL trailing
	// colons, then surrounding whitespace.
	key := strings.TrimRight(rawKey, ":")
	key = strings.TrimSpace(key)
	if canonicalKeys[key] {
		if warn != nil {
			warn("Directive '" + rawKey + "' normalized to '" + key + "'.")
		}
		return key, true
	}
	if alias, ok := keyAliases[key]; ok {
		if warn != nil {
			warn("Non-standard directive '" + rawKey + "' interpreted as '" + alias + "'.")
		}
		return alias, true
	}

	low := strings.ToLower(key)
	for cand := range canonicalKeys {
		if strings.ToLower(cand) == low {
			if warn != nil {
				warn("Directive '" + rawKey + "' has wrong case, interpreted as '" + cand + "'.")
			}
			return cand, true
		}
	}
	for alias, cand := range keyAliases {
		if strings.ToLower(alias) == low {
			if warn != nil {
				warn("Non-standard directive '" + rawKey + "' interpreted as '" + cand + "'.")
			}
			return cand, true
		}
	}
	return "", false
}
