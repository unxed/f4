package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/unxed/f4/fusefs"
	"github.com/unxed/vtui"
)

// The mounts dialog (FUSE.md, iteration 2): one list over the live mounts,
// with Go to and Unmount on the selected one.
//
// It lists the mounts this process owns — the ones the panel command makes.
// Mounts started from a shell live in the cross-process registry and are a
// separate step.
func init() {
	RegisterAction(Action{
		Name:        "Panel.MountList",
		Area:        "Shell",
		Label:       "FUSE Mounts",
		Description: "List the live FUSE mounts, go to one or unmount it",
		MenuPath:    "Commands",
		Visible:     fusefs.Supported,
		Handler: func() bool {
			pf := findPanelsFrameAnyScreen()
			if pf == nil {
				return false
			}
			showMountList(pf)
			return true
		},
	})
}

// mountRow is one line of the dialog: a mount this process owns, or a record
// of one some other f4 owns.
type mountRow struct {
	point  string
	source string
	mode   string
	age    time.Duration
	note   string
	live   *fusefs.Mount // nil when the mount belongs to another process
}

// mountRows lists what is mounted right now: this process's mounts first,
// then the registry records that describe everybody else's — a mount started
// from a shell or by fstab is invisible otherwise.
func mountRows() []mountRow {
	var rows []mountRow
	seen := make(map[string]bool)
	for _, m := range fusefs.List() {
		rows = append(rows, mountRow{
			point:  m.MountPoint,
			source: m.Source,
			mode:   mountMode(m.ReadOnly),
			age:    time.Since(m.Since).Truncate(time.Second),
			live:   m,
		})
		seen[m.MountPoint] = true
	}
	recs, err := fusefs.Mounts()
	if err != nil {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].age > rows[j].age })
		return rows
	}
	for _, r := range recs {
		if seen[r.MountPoint] {
			continue
		}
		rows = append(rows, mountRow{
			point:  r.MountPoint,
			source: r.Source,
			mode:   r.Mode(),
			age:    r.Age().Truncate(time.Second),
			note:   fmt.Sprintf(" (pid %d)", r.PID),
		})
	}
	// Oldest first, the way fusefs.List() orders its own: a merged list that
	// kept this process's mounts on top would read as two lists stacked.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].age > rows[j].age })
	return rows
}

func showMountList(pf *PanelsFrame) {
	rows := mountRows()
	if len(rows) == 0 {
		vtui.ShowMessage(Msg("Mounts.Title"), "Nothing is mounted.", []string{"&Ok"})
		return
	}

	menu := vtui.NewVMenu(Msg("Mounts.Title"))
	if live := liveRows(rows); len(live) > 1 {
		menu.AddItem(vtui.MenuItem{
			Text:     fmt.Sprintf("Unmount all (%d)", len(live)),
			UserData: -1,
		})
	}
	for i, r := range rows {
		menu.AddItem(vtui.MenuItem{
			Text:     fmt.Sprintf("%s  %s %s  \u2190  %s%s", r.point, r.mode, r.age, r.source, r.note),
			UserData: i,
		})
	}

	w, h := 70, len(rows)+2
	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	if maxH := scrH - 2; h > maxH && maxH >= 5 {
		h = maxH
	}
	if w > scrW-2 {
		w = scrW - 2
	}
	x, y := (scrW-w)/2, (scrH-h)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	menu.SetPosition(x, y, x+w-1, y+h-1)

	menu.OnAction = func(idx int) {
		menu.Close()
		if idx < 0 || idx >= len(menu.Items) {
			return
		}
		i, ok := menu.Items[idx].UserData.(int)
		if !ok {
			return
		}
		if i < 0 {
			unmountAll(rows)
			return
		}
		if i >= len(rows) {
			return
		}
		askMountAction(pf, rows[i])
	}
	vtui.FrameManager.Push(menu)
}

// askMountAction offers what can be done to the selected mount. Unmount does
// not force: a busy mount is a question for the user ("something is still in
// there"), not an error to paper over. A mount owned by another process can
// only be visited from here — taking it down is what f4 --umount is for.
func askMountAction(pf *PanelsFrame, row mountRow) {
	buttons := []string{"&Go to", "&Unmount", "&Cancel"}
	if row.live == nil {
		buttons = []string{"&Go to", "&Cancel"}
	}
	dlg := vtui.ShowMessage(" Mount ", row.point, buttons)
	dlg.OnResult = func(code int) {
		if code == 0 {
			if fsp := pf.getActivePanel(); fsp != nil {
				pf.NavigateToPath(fsp, row.point)
			}
			return
		}
		if code == 1 && row.live != nil {
			if err := row.live.Unmount(); err != nil {
				vtui.ShowMessage(" Mount ", fmt.Sprintf("Cannot unmount %s:\n%v", row.point, err), []string{"&Ok"})
			}
		}
	}
}

// liveRows are the mounts this process can actually take down.
func liveRows(rows []mountRow) []mountRow {
	var live []mountRow
	for _, r := range rows {
		if r.live != nil {
			live = append(live, r)
		}
	}
	return live
}

// unmountAll takes down every mount this process owns and reports the ones
// that would not go, rather than stopping at the first. A busy mount is not a
// reason to leave the others up.
func unmountAll(rows []mountRow) {
	var failed []string
	for _, r := range liveRows(rows) {
		if err := r.live.Unmount(); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", r.point, err))
		}
	}
	if len(failed) > 0 {
		vtui.ShowMessage(Msg("Mounts.Title"), "Still mounted:\n"+strings.Join(failed, "\n"), []string{"&Ok"})
	}
}

// mountMode renders the access mode the way mount(8) output does, so a mount
// this process owns and a record from the registry read the same.
func mountMode(readOnly bool) string {
	if readOnly {
		return "ro"
	}
	return "rw"
}
