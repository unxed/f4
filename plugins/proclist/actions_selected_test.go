//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

// ghost is a process under a pid nothing has, for actions that must fail;
// each i gives a distinct one.
func ghost(i int) sample {
	return sample{pid: 1<<30 + i, name: "ghost" + strconv.Itoa(i)}
}

func helperSample(cmd *exec.Cmd, name string) sample {
	return sample{pid: cmd.Process.Pid, name: name}
}

func stopHelper(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func selectAll(p *procListPanel, samples ...sample) {
	for _, s := range samples {
		p.marks.set(s.key(), true)
	}
}

func activeToast(t *testing.T) string {
	t.Helper()
	testutil.DrainUITasks()
	return vtui.FrameManager.GetActiveToast()
}

func killDialog(t *testing.T) *vtui.Window {
	t.Helper()
	dlg, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("confirmKill did not push a dialog: top frame = %T", vtui.FrameManager.GetTopFrame())
	}
	return dlg
}

func dialogText(dlg *vtui.Window) string {
	var lines []string
	for _, child := range dlg.GetChildren() {
		if text, ok := child.(*vtui.Text); ok {
			lines = append(lines, text.GetText())
		}
	}
	return strings.Join(lines, "\n")
}

// TestTargetsAreTheSelectionElseTheCursor is the rule every action follows:
// the selected processes when there are any, in the order shown and then
// those a search hides, else the one under the cursor.
func TestTargetsAreTheSelectionElseTheCursor(t *testing.T) {
	p := fakePanel(t)
	p.table.SetSelectPos(0)
	if got := p.targets(); len(got) != 1 || got[0].pid != 101 {
		t.Fatalf("targets with nothing selected = %v, want the cursor's 101", got)
	}

	samples := markTestSamples()
	selectAll(p, samples[6], samples[2]) // 107 sshd, 103 Chrome
	p.table.SetSearchText("sshd")
	var pids []int
	for _, s := range p.targets() {
		pids = append(pids, s.pid)
	}
	if fmt.Sprint(pids) != "[107 103]" {
		t.Fatalf("targets = %v, want [107 103]: the shown one first, then the one the search hides", pids)
	}
}

// TestKillConfirmationForSeveralCountsAndNamesThem: the dialog says how many
// processes it is about to kill and names the first few, and Cancel kills
// none of them.
func TestKillConfirmationForSeveralCountsAndNamesThem(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := frozenTestPanel(t)
	var ghosts []sample
	for i := range 7 {
		ghosts = append(ghosts, ghost(i))
	}
	p.applySamples(ghosts)
	p.sortBy(colPID)
	selectAll(p, ghosts...)

	p.confirmKill()
	dlg := killDialog(t)
	if !dlg.IsWarning {
		t.Fatal("the kill confirmation for several processes is not a warning")
	}
	text := dialogText(dlg)
	for _, want := range []string{
		fmt.Sprintf(i18n.Msg("ProcList.Kill.Item"), "ghost0", ghosts[0].pid),
		fmt.Sprintf(i18n.Msg("ProcList.Kill.Item"), "ghost3", ghosts[3].pid),
		fmt.Sprintf(i18n.Msg("ProcList.Kill.More"), 3),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("kill confirmation %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "ghost4") {
		t.Errorf("kill confirmation %q names more than four of seven processes", text)
	}
	_, btnCancel := killDialogButtons(t, dlg)
	if dlg.GetFocusedItem() != btnCancel {
		t.Fatal("Cancel is not the default button of the kill confirmation for several")
	}
	btnCancel.OnClick()
	if !dlg.IsDone() {
		t.Fatal("Cancel did not close the dialog")
	}
}

// TestKillSeveralKillsTheSelectedAndReportsFailures: Kill reaches every
// selected process, and the ones it could not kill are counted in one toast.
func TestKillSeveralKillsTheSelectedAndReportsFailures(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := frozenTestPanel(t)
	a, b := startSleepHelper(t), startSleepHelper(t)
	defer stopHelper(a)
	defer stopHelper(b)
	missing := ghost(0)
	p.applySamples([]sample{helperSample(a, "a"), helperSample(b, "b"), missing})
	selectAll(p, helperSample(a, "a"), helperSample(b, "b"), missing)

	p.confirmKill()
	btnKill, _ := killDialogButtons(t, killDialog(t))
	btnKill.OnClick()
	for _, cmd := range []*exec.Cmd{a, b} {
		if err := cmd.Wait(); err == nil {
			t.Fatalf("helper %d exited cleanly; Kill should have killed it", cmd.Process.Pid)
		}
	}
	toast := activeToast(t)
	if !strings.Contains(toast, strconv.Itoa(missing.pid)) {
		t.Fatalf("toast %q does not name the process that could not be killed (%d)", toast, missing.pid)
	}
}

// TestKillSkipsATargetTheSnapshotNoLongerLists: the dialog may stay open
// across refreshes. A target that is gone by the time Kill is pressed --
// or whose pid now names another process -- must not be signalled.
func TestKillSkipsATargetTheSnapshotNoLongerLists(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := frozenTestPanel(t)
	cmd := startSleepHelper(t)
	defer stopHelper(cmd)
	original := helperSample(cmd, "sleep")
	original.start = 1
	p.applySamples([]sample{original})
	selectAll(p, original)

	p.confirmKill()
	btnKill, _ := killDialogButtons(t, killDialog(t))
	// Meanwhile the pid turns out to belong to a process started later.
	reused := original
	reused.start = 2
	p.applySamples([]sample{reused})
	btnKill.OnClick()

	exited := make(chan struct{})
	go func() {
		_, _ = cmd.Process.Wait()
		close(exited)
	}()
	select {
	case <-exited:
		t.Fatal("Kill signalled a pid whose process is no longer the one selected")
	case <-time.After(300 * time.Millisecond):
	}
	_ = cmd.Process.Kill()
	<-exited
}

func TestAdjustPriorityOfSeveral(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := frozenTestPanel(t)
	a, b := startSleepHelper(t), startSleepHelper(t)
	defer stopHelper(a)
	defer stopHelper(b)
	p.applySamples([]sample{helperSample(a, "a"), helperSample(b, "b"), ghost(0), ghost(1)})
	p.sortBy(colPID)

	// Lowering needs no privilege (see TestAdjustPriorityLowersTheSelectedProcess).
	selectAll(p, helperSample(a, "a"), helperSample(b, "b"))
	p.adjustPriority(false)
	if got, want := activeToast(t), fmt.Sprintf(i18n.Msg("ProcList.Priority.ChangedMany"), 2); got != want {
		t.Fatalf("toast = %q, want %q", got, want)
	}

	clear(p.marks)
	selectAll(p, ghost(0), ghost(1))
	p.adjustPriority(false)
	toast := activeToast(t)
	if !strings.Contains(toast, strconv.Itoa(ghost(0).pid)) || !strings.Contains(toast, " 2 ") {
		t.Fatalf("toast %q does not count the 2 failures and name the first, %d", toast, ghost(0).pid)
	}
}

// TestToggleSuspendOfSeveralTogglesAsAGroup: a mixed selection is suspended
// whole, and a wholly suspended one is resumed whole.
func TestToggleSuspendOfSeveralTogglesAsAGroup(t *testing.T) {
	if !suspendResumeSupported {
		t.Skip("suspend/resume has no supported API here (actions_windows.go)")
	}
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	p := frozenTestPanel(t)
	a, b := startSleepHelper(t), startSleepHelper(t)
	defer stopHelper(a)
	defer stopHelper(b)
	sa, sb := helperSample(a, "a"), helperSample(b, "b")
	p.applySamples([]sample{sa, sb})

	p.table.SetSelectPos(p.positionOf(func(s sample) bool { return s.pid == sa.pid }))
	p.toggleSuspend() // a alone, from the cursor
	if !p.suspended[sa.key()] {
		t.Fatal("Ctrl+F8 on a did not suspend it")
	}

	selectAll(p, sa, sb)
	p.toggleSuspend()
	if !p.suspended[sa.key()] || !p.suspended[sb.key()] {
		t.Fatalf("Ctrl+F8 on a mixed selection left %v, want both suspended", p.suspended)
	}
	if got, want := activeToast(t), fmt.Sprintf(i18n.Msg("ProcList.Suspend.DoneMany"), 1); got != want {
		t.Fatalf("toast = %q, want %q: only b needed suspending", got, want)
	}

	p.toggleSuspend()
	if len(p.suspended) != 0 {
		t.Fatalf("Ctrl+F8 on a suspended selection left %v suspended, want none", p.suspended)
	}
	if got, want := activeToast(t), fmt.Sprintf(i18n.Msg("ProcList.Resume.DoneMany"), 2); got != want {
		t.Fatalf("toast = %q, want %q", got, want)
	}
}
