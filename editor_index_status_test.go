package main

import (
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/vtui"
)

// pumpUntil drains UI tasks until cond holds, which is how these tests wait for
// work the indexer posts back to the UI thread.
func pumpUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !cond() {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline.C:
			t.Fatalf("timeout waiting for %s", what)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// A fully read file (non-UTF-8) has no scan at all — the restore must still
// resolve, or Loading stays on screen until a key press aborts it.
func TestIndexRestore_ResolvesForFullyReadFile(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	pt := piecetable.New([]byte("line one\nline two\nline three\nline four\nline five\n"))
	ev := newEditorView(pt, nil, "", false)
	ev.targetLine = 3
	ev.targetPos = 2
	ev.targetTopRow = 3
	ev.targetLeft = 0

	ev.StartIndexing()

	if ev.targetLine != -1 {
		t.Fatalf("StartIndexing on a fully read file must resolve the restore, targetLine = %d", ev.targetLine)
	}
	if ev.CursorLine != 3 || ev.CursorPos != 2 || ev.ScrollTopRow != 3 {
		t.Errorf("restore = line %d pos %d top %d, want 3, 2, 3", ev.CursorLine, ev.CursorPos, ev.ScrollTopRow)
	}
}

// TestIndexStatus_ReachesCompleteAndNotifies covers the state machine end to
// end: a scan announces itself, reports progress, and settles on complete,
// telling subscribers about every phase it passes through.
func TestIndexStatus_ReachesCompleteAndNotifies(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	content, _, _ := bigSearchCorpus()
	pt, buf := lazyEditorBuffer(t, content)
	ev := newEditorView(pt, nil, "", false)
	ev.asyncBuf = buf

	var phases []IndexPhase
	unsubscribe := ev.SubscribeIndex(func(s IndexStatus) {
		if len(phases) == 0 || phases[len(phases)-1] != s.Phase {
			phases = append(phases, s.Phase)
		}
	})
	defer unsubscribe()

	if got := ev.IndexState().Phase; got != IndexIdle {
		t.Fatalf("phase before indexing = %v, want idle", got)
	}

	ev.StartIndexing()
	if got := ev.IndexState().Phase; got != IndexScanning {
		t.Fatalf("phase after StartIndexing = %v, want scanning", got)
	}

	pumpUntil(t, "the index to complete", func() bool { return ev.indexIsComplete() })

	st := ev.IndexState()
	if st.Lines != strings.Count(content, "\n")+1 {
		t.Errorf("indexed %d lines, want %d", st.Lines, strings.Count(content, "\n")+1)
	}
	if st.Percent() != 100 {
		t.Errorf("percent = %d at completion, want 100", st.Percent())
	}
	if len(phases) < 2 || phases[0] != IndexScanning || phases[len(phases)-1] != IndexComplete {
		t.Errorf("phases seen = %v, want scanning through to complete", phases)
	}
}

// TestIndexStatus_ResumesAfterAnEdit is the behaviour that replaced cancelling
// the scan for good: an edit stops the run, and the index picks up from the
// line it had reached rather than staying short for the rest of the session.
func TestIndexStatus_ResumesAfterAnEdit(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	content, _, _ := bigSearchCorpus()
	pt, buf := lazyEditorBuffer(t, content)
	ev := newEditorView(pt, nil, "", false)
	ev.asyncBuf = buf

	ev.StartIndexing()
	pumpUntil(t, "the index to complete", func() bool { return ev.indexIsComplete() })
	before := ev.li.LineCount()

	// Interrupt it the way a keystroke does, then let the debounce fire.
	ev.noteBufferEdit()
	ev.pt.Insert(0, []byte("typed\n"))
	ev.li.UpdateAfterInsert(0, []byte("typed\n"))
	ev.setIndexStatus(IndexStatus{Phase: IndexIdle, Total: int64(ev.pt.Size())})

	pumpUntil(t, "the scan to resume and finish", func() bool { return ev.indexIsComplete() })

	if got, want := ev.li.LineCount(), before+1; got != want {
		t.Errorf("line count after the edit = %d, want %d", got, want)
	}
	if got := ev.IndexState().Lines; got != ev.li.LineCount() {
		t.Errorf("status reports %d lines, index holds %d", got, ev.li.LineCount())
	}
}

// TestEnsureIndexedTo_ResolvesAMatchPastTheScan covers the wrong-cursor bug: a
// search reads the whole buffer and can land on text the index has not reached,
// where asking for its line used to answer with the last line it knew and a
// column counted from there.
func TestEnsureIndexedTo_ResolvesAMatchPastTheScan(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	drainPendingTasks()

	const lines = 4000
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString("a line of text\n")
	}
	content := sb.String()
	needleOff := len(content)
	content += "NEEDLE here\n"

	ev := newEditorView(piecetable.New([]byte(content)), nil, "", false)
	// An index that stopped early, which is what a scan in progress looks like.
	ev.li.Rebuild(piecetable.New([]byte(content[:1000])))
	ev.setIndexStatus(IndexStatus{Phase: IndexScanning, Total: int64(len(content))})

	shortLines := ev.li.LineCount()
	if shortLines >= lines {
		t.Fatalf("precondition: index should be short, holds %d lines", shortLines)
	}

	ev.selectFoundPattern(needleOff, len("NEEDLE"))

	if got, want := ev.CursorLine, lines; got != want {
		t.Errorf("cursor line = %d, want %d", got, want)
	}
	if got, want := ev.CursorPos, len("NEEDLE"); got != want {
		t.Errorf("cursor column = %d, want %d", got, want)
	}
	if ev.li.LineCount() <= shortLines {
		t.Error("the index was not extended to cover the match")
	}
}
