package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// menuRows returns what the folder-history dialog paints on its rows, top to
// bottom, without the border and the marker column.
func menuRows(scr *vtui.ScreenBuf, m *vtui.VMenu, search *historySearch) []string {
	m.Show(scr)
	search.draw(scr)
	var rows []string
	for y := m.Y1 + 1; y < m.Y2; y++ {
		var sb strings.Builder
		for x := m.X1 + 1; x < m.X2; x++ {
			sb.WriteRune(testutil.Rune(scr.GetCell(x, y).Char))
		}
		rows = append(rows, strings.TrimSpace(sb.String()))
	}
	return rows
}

// TestIssue1233PinnedFoldersStayAtTheTopWhileTheHistoryScrolls is the
// regression test for issue #1233: the folders with a hotkey sit at the top of
// the folder history, but the dialog opens at the newest entry, so a long
// history scrolled them out of sight and only a trip to the top showed them.
func TestIssue1233PinnedFoldersStayAtTheTopWhileTheHistoryScrolls(t *testing.T) {
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	// Newest first, as history providers keep it.
	records := make([]history.HistoryRecord, 0, 62)
	for i := 0; i < 60; i++ {
		records = append(records, history.HistoryRecord{Name: fmt.Sprintf("/dir%02d", i)})
	}
	records = append(records, history.HistoryRecord{Name: "/pin-a", Lock: true}, history.HistoryRecord{Name: "/pin-b", Lock: true})

	menu := vtui.NewVMenu("Folders")
	search := newHistorySearch(menu, records, "")
	defer search.cleanup()
	search.supportsLocks = true
	search.pinSlotOf = func(rec history.HistoryRecord) int {
		switch rec.Name {
		case "/pin-a":
			return 1
		case "/pin-b":
			return 2
		}
		return -1
	}
	search.applyFilter()

	assertPinnedOnTop := func(when string) {
		t.Helper()
		rows := menuRows(scr, menu, search)
		if len(rows) < 4 || !strings.Contains(rows[0], "/pin-a") || !strings.Contains(rows[1], "/pin-b") {
			t.Errorf("%s: the top rows are %q, want the pinned folders /pin-a and /pin-b first", when, rows)
		}
	}

	// The dialog opens on the newest entry: the end of a 60-entry list.
	assertPinnedOnTop("on opening")
	rows := menuRows(scr, menu, search)
	if last := rows[len(rows)-1]; !strings.Contains(last, "/dir00") {
		t.Errorf("the bottom row is %q, want the newest entry /dir00", last)
	}

	key := func(vk uint16) {
		menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk})
	}
	key(vtinput.VK_PRIOR)
	assertPinnedOnTop("after PgUp")
	key(vtinput.VK_HOME)
	assertPinnedOnTop("after Home")
	key(vtinput.VK_END)
	assertPinnedOnTop("after End")
}
