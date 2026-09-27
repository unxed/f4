//go:build linux || windows || darwin

package proclist

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// configDialogApp is a minimal vfs.App double, the same shape
// plugins/mediainfo/config_dialog_test.go's own configDialogApp uses:
// configure (config_dialog.go) never actually calls any of its methods, but
// the Run signature (vfs.PluginCommand.Run) requires a real vfs.App value.
type configDialogApp struct{}

func (configDialogApp) GetActivePanelVFS() vfs.VFS  { return nil }
func (configDialogApp) GetPassivePanelVFS() vfs.VFS { return nil }
func (configDialogApp) GetSelectedNames() []string  { return nil }
func (configDialogApp) GetSelectedName() string     { return "" }
func (configDialogApp) RefreshAll()                 {}
func (configDialogApp) SetPendingSelection(string)  {}
func (configDialogApp) RunProgressTask(string, string, bool, func(context.Context, func(string, int)) error, func(error)) {
}
func (configDialogApp) RunAdvancedProgressTask(string, bool, func(context.Context, vfs.TaskReporter) error, func(error)) {
}
func (configDialogApp) Message(string, string, []string) int { return 0 }
func (configDialogApp) InputBox(string, string, string, func(string)) {
}
func (configDialogApp) Menu(string, []string, func(int)) {}

// configDialogControls indexes configure's dialog children by kind, the
// same "walk GetChildren, bucket by concrete type" idiom
// plugins/mediainfo/config_dialog_test.go's openConfigDialog uses.
type configDialogControls struct {
	columns  []*vtui.Checkbox
	interval *vtui.Edit
	save     *vtui.Button
	cancel   *vtui.Button
}

func openConfigDialog(t *testing.T, plugin *Plugin) (*vtui.Window, configDialogControls) {
	t.Helper()
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	plugin.configure(configDialogApp{})
	dialog, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("top frame = %T, want *vtui.Window", vtui.FrameManager.GetTopFrame())
	}

	var controls configDialogControls
	var buttons []*vtui.Button
	for _, child := range dialog.GetChildren() {
		switch control := child.(type) {
		case *vtui.Checkbox:
			controls.columns = append(controls.columns, control)
		case *vtui.Edit:
			controls.interval = control
		case *vtui.Button:
			buttons = append(buttons, control)
		}
	}
	if len(controls.columns) != len(allColumnSpecs) || controls.interval == nil || len(buttons) != 2 {
		t.Fatalf("dialog controls are incomplete: %d checkboxes, interval=%v, %d buttons",
			len(controls.columns), controls.interval != nil, len(buttons))
	}
	controls.save, controls.cancel = buttons[0], buttons[1]
	return dialog, controls
}

func expectConfigSettingsError(t *testing.T, dialog *vtui.Window) {
	t.Helper()
	if dialog.IsDone() {
		t.Fatal("dialog closed after rejected settings")
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == dialog {
		t.Fatal("settings error dialog was not shown")
	}
}

func TestConfigureShowsTheCurrentSettings(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	plugin.store = &settingsStore{current: Settings{VisibleColumns: []string{"pid", "cpu"}, RefreshIntervalMS: 750}}
	_, controls := openConfigDialog(t, plugin)

	for i, c := range allColumnSpecs {
		want := c.key == "pid" || c.key == "cpu"
		if got := controls.columns[i].State == 1; got != want {
			t.Errorf("checkbox %d (%s) = %v, want %v", i, c.key, got, want)
		}
	}
	if got := controls.interval.GetText(); got != "750" {
		t.Fatalf("interval field = %q, want %q", got, "750")
	}
}

func TestConfigureSavesAndCancelsSettings(t *testing.T) {
	plugin := NewPlugin(t.TempDir())
	store := &settingsStore{
		path:    filepath.Join(t.TempDir(), "plugins", "proclist.json"),
		current: DefaultSettings(),
	}
	plugin.store = store
	dialog, controls := openConfigDialog(t, plugin)

	for i, c := range allColumnSpecs {
		controls.columns[i].State = boolInt(c.key == "pid" || c.key == "name")
	}
	controls.interval.SetText("1200")
	controls.save.OnClick()

	if !dialog.IsDone() {
		t.Fatal("dialog remained open after a successful save")
	}
	got := store.snapshot()
	if got.RefreshIntervalMS != 1200 {
		t.Fatalf("saved RefreshIntervalMS = %d, want 1200", got.RefreshIntervalMS)
	}
	want := map[string]bool{"pid": true, "name": true}
	if len(got.VisibleColumns) != len(want) {
		t.Fatalf("saved VisibleColumns = %#v, want %#v", got.VisibleColumns, want)
	}
	for _, k := range got.VisibleColumns {
		if !want[k] {
			t.Fatalf("saved VisibleColumns = %#v, want only %#v", got.VisibleColumns, want)
		}
	}

	plugin = NewPlugin(t.TempDir())
	store = &settingsStore{current: DefaultSettings()}
	plugin.store = store
	dialog, controls = openConfigDialog(t, plugin)
	controls.interval.SetText("999")
	controls.cancel.OnClick()
	if !dialog.IsDone() {
		t.Fatal("dialog remained open after cancel")
	}
	if got := store.snapshot(); got.RefreshIntervalMS != DefaultSettings().RefreshIntervalMS {
		t.Fatalf("cancel changed settings: %#v", got)
	}
}

func TestConfigureReportsSaveErrors(t *testing.T) {
	t.Run("not initialized", func(t *testing.T) {
		plugin := NewPlugin(t.TempDir())
		dialog, controls := openConfigDialog(t, plugin)
		controls.save.OnClick()
		expectConfigSettingsError(t, dialog)
	})

	t.Run("all columns unchecked", func(t *testing.T) {
		plugin := NewPlugin(t.TempDir())
		plugin.store = &settingsStore{current: DefaultSettings()}
		dialog, controls := openConfigDialog(t, plugin)
		for _, cb := range controls.columns {
			cb.State = 0
		}
		controls.save.OnClick()
		expectConfigSettingsError(t, dialog)
	})

	t.Run("non-numeric interval", func(t *testing.T) {
		plugin := NewPlugin(t.TempDir())
		plugin.store = &settingsStore{current: DefaultSettings()}
		dialog, controls := openConfigDialog(t, plugin)
		controls.interval.SetText("not a number")
		controls.save.OnClick()
		expectConfigSettingsError(t, dialog)
	})

	t.Run("interval out of range", func(t *testing.T) {
		plugin := NewPlugin(t.TempDir())
		plugin.store = &settingsStore{current: DefaultSettings()}
		dialog, controls := openConfigDialog(t, plugin)
		controls.interval.SetText(strconv.Itoa(int(maxRefreshInterval.Milliseconds()) + 1))
		controls.save.OnClick()
		expectConfigSettingsError(t, dialog)
	})

	t.Run("store write failure", func(t *testing.T) {
		plugin := NewPlugin(t.TempDir())
		blocker := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(blocker, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		plugin.store = &settingsStore{
			path:    filepath.Join(blocker, "proclist.json"),
			current: DefaultSettings(),
		}
		dialog, controls := openConfigDialog(t, plugin)
		controls.interval.SetText("1000")
		controls.save.OnClick()
		expectConfigSettingsError(t, dialog)
	})
}

func TestBoolInt(t *testing.T) {
	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("boolInt did not map bool to 0/1")
	}
}
