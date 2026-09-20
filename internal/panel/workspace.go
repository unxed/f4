package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

type PanelSessionState struct {
	Path                   string
	Cursor                 string
	ViewMode               int
	SortMode               int
	SortReverse            bool
	UseSortGroups          bool
	GroupBy                GroupMode
	GroupReverse           bool
	GroupFoldersSeparately bool
}

type WorkspaceSessionState struct {
	Number                          int
	Left, Right                     PanelSessionState
	ActivePanel, WidePanel          int
	ShowPanels, ShowLeft, ShowRight bool
}

var (
	LastWorkspaceSessions []WorkspaceSessionState
	LastActiveWorkspace   int
)

func LegacyWorkspaceSession() WorkspaceSessionState {
	return WorkspaceSessionState{
		Number: 1,
		Left: PanelSessionState{
			Path: LastLeftPath, Cursor: LastLeftCursor, ViewMode: LastLeftViewMode,
			SortMode: LastLeftSortMode, SortReverse: LastLeftSortRev,
			UseSortGroups: LastLeftSortGroups,
			GroupBy:       LastLeftGroupBy, GroupReverse: LastLeftGroupReverse, GroupFoldersSeparately: LastLeftGroupFoldersSeparately,
		},
		Right: PanelSessionState{
			Path: LastRightPath, Cursor: LastRightCursor, ViewMode: LastRightViewMode,
			SortMode: LastRightSortMode, SortReverse: LastRightSortRev,
			UseSortGroups: LastRightSortGroups,
			GroupBy:       LastRightGroupBy, GroupReverse: LastRightGroupReverse, GroupFoldersSeparately: LastRightGroupFoldersSeparately,
		},
		ActivePanel: LastActivePanel,
		WidePanel:   LastWidePanel,
		ShowPanels:  LastShowPanels,
		ShowLeft:    LastShowLeft,
		ShowRight:   LastShowRight,
	}
}

func SetLegacyWorkspaceSession(state WorkspaceSessionState) {
	LastRightGroupBy, LastRightGroupReverse, LastRightGroupFoldersSeparately = state.Right.GroupBy, state.Right.GroupReverse, state.Right.GroupFoldersSeparately
	LastLeftGroupBy, LastLeftGroupReverse, LastLeftGroupFoldersSeparately = state.Left.GroupBy, state.Left.GroupReverse, state.Left.GroupFoldersSeparately
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
		if pf, ok := frame.(*PanelsFrame); ok && !pf.Closed {
			return pf
		}
	}
	return nil
}

func CaptureWorkspaceSession(pf *PanelsFrame) WorkspaceSessionState {
	activePanel := pf.ActiveIdx
	for idx, p := range pf.Panels {
		if IsAIPanel(p) {
			activePanel = 1 - idx
			break
		}
	}

	state := WorkspaceSessionState{
		ActivePanel: activePanel,
		WidePanel:   -1,
		ShowPanels:  pf.ShowPanels,
		ShowLeft:    pf.ShowLeftPanel,
		ShowRight:   pf.ShowRightPanel,
	}
	if pf.Wide {
		if pf.WidePanel >= 0 && pf.WidePanel < 2 && !IsAIPanel(pf.Panels[pf.WidePanel]) {
			state.WidePanel = pf.WidePanel
		}
	}
	if left, ok := pf.Panels[0].(*FileSystemPanel); ok {
		path := left.PersistentPath()
		cursor := left.GetSelectedName()
		if IsAIPanel(left) {
			path = AIPrevPath[0]
			if path == "" {
				path = "."
			}
			cursor = ""
		}
		state.Left = PanelSessionState{
			Path: path, Cursor: cursor, ViewMode: int(left.ViewMode),
			SortMode: int(left.SortMode), SortReverse: left.SortReverse,
			UseSortGroups: left.UseSortGroups,
			GroupBy:       left.GroupBy, GroupReverse: left.GroupReverse, GroupFoldersSeparately: left.GroupFoldersSeparately,
		}
	}
	if right, ok := pf.Panels[1].(*FileSystemPanel); ok {
		path := right.PersistentPath()
		cursor := right.GetSelectedName()
		if IsAIPanel(right) {
			path = AIPrevPath[1]
			if path == "" {
				path = "."
			}
			cursor = ""
		}
		state.Right = PanelSessionState{
			Path: path, Cursor: cursor, ViewMode: int(right.ViewMode),
			SortMode: int(right.SortMode), SortReverse: right.SortReverse,
			UseSortGroups: right.UseSortGroups,
			GroupBy:       right.GroupBy, GroupReverse: right.GroupReverse, GroupFoldersSeparately: right.GroupFoldersSeparately,
		}
	}
	return state
}

func CaptureWorkspaceSessions() ([]WorkspaceSessionState, int) {
	if vtui.FrameManager == nil {
		return nil, 0
	}
	states := make([]WorkspaceSessionState, 0, len(vtui.FrameManager.Screens))
	active := 0
	lastNonAIActive := 0

	for screenIdx, screen := range vtui.FrameManager.Screens {
		pf := panelsFrameOnScreen(screen)
		if pf == nil {
			continue
		}
		hasAI := IsAIPanel(pf.Panels[0]) || IsAIPanel(pf.Panels[1])
		if !hasAI {
			lastNonAIActive = len(states)
		}
		if screenIdx == vtui.FrameManager.ActiveIdx {
			active = len(states)
		}
		state := CaptureWorkspaceSession(pf)
		state.Number = screen.Number
		states = append(states, state)
	}
	if active >= len(states) {
		active = 0
	}
	if active < len(vtui.FrameManager.Screens) {
		if pf := panelsFrameOnScreen(vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx]); pf != nil {
			if IsAIPanel(pf.Panels[0]) || IsAIPanel(pf.Panels[1]) {
				active = lastNonAIActive
			}
		}
	}
	return states, active
}

func parseSessionInt(ini *ini.File, section, key string, fallback int) int {
	value := fallback
	fmt.Sscanf(ini.GetString(section, key, fmt.Sprintf("%d", fallback)), "%d", &value)
	return value
}

func LoadWorkspaceSessions(ini *ini.File) ([]WorkspaceSessionState, int) {
	count := parseSessionInt(ini, "Workspaces", "Count", 0)
	if count <= 0 || count > 100 {
		return nil, 0
	}
	states := make([]WorkspaceSessionState, 0, count)
	for i := 0; i < count; i++ {
		section := fmt.Sprintf("Workspace/%d", i)
		leftSection := section + "/Left"
		rightSection := section + "/Right"
		state := WorkspaceSessionState{
			Number:      parseSessionInt(ini, section, "Number", i+1),
			ActivePanel: parseSessionInt(ini, section, "ActivePanel", 1),
			WidePanel:   parseSessionInt(ini, section, "WidePanel", -1),
			ShowPanels:  ini.GetString(section, "ShowPanels", "1") == "1",
			ShowLeft:    ini.GetString(section, "ShowLeft", "1") == "1",
			ShowRight:   ini.GetString(section, "ShowRight", "1") == "1",
			Left: PanelSessionState{
				Path: ini.GetString(leftSection, "Folder", ""), Cursor: ini.GetString(leftSection, "CurFile", ""),
				ViewMode:               parseSessionInt(ini, leftSection, "ViewMode", int(ViewModeMedium)),
				SortMode:               parseSessionInt(ini, leftSection, "SortMode", int(SortName)),
				SortReverse:            ini.GetString(leftSection, "SortReverse", "0") == "1",
				UseSortGroups:          ini.GetString(leftSection, "UseSortGroups", "0") == "1",
				GroupBy:                ValidGroupMode(GroupMode(parseSessionInt(ini, leftSection, "GroupBy", 0))),
				GroupReverse:           ini.GetString(leftSection, "GroupReverse", "0") == "1",
				GroupFoldersSeparately: ini.GetString(leftSection, "GroupFoldersSeparately", "1") == "1",
			},
			Right: PanelSessionState{
				Path: ini.GetString(rightSection, "Folder", ""), Cursor: ini.GetString(rightSection, "CurFile", ""),
				ViewMode:               parseSessionInt(ini, rightSection, "ViewMode", int(ViewModeMedium)),
				SortMode:               parseSessionInt(ini, rightSection, "SortMode", int(SortName)),
				SortReverse:            ini.GetString(rightSection, "SortReverse", "0") == "1",
				UseSortGroups:          ini.GetString(rightSection, "UseSortGroups", "0") == "1",
				GroupBy:                ValidGroupMode(GroupMode(parseSessionInt(ini, rightSection, "GroupBy", 0))),
				GroupReverse:           ini.GetString(rightSection, "GroupReverse", "0") == "1",
				GroupFoldersSeparately: ini.GetString(rightSection, "GroupFoldersSeparately", "1") == "1",
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

func WorkspaceSessionsForRestore(states []WorkspaceSessionState, active int, restoreTabs bool) ([]WorkspaceSessionState, int) {
	if restoreTabs || len(states) == 0 {
		return states, active
	}
	if active < 0 || active >= len(states) {
		active = 0
	}
	for i, state := range states {
		if i == active {
			return []WorkspaceSessionState{state}, 0
		}
	}
	return nil, 0
}

func RenumberWorkspaceScreens() {
	if vtui.FrameManager == nil {
		return
	}
	for i, screen := range vtui.FrameManager.Screens {
		if screen != nil {
			screen.Number = i + 1
		}
	}
}

func writePanelSession(sb *strings.Builder, section string, state PanelSessionState) {
	fmt.Fprintf(sb, "\n[%s]\n", section)
	fmt.Fprintf(sb, "Folder = %s\n", state.Path)
	fmt.Fprintf(sb, "CurFile = %s\n", state.Cursor)
	fmt.Fprintf(sb, "ViewMode = %d\n", state.ViewMode)
	fmt.Fprintf(sb, "SortMode = %d\n", state.SortMode)
	fmt.Fprintf(sb, "SortReverse = %d\n", map[bool]int{true: 1}[state.SortReverse])
	fmt.Fprintf(sb, "UseSortGroups = %d\n", map[bool]int{true: 1}[state.UseSortGroups])
	fmt.Fprintf(sb, "GroupBy = %d\nGroupReverse = %d\nGroupFoldersSeparately = %d\n", ValidGroupMode(state.GroupBy), map[bool]int{true: 1}[state.GroupReverse], map[bool]int{true: 1}[state.GroupFoldersSeparately])
}

func WriteWorkspaceSessions(sb *strings.Builder, states []WorkspaceSessionState, active int) {
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

// navigatePanelTo moves a panel to a saved path. A failed restore is left
// alone: retrying the same path through the current VFS can turn a provider's
// internal path into a local OS path during startup.
func navigatePanelTo(pf *PanelsFrame, panel *FileSystemPanel, path string) {
	if path != "" {
		pf.NavigateToPath(panel, path)
	}
}

// IsStartupFile says whether a path named on the command line is a file rather
// than a folder: something that exists and is not a directory. Such a path opens
// in the viewer (issue #991), and its panel shows the folder it is in.
func IsStartupFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// StartupKeepPanel stands in for a path in ApplyStartupDirs and says that panel
// is left as the session restored it. It cannot be an absolute path, so it never
// clashes with one, and a process that does not know it (an older daemon a newer
// client attaches to) fails to navigate to it and leaves the panel alone anyway.
const StartupKeepPanel = "-"

// ApplyStartupDirs opens left and right in the two panels, so `cd dir && f4`
// shows dir and `f4 dir1 dir2` shows both, rather than session.ini's paths. It
// runs after ApplyWorkspaceSession and therefore wins; an empty left changes
// nothing, and an empty right sends both panels to left.
//
// A path that names a file sends its panel to the file's folder with the cursor
// on the file, so closing the viewer `f4 file` opened lands on it (issue #991).
//
// A right that differs from left only ever comes from the command line, so it
// also says the focus belongs on the directory named first.
func ApplyStartupDirs(pf *PanelsFrame, left, right string) {
	if pf == nil || left == "" {
		return
	}
	fromCommandLine := right != "" && right != left
	if right == "" {
		right = left
	}
	for idx, dir := range [2]string{left, right} {
		if dir == StartupKeepPanel {
			continue
		}
		if fsp, ok := pf.Panels[idx].(*FileSystemPanel); ok && fsp != nil {
			focus := ""
			if IsStartupFile(dir) {
				dir, focus = filepath.Dir(dir), filepath.Base(dir)
			}
			navigatePanelTo(pf, fsp, dir)
			// The pending cursor names a file of the directory just left;
			// for a file it names that file, placed when the listing arrives.
			fsp.PendingSelection = focus
		}
	}
	if fromCommandLine {
		pf.ActiveIdx = 0
	}
}

func ApplyWorkspaceSession(pf *PanelsFrame, state WorkspaceSessionState, width, height int, restorePaths bool) {
	if pf == nil {
		return
	}
	left, leftOK := pf.Panels[0].(*FileSystemPanel)
	right, rightOK := pf.Panels[1].(*FileSystemPanel)
	if !leftOK || left == nil || !rightOK || right == nil {
		// A freshly constructed background workspace has not been laid out yet,
		// so ResizeConsole must create its file panels before session state can
		// be applied. The first workspace is already resized by SetupUI, while
		// restored background workspaces reach this function directly.
		pf.ResizeConsole(width, height)
		left, leftOK = pf.Panels[0].(*FileSystemPanel)
		right, rightOK = pf.Panels[1].(*FileSystemPanel)
		if !leftOK || left == nil || !rightOK || right == nil {
			return
		}
	}
	left.SetViewMode(validSessionViewMode(state.Left.ViewMode))
	right.SetViewMode(validSessionViewMode(state.Right.ViewMode))
	left.SortMode, right.SortMode = SortMode(state.Left.SortMode), SortMode(state.Right.SortMode)
	left.SortReverse, right.SortReverse = state.Left.SortReverse, state.Right.SortReverse
	left.UseSortGroups, right.UseSortGroups = state.Left.UseSortGroups, state.Right.UseSortGroups

	left.SetGrouping(state.Left.GroupBy, state.Left.GroupReverse, state.Left.GroupFoldersSeparately)

	right.SetGrouping(state.Right.GroupBy, state.Right.GroupReverse, state.Right.GroupFoldersSeparately)

	if restorePaths {
		navigatePanelTo(pf, left, state.Left.Path)
		navigatePanelTo(pf, right, state.Right.Path)
		left.PendingSelection, right.PendingSelection = state.Left.Cursor, state.Right.Cursor
	}

	pf.ActiveIdx = state.ActivePanel
	if pf.ActiveIdx < 0 || pf.ActiveIdx > 1 {
		pf.ActiveIdx = 1
	}
	pf.ShowPanels, pf.ShowLeftPanel, pf.ShowRightPanel = state.ShowPanels, state.ShowLeft, state.ShowRight
	pf.Wide, pf.WidePanel = false, -1
	if state.WidePanel == 0 || state.WidePanel == 1 {
		pf.Wide, pf.WidePanel, pf.ActiveIdx, pf.ShowPanels = true, state.WidePanel, state.WidePanel, true
	}
	pf.ResizeConsole(width, height)
}
