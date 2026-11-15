package netfox

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type mockApp struct{}

func (m *mockApp) GetActivePanelVFS() vfs.VFS      { return nil }
func (m *mockApp) GetPassivePanelVFS() vfs.VFS     { return nil }
func (m *mockApp) GetSelectedNames() []string      { return nil }
func (m *mockApp) GetSelectedName() string         { return "" }
func (m *mockApp) RefreshAll()                     {}
func (m *mockApp) SetPendingSelection(name string) {}
func (m *mockApp) RunProgressTask(title, startMsg string, forked bool, worker func(ctx context.Context, update func(msg string, percent int)) error, onComplete func(err error)) {
}
func (m *mockApp) RunAdvancedProgressTask(title string, forked bool, worker func(ctx context.Context, reporter vfs.TaskReporter) error, onComplete func(err error)) {
}
func (m *mockApp) Message(title, msg string, buttons []string) int               { return 0 }
func (m *mockApp) InputBox(title, prompt, history string, callback func(string)) {}
func (m *mockApp) Menu(title string, items []string, callback func(int))         {}

func TestConnectionDialogLayout(t *testing.T) {
	vtui.SetDefaultPalette()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	// Create a temporary VFS
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_netfox.json")
	nf := NewNetFoxVFS(dbPath)

	app := &mockApp{}

	// Show dialog pushes it to the FrameManager
	showConnectionDialog(app, nf, "")

	top := vtui.FrameManager.GetTopFrame()
	dlg, ok := top.(vtui.Container)
	if !ok {
		t.Fatalf("Top frame is not a vtui.Container")
	}

	// Validate the layout
	vtui.AssertLayout(t, dlg)
}

func TestProtoUIContainerMovesProtocolControls(t *testing.T) {
	first := vtui.NewCheckbox(10, 20, "first", false)
	second := vtui.NewCheckbox(10, 20, "second", false)
	container := &protoUIContainer{
		active: "first",
		uis: map[string]vtui.UIElement{
			"first":  first,
			"second": second,
		},
	}
	container.SetPosition(10, 20, 65, 20)
	x1, y1, x2, y2 := first.GetPosition()
	firstBefore := [4]int{x1, y1, x2, y2}
	x1, y1, x2, y2 = second.GetPosition()
	secondBefore := [4]int{x1, y1, x2, y2}

	container.MoveRelative(3, 4)

	for name, ui := range map[string]vtui.UIElement{"first": first, "second": second} {
		x1, y1, x2, y2 := ui.GetPosition()
		before := firstBefore
		if name == "second" {
			before = secondBefore
		}
		if x1 != before[0]+3 || y1 != before[1]+4 || x2 != before[2]+3 || y2 != before[3]+4 {
			t.Errorf("%s position = (%d,%d)-(%d,%d), want original position + (3,4)", name, x1, y1, x2, y2)
		}
	}
}
