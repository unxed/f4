package viewer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestUnwrappedLongLineLookaheadDoesNotEvictDisplayWindow(t *testing.T) {
	data := append(bytes.Repeat([]byte("x"), 300*1024), '\n')
	path := filepath.Join(t.TempDir(), "long-line.txt")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 2)
	vtui.FrameManager.Init(scr)
	backend, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(t.TempDir()), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()

	vv := &ViewerView{Backend: backend, WrapMode: false}
	vv.SetPosition(0, 0, 79, 1)
	deadline := time.Now().Add(5 * time.Second)
	for !vv.eofVisible {
		vv.renderText(scr, 80, 1)
		if vv.eofVisible {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viewer did not finish looking ahead through the long line")
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if scr.GetCell(0, 1).Char != 'x' {
		t.Fatal("viewer did not preserve the visible row while looking ahead")
	}
}

func TestUnwrappedLongLineKeepsFollowingRowsAndNavigation(t *testing.T) {
	longLine := bytes.Repeat([]byte("x"), 300*1024)
	data := append(append(append([]byte{}, longLine...), '\n'), []byte("following row\nlast row")...)
	path := filepath.Join(t.TempDir(), "long-line.txt")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 3)
	vtui.FrameManager.Init(scr)
	backend, err := NewViewerBackend(context.Background(), vfs.NewOSVFS(t.TempDir()), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()

	vv := &ViewerView{Backend: backend, WrapMode: false}
	vv.SetPosition(0, 0, 79, 2)
	waitFor := func(done func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !done() {
			vv.renderText(scr, 80, 2)
			if done() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("viewer did not finish the asynchronous long-line operation")
			}
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	waitFor(func() bool { return screenContains(scr, "following row") })
	if !screenContains(scr, "following row") {
		t.Fatal("viewer did not render text after the long line")
	}

	// From the start of the following row, Up must reach the long line's
	// beginning even though the newline is beyond the display-cache window.
	vv.TopOffset = int64(len(longLine) + 1)
	if !vv.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_UP}) {
		t.Fatal("viewer did not handle Up")
	}
	waitFor(func() bool { return !vv.Busy })
	if vv.TopOffset != 0 {
		t.Fatalf("Up moved to %d, want the start of the long line", vv.TopOffset)
	}

	vv.jumpToEnd()
	waitFor(func() bool { return !vv.Busy })
	wantEnd := int64(len(longLine) + 1)
	if vv.TopOffset != wantEnd {
		t.Fatalf("End moved to %d, want the start of the last two rows at %d", vv.TopOffset, wantEnd)
	}
	if !screenContains(scr, "following row") || !screenContains(scr, "last row") {
		t.Fatal("End did not display the final rows after the long line")
	}
}
