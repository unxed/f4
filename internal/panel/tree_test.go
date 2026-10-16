package panel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// newBareTreePanel builds a TreePanel rooted directly at root (bypassing
// NewTreePanel's volume-root + revealPath logic), for deterministic,
// filesystem-root-independent tests of the flat-list splice mechanics
// (expandAt/collapseAt/spliceChildren).
func newBareTreePanel(root string) *TreePanel {
	t := &TreePanel{
		root:  root,
		items: []treeItem{{name: root, path: root, depth: 0, parentIndex: -1, expandable: true, collapsed: true}},
	}
	t.Table = vtui.NewTable(0, 0, 1, 1, []vtui.TableColumn{{Title: "Name"}})
	return t
}

// resetTreeExpandCache clears the process-lifetime tree expand cache
// (treeExpandCache, f4#1602 part 8 of N) before a test and again on
// cleanup, so a prior test's cache entries can never leak into this one and
// this one's can never leak into a later one. t.TempDir() already gives
// every test its own directory no other test can name, so key collisions
// are not the actual concern -- a clean, deterministic starting state is.
func resetTreeExpandCache(t *testing.T) {
	t.Helper()
	clear := func() {
		treeExpandCacheMu.Lock()
		treeExpandCache = map[string]struct{}{}
		treeExpandCacheMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// TestTreeExpandCollapse_SpliceLogic drives expandAt/collapseAt directly
// against a small real directory tree and checks the flat list's shape
// (depth, parentIndex, collapsed) after each step -- the mechanics f4#1602's
// tracking comment asked to model on far2l's own flat TreeItem list rather
// than a nested structure.
func TestTreeExpandCollapse_SpliceLogic(t *testing.T) {
	root := t.TempDir()
	childA := filepath.Join(root, "childA")
	childB := filepath.Join(root, "childB")
	grandchild := filepath.Join(childA, "grandchild")
	if err := os.MkdirAll(grandchild, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(childB, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tp := newBareTreePanel(root)
	tp.expandAt(0)

	if len(tp.items) != 3 {
		t.Fatalf("after expanding root, items = %d, want 3 (root, childA, childB): %+v", len(tp.items), tp.items)
	}
	if tp.items[0].collapsed {
		t.Error("root should be marked expanded after expandAt")
	}
	if tp.items[1].name != "childA" || tp.items[1].depth != 1 || tp.items[1].parentIndex != 0 {
		t.Errorf("items[1] = %+v, want childA at depth 1, parentIndex 0", tp.items[1])
	}
	if tp.items[2].name != "childB" || tp.items[2].depth != 1 || tp.items[2].parentIndex != 0 {
		t.Errorf("items[2] = %+v, want childB at depth 1, parentIndex 0", tp.items[2])
	}
	if tp.items[1].last[0] {
		t.Error("childA is not the last sibling; last[0] should be false")
	}
	if !tp.items[2].last[0] {
		t.Error("childB is the last sibling; last[0] should be true")
	}

	// Expand childA (index 1): grandchild is spliced in right after it, and
	// childB's index must shift from 2 to 3.
	tp.expandAt(1)
	if len(tp.items) != 4 {
		t.Fatalf("after expanding childA, items = %d, want 4: %+v", len(tp.items), tp.items)
	}
	if tp.items[2].name != "grandchild" || tp.items[2].depth != 2 || tp.items[2].parentIndex != 1 {
		t.Errorf("items[2] = %+v, want grandchild at depth 2, parentIndex 1", tp.items[2])
	}
	if tp.items[3].name != "childB" || tp.items[3].parentIndex != 0 {
		t.Errorf("items[3] = %+v, want childB with parentIndex fixed up to 0", tp.items[3])
	}

	// Collapsing childA removes grandchild and restores childB's index.
	tp.collapseAt(1)
	if len(tp.items) != 3 {
		t.Fatalf("after collapsing childA, items = %d, want 3: %+v", len(tp.items), tp.items)
	}
	if !tp.items[1].collapsed {
		t.Error("childA should be marked collapsed again")
	}
	if tp.items[2].name != "childB" || tp.items[2].parentIndex != 0 {
		t.Errorf("items[2] = %+v, want childB with parentIndex restored to 0", tp.items[2])
	}

	// childB has no subdirectories: expanding it must flip expandable to
	// false instead of leaving a dead "+" marker.
	tp.expandAt(2)
	if tp.items[2].expandable {
		t.Error("childB has no subdirectories; expandAt should have cleared expandable")
	}
}

func TestTreeShowsDirectorySymlinksWithoutFollowingThem(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}

	tp := newBareTreePanel(root)
	tp.expandAt(0)
	idx := tp.findChild(0, "link")
	if idx < 0 {
		t.Fatal("directory symlink is missing from the tree")
	}
	item := tp.items[idx]
	if !item.vfsItem.IsDir || !item.vfsItem.IsSymlink || vfs.LinkKindOf(&item.vfsItem) != vfs.LinkSymlink {
		t.Fatalf("directory symlink metadata = %+v", item.vfsItem)
	}
	if item.expandable {
		t.Fatal("directory symlink must be a leaf to prevent cycles")
	}
	before := len(tp.items)
	tp.expandAt(idx)
	if len(tp.items) != before {
		t.Fatal("expanding a directory symlink followed its target")
	}
}

// TestTreePanel_Rescan drives Rescan directly against a small real
// directory tree that changes on disk while the tree is open: a previously
// expanded node loses its only child, a brand new sibling appears next to
// it, and a directory under a node that was never expanded gains a child
// of its own. Ctrl+R must pick up the first two (f4#1602, part 6) and leave
// the third alone -- Rescan only ever re-reads what the user actually
// expanded.
func TestTreePanel_Rescan(t *testing.T) {
	root := t.TempDir()
	childA := filepath.Join(root, "childA")
	childB := filepath.Join(root, "childB")
	grandchild := filepath.Join(childA, "grandchild")
	if err := os.MkdirAll(grandchild, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(childB, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tp := newBareTreePanel(root)
	tp.expandAt(0) // root -> childA, childB
	tp.expandAt(1) // childA -> grandchild
	tp.syncRows()
	tp.setCursor(2) // grandchild

	if got := tp.items[2].name; got != "grandchild" {
		t.Fatalf("precondition: cursor row = %q, want grandchild", got)
	}

	// Disk changes made entirely outside the tree: grandchild is gone,
	// childA has a new sibling, and childB (never expanded) gains a child
	// the tree must not surface.
	if err := os.RemoveAll(grandchild); err != nil {
		t.Fatalf("remove grandchild: %v", err)
	}
	childC := filepath.Join(root, "childC")
	if err := os.Mkdir(childC, 0o700); err != nil {
		t.Fatalf("mkdir childC: %v", err)
	}
	hiddenUnderB := filepath.Join(childB, "hidden")
	if err := os.Mkdir(hiddenUnderB, 0o700); err != nil {
		t.Fatalf("mkdir hiddenUnderB: %v", err)
	}

	tp.Rescan()

	// grandchild is gone; the cursor must land on childA, the closest
	// ancestor that still exists, not snap back to root.
	idx := tp.cursorIndex()
	if idx < 0 {
		t.Fatal("cursor should land on a real row after Rescan")
	}
	if got := tp.items[idx].path; got != childA {
		t.Errorf("cursor after Rescan = %q, want %q (closest surviving ancestor)", got, childA)
	}

	// childC must now be visible as a root-level row: root was itself
	// expanded, so Rescan must have re-read it.
	if tp.findChild(0, "childC") == -1 {
		t.Error("childC should appear under root after Rescan")
	}
	// childA had its only child removed: expandAt must have cleared its
	// expandable flag rather than leaving a dead "+" marker.
	if tp.items[idx].expandable {
		t.Error("childA has no subdirectories left; Rescan should have cleared expandable")
	}
	// childB was never expanded before Rescan; its own new child must stay
	// hidden until the user actually expands it.
	bIdx := tp.findChild(0, "childB")
	if bIdx == -1 {
		t.Fatal("childB should still be listed under root")
	}
	if !tp.items[bIdx].collapsed {
		t.Error("childB was never expanded before Rescan; it must still be collapsed")
	}
	if tp.findChild(bIdx, "hidden") != -1 {
		t.Error("childB's new child must not be spliced in: Rescan never expands a node the user did not")
	}
}

// TestNewTreePanel_RootsAtVolumeAndRevealsCwd checks NewTreePanel's actual
// construction path: rooted at the current volume (not just the source
// panel's directory) and pre-expanded down to it, with the cursor left on
// the source directory's own row.
func TestNewTreePanel_RootsAtVolumeAndRevealsCwd(t *testing.T) {
	root := t.TempDir()
	fsp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	waitForLoad(t, fsp)

	tp := NewTreePanel(fsp)
	wantPath := fsp.Vfs.GetPath() // canonical form (e.g. macOS resolves /tmp -> /private/tmp)

	if got, want := tp.root, treeVolumeRoot(wantPath); got != want {
		t.Errorf("tree root = %q, want %q (the volume root, not just the source directory)", got, want)
	}
	idx := tp.cursorIndex()
	if idx < 0 {
		t.Fatal("cursor should land on a real row after construction")
	}
	if got := filepath.Clean(tp.items[idx].path); got != filepath.Clean(wantPath) {
		t.Errorf("cursor path = %q, want %q (the source panel's own directory)", got, wantPath)
	}
}

// TestNewTreePanel_RootAtCurrentDirWhenConfigured checks the
// TreeRootWholeVolume follow-up (f4#1602, part 7 of N): with the setting
// off, the tree roots at the source panel's own current directory instead
// of its whole volume, and the cursor lands on that same root row rather
// than on some deeper child of it.
func TestNewTreePanel_RootAtCurrentDirWhenConfigured(t *testing.T) {
	before := config.App
	defer func() { config.App = before }()
	config.App.TreeRootWholeVolume = false

	root := t.TempDir()
	fsp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	waitForLoad(t, fsp)

	tp := NewTreePanel(fsp)
	wantPath := fsp.Vfs.GetPath() // canonical form (e.g. macOS resolves /tmp -> /private/tmp)

	if got, want := tp.root, wantPath; got != want {
		t.Errorf("tree root = %q, want %q (the source panel's own directory, not the volume root)", got, want)
	}
	if got, want := tp.root, treeVolumeRoot(wantPath); got == want {
		t.Errorf("tree root = %q, should not equal the volume root %q when TreeRootWholeVolume is off", got, want)
	}
	idx := tp.cursorIndex()
	if idx < 0 {
		t.Fatal("cursor should land on a real row after construction")
	}
	if got := filepath.Clean(tp.items[idx].path); got != filepath.Clean(wantPath) {
		t.Errorf("cursor path = %q, want %q (the root row itself)", got, wantPath)
	}
}

// TestNewTreePanel_RevealsThroughSymlinkedPathComponent exercises the
// symlink half of revealPath's os.Stat fallback (revealChild) with a
// symlink created directly, rather than relying on a platform-specific
// symlink already sitting in the CI runner's own temp directory layout
// (macOS's /var -> /private/var, which every os.TempDir()-derived path
// crosses on darwin runners -- see f4#1602's tracking comment for the CI
// failure this covers): the source panel's own directory is reached only
// through a symlinked path component ("link"), and NewTreePanel must still
// land the cursor on it rather than stopping at "link" itself, which
// scanChildDirs deliberately never lists as an expandable row (see its own
// doc comment).
func TestNewTreePanel_RevealsThroughSymlinkedPathComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink needs elevated privileges on Windows CI runners")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	target := filepath.Join(link, "sub")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fsp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(target))
	waitForLoad(t, fsp)

	tp := NewTreePanel(fsp)
	idx := tp.cursorIndex()
	if idx < 0 {
		t.Fatal("cursor should land on a real row after construction")
	}
	if got, want := filepath.Clean(tp.items[idx].path), filepath.Clean(target); got != want {
		t.Errorf("cursor path = %q, want %q (revealed through the symlinked \"link\" component)", got, want)
	}
}

// TestTreePanel_RightLeftExpandCollapse drives Right/Left through
// ProcessKey, the same path a real keypress takes, and checks they toggle
// expansion instead of falling through to the underlying table.
func TestTreePanel_RightLeftExpandCollapse(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "child"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tp := newBareTreePanel(root)
	tp.syncRows()

	if !tp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT}) {
		t.Fatal("Right should be consumed by the tree")
	}
	if len(tp.items) != 2 {
		t.Fatalf("after Right on root, items = %d, want 2 (root, child)", len(tp.items))
	}
	if tp.items[0].collapsed {
		t.Error("root should be expanded after Right")
	}

	if !tp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT}) {
		t.Fatal("Left should be consumed by the tree")
	}
	if len(tp.items) != 1 {
		t.Fatalf("after Left on root, items = %d, want 1 (collapsed back to just root)", len(tp.items))
	}
}

// TestTreePanel_EnterNavigatesSourceAndCloses drives the tree's Enter
// handler directly: it must move the source panel to the highlighted
// directory (the same round trip bookmarks_dialog.go's Enter performs via
// NavigateToBookmark) and then close itself via ToggleAltPanel.
func TestTreePanel_EnterNavigatesSourceAndCloses(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	vtui.FrameManager.Push(pf)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(pf) })

	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	src := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	waitForLoad(t, src)
	pf.Panels[pf.ActiveIdx] = src

	tp := newBareTreePanel(root)
	tp.src = src
	tp.expandAt(0)
	tp.syncRows()
	tp.Table.SelectPos = 1 // the only child row
	opp := 1 - pf.ActiveIdx
	pf.AltPanels[opp] = tp

	if got := tp.items[1].path; got != child {
		t.Fatalf("precondition: selected row path = %q, want %q", got, child)
	}

	if !tp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN}) {
		t.Fatal("Enter on a directory row should be consumed by the tree")
	}

	if got := src.Vfs.GetPath(); got != child {
		t.Errorf("source panel path = %q, want %q", got, child)
	}
	if pf.AltPanels[opp] != nil {
		t.Errorf("tree should have closed itself, AltPanels[%d] = %v", opp, pf.AltPanels[opp])
	}
}

// TestTreeCollapse_ForgetsCachedDescendants checks the treeExpandCache side
// of collapseAt (f4#1602, part 8 of N): collapsing a node must forget both
// its own cache entry and every descendant's, not just the rows it splices
// out of the flat list -- otherwise a later re-expand of that same node (in
// this tree panel, or in a brand new one opened afterward) would silently
// resurrect a deeper branch the cache still remembered from before the
// user's own collapse.
func TestTreeCollapse_ForgetsCachedDescendants(t *testing.T) {
	resetTreeExpandCache(t)

	root := t.TempDir()
	other := filepath.Join(root, "other")
	deep := filepath.Join(other, "deep")
	// deep needs a subdirectory of its own: expandAt marks a childless
	// directory non-expandable and never caches it.
	if err := os.MkdirAll(filepath.Join(deep, "leaf"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tp := newBareTreePanel(root)
	tp.expandAt(0)
	otherIdx := tp.findChild(0, "other")
	if otherIdx == -1 {
		t.Fatal("precondition: other should be listed under root")
	}
	tp.expandAt(otherIdx)
	deepIdx := tp.findChild(otherIdx, "deep")
	if deepIdx == -1 {
		t.Fatal("precondition: deep should be listed under other")
	}
	tp.expandAt(deepIdx)

	treeExpandCacheMu.Lock()
	_, otherCached := treeExpandCache[other]
	_, deepCached := treeExpandCache[deep]
	treeExpandCacheMu.Unlock()
	if !otherCached {
		t.Fatal("precondition: other should be cached as expanded")
	}
	if !deepCached {
		t.Fatal("precondition: deep should be cached as expanded")
	}

	tp.collapseAt(otherIdx)

	treeExpandCacheMu.Lock()
	_, otherCached = treeExpandCache[other]
	_, deepCached = treeExpandCache[deep]
	treeExpandCacheMu.Unlock()
	if otherCached {
		t.Error("collapsing other should forget its own cache entry")
	}
	if deepCached {
		t.Error("collapsing other should also forget deep's cache entry: it is no longer reachable without re-expanding other")
	}
}

// TestNewTreePanel_ResumesExpandedBranchesAcrossInstances is the actual
// f4#1602 part 8 scenario: a tree panel the user drilled into ("other",
// then "other/deep") and then closed -- NewTreePanel rebuilds TreePanel from
// scratch on every Ctrl+T, so without treeExpandCache this state would
// simply be gone once that first instance stopped existing. A second,
// independent TreePanel instance (as a real Ctrl+T-Ctrl+T-again would
// produce) must resume both branches already expanded, from the
// process-lifetime cache alone -- it never itself expanded either node.
func TestNewTreePanel_ResumesExpandedBranchesAcrossInstances(t *testing.T) {
	resetTreeExpandCache(t)
	before := config.App
	defer func() { config.App = before }()
	config.App.TreeRootWholeVolume = false // root the tree at its own directory, not the OS volume root

	root := t.TempDir()
	// "leaf" keeps deep expandable: expandAt never caches a childless
	// directory, so an empty deep could never be resumed expanded.
	if err := os.MkdirAll(filepath.Join(root, "other", "deep", "leaf"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// fsp resolves root to its canonical form (e.g. macOS resolves /tmp ->
	// /private/tmp) -- built once and reused for both instances below, so
	// "first" (built directly, bypassing NewTreePanel) and "second" (built
	// through it) key treeExpandCache with the exact same path strings.
	fsp := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	waitForLoad(t, fsp)
	canonicalRoot := fsp.Vfs.GetPath()

	first := newBareTreePanel(canonicalRoot)
	first.expandAt(0)
	otherIdx := first.findChild(0, "other")
	if otherIdx == -1 {
		t.Fatal("precondition: other should be listed under root")
	}
	first.expandAt(otherIdx)
	deepIdx := first.findChild(otherIdx, "deep")
	if deepIdx == -1 {
		t.Fatal("precondition: deep should be listed under other")
	}
	first.expandAt(deepIdx)
	// first is simply discarded here, the same as closing the tree panel
	// (Ctrl+T again, or Enter) would do -- nothing about it is carried
	// forward explicitly into the second instance below.

	second := NewTreePanel(fsp)

	otherIdx2 := second.findChild(0, "other")
	if otherIdx2 == -1 {
		t.Fatal("other should be listed under root in the second tree panel")
	}
	if second.items[otherIdx2].collapsed {
		t.Error("a freshly opened tree panel should resume \"other\" already expanded, from the process-lifetime cache")
	}
	deepIdx2 := second.findChild(otherIdx2, "deep")
	if deepIdx2 == -1 {
		t.Fatal("deep should be listed under other in the second tree panel")
	}
	if second.items[deepIdx2].collapsed {
		t.Error("a freshly opened tree panel should resume \"other/deep\" already expanded too")
	}
}

// explorerTreeSetup opens a tree beside src the way Ctrl+T does, with the
// cursor on the root row and two child folders under it.
func explorerTreeSetup(t *testing.T) (*PanelsFrame, *FileSystemPanel, *TreePanel, string, string) {
	t.Helper()
	pf := setupMockPanelsFrame(t)
	vtui.FrameManager.Push(pf)
	t.Cleanup(func() { vtui.FrameManager.RemoveFrame(pf) })

	root := t.TempDir()
	first, second := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	src := NewFileSystemPanel(0, 0, 40, 20, vfs.NewOSVFS(root))
	waitForLoad(t, src)
	pf.Panels[pf.ActiveIdx] = src

	tp := newBareTreePanel(root)
	tp.src = src
	tp.expandAt(0)
	tp.syncRows()
	opp := 1 - pf.ActiveIdx
	pf.AltPanels[opp] = tp
	pf.ActiveIdx = opp
	return pf, src, tp, first, second
}

func TestTreeExplorerModeShowsTheFolderUnderTheCursor_Issue1602(t *testing.T) {
	saved := config.App.TreeExplorerMode
	t.Cleanup(func() { config.App.TreeExplorerMode = saved })
	down := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}

	config.App.TreeExplorerMode = false
	_, src, tp, _, _ := explorerTreeSetup(t)
	start := src.Vfs.GetPath()
	tp.ProcessKey(down)
	if got := src.Vfs.GetPath(); got != start {
		t.Fatalf("with the setting off the panel moved to %q", got)
	}

	config.App.TreeExplorerMode = true
	pf, src, tp, first, second := explorerTreeSetup(t)
	tp.ProcessKey(down)
	if got := src.Vfs.GetPath(); got != first {
		t.Fatalf("after Down the panel shows %q, want %q", got, first)
	}
	tp.ProcessKey(down)
	if got := src.Vfs.GetPath(); got != second {
		t.Fatalf("after the second Down the panel shows %q, want %q", got, second)
	}

	opp := pf.ActiveIdx
	tp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	if pf.AltPanels[opp] != tp {
		t.Errorf("Enter closed the tree")
	}
	if pf.Panels[pf.ActiveIdx] != Panel(src) {
		t.Errorf("Enter did not move to the panel with the files")
	}
	if got := src.Vfs.GetPath(); got != second {
		t.Errorf("after Enter the panel shows %q, want %q", got, second)
	}
}

// TestTreeMouseExpandCollapse_Issue1602: a click on the "+"/"-" marker of a
// folder, or a double click on its name, opens and closes it like Right and
// Left; one click on the name only moves the cursor.
func TestTreeMouseExpandCollapse_Issue1602(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "childA", "grandchild"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "childB"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tp := newBareTreePanel(root)
	tp.Table.ShowHeader = false
	tp.Table.SetPosition(0, 0, 39, 9)
	tp.expandAt(0)
	tp.syncRows()

	click := func(row, x int, flags uint32) {
		t.Helper()
		down := &vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: true,
			ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: flags,
			MouseX: testutil.Int16(x), MouseY: testutil.Int16(row)}
		tp.ProcessMouse(down)
		tp.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: down.MouseX, MouseY: down.MouseY})
	}
	childA := func() treeItem { return tp.items[1] }

	// childA is row 1 at depth 1: "├─" takes two columns, the marker is columns 2-3.
	if len(tp.items) != 3 || !childA().collapsed {
		t.Fatalf("setup: items %d, childA collapsed %v", len(tp.items), childA().collapsed)
	}
	click(1, 6, 0)
	if len(tp.items) != 3 || !childA().collapsed {
		t.Errorf("a single click on the name changed the tree: %d items", len(tp.items))
	}
	click(1, 2, 0)
	if len(tp.items) != 4 || childA().collapsed {
		t.Errorf("a click on the + marker did not expand childA: %d items", len(tp.items))
	}
	click(1, 2, 0)
	if len(tp.items) != 3 || !childA().collapsed {
		t.Errorf("a click on the - marker did not collapse childA: %d items", len(tp.items))
	}
	click(1, 6, vtinput.DoubleClick)
	if len(tp.items) != 4 || childA().collapsed {
		t.Errorf("a double click on the name did not expand childA: %d items", len(tp.items))
	}
	click(1, 2, vtinput.DoubleClick)
	if len(tp.items) != 4 {
		t.Errorf("the second press of a double click on the marker toggled again: %d items", len(tp.items))
	}
	click(1, 6, vtinput.DoubleClick)
	if len(tp.items) != 3 || !childA().collapsed {
		t.Errorf("a double click on the name did not collapse childA: %d items", len(tp.items))
	}
}

// TestTreeRowTakesTheDirectoryHighlightColor_Issue1602: the tree paints a folder
// with the color the highlight rules give a directory, not the plain panel text
// color (part 13).
func TestTreeRowTakesTheDirectoryHighlightColor_Issue1602(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	oldRules, oldUser := theme.GlobalFileHighlighter.Rules, theme.GlobalFileHighlighter.UserRules
	defer func() {
		theme.GlobalFileHighlighter.Rules = oldRules
		theme.GlobalFileHighlighter.UserRules = oldUser
	}()
	theme.GlobalFileHighlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Directories
IncludeAttributes = Directory
NormalColor = foreground:#00FF00
`)))

	plain := vtui.Palette[theme.ColPanelText]
	row := treeItem{name: "work", path: "/work", depth: 1}
	got := row.GetCellAttr(0, plain)
	if got == plain {
		t.Fatal("a folder row kept the plain panel text color despite the directory rule")
	}
	if want := theme.GlobalFileHighlighter.GetColor(&vfs.VFSItem{Name: "work", IsDir: true}, plain, false, false); got != want {
		t.Errorf("tree row color %#x differs from the file panel's directory color %#x", got, want)
	}

}

func TestTreeRowUsesSymlinkAndJunctionHighlightColors(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	oldRules, oldUser := theme.GlobalFileHighlighter.Rules, theme.GlobalFileHighlighter.UserRules
	defer func() {
		theme.GlobalFileHighlighter.Rules = oldRules
		theme.GlobalFileHighlighter.UserRules = oldUser
	}()
	theme.GlobalFileHighlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Directories
IncludeAttributes = Directory
NormalColor = foreground:#00FF00
ContinueProcessing = 1

[Highlight_1]
Name = Symlinks
IncludeAttributes = Symlink
NormalColor = foreground:#FF0000
ContinueProcessing = 1

[Highlight_2]
Name = Junctions
IncludeAttributes = Junction
NormalColor = foreground:#0000FF
`)))

	plain := vtui.Palette[theme.ColPanelText]
	rows := []struct {
		name string
		item vfs.VFSItem
	}{
		{name: "symlink", item: vfs.VFSItem{Name: "symlink", IsDir: true, IsSymlink: true}},
		{name: "junction", item: vfs.VFSItem{Name: "junction", IsDir: true, IsSymlink: true, ReparseTag: vfs.ReparseTagMountPoint}},
	}
	colors := make(map[string]uint64)
	for _, tc := range rows {
		row := treeItem{name: tc.name, vfsItem: tc.item}
		got := row.GetCellAttr(0, plain)
		want := theme.GlobalFileHighlighter.GetColor(&tc.item, plain, false, false)
		if got != want || got == plain {
			t.Errorf("%s tree color = %#x, want highlighted %#x", tc.name, got, want)
		}
		colors[tc.name] = got
	}
	if colors["symlink"] == colors["junction"] {
		t.Errorf("symlink and junction colors are indistinguishable: %#x", colors["symlink"])
	}
}
