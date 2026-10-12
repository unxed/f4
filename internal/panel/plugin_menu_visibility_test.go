package panel

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// pumpTasks runs the UI tasks that arrive within a short quiet period: the
// menu is built by a task, and PostTask may deliver it from another goroutine.
func pumpTasks(t *testing.T) {
	t.Helper()
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

// Entries hidden in the settings are left out of the F11 menu, and the rows
// that remain run their own plugin, not the one that used to sit at the same
// position (f4#918).
func TestPluginMenuLeavesOutHiddenEntries(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)

	saved := plughost.PluginMenuItems
	t.Cleanup(func() { plughost.PluginMenuItems = saved })
	var ran []string
	mk := func(name string) plughost.PluginMenuItem {
		return plughost.PluginMenuItem{ActionName: "test.menu." + strings.ToLower(name), Label: name, Handler: func(vfs.App) { ran = append(ran, name) }}
	}
	plughost.PluginMenuItems = []plughost.PluginMenuItem{mk("Alpha"), mk("Beta"), mk("Gamma")}

	path := PluginMenuVisibilityFilePath()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
	if err := SavePluginMenuHidden([]string{"test.menu.beta"}); err != nil {
		t.Fatal(err)
	}

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	pf.ShowPluginMenu()
	pumpTasks(t)
	var menu *vtui.VMenu
	for _, screen := range vtui.FrameManager.Screens {
		for _, frame := range screen.Frames {
			if wrapped, ok := frame.(*menuKeyLabelsFrame); ok {
				menu = wrapped.VMenu
			}
		}
	}
	if menu == nil {
		t.Fatalf("the F11 menu was not opened; top frame %T", vtui.FrameManager.GetTopFrame())
	}
	var labels []string
	for _, item := range menu.Items {
		labels = append(labels, strings.ReplaceAll(item.Text, "&", ""))
	}
	joined := strings.Join(labels, "|")
	if strings.Contains(joined, "Beta") {
		t.Errorf("the hidden entry is still in the menu: %v", labels)
	}
	if len(labels) != 2 || !strings.Contains(joined, "Alpha") || !strings.Contains(joined, "Gamma") {
		t.Fatalf("the menu rows = %v, want Alpha and Gamma", labels)
	}

	// The second row is Gamma, though Gamma is the third entry.
	menu.SelectPos = 1
	menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	pumpTasks(t)
	if len(ran) != 1 || ran[0] != "Gamma" {
		t.Errorf("Enter on the second row ran %v, want Gamma", ran)
	}
}
