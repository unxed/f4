package vfs

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16At(buf []byte, offset, length int) string {
	units := make([]uint16, length/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(buf[offset+2*i:])
	}
	return string(utf16.Decode(units))
}

// f4#1828: the data a junction is made of is the Microsoft mount point
// reparse buffer, with the \??\ form of the target as the substitute name and
// the plain one as the print name.
func TestMountPointReparseBufferLayout(t *testing.T) {
	buf, err := mountPointReparseBuffer(`C:\Data\Sub dir`)
	if err != nil {
		t.Fatal(err)
	}
	if tag := binary.LittleEndian.Uint32(buf); tag != 0xA0000003 {
		t.Fatalf("tag = %#x, want IO_REPARSE_TAG_MOUNT_POINT", tag)
	}
	if dataLen := int(binary.LittleEndian.Uint16(buf[4:])); dataLen != len(buf)-8 {
		t.Fatalf("data length = %d, buffer holds %d after the header", dataLen, len(buf)-8)
	}
	subOff, subLen := int(binary.LittleEndian.Uint16(buf[8:])), int(binary.LittleEndian.Uint16(buf[10:]))
	printOff, printLen := int(binary.LittleEndian.Uint16(buf[12:])), int(binary.LittleEndian.Uint16(buf[14:]))
	const pathBuffer = 16 // header (8) + the four 16-bit fields (8)
	if got := utf16At(buf, pathBuffer+subOff, subLen); got != `\??\C:\Data\Sub dir` {
		t.Errorf("substitute name = %q", got)
	}
	if got := utf16At(buf, pathBuffer+printOff, printLen); got != `C:\Data\Sub dir` {
		t.Errorf("print name = %q", got)
	}
	// Both names end in a NUL, which the lengths do not count.
	if binary.LittleEndian.Uint16(buf[pathBuffer+subOff+subLen:]) != 0 {
		t.Error("substitute name is not NUL-terminated")
	}
	if binary.LittleEndian.Uint16(buf[pathBuffer+printOff+printLen:]) != 0 {
		t.Error("print name is not NUL-terminated")
	}
}

func TestMountPointReparseBufferNormalisesTheTarget(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`D:/music/`, `D:\music`},
		{`\\?\E:\x`, `E:\x`},
		{`F:\`, `F:\`},
	} {
		buf, err := mountPointReparseBuffer(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		printOff, printLen := int(binary.LittleEndian.Uint16(buf[12:])), int(binary.LittleEndian.Uint16(buf[14:]))
		if got := utf16At(buf, 16+printOff, printLen); got != tc.want {
			t.Errorf("target %q: print name %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A junction cannot point to a network path or to a relative one; the error
// says so instead of leaving an empty folder behind.
func TestMountPointReparseBufferRefusesWhatJunctionsCannotReach(t *testing.T) {
	for _, target := range []string{`\\server\share\x`, `relative\dir`, ``, `C:`} {
		if _, err := mountPointReparseBuffer(target); err == nil {
			t.Errorf("target %q was accepted", target)
		} else if !strings.Contains(err.Error(), "junction") {
			t.Errorf("target %q: unhelpful error %q", target, err)
		}
	}
}
