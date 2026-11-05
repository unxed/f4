package viewer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// ansiStateAtSync asks ansiStateAt until the background scan has an answer,
// running the tasks the frame manager has queued in between, as the event
// loop would.
func ansiStateAtSync(t *testing.T, vv *ViewerView, off int64, base uint64) uint64 {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		state, known := vv.ansiStateAt(off, base)
		if known {
			return state
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatalf("the colours at offset %d were not worked out in time", off)
		}
	}
}

// A terminal log longer than the backend's 256 KiB display window, shown from
// near its end, must come out of "[ Loading... ]": working out the colours
// above the window used to read through the display cache, so the scan and
// the rows on screen evicted each other's window every frame, for good
// (unxed/f4#2268).
func TestANSIViewerLargeLogShowsItsTailWithTheColoursAbove(t *testing.T) {
	const lines = 14000
	var log strings.Builder
	log.WriteString("\x1b[31mfirst line turns the text red\n")
	starts := make([]int, lines+1)
	for i := 1; i <= lines; i++ {
		starts[i] = log.Len()
		fmt.Fprintf(&log, "line %05d with some filler text to make the log large\n", i)
	}
	if log.Len() <= 700*1024 {
		t.Fatalf("fixture is only %d bytes", log.Len())
	}
	path := filepath.Join(t.TempDir(), "big.log")
	if err := os.WriteFile(path, []byte(log.String()), 0600); err != nil {
		t.Fatal(err)
	}

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	vb, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(t.TempDir()), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = vb.Close() }()

	vv := &ViewerView{Backend: vb, AnsiMode: true, WrapMode: false, TopOffset: int64(starts[lines-100])}
	vv.SetPosition(0, 0, 79, 10)
	vv.SetVisible(true)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 11)

	want := fmt.Sprintf("line %05d", lines-100)
	rowText := func() string {
		var b strings.Builder
		for x := 0; x < len(want); x++ {
			b.WriteRune(testutil.Rune(scr.GetCell(x, 1).Char))
		}
		return b.String()
	}
	deadline := time.After(10 * time.Second)
	for {
		vv.DisplayObject(scr)
		if rowText() == want {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("first row is %q, want %q: the viewer stayed on Loading", rowText(), want)
		}
	}
	// The scan finishes behind the first paint; the frame after it is coloured.
	for i := 0; i < 200 && vtui.GetIndexFore(scr.GetCell(0, 1).Attributes) != 1; i++ {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(20 * time.Millisecond):
		}
		vv.DisplayObject(scr)
	}
	if got := vtui.GetIndexFore(scr.GetCell(0, 1).Attributes); got != 1 {
		t.Fatalf("fg %d at the tail, want 1: the red of the first line was not carried down", got)
	}
}
