package history

import "github.com/unxed/vtui"

// History bucket names follow Far's SavedDialogHistory naming. Sharing the
// buckets is the point: a string typed into the editor's search dialog shows
// up in the viewer and in the file search dialog too, exactly like in Far.
const (
	SearchTextHistoryID  = "SearchText"
	ReplaceTextHistoryID = "ReplaceText"
	FileMasksHistoryID   = "Masks"

	CopyDestHistoryID       = "Copy"
	NewFolderHistoryID      = "NewFolder"
	NewEditHistoryID        = "NewEdit"
	ExternalEditorHistoryID = "ExternalEditor"
)

// AttachHistory turns a plain dialog input into a history backed one, the
// equivalent of Far's DIF_HISTORY. The stored entries are loaded eagerly
// because Edit.HistoryUp and Edit.HistoryDown (Ctrl+E / Ctrl+X) walk the
// local slice; only the Ctrl+Down drop-down re-reads the provider itself.
func AttachHistory(edit *vtui.Edit, historyID string) *vtui.Edit {
	if edit == nil || historyID == "" {
		return edit
	}
	edit.HistoryID = historyID
	edit.ShowHistoryButton = true
	edit.DeduplicateHistory = true
	// These fields hold names, masks and search text. The completion menu's
	// forgiveness of typos suits a shell command, not "*.d" offering "*.xls"
	// (#1155); only the command line keeps it.
	edit.StrictAutoComplete = true
	if vtui.GlobalHistoryProvider != nil {
		edit.History = vtui.GlobalHistoryProvider.LoadHistory(historyID)
	}
	return edit
}

// ClearKeepingPinned returns the Edit.ClearHistory hook of a field whose
// history is rich (the command line): Del in its completion menu asks once
// and clears every entry except the ones pinned with Insert in the Alt+F8
// list, exactly as that list does (#1155).
func ClearKeepingPinned(edit *vtui.Edit, historyID string) func(done func()) {
	return func(done func()) {
		buttons := []string{vtui.Msg("vtui.Ok"), vtui.Msg("vtui.Cancel")}
		dlg := vtui.ShowMessage(vtui.Msg("vtui.History"), vtui.Msg("vtui.HistoryClearConfirm"), buttons)
		dlg.OnResult = func(code int) {
			if code != 0 {
				return
			}
			hp, rich := vtui.GlobalHistoryProvider.(*F4HistoryProvider)
			var kept []HistoryRecord
			if rich {
				for _, record := range hp.LoadRichHistory(historyID) {
					if record.Lock {
						kept = append(kept, record)
					}
				}
			}
			edit.History = ExtractHistoryNames(kept)
			edit.HistoryPos = -1
			if rich {
				hp.SaveRichHistory(historyID, kept)
			} else if vtui.GlobalHistoryProvider != nil {
				vtui.GlobalHistoryProvider.SaveHistory(historyID, edit.History)
			}
			done()
		}
	}
}

// AttachHistoryUseLast is AttachHistory plus Far's DIF_USELASTHISTORY: when
// the field would otherwise open empty it starts out holding the most recent
// entry. The text is selected, so the first keystroke replaces it instead of
// appending to it.
func AttachHistoryUseLast(edit *vtui.Edit, historyID string) *vtui.Edit {
	AttachHistory(edit, historyID)
	if edit == nil || edit.GetText() != "" || len(edit.History) == 0 {
		return edit
	}
	edit.SetText(edit.History[0])
	edit.SelectAll()
	return edit
}

// CommitHistory records an accepted value. Deduplication, the length limit
// and persistence all live in Edit.AddHistory; this only keeps the call sites
// from having to re-check that the field exists and is history backed.
func CommitHistory(edit *vtui.Edit, value string) {
	if edit == nil || edit.HistoryID == "" || value == "" {
		return
	}
	edit.AddHistory(value)
}

// InputBoxEdit returns the single Edit laid out by vtui.InputBox, which has no
// history support of its own. Locating the field here beats forking InputBox
// in vtui just to thread a history name through it.
func InputBoxEdit(dlg *vtui.Window) *vtui.Edit {
	if dlg == nil {
		return nil
	}
	for _, child := range dlg.GetChildren() {
		if edit, ok := child.(*vtui.Edit); ok {
			return edit
		}
	}
	return nil
}
