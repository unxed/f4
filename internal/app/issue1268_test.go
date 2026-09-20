package app

import (
	"context"
	"testing"
	"time"

	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/vtui"
)

// pumpFor runs the queued UI tasks for d.
func pumpFor(d time.Duration) {
	deadline := time.After(d)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			return
		}
	}
}

// A progress screen that had to wait for a modal (the SQLite client is one)
// was shown when the modal went, even though its worker was done by then, and
// stayed on screen for good: nothing was left to close it (#1268).
func TestProgressTaskThatWaitedForAModalIsNotShownAfterItsWorkerIsDone(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := paneltest.SetupMockPanelsFrame(t)
	t.Cleanup(pf.Close)
	vtui.FrameManager.Push(pf)
	vtui.FrameManager.Push(vtui.NewVMenu("modal"))
	screens := len(vtui.FrameManager.Screens)

	completed := false
	pf.RunProgressTask("Title", "Working...", false,
		func(_ context.Context, _ func(string, int)) error { return nil },
		func(error) { completed = true })
	// The worker finishes at once, while the modal is still up.
	deadline := time.Now().Add(5 * time.Second)
	for !completed && time.Now().Before(deadline) {
		pumpFor(20 * time.Millisecond)
	}
	if !completed {
		t.Fatal("the task never completed")
	}
	if len(vtui.FrameManager.Screens) != screens {
		t.Fatalf("a progress screen appeared over the modal: %d screens, want %d", len(vtui.FrameManager.Screens), screens)
	}

	// The modal goes; the waiting progress screen must not turn up now.
	vtui.FrameManager.Pop()
	pumpFor(400 * time.Millisecond)
	if len(vtui.FrameManager.Screens) != screens {
		t.Fatalf("a finished task's progress screen appeared after the modal closed: %d screens, want %d", len(vtui.FrameManager.Screens), screens)
	}
}
