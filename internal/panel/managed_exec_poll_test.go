package panel

import (
	"sync/atomic"
	"testing"
	"time"
)

// countManagedExecFrames swaps managedExecFrameRequest for a counter.
func countManagedExecFrames(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	previous := managedExecFrameRequest
	managedExecFrameRequest = func() func() { return func() { n.Add(1) } }
	t.Cleanup(func() { managedExecFrameRequest = previous })
	return &n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// While a command runs, the panels ask for frames of their own: the #1603
// stop detection reads IsBusy() once per frame, and a stopped job prints
// nothing that would bring one. The ticker ends with the execution, by
// whatever path it ended, and with the frame.
func TestManagedExecPoll_KeepsFramesComingWhileExecuting(t *testing.T) {
	frames := countManagedExecFrames(t)
	pf := &PanelsFrame{}

	pf.Executing = true
	pf.armManagedExecDebounce()
	stop := pf.managedExecPollStop
	pf.armManagedExecDebounce() // a second arm keeps the one ticker
	if pf.managedExecPollStop != stop {
		t.Fatal("arming again started a second ticker")
	}
	waitFor(t, "a frame request while executing", func() bool { return frames.Load() >= 2 })

	pf.pollManagedExecutionDebounce() // still executing: keeps ticking
	if pf.managedExecPollStop == nil {
		t.Fatal("the ticker stopped while the command still runs")
	}

	pf.Executing = false // ended elsewhere, e.g. by the completion marker
	pf.pollManagedExecutionDebounce()
	if pf.managedExecPollStop != nil {
		t.Fatal("the ticker outlived the execution")
	}
	settled := frames.Load()
	time.Sleep(3 * managedExecPollEvery)
	if got := frames.Load(); got > settled+1 {
		t.Errorf("frames kept coming after the execution: %d -> %d", settled, got)
	}

	pf.Executing = true
	pf.armManagedExecDebounce()
	pf.stopManagedExecPoll() // what Close does
	if pf.managedExecPollStop != nil {
		t.Error("stopping left the ticker registered")
	}
}
