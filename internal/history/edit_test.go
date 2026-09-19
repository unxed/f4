package history

import (
	"path/filepath"
	"testing"

	"github.com/unxed/vtui"
)

// #1155: history-backed dialog fields list only entries that contain what was
// typed, where the default menu offers "*.xls" for the mask "*.d".
func TestAttachHistoryMakesAutoCompleteStrict(t *testing.T) {
	prev := vtui.GlobalHistoryProvider
	defer func() { vtui.GlobalHistoryProvider = prev }()
	vtui.GlobalHistoryProvider = NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))

	edit := AttachHistory(vtui.NewEdit(0, 0, 20, ""), FileMasksHistoryID)
	if !edit.StrictAutoComplete {
		t.Fatal("AttachHistory should make the completion menu strict")
	}
	if AttachHistory(nil, FileMasksHistoryID) != nil {
		t.Fatal("AttachHistory(nil) should stay nil")
	}
}

// #1155: Del in the command line's completion menu clears the history the way
// the Alt+F8 list does: after a confirmation, and keeping the pinned entries.
func TestClearKeepingPinned(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())
	prev := vtui.GlobalHistoryProvider
	defer func() { vtui.GlobalHistoryProvider = prev }()
	hp := NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	vtui.GlobalHistoryProvider = hp
	hp.SaveRichHistory("cmdline", []HistoryRecord{{Name: "make"}, {Name: "deploy", Lock: true}, {Name: "ls"}})

	edit := vtui.NewEdit(0, 0, 20, "")
	edit.History = hp.LoadHistory("cmdline")
	edit.HistoryPos = 1
	clearHistory := ClearKeepingPinned(edit, "cmdline")

	done := 0
	clearHistory(func() { done++ })
	dlg := fm.GetTopFrame()
	dlg.SetExitCode(1) // Cancel
	if done != 0 || len(edit.History) != 3 || len(hp.LoadRichHistory("cmdline")) != 3 {
		t.Fatalf("a cancelled clear changed something: done %d, history %v", done, edit.History)
	}

	clearHistory(func() { done++ })
	fm.GetTopFrame().SetExitCode(0) // Ok
	if done != 1 {
		t.Fatalf("done called %d times, want 1", done)
	}
	if len(edit.History) != 1 || edit.History[0] != "deploy" || edit.HistoryPos != -1 {
		t.Errorf("only the pinned entry should be left in the field: %v (pos %d)", edit.History, edit.HistoryPos)
	}
	rich := hp.LoadRichHistory("cmdline")
	if len(rich) != 1 || rich[0].Name != "deploy" || !rich[0].Lock {
		t.Errorf("only the pinned record should be left in the store: %#v", rich)
	}
}
