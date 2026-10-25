package terminal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

// showEnv is a full 80x24 terminal whose every row holds a link, drawn on
// an 80x24 screen: the bottom row carries the cursor, so nothing is shifted.
func showEnv(t testing.TB) (*TerminalView, *vtui.ScreenBuf) {
	t.Helper()
	tv := NewTerminalView(80, 24)
	pty := &mockPty{}
	tv.Pty = pty
	tv.SetCursor(0, 0)
	p := NewAnsiParser(tv, pty)
	t.Cleanup(tv.Close)
	for i := 0; i < 24; i++ {
		line := fmt.Sprintf("row %02d see https://example.com/%02d here", i, i)
		if i < 23 {
			line += "\r\n"
		}
		p.Process([]byte(line))
	}
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 24)
	tv.SetPosition(0, 0, 79, 23)
	tv.SetFocus(true)
	return tv, scr
}

// Show runs on every frame, a command's output included, so it must not do
// per-line work: no copy of each line, and no search for links while no
// link is under the pointer.
func TestShow_DoesNotAllocatePerLine(t *testing.T) {
	tv, scr := showEnv(t)
	tv.Show(scr) // the first frame sizes the scratch line
	allocs := testing.AllocsPerRun(20, func() { tv.Show(scr) })
	if allocs >= 24 {
		t.Errorf("Show allocated %.0f times for 24 lines, want fewer than one per line", allocs)
	}
	t.Logf("Show allocates %.0f times for 24 lines", allocs)
}

// BenchmarkShow is the per-frame cost of drawing a full 80x24 terminal.
func BenchmarkShow(b *testing.B) {
	tv, scr := showEnv(b)
	b.ReportAllocs()
	for b.Loop() {
		tv.Show(scr)
	}
}

// The link under the pointer is still underlined, and only it.
func TestShow_UnderlinesTheHoveredLinkOnly(t *testing.T) {
	tv, scr := showEnv(t)
	tv.Show(scr)
	row := 5
	col := strings.Index(fmt.Sprintf("row %02d see https://example.com/%02d here", row, row), "https")
	if !tv.UpdateURLHover(col+3, row) {
		t.Fatal("hovering the link did not ask for a repaint")
	}
	tv.Show(scr)

	underlined := func(x, y int) bool {
		return scr.GetCell(x, y).Attributes&vtui.CommonLvbUnderscore != 0
	}
	if !underlined(col, row) || !underlined(col+len("https://example.com/05")-1, row) {
		t.Error("the hovered link is not underlined")
	}
	if underlined(col-2, row) || underlined(col, row+1) {
		t.Error("cells outside the hovered link are underlined")
	}

	tv.UpdateURLHover(0, row) // the pointer leaves the link
	tv.Show(scr)
	if underlined(col, row) {
		t.Error("the link stays underlined after the pointer left it")
	}
}
