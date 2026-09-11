package netfox

import (
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type contextEditorApp struct{ netFoxPluginTestApp }

func (*contextEditorApp) OpenSettings(string, string, string, bool) bool {
	panic("contextual connection editor must not open Settings")
}
func TestConnectionCommandsKeepLocalEditors(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 40)
	vtui.FrameManager.Init(scr)
	app := &contextEditorApp{netFoxPluginTestApp{active: &netFoxVFSWrapper{NetFoxVFS: NewNetFoxVFS(t.TempDir() + "/netfox.json")}, selected: "example"}}
	for _, run := range []func(vfs.App){addNetFoxConnection, editNetFoxConnection} {
		run(app)
		if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window); !ok {
			t.Fatalf("expected connection dialog, got %T", vtui.FrameManager.GetTopFrame())
		}
		vtui.FrameManager.Pop()
	}
}
