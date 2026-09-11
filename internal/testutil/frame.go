package testutil

import (
	"bytes"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/unxed/vtui"
	"github.com/unxed/vtui/vreactive"
)

func CloseFrameManagerScreens(screens []*vtui.AppScreen) {
	for _, screen := range screens {
		if screen == nil {
			continue
		}
		for _, frame := range screen.Frames {
			if frame != nil {
				frame.Close()
			}
		}
	}
}

func CloseFrameManagerFrames(manager *vtui.FrameManagerType) {
	if manager != nil {
		CloseFrameManagerScreens(manager.Screens)
	}
}

// SetFrameManagerScreens installs a screen fixture for the current manager.
// Its cleanup closes exactly the frames supplied by the fixture and then
// restores the manager's previous screen state.
func SetFrameManagerScreens(t *testing.T, screens []*vtui.AppScreen, activeIdx int) func() {
	t.Helper()
	manager := vtui.FrameManager
	oldScreens := manager.Screens
	oldActiveIdx := manager.ActiveIdx
	manager.Screens = screens
	manager.ActiveIdx = activeIdx
	return func() {
		CloseFrameManagerScreens(screens)
		manager.Screens = oldScreens
		manager.ActiveIdx = oldActiveIdx
	}
}

// AppendFrameManagerScreen adds one owned screen fixture and removes it during
// cleanup after closing its frames.
func AppendFrameManagerScreen(t *testing.T, screen *vtui.AppScreen, activeIdx int) func() {
	t.Helper()
	manager := vtui.FrameManager
	oldScreens := manager.Screens
	oldActiveIdx := manager.ActiveIdx
	manager.Screens = append(manager.Screens, screen)
	manager.ActiveIdx = activeIdx
	return func() {
		CloseFrameManagerScreens([]*vtui.AppScreen{screen})
		manager.Screens = oldScreens
		manager.ActiveIdx = oldActiveIdx
	}
}

func TaskPumpGoroutineProfile() (int, string, error) {
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		return 0, "", err
	}
	profile := stacks.String()
	return strings.Count(profile, ".startTaskPump.func"), profile, nil
}

// WaitForTaskPumpExit samples the goroutine profile until at most limit task
// pumps remain or timeout passes, and returns the last count and profile.
//
// One sample taken straight after Shutdown is not enough. Shutdown joins the
// pump through a WaitGroup, and the pump calls Done from its own deferred call:
// Wait returns while the goroutine is still finishing its return, and a
// profile taken in that window still lists it. A stopped pump has nothing
// left to do but return, so waiting only outlasts one that is leaving; a pump
// whose manager was never shut down stays parked in its select and is still
// counted when the wait gives up.
func WaitForTaskPumpExit(limit int, timeout time.Duration) (int, string, error) {
	return waitForTaskPumpExit(TaskPumpGoroutineProfile, limit, timeout)
}

func waitForTaskPumpExit(sample func() (int, string, error), limit int, timeout time.Duration) (int, string, error) {
	deadline := time.Now().Add(timeout)
	for {
		count, profile, err := sample()
		if err != nil || count <= limit || !time.Now().Before(deadline) {
			return count, profile, err
		}
		time.Sleep(time.Millisecond)
	}
}

func PumpUntilToastActive(t *testing.T) {
	t.Helper()
	for vtui.FrameManager.GetActiveToast() == "" {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(time.Second):
			t.Fatal("toast did not start")
		}
	}
}

func WaitForToastExpiry(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-vtui.FrameManager.RedrawChan:
			if vtui.FrameManager.GetActiveToast() == "" {
				return
			}
		case <-deadline:
			t.Fatal("toast did not expire")
		}
	}
}

// DrainPendingTasks discards whatever is queued for the UI thread without
// running any of it.
func DrainPendingTasks() {
	for {
		select {
		case <-vtui.FrameManager.TaskChan:
		default:
			return
		}
	}
}

// DrainUITasks runs queued UI work until a task posted now comes back round,
// which is when everything posted before it has run.
func DrainUITasks() {
	done := make(chan struct{})
	vtui.FrameManager.PostTask(func() { close(done) })
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-done:
			return
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			return
		}
	}
}

// ScreenRow reads a stretch of one row back out of the screen.
func ScreenRow(scr *vtui.ScreenBuf, y, x1, x2 int) string {
	runes := make([]rune, x2-x1+1)
	for i := range runes {
		cell := scr.GetCell(x1+i, y)
		runes[i] = vtui.CellBaseRune(cell.Char)
		if runes[i] == 0 {
			runes[i] = ' '
		}
	}
	return string(runes)
}

// SwapFrameManager replaces the global vtui.FrameManager with a fresh,
// independent instance and returns a function that restores the original
// pointer. In particular, the fresh manager has its own task queue: Init
// deliberately preserves an existing queue, which is useful in production but
// lets queued UI work escape from one test into the next.
//
// Each drain runs before the swap and before the restore, in the order given.
// A caller passes the waits for whichever background workers its own package
// leaves running: those workers read vtui.FrameManager while they run, and one
// still reading the manager being replaced is what the race detector reports
// against whichever test is unlucky enough to do the replacing. Joining them
// first is what makes the swap safe.
func SwapFrameManager(t *testing.T, drains ...func(*testing.T)) func() {
	t.Helper()
	for _, drain := range drains {
		drain(t)
	}
	old := vtui.FrameManager
	oldUpdateQueue := vreactive.GlobalUpdateQueue
	oldAnimationManager := vreactive.GlobalAnimationManager
	fresh := vtui.NewFrameManager()
	vtui.FrameManager = fresh

	return func() {
		for _, drain := range drains {
			drain(t)
		}
		CloseFrameManagerFrames(fresh)
		fresh.Shutdown()
		vtui.FrameManager = old
		vreactive.GlobalUpdateQueue = oldUpdateQueue
		vreactive.GlobalAnimationManager = oldAnimationManager
	}
}
