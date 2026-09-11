package cloudfox

import (
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type contextEditorApp struct {
	vfs.App
	choseProvider bool
}

func (*contextEditorApp) OpenSettings(string, string, string, bool) bool {
	panic("contextual profile editor must not open Settings")
}
func (a *contextEditorApp) Menu(_ string, labels []string, _ func(int)) {
	a.choseProvider = len(labels) > 0
}
func TestProfileEditorRemainsContextual(t *testing.T) {
	fm := newMasterPasswordPromptFrameManager(t)
	plugin := NewPlugin(Options{ConfigDir: t.TempDir(), Portable: true})
	app := &contextEditorApp{}
	editor := &simpleProfileEditor{plugin: plugin}
	manager := NewManagerVFS(nil, nil)
	editor.EditProfile(app, manager, nil)
	if !app.choseProvider {
		t.Fatal("new profile did not show provider chooser")
	}
	editor.EditProfile(app, manager, &Connection{Name: "Example", Provider: ProviderS3})
	if _, ok := fm.GetTopFrame().(*vtui.Window); !ok {
		t.Fatalf("expected profile dialog, got %T", fm.GetTopFrame())
	}
}
