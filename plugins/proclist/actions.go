//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/vtui"
)

// f4#312 part 3 of 4: process management on top of parts 1/2's view-only
// list. Every handler here is reached only from panel.go's ProcessKey, and
// acts on its targets: the selected processes when there are any (marks.go,
// f4#2130), else the process under the cursor -- the file panels' rule, and
// FAR3's ProcList's, which applies F8 and Shift+F1/F2 to every selected
// process too. Every one of them is silent about an empty table (nothing
// to act on) but never silent about a failed OS call:
// killProcess/changePriority/suspendProcess/resumeProcess (actions_unix.go,
// actions_windows.go) can fail on a process this user cannot touch --
// another user's, or one root-owned without privilege -- and that failure
// reaches the user as a toast rather than disappearing. With several
// targets the toast counts the failures and names the first.
//
// FAR3's own ProcList always warns before F8 (Plist.cpp's own confirmation,
// "Note: this action is irreversible!"); confirmKill below is the same
// warning, unconditional -- there is no config knob to skip it, matching how
// narrowly this plugin scopes everything else (see plugin.go's own package
// doc) rather than adding a setting nothing yet asked for.

const toastErrorDuration = 3 * time.Second
const toastInfoDuration = 1500 * time.Millisecond

// targets is what an action acts on: the selected processes, top to bottom
// as shown and then those a QuickSearch hides, or else the process under
// the cursor.
func (p *procListPanel) targets() []sample {
	if len(p.marks) == 0 {
		if s, ok := p.selectedSample(); ok {
			return []sample{s}
		}
		return nil
	}
	targets := make([]sample, 0, len(p.marks))
	seen := make(map[procKey]bool, len(p.marks))
	p.eachShown(func(_ int, s sample) {
		if p.marks.has(s.key()) {
			targets = append(targets, s)
			seen[s.key()] = true
		}
	})
	for _, r := range p.table.Rows {
		if pr, ok := r.(procRow); ok && p.marks.has(pr.s.key()) && !seen[pr.s.key()] {
			targets = append(targets, pr.s)
		}
	}
	return targets
}

// stillListed keeps the targets the current snapshot still lists as the same
// process. The kill dialog may stay open across many refreshes; a target that
// exited meanwhile needs no kill, and a pid the OS has since given to another
// process must not get one.
func (p *procListPanel) stillListed(targets []sample) []sample {
	listed := p.listedKeys()
	kept := make([]sample, 0, len(targets))
	for _, s := range targets {
		if listed[s.key()] {
			kept = append(kept, s)
		}
	}
	return kept
}

// failures collects what went wrong over several targets, for one toast.
type failures struct {
	count    int
	firstPID int
	firstErr error
}

func (f *failures) add(pid int, err error) {
	if f.count == 0 {
		f.firstPID, f.firstErr = pid, err
	}
	f.count++
}

// toast reports the failures among total targets, with messageKey's
// "%d of %d (PID %d: %v)" wording; nothing when there were none.
func (f failures) toast(messageKey string, total int) {
	if f.count > 0 {
		toast.Show(fmt.Sprintf(i18n.Msg(messageKey), f.count, total, f.firstPID, f.firstErr), toastErrorDuration)
	}
}

// killPreviewLimit is how many of the processes F8 is about to kill its
// dialog names; the rest are counted.
const killPreviewLimit = 5

// confirmKill shows the "this action cannot be undone" dialog FAR3's own F8
// help text describes, then -- only on confirmation -- sends an
// unconditional kill (SIGKILL on *nix, TerminateProcess on Windows; never a
// graceful SIGTERM-style request, matching what F8 does in FAR3). With
// several processes selected the dialog says how many and names the first
// few, the way the file panels' delete confirmation counts its files.
func (p *procListPanel) confirmKill() {
	targets := p.targets()
	if len(targets) == 0 {
		return
	}

	title := i18n.Msg("ProcList.Kill.Title")
	var lines []string
	if len(targets) == 1 {
		s := targets[0]
		lines = vtui.WrapText(fmt.Sprintf(i18n.Msg("ProcList.Kill.Confirm"), s.name, s.pid), 46)
	} else {
		title = i18n.Msg("ProcList.Kill.TitleMany")
		lines = killManyLines(targets)
	}

	const dialogWidth = 50
	height := 8 + len(lines)
	dlg := vtui.NewCenteredDialog(dialogWidth, height, title)
	// Killing a process is never recoverable, unlike moving a file to the
	// Recycle Bin -- actions.go in internal/app keeps that distinction for
	// delete/trash, and it applies here too.
	dlg.IsWarning = true

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dialogWidth-4, height-4)
	for _, l := range lines {
		t := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(t)
		vbox.Add(t, vtui.Margins{}, vtui.AlignCenter)
	}

	btnKill := vtui.NewButton(0, 0, i18n.Msg("ProcList.Kill.Btn"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	btnCancel.IsDefault = true
	dlg.AddItem(btnKill)
	dlg.AddItem(btnCancel)

	hbox := vtui.NewHBoxLayout(0, 0, dialogWidth-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnKill, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnKill.OnClick = func() {
		dlg.Close()
		p.killTargets(targets)
		// No success toast and no optimistic row removal: the next refresh
		// tick (at most procListRefreshInterval away) simply stops listing a
		// pid that no longer exists, the same way any other process's exit
		// is reflected. Its selection goes with it (applySamples); a process
		// that could not be killed stays selected, so the user sees which.
	}
	// Cancel focused by default: kill is the one action on this whole panel
	// an accidental Enter must not trigger.
	dlg.SetFocusedItem(btnCancel)
	vtui.FrameManager.Push(dlg)
}

// killManyLines is the kill confirmation for several processes: how many,
// the first few by name, and the rest counted, in at most killPreviewLimit
// name lines.
func killManyLines(targets []sample) []string {
	shown := targets
	if len(shown) > killPreviewLimit {
		shown = shown[:killPreviewLimit-1]
	}
	names := make([]string, 0, killPreviewLimit)
	for _, s := range shown {
		names = append(names, fmt.Sprintf(i18n.Msg("ProcList.Kill.Item"), s.name, s.pid))
	}
	if rest := len(targets) - len(shown); rest > 0 {
		names = append(names, fmt.Sprintf(i18n.Msg("ProcList.Kill.More"), rest))
	}
	return vtui.WrapText(fmt.Sprintf(i18n.Msg("ProcList.Kill.ConfirmMany"), len(targets), strings.Join(names, "\n")), 46)
}

// killTargets kills what the current snapshot still lists of targets. f4's
// own process, when it is among them, goes last: selecting everything and
// pressing F8 still reaches every other process before f4 itself is gone.
func (p *procListPanel) killTargets(targets []sample) {
	targets = p.stillListed(targets)
	if len(targets) == 1 {
		if err := killProcess(targets[0].pid); err != nil {
			toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Kill.Failed"), targets[0].pid, err), toastErrorDuration)
		}
		return
	}
	self := os.Getpid()
	var failed failures
	killedSelf := false
	for _, s := range targets {
		if s.pid == self {
			killedSelf = true
			continue
		}
		if err := killProcess(s.pid); err != nil {
			failed.add(s.pid, err)
		}
	}
	failed.toast("ProcList.Kill.FailedMany", len(targets))
	if killedSelf {
		_ = killProcess(self)
	}
}

// adjustPriority moves the targets one rung up (raise, Shift+F2) or down
// (lower, Shift+F1) the six-level priority ladder each platform's
// changePriority defines -- FAR3's own Shift-F1/F2 gesture, ported from
// Windows' six PRIORITY_CLASS values to an equivalent six-rung nice ladder
// on *nix (actions_unix.go). Each process moves one rung from where it is,
// so a selection of processes at different priorities keeps their order.
// There is no priority column in this v1 table (a part 4/detail-view
// candidate), so both outcomes get a toast: silently doing nothing on a
// permission failure would be indistinguishable from a keypress that did
// not register at all. One process is named with its new priority; several
// are counted. The selection stays, so a second press moves them again.
func (p *procListPanel) adjustPriority(up bool) {
	targets := p.targets()
	switch len(targets) {
	case 0:
		return
	case 1:
		s := targets[0]
		level, err := changePriority(s.pid, up)
		if err != nil {
			toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Priority.Failed"), s.pid, err), toastErrorDuration)
			return
		}
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Priority.Changed"), s.name, priorityLevelName(level)), toastInfoDuration)
		return
	}
	var failed failures
	for _, s := range targets {
		if _, err := changePriority(s.pid, up); err != nil {
			failed.add(s.pid, err)
		}
	}
	if failed.count > 0 {
		failed.toast("ProcList.Priority.FailedMany", len(targets))
		return
	}
	toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Priority.ChangedMany"), len(targets)), toastInfoDuration)
}

// priorityLevelKeys is priorityLadder's shared naming, indexed the same way
// actions_unix.go's niceLadder and actions_windows.go's priorityLadder both
// are: 0 Idle .. 5 Realtime.
var priorityLevelKeys = [...]string{
	"ProcList.Priority.LevelIdle",
	"ProcList.Priority.LevelBelowNormal",
	"ProcList.Priority.LevelNormal",
	"ProcList.Priority.LevelAboveNormal",
	"ProcList.Priority.LevelHigh",
	"ProcList.Priority.LevelRealtime",
}

func priorityLevelName(level int) string {
	if level < 0 || level >= len(priorityLevelKeys) {
		return ""
	}
	return i18n.Msg(priorityLevelKeys[level])
}

// toggleSuspend is Ctrl+F8: not a FAR3 gesture (its ProcList has no
// suspend/resume at all), added here because SIGSTOP/SIGCONT make it
// essentially free on *nix -- and left out of the key's reach entirely on
// Windows, which the ticket's own scope (f4#312 part 3) says not to force
// through an unsupported API. suspendResumeSupported is false there
// (actions_windows.go), and panel.go's ProcessKey never calls this method
// in that build in the first place; the check is repeated here only so this
// method stays safe to call regardless of which caller reaches it.
//
// Several targets toggle as one, the way a group checkbox does: when this
// panel has suspended every one of them, Ctrl+F8 resumes them all;
// otherwise it suspends the ones still running, so a mixed selection ends up
// all suspended and the next Ctrl+F8 resumes it whole.
func (p *procListPanel) toggleSuspend() {
	if !suspendResumeSupported {
		return
	}
	targets := p.targets()
	if len(targets) == 0 {
		return
	}
	if len(targets) == 1 {
		p.toggleSuspendOne(targets[0])
		return
	}

	resume := true
	for _, s := range targets {
		if !p.suspended[s.key()] {
			resume = false
			break
		}
	}
	var failed failures
	attempted := 0
	for _, s := range targets {
		switch {
		case resume:
			attempted++
			if err := resumeProcess(s.pid); err != nil {
				failed.add(s.pid, err)
				continue
			}
			delete(p.suspended, s.key())
		case !p.suspended[s.key()]:
			attempted++
			if err := suspendProcess(s.pid); err != nil {
				failed.add(s.pid, err)
				continue
			}
			p.suspended[s.key()] = true
		}
	}
	if resume {
		if failed.count > 0 {
			failed.toast("ProcList.Resume.FailedMany", attempted)
			return
		}
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Resume.DoneMany"), attempted), toastInfoDuration)
		return
	}
	if failed.count > 0 {
		failed.toast("ProcList.Suspend.FailedMany", attempted)
		return
	}
	toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Suspend.DoneMany"), attempted), toastInfoDuration)
}

func (p *procListPanel) toggleSuspendOne(s sample) {
	if p.suspended[s.key()] {
		if err := resumeProcess(s.pid); err != nil {
			toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Resume.Failed"), s.pid, err), toastErrorDuration)
			return
		}
		delete(p.suspended, s.key())
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Resume.Done"), s.pid), toastInfoDuration)
		return
	}

	if err := suspendProcess(s.pid); err != nil {
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Suspend.Failed"), s.pid, err), toastErrorDuration)
		return
	}
	p.suspended[s.key()] = true
	toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Suspend.Done"), s.pid), toastInfoDuration)
}
