package mediainfo

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestReportWindowF4InvokesEditorOnlyOnKeyDown(t *testing.T) {
	calls := 0
	window := &reportWindow{Window: vtui.NewWindow(1, 1, 20, 5, "report"), onEditor: func() { calls++ }}
	if !window.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F4}) {
		t.Fatal("F4 was not consumed")
	}
	if calls != 1 {
		t.Fatalf("editor calls = %d, want 1", calls)
	}
	if window.ProcessKey(&vtinput.InputEvent{KeyDown: false, VirtualKeyCode: vtinput.VK_F4}) {
		t.Fatal("key-up event was unexpectedly consumed")
	}
}

func TestShowReportDialogBuildsReportWindow(t *testing.T) {
	fm := configDialogFrameManager(t)
	plugin := &Plugin{store: &settingsStore{current: Settings{Language: "en"}}}
	fs := vfs.NewOSVFS(t.TempDir())
	plugin.showReportDialog(configDialogApp{}, fs, "movie.mp4", "", Report{General: General{Format: "MPEG-4"}}, "General\nFormat : MPEG-4", true)
	window, ok := fm.GetTopFrame().(*reportWindow)
	if !ok || window == nil {
		t.Fatalf("top frame = %T, want reportWindow", fm.GetTopFrame())
	}
	if !window.ShowClose || !window.ShowZoom || window.MinW != 54 || window.MinH != 12 {
		t.Fatalf("dialog options = close %v zoom %v min %dx%d", window.ShowClose, window.ShowZoom, window.MinW, window.MinH)
	}
	window.Close()
}

func TestMetadataIntegerHelpersHandleBounds(t *testing.T) {
	if got := u16([]byte{1, 2}, binary.BigEndian); got != 0x0102 || u16([]byte{1}, binary.BigEndian) != 0 {
		t.Fatal("u16 bounds or byte order are wrong")
	}
	if got := u32([]byte{1, 2, 3, 4}, binary.BigEndian); got != 0x01020304 || u32(nil, binary.BigEndian) != 0 {
		t.Fatal("u32 bounds or byte order are wrong")
	}
	if got := u64([]byte{1, 2, 3, 4, 5, 6, 7, 8}, binary.BigEndian); got != 0x0102030405060708 || u64([]byte{1}, binary.BigEndian) != 0 {
		t.Fatal("u64 bounds or byte order are wrong")
	}
	if got, ok := metadataInt(42); !ok || got != 42 {
		t.Fatalf("metadataInt(42) = %d, %v", got, ok)
	}
	if got, ok := metadataDuration(uint64(time.Second)); !ok || got != time.Second {
		t.Fatalf("metadataDuration = %v, %v", got, ok)
	}
	if _, ok := metadataDuration(^uint64(0)); ok {
		t.Fatal("metadataDuration accepted an overflowing value")
	}
}

func TestFourCCAndCleanTextNormalizeMetadata(t *testing.T) {
	if fourCC([]byte("WAVE")) != "WAVE" || fourCC([]byte("AB  ")) != "AB" {
		t.Fatal("printable FourCC was not trimmed")
	}
	if got := fourCC([]byte{0, 1, 2, 3}); got != "0x00010203" {
		t.Fatalf("binary FourCC = %q", got)
	}
	if fourCC([]byte("abc")) != "" {
		t.Fatal("short FourCC was accepted")
	}
	if got := cleanText([]byte(" value \x00\x00 ")); got != "value" {
		t.Fatalf("cleanText = %q", got)
	}
}

func TestDurationAndScalarDecodersCoverZeroAndOverflow(t *testing.T) {
	if durationFromUnits(0, 1) != 0 || durationFromUnits(1, 0) != 0 || durationFromUnits(^uint64(0), 1) != 0 {
		t.Fatal("durationFromUnits did not reject zero or overflow")
	}
	if got := durationFromUnits(3, 2); got != 1500*time.Millisecond {
		t.Fatalf("durationFromUnits = %v", got)
	}
	if signedInt32Bits(0xffffffff) != -1 || !*boolPtr(true) || parseDecimal(" 1.25 ") != 1.25 || parseDecimal("bad") != 0 {
		t.Fatal("scalar decoder result is wrong")
	}
}

func TestISO639AndUTF16Decoders(t *testing.T) {
	code := uint16(1<<10 | 2<<5 | 3)
	if parseISO639(code) != "bcd" || parseISO639(0) != "" || parseISO639(0xffff) != "" {
		t.Fatal("ISO-639 decoder result is wrong")
	}
	words := []uint16{' ', 'A', 'B', 0}
	little := make([]byte, len(words)*2+1)
	for i, word := range words {
		binary.LittleEndian.PutUint16(little[i*2:], word)
	}
	if decodeUTF16(little, true) != "AB" {
		t.Fatalf("little-endian UTF-16 = %q", decodeUTF16(little, true))
	}
	big := make([]byte, len(words)*2)
	for i, word := range words {
		binary.BigEndian.PutUint16(big[i*2:], word)
	}
	if decodeUTF16(big, false) != "AB" {
		t.Fatalf("big-endian UTF-16 = %q", decodeUTF16(big, false))
	}
}

func TestCanonicalTagMapsCommonAliases(t *testing.T) {
	for code, want := range map[string]string{
		"©nam": "Title", "IART": "Artist", "album": "Album", "COMM": "Comment",
		"CPRT": "Copyright", "TCON": "Genre", "TYER": "Date", "TSSE": "Encoded application",
		"TRCK": "Track", "TPOS": "Disc", "TPE2": "Album artist", "TCOM": "Composer",
		"custom": "custom",
	} {
		if got := canonicalTag(code); got != want {
			t.Errorf("canonicalTag(%q) = %q, want %q", code, got, want)
		}
	}
	if got := canonicalTag(" "); got != "" {
		t.Fatalf("blank canonical tag = %q", got)
	}
}

func TestSTLFieldDecimalAndCharacterTables(t *testing.T) {
	if cleanSTLField([]byte(" title \x00\x8f")) != "title" {
		t.Fatal("STL field was not cleaned")
	}
	if parseSTLDecimal([]byte(" 25 ")) != 25 || parseSTLDecimal([]byte("bad")) != 0 {
		t.Fatal("STL decimal parsing is wrong")
	}
	for code, want := range map[string]string{"00": "ISO 6937-2 Latin", "01": "ISO 8859-5 Cyrillic", "02": "ISO 8859-6 Arabic", "03": "ISO 8859-7 Greek", "04": "ISO 8859-8 Hebrew", "99": "99"} {
		if got := stlCharacterTable(code); got != want {
			t.Errorf("stlCharacterTable(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestSTLTimecodeValidatesFrameAndClockRanges(t *testing.T) {
	if got, ok := stlTimecode([]byte{1, 2, 3, 4}, 25); !ok || got != time.Hour+2*time.Minute+3*time.Second+160*time.Millisecond {
		t.Fatalf("valid STL timecode = %v, %v", got, ok)
	}
	for _, input := range [][]byte{{1, 60, 0, 0}, {1, 0, 60, 0}, {1, 0, 0, 25}, {1, 0, 0, 0}} {
		if _, ok := stlTimecode(input, 25); ok {
			t.Fatalf("invalid STL timecode accepted: %v", input)
		}
	}
	if _, ok := stlTimecode([]byte{1, 2, 3}, 25); ok {
		t.Fatal("short STL timecode accepted")
	}
}

func TestUTF16DecoderTrimsUnicodeWhitespace(t *testing.T) {
	words := []uint16{'\t', 'x', '\n'}
	b := make([]byte, len(words)*2)
	for i, word := range words {
		binary.LittleEndian.PutUint16(b[i*2:], word)
	}
	if got := decodeUTF16(b, true); got != "x" {
		t.Fatalf("trimmed UTF-16 = %q", got)
	}
	if got := strings.TrimSpace(string(utf16.Decode(words))); got != "x" {
		t.Fatalf("fixture sanity check = %q", got)
	}
}
