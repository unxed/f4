package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/f4/vfs/hostpath"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// dragOutState remembers a left button press that may become a drag out of
// f4, and the names it would carry. A press on a marked file carries every
// marked file and starts the drag on the first move, as it always has. A
// press on an unmarked file while nothing is marked carries just that file:
// the press has already put the cursor on it, so this is the "current file"
// that every other command falls back to. That drag starts only once the
// pointer leaves the panel's rows, so a left drag inside the panel still
// just moves the cursor.
type dragOutState struct {
	panel      *FileSystemPanel
	names      []string
	x, y       int
	armed      bool
	cursorOnly bool
}

// readOnlyVFS is implemented by a file system that already knows it cannot
// be written to. Nothing implements it yet: a VFS that does not is assumed
// to accept files, and a refusal then arrives as an ordinary file operation
// error, in the dialog the user already knows. Archives that are opened
// read-only should implement it so the pointer says "no" before the drop.
type readOnlyVFS interface {
	IsReadOnly() bool
}

// dropTargetInfo is where a payload dropped at a screen cell would land.
// The destination is a directory inside the panel's own VFS, which may be an
// archive or a network connection just as well as the local disk.
type dropTargetInfo struct {
	panelIdx int
	panel    *FileSystemPanel
	dir      string
	fs       vfs.VFS
	entryIdx int
}

// dropSourceGroup is one source directory and the names taken from it. A
// file manager copies "these names out of that directory", so a drop of
// files scattered over several directories becomes several operations.
type dropSourceGroup struct {
	dir   string
	names []string
}

// groupDropSources turns the absolute paths of a payload into groups,
// preserving the order the directories were first seen and sorting the names
// inside each group, so the same drop always produces the same operations.
func normalizeExternalDropPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if hostmode.Posix() {
		// Wine knows how this prefix maps drives onto the file system and
		// nothing else does, so ask it first. The string rules below are
		// the fallback for when it cannot answer: they cover the two
		// spellings that carry their own meaning -- Z: for the root and
		// the \\?\unix form for a file no drive covers -- but a drop can
		// just as well name C:\users\... or a drive the user mapped
		// himself, and those a string rule cannot reach.
		if unix, ok := hostUnixPath(raw); ok && unix != "" {
			cleaned := hostpath.Clean(unix)
			vtui.DebugLog("DND: Wine translated %q to %q", raw, cleaned)
			return cleaned
		}
		vtui.DebugLog("DND: Wine did not translate %q, falling back to the string rules", raw)
		// If path came from Wine as Z:\... or z:\... (which maps to root /)
		if len(raw) >= 3 && (raw[0] == 'Z' || raw[0] == 'z') && raw[1] == ':' && (raw[2] == '\\' || raw[2] == '/') {
			raw = "/" + strings.TrimPrefix(raw[3:], "\\")
			raw = strings.ReplaceAll(raw, "\\", "/")
		} else if strings.HasPrefix(raw, `\\?\unix\`) || strings.HasPrefix(raw, `\\?\unix/`) {
			raw = strings.TrimPrefix(raw, `\\?\unix\`)
			raw = strings.TrimPrefix(raw, `\\?\unix/`)
			raw = "/" + strings.ReplaceAll(raw, "\\", "/")
		} else if strings.HasPrefix(raw, `\??\unix\`) || strings.HasPrefix(raw, `\??\unix/`) {
			raw = strings.TrimPrefix(raw, `\??\unix\`)
			raw = strings.TrimPrefix(raw, `\??\unix/`)
			raw = "/" + strings.ReplaceAll(raw, "\\", "/")
		}
	}
	cleaned := hostpath.Clean(raw)
	vtui.DebugLog("DND: dropped path %q resolved to %q", raw, cleaned)
	return cleaned
}

func groupDropSources(paths []string) []dropSourceGroup {
	byDir := make(map[string][]string)
	seen := make(map[string]bool)
	var order []string

	for _, raw := range paths {
		normalized := normalizeExternalDropPath(raw)
		if normalized == "" {
			continue
		}
		dir := hostpath.Dir(normalized)
		name := hostpath.Base(normalized)
		if name == "" || name == "." || name == ".." {
			continue
		}
		key := hostpath.Join(dir, name)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := byDir[dir]; !ok {
			order = append(order, dir)
		}
		byDir[dir] = append(byDir[dir], name)
	}

	groups := make([]dropSourceGroup, 0, len(order))
	for _, dir := range order {
		names := byDir[dir]
		sort.Strings(names)
		groups = append(groups, dropSourceGroup{dir: dir, names: names})
	}
	return groups
}

// chooseDropAction picks what a drop does. Every graphical file manager, and
// Far itself for its own drags, agrees on the modifiers: Shift moves, Ctrl
// copies. With neither the source's own suggestion wins, and copy is the
// fallback because it is the one that cannot lose data.
func chooseDropAction(allowed, suggested vtui.DropAction, mods vtinput.ControlKeyState) vtui.DropAction {
	if allowed == vtui.DropNone {
		return vtui.DropNone
	}
	shift := mods&vtinput.ShiftPressed != 0
	ctrl := mods&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0

	if shift && !ctrl && allowed.Has(vtui.DropMove) {
		return vtui.DropMove
	}
	if ctrl && !shift && allowed.Has(vtui.DropCopy) {
		return vtui.DropCopy
	}
	if suggested != vtui.DropNone && allowed.Has(suggested) {
		return suggested
	}
	if allowed.Has(vtui.DropCopy) {
		return vtui.DropCopy
	}
	if allowed.Has(vtui.DropMove) {
		return vtui.DropMove
	}
	return vtui.DropNone
}

// vfsAcceptsDrop reports whether files can be written into this file system.
func vfsAcceptsDrop(v vfs.VFS) bool {
	if v == nil {
		return false
	}
	if ro, ok := v.(readOnlyVFS); ok && ro.IsReadOnly() {
		return false
	}
	return true
}

// resolveDropTarget maps a screen cell to the panel under it and to the
// directory a drop there would go into: the directory under the cursor if
// the cursor is on one, otherwise the panel's current directory.
func (pf *PanelsFrame) resolveDropTarget(mx, my int) (dropTargetInfo, bool) {
	info := dropTargetInfo{panelIdx: -1, entryIdx: -1}
	if pf == nil || !pf.showPanels {
		return info, false
	}

	for i, p := range pf.panels {
		if pf.wide && i != pf.widePanel {
			continue
		}
		if !pf.wide && i == 0 && !pf.showLeftPanel {
			continue
		}
		if !pf.wide && i == 1 && !pf.showRightPanel {
			continue
		}
		fsp, ok := p.(*FileSystemPanel)
		if !ok || fsp == nil || fsp.vfs == nil {
			continue
		}
		x1, y1, x2, y2 := fsp.GetPosition()
		if mx < x1 || mx > x2 || my < y1 || my > y2 {
			continue
		}

		info.panelIdx, info.panel, info.fs = i, fsp, fsp.vfs
		info.dir = fsp.vfs.GetPath()

		// An info or quick view panel covers the file panel: the file
		// panel is still the logical target, but no row under the
		// cursor belongs to it, so the drop goes to its directory.
		if pf.altPanels[i] != nil {
			return info, true
		}

		if idx := fsp.mouseEntryIndex(mx, my); idx >= 0 && idx < len(fsp.entries) {
			e := fsp.entries[idx]
			if e.IsDir && e.Name != ".." {
				info.dir = fsp.vfs.Join(fsp.vfs.GetPath(), e.Name)
				info.entryIdx = idx
			}
		}
		return info, true
	}
	return info, false
}

// HandleDrag implements vtui.DropTarget. It answers what a drop at this cell
// would do, and on the drop itself starts the transfer.
func (pf *PanelsFrame) HandleDrag(ev *vtui.DragEvent) vtui.DropAction {
	if ev == nil || ev.Phase == vtui.DragLeave {
		return vtui.DropNone
	}
	if !ev.Payload.OffersFiles() {
		return vtui.DropNone
	}
	// During the drag a protocol may only announce the type; by the drop
	// the paths have to be there, or there is nothing to transfer.
	if ev.Phase == vtui.DragDrop && !ev.Payload.HasFiles() {
		return vtui.DropNone
	}
	info, ok := pf.resolveDropTarget(ev.X, ev.Y)
	if !ok || !vfsAcceptsDrop(info.fs) {
		if ev.Phase != vtui.DragOver {
			vtui.DebugLog("DND: nothing at cell %d,%d accepts a drop", ev.X, ev.Y)
		}
		return vtui.DropNone
	}
	action := chooseDropAction(ev.Allowed, ev.Suggested, ev.Modifiers)
	if ev.Phase != vtui.DragOver {
		vtui.DebugLog("DND: a drop at %d,%d would go to %q as %s",
			ev.X, ev.Y, info.dir, action)
	}
	if action == vtui.DropNone || ev.Phase != vtui.DragDrop {
		return action
	}
	pf.dropExternalFiles(info, ev.Payload.Paths, action == vtui.DropMove)
	return action
}

// dropExternalFiles brings files dropped by another application into the
// panel. The panel may be showing an archive or a network connection, so the
// transfer goes through its VFS and the usual progress, overwrite and error
// dialogs - the same road F5 takes. Groups run one after another rather than
// at once, so their dialogs do not fight over the screen.
func (pf *PanelsFrame) dropExternalFiles(info dropTargetInfo, paths []string, isMove bool) {
	if pf == nil || info.fs == nil {
		return
	}
	groups := groupDropSources(paths)
	if len(groups) == 0 {
		return
	}

	dst, dstDir := info.fs, info.dir
	vtui.DebugLog("DND: dropExternalFiles starting %d group(s) into %q (move=%v)", len(groups), dstDir, isMove)

	groups, missing := reachableDropSources(groups)
	if len(missing) > 0 {
		// A queued task that fails leaves nothing on screen but a
		// refreshed panel, which reads as "the drop did nothing": the
		// reason is buried in the queue window. A source we cannot even
		// open is worth saying out loud, before any of it starts.
		reportUnreachableDropSources(missing)
	}
	if len(groups) == 0 {
		return
	}

	var run func(i int)
	run = func(i int) {
		if i >= len(groups) {
			vtui.FrameManager.PostTask(func() {
				pf.RefreshAll()
				vtui.FrameManager.Redraw()
			})
			return
		}
		g := groups[i]
		vtui.DebugLog("DND: dropExternalFiles group %d: srcDir=%q names=%v -> dstDir=%q", i, g.dir, g.names, dstDir)
		src := vfs.NewOSVFS(g.dir)
		go ExecuteFileOp(pf, src, dst, g.names, dstDir, isMove, AppConfig.DefaultFileOpMode, func() {
			run(i + 1)
		})
	}
	run(0)
}

// reachableDropSources drops every name the source file system cannot stat
// and returns what is left, along with the full paths that were refused. The
// point is to fail before the operation starts rather than inside it. A path
// arriving from another application has crossed at least one translation on
// its way here, and the failure worth naming is the one that lands somewhere
// real-looking but wrong: the panel refreshes, the queued task dies quietly,
// and the drop appears to have done nothing at all.
func reachableDropSources(groups []dropSourceGroup) ([]dropSourceGroup, []string) {
	var kept []dropSourceGroup
	var missing []string
	for _, g := range groups {
		src := vfs.NewOSVFS(g.dir)
		var names []string
		for _, n := range g.names {
			full := src.Join(src.GetPath(), n)
			st, err := src.Stat(context.Background(), full)
			if err != nil {
				vtui.DebugLog("DND: dropped source %q cannot be opened: %v", full, err)
				missing = append(missing, full)
				continue
			}
			// Record the answer, not just its absence. The same path is
			// looked up again inside the file operation, on another
			// goroutine, and if the two disagree that is the finding.
			vtui.DebugLog("DND: dropped source %q is reachable (dir=%v size=%d)", full, st.IsDir, st.Size)
			names = append(names, n)
		}
		if len(names) > 0 {
			kept = append(kept, dropSourceGroup{dir: g.dir, names: names})
		}
	}
	return kept, missing
}

// reportUnreachableDropSources names the dropped files that could not be
// reached. One dialog for the lot: a drop of a directory's worth of
// unreadable files must not become a dialog each.
func reportUnreachableDropSources(missing []string) {
	const maxListed = 5
	shown := missing
	if len(shown) > maxListed {
		shown = shown[:maxListed]
	}
	body := "These dropped files could not be read:\n\n" + strings.Join(shown, "\n")
	if len(missing) > len(shown) {
		body += fmt.Sprintf("\n... and %d more", len(missing)-len(shown))
	}
	vtui.FrameManager.PostTask(func() {
		vtui.ShowMessage(" Drag and Drop ", body, []string{"&Ok"})
	})
}

// processDragOutGesture watches the left button for the start of a drag out
// of f4. It returns true only once a drag has actually begun, so the press
// itself and every ordinary drag fall through to the panel untouched.
func (pf *PanelsFrame) processDragOutGesture(e *vtinput.InputEvent, mx, my int) bool {
	if pf == nil || e == nil || e.Type != vtinput.MouseEventType || e.WheelDirection != 0 {
		return false
	}
	if e.ButtonState&vtinput.FromLeft1stButtonPressed == 0 {
		pf.dragOut = dragOutState{}
		return false
	}

	if e.MouseEventFlags&vtinput.MouseMoved == 0 {
		pf.dragOut = dragOutState{}
		if !e.KeyDown {
			return false
		}
		info, ok := pf.resolveDropTarget(mx, my)
		if !ok || info.panel == nil || pf.altPanels[info.panelIdx] != nil {
			return false
		}
		idx := info.panel.mouseEntryIndex(mx, my)
		if idx < 0 || idx >= len(info.panel.entries) {
			return false
		}
		names, cursorOnly, ok := dragOutNames(info.panel, idx)
		if !ok {
			return false
		}
		pf.dragOut = dragOutState{panel: info.panel, names: names, x: mx, y: my, armed: true, cursorOnly: cursorOnly}
		if cursorOnly {
			vtui.DebugLog("DND: drag out armed on the current file at %d,%d", mx, my)
		} else {
			vtui.DebugLog("DND: drag out armed on a marked file at %d,%d", mx, my)
		}
		return false
	}

	if !pf.dragOut.armed || (mx == pf.dragOut.x && my == pf.dragOut.y) {
		return false
	}
	if pf.dragOut.cursorOnly && pf.dragOut.panel.pointerInsideRows(mx, my) {
		return false
	}
	panel, names := pf.dragOut.panel, pf.dragOut.names
	pf.dragOut = dragOutState{}
	vtui.DebugLog("DND: drag out gesture triggered at %d,%d", mx, my)
	return pf.startDragOut(panel, names)
}

// dragOutNames decides what a left press on entry idx would drag out of the
// panel. A marked entry drags every marked file. An unmarked entry drags
// itself, but only while nothing else is marked - with marks present a
// press elsewhere keeps its old meaning of just moving the cursor. The
// parent entry is never dragged. cursorOnly reports the second case.
func dragOutNames(fsp *FileSystemPanel, idx int) (names []string, cursorOnly bool, ok bool) {
	if fsp == nil || idx < 0 || idx >= len(fsp.entries) {
		return nil, false, false
	}
	entry := fsp.entries[idx]
	if entry.Selected {
		return fsp.GetMarkedNames(), false, true
	}
	if entry.Name == ".." || len(fsp.GetMarkedNames()) != 0 {
		return nil, false, false
	}
	return []string{entry.Name}, true, true
}

// pointerInsideRows reports whether a screen cell lies inside the panel's
// file rows, the area a left drag is allowed to keep the cursor in.
func (fp *FileSystemPanel) pointerInsideRows(mx, my int) bool {
	if fp == nil || fp.table == nil {
		return false
	}
	if mx < fp.table.X1 || mx > fp.table.X2 {
		return false
	}
	row := my - (fp.table.Y1 + fp.table.MarginTop)
	return row >= 0 && row < fp.table.ViewHeight
}

// dragOutRefusal names the reason a drag out cannot start, or "" when it
// can. One place rather than three, so the guard and the log line it writes
// can never drift apart.
func dragOutRefusal(fsp *FileSystemPanel, names []string) string {
	if fsp == nil {
		return "no panel under the pointer"
	}
	if !vtui.DragOutSupported() {
		return "the backend offers no drag source"
	}
	if len(names) == 0 {
		return "nothing to drag"
	}
	return ""
}

// startDragOut offers the named files of the panel - the marked ones, or the
// current one when nothing is marked - to the rest of the desktop. Only copy
// is offered from a materialised panel: a move would have f4 delete the
// originals because the receiver said it took them, which is more trust
// than files deserve.
func (pf *PanelsFrame) startDragOut(fsp *FileSystemPanel, names []string) bool {
	if reason := dragOutRefusal(fsp, names); reason != "" {
		vtui.DebugLog("DND: drag out not started: %s", reason)
		return false
	}
	paths, ok := localDragPaths(fsp, names)
	if !ok {
		tempDir, err := os.MkdirTemp("", "f4drag-*")
		if err != nil {
			vtui.DebugLog("DND: failed to create temp dir for drag out: %v", err)
			return false
		}

		src := fsp.vfs
		dst := vfs.NewOSVFS(tempDir)

		ExecuteFileOp(pf, src, dst, names, tempDir, false, 1, func() {
			var dragPaths []string
			for _, name := range names {
				p := filepath.Join(tempDir, name)
				if _, err := os.Stat(p); err == nil {
					dragPaths = append(dragPaths, p)
				}
			}
			if len(dragPaths) == 0 {
				os.RemoveAll(tempDir)
				return
			}

			payload := vtui.DragPayload{Kinds: []string{"text/uri-list"}, Paths: dragPaths}
			go func() {
				action, err := vtui.StartDrag(payload, vtui.DropCopy)
				if err != nil {
					vtui.DebugLog("DND: drag out failed: %v", err)
				} else {
					vtui.DebugLog("DND: drag out finished as %s", action)
				}
				os.RemoveAll(tempDir)
			}()
		})
		return true
	}

	payload := vtui.DragPayload{Kinds: []string{"text/uri-list"}, Paths: paths}
	go func() {
		action, err := vtui.StartDrag(payload, vtui.DropCopy|vtui.DropMove|vtui.DropLink)
		if err != nil {
			vtui.DebugLog("DND: drag out failed: %v", err)
			return
		}
		vtui.DebugLog("DND: drag out finished as %s", action)
	}()
	return true
}

// localDragPaths turns marked names into paths another application can open.
// It reports false for anything but a local file system: a file inside an
// archive or on a remote host has no such path until it is materialised into
// a temporary directory, which is a copy nobody asked for and needs its own
// progress and cleanup - see DRAGDROP.md.
func localDragPaths(fsp *FileSystemPanel, names []string) ([]string, bool) {
	local, ok := fsp.vfs.(*vfs.OSVFS)
	if !ok {
		return nil, false
	}
	paths := make([]string, 0, len(names))
	for _, n := range names {
		p := local.Join(local.GetPath(), n)
		if hostmode.Posix() && runtime.GOOS == "windows" {
			// CF_HDROP carries DOS paths, so a posix path has to be
			// translated before anything else can open it. Wine does the
			// translation properly; the Z: rule below only guesses, and
			// guesses wrong in any prefix whose Z: is not the root -- an
			// unopenable path is why a target refuses the drop.
			if dos, ok := hostDosPath(p); ok && dos != "" {
				p = dos
			} else if strings.HasPrefix(p, "/") {
				p = `Z:` + strings.ReplaceAll(p, "/", `\`)
			}
		}
		paths = append(paths, p)
	}
	return paths, true
}

// installPanelDropTarget makes the panels the drop target of whatever
// graphical backend is running. In a terminal no backend registers a drag
// and drop protocol, so the target is simply never asked anything.
func installPanelDropTarget(pf *PanelsFrame) {
	if pf == nil {
		return
	}
	vtui.SetDropTarget(pf)
}
