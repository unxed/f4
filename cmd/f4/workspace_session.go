package main

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type panelSessionState struct {
	Path          string
	Cursor        string
	ViewMode      int
	SortMode      int
	SortReverse   bool
	UseSortGroups bool
}

type workspaceSessionState struct {
	Number                          int
	Left, Right                     panelSessionState
	ActivePanel, WidePanel          int
	ShowPanels, ShowLeft, ShowRight bool
}

var (
	LastWorkspaceSessions []workspaceSessionState
	LastActiveWorkspace   int
)

func legacyWorkspaceSession() workspaceSessionState {
	return workspaceSessionState{
		Number: 1,
		Left: panelSessionState{
			Path: LastLeftPath, Cursor: LastLeftCursor, ViewMode: LastLeftViewMode,
			SortMode: LastLeftSortMode, SortReverse: LastLeftSortRev,
			UseSortGroups: LastLeftSortGroups,
		},
		Right: panelSessionState{
			Path: LastRightPath, Cursor: LastRightCursor, ViewMode: LastRightViewMode,
			SortMode: LastRightSortMode, SortReverse: LastRightSortRev,
			UseSortGroups: LastRightSortGroups,
		},
		ActivePanel: LastActivePanel,
		WidePanel:   LastWidePanel,
		ShowPanels:  LastShowPanels,
		ShowLeft:    LastShowLeft,
		ShowRight:   LastShowRight,
	}
}

func setLegacyWorkspaceSession(state workspaceSessionState) {
	LastLeftPath, LastRightPath = state.Left.Path, state.Right.Path
	LastLeftCursor, LastRightCursor = state.Left.Cursor, state.Right.Cursor
	LastLeftViewMode, LastRightViewMode = state.Left.ViewMode, state.Right.ViewMode
	LastLeftSortMode, LastRightSortMode = state.Left.SortMode, state.Right.SortMode
	LastLeftSortRev, LastRightSortRev = state.Left.SortReverse, state.Right.SortReverse
	LastLeftSortGroups, LastRightSortGroups = state.Left.UseSortGroups, state.Right.UseSortGroups
	LastActivePanel, LastWidePanel = state.ActivePanel, state.WidePanel
	LastShowPanels, LastShowLeft, LastShowRight = state.ShowPanels, state.ShowLeft, state.ShowRight
}

func panelsFrameOnScreen(screen *vtui.AppScreen) *PanelsFrame {
	if screen == nil {
		return nil
	}
	for _, frame := range screen.Frames {
		if pf, ok := frame.(*PanelsFrame); ok && !pf.closed {
			return pf
		}
	}
	return nil
}

func captureWorkspaceSession(pf *PanelsFrame) workspaceSessionState {
	activePanel := pf.activeIdx
	for idx, p := range pf.panels {
		if isAIPanel(p) {
			activePanel = 1 - idx
			break
		}
	}

	state := workspaceSessionState{
		ActivePanel: activePanel,
		WidePanel:   -1,
		ShowPanels:  pf.showPanels,
		ShowLeft:    pf.showLeftPanel,
		ShowRight:   pf.showRightPanel,
	}
	if pf.wide {
		if pf.widePanel >= 0 && pf.widePanel < 2 && !isAIPanel(pf.panels[pf.widePanel]) {
			state.WidePanel = pf.widePanel
		}
	}
	if left, ok := pf.panels[0].(*FileSystemPanel); ok {
		path := left.persistentPath()
		cursor := left.GetSelectedName()
		if isAIPanel(left) {
			path = aiPrevPath[0]
			if path == "" {
				path = "."
			}
			cursor = ""
		}
		state.Left = panelSessionState{
			Path: path, Cursor: cursor, ViewMode: int(left.viewMode),
			SortMode: int(left.sortMode), SortReverse: left.sortReverse,
			UseSortGroups: left.useSortGroups,
		}
	}
	if right, ok := pf.panels[1].(*FileSystemPanel); ok {
		path := right.persistentPath()
		cursor := right.GetSelectedName()
		if isAIPanel(right) {
			path = aiPrevPath[1]
			if path == "" {
				path = "."
			}
			cursor = ""
		}
		state.Right = panelSessionState{
			Path: path, Cursor: cursor, ViewMode: int(right.viewMode),
			SortMode: int(right.sortMode), SortReverse: right.sortReverse,
			UseSortGroups: right.useSortGroups,
		}
	}
	return state
}

func captureWorkspaceSessions() ([]workspaceSessionState, int) {
	if vtui.FrameManager == nil {
		return nil, 0
	}
	states := make([]workspaceSessionState, 0, len(vtui.FrameManager.Screens))
	active := 0
	lastNonAIActive := 0

	for screenIdx, screen := range vtui.FrameManager.Screens {
		pf := panelsFrameOnScreen(screen)
		if pf == nil {
			continue
		}
		hasAI := isAIPanel(pf.panels[0]) || isAIPanel(pf.panels[1])
		if !hasAI {
			lastNonAIActive = len(states)
		}
		if screenIdx == vtui.FrameManager.ActiveIdx {
			active = len(states)
		}
		state := captureWorkspaceSession(pf)
		state.Number = screen.Number
		states = append(states, state)
	}
	if active >= len(states) {
		active = 0
	}
	if active < len(vtui.FrameManager.Screens) {
		if pf := panelsFrameOnScreen(vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx]); pf != nil {
			if isAIPanel(pf.panels[0]) || isAIPanel(pf.panels[1]) {
				active = lastNonAIActive
			}
		}
	}
	return states, active
}

func parseSessionInt(ini *IniFile, section, key string, fallback int) int {
	value := fallback
	fmt.Sscanf(ini.GetString(section, key, fmt.Sprintf("%d", fallback)), "%d", &value)
	return value
}

func loadWorkspaceSessions(ini *IniFile) ([]workspaceSessionState, int) {
	count := parseSessionInt(ini, "Workspaces", "Count", 0)
	if count <= 0 || count > 100 {
		return nil, 0
	}
	states := make([]workspaceSessionState, 0, count)
	for i := 0; i < count; i++ {
		section := fmt.Sprintf("Workspace/%d", i)
		leftSection := section + "/Left"
		rightSection := section + "/Right"
		state := workspaceSessionState{
			Number:      parseSessionInt(ini, section, "Number", i+1),
			ActivePanel: parseSessionInt(ini, section, "ActivePanel", 1),
			WidePanel:   parseSessionInt(ini, section, "WidePanel", -1),
			ShowPanels:  ini.GetString(section, "ShowPanels", "1") == "1",
			ShowLeft:    ini.GetString(section, "ShowLeft", "1") == "1",
			ShowRight:   ini.GetString(section, "ShowRight", "1") == "1",
			Left: panelSessionState{
				Path: ini.GetString(leftSection, "Folder", ""), Cursor: ini.GetString(leftSection, "CurFile", ""),
				ViewMode:      parseSessionInt(ini, leftSection, "ViewMode", int(ViewModeMedium)),
				SortMode:      parseSessionInt(ini, leftSection, "SortMode", int(SortName)),
				SortReverse:   ini.GetString(leftSection, "SortReverse", "0") == "1",
				UseSortGroups: ini.GetString(leftSection, "UseSortGroups", "0") == "1",
			},
			Right: panelSessionState{
				Path: ini.GetString(rightSection, "Folder", ""), Cursor: ini.GetString(rightSection, "CurFile", ""),
				ViewMode:      parseSessionInt(ini, rightSection, "ViewMode", int(ViewModeMedium)),
				SortMode:      parseSessionInt(ini, rightSection, "SortMode", int(SortName)),
				SortReverse:   ini.GetString(rightSection, "SortReverse", "0") == "1",
				UseSortGroups: ini.GetString(rightSection, "UseSortGroups", "0") == "1",
			},
		}
		if state.ActivePanel < 0 || state.ActivePanel > 1 {
			state.ActivePanel = 1
		}
		if state.WidePanel < -1 || state.WidePanel > 1 {
			state.WidePanel = -1
		}
		if state.Number < 1 {
			state.Number = i + 1
		}
		states = append(states, state)
	}
	active := parseSessionInt(ini, "Workspaces", "Active", 0)
	if active < 0 || active >= len(states) {
		active = 0
	}
	return states, active
}

func workspaceSessionsForRestore(states []workspaceSessionState, active int, restoreTabs bool) ([]workspaceSessionState, int) {
	if restoreTabs || len(states) == 0 {
		return states, active
	}
	if active < 0 || active >= len(states) {
		active = 0
	}
	for i, state := range states {
		if i == active {
			return []workspaceSessionState{state}, 0
		}
	}
	return nil, 0
}

func renumberWorkspaceScreens() {
	if vtui.FrameManager == nil {
		return
	}
	for i, screen := range vtui.FrameManager.Screens {
		if screen != nil {
			screen.Number = i + 1
		}
	}
}

func writePanelSession(sb *strings.Builder, section string, state panelSessionState) {
	fmt.Fprintf(sb, "\n[%s]\n", section)
	fmt.Fprintf(sb, "Folder = %s\n", state.Path)
	fmt.Fprintf(sb, "CurFile = %s\n", state.Cursor)
	fmt.Fprintf(sb, "ViewMode = %d\n", state.ViewMode)
	fmt.Fprintf(sb, "SortMode = %d\n", state.SortMode)
	fmt.Fprintf(sb, "SortReverse = %d\n", map[bool]int{true: 1}[state.SortReverse])
	fmt.Fprintf(sb, "UseSortGroups = %d\n", map[bool]int{true: 1}[state.UseSortGroups])
}

func writeWorkspaceSessions(sb *strings.Builder, states []workspaceSessionState, active int) {
	if len(states) == 0 {
		return
	}
	fmt.Fprintf(sb, "\n[Workspaces]\nCount = %d\nActive = %d\n", len(states), active)
	for i, state := range states {
		section := fmt.Sprintf("Workspace/%d", i)
		fmt.Fprintf(sb, "\n[%s]\n", section)
		fmt.Fprintf(sb, "Number = %d\nActivePanel = %d\nWidePanel = %d\n", state.Number, state.ActivePanel, state.WidePanel)
		fmt.Fprintf(sb, "ShowPanels = %d\nShowLeft = %d\nShowRight = %d\n",
			map[bool]int{true: 1}[state.ShowPanels], map[bool]int{true: 1}[state.ShowLeft], map[bool]int{true: 1}[state.ShowRight])
		writePanelSession(sb, section+"/Left", state.Left)
		writePanelSession(sb, section+"/Right", state.Right)
	}
}

func validSessionViewMode(mode int) ViewMode {
	viewMode := ViewMode(mode)
	if viewMode != ViewModeMedium && viewMode != ViewModeDetailed && viewMode != ViewModeBrief {
		return ViewModeMedium
	}
	return viewMode
}

// navigatePanelTo moves a panel to path. What does not open as an object of its
// own (an archive, a provider resource) stays a plain path in the current VFS;
// a URI without its plugin is not even that.
func navigatePanelTo(pf *PanelsFrame, panel *FileSystemPanel, path string) {
	if path == "" || pf.NavigateToPath(panel, path) {
		return
	}
	if vfs.IsURIPath(path) {
		return
	}
	// A path that no longer exists leaves the VFS where it was, so re-reading
	// the directory would only redraw the one already on screen.
	if err := panel.vfs.SetPath(path); err != nil {
		vtui.DebugLog("SESSION: cannot open %s: %v", path, err)
		return
	}
	panel.ReadDirectory()
}

// applyStartupDir opens dir in both panels, so `cd dir && f4` shows dir and not
// session.ini's paths. It runs after applyWorkspaceSession and therefore wins;
// an empty dir changes nothing.
func applyStartupDir(pf *PanelsFrame, dir string) {
	if pf == nil || dir == "" {
		return
	}
	for _, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok && fsp != nil {
			navigatePanelTo(pf, fsp, dir)
			// The pending cursor names a file of the directory just left.
			fsp.pendingSelection = ""
		}
	}
}

func applyWorkspaceSession(pf *PanelsFrame, state workspaceSessionState, width, height int, restorePaths bool) {
	if pf == nil {
		return
	}
	left, leftOK := pf.panels[0].(*FileSystemPanel)
	right, rightOK := pf.panels[1].(*FileSystemPanel)
	if !leftOK || left == nil || !rightOK || right == nil {
		// A freshly constructed background workspace has not been laid out yet,
		// so ResizeConsole must create its file panels before session state can
		// be applied. The first workspace is already resized by SetupUI, while
		// restored background workspaces reach this function directly.
		pf.ResizeConsole(width, height)
		left, leftOK = pf.panels[0].(*FileSystemPanel)
		right, rightOK = pf.panels[1].(*FileSystemPanel)
		if !leftOK || left == nil || !rightOK || right == nil {
			return
		}
	}
	left.SetViewMode(validSessionViewMode(state.Left.ViewMode))
	right.SetViewMode(validSessionViewMode(state.Right.ViewMode))
	left.sortMode, right.sortMode = SortMode(state.Left.SortMode), SortMode(state.Right.SortMode)
	left.sortReverse, right.sortReverse = state.Left.SortReverse, state.Right.SortReverse
	left.useSortGroups, right.useSortGroups = state.Left.UseSortGroups, state.Right.UseSortGroups

	if restorePaths {
		navigatePanelTo(pf, left, state.Left.Path)
		navigatePanelTo(pf, right, state.Right.Path)
		left.pendingSelection, right.pendingSelection = state.Left.Cursor, state.Right.Cursor
	}

	pf.activeIdx = state.ActivePanel
	if pf.activeIdx < 0 || pf.activeIdx > 1 {
		pf.activeIdx = 1
	}
	pf.showPanels, pf.showLeftPanel, pf.showRightPanel = state.ShowPanels, state.ShowLeft, state.ShowRight
	pf.wide, pf.widePanel = false, -1
	if state.WidePanel == 0 || state.WidePanel == 1 {
		pf.wide, pf.widePanel, pf.activeIdx, pf.showPanels = true, state.WidePanel, state.WidePanel, true
	}
	pf.ResizeConsole(width, height)
}
