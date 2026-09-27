//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/vtui"
)

// f4#312 part 3 of 4: process management on top of parts 1/2's view-only
// list. Every handler here is reached only from panel.go's ProcessKey, on
// the process under the cursor (selectedSample), and every one of them is
// silent about an empty table (nothing selected) but never silent about a
// failed OS call: killProcess/changePriority/suspendProcess/resumeProcess
// (actions_unix.go, actions_windows.go) can fail on a process this user
// cannot touch -- another user's, or one root-owned without privilege --
// and that failure reaches the user as a toast rather than disappearing.
//
// FAR3's own ProcList always warns before F8 (Plist.cpp's own confirmation,
// "Note: this action is irreversible!"); confirmKill below is the same
// warning, unconditional -- there is no config knob to skip it, matching how
// narrowly this plugin scopes everything else (see plugin.go's own package
// doc) rather than adding a setting nothing yet asked for.

const toastErrorDuration = 3 * time.Second
const toastInfoDuration = 1500 * time.Millisecond

// confirmKill shows the "this action cannot be undone" dialog FAR3's own F8
// help text describes, then -- only on confirmation -- sends an
// unconditional kill (SIGKILL on *nix, TerminateProcess on Windows; never a
// graceful SIGTERM-style request, matching what F8 does in FAR3).
func (p *procListPanel) confirmKill() {
	s, ok := p.selectedSample()
	if !ok {
		return
	}

	title := i18n.Msg("ProcList.Kill.Title")
	msg := fmt.Sprintf(i18n.Msg("ProcList.Kill.Confirm"), s.name, s.pid)
	lines := vtui.WrapText(msg, 46)

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
		if err := killProcess(s.pid); err != nil {
			toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Kill.Failed"), s.pid, err), toastErrorDuration)
		}
		// No success toast and no optimistic row removal: the next refresh
		// tick (at most procListRefreshInterval away) simply stops listing a
		// pid that no longer exists, the same way any other process's exit
		// is reflected.
	}
	// Cancel focused by default: kill is the one action on this whole panel
	// an accidental Enter must not trigger.
	dlg.SetFocusedItem(btnCancel)
	vtui.FrameManager.Push(dlg)
}

// adjustPriority moves the selected process one rung up (raise, Shift+F2)
// or down (lower, Shift+F1) the six-level priority ladder each platform's
// changePriority defines -- FAR3's own Shift-F1/F2 gesture, ported from
// Windows' six PRIORITY_CLASS values to an equivalent six-rung nice ladder
// on *nix (actions_unix.go). There is no priority column in this v1 table
// (a part 4/detail-view candidate), so both outcomes get a toast: silently
// doing nothing on a permission failure would be indistinguishable from a
// keypress that did not register at all.
func (p *procListPanel) adjustPriority(up bool) {
	s, ok := p.selectedSample()
	if !ok {
		return
	}
	level, err := changePriority(s.pid, up)
	if err != nil {
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Priority.Failed"), s.pid, err), toastErrorDuration)
		return
	}
	toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Priority.Changed"), s.name, priorityLevelName(level)), toastInfoDuration)
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
func (p *procListPanel) toggleSuspend() {
	if !suspendResumeSupported {
		return
	}
	s, ok := p.selectedSample()
	if !ok {
		return
	}

	if p.suspended[s.pid] {
		if err := resumeProcess(s.pid); err != nil {
			toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Resume.Failed"), s.pid, err), toastErrorDuration)
			return
		}
		delete(p.suspended, s.pid)
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Resume.Done"), s.pid), toastInfoDuration)
		return
	}

	if err := suspendProcess(s.pid); err != nil {
		toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Suspend.Failed"), s.pid, err), toastErrorDuration)
		return
	}
	p.suspended[s.pid] = true
	toast.Show(fmt.Sprintf(i18n.Msg("ProcList.Suspend.Done"), s.pid), toastInfoDuration)
}
