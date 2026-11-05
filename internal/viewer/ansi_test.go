package viewer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type ansiViewModeSource bool

func (s ansiViewModeSource) ANSIViewMode() bool { return bool(s) }

func TestANSIViewModeIncludesVirtualSource(t *testing.T) {
	if !ansiViewMode("Terminal Log", ansiViewModeSource(true)) {
		t.Fatal("virtual ANSI source should enable ANSI view mode")
	}
	if ansiViewMode("plain.log", ansiViewModeSource(false)) {
		t.Fatal("virtual source disabled ANSI view mode")
	}
	if !ansiViewMode("colors.ansi", nil) {
		t.Fatal("ANSI filename should continue to enable ANSI view mode")
	}
}

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
	plain, _, seqs := stripANSI([]byte("x\x1b=y"))
	if string(plain) != "x\x1b=y" || len(seqs) != 0 {
		t.Fatalf("plain = %q seqs = %v", plain, seqs)
	}
}

func TestStripANSIDropsOSCAndCharsetSelection(t *testing.T) {
	plain, _, seqs := stripANSI([]byte("a\x1b]0;title\x07b\x1b(Bc\x1b]8;;http://x\x1b\\d"))
	if string(plain) != "abcd" || len(seqs) != 0 {
		t.Fatalf("plain = %q seqs = %v, want abcd", plain, seqs)
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

// f4#1929: a line that begins with a colour keeps it. The row before it ends
// at its newline, so the next row starts at the colour's sequence rather than
// after it.
func TestANSILineStartingWithColourKeepsIt(t *testing.T) {
	data := []byte("\x1b[31mred\x1b[0m\n\x1b[32mgreen\x1b[0m\n")
	base := vtui.SetIndexBack(vtui.SetIndexFore(0, 7), 0)
	state := base
	var fores []uint8
	for off := 0; off < len(data); {
		row := layoutViewerTextRowANSI(data[off:], 80, 8, false, true)
		cells, _ := ansiRowCells(data[off:off+row.textLen], base, &state, 8, 80)
		if len(cells) == 0 || row.lineLen == 0 {
			t.Fatalf("empty row at byte %d: %+v", off, row)
		}
		fores = append(fores, vtui.GetIndexFore(cells[0].Attributes))
		off += row.lineLen
	}
	if len(fores) != 2 || fores[0] != 1 || fores[1] != 2 {
		t.Errorf("the rows start with colours %v, want [1 2]", fores)
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

func TestIsANSIFileName(t *testing.T) {
	for name, want := range map[string]bool{
		"art.ans": true, "/a/b/ART.ANSI": true, "boot.log": false, "ans": false, "x.ans.txt": false,
	} {
		if got := isANSIFileName(name); got != want {
			t.Errorf("isANSIFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestANSIStateOverCarriesColoursAcrossChunks(t *testing.T) {
	base := vtui.SetIndexBack(vtui.SetIndexFore(0, 7), 0)
	state := ansiStateOver([]byte("plain\n\x1b[32mgreen\n"), base, base)
	if vtui.GetIndexFore(state) != 2 {
		t.Fatalf("fg %d after a green line, want 2", vtui.GetIndexFore(state))
	}
	state = ansiStateOver([]byte("more\n\x1b[0mreset\n"), base, state)
	if state != base {
		t.Errorf("state %#x after a reset, want the base %#x", state, base)
	}
}

// A window that opens in the middle of a log continues the colours of the
// lines above it (f4#1929).
func TestANSIStateAtContinuesColoursFromAbove(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "log.txt")
	content := "one\n\x1b[31mred\nstill red\n\x1b[0mplain\nlast\n"
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	vb, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(t.TempDir()), tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	for i := 0; i < 100; i++ {
		if _, err := vb.ReadAt(0, 1); err != piecetable.ErrLoading {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(100 * time.Millisecond):
		}
	}

	base := vtui.SetIndexBack(vtui.SetIndexFore(0, 7), 0)
	vv := &ViewerView{Backend: vb}
	still := int64(len("one\n\x1b[31mred\n"))
	if got := vtui.GetIndexFore(ansiStateAtSync(t, vv, still, base)); got != 1 {
		t.Errorf("fg %d at the second red line, want 1", got)
	}
	// Scrolling down uses the cached state and gives the same result as a
	// scan from the start.
	plain := int64(len("one\n\x1b[31mred\nstill red\n\x1b[0m"))
	if got := ansiStateAtSync(t, vv, plain, base); got != base {
		t.Errorf("state %#x after the reset, want the base %#x", got, base)
	}
	if got := ansiStateAtSync(t, vv, still, base); vtui.GetIndexFore(got) != 1 {
		t.Errorf("fg %d scrolling back up, want 1", vtui.GetIndexFore(got))
	}
}
