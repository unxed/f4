package viewer

import (
	"testing"

	"github.com/unxed/vtui"
)

func TestStripANSI(t *testing.T) {
	in := []byte("a\x1b[1mb\x1b[0;32mc\x1b[?25hd\x1b[")
	plain, orig, seqs := stripANSI(in)
	if string(plain) != "abcd" {
		t.Fatalf("plain = %q, want abcd", plain)
	}
	// b stands at 5, c at 13 and d at 20 in the source; the end maps to len(in).
	if want := []int{0, 5, 13, 20, len(in)}; len(orig) != len(want) {
		t.Fatalf("orig = %v, want %v", orig, want)
	} else {
		for i := range want {
			if orig[i] != want[i] {
				t.Fatalf("orig = %v, want %v", orig, want)
			}
		}
	}
	if len(seqs) != 3 || seqs[0].pos != 1 || seqs[1].pos != 2 || seqs[2].pos != 3 {
		t.Fatalf("seqs = %+v", seqs)
	}
	if seqs[2].final != 0 {
		t.Errorf("a private-mode sequence must not count as SGR, final = %q", seqs[2].final)
	}
}

func TestStripANSIKeepsALoneEscape(t *testing.T) {
	plain, _, seqs := stripANSI([]byte("x\x1b]y"))
	if string(plain) != "x\x1b]y" || len(seqs) != 0 {
		t.Fatalf("plain = %q seqs = %v", plain, seqs)
	}
}

func TestApplySGR(t *testing.T) {
	base := vtui.SetIndexBack(vtui.SetIndexFore(0, 7), 0)
	fg := func(a uint64) uint8 { return vtui.GetIndexFore(a) }
	run := func(attr uint64, params ...int) uint64 {
		return applyANSISeq(attr, base, ansiSeq{params: params, final: 'm'})
	}
	if got := fg(run(base, 32)); got != 2 {
		t.Errorf("32 -> fg %d, want 2", got)
	}
	if got := fg(run(base, 92)); got != 10 {
		t.Errorf("92 -> fg %d, want 10", got)
	}
	if got := vtui.GetIndexBack(run(base, 44)); got != 4 {
		t.Errorf("44 -> bg %d, want 4", got)
	}
	if got := run(base, 1); got&vtui.ForegroundIntensity == 0 {
		t.Error("1 did not set the bold flag")
	}
	bold := run(base, 1, 31)
	if bold&vtui.ForegroundIntensity == 0 || fg(bold) != 1 {
		t.Errorf("1;31 -> %#x", bold)
	}
	if got := run(bold, 0); got != base {
		t.Errorf("0 -> %#x, want the base %#x", got, base)
	}
	if got := applyANSISeq(bold, base, ansiSeq{final: 'm'}); got != base {
		t.Errorf("a bare ESC[m -> %#x, want the base", got)
	}
	if got := fg(run(run(base, 33), 39)); got != 7 {
		t.Errorf("39 -> fg %d, want the base's 7", got)
	}
	if got := fg(run(base, 38, 5, 208)); got != 208 {
		t.Errorf("38;5;208 -> fg %d", got)
	}
	rgb := run(base, 38, 2, 1, 2, 3)
	if rgb&vtui.IsFgRGB == 0 || vtui.GetRGBFore(rgb) != 0x010203 {
		t.Errorf("38;2;1;2;3 -> %#x", rgb)
	}
	// The colour after 38 is not read as separate codes: 5;1 must not be bold.
	if got := run(base, 38, 5, 1); got&vtui.ForegroundIntensity != 0 || fg(got) != 1 {
		t.Errorf("38;5;1 -> %#x", got)
	}
	if got := applyANSISeq(bold, base, ansiSeq{params: []int{31}, final: 'H'}); got != bold {
		t.Error("a sequence other than SGR changed the colours")
	}
}

func TestLayoutViewerTextRowANSIIgnoresSequences(t *testing.T) {
	data := []byte("\x1b[1mhello\x1b[0m world\n")
	row := layoutViewerTextRowANSI(data, 80, 8, false, true)
	if row.visualWidth != len("hello world") {
		t.Errorf("visual width %d, want %d", row.visualWidth, len("hello world"))
	}
	if row.lineLen != len(data) || row.textLen != len(data)-1 || !row.foundNewline {
		t.Errorf("row = %+v for %d bytes", row, len(data))
	}
	// A wrapped row breaks after the same number of visible characters.
	wrapped := layoutViewerTextRowANSI([]byte("\x1b[31mabcdefgh\x1b[0mij\n"), 4, 8, true, true)
	if wrapped.visualWidth != 4 || wrapped.foundNewline {
		t.Errorf("wrapped row = %+v", wrapped)
	}
	if want := len("\x1b[31mabcd"); wrapped.lineLen != want {
		t.Errorf("wrapped lineLen %d, want %d", wrapped.lineLen, want)
	}
	// Without the mode the sequence is text and takes room.
	plainRow := layoutViewerTextRowANSI(data, 80, 8, false, false)
	if plainRow.visualWidth <= len("hello world") {
		t.Errorf("plain layout width %d ignores the sequences", plainRow.visualWidth)
	}
}

func TestANSIRowCellsCarryColoursAcrossRows(t *testing.T) {
	base := vtui.SetIndexBack(vtui.SetIndexFore(0, 7), 0)
	state := base
	cells, offs := ansiRowCells([]byte("ab\x1b[32mcd"), base, &state, 8, 40)
	if len(cells) != 4 || len(offs) != 4 {
		t.Fatalf("%d cells, %d offsets", len(cells), len(offs))
	}
	if vtui.GetIndexFore(cells[0].Attributes) != 7 || vtui.GetIndexFore(cells[2].Attributes) != 2 {
		t.Errorf("colours %d %d, want 7 then 2", vtui.GetIndexFore(cells[0].Attributes), vtui.GetIndexFore(cells[2].Attributes))
	}
	if offs[2] != 7 {
		t.Errorf("cell c is at source byte %d, want 7", offs[2])
	}
	// The next row starts green: nothing reset it.
	next, _ := ansiRowCells([]byte("ef"), base, &state, 8, 40)
	if vtui.GetIndexFore(next[0].Attributes) != 2 {
		t.Errorf("next row starts with fg %d, want 2", vtui.GetIndexFore(next[0].Attributes))
	}
}
