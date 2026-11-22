package viewer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// appendTo adds bytes to a file the way a log is written to: opened for
// append, written, closed, with the viewer's own handle untouched.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		t.Fatalf("append: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// pumpTasks runs whatever the frame manager has queued, so the backend's
// background fetches land without a running event loop.
func pumpTasks(vv *ViewerView, scr *vtui.ScreenBuf, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if vv != nil && scr != nil {
				vv.Show(scr)
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// An open handle keeps the size the file had when it was opened. That is the
// right answer everywhere except on a log that is still being written to, and
// it is why the viewer's tail-follow never fired: the size it compared against
// could not change.
func TestOSFileWrapperRefreshSizeSeesAppendedBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "log.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}

	f, err := vfs.NewOSVFS(root).Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if got := f.Size(); got != int64(len("first\n")) {
		t.Fatalf("initial size = %d, want %d", got, len("first\n"))
	}

	appendTo(t, path, "second\n")

	if got := f.Size(); got != int64(len("first\n")) {
		t.Fatalf("size before refresh = %d, want the size at open time %d", got, len("first\n"))
	}

	refresher, ok := f.(vfs.SizeRefresher)
	if !ok {
		t.Fatal("a local file handle does not implement vfs.SizeRefresher")
	}
	size, err := refresher.RefreshSize(context.Background())
	if err != nil {
		t.Fatalf("RefreshSize: %v", err)
	}
	want := int64(len("first\nsecond\n"))
	if size != want {
		t.Fatalf("RefreshSize = %d, want %d", size, want)
	}
	if got := f.Size(); got != want {
		t.Fatalf("Size after refresh = %d, want %d", got, want)
	}
}

// The tail of a growing file was cached as a short read that stopped at the
// old end. Serving that window again would hide exactly the bytes a refresh
// went looking for, so a size change has to drop it.
func TestViewerBackendRefreshDropsStaleTailCache(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	root := t.TempDir()
	path := filepath.Join(root, "log.txt")
	if err := os.WriteFile(path, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(root)
	backend, err := NewViewerBackend(context.Background(), v, path)
	if err != nil {
		t.Fatalf("NewViewerBackend: %v", err)
	}
	defer backend.Close()

	// Prime the window cache on the current tail.
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := backend.ReadAt(0, 64)
		if err == nil && strings.HasPrefix(string(data), "first\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out priming the cache: %v", err)
		}
		pumpTasks(nil, nil, 20*time.Millisecond)
	}

	appendTo(t, path, "second\n")

	if !backend.Refresh(context.Background()) {
		t.Fatal("Refresh did not notice the file grew")
	}
	if got, want := backend.Size(), int64(len("first\nsecond\n")); got != want {
		t.Fatalf("Size after Refresh = %d, want %d", got, want)
	}

	deadline = time.Now().Add(2 * time.Second)
	for {
		data, err := backend.ReadAt(0, 64)
		if err == nil && strings.Contains(string(data), "second") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("appended bytes never became readable: %q, %v", string(data), err)
		}
		pumpTasks(nil, nil, 20*time.Millisecond)
	}

	// A second Refresh with nothing written in between must report no change,
	// so that the poll does not redraw the screen every half second.
	if backend.Refresh(context.Background()) {
		t.Error("Refresh reported a change on an unchanged file")
	}
}

// A viewer parked at the end of a file follows it as it grows; the reader does
// not have to reopen it and scroll down again.
func TestViewerFollowsGrowingFileFromEnd(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 6)
	vtui.FrameManager.Init(scr)

	root := t.TempDir()
	path := filepath.Join(root, "log.txt")
	if err := os.WriteFile(path, []byte("line one\nline two\n"), 0600); err != nil {
		t.Fatal(err)
	}

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()

	vv.SetPosition(0, 0, 39, 5)
	vv.SetVisible(true)
	vv.Show(scr)
	pumpTasks(vv, scr, 300*time.Millisecond)

	vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	deadline := time.Now().Add(2 * time.Second)
	for !vv.eofVisible {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the end of the file to come on screen")
		}
		pumpTasks(vv, scr, 20*time.Millisecond)
	}

	sizeAtEnd := vv.Backend.Size()
	appendTo(t, path, "line three\n")

	deadline = time.Now().Add(3 * time.Second)
	for vv.Backend.Size() == sizeAtEnd {
		if time.Now().After(deadline) {
			t.Fatal("the viewer never noticed the file grew")
		}
		pumpTasks(vv, scr, 20*time.Millisecond)
	}

	// Following means the new last line ends up on screen, not merely that the
	// size was updated.
	deadline = time.Now().Add(3 * time.Second)
	for !screenContains(scr, "line three") {
		if time.Now().After(deadline) {
			t.Fatalf("appended line never reached the screen; TopOffset=%d size=%d", vv.TopOffset, vv.Backend.Size())
		}
		pumpTasks(vv, scr, 20*time.Millisecond)
	}
}

// A reader who scrolled up is reading something, and a log writing to its own
// end must not yank the viewport away from them.
func TestViewerDoesNotFollowWhenScrolledAwayFromEnd(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(40, 5)
	vtui.FrameManager.Init(scr)

	root := t.TempDir()
	path := filepath.Join(root, "log.txt")
	var builder strings.Builder
	for i := 0; i < 40; i++ {
		builder.WriteString("old line\n")
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0600); err != nil {
		t.Fatal(err)
	}

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()

	vv.SetPosition(0, 0, 39, 4)
	vv.SetVisible(true)
	vv.Show(scr)
	pumpTasks(vv, scr, 300*time.Millisecond)

	if vv.eofVisible {
		t.Fatal("the whole file fits on screen; the test cannot tell following from not following")
	}
	topBefore := vv.TopOffset

	appendTo(t, path, "brand new line\n")
	pumpTasks(vv, scr, 1500*time.Millisecond)

	if vv.TopOffset != topBefore {
		t.Errorf("viewport moved from %d to %d while the reader was not at the end of the file", topBefore, vv.TopOffset)
	}
	if screenContains(scr, "brand new line") {
		t.Error("the appended line was scrolled into view although the reader had scrolled up")
	}
}

// screenContains reports whether text appears on any row of the screen buffer.
func screenContains(scr *vtui.ScreenBuf, text string) bool {
	for y := 0; y < scr.Height(); y++ {
		var row strings.Builder
		for x := 0; x < scr.Width(); x++ {
			row.WriteRune(rune(scr.GetCell(x, y).Char))
		}
		if strings.Contains(row.String(), text) {
			return true
		}
	}
	return false
}

// frameRecorder is a renderer that keeps the text of every frame the frame
// manager flushes, so a test can look at what was on screen in between, not
// only at where it ended up.
type frameRecorder struct {
	mu     sync.Mutex
	frames [][]string
}

func (r *frameRecorder) Render(buf, _ []vtui.CharInfo, width, height int, _ bool) {
	rows := make([]string, height)
	for y := range rows {
		var row strings.Builder
		for x := 0; x < width; x++ {
			row.WriteRune(rune(buf[y*width+x].Char))
		}
		rows[y] = row.String()
	}
	r.mu.Lock()
	r.frames = append(r.frames, rows)
	r.mu.Unlock()
}
func (r *frameRecorder) SetCursor(int, int, bool, vtui.CursorShape) {}
func (r *frameRecorder) SetPalette(*[256]uint32)                    {}
func (r *frameRecorder) SetWindowTitle(string)                      {}
func (r *frameRecorder) Flush()                                     {}

func (r *frameRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

func (r *frameRecorder) since(n int) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.frames[n:]...)
}

// Following a growing file must go from one tail straight to the next. It
// used to be started in the middle of painting a frame: the desktop under the
// viewer was already drawn, the viewer returned without drawing, and every
// growth of the file put an empty viewer on screen for a frame -- in hex mode
// followed by a "Loading..." one -- which reads as the whole file flickering
// (#428). This runs the real frame manager loop, the way the application
// does, because the viewer painted on its own never showed it.
func TestViewerFollowingPaintsNoEmptyOrLoadingFrame(t *testing.T) {
	for _, hex := range []bool{false, true} {
		name := "text"
		if hex {
			name = "hex"
		}
		t.Run(name, func(t *testing.T) {
			const width, height = 60, 8
			rec := &frameRecorder{}
			scr := vtui.NewSilentScreenBuf()
			scr.Renderer = rec
			scr.AllocBuf(width, height)
			vtui.FrameManager.Init(scr)

			root := t.TempDir()
			path := filepath.Join(root, "log.txt")
			var initial strings.Builder
			for i := 0; i < 30; i++ {
				fmt.Fprintf(&initial, "old line %02d\n", i)
			}
			if err := os.WriteFile(path, []byte(initial.String()), 0600); err != nil {
				t.Fatal(err)
			}

			vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
			if err != nil {
				t.Fatalf("NewViewerView: %v", err)
			}
			defer vv.Close()
			vv.HexMode = hex
			vv.ResizeConsole(width, height)
			vtui.FrameManager.AddScreen(vv)

			run := func(d time.Duration) {
				for deadline := time.Now().Add(d); time.Now().Before(deadline); {
					vtui.FrameManager.Step(5 * time.Millisecond)
				}
			}
			run(200 * time.Millisecond)
			vtui.FrameManager.InjectEvents([]*vtinput.InputEvent{{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END}})
			deadline := time.Now().Add(3 * time.Second)
			for !vv.eofVisible || vv.Busy {
				if time.Now().After(deadline) {
					t.Fatal("timed out waiting for the end of the file to come on screen")
				}
				run(20 * time.Millisecond)
			}
			run(100 * time.Millisecond)

			// Several growths, several poll ticks apart.
			start := rec.count()
			last := ""
			for i := 0; i < 4; i++ {
				last = fmt.Sprintf("new line %d", i)
				appendTo(t, path, last+"\n")
				run(viewerTailPollInterval + 150*time.Millisecond)
			}

			frames := rec.since(start)
			if len(frames) == 0 {
				t.Fatal("nothing was painted while the file grew")
			}
			for i, rows := range frames {
				// Row 0 is the title bar; the first content row of a viewer
				// at the end of a 30-line file always has text on it.
				if strings.TrimSpace(rows[1]) == "" {
					t.Fatalf("frame %d of %d painted an empty viewer:\n%s", i, len(frames), strings.Join(rows, "\n"))
				}
				for _, row := range rows {
					if strings.Contains(row, "Loading") {
						t.Fatalf("frame %d of %d painted a loading placeholder:\n%s", i, len(frames), strings.Join(rows, "\n"))
					}
				}
			}

			// And it did follow: the last line written is on the last frame.
			want := last
			if hex {
				// The hex view shows bytes, so look for the row holding the
				// file's last byte instead.
				want = fmt.Sprintf("%010X:", (vv.Backend.Size()-1)&^0xF)
			}
			final := strings.Join(frames[len(frames)-1], "\n")
			if !strings.Contains(final, want) {
				t.Fatalf("the viewer did not follow to %q:\n%s", want, final)
			}
		})
	}
}

// A viewer read from the top of a log that keeps growing must not blink. Every
// growth drops the window cache, and the frame painted right after it used to
// show "[ Loading... ]" in place of the text until the window came back -- the
// whole viewer flickering about once a second on f4's own debug.log (#1624).
// The viewer is not at the end, so it does not follow: the text on screen stays
// the same, and so must every frame painted while the file grows.
func TestViewerGrowingFileReadFromTopPaintsNoLoadingFrame(t *testing.T) {
	for _, hex := range []bool{false, true} {
		name := "text"
		if hex {
			name = "hex"
		}
		t.Run(name, func(t *testing.T) {
			const width, height = 60, 8
			rec := &frameRecorder{}
			scr := vtui.NewSilentScreenBuf()
			scr.Renderer = rec
			scr.AllocBuf(width, height)
			vtui.FrameManager.Init(scr)

			root := t.TempDir()
			path := filepath.Join(root, "log.txt")
			var initial strings.Builder
			for i := 0; i < 30; i++ {
				fmt.Fprintf(&initial, "old line %02d\n", i)
			}
			if err := os.WriteFile(path, []byte(initial.String()), 0600); err != nil {
				t.Fatal(err)
			}

			vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
			if err != nil {
				t.Fatalf("NewViewerView: %v", err)
			}
			defer vv.Close()
			vv.HexMode = hex
			vv.ResizeConsole(width, height)
			vtui.FrameManager.AddScreen(vv)

			run := func(d time.Duration) {
				for deadline := time.Now().Add(d); time.Now().Before(deadline); {
					vtui.FrameManager.Step(5 * time.Millisecond)
				}
			}
			want := "old line 00"
			if hex {
				want = "0000000000:"
			}
			deadline := time.Now().Add(3 * time.Second)
			for !screenContains(scr, want) || vv.Busy {
				if time.Now().After(deadline) {
					t.Fatal("timed out waiting for the top of the file to come on screen")
				}
				run(20 * time.Millisecond)
			}
			run(100 * time.Millisecond)
			if vv.eofVisible {
				t.Fatal("the whole file fits on screen; the test would be following, not reading from the top")
			}
			topBefore := vv.TopOffset

			start := rec.count()
			for i := 0; i < 4; i++ {
				appendTo(t, path, fmt.Sprintf("new line %d\n", i))
				run(viewerTailPollInterval + 150*time.Millisecond)
			}

			frames := rec.since(start)
			if len(frames) == 0 {
				t.Fatal("nothing was painted while the file grew")
			}
			for i, rows := range frames {
				if !strings.Contains(rows[1], want) {
					t.Fatalf("frame %d of %d lost the first row of the file:\n%s", i, len(frames), strings.Join(rows, "\n"))
				}
				for _, row := range rows {
					if strings.Contains(row, "Loading") {
						t.Fatalf("frame %d of %d painted a loading placeholder:\n%s", i, len(frames), strings.Join(rows, "\n"))
					}
				}
			}
			if vv.TopOffset != topBefore {
				t.Fatalf("viewport moved from %d to %d while the reader was at the top", topBefore, vv.TopOffset)
			}
		})
	}
}
