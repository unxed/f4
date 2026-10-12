package app

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/vtui"
)

// pluginScheduleFixture registers one row of each group, resets both
// throttles and hands back the counters and the bar, so the tests below can
// ask refreshMenuRowStates directly with a tree of their own instead of the
// one BuildMenuItems assembles.
type pluginScheduleFixture struct {
	items       []vtui.MenuBarItem
	coreCalls   *int
	pluginCalls *int
}

func newPluginScheduleFixture(t *testing.T) *pluginScheduleFixture {
	t.Helper()
	coreKey := history.MenuHistoryItemKey("test.pluginSchedule.core")
	pluginKey := history.MenuHistoryItemKey(pluginMenuKeyPrefix + "test.pluginSchedule.plugin")
	coreCalls, pluginCalls := 0, 0

	menuRowEnabledMu.Lock()
	savedCore, hadCore := menuRowEnabled[coreKey]
	savedPlugin, hadPlugin := menuRowEnabled[pluginKey]
	menuRowEnabled[coreKey] = func() bool { coreCalls++; return true }
	menuRowEnabled[pluginKey] = func() bool { pluginCalls++; return true }
	menuRowLast.valid = false
	pluginMenuLast.valid = false
	menuRowEnabledMu.Unlock()
	t.Cleanup(func() {
		menuRowEnabledMu.Lock()
		defer menuRowEnabledMu.Unlock()
		if hadCore {
			menuRowEnabled[coreKey] = savedCore
		} else {
			delete(menuRowEnabled, coreKey)
		}
		if hadPlugin {
			menuRowEnabled[pluginKey] = savedPlugin
		} else {
			delete(menuRowEnabled, pluginKey)
		}
		menuRowLast.valid = false
		pluginMenuLast.valid = false
	})

	items := []vtui.MenuBarItem{{SubItems: []vtui.MenuItem{
		{Text: "core", UserData: coreKey},
		{Text: "plugin", UserData: pluginKey},
	}}}
	return &pluginScheduleFixture{items: items, coreCalls: &coreCalls, pluginCalls: &pluginCalls}
}

// f4#1832: a plugin row's Enabled can read the file under the cursor (the
// archive plugin scans up to sfxProbeLimit of it, hundreds of milliseconds on
// a video), so it is asked only while the bar is up and only once per
// opening. The rows of the action registry keep following the cursor as
// f4#1814 requires.
func TestPluginRowsAreAskedOnlyWhileTheBarIsUp(t *testing.T) {
	f := newPluginScheduleFixture(t)
	pf, src := bigFolderFrame(t, 3)

	// Bar down, first call: the built-in row is asked, the plugin one is not.
	refreshMenuRowStates(f.items)
	if *f.pluginCalls != 0 {
		t.Errorf("plugin row asked with the bar down: %d calls, want 0", *f.pluginCalls)
	}
	if *f.coreCalls != 1 {
		t.Errorf("built-in row asked %d times, want 1", *f.coreCalls)
	}

	// Bar down, the cursor walks: still no plugin ask.
	for i := 1; i <= 20; i++ {
		src.SetCursorIndex(1 + i%3)
		refreshMenuRowStates(f.items)
	}
	if *f.pluginCalls != 0 {
		t.Errorf("plugin row asked on %d cursor steps with the bar down, want 0", *f.pluginCalls)
	}
	if *f.coreCalls == 1 {
		t.Error("the built-in row stopped following the cursor")
	}

	// The bar comes up: the plugin row is asked, once.
	pf.MenuBar.Active = true
	refreshMenuRowStates(f.items)
	if *f.pluginCalls != 1 {
		t.Errorf("plugin row asked %d times after the bar came up, want 1", *f.pluginCalls)
	}

	// The bar stays up and the rows' context ages: the interval asks the
	// built-in rows again, but not the plugin ones. The interval itself is
	// pushed out of the way so the count cannot depend on how slow the
	// runner is; only the expired stamp below decides.
	menuRowEnabledMu.Lock()
	savedEvery := menuRowRefreshEvery
	menuRowRefreshEvery = time.Hour
	menuRowLast.at = time.Now().Add(-2 * time.Hour)
	menuRowEnabledMu.Unlock()
	t.Cleanup(func() {
		menuRowEnabledMu.Lock()
		menuRowRefreshEvery = savedEvery
		menuRowEnabledMu.Unlock()
	})
	beforeCore, beforePlugin := *f.coreCalls, *f.pluginCalls
	for i := 0; i < 50; i++ {
		refreshMenuRowStates(f.items)
	}
	if *f.pluginCalls != beforePlugin {
		t.Errorf("plugin row asked %d more times while the bar stayed up, want 0 (the interval is for the built-in rows only)",
			*f.pluginCalls-beforePlugin)
	}
	if *f.coreCalls != beforeCore+1 {
		t.Errorf("the expired stamp asked the built-in row %d times over 50 calls, want 1", *f.coreCalls-beforeCore)
	}

	// Down and up again: a new opening, asked afresh.
	pf.MenuBar.Active = false
	refreshMenuRowStates(f.items)
	pf.MenuBar.Active = true
	refreshMenuRowStates(f.items)
	if *f.pluginCalls != 2 {
		t.Errorf("plugin row asked %d times over two openings, want 2", *f.pluginCalls)
	}

	if f.items[0].SubItems[1].Disabled {
		t.Error("plugin row dimmed although its Enabled said yes")
	}
}

// The stamp is what decides the schedule above, so it must see the bar's own
// flag: what vtui sets when F9, a click on the top line or an Alt hotkey
// raises the bar.
func TestMenuRowStampSeesTheBarUp(t *testing.T) {
	pf, _ := bigFolderFrame(t, 1)
	if currentMenuRowStamp(nil).menuOpen {
		t.Error("bar down: the stamp must not say the menu is open")
	}
	pf.MenuBar.Active = true
	if !currentMenuRowStamp(nil).menuOpen {
		t.Error("the bar is up: the stamp must see it")
	}
}
