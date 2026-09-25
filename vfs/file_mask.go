package vfs

import (
	"path/filepath"
	"regexp"
	"strings"
)

// MatchFileMask reports whether name satisfies the given far2l-style mask.
//
// The syntax is the one f4 uses wherever a user names a set of files:
// commas and semicolons separate include globs, and a pipe separates
// includes from excludes. A section wrapped in /.../ is a regular expression.
// Matching honours ignoreCase for both globs and regular expressions.
func MatchFileMask(name, mask string, ignoreCase bool) bool {
	mask = strings.TrimSpace(mask)
	if mask == "" || name == "" {
		return false
	}
	includes, excludes := splitFileMaskIncludeExclude(mask)
	if len(includes) == 0 {
		return false
	}
	matchAny := func(list []string) bool {
		for _, item := range list {
			item = strings.TrimSpace(item)
			if item != "" && matchOneFileMask(name, item, ignoreCase) {
				return true
			}
		}
		return false
	}
	return matchAny(includes) && !matchAny(excludes)
}

func splitFileMaskIncludeExclude(mask string) (includes, excludes []string) {
	inc, exc, hasExc := splitFileMaskOnPipe(mask)
	includes = splitFileMaskCommaSemi(inc)
	if hasExc {
		excludes = splitFileMaskCommaSemi(exc)
	}
	return includes, excludes
}

func splitFileMaskOnPipe(mask string) (inc, exc string, hasExc bool) {
	depth := 0
	for i := 0; i < len(mask); i++ {
		switch mask[i] {
		case '/':
			if depth == 0 {
				depth = 1
			} else {
				depth = 0
			}
		case '|':
			if depth == 0 {
				return mask[:i], mask[i+1:], true
			}
		}
	}
	return mask, "", false
}

func splitFileMaskCommaSemi(side string) []string {
	if side == "" {
		return nil
	}
	var out []string
	var current strings.Builder
	depth := 0
	flush := func() {
		item := strings.TrimSpace(current.String())
		current.Reset()
		if item != "" {
			out = append(out, item)
		}
	}
	for i := 0; i < len(side); i++ {
		switch side[i] {
		case '/':
			if depth == 0 {
				depth = 1
			} else {
				depth = 0
			}
			current.WriteByte(side[i])
		case ',', ';':
			if depth == 0 {
				flush()
			} else {
				current.WriteByte(side[i])
			}
		default:
			current.WriteByte(side[i])
		}
	}
	flush()
	return out
}

func matchOneFileMask(name, mask string, ignoreCase bool) bool {
	if len(mask) >= 2 && mask[0] == '/' && mask[len(mask)-1] == '/' {
		pattern := mask[1 : len(mask)-1]
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		return err == nil && re.MatchString(name)
	}
	if mask == "*.*" {
		mask = "*"
	}
	m, n := mask, name
	if ignoreCase {
		m = strings.ToLower(m)
		n = strings.ToLower(n)
	}
	ok, err := filepath.Match(m, n)
	return err == nil && ok
}
