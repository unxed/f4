package panel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// treeExpandCache remembers, for the lifetime of this f4 process only, every
// directory path any tree panel (Ctrl+T) has ever expanded, across separate
// TreePanel instances: TreePanel is rebuilt from scratch on every Ctrl+T
// (see NewTreePanel), so without this the whole expand/collapse state a user
// built up while browsing was thrown away the moment they closed the tree
// (Enter, or Ctrl+T again) and started over fully collapsed at the next
// open. Keyed by absolute path -- not by root or by which TreePanel expanded
// it -- since a path names the same directory on disk regardless of which
// (volume- or cwd-rooted, see TreeRootWholeVolume) tree happened to reach
// it.
//
// It is the working copy; tree_persist.go mirrors it to a file so that it
// also survives a restart of f4 (the owner's answer on f4#1602: as far2l
// does), once the application has named that file.
var (
	treeExpandCacheMu sync.Mutex
	treeExpandCache   = map[string]struct{}{}
)

// rememberTreeExpanded records that path's children are now visible in some
// tree panel. Called only by expandAt itself, never by its callers, so
// every way a node can become expanded -- Right arrow, revealPath's
// auto-expand of the chain down to the source panel's directory,
// RefreshChildrenAndSelect, RefreshAfterDelete, Rescan -- is covered by this
// one call site.
func rememberTreeExpanded(path string) {
	treeExpandCacheMu.Lock()
	treeExpandCache[path] = struct{}{}
	treeExpandCacheMu.Unlock()
	treeExpandChanged()
}

// forgetTreeExpanded is rememberTreeExpanded's inverse, called by collapseAt
// for the node it collapses and for every descendant row collapsing removes
// along with it -- otherwise a later re-expand of an ancestor the user
// explicitly collapsed would silently resurrect a deeper branch the cache
// still remembered from before, contradicting the user's own last action on
// it.
func forgetTreeExpanded(path string) {
	treeExpandCacheMu.Lock()
	delete(treeExpandCache, path)
	treeExpandCacheMu.Unlock()
	treeExpandChanged()
}

// treeExpandedSnapshot returns every path treeExpandCache currently holds,
// for NewTreePanel to replay (via applyExpandedPaths) into a freshly built
// tree. A snapshot rather than a live iterator: the replay mutates the
// cache right back (through expandAt's own rememberTreeExpanded calls),
// which would race with ranging over the same map directly.
func treeExpandedSnapshot() []string {
	treeLoadPersisted()
	treeExpandCacheMu.Lock()
	defer treeExpandCacheMu.Unlock()
	paths := make([]string, 0, len(treeExpandCache))
	for p := range treeExpandCache {
		paths = append(paths, p)
	}
	return paths
}

// treeChildScanCap bounds how many directory entries a single expand reads
// before giving up on that one directory -- a defensive limit against a
// pathological single directory (e.g. a maildir with tens of thousands of
// entries), not a feature. It does not bound the tree as a whole: expansion
// is lazy (treeItem.collapsed), so the overall tree size is however much
// the user has actually expanded, never the whole disk at once.
const treeChildScanCap = 20000

// treeItem is one row of the flat tree list. Modeled directly on far2l's
// own tree panel (far2l/src/panels/treelist.cpp, treelist.hpp -- see
// f4#1602's tracking comment): the tree is a flat, index-addressed list,
// not a nested structure, so expanding or collapsing a node only ever
// splices a contiguous run of its descendants in or out of this same list.
// Nothing here ever walks the whole subtree on a keypress or a render --
// only expanding a node reads that one directory's immediate children.
type treeItem struct {
	name string
	path string
	// depth is the item's nesting level; the root is 0.
	depth int
	// parentIndex is the row index of this item's parent in the flat list,
	// -1 for the root. Used to find a node's contiguous descendant run
	// (collapseAt) and to fix up indices after a splice (spliceChildren).
	parentIndex int
	// vfsItem carries directory/link attributes used by highlight.ini. The tree
	// keeps links to directories as leaf rows so following one cannot create a
	// cycle in the volume tree.
	vfsItem vfs.VFSItem
	// expandable is true while this directory might still have
	// subdirectories. It starts optimistic (every directory is assumed
	// expandable) and is corrected to false the first time expandAt
	// actually reads an empty result -- avoiding a second stat/readdir just
	// to answer "does it have children" up front.
	expandable bool
	// collapsed is true while expandable's children are not currently
	// spliced into the list. Meaningless when expandable is false.
	collapsed bool
	// last records, for each ancestor level from the root down to (but not
	// including) this item, whether that ancestor was the last child among
	// its own siblings -- len(last) == depth. last[depth-1] (the item's own
	// slot) says whether THIS item is the last child, deciding its own
	// connector glyph (└─ vs ├─); last[0..depth-2] says whether each
	// ancestor's vertical guide line continues past this row or stops
	// (blank), the standard "tree" / far2l style of connector rendering.
	last []bool
}

// GetCellText implements vtui.TableRow, rendering the ASCII tree connectors
// from depth+last, an expand-state marker, and the name -- see treeItem's
// own doc comment for what each piece of state means.
func (it treeItem) GetCellText(int) string {
	var b strings.Builder
	for i := 0; i < it.depth-1; i++ {
		if it.last[i] {
			b.WriteString("  ")
		} else {
			b.WriteString("\u2502 ") // │
		}
	}
	if it.depth > 0 {
		if it.last[it.depth-1] {
			b.WriteString("\u2514\u2500") // └─
		} else {
			b.WriteString("\u251c\u2500") // ├─
		}
	}
	switch {
	case it.expandable && it.collapsed:
		b.WriteString("+ ")
	case it.expandable && !it.collapsed:
		b.WriteString("- ")
	default:
		b.WriteString("  ")
	}
	b.WriteString(it.name)
	return b.String()
}

// GetCellAttr implements vtui.CellColorableRow: a tree row is a directory, so
// it takes the color the file panels give a directory by the highlight rules
// (highlight.ini, Settings Center), cursor and inactive-cursor states included
// (f4#1602, part 13). The connectors and the +/- marker share the row's color.
func (it treeItem) GetCellAttr(_ int, defaultAttr uint64) uint64 {
	if theme.GlobalFileHighlighter == nil {
		return defaultAttr
	}
	isCursor := defaultAttr == vtui.Palette[theme.ColPanelCursor] ||
		defaultAttr == vtui.Palette[theme.ColPanelSelectedCursor] ||
		defaultAttr == vtui.Palette[theme.ColPanelInactiveCursor] ||
		defaultAttr == vtui.Palette[theme.ColPanelInactiveSelectedCursor]
	item := it.vfsItem
	item.Name = it.name
	item.IsDir = true
	item.IsHidden = item.IsHidden || (runtime.GOOS != "windows" && strings.HasPrefix(it.name, "."))
	return theme.GlobalFileHighlighter.GetColor(&item, defaultAttr, false, isCursor)
}

// TreePanel is far2l/DOS Navigator's directory tree (far2l: Alt+F10; DOS
// Navigator has an equivalent tree view), bound to Ctrl+T -- see AltPanel's
// own doc comment, which already named "tree" alongside "info"/"quick_view"
// as a planned Kind. First atomic part of the DOS Navigator ideas ticket
// (f4#1602). The tree is rooted at the source panel's current volume by
// default (its filesystem root on POSIX, the drive root on Windows) --
// matching far2l's own "tree of the current disk" scope -- or, when the
// TreeRootWholeVolume setting (f4:config, part 7 of f4#1602) is off, at the
// source panel's own current directory instead; either way it is lazily
// expanded: only the root and the chain of directories down to the source
// panel's current directory are read up front (revealPath), everything
// else is read the first time the user expands it (Right arrow / expandAt).
//
// Enter on a row changes Source()'s directory to that row's path and closes
// the tree, the same "pick a target, then get out of the way" round trip
// bookmarks_dialog.go's Enter handling already does via NavigateToBookmark.
// Right/Left expand/collapse the row under the cursor without navigating.
//
// The tree is also a real destination panel, not just a navigator: when it
// has focus, F5/F6 (actionCopyMove, internal/app/actions.go) copy/move the
// selection from Source() -- the other, still-visible panel that opened the
// tree -- into SelectedPath(), the highlighted node, without navigating
// into it first (part 3 of f4#1602), F7 (actionMkDir) creates a new
// subdirectory right under the highlighted node the same way, refreshing it
// via RefreshChildrenAndSelect (part 4 of f4#1602), and F8/Del
// (actionDeleteWithDisposition) delete the highlighted node itself,
// reusing the very same confirmation dialog as an ordinary panel delete,
// then leave the cursor on a neighboring row via RefreshAfterDelete (part 5
// of f4#1602). The tree's own root row refuses F8/Del (IsRootSelected):
// deleting it would mean deleting the whole current volume, which is not a
// node a real panel could ever have offered as a delete target either.
// Ctrl+R (Panel.Rescan, internal/app/actions_table.go) re-reads every
// directory the tree currently has expanded from disk while it has focus,
// via Rescan (part 6 of f4#1602) -- the same far2l Ctrl+R the owner named
// alongside F5/F6/F7/F8/Del as expected tree behavior, and otherwise the
// one way an already-expanded node's listing could go stale for as long as
// the tree stays open. Which branches are expanded also survives closing
// and reopening the tree (Ctrl+T) within the same run of f4 -- but not a
// restart of the f4 process itself -- via treeExpandCache (part 8 of
// f4#1602); see that cache's own doc comment for why in-memory, not
// persisted to settings.ini/f4:config, is the scope this part chose.
type TreePanel struct {
	src     *FileSystemPanel
	Frame   *vtui.BorderedFrame
	Table   *vtui.Table
	Focused bool

	root  string
	items []treeItem
}

// NewTreePanel builds the tree for src's current volume, pre-expanded down
// to src's current directory. Called by PanelsFrame.ToggleAltPanel
// (Ctrl+T), which also takes care of placement, sizing and redraw --
// SetPosition below only needs to be internally consistent, not final.
func NewTreePanel(src *FileSystemPanel) *TreePanel {
	x1, y1, x2, y2 := src.GetPosition()
	t := &TreePanel{src: src, root: treeRoot(src)}

	t.Frame = vtui.NewBorderedFrame(x1, y1, x2, y2, vtui.SingleBox, i18n.Msg("TreePanel.Title"))
	t.Frame.ColorBoxIdx = theme.ColPanelBox
	t.Frame.ColorTitleIdx = theme.ColPanelTitle

	t.Table = vtui.NewTable(0, 0, 1, 1, []vtui.TableColumn{{Title: i18n.Msg("TreePanel.ColumnName")}})
	t.Table.ShowHeader = false
	t.Table.QuickSearch = true
	t.Table.ColorTextIdx = theme.ColPanelText
	t.Table.ColorItemSelectTextIdx = theme.ColPanelSelectedText
	t.Table.ColorBoxIdx = theme.ColPanelBox

	t.items = []treeItem{{name: t.root, path: t.root, depth: 0, parentIndex: -1, expandable: true, collapsed: true}}
	t.expandAt(0)
	t.revealPath(treeRootFor(src))
	// Resume whatever branches an earlier tree panel (this same run of f4,
	// possibly a different TreePanel instance -- see treeExpandCache's own
	// doc comment) left expanded, in addition to the cwd chain revealPath
	// just walked above. Applied after revealPath, not before, so this
	// never disturbs the cursor revealPath already placed on src's current
	// directory.
	t.applyExpandedPaths(treeExpandedSnapshot())
	t.syncRows()

	t.SetFocus(false)
	t.SetPosition(x1, y1, x2, y2)
	return t
}

func treeRootFor(src *FileSystemPanel) string {
	if src == nil || src.Vfs == nil {
		return ""
	}
	return src.Vfs.GetPath()
}

// treeRoot is the tree's own root row's path: src's whole current volume by
// default (far2l's own scope), or just src's current directory when the
// TreeRootWholeVolume setting (#1602 follow-up) is off.
func treeRoot(src *FileSystemPanel) string {
	path := treeRootFor(src)
	if config.App.TreeRootWholeVolume {
		return treeVolumeRoot(path)
	}
	return path
}

// treeVolumeRoot returns path's volume root: "/" on POSIX (VolumeName is
// always "" there), the drive root ("C:\") on Windows.
func treeVolumeRoot(path string) string {
	if vol := filepath.VolumeName(path); vol != "" {
		return vol + string(filepath.Separator)
	}
	return string(filepath.Separator)
}

// scanChildDirs lists immediate directories and links to directories, sorted
// case-insensitively. OSVFS supplies the same IsDir/IsSymlink/ReparseTag
// metadata as file panels; links are shown but never expanded, avoiding
// cycles through symlinks or junctions.
func scanChildDirs(dir string) []vfs.VFSItem {
	var entries []vfs.VFSItem
	if err := vfs.NewOSVFS(dir).ReadDir(context.Background(), dir, func(batch []vfs.VFSItem) {
		entries = append(entries, batch...)
	}); err != nil {
		return nil
	}
	directories := make([]vfs.VFSItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir {
			directories = append(directories, entry)
		}
		if len(directories) >= treeChildScanCap {
			break
		}
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i].Name) < strings.ToLower(directories[j].Name)
	})
	return directories
}

// expandAt reads items[at]'s immediate subdirectories (if any) and splices
// them into the flat list right after it, correcting parentIndex on every
// item that used to sit after the splice point. A no-op if the item is
// already expanded or not expandable.
func (t *TreePanel) expandAt(at int) {
	if at < 0 || at >= len(t.items) {
		return
	}
	item := &t.items[at]
	if !item.expandable || !item.collapsed {
		return
	}
	entries := scanChildDirs(item.path)
	if len(entries) == 0 {
		item.expandable = false
		return
	}

	children := make([]treeItem, len(entries))
	for i, entry := range entries {
		link := vfs.LinkKindOf(&entry) != vfs.LinkNone
		last := append(append([]bool{}, item.last...), i == len(entries)-1)
		children[i] = treeItem{
			name:        entry.Name,
			path:        filepath.Join(item.path, entry.Name),
			depth:       item.depth + 1,
			parentIndex: at,
			vfsItem:     entry,
			expandable:  !link,
			collapsed:   !link,
			last:        last,
		}
	}
	t.spliceChildren(at, children)
	t.items[at].collapsed = false
	rememberTreeExpanded(item.path)
}

// spliceChildren inserts children right after items[at], shifting
// parentIndex on every existing item that pointed past at (i.e. whose
// parent -- or an ancestor's position -- moved).
func (t *TreePanel) spliceChildren(at int, children []treeItem) {
	n := len(children)
	for i := range t.items {
		if t.items[i].parentIndex > at {
			t.items[i].parentIndex += n
		}
	}
	tail := append([]treeItem{}, t.items[at+1:]...)
	merged := append([]treeItem{}, t.items[:at+1]...)
	merged = append(merged, children...)
	merged = append(merged, tail...)
	t.items = merged
}

// collapseAt removes items[at]'s descendant run (the contiguous items right
// after it whose depth is greater) from the flat list, fixing up
// parentIndex on everything that pointed past the removed range. A no-op if
// the item has no children currently spliced in.
func (t *TreePanel) collapseAt(at int) {
	if at < 0 || at >= len(t.items) || t.items[at].collapsed {
		return
	}
	depth := t.items[at].depth
	end := at + 1
	for end < len(t.items) && t.items[end].depth > depth {
		end++
	}
	removed := end - (at + 1)
	if removed > 0 {
		for i := at + 1; i < end; i++ {
			forgetTreeExpanded(t.items[i].path)
		}
		t.items = append(t.items[:at+1], t.items[end:]...)
		for i := range t.items {
			if t.items[i].parentIndex >= end {
				t.items[i].parentIndex -= removed
			}
		}
	}
	t.items[at].collapsed = true
	forgetTreeExpanded(t.items[at].path)
}

// revealPath expands the chain of directories from the root down to
// target (normally src's current directory), one level at a time, and
// leaves the cursor on target's own row. It stops silently at whatever
// level it can no longer match or expand -- e.g. target outside root's
// volume, or a permission error on a directory the source panel itself
// somehow still reached -- rather than fail the whole tree open.
//
// Matching each chain component against the already-expanded children
// (findChild) is name-based, which a short (8.3-style) Windows path component
// (e.g. "RUNNER~1" for "runneradmin", which GitHub's Windows runners set
// %TEMP% to) can defeat even though target names a real directory. Symlink
// directories now appear as leaf rows, so the path chain can still be
// revealed through one without making ordinary expansion follow it.
func (t *TreePanel) revealPath(target string) {
	if idx := t.resolvePath(target); idx != -1 {
		t.setCursor(idx)
	}
}

// resolvePath is revealPath's underlying walk, split out so Rescan (below)
// can both re-expand a previously-expanded directory and probe whether a
// path still exists without always moving the cursor there. Returns the
// row index target now lives at (expanding whatever chain of directories
// it needs to along the way), or -1 if it stops silently at whatever level
// it can no longer match or expand -- see revealPath's own doc comment for
// why that can happen even for a real, existing directory.
func (t *TreePanel) resolvePath(target string) int {
	root := strings.TrimRight(t.root, string(filepath.Separator))
	target = strings.TrimRight(filepath.Clean(target), string(filepath.Separator))
	rel := strings.TrimPrefix(target, root)
	rel = strings.Trim(rel, string(filepath.Separator))
	if rel == "" {
		return 0
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := 0
	for _, part := range parts {
		t.expandAt(current)
		next := t.findChild(current, part)
		if next == -1 {
			next = t.revealChild(current, part)
		}
		if next == -1 {
			return -1
		}
		current = next
	}
	return current
}

// findChild returns the row index of items[parent]'s already-listed child
// named name, or -1 if there is none. Matching is case-insensitive on
// Windows, where the very listing being searched (scanChildDirs's) already
// came from a case-preserving but case-insensitive filesystem, so two
// spellings of the same name are the same child.
func (t *TreePanel) findChild(parent int, name string) int {
	for i := parent + 1; i < len(t.items) && t.items[i].parentIndex >= parent; i++ {
		if t.items[i].parentIndex == parent && pathNamesEqual(t.items[i].name, name) {
			return i
		}
	}
	return -1
}

// pathNamesEqual compares one path component of a revealPath chain against
// a candidate child name. Case-insensitive on Windows (see findChild);
// exact everywhere else, where case is part of a file's identity.
func pathNamesEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// revealChild splices in a single node for a real subdirectory under
// items[parent] when its spelling does not match a listed child (notably an
// 8.3-style Windows path). This bounded lookup is anchored to the target path
// revealPath is unwinding, rather than a general directory walk. Returns -1
// if name is not actually a subdirectory there.
func (t *TreePanel) revealChild(parent int, name string) int {
	path := filepath.Join(t.items[parent].path, name)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return -1
	}

	depth := t.items[parent].depth
	end := parent + 1
	for end < len(t.items) && t.items[end].depth > depth {
		end++
	}
	lastSibling := -1
	for i := parent + 1; i < end; i++ {
		if t.items[i].parentIndex == parent {
			lastSibling = i
		}
	}
	if lastSibling != -1 {
		t.items[lastSibling].last[t.items[lastSibling].depth-1] = false
	}

	child := treeItem{
		name:        name,
		path:        path,
		depth:       depth + 1,
		parentIndex: parent,
		expandable:  true,
		collapsed:   true,
		last:        append(append([]bool{}, t.items[parent].last...), true),
	}
	t.spliceChildren(end-1, []treeItem{child})
	t.items[parent].expandable = true
	t.items[parent].collapsed = false
	return end
}

func (t *TreePanel) setCursor(idx int) {
	if idx < 0 || idx >= len(t.items) {
		return
	}
	t.Table.SelectPos = idx
	t.Table.EnsureVisible()
}

func (t *TreePanel) syncRows() {
	rows := make([]vtui.TableRow, len(t.items))
	for i, it := range t.items {
		rows[i] = it
	}
	t.Table.SetRows(rows)
}

func (t *TreePanel) cursorIndex() int {
	idx := t.Table.RowAt(t.Table.SelectPos)
	if idx < 0 || idx >= len(t.items) {
		return -1
	}
	return idx
}

func (t *TreePanel) Source() *FileSystemPanel { return t.src }
func (t *TreePanel) Kind() string             { return "tree" }

func (t *TreePanel) SetPosition(x1, y1, x2, y2 int) {
	t.Frame.SetPosition(x1, y1, x2, y2)
	b := t.Frame.GetBorderThickness()
	ix1, iy1, ix2, iy2 := x1+b, y1+b, x2-b, y2-b
	if ix2 < ix1 {
		ix2 = ix1
	}
	if iy2 < iy1 {
		iy2 = iy1
	}
	t.Table.SetPosition(ix1, iy1, ix2, iy2)
}

func (t *TreePanel) GetPosition() (int, int, int, int) { return t.Frame.GetPosition() }

// SetFocus flips the visible focus marker -- the frame title recolours the
// same way InfoPanel/QuickViewPanel's do, and the cursor row switches
// between the active/inactive cursor colors the same way
// FileSystemPanel.SetFocus (list.go) and ProcList's own panel do for their
// own vtui.Table.
func (t *TreePanel) SetFocus(f bool) {
	t.Focused = f
	if f {
		t.Frame.ColorTitleIdx = theme.ColPanelSelectedTitle
		t.Table.ColorSelectedTextIdx = theme.ColPanelCursor
		t.Table.ColorItemSelectCursorIdx = theme.ColPanelSelectedCursor
	} else {
		t.Frame.ColorTitleIdx = theme.ColPanelTitle
		t.Table.ColorSelectedTextIdx = theme.ColPanelInactiveCursor
		t.Table.ColorItemSelectCursorIdx = theme.ColPanelInactiveSelectedCursor
	}
	t.Table.SetFocus(f)
}

func (t *TreePanel) IsFocused() bool { return t.Focused }

func (t *TreePanel) ProcessKey(e *vtinput.InputEvent) bool {
	before := t.SelectedPath()
	handled := t.processKey(e)
	t.followCursor(before)
	return handled
}

func (t *TreePanel) processKey(e *vtinput.InputEvent) bool {
	if e != nil && e.KeyDown {
		switch e.VirtualKeyCode {
		case vtinput.VK_RETURN:
			t.activateSelected()
			return true
		case vtinput.VK_RIGHT:
			idx := t.cursorIndex()
			if idx >= 0 && t.items[idx].expandable && t.items[idx].collapsed {
				t.expandAt(idx)
				t.syncRows()
				t.setCursor(idx)
			}
			return true
		case vtinput.VK_LEFT:
			idx := t.cursorIndex()
			if idx >= 0 && !t.items[idx].collapsed {
				t.collapseAt(idx)
				t.syncRows()
				t.setCursor(idx)
			} else if idx > 0 {
				t.setCursor(t.items[idx].parentIndex)
			}
			return true
		}
	}
	return t.Table.ProcessKey(e)
}

func (t *TreePanel) ProcessMouse(e *vtinput.InputEvent) bool {
	before := t.SelectedPath()
	handled := t.Table.ProcessMouse(e)
	if t.toggleOnMouse(e) {
		handled = true
	}
	t.followCursor(before)
	return handled
}

// toggleOnMouse expands or collapses the folder under the mouse, as Right
// and Left do (f4#1602: the tree could only be opened with the keys): a left
// click on the "+"/"-" marker of a row, or a double click on the rest of it.
// A double click on the marker is the second toggle of the same gesture --
// the first press has already done it -- so it is left alone. Table has
// already moved the cursor onto the clicked row.
func (t *TreePanel) toggleOnMouse(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType || e.ButtonState != vtinput.FromLeft1stButtonPressed || !vtui.IsMousePress(e) {
		return false
	}
	if !t.Table.HitTest(int(e.MouseX), int(e.MouseY)) || t.Table.GetClickIndex(int(e.MouseY)) < 0 {
		return false
	}
	idx := t.cursorIndex()
	if idx < 0 || !t.items[idx].expandable {
		return false
	}
	markerX := t.Table.X1 + t.items[idx].depth*2
	onMarker := int(e.MouseX) >= markerX && int(e.MouseX) <= markerX+1
	double := e.MouseEventFlags&vtinput.DoubleClick != 0
	if onMarker == double {
		return false
	}
	if t.items[idx].collapsed {
		t.expandAt(idx)
	} else {
		t.collapseAt(idx)
	}
	t.syncRows()
	t.setCursor(idx)
	return true
}

// followCursor shows the folder under the cursor in the panel that opened
// the tree when the tree works like Explorer's (TreeExplorerMode, f4#1602):
// the tree on one side, the files of the folder chosen in it on the other.
// A folder the panel cannot list leaves the panel where it was, without a
// message: the cursor only passes over it.
func (t *TreePanel) followCursor(before string) {
	if !config.App.TreeExplorerMode || t.src == nil {
		return
	}
	path := t.SelectedPath()
	if path == "" || path == before {
		return
	}
	if pf := FindPanelsFrameAnyScreen(); pf != nil {
		var refused error
		pf.navigateToPath(t.src, path, &refused)
	}
}

func (t *TreePanel) GetSelectedName() string {
	idx := t.cursorIndex()
	if idx < 0 {
		return ""
	}
	return t.items[idx].name
}

// SelectedPath returns the absolute filesystem path of the row under the
// cursor, or "" if the cursor is out of range. This is the tree acting as a
// full panel in its own right rather than a pure navigator (see far2l's
// treelist.cpp and f4#1602's tracking comment): F5/F6 (actionCopyMove) use
// it as the copy/move destination when the tree has focus, taking the
// selection itself from Source(), the other, still-visible panel that
// opened the tree.
func (t *TreePanel) SelectedPath() string {
	idx := t.cursorIndex()
	if idx < 0 {
		return ""
	}
	return t.items[idx].path
}

// RefreshChildrenAndSelect re-scans the disk children of the row under the
// cursor and leaves the cursor on the child named name if one is found
// there, or on the node itself otherwise (e.g. name no longer exists). Used
// by F7 (actionMkDir, internal/app/actions.go) after it creates a new
// subdirectory directly under the tree's highlighted node while the tree has
// focus (f4#1602, part 4 of N): the node is force-expanded -- even if it was
// collapsed before, since a moment ago it may have had no children at all --
// so the newly created one becomes visible. A no-op if the cursor is out of
// range.
func (t *TreePanel) RefreshChildrenAndSelect(name string) {
	idx := t.cursorIndex()
	if idx < 0 {
		return
	}
	if !t.items[idx].collapsed {
		t.collapseAt(idx)
	}
	t.items[idx].expandable = true
	t.items[idx].collapsed = true
	t.expandAt(idx)
	t.syncRows()
	if child := t.findChild(idx, name); child != -1 {
		t.setCursor(child)
		return
	}
	t.setCursor(idx)
}

// IsRootSelected reports whether the cursor is on the tree's own root row --
// the whole current volume, rather than one of its subdirectories. F8/Del
// (actionDeleteWithDisposition, internal/app/actions.go) checks this before
// acting on the highlighted node: the root has no parent row to refresh
// afterward, and "deleting" it would mean deleting the whole volume the
// source panel is browsing, never a sensible interpretation of Del on a
// tree node (f4#1602, part 5 of N). The root is always row 0: nothing is
// ever spliced in ahead of it (spliceChildren only ever inserts after an
// existing row).
func (t *TreePanel) IsRootSelected() bool {
	return t.cursorIndex() == 0
}

// siblingIndices returns the flat-list row indices of items[parent]'s own
// children -- items[i].parentIndex == parent -- in the same top-to-bottom
// order the tree lists them, skipping over any already-expanded
// descendants of an earlier sibling that sit at a deeper level in between
// two siblings in the flat list. Mirrors the range scan revealChild already
// does to find the last sibling before splicing in a new one.
func (t *TreePanel) siblingIndices(parent int) []int {
	depth := t.items[parent].depth
	end := parent + 1
	for end < len(t.items) && t.items[end].depth > depth {
		end++
	}
	var out []int
	for i := parent + 1; i < end; i++ {
		if t.items[i].parentIndex == parent {
			out = append(out, i)
		}
	}
	return out
}

// RefreshAfterDelete re-scans the parent of the row under the cursor after
// its underlying directory has just been removed from disk, and leaves the
// cursor on a neighboring row rather than on the now-nonexistent path: the
// sibling that used to follow the deleted node, or, failing that (the
// deleted node was the last child), the one that used to precede it, or the
// parent itself if the deleted node had no siblings at all. Used by F8/Del
// (actionDeleteWithDisposition, internal/app/actions.go) while the tree has
// focus (f4#1602, part 5 of N) -- the mirror image of RefreshChildrenAndSelect
// (part 4 of N), which refreshes the highlighted node's own children after
// adding one beneath it; here the deleted node itself is gone, so it is the
// *parent* that needs re-scanning. A no-op if the cursor is out of range or
// already on the root (see IsRootSelected; there is no parent to refresh).
func (t *TreePanel) RefreshAfterDelete() {
	idx := t.cursorIndex()
	if idx < 0 || idx == 0 {
		return
	}
	parent := t.items[idx].parentIndex
	if parent < 0 {
		return
	}

	var next, prev string
	siblings := t.siblingIndices(parent)
	for i, s := range siblings {
		if s != idx {
			continue
		}
		if i+1 < len(siblings) {
			next = t.items[siblings[i+1]].name
		}
		if i-1 >= 0 {
			prev = t.items[siblings[i-1]].name
		}
		break
	}

	if !t.items[parent].collapsed {
		t.collapseAt(parent)
	}
	t.items[parent].expandable = true
	t.items[parent].collapsed = true
	t.expandAt(parent)
	t.syncRows()

	if next != "" {
		if child := t.findChild(parent, next); child != -1 {
			t.setCursor(child)
			return
		}
	}
	if prev != "" {
		if child := t.findChild(parent, prev); child != -1 {
			t.setCursor(child)
			return
		}
	}
	t.setCursor(parent)
}

// applyExpandedPaths re-expands every directory named in paths -- via
// resolvePath, which also expands every ancestor it walks through on the
// way to each one, then expandAt(idx) for the path itself, the same two-step
// resolvePath's own doc comment already spells out. Used both by Rescan
// (paths collected from the tree's own current, in-session items) and by
// NewTreePanel (paths from treeExpandCache, the process-lifetime cache, so a
// freshly opened tree resumes wherever the previous one left off). A path
// that no longer resolves -- its directory renamed, removed, or simply
// outside this tree's own root -- is silently skipped, exactly like
// resolvePath's other callers; order does not matter, since expanding an
// already-expanded node (or one whose ancestor got expanded by an earlier
// entry in paths) is a no-op.
func (t *TreePanel) applyExpandedPaths(paths []string) {
	for _, p := range paths {
		if idx := t.resolvePath(p); idx != -1 {
			t.expandAt(idx)
		}
	}
}

// Rescan re-reads every directory the tree currently has expanded, from
// the root back down, dropping subdirectories that no longer exist on disk
// and picking up ones that appeared since -- far2l's own Ctrl+R on its tree
// panel (far2l/src/panels/treelist.cpp), which the owner named explicitly
// as expected tree behavior alongside F5/F6/F7/F8/Del (f4#1602, part 6 of
// N). It matters because expandAt only ever reads a directory's children
// once, the first time it is expanded (see treeChildScanCap's doc comment
// on the tree's own laziness): left alone, an already-expanded node's
// listing goes stale for as long as the tree stays open, however long that
// is, while the other panel or an outside process adds, removes or renames
// directories under it. Rescan(), unlike the constructor, never touches a
// node the user never expanded in the first place -- a collapsed node's
// subtree is simply not there to go stale.
//
// The cursor stays on its current row's path if that directory still
// exists, or moves up to the closest ancestor that does otherwise (e.g. the
// highlighted node itself was just removed from outside the tree) -- never
// simply back to the root, which would needlessly throw away how deep the
// user had navigated for an unrelated change two levels up.
func (t *TreePanel) Rescan() {
	idx := t.cursorIndex()
	var selectedPath string
	if idx >= 0 {
		selectedPath = t.items[idx].path
	}

	var expandedPaths []string
	for _, it := range t.items {
		if it.expandable && !it.collapsed && it.path != t.root {
			expandedPaths = append(expandedPaths, it.path)
		}
	}

	t.items = []treeItem{{name: t.root, path: t.root, depth: 0, parentIndex: -1, expandable: true, collapsed: true}}
	t.expandAt(0)
	t.applyExpandedPaths(expandedPaths)

	pos := -1
	for target := selectedPath; target != ""; {
		if pos = t.resolvePath(target); pos != -1 {
			break
		}
		parent := filepath.Dir(target)
		if parent == target {
			break
		}
		target = parent
	}
	if pos == -1 {
		pos = 0
	}
	t.syncRows()
	t.setCursor(pos)
}

// activateSelected changes the source panel's directory to the highlighted
// row and closes the tree. pf.ToggleAltPanel("tree", nil) is safe here: it
// only ever calls its factory argument on the branch that opens a new tree,
// never on the branch that closes an existing one by Kind, which is the
// only branch a tree panel that is already open and handling its own Enter
// key can ever reach.
func (t *TreePanel) activateSelected() {
	idx := t.cursorIndex()
	if idx < 0 || t.src == nil {
		return
	}
	path := t.items[idx].path
	pf := FindPanelsFrameAnyScreen()
	if pf == nil {
		return
	}
	pf.NavigateToPath(t.src, path)
	if config.App.TreeExplorerMode {
		// Like Explorer's: Enter goes over to the files of the folder and
		// the tree stays open beside them.
		for i, p := range pf.Panels {
			if p == Panel(t.src) {
				pf.ActiveIdx = i
			}
		}
		return
	}
	pf.ToggleAltPanel("tree", nil)
}

func (t *TreePanel) Show(scr *vtui.ScreenBuf) {
	t.Frame.Show(scr)
	t.Table.Show(scr)
}

// Close is a no-op: NewTreePanel builds the tree synchronously and starts no
// background work, unlike QuickViewPanel's scan goroutine or ProcList's
// refresh ticker.
func (t *TreePanel) Close() {}
