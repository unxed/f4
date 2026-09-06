package main

import "strings"

// destMask returns the wildcard mask typed as the last component of a copy or
// move destination, or "" when there is none.
//
// far2l treats such a component as a rename mask — "*.bak" copies every file
// under its own name with a new extension — rather than as a literal name.
//
// Only the last component is inspected. A wildcard earlier in the path names
// a directory that either exists or does not, and either way the operation
// fails on its own terms rather than producing a surprise.
//
// A trailing slash means the destination is a directory and nothing else.
// far2l reads the mask through PointToName(Dest), so a slash-terminated
// destination has an empty last component, ConvertWildcards finds no wildcard
// in it and returns FALSE, leaving the path literal (processname.cpp:113-119).
// That rule matters here beyond parity: the copy dialog opens on the passive
// panel's path with a separator appended, so trimming the slash first would
// read a directory whose own name holds '*' or '?' as a mask and scatter the
// files one level up under generated names.
func destMask(destInput string) string {
	if destInput == "" {
		return ""
	}
	if strings.HasSuffix(destInput, "/") || strings.HasSuffix(destInput, "\\") {
		return ""
	}
	last := destInput
	if i := strings.LastIndexAny(destInput, "/\\"); i >= 0 {
		last = destInput[i+1:]
	}
	if strings.ContainsAny(last, "*?") {
		return last
	}
	return ""
}

// destWithoutMask is the destination directory left once the mask component
// is taken off. A bare mask means the source panel's own directory, which is
// what makes "*.bak" the in-place backup idiom.
func destWithoutMask(destInput string) string {
	trimmed := strings.TrimRight(destInput, "/\\")
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		if dir := trimmed[:i+1]; dir != "" {
			return dir
		}
	}
	return "."
}

// applyFileMask generates a name from a source name and a wildcard mask, port
// of far2l's ConvertWildcards (src/mix/processname.cpp).
//
// Two parameters of the original are deliberately absent, both checked
// against far2l rather than assumed:
//
// SelectedFolderNameLength is dead weight inside ShellCopy. copy.cpp:1583-1585
// sets it to the whole length of the selected name for a directory and to
// zero for a file; at the whole length Truncate is a no-op and the remainder
// appended afterwards is empty, at zero the block is skipped entirely, so both
// branches generate from the same name. The documented dir1 + dir1/file1 case
// needs a length that is neither, which only the plugin API can pass
// (ProcessName with PN_GENERATENAME). Nothing to port.
//
// BeforeNameLength is reachable but not wanted here yet. When the destination
// is a bare mask, far2l puts the source item's own directory back in front of
// the generated name, so "*.bak" over a search panel writes each copy beside
// its original. f4 does have names carrying a path — the temporary panel lists
// references by full path, and panelized search results land there — but a
// bare mask cannot reach a temporary panel at all today: it resolves to that
// panel's synthetic root, which refuses Create. Porting the rule now would add
// only unreachable code. Into a real directory ("/target/*.bak") the mask
// already applies to the final component, as far2l does.
//
// The rules are not the ones a glob matcher uses, and they are worth stating:
// '?' takes one character of the source, '*' takes source characters until the
// next mask character matches (with a mask dot stopping at the source's *last*
// dot, so "*.bak" splits extension the way one expects), a literal character
// is written out and consumes a source character unless the source is sitting
// on a dot, a mask dot skips the source past its next dot when a wildcard
// follows, and a trailing dot in the result is dropped.
func applyFileMask(srcName, mask string) string {
	if !strings.ContainsAny(mask, "*?") {
		return mask
	}
	src := []rune(srcName)
	w := []rune(mask)
	out := make([]rune, 0, len(src)+len(w))

	srcDot := -1
	for i, r := range src {
		if r == '.' {
			srcDot = i
		}
	}
	// A mask char past the end reads as NUL in the original and matches
	// nothing, which is what makes a trailing '*' copy the rest.
	at := func(i int) rune {
		if i < len(w) {
			return w[i]
		}
		return 0
	}

	si, wi := 0, 0
	for wi < len(w) {
		switch w[wi] {
		case '?':
			wi++
			if si < len(src) {
				out = append(out, src[si])
				si++
			}
		case '*':
			wi++
			for si < len(src) {
				if at(wi) == '.' && srcDot >= 0 && !strings.ContainsRune(string(w[wi+1:]), '.') {
					if si == srcDot {
						break
					}
				} else if src[si] == at(wi) {
					break
				}
				out = append(out, src[si])
				si++
			}
		case '.':
			wi++
			out = append(out, '.')
			if strings.ContainsAny(string(w[wi:]), "*?") {
				for si < len(src) {
					c := src[si]
					si++
					if c == '.' {
						break
					}
				}
			}
		default:
			out = append(out, w[wi])
			wi++
			if si < len(src) && src[si] != '.' {
				si++
			}
		}
	}

	if n := len(out); n > 0 && out[n-1] == '.' {
		out = out[:n-1]
	}
	return string(out)
}
