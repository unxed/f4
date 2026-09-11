package testutil

import (
	"testing"
	"time"

	"github.com/unxed/vtui"
)

// The harness's own teardown test: SwapFrameManager's restore closes the
// fresh manager, and a manager that leaks its task pump would leave every
// later test running against a queue nobody drains.
func TestFrameManagerShutdownStopsTaskPumpAndUnblocksPostTask(t *testing.T) {
	before, _, err := TaskPumpGoroutineProfile()
	if err != nil {
		t.Fatalf("capture initial goroutine profile: %v", err)
	}

	manager := vtui.NewFrameManager()
	manager.Init(vtui.NewSilentScreenBuf())
	t.Cleanup(manager.Shutdown)
	taskChan := manager.TaskChan
	if taskChan == nil {
		t.Fatal("Init did not create TaskChan")
	}
	started, profile, err := TaskPumpGoroutineProfile()
	if err != nil {
		t.Fatalf("capture running goroutine profile: %v", err)
	}
	if started <= before {
		t.Fatalf("Init did not start a visible task-pump goroutine: before=%d after=%d\n%s", before, started, profile)
	}

	manager.PostTask(func() {})

	start := make(chan struct{})
	postReturned := make(chan struct{})
	shutdownReturned := make(chan struct{})
	go func() {
		<-start
		manager.PostTask(func() {})
		close(postReturned)
	}()
	go func() {
		<-start
		manager.Shutdown()
		close(shutdownReturned)
	}()
	close(start)

	for _, operation := range []struct {
		name string
		done <-chan struct{}
	}{
		{name: "PostTask", done: postReturned},
		{name: "Shutdown", done: shutdownReturned},
	} {
		select {
		case <-operation.done:
		case <-time.After(time.Second):
			t.Fatalf("concurrent %s did not return after manager shutdown", operation.name)
		}
	}

	if manager.TaskChan != nil {
		t.Fatal("Shutdown left TaskChan attached to the manager")
	}
	select {
	case <-taskChan:
		t.Fatal("the stopped task pump delivered a queued task")
	default:
	}

	after, profile, err := WaitForTaskPumpExit(before, time.Second)
	if err != nil {
		t.Fatalf("capture final goroutine profile: %v", err)
	}
	if after > before {
		t.Fatalf("FrameManager task pump did not exit: before=%d after=%d\n%s", before, after, profile)
	}
}

// Main's leak check used to judge the task pumps from one profile taken right
// after Shutdown, and failed a -race run over a pump whose deferred
// WaitGroup.Done had already let Shutdown return but which was still inside
// runtime.deferreturn when the profile was taken. A pump that is leaving must
// be waited out.
func TestWaitForTaskPumpExitWaitsOutALeavingPump(t *testing.T) {
	counts := []int{1, 1, 0}
	calls := 0
	sample := func() (int, string, error) {
		count := counts[calls]
		calls++
		return count, "", nil
	}

	count, _, err := waitForTaskPumpExit(sample, 0, time.Minute)
	if err != nil || count != 0 || calls != len(counts) {
		t.Fatalf("waitForTaskPumpExit = count %d, err %v after %d samples; want count 0 after %d samples",
			count, err, calls, len(counts))
	}
}

// Waiting must not hide a pump that never leaves: when the time is up the
// count and profile of that pump are what the check reports.
func TestWaitForTaskPumpExitReportsAPumpThatStays(t *testing.T) {
	sample := func() (int, string, error) { return 1, "stuck pump", nil }

	count, profile, err := waitForTaskPumpExit(sample, 0, 10*time.Millisecond)
	if err != nil || count != 1 || profile != "stuck pump" {
		t.Fatalf("waitForTaskPumpExit = count %d, profile %q, err %v; want the stuck pump reported", count, profile, err)
	}
}
