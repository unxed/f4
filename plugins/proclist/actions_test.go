//go:build linux || windows || darwin

package proclist

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// newTestProcListPanel builds a panel the same way TestProcListPanelWiring
// (panel_test.go) does, but returns the concrete type so these tests can
// reach applySamples/selectedSample/suspended directly instead of going
// through ProcessKey and real /proc-style samples.
func newTestProcListPanel(t *testing.T) *procListPanel {
	t.Helper()
	controller, err := newProcListPanel(vfs.PanelContext{Bounds: [4]int{0, 0, 39, 19}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	p, ok := controller.(*procListPanel)
	if !ok {
		t.Fatalf("newProcListPanel returned %T, want *procListPanel", controller)
	}
	return p
}

// killDialogButtons splits confirmKill's dialog children into its two
// buttons, in the order confirmKill itself adds them (btnKill, then
// btnCancel) -- the same "walk GetChildren, bucket by concrete type" idiom
// config_dialog_test.go's openConfigDialog uses for ProcList.Config's own
// dialog.
func killDialogButtons(t *testing.T, dlg *vtui.Window) (kill, cancel *vtui.Button) {
	t.Helper()
	var buttons []*vtui.Button
	for _, child := range dlg.GetChildren() {
		if b, ok := child.(*vtui.Button); ok {
			buttons = append(buttons, b)
		}
	}
	if len(buttons) != 2 {
		t.Fatalf("kill confirmation dialog has %d buttons, want 2", len(buttons))
	}
	return buttons[0], buttons[1]
}

// TestPriorityLevelName locks down priorityLevelKeys' index-to-key mapping
// and its out-of-range guard: adjustPriority names the resulting rung
// through this function in every Shift+F1/F2 toast, so a wrong index here
// would misname the toast without any other test in this package noticing.
func TestPriorityLevelName(t *testing.T) {
	cases := []struct {
		level int
		want  string
	}{
		{-1, ""},
		{0, i18n.Msg("ProcList.Priority.LevelIdle")},
		{1, i18n.Msg("ProcList.Priority.LevelBelowNormal")},
		{2, i18n.Msg("ProcList.Priority.LevelNormal")},
		{3, i18n.Msg("ProcList.Priority.LevelAboveNormal")},
		{4, i18n.Msg("ProcList.Priority.LevelHigh")},
		{5, i18n.Msg("ProcList.Priority.LevelRealtime")},
		{6, ""},
	}
	for _, c := range cases {
		if got := priorityLevelName(c.level); got != c.want {
			t.Errorf("priorityLevelName(%d) = %q, want %q", c.level, got, c.want)
		}
	}
}

// TestActionsWithNoSelectionAreNoOps covers every F8/Shift+F1/Shift+F2/
// Ctrl+F8 handler's shared guard: selectedSample's own doc comment says none
// of them "can do anything useful with an empty table", so all four must
// return quietly instead of dereferencing a sample that is not there.
func TestActionsWithNoSelectionAreNoOps(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	p.applySamples(nil) // empty table: selectedSample must report !ok

	p.confirmKill()
	if top := vtui.FrameManager.GetTopFrame(); top != nil {
		t.Fatalf("confirmKill with no selection pushed a frame: %T", top)
	}

	p.adjustPriority(true)
	p.adjustPriority(false)
	p.toggleSuspend()
	if len(p.suspended) != 0 {
		t.Fatalf("toggleSuspend with no selection touched p.suspended: %#v", p.suspended)
	}
}

// TestAdjustPriorityReportsFailureForAnUncontactablePid exercises
// adjustPriority's error branch: a pid this unlikely to exist -- the same
// value TestKillProcessOnANonexistentPidFails (actions_unix_test.go) uses
// for the same reason -- must reach the user as a toast, not disappear.
func TestAdjustPriorityReportsFailureForAnUncontactablePid(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	p.applySamples([]sample{{pid: 1 << 30, name: "ghost"}})

	p.adjustPriority(true)
	testutil.DrainUITasks()
	if vtui.FrameManager.GetActiveToast() == "" {
		t.Fatal("adjustPriority on a nonexistent pid did not show a failure toast")
	}
}

// TestAdjustPriorityLowersTheSelectedProcess exercises the success path end
// to end (panel -> actions.go -> the platform's real changePriority
// syscalls), the same "spawn a disposable child, act on its pid" pattern
// TestChangePriorityLowersOwnNiceValue (actions_unix_test.go) uses one layer
// down. Only the "lower priority" direction (up=false, a higher nice value
// on *nix) is exercised against the real process: raising it back needs
// CAP_SYS_NICE/RLIMIT_NICE this test's CI user may not have -- the same
// restriction that test already avoids for the same reason. adjustPriority
// itself has no branch on the direction, only on changePriority's error, so
// one successful call already covers its whole success path.
func TestAdjustPriorityLowersTheSelectedProcess(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	cmd := startSleepHelper(t)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	p.applySamples([]sample{{pid: cmd.Process.Pid, name: "sleep"}})

	p.adjustPriority(false)
	testutil.DrainUITasks()
	if vtui.FrameManager.GetActiveToast() == "" {
		t.Fatal("adjustPriority on a real process did not show a toast")
	}
}

// TestToggleSuspendTogglesSuspendedMap covers Ctrl+F8 where SIGSTOP/SIGCONT
// are actually available (actions_unix.go); actions_windows_test.go's own
// TestSuspendResumeReportUnsupported already covers the false side of
// suspendResumeSupported at the lower layer.
func TestToggleSuspendTogglesSuspendedMap(t *testing.T) {
	if !suspendResumeSupported {
		t.Skip("suspend/resume has no supported API here (actions_windows.go)")
	}
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	cmd := startSleepHelper(t)
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	s := sample{pid: cmd.Process.Pid, name: "sleep"}
	p.applySamples([]sample{s})

	p.toggleSuspend()
	if !p.suspended[s.key()] {
		t.Fatal("toggleSuspend did not mark the process suspended")
	}

	p.toggleSuspend()
	if p.suspended[s.key()] {
		t.Fatal("toggleSuspend (second call) did not clear the suspended mark")
	}
}

// TestConfirmKillCancelClosesWithoutKilling covers F8's confirmation dialog
// itself: FAR3's own ProcList always warns before F8, and Cancel -- the
// dialog's default-focused button, so an accidental Enter cannot kill
// anything -- must close the dialog without ever reaching killProcess.
func TestConfirmKillCancelClosesWithoutKilling(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	// Cancel never calls killProcess, so this can safely be a pid that does
	// not exist -- no real process is needed to prove that.
	p.applySamples([]sample{{pid: 1 << 30, name: "imaginary"}})

	p.confirmKill()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("confirmKill did not push a dialog: top frame = %T", vtui.FrameManager.GetTopFrame())
	}
	if !dlg.IsWarning {
		t.Fatal("kill confirmation dialog is not flagged IsWarning (killing is never recoverable)")
	}

	btnKill, btnCancel := killDialogButtons(t, dlg)
	if dlg.GetFocusedItem() != btnCancel {
		t.Fatal("Cancel is not the confirmation dialog's default-focused button")
	}
	_ = btnKill // only Cancel is clicked in this test; Kill is covered below

	btnCancel.OnClick()
	if !dlg.IsDone() || dlg.ExitCode != -1 {
		t.Fatalf("Cancel did not close the dialog: IsDone=%v ExitCode=%d", dlg.IsDone(), dlg.ExitCode)
	}
}

// TestConfirmKillKillTerminatesTheSelectedProcess covers the confirmed path:
// clicking Kill must send the real, unconditional kill (killProcess) and
// close the dialog, with no success toast (the next refresh tick simply
// stops listing the pid, as actions.go's own comment explains).
func TestConfirmKillKillTerminatesTheSelectedProcess(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	cmd := startSleepHelper(t)
	p.applySamples([]sample{{pid: cmd.Process.Pid, name: "sleep"}})

	p.confirmKill()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("confirmKill did not push a dialog: top frame = %T", vtui.FrameManager.GetTopFrame())
	}
	btnKill, _ := killDialogButtons(t, dlg)
	btnKill.OnClick()
	if !dlg.IsDone() {
		t.Fatal("Kill did not close the confirmation dialog")
	}

	err := cmd.Wait()
	if err == nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatal("cmd.Wait() succeeded; the process should have been killed by the Kill button")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("cmd.Wait() error = %v (%T), want *exec.ExitError", err, err)
	}
}

// TestConfirmKillKillShowsAToastWhenTheOSCallFails covers the same failed-OS-
// call toast adjustPriority already gets its own test for, this time for F8:
// actions.go's own doc comment says killProcess "can fail on a process this
// user cannot touch ... and that failure reaches the user as a toast rather
// than disappearing".
func TestConfirmKillKillShowsAToastWhenTheOSCallFails(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	p := newTestProcListPanel(t)
	p.applySamples([]sample{{pid: 1 << 30, name: "ghost"}})

	p.confirmKill()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("confirmKill did not push a dialog: top frame = %T", vtui.FrameManager.GetTopFrame())
	}
	btnKill, _ := killDialogButtons(t, dlg)
	btnKill.OnClick()

	testutil.DrainUITasks()
	if vtui.FrameManager.GetActiveToast() == "" {
		t.Fatal("killing a nonexistent pid did not show a failure toast")
	}
}
