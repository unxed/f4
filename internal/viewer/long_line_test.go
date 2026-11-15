package viewer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/vfs"
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
