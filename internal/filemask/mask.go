// Package filemask matches a file name against a far2l-style file mask.
//
// It is the syntax f4 already speaks wherever a user names a set of files --
// associations, selection, directory sync -- and it lives on its own so that
// code which is not a panel can ask the same question the panel asks.
package filemask

import "github.com/unxed/f4/vfs"

// Match reports whether name satisfies the given far2l-style mask.
//
// Syntax mirrors far2l's CFileMask:
//   - "," and ";" separate include globs (OR).
//   - "|" splits include vs. exclude; matches on the exclude side veto.
//   - A section wrapped in "/…/" is a regular expression.
//
// Matching honours ignoreCase for both globs and regex (via (?i)).
// A mask that fails to parse (bad regex, empty include list) never
// matches — safer than silently degrading to "matches everything".
func Match(name, mask string, ignoreCase bool) bool {
	return vfs.MatchFileMask(name, mask, ignoreCase)
}
