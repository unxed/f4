package app

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/vtui"
)

// f4#1832: GetMenuBar is asked for on every key and frame; the rows' Enabled
// functions must not run each time, only when the context changed or after
// menuRowRefreshEvery.
func TestRefreshMenuRowStatesAsksOnlyWhenTheContextChanged(t *testing.T) {
	key := history.MenuHistoryItemKey("test.refresh.throttle")
	calls, answer := 0, true
	menuRowEnabledMu.Lock()
	saved, hadSaved := menuRowEnabled[key]
	menuRowEnabled[key] = func() bool { calls++; return answer }
	menuRowLast.valid = false
	menuRowEnabledMu.Unlock()
	t.Cleanup(func() {
		menuRowEnabledMu.Lock()
		defer menuRowEnabledMu.Unlock()
		if hadSaved {
			menuRowEnabled[key] = saved
		} else {
			delete(menuRowEnabled, key)
		}
		menuRowLast.valid = false
	})

	items := []vtui.MenuBarItem{{SubItems: []vtui.MenuItem{{Text: "row", UserData: key}}}}
	for i := 0; i < 50; i++ {
		refreshMenuRowStates(items)
	}
	if calls != 1 {
		t.Fatalf("Enabled ran %d times for 50 calls in one context, want 1", calls)
	}
	if items[0].SubItems[0].Disabled {
		t.Error("the row is dimmed although Enabled said yes")
	}

	// A rebuilt menu is a new context and is asked at once.
	answer = false
	rebuilt := []vtui.MenuBarItem{{SubItems: []vtui.MenuItem{{Text: "row", UserData: key}}}}
	refreshMenuRowStates(rebuilt)
	if calls != 2 || !rebuilt[0].SubItems[0].Disabled {
		t.Errorf("a rebuilt menu was not refreshed: calls=%d disabled=%v", calls, rebuilt[0].SubItems[0].Disabled)
	}

	// The same context is asked again once the interval has passed.
	menuRowEnabledMu.Lock()
	menuRowLast.at = time.Now().Add(-2 * menuRowRefreshEvery)
	menuRowEnabledMu.Unlock()
	answer = true
	refreshMenuRowStates(rebuilt)
	if calls != 3 || rebuilt[0].SubItems[0].Disabled {
		t.Errorf("a stale answer stayed: calls=%d disabled=%v", calls, rebuilt[0].SubItems[0].Disabled)
	}
}
