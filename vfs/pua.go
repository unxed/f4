package vfs

import (
	"strings"
	"unicode/utf8"
)

// This file implements the same UnRAR-compatible PUA mapping
// github.com/unxed/zip and github.com/unxed/tar already use for entry
// names that are not valid UTF-8 (see their pua.go / sqlite.go). f4 mirrors
// it exactly here, at the local OS VFS boundary, instead of inventing a
// second scheme: Unix file names are raw byte strings and are not
// guaranteed to be valid UTF-8, but almost everything downstream of
// vfs.OSVFS -- panel sorting and rendering, session/history persistence via
// encoding/json, search -- requires (or silently mangles) a valid UTF-8 Go
// string. encoding/json in particular is documented to replace an invalid
// UTF-8 byte with the Unicode replacement rune when it marshals a string,
// which is a real, irreversible loss of the original bytes for anything
// f4 persists across restarts (recent directories/files, per-file cursor
// position, ...).
//
// The fix is to make sure a non-UTF-8 name never travels through f4 as a
// raw byte string in the first place: decodeUTF8OrMap turns it into a
// spelling that is valid UTF-8 (so it round-trips losslessly through JSON,
// sorts, search, and display) while still carrying every original byte,
// recoverable with encodeMappedString immediately before the name is
// handed back to a syscall.

// MappedStringMark is U+FFFE, spelled by its numeric code point rather
// than an escape sequence or a literal character so that this
// noncharacter -- which has no assigned glyph -- never has to appear as a
// raw byte sequence in this file's own source.
const MappedStringMark = rune(0xFFFE)

// MappedStringMarkStr is MappedStringMark as a string; the conversion is
// from a rune-typed constant, so it is the same one-codepoint idiom
// string(aRune) always is, not the "string(anInt)" pattern go vet flags.
const MappedStringMarkStr = string(MappedStringMark)

// privateUseBase is where the mapping puts byte 0x00; the 256 byte values
// run from there to privateUseBase+0xFF, exactly as in unxed/zip and
// unxed/tar.
const privateUseBase = rune(0xE000)

// decodeUTF8OrMap returns b unchanged as a string when it is already valid
// UTF-8. Otherwise it returns a MappedStringMark-prefixed string with one
// private-use rune per byte of b, so every byte survives losslessly inside
// a value that is itself valid UTF-8.
func decodeUTF8OrMap(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b)*3 + 3)
	sb.WriteRune(MappedStringMark)
	for _, c := range b {
		sb.WriteRune(privateUseBase + rune(c))
	}
	return sb.String()
}

// encodeMappedString reverses decodeUTF8OrMap. A string that does not start
// with MappedStringMark is assumed to already be the raw name (e.g. a
// plain, valid-UTF-8 name that was never mapped) and is returned as-is.
//
// If the string starts with the mark but a rune in it falls outside the
// mapped private-use range -- which can only happen if the mapped string
// was edited after the fact, since nothing else in f4 produces a mapped
// string this function wouldn't itself round-trip -- the string is taken
// at face value instead of narrowing an unrelated rune into a stray byte.
func encodeMappedString(s string) []byte {
	if !strings.HasPrefix(s, MappedStringMarkStr) {
		return []byte(s)
	}
	runes := []rune(s)
	b := make([]byte, len(runes)-1)
	for i, r := range runes[1:] {
		if r < privateUseBase || r > privateUseBase+0xFF {
			return []byte(s)
		}
		b[i] = byte(r - privateUseBase)
	}
	return b
}

// decodeMappedPathSegments reverses decodeUTF8OrMap for every "/"-separated
// component of a path, not just a bare name. A path handed to OSVFS (Open,
// Remove, Rename, ...) is built by joining a directory with a
// VFSItem.Name that ReadDir/Stat/Lstat may have mapped, so the mark can sit
// in the middle of the string, not only at its start.
//
// Splitting only on "/" and never on "\" is deliberate, not an oversight:
// "\" is a perfectly ordinary, legal byte in a Unix file name (the mapping
// this reverses is a Unix concern in the first place -- see the file
// comment), so treating it as a separator here would misparse an unmapped
// segment that happens to contain one. It costs nothing on Windows either:
// decodeUTF8OrMap is a no-op there because names arriving through UTF-16
// are already valid UTF-8, so MappedStringMarkStr can never actually occur
// in a Windows path and the cheap Contains check below always returns
// early.
func decodeMappedPathSegments(p string) string {
	if !strings.Contains(p, MappedStringMarkStr) {
		return p
	}
	var sb strings.Builder
	sb.Grow(len(p))
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			seg := p[start:i]
			if strings.HasPrefix(seg, MappedStringMarkStr) {
				sb.Write(encodeMappedString(seg))
			} else {
				sb.WriteString(seg)
			}
			if i < len(p) {
				sb.WriteByte(p[i])
			}
			start = i + 1
		}
	}
	return sb.String()
}

// DisplayName renders a possibly MappedStringMark-prefixed name for the
// screen. The private-use range decodeUTF8OrMap escapes into has no
// assigned glyph in any font, so painting the mapped runes as-is would
// trade the mojibake this mapping is meant to fix for a row of tofu boxes
// -- worse than today's behaviour, not better.
//
// Rather than invent a rendering rule of its own, DisplayName decodes the
// mapped string back to the original bytes and hands that (as a plain, and
// on Unix possibly still-invalid-UTF-8, Go string) to the same rendering
// path every other name already goes through. vtui's SanitizeCluster
// (github.com/unxed/vtui/textseg.go) already turns any byte it cannot
// decode into a plain "?" while leaving every valid run of the name --
// letters, the extension -- exactly as it reads; that is the existing,
// established convention this reuses instead of replacing.
//
// It never changes VFSItem.Name/FileEntry.Name itself -- only the string
// actually painted to the screen -- so selection, sorting, path
// construction and persistence keep operating on the lossless mapped form.
func DisplayName(name string) string {
	if !strings.HasPrefix(name, MappedStringMarkStr) {
		return name
	}
	return string(encodeMappedString(name))
}
