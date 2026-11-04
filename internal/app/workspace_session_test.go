package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestWorkspaceSessionSerializationPreservesOrderNumbersAndActiveTab(t *testing.T) {
	states := []panel.WorkspaceSessionState{
		{
			Number: 2, ActivePanel: 0, WidePanel: -1,
			ShowPanels: true, ShowLeft: true, ShowRight: true,
			Left:  panel.PanelSessionState{Path: "C:/alpha", Cursor: "a.txt", ViewMode: int(panel.ViewModeBrief), SortMode: int(panel.SortName)},
			Right: panel.PanelSessionState{Path: "D:/beta", Cursor: "b.txt", ViewMode: int(panel.ViewModeDetailed), SortMode: int(panel.SortTime), SortReverse: true},
		},
		{
			Number: 1, ActivePanel: 1, WidePanel: 1,
			ShowPanels: true, ShowLeft: false, ShowRight: true, TerminalOnly: true,
			Left:  panel.PanelSessionState{Path: "C:/short", ViewMode: int(panel.ViewModeMedium), SortMode: int(panel.SortExt)},
			Right: panel.PanelSessionState{Path: "D:/long", ViewMode: int(panel.ViewModeBrief), SortMode: int(panel.SortSize)},
		},
	}

	var encoded strings.Builder
	panel.WriteWorkspaceSessions(&encoded, states, 1)
	got, active := panel.LoadWorkspaceSessions(ini.Parse(strings.NewReader(encoded.String())))
	if active != 1 {
		t.Fatalf("active workspace = %d, want 1", active)
	}
	if !reflect.DeepEqual(got, states) {
		t.Fatalf("workspace session round trip mismatch:\n got: %#v\nwant: %#v", got, states)
	}
}

func TestCaptureWorkspaceSessionsUsesTabOrderAndActiveIndex(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	newPanels := func(leftPath, rightPath string) *panel.PanelsFrame {
		return &panel.PanelsFrame{
			Panels: [2]panel.Panel{
				&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(leftPath), ViewMode: panel.ViewModeBrief, SortMode: panel.SortName},
				&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(rightPath), ViewMode: panel.ViewModeDetailed, SortMode: panel.SortSize},
			},
			ActiveIdx: 1, WidePanel: -1, ShowPanels: true, ShowLeftPanel: true, ShowRightPanel: true,
		}
	}
	first := newPanels(t.TempDir(), t.TempDir())
	second := newPanels(t.TempDir(), t.TempDir())
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{
		{Number: 8, Frames: []vtui.Frame{first}},
		{Number: 3, Frames: []vtui.Frame{second}},
	}, 1))

	states, active := panel.CaptureWorkspaceSessions()
	if active != 1 {
		t.Fatalf("captured active workspace = %d, want 1", active)
	}
	if len(states) != 2 || states[0].Number != 8 || states[1].Number != 3 {
		t.Fatalf("captured workspace order/numbers = %#v", states)
	}
	if states[0].Left.Path != first.Panels[0].(*panel.FileSystemPanel).Vfs.GetPath() ||
		states[1].Right.Path != second.Panels[1].(*panel.FileSystemPanel).Vfs.GetPath() {
		t.Fatal("captured panel paths do not belong to their ordered workspaces")
	}
}

func TestCaptureWorkspaceSessionPreservesPendingProviderPath(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := &panel.PanelsFrame{
		Panels: [2]panel.Panel{
			&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())},
			&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())},
		},
	}
	left := pf.Panels[0].(*panel.FileSystemPanel)
	left.ProviderOpenTarget = "cloud://account/photos"
	left.ProviderOpenTask = &vtui.TaskContext{}

	state := panel.CaptureWorkspaceSession(pf)
	if state.Left.Path != left.ProviderOpenTarget {
		t.Fatalf("saved pending path = %q, want %q", state.Left.Path, left.ProviderOpenTarget)
	}
}

type workspaceSessionNestedVFS struct {
	*vfs.NullVFS
	parent vfs.VFS
	path   string
}

func (v *workspaceSessionNestedVFS) GetPath() string    { return v.path }
func (v *workspaceSessionNestedVFS) ParentVFS() vfs.VFS { return v.parent }

func TestCaptureWorkspaceSessionSkipsUnrestorableNestedPath(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	remote := &workspaceSessionNestedVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "/home/user",
	}
	pf := &panel.PanelsFrame{
		Panels: [2]panel.Panel{
			&panel.FileSystemPanel{Vfs: remote},
			&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())},
		},
	}

	state := panel.CaptureWorkspaceSession(pf)
	if state.Left.Path != "" {
		t.Fatalf("unrestorable nested path saved as %q, want empty", state.Left.Path)
	}
}

func TestCaptureWorkspaceSessionKeepsPersistentNestedURI(t *testing.T) {
	remote := &workspaceSessionNestedVFS{
		NullVFS: vfs.NewNullVFS(0),
		parent:  vfs.NewNullVFS(0),
		path:    "archive://bundle/path",
	}
	pf := &panel.PanelsFrame{
		Panels: [2]panel.Panel{
			&panel.FileSystemPanel{Vfs: remote},
			&panel.FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir())},
		},
	}

	state := panel.CaptureWorkspaceSession(pf)
	if state.Left.Path != remote.path {
		t.Fatalf("persistent nested URI saved as %q, want %q", state.Left.Path, remote.path)
	}
}

func TestApplyWorkspaceSessionInitializesFreshPanelsFrame(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pf := panel.NewPanelsFrame()
	t.Cleanup(func() { pf.Close() })
	state := panel.WorkspaceSessionState{
		ActivePanel: 0,
		WidePanel:   -1,
		ShowPanels:  false, TerminalOnly: true,
		ShowLeft:  true,
		ShowRight: true,
		Left: panel.PanelSessionState{
			ViewMode: int(panel.ViewModeBrief), SortMode: int(panel.SortExt), SortReverse: true,
		},
		Right: panel.PanelSessionState{
			ViewMode: int(panel.ViewModeDetailed), SortMode: int(panel.SortTime),
		},
	}
	var encoded strings.Builder
	panel.WriteWorkspaceSessions(&encoded, []panel.WorkspaceSessionState{state}, 0)
	states, _ := panel.LoadWorkspaceSessions(ini.Parse(strings.NewReader(encoded.String())))
	if len(states) != 1 {
		t.Fatalf("restored %d workspace states, want 1", len(states))
	}
	state = states[0]

	// Background workspaces are restored immediately after construction,
	// before SetupUI has explicitly resized them.
	panel.ApplyWorkspaceSession(pf, state, 80, 25, false)

	left, leftOK := pf.Panels[0].(*panel.FileSystemPanel)
	right, rightOK := pf.Panels[1].(*panel.FileSystemPanel)
	if !leftOK || left == nil || !rightOK || right == nil {
		t.Fatalf("fresh workspace panels were not initialized: left=%T right=%T", pf.Panels[0], pf.Panels[1])
	}
	if pf.ActiveIdx != 0 {
		t.Fatalf("active panel = %d, want 0", pf.ActiveIdx)
	}
	if !pf.TerminalOnly || !pf.PanelsLocked() {
		t.Fatal("terminal-only workspace state was not applied")
	}
	if left.ViewMode != panel.ViewModeBrief || left.SortMode != panel.SortExt || !left.SortReverse {
		t.Fatalf("left panel state was not restored: view=%v sort=%v reverse=%v", left.ViewMode, left.SortMode, left.SortReverse)
	}
	if right.ViewMode != panel.ViewModeDetailed || right.SortMode != panel.SortTime || right.SortReverse {
		t.Fatalf("right panel state was not restored: view=%v sort=%v reverse=%v", right.ViewMode, right.SortMode, right.SortReverse)
	}
}

func TestWorkspaceSessionsForRestore(t *testing.T) {
	states := []panel.WorkspaceSessionState{{Number: 2}, {Number: 7}, {Number: 9}}

	all, active := panel.WorkspaceSessionsForRestore(states, 1, true)
	if !reflect.DeepEqual(all, states) || active != 1 {
		t.Fatalf("enabled restoration changed sessions: states=%#v active=%d", all, active)
	}

	one, active := panel.WorkspaceSessionsForRestore(states, 1, false)
	if len(one) != 1 || one[0].Number != 7 || active != 0 {
		t.Fatalf("disabled restoration = %#v, active=%d; want active session 7 only", one, active)
	}

	one, active = panel.WorkspaceSessionsForRestore(states, 99, false)
	if len(one) != 1 || one[0].Number != 2 || active != 0 {
		t.Fatalf("invalid active index fallback = %#v, active=%d; want first session", one, active)
	}
}

func TestRenumberWorkspaceScreensFollowsCurrentOrder(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	t.Cleanup(testutil.SetFrameManagerScreens(t, []*vtui.AppScreen{
		{Number: 7},
		{Number: 2},
		{Number: 11},
	}, 0))

	panel.RenumberWorkspaceScreens()

	got := []int{
		vtui.FrameManager.Screens[0].Number,
		vtui.FrameManager.Screens[1].Number,
		vtui.FrameManager.Screens[2].Number,
	}
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("renumbered workspace screens = %v, want %v", got, want)
	}
}
