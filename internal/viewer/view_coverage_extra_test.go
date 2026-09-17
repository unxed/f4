package viewer

import (
	"context"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestViewerFrameMetadataModesAndNavigation(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldApp := App
	App = nil
	t.Cleanup(func() { App = oldApp })

	data := make([]byte, 64)
	for i := range data {
		data[i] = 0x90 // one-byte NOPs make decode navigation deterministic
	}
	ctx, cancel := context.WithCancel(context.Background())
	backend := &ViewerBackend{
		File:       &vfs.MemoryReadAtCloser{Data: data},
		size:       int64(len(data)),
		ctx:        ctx,
		cancelCtx:  cancel,
		totalLines: -1,
	}
	vv := &ViewerView{
		Backend:   backend,
		VFS:       vfs.NewNullVFS(0),
		Path:      "/tmp/readme.txt",
		WrapMode:  true,
		menuBar:   vtui.NewMenuBar(nil),
		ScrollBar: vtui.NewScrollBar(0, 0, 0),
	}
	t.Cleanup(vv.Close)

	if vv.GetTitle() != "View: readme.txt" || vv.GetWorkspaceTabTitle() != "readme.txt" || vv.GetWorkspaceTabMarker() != "V" {
		t.Fatalf("viewer titles = %q, %q, %q", vv.GetTitle(), vv.GetWorkspaceTabTitle(), vv.GetWorkspaceTabMarker())
	}
	if labels := vv.GetKeyLabels(); labels == nil || len(labels.Normal) < 8 {
		t.Fatalf("viewer key labels = %#v", labels)
	}
	vv.SetPosition(1, 2, 70, 22)
	if x1, y1, x2, y2 := vv.ScrollBar.GetPosition(); x1 != 70 || y1 != 3 || x2 != 70 || y2 != 22 {
		t.Fatalf("scrollbar position = (%d,%d)-(%d,%d)", x1, y1, x2, y2)
	}
	vv.menuBar.SetPosition(1, 2, 70, 2)
	if vv.menuBarPinned() {
		t.Fatal("menu bar on the title row was reported as pinned")
	}
	vv.menuBar.SetPosition(1, 1, 70, 1)
	if !vv.menuBarPinned() {
		t.Fatal("menu bar above the title row was not reported as pinned")
	}

	vv.DisasmMode = 0
	if got := vv.disasmMode(); got != 64 || vv.DisasmMode != 64 {
		t.Fatalf("disasmMode() = %d, stored mode %d; want 64", got, vv.DisasmMode)
	}
	if got := vv.CycleDisasmMode(); got != 32 {
		t.Fatalf("CycleDisasmMode() = %d, want 32", got)
	}
	vv.DisasmMode = 64
	vv.DecodeMode = true
	if got := vv.decodeStep(0); got != 1 {
		t.Fatalf("decodeStep(NOP) = %d, want 1", got)
	}

	vv.DecodeMode = false
	vv.HexMode = true
	vv.gotoPosition(31)
	if vv.TopOffset != 16 {
		t.Fatalf("hex goto offset = %d, want 16", vv.TopOffset)
	}
	if !vv.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}) || vv.TopOffset != 32 {
		t.Fatalf("hex Down offset = %d, want 32", vv.TopOffset)
	}
	if !vv.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_UP}) || vv.TopOffset != 16 {
		t.Fatalf("hex Up offset = %d, want 16", vv.TopOffset)
	}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vv.SetPosition(0, 0, 79, 23)
	vv.Show(scr)
	vv.HexMode = false
	vv.DecodeMode = true
	vv.Show(scr)
	vv.DecodeMode = false
	vv.Reload()
}
