package main

import (
	"context"
	"fmt"
	"github.com/unxed/f4/vfs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type DriveEntry struct {
	Name    string
	Factory func() vfs.VFS
}

var DriveRegistry []DriveEntry
var pluginRegistryMu sync.RWMutex

func RegisterDrive(name string, factory func() vfs.VFS) {
	pluginRegistryMu.Lock()
	defer pluginRegistryMu.Unlock()
	for i, d := range DriveRegistry {
		if d.Name == name {
			DriveRegistry[i].Factory = factory
			return
		}
	}
	DriveRegistry = append(DriveRegistry, DriveEntry{Name: name, Factory: factory})
}

func driveRegistrySnapshot() []DriveEntry {
	pluginRegistryMu.RLock()
	defer pluginRegistryMu.RUnlock()
	return append([]DriveEntry(nil), DriveRegistry...)
}

type HotkeyEntry struct {
	VK      uint16
	Mods    vtinput.ControlKeyState
	Handler func(app vfs.App)
}

var GlobalHotkeys []HotkeyEntry

func RegisterGlobalHotkey(vk uint16, mods vtinput.ControlKeyState, handler func(app vfs.App)) {
	pluginRegistryMu.Lock()
	GlobalHotkeys = append(GlobalHotkeys, HotkeyEntry{VK: vk, Mods: mods, Handler: handler})
	pluginRegistryMu.Unlock()
}

func globalHotkeysSnapshot() []HotkeyEntry {
	pluginRegistryMu.RLock()
	defer pluginRegistryMu.RUnlock()
	return append([]HotkeyEntry(nil), GlobalHotkeys...)
}
func (pf *PanelsFrame) GetActivePanelVFS() vfs.VFS  { return pf.Active().(*FileSystemPanel).vfs }
func (pf *PanelsFrame) GetPassivePanelVFS() vfs.VFS { return pf.Passive().(*FileSystemPanel).vfs }
func (pf *PanelsFrame) GetSelectedNames() []string {
	return pf.Active().(*FileSystemPanel).GetSelectedNames()
}
func (pf *PanelsFrame) GetMarkedNames() []string {
	if panel := pf.getActivePanel(); panel != nil {
		return panel.GetMarkedNames()
	}
	return nil
}
func (pf *PanelsFrame) ReplaceMarkedNames(names []string) {
	if panel := pf.getActivePanel(); panel != nil {
		panel.ReplaceMarkedNames(names)
	}
}
func (pf *PanelsFrame) GetSelectedName() string {
	return pf.Active().(*FileSystemPanel).GetSelectedName()
}
func (pf *PanelsFrame) SetPendingSelection(name string) {
	if fsp := pf.getActivePanel(); fsp != nil {
		fsp.pendingSelection = name
	}
}

func (pf *PanelsFrame) addCommandHistory(cmd string) {
	pf.cmdLine.Edit.AddHistory(cmd)
	hp, isF4 := vtui.GlobalHistoryProvider.(*F4HistoryProvider)
	if !isF4 {
		if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory("cmdline", pf.cmdLine.Edit.History)
		}
		return
	}

	rich := hp.LoadRichHistory("cmdline")
	if len(rich) == 0 {
		rich = recordsFromNames(pf.cmdLine.Edit.History)
	}
	var newRich []HistoryRecord
	curDir := ""
	if fsp := pf.getActivePanel(); fsp != nil {
		curDir = fsp.vfs.GetPath()
	}

	newRich = append(newRich, HistoryRecord{
		Name:      cmd,
		Dir:       curDir,
		Timestamp: time.Now(),
	})

	for _, r := range rich {
		if r.Name != cmd {
			newRich = append(newRich, r)
		} else if r.Lock {
			newRich[0].Lock = true
		}
	}

	limit := pf.cmdLine.Edit.HistoryLimit
	if limit <= 0 {
		limit = 100
	}
	newRich = limitRichHistory(newRich, limit)
	hp.SaveRichHistory("cmdline", newRich)

	var strHist []string
	for _, r := range newRich {
		strHist = append(strHist, r.Name)
	}
	pf.cmdLine.Edit.History = strHist
}
func (pf *PanelsFrame) insertPathToCmdLine(path string) {
	if path != "" {
		special := " &|;<>()$`\\\"'"
		if runtime.GOOS == "windows" {
			// Backslash is the path separator there, not an escape
			// character; with it in the set every single path got quoted.
			special = " &|;<>()^\"'"
		}
		if strings.ContainsAny(path, special) {
			if runtime.GOOS == "windows" {
				if !strings.HasPrefix(path, "\"") {
					path = "\"" + path + "\""
				}
			} else {
				if !strings.HasPrefix(path, "'") {
					path = "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
				}
			}
		}
		txt := pf.cmdLine.Edit.GetText()
		if len(txt) > 0 && txt[len(txt)-1] != ' ' {
			pf.cmdLine.InsertString(" ")
		}
		pf.cmdLine.InsertString(path)
	}
}

// handlePanelPathEditHotkey inserts a panel path into the focused dialog edit.
// It lives in the frame-level event filter because modal dialogs otherwise
// consume Ctrl+[ and Ctrl+] before PanelsFrame can see them.
func handlePanelPathEditHotkey(e *vtinput.InputEvent) bool {
	if e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	ctrl := e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0
	alt := e.ControlKeyState&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0
	shift := e.ControlKeyState&vtinput.ShiftPressed != 0
	left := e.VirtualKeyCode == vtinput.VK_OEM_4 || e.Char == '['
	right := e.VirtualKeyCode == vtinput.VK_OEM_6 || e.Char == ']'
	if !ctrl || alt || shift || (!left && !right) || vtui.FrameManager == nil {
		return false
	}

	top := vtui.FrameManager.GetTopFrame()
	focusContainer, ok := top.(vtui.FocusContainer)
	if !ok {
		return false
	}
	edit, ok := focusContainer.GetFocusedItem().(*vtui.Edit)
	if !ok || edit.IsDisabled() {
		return false
	}

	var pf *PanelsFrame
	frames := vtui.FrameManager.GetActiveFrames(vtui.FrameManager.ActiveIdx)
	for i := len(frames) - 1; i >= 0; i-- {
		if candidate, ok := frames[i].(*PanelsFrame); ok {
			pf = candidate
			break
		}
	}
	if pf == nil {
		return false
	}

	var panel *FileSystemPanel
	if left {
		panel = pf.visualLeftFSP()
	} else {
		panel = pf.visualRightFSP()
	}
	if panel == nil || panel.vfs == nil || panel.vfs.GetPath() == "" {
		return false
	}
	edit.InsertString(panel.vfs.GetPath())
	vtui.FrameManager.Redraw()
	return true
}

type PanelController interface {
	ProcessPanelKey(app vfs.App, e *vtinput.InputEvent) bool
}

// A Panel is an interface for any content that can be placed in the "half" of the manager.
// This could be a file list, a folder tree, or even a quick view panel (Viewer).
type Panel interface {
	Show(scr *vtui.ScreenBuf)
	ProcessKey(e *vtinput.InputEvent) bool
	ProcessMouse(e *vtinput.InputEvent) bool
	SetFocus(f bool)
	IsFocused() bool
	SetPosition(x1, y1, x2, y2 int)
	GetPosition() (int, int, int, int)
	GetSelectedName() string
}

// PanelsFrame is the main frame of the f4 manager, containing left and right panels.
type PanelsFrame struct {
	vtui.BaseFrame
	panels  [2]Panel
	dragOut dragOutState
	// externalUIRunner is normally nil, which selects the real desktop
	// launcher. Tests install a per-frame recorder instead of spawning native
	// Explorer/association windows.
	externalUIRunner externalUICommandRunner
	// altPanels[i] holds an alternate view (Info / Quick view / Tree)
	// covering slot i's file panel. When non-nil it's rendered in
	// place of panels[i]; panels[i] stays alive underneath and is
	// still the "logical" panel for command dispatch. Alt panels
	// never take focus (see AltPanel in info_panel.go).
	altPanels             [2]AltPanel
	activeIdx             int    // 0 for left, 1 for right
	folderHistoryPos      [2]int // position in provider's newest-first folder history
	executing             bool
	afterExecution        func() // run once by endExecution; queues the next user-menu step
	shellPromptReady      bool
	ignoreNextPrompt      bool
	returnToPanels        bool
	cmdSession            *cmdShellSession // local cmd.exe completion tracking (Windows)
	workspaceCommandTitle string

	menuBar *vtui.MenuBar
	cmdLine *CommandLine
	keyBar  *vtui.KeyBar

	showKeyBar     bool
	showPanels     bool
	showLeftPanel  bool
	showRightPanel bool
	wide           bool
	widePanel      int // -1 for normal split, 0/1 for the slot occupying the full width
	// panelMouseCapture owns a complete button-down/move/release gesture.
	// Without it, dragging a row across the split can accidentally start the
	// other panel's scrollbar (or vice versa).
	panelMouseCapture Panel
	middleMouseDown   bool
	lastW             int
	lastH             int

	// Panel geometry offsets, adjusted by Ctrl+Left/Right (width) and
	// Ctrl+Up/Down (height). Names and semantics match far2l's [Layout]:
	// widthDecrement > 0 grows the right panel, < 0 grows the left;
	// leftHeightDecrement / rightHeightDecrement >= 0 shrink the
	// corresponding panel from the bottom, growing the terminal area
	// above it. Ctrl+Up/Down bumps both symmetrically for now — the
	// asymmetric Ctrl+Shift+Up/Down handler will come as a follow-up
	// PR, which is why the fields are split already.
	widthDecrement       int
	leftHeightDecrement  int
	rightHeightDecrement int

	// Integrated Terminal
	pty            PtyBackend
	remotePtys     map[vfs.VFS]PtyBackend
	ptyMutex       sync.Mutex
	termView       *TerminalView
	parser         *AnsiParser
	terminalRedraw *terminalRedrawScheduler

	// Process-environment updates use their own locks so an Apply from a
	// plugin cannot interleave a private assignment script with user input.
	processEnvironmentMu                sync.Mutex
	processEnvironmentWriteMu           sync.Mutex
	processEnvironmentGeneration        uint64
	pendingProcessEnvironmentGeneration uint64
	pendingProcessEnvironment           []vfs.ProcessEnvironmentChange
	processEnvironmentBusy              bool
	processEnvironmentDeliveryFailed    bool
	processEnvironmentInFlight          *processEnvironmentShellInFlight
	processEnvironmentOutputTail        []byte
	deferredProcessEnvironmentInput     []byte
	processEnvironmentClosed            bool

	lastAlt        bool
	lastBusy       bool
	lastShowPanels bool

	lastAutoRefresh    time.Time
	lastKey            rune
	lastKeyEvent       time.Time
	commandLineFocused bool

	lastPtyPath string
	lastPtyVFS  vfs.VFS
	closed      bool

	shellMode         ShellMode
	hostConsoleActive bool
	hostConsoleMu     sync.Mutex
	lastOverlayDraw   time.Time

	// Terminal mouse-selection state. Kept in PanelsFrame because
	// mouse routing lives here; the highlight and text extraction
	// live on the TerminalView itself.
	termSelDragging bool      // LMB gesture is waiting for its release
	termSelClickN   int       // 1 / 2 / 3 for triple-click detection
	termSelClickAt  time.Time // time of the last click
	termSelClickX   int
	termSelClickY   int
}

func (pf *PanelsFrame) Left() Panel  { return pf.panels[0] }
func (pf *PanelsFrame) Right() Panel { return pf.panels[1] }

// visualLeftFSP / visualRightFSP return the file panels resolved by
// their on-screen X-position rather than slot index. The Ctrl+U
// panel swap re-assigns pf.panels[0]/[1] but keeps the two frames
// where the user sees them, so index-based routing sends Ctrl+[/]
// to the wrong side after a swap. In single-panel mode the visible
// panel occupies both visual sides, so both resolvers return it.
func (pf *PanelsFrame) visualLeftFSP() *FileSystemPanel {
	a, _ := pf.panels[0].(*FileSystemPanel)
	b, _ := pf.panels[1].(*FileSystemPanel)
	if pf.showPanels && pf.showLeftPanel != pf.showRightPanel {
		if pf.showLeftPanel {
			return a
		}
		return b
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	xA, _, _, _ := a.GetPosition()
	xB, _, _, _ := b.GetPosition()
	if xA <= xB {
		return a
	}
	return b
}

func (pf *PanelsFrame) visualRightFSP() *FileSystemPanel {
	a, _ := pf.panels[0].(*FileSystemPanel)
	b, _ := pf.panels[1].(*FileSystemPanel)
	if pf.showPanels && pf.showLeftPanel != pf.showRightPanel {
		if pf.showLeftPanel {
			return a
		}
		return b
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	xA, _, _, _ := a.GetPosition()
	xB, _, _, _ := b.GetPosition()
	if xA > xB {
		return a
	}
	return b
}
func (pf *PanelsFrame) Active() Panel  { return pf.panels[pf.activeIdx] }
func (pf *PanelsFrame) Passive() Panel { return pf.panels[1-pf.activeIdx] }

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 1, widePanel: -1, folderHistoryPos: [2]int{-1, -1}}
	pf.terminalRedraw = newTerminalRedrawScheduler(func() { vtui.FrameManager.Redraw() })
	pf.SetHelp("Panels")
	pf.showKeyBar = true
	pf.showPanels = true
	pf.lastShowPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true
	pf.widthDecrement = AppConfig.WidthDecrement
	pf.leftHeightDecrement = AppConfig.LeftHeightDecrement
	pf.rightHeightDecrement = AppConfig.RightHeightDecrement
	pf.shellMode = resolveShellMode(ShellModeConfig{
		ConsoleMode:      AppConfig.ConsoleMode,
		ConsoleOverlayUI: AppConfig.ConsoleOverlayUI,
	})
	vtui.DebugLog("SHELL: mode=%s cfg.ConsoleMode=%q cfg.ConsoleOverlayUI=%v view=%s backend=%q",
		pf.shellMode, AppConfig.ConsoleMode, AppConfig.ConsoleOverlayUI,
		consoleViewStyleFor(pf.shellMode), SelectedTTYBackend)

	pf.menuBar = vtui.NewMenuBar(nil)
	pf.menuBar.SetOwner(pf)
	pf.menuBar.Items = pf.buildMenuItems()
	// We no longer need pf.menuBar.OnCommand for routing!
	pf.cmdLine = NewCommandLine(Msg("Panels.Prompt"))
	if AppConfig.NavigationMode == NavigationSearchFirst {
		pf.cmdLine.SetFocus(false)
	}
	pf.cmdLine.Edit.HistoryID = "cmdline"
	if vtui.GlobalHistoryProvider != nil {
		pf.cmdLine.Edit.History = vtui.GlobalHistoryProvider.LoadHistory("cmdline")
	}
	pf.keyBar = vtui.NewKeyBar()
	pf.keyBar.SetOwner(pf)

	pf.termView = NewTerminalView(80, 24)
	pf.termView.OnBusyChange = func(busy bool) {
		localShell := pf.localShellIsActive()
		if localShell {
			pf.noteLocalShellBusy(busy)
		}
		// Use PostTask to ensure state changes happen on the UI thread
		vtui.FrameManager.PostTask(func() {
			if busy {
				pf.executing = true
			} else {
				pf.shellPromptReady = true
				ignoredPrompt := pf.executing && pf.ignoreNextPrompt
				if ignoredPrompt {
					// A command can be entered before the initial prompt has
					// finished crossing ConPTY. That prompt belongs to shell
					// startup, not to the command just sent; consuming it as
					// completion would return to panels while a batch file is
					// still running.
					pf.ignoreNextPrompt = false
				}
				if pf.executing && !ignoredPrompt {
					pf.endExecution()
				}
			}
			if localShell && !busy {
				pf.catchUpProcessEnvironment(true)
			}
		})
	}
	if runtime.GOOS == "windows" {
		pf.cmdSession = newCmdShellSession(pf)
		pf.termView.OnShellMark = func(mark string, snap promptSnapshot) {
			if pf.localShellIsActive() {
				pf.cmdSession.handleMark(mark, snap)
			}
		}
		logWindowsReflowRemoved()
	}
	// Parser will be fully initialized in initPTY once pty is ready
	pf.initPTY()
	pf.termView.pty = pf.pty
	installPanelDropTarget(pf)

	return pf
}

func (pf *PanelsFrame) searchFirstMode() bool {
	return AppConfig.NavigationMode == NavigationSearchFirst
}

func isCommandFocusToggleKey(e *vtinput.InputEvent) bool {
	if e.VirtualKeyCode == vtinput.VK_OEM_3 {
		return true
	}
	// The gogpu backend currently has no KeyGrave -> VK mapping and emits
	// this physical key as a text-only event. Cover its English and Russian
	// layout output without treating arbitrary known virtual keys as toggles.
	return e.VirtualKeyCode == 0 && (e.Char == '`' || e.Char == 'ё')
}

// setCommandLineFocus changes the explicit input target used by search-first
// navigation. Classic and Vim modes intentionally retain their legacy focus
// model, where the active panel and command edit can both appear focused.
func (pf *PanelsFrame) setCommandLineFocus(focused bool) {
	if !pf.searchFirstMode() {
		return
	}
	pf.commandLineFocused = focused
	pf.cmdLine.SetFocus(focused)
	for i, panel := range pf.panels {
		if panel == nil {
			continue
		}
		panel.SetFocus(i == pf.activeIdx && !focused)
		if fsp, ok := panel.(*FileSystemPanel); ok {
			fsp.showInactiveCursor = i == pf.activeIdx && focused
		}
		if pf.altPanels[i] != nil {
			pf.altPanels[i].SetFocus(i == pf.activeIdx && !focused)
		}
	}
	vtui.FrameManager.Redraw()
}

// insertSelectedFileName is shared by the bindable Ctrl+Enter action and the
// frame-level safety net for a key event that bypasses hotkey dispatch. Keeping
// the operation here prevents a modified Enter from ever degrading into plain
// directory activation.
func (pf *PanelsFrame) insertSelectedFileName() bool {
	fsp := pf.getActivePanel()
	if fsp == nil {
		return false
	}
	name := fsp.GetSelectedName()
	if name == "" {
		return false
	}
	// Escape spaces and special characters for shell commands.
	if strings.ContainsAny(name, " &|;<>()$`\\\"'") {
		if runtime.GOOS == "windows" {
			if !strings.HasPrefix(name, "\"") {
				name = "\"" + name + "\""
			}
		} else if !strings.HasPrefix(name, "'") {
			name = "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
		}
	}
	txt := pf.cmdLine.Edit.GetText()
	if len(txt) > 0 && txt[len(txt)-1] != ' ' {
		pf.cmdLine.InsertString(" ")
	}
	pf.cmdLine.InsertString(name)
	return true
}

// applyNavigationMode resets transient focus when the setting is changed.
func (pf *PanelsFrame) applyNavigationMode() {
	pf.commandLineFocused = false
	if pf.searchFirstMode() {
		pf.cmdLine.SetFocus(false)
		pf.setCommandLineFocus(false)
		return
	}
	pf.cmdLine.SetFocus(true)
	for i, panel := range pf.panels {
		if panel != nil {
			panel.SetFocus(i == pf.activeIdx)
		}
		if fsp, ok := panel.(*FileSystemPanel); ok {
			fsp.showInactiveCursor = false
		}
	}
}
func isAIPanel(panel Panel) bool {
	if fsp, ok := panel.(*FileSystemPanel); ok && fsp != nil && fsp.vfs != nil {
		if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
			return tp.GetTitle() == "ai"
		}
	}
	return false
}

// leftMenu builds the custom side menu for the left panel. View and
// sort modes act on a fixed side through Cm commands, so they stay
// command-routed rather than generated from the action registry.
func (pf *PanelsFrame) leftMenu() vtui.MenuBarItem {
	if isAIPanel(pf.panels[0]) {
		return vtui.MenuBarItem{Label: "&" + Msg("Menu.Left"), SubItems: []vtui.MenuItem{
			{Text: "&1. " + Msg("Action.AI.ViewContext"), Command: CmLeftAIContext, Shortcut: "Ctrl+1"},
			{Text: "&2. " + Msg("Action.AI.ViewChat"), Command: CmLeftAIChat, Shortcut: "Ctrl+2"},
			{Text: "&3. " + Msg("Action.AI.ViewOut"), Command: CmLeftAIOut, Shortcut: "Ctrl+3"},
			{Text: "&4. " + Msg("Action.AI.ViewMem"), Command: CmLeftAIMem, Shortcut: "Ctrl+4"},
			{Separator: true},
			{Text: Msg("Menu.Left.DriveMenu"), Command: CmLeftDriveMenu, Shortcut: "Alt+F1"},
			{Separator: true},
			{Text: Msg("FileOp.BtnBackground"), Command: CmBackground},
			{Text: Msg("Action.Workspace.New"), Command: CmWorkspaceNew, Shortcut: "Ctrl+N"},
			{Text: Msg("Action.Workspace.Close"), Command: CmWorkspaceClose, Shortcut: "Ctrl+W"},
			{Text: Msg("Menu.Exit"), Command: vtui.CmQuit},
		}}
	}
	return vtui.MenuBarItem{Label: "&" + Msg("Menu.Left"), SubItems: []vtui.MenuItem{
		{Text: "&" + Msg("Menu.Left.Brief"), Command: CmLeftBrief},
		{Text: "&" + Msg("Menu.Left.Medium"), Command: CmLeftMedium},
		{Text: "&" + Msg("Menu.Left.Detailed"), Command: CmLeftDetailed},
		{Text: "&" + Msg("Menu.Left.Wide"), Command: CmLeftWide},
		{Separator: true},
		{Text: "&" + Msg("Menu.SortName"), Command: CmLeftSortName},
		{Text: "&" + Msg("Menu.SortExt"), Command: CmLeftSortExt},
		{Text: "&" + Msg("Menu.SortTime"), Command: CmLeftSortTime},
		{Text: "&" + Msg("Menu.SortSize"), Command: CmLeftSortSize},
		{Text: "&" + Msg("Menu.SortUnsorted"), Command: CmLeftSortUnsorted},
		{Separator: true},
		{Text: Msg("Menu.Left.DriveMenu"), Command: CmLeftDriveMenu, Shortcut: "Alt+F1"},
		{Separator: true},
		{Text: Msg("FileOp.BtnBackground"), Command: CmBackground},
		{Text: Msg("Action.Workspace.New"), Command: CmWorkspaceNew, Shortcut: "Ctrl+N"},
		{Text: Msg("Action.Workspace.Close"), Command: CmWorkspaceClose, Shortcut: "Ctrl+W"},
		{Text: Msg("Menu.Exit"), Command: vtui.CmQuit},
	}}
}

// rightMenu builds the custom side menu for the right panel.
func (pf *PanelsFrame) rightMenu() vtui.MenuBarItem {
	if isAIPanel(pf.panels[1]) {
		return vtui.MenuBarItem{Label: "&" + Msg("Menu.Right"), SubItems: []vtui.MenuItem{
			{Text: "&1. " + Msg("Action.AI.ViewContext"), Command: CmRightAIContext, Shortcut: "Ctrl+1"},
			{Text: "&2. " + Msg("Action.AI.ViewChat"), Command: CmRightAIChat, Shortcut: "Ctrl+2"},
			{Text: "&3. " + Msg("Action.AI.ViewOut"), Command: CmRightAIOut, Shortcut: "Ctrl+3"},
			{Text: "&4. " + Msg("Action.AI.ViewMem"), Command: CmRightAIMem, Shortcut: "Ctrl+4"},
			{Separator: true},
			{Text: Msg("Menu.Right.DriveMenu"), Command: CmRightDriveMenu, Shortcut: "Alt+F2"},
		}}
	}
	return vtui.MenuBarItem{Label: "&" + Msg("Menu.Right"), SubItems: []vtui.MenuItem{
		{Text: "&" + Msg("Menu.Left.Brief"), Command: CmRightBrief},
		{Text: "&" + Msg("Menu.Left.Medium"), Command: CmRightMedium},
		{Text: "&" + Msg("Menu.Left.Detailed"), Command: CmRightDetailed},
		{Text: "&" + Msg("Menu.Left.Wide"), Command: CmRightWide},
		{Separator: true},
		{Text: "&" + Msg("Menu.SortName"), Command: CmRightSortName},
		{Text: "&" + Msg("Menu.SortExt"), Command: CmRightSortExt},
		{Text: "&" + Msg("Menu.SortTime"), Command: CmRightSortTime},
		{Text: "&" + Msg("Menu.SortSize"), Command: CmRightSortSize},
		{Text: "&" + Msg("Menu.SortUnsorted"), Command: CmRightSortUnsorted},
		{Separator: true},
		{Text: Msg("Menu.Right.DriveMenu"), Command: CmRightDriveMenu, Shortcut: "Alt+F2"},
	}}
}

// appendTerminalMenuItems keeps terminal-specific log commands reachable from
// the ordinary Files menu while panels are hidden. The terminal actions use
// their Terminal-area shortcuts, so this changes only menu presentation.
func appendTerminalMenuItems(items []vtui.MenuBarItem) []vtui.MenuBarItem {
	terminalItems := BuildMenuBarItems("Terminal")
	if len(terminalItems) == 0 || len(terminalItems[0].SubItems) == 0 {
		return items
	}

	filesLabel := Msg("Menu.Shell.Files")
	for i := range items {
		if items[i].Label != filesLabel {
			continue
		}
		if len(items[i].SubItems) > 0 {
			items[i].SubItems = append(items[i].SubItems, vtui.MenuItem{Separator: true})
		}
		items[i].SubItems = append(items[i].SubItems, terminalItems[0].SubItems...)
		break
	}
	return items
}

// buildMenuItems assembles the main menu: the custom Left/Right panel
// menus around the Files/Commands/Options menus generated from the
// action registry. With panels hidden, the ordinary Shell menu remains
// available so Options and panel actions do not disappear behind the
// terminal-log menu.
func (pf *PanelsFrame) buildMenuItems() []vtui.MenuBarItem {
	if !pf.showPanels {
		return appendTerminalMenuItems(BuildMenuBarItems("Shell"))
	}
	items := []vtui.MenuBarItem{pf.leftMenu()}
	items = append(items, BuildMenuBarItems("Shell")...)
	return append(items, pf.rightMenu())
}

// GetMenuBar returns the main menu bar. Items are rebuilt on every
// call, so shortcuts and checkmarks always follow the active bindings
// and the current panel state.
func (pf *PanelsFrame) GetMenuBar() *vtui.MenuBar {
	pf.menuBar.Items = pf.buildMenuItems()
	pf.updateMenuCheckmarks()
	return pf.menuBar
}

func getMenuText(current, target ViewMode, label string) string {
	if current == target {
		return "√" + label
	}
	return " " + label
}

func getSortMenuText(current, target SortMode, label string) string {
	if current == target {
		return "√" + label
	}
	return " " + label
}

var commandToActionName = map[int]string{
	CmLeftBrief:             "Panel.Left.ViewBrief",
	CmLeftMedium:            "Panel.Left.ViewMedium",
	CmLeftDetailed:          "Panel.Left.ViewDetailed",
	CmLeftWide:              "Panel.Left.ViewWide",
	CmRightBrief:            "Panel.Right.ViewBrief",
	CmRightMedium:           "Panel.Right.ViewMedium",
	CmRightDetailed:         "Panel.Right.ViewDetailed",
	CmRightWide:             "Panel.Right.ViewWide",
	CmLeftSortName:          "Panel.Left.SortByName",
	CmLeftSortExt:           "Panel.Left.SortByExt",
	CmLeftSortTime:          "Panel.Left.SortByTime",
	CmLeftSortSize:          "Panel.Left.SortBySize",
	CmLeftSortUnsorted:      "Panel.Left.SortUnsorted",
	CmRightSortName:         "Panel.Right.SortByName",
	CmRightSortExt:          "Panel.Right.SortByExt",
	CmRightSortTime:         "Panel.Right.SortByTime",
	CmRightSortSize:         "Panel.Right.SortBySize",
	CmRightSortUnsorted:     "Panel.Right.SortUnsorted",
	CmLeftAIContext:         "AI.Left.ViewContext",
	CmLeftAIChat:            "AI.Left.ViewChat",
	CmLeftAIOut:             "AI.Left.ViewOut",
	CmLeftAIMem:             "AI.Left.ViewMem",
	CmRightAIContext:        "AI.Right.ViewContext",
	CmRightAIChat:           "AI.Right.ViewChat",
	CmRightAIOut:            "AI.Right.ViewOut",
	CmRightAIMem:            "AI.Right.ViewMem",
	CmBackground:            "App.Background",
	CmWorkspaceNew:          "Workspace.New",
	CmWorkspaceClose:        "Workspace.Close",
	CmLeftDriveMenu:         "Panel.LeftDriveMenu",
	CmRightDriveMenu:        "Panel.RightDriveMenu",
	vtui.CmQuit:             "App.Quit",
	CmView:                  "File.View",
	CmEdit:                  "File.Edit",
	CmCopy:                  "File.Copy",
	CmMove:                  "File.Move",
	CmMkDir:                 "File.MakeDir",
	CmDelete:                "File.Delete",
	CmFindFile:              "File.Find",
	CmBookmarks:             "Panel.Bookmarks",
	CmPanelSettings:         "Settings.Panel",
	CmEditorSettings:        "Settings.Editor",
	CmColorerSettings:       "Settings.Colorer",
	CmAppearanceSettings:    "Settings.Appearance",
	CmConfirmationsSettings: "Settings.Confirmations",
	CmLanguage:              "Settings.Language",
	CmHelpLanguage:          "Settings.HelpLanguage",
	CmPlugins:               "Settings.Plugins",
}

// Fixed-side menu commands intentionally have exact action IDs above so every
// user-invokable item is represented in the registry. Their shortcut column,
// however, keeps showing the active-panel bindings used by Ctrl+1..4 and
// Ctrl+F3..F7; the fixed-side actions themselves do not claim extra keys.
var commandShortcutActionName = map[int]string{
	CmLeftBrief:         "Panel.ViewBrief",
	CmLeftMedium:        "Panel.ViewMedium",
	CmLeftDetailed:      "Panel.ViewDetailed",
	CmLeftWide:          "Panel.ViewWide",
	CmRightBrief:        "Panel.ViewBrief",
	CmRightMedium:       "Panel.ViewMedium",
	CmRightDetailed:     "Panel.ViewDetailed",
	CmRightWide:         "Panel.ViewWide",
	CmLeftSortName:      "Panel.SortByName",
	CmLeftSortExt:       "Panel.SortByExt",
	CmLeftSortTime:      "Panel.SortByTime",
	CmLeftSortSize:      "Panel.SortBySize",
	CmLeftSortUnsorted:  "Panel.SortUnsorted",
	CmRightSortName:     "Panel.SortByName",
	CmRightSortExt:      "Panel.SortByExt",
	CmRightSortTime:     "Panel.SortByTime",
	CmRightSortSize:     "Panel.SortBySize",
	CmRightSortUnsorted: "Panel.SortUnsorted",
}

func (pf *PanelsFrame) updateMenuCheckmarks() {
	if pf.panels[0] == nil || pf.panels[1] == nil || pf.menuBar == nil || len(pf.menuBar.Items) < 5 {
		return
	}
	if len(pf.menuBar.Items[0].SubItems) < 10 || len(pf.menuBar.Items[4].SubItems) < 10 {
		return
	}

	lMode, rMode := ViewModeMedium, ViewModeMedium
	lSort, rSort := SortName, SortName
	if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
		lMode = fsp.viewMode
		lSort = fsp.sortMode
	}
	if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
		rMode = fsp.viewMode
		rSort = fsp.sortMode
	}

	if pf.wide && pf.widePanel == 0 {
		lMode = ViewModeWide
	}
	if pf.wide && pf.widePanel == 1 {
		rMode = ViewModeWide
	}
	modeItems := []struct {
		mode ViewMode
		key  string
	}{{ViewModeBrief, "Brief"}, {ViewModeMedium, "Medium"}, {ViewModeDetailed, "Detailed"}, {ViewModeWide, "Wide"}}
	for i, item := range modeItems {
		pf.menuBar.Items[0].SubItems[i].Text = getMenuText(lMode, item.mode, "&"+Msg("Menu.Left."+item.key))
		pf.menuBar.Items[4].SubItems[i].Text = getMenuText(rMode, item.mode, "&"+Msg("Menu.Left."+item.key))
	}
	for i, item := range []struct {
		mode SortMode
		key  string
	}{{SortName, "SortName"}, {SortExt, "SortExt"}, {SortTime, "SortTime"}, {SortSize, "SortSize"}, {SortUnsorted, "SortUnsorted"}} {
		pf.menuBar.Items[0].SubItems[i+5].Text = getSortMenuText(lSort, item.mode, "&"+Msg("Menu."+item.key))
		pf.menuBar.Items[4].SubItems[i+5].Text = getSortMenuText(rSort, item.mode, "&"+Msg("Menu."+item.key))
	}

	// Update shortcuts dynamically from the action registry. Framework-owned
	// native keys are intentionally absent from HotkeyManager defaults, but
	// custom side menus must still advertise them (issue #651).
	if hm := GlobalHotkeysMgr; hm != nil {
		area := "Shell"
		for i := range pf.menuBar.Items {
			for j := range pf.menuBar.Items[i].SubItems {
				sub := &pf.menuBar.Items[i].SubItems[j]
				if actName, ok := commandToActionName[sub.Command]; ok {
					if shortcutAction, exists := commandShortcutActionName[sub.Command]; exists {
						actName = shortcutAction
					}
					sub.Shortcut = MenuShortcutsForAction(area, actName)
				}
			}
		}
	}
}

// osHostname is os.Hostname behind a seam so prompt tests can pin a value:
// real hostnames range from "mac" to a CI runner's 60-character UUID soup.
var osHostname = os.Hostname

func (pf *PanelsFrame) buildPrompt() []vtui.CharInfo {
	var path string
	var vfsTitle string
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		path = fsp.persistentPath()
		if fsp.providerOpenTask != nil {
			if colon := strings.IndexByte(path, ':'); colon > 0 {
				vfsTitle = path[:colon]
			}
		} else if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
			vfsTitle = tp.GetTitle()
		}
	}

	usr, _ := user.Current()
	username := "user"
	home := ""
	if usr != nil {
		username = usr.Username
		// On Windows, username often contains host or domain (e.g. "HOST\User")
		if idx := strings.LastIndex(username, "\\"); idx != -1 {
			username = username[idx+1:]
		}
		home = usr.HomeDir
	}

	host, _ := osHostname()
	if host == "" {
		host = "localhost"
	}

	userHostStr := username + "@" + host
	if vfsTitle != "" {
		userHostStr = vfsTitle
		home = "" // Do not use local home dir replacement for remote paths
	}

	displayPath := path
	if home != "" && strings.HasPrefix(displayPath, home) {
		displayPath = "~" + displayPath[len(home):]
	}

	sepStr := ":"
	suffixStr := "$ "

	if runtime.GOOS == "windows" {
		sepStr = " "
		suffixStr = ">"
		// Windows prompt usually displays the absolute path without '~'
		displayPath = path
	}
	// Some virtual filesystems expose a complete visual path whose root already
	// contains their title (for example "Account:\\Folder"). Keep the coloured
	// title prefix, but do not duplicate it before the path.
	if vfsTitle != "" && strings.HasPrefix(displayPath, vfsTitle+":") {
		displayPath = strings.TrimPrefix(displayPath, vfsTitle)
		sepStr = ""
	}

	maxPromptLen := pf.lastW / 2
	if maxPromptLen < 30 {
		maxPromptLen = pf.lastW - 15
	}
	if maxPromptLen < 15 {
		maxPromptLen = 15
	}

	// The user@host prefix gets at most half the budget: a long hostname
	// (CI runners, corporate DHCP names) otherwise pushes the prompt past
	// maxPromptLen no matter how hard the path is truncated.
	if maxUserHost := maxPromptLen / 2; runewidth.StringWidth(userHostStr) > maxUserHost {
		userHostStr = vtui.TruncateMiddle(userHostStr, maxUserHost)
	}

	maxPathLen := maxPromptLen - runewidth.StringWidth(userHostStr) - runewidth.StringWidth(sepStr) - runewidth.StringWidth(suffixStr)
	if maxPathLen < 10 {
		maxPathLen = 10
	}

	if runewidth.StringWidth(displayPath) > maxPathLen {
		displayPath = vtui.TruncateMiddle(displayPath, maxPathLen)
	}

	if pf.searchFirstMode() && pf.showPanels && !pf.commandLineFocused {
		plainPrompt := userHostStr + sepStr + displayPath + suffixStr
		return vtui.StringToCharInfo(plainPrompt, vtui.Palette[ColCommandLineInactivePrompt])
	}

	baseAttr := vtui.Palette[ColCommandLinePrompt]
	// Only the user@host part gets a colour of its own, the way bash shows it.
	// Everything else stays on CommandLine.Prefix so the prompt follows the
	// active theme instead of a hardcoded blue and white.
	greenAttr := vtui.SetRGBFore(baseAttr, 0x8AE234)

	var prompt []vtui.CharInfo
	prompt = append(prompt, vtui.StringToCharInfo(userHostStr, greenAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(sepStr, baseAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(displayPath, baseAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(suffixStr, baseAttr)...)

	return prompt
}

// localPTY reads the local PTY under the mutex initPTY publishes it with.
// The read has to be locked and not merely nil checked: an interface value
// is two words wide, and a racing reader can see the type word of a *PTY
// with the data word still zero. Such a value passes an "!= nil" guard and
// then calls the method on a nil receiver, which is how F10 pressed in the
// first milliseconds of a session used to crash inside PTY.Close while the
// shell was still being spawned.
func (pf *PanelsFrame) localPTY() PtyBackend {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	return pf.pty
}

// takeLocalPTY hands the local PTY to the caller and clears the field, so a
// shutdown path owns it outright: whoever gets it closes it, and a second
// path finds nothing left to close twice.
func (pf *PanelsFrame) takeLocalPTY() PtyBackend {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	pty := pf.pty
	pf.pty = nil
	return pty
}

// spawnLocalShellPTY gates initPTY's fork of the user's shell. Tests turn
// it off in TestMain: 185 frames spawned per test run each forked a real
// shell, and the leaked ptys exhausted macOS's ptmx_max (511), killing
// unrelated PTY tests with ENXIO ("device not configured").
var spawnLocalShellPTY = true

// newLocalPTY is a seam for the session lifecycle tests. Production always
// uses the platform PTY implementation; tests can provide a controllable
// backend without allocating a real terminal.
var newLocalPTY = func() (PtyBackend, error) {
	return NewPTY()
}

// resetLocalShell tears down the current local shell and starts a fresh one.
// The shell itself is the persistent session used by the command line; reset
// is deliberately explicit so a plain `exit` can clear all shell-local state
// without asking the parent process that launched f4 to do anything.
func (pf *PanelsFrame) resetLocalShell() bool {
	return pf.restartLocalShell(false)
}

// localShellGone is what the read loop reports when the local shell's output
// ends: the shell process has exited on its own. On Windows that is what
// `exit` inside a batch file does -- cmd runs batch files in-process, so the
// exit ends cmd itself, not just the batch (issue #409; `exit /b` ends only
// the batch). Nothing can come back from that shell: no prompt to end the
// command, no process for Ctrl+C or Ctrl+Break to reach. So the execution is
// ended here, the panels come back, and a fresh shell is started with the
// screen left as the old one left it, so the batch's output stays readable.
//
// Runs on the UI goroutine. A shell that was replaced deliberately
// (resetLocalShell) or taken by shutdown is no longer the local PTY by the
// time its read loop ends, and is left alone.
func (pf *PanelsFrame) localShellGone(p PtyBackend) {
	if !pf.isLocalPTY(p) {
		return
	}
	vtui.DebugLog("PTY: local shell exited on its own; ending the command and starting a fresh shell")
	// Whatever was queued behind the running command belongs to the shell
	// that just died; it is not replayed into the fresh one.
	pf.afterExecution = nil
	if pf.executing {
		pf.endExecution()
	}
	pf.restartLocalShell(true)
}

// restartLocalShell replaces the local shell. keepScreen leaves the terminal
// contents in place (with the cursor moved to a fresh line) instead of
// clearing them; a typed `exit` wants a clean screen, a shell that died under
// a batch file wants the batch's output kept.
func (pf *PanelsFrame) restartLocalShell(keepScreen bool) bool {
	pf.processEnvironmentWriteMu.Lock()
	pty := pf.localPTY()
	if pty == nil {
		pf.processEnvironmentWriteMu.Unlock()
		return false
	}

	pf.ptyMutex.Lock()
	if pf.pty != pty {
		pf.ptyMutex.Unlock()
		pf.processEnvironmentWriteMu.Unlock()
		return false
	}
	pf.pty = nil
	pf.ptyMutex.Unlock()
	pf.processEnvironmentWriteMu.Unlock()

	// An environment update may have been serialized for the old shell. Put it
	// back in the pending queue so the new shell receives it, and release the
	// old transport's temporary files immediately instead of waiting for the
	// acknowledgement timeout.
	pf.processEnvironmentMu.Lock()
	inFlight := pf.processEnvironmentInFlight
	pf.processEnvironmentInFlight = nil
	if inFlight != nil {
		if inFlight.timeout != nil {
			inFlight.timeout.Stop()
		}
		pf.pendingProcessEnvironment = coalesceProcessEnvironmentChanges(append(
			cloneProcessEnvironmentChanges(inFlight.changes),
			pf.pendingProcessEnvironment...,
		))
		if inFlight.generation > pf.pendingProcessEnvironmentGeneration {
			pf.pendingProcessEnvironmentGeneration = inFlight.generation
		}
	}
	pf.processEnvironmentBusy = false
	pf.processEnvironmentDeliveryFailed = false
	pf.processEnvironmentOutputTail = nil
	pf.deferredProcessEnvironmentInput = nil
	pf.processEnvironmentMu.Unlock()
	if inFlight != nil && inFlight.cleanup != nil {
		inFlight.cleanup()
	}
	if inFlight != nil && inFlight.muted && pf.termView != nil {
		pf.termView.SetMuted(false)
	}

	_ = pty.Close()

	pf.executing = false
	pf.afterExecution = nil
	pf.termView.ResetKeyboardProtocols()
	pf.shellPromptReady = false
	pf.ignoreNextPrompt = false
	pf.returnToPanels = false
	pf.workspaceCommandTitle = ""
	pf.lastPtyPath = ""
	if pf.cmdSession != nil {
		pf.cmdSession.close()
		pf.cmdSession = newCmdShellSession(pf)
	}
	pf.lastPtyVFS = nil
	if pf.termView != nil {
		pf.termView.SetMuted(false)
		pf.termView.pty = nil
		if keepScreen {
			if pf.termView.UseAltScreen {
				pf.termView.ResetBuffer(pf.termView.Width, pf.termView.Height)
			} else if pf.termView.CursorX != 0 {
				parser := NewAnsiParser(pf.termView, nil)
				parser.Process([]byte("\r\n"))
			}
		} else {
			pf.termView.ResetBuffer(pf.termView.Width, pf.termView.Height)
		}
	}
	pf.parser = NewAnsiParser(pf.termView, nil)
	pf.parser.replyTo = pf.activeReplyPTY
	pf.initPTY()
	return true
}

func (pf *PanelsFrame) initPTY() {
	// Always initialize the parser to prevent nil dereference
	pf.parser = NewAnsiParser(pf.termView, nil)
	pf.parser.replyTo = pf.activeReplyPTY

	if !spawnLocalShellPTY {
		return
	}

	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	uiFrames := vtui.FrameManager
	go func() {
		pf.ptyMutex.Lock()
		if pf.closed {
			pf.ptyMutex.Unlock()
			return
		}
		p := pf.pty
		pf.ptyMutex.Unlock()

		if p == nil {
			var err error
			p, err = newLocalPTY()
			if err != nil {
				vtui.DebugLog("PTY: Failed to allocate local PTY: %v", err)
				logPTYDiagnostics()
				pf.reportLocalPTYFailure(err)
				return
			}

			if runtime.GOOS == "windows" {
				os.Setenv("PROMPT", windowsShellPrompt)
			}
			inheritedEnvironmentGeneration := globalProcessEnvironment.currentGeneration()

			shell := GetSystemShell()
			if err := p.Run(shell); err != nil {
				vtui.DebugLog("PTY: Failed to run shell: %v", err)
				p.Close()
				return
			}

			pf.ptyMutex.Lock()
			if pf.closed {
				pf.ptyMutex.Unlock()
				p.Close()
				return
			}
			pf.pty = p
			serializedPTY := &processEnvironmentSerializedPTY{owner: pf, backend: p}
			if pf.shellMode == ShellModeHost {
				muted := mutedPTY{backend: serializedPTY}
				pf.parser.pty = muted
				pf.termView.pty = muted
			} else {
				pf.parser.pty = serializedPTY
				pf.termView.pty = serializedPTY
			}
			pf.ptyMutex.Unlock()
			pf.localShellStarted(inheritedEnvironmentGeneration)

			uiFrames.PostTask(func() {
				pf.ResizeConsole(pf.lastW, pf.lastH)
				pf.RefreshAll()
				uiFrames.Redraw()
			})
		}

		// Local PTY has its own dedicated read loop.
		buf := make([]byte, 32768)
		for {
			n, err := p.Read(buf)
			if err != nil {
				vtui.DebugLog("PTY: Local read loop exited: %v", err)
				// A shell that is gone cannot print the prompt that would
				// end the command, and cannot take another one either.
				uiFrames.PostTask(func() { pf.localShellGone(p) })
				return
			}
			pf.consumeLocalOutput(p, buf[:n])
		}
	}()
}

// consumeLocalOutput routes one read from the local PTY: to the reflow
// oracle's scratch parser while one of its passes is in flight, otherwise to
// the display's parser (and the host console in passthrough mode). Tests
// drive it directly with a fake PTY, so it must contain everything the read
// loop does with the bytes.
func (pf *PanelsFrame) consumeLocalOutput(p PtyBackend, data []byte) {
	pf.processEnvironmentShellOutput(data)

	pf.ptyMutex.Lock()
	shouldProcess := (pf.getActivePTYUnsafe() == p)
	pf.ptyMutex.Unlock()

	pf.displayLocalOutput(shouldProcess, data)
}

// displayLocalOutput is what consumeLocalOutput does with bytes meant for
// the display; split out so route can hand them over a piece at a time.
func (pf *PanelsFrame) displayLocalOutput(shouldProcess bool, data []byte) {
	if !shouldProcess {
		return
	}
	if pf.shellMode == ShellModeHost && pf.isHostConsoleActive() {
		vtui.WritePassthrough(data)
		pf.parser.Process(data)
		if pf.overlayLines() > 0 && time.Since(pf.lastOverlayDraw) > 30*time.Millisecond {
			pf.drawHostConsoleOverlay()
			pf.lastOverlayDraw = time.Now()
		}
		return
	}
	pf.parser.Process(data)
	pf.terminalRedraw.request()
}

// reportLocalPTYFailure surfaces a NewPTY() failure to the person instead of
// leaving it only in the debug log. Without this, a platform where PTY
// allocation fails (see issue #444, FreeBSD and illumos before their
// backends were fixed) looked identical to a healthy f4 whose terminal
// silently ignores every keystroke: panels, menus and the viewer all work,
// because none of them touch the PTY, so the only visible symptom was an
// empty terminal and no error anywhere the person could see without
// starting f4 with --debug.
func localPTYFailureMessage(err error) string {
	return fmt.Sprintf(Msg("Terminal.PTYAllocFailed"), err)
}

func (pf *PanelsFrame) reportLocalPTYFailure(err error) {
	if vtui.FrameManager == nil || pf.shellMode == ShellModeSimpleInline || pf.shellMode == ShellModeSimpleCaptured {
		return
	}
	vtui.FrameManager.PostTask(func() {
		showToast(localPTYFailureMessage(err), 8*time.Second)
	})
}

// ConfirmClose prevents the last workspace containing file panels from being
// removed while editor/viewer workspaces remain. FrameManager asks this vetoer
// before every close path, including its native Ctrl+W fallback; without it,
// the final panel-less workspace could emit CmQuit without PanelsFrame's exit
// confirmation policy (issue #531).
func (pf *PanelsFrame) ConfirmClose() bool {
	if pf == nil || pf.closed || vtui.FrameManager == nil {
		return true
	}
	for _, screen := range vtui.FrameManager.Screens {
		if workspaceHasOpenPanels(screen) && workspaceContainsPanelsFrame(screen, pf) {
			return !isOnlyPanelsWorkspace(screen)
		}
	}
	return true
}

func workspaceContainsPanelsFrame(screen *vtui.AppScreen, target *PanelsFrame) bool {
	if screen == nil || target == nil {
		return false
	}
	for _, frame := range screen.Frames {
		if frame == target {
			return true
		}
	}
	return false
}

func (pf *PanelsFrame) Close() {
	if pf.terminalRedraw != nil {
		pf.terminalRedraw.stop()
	}
	pf.cmdSession.close()
	if pf.shellMode == ShellModeHost && pf.isHostConsoleActive() {
		pf.leaveHostConsole()
	}
	if pf.wide && pf.widePanel >= 0 && pf.widePanel < 2 {
		if isAIPanel(pf.panels[pf.widePanel]) {
			pf.exitWide()
		}
	}
	pf.closeProcessEnvironmentShell()

	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	pf.closed = true

	for _, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok && fsp != nil {
			fsp.cancelProviderOpen()
			if fsp.cancelLoad != nil {
				fsp.cancelLoad()
			}
			fsp.stopLoadingAnimation()
		}
	}
	for i, alt := range pf.altPanels {
		if closer, ok := alt.(interface{ Close() }); ok {
			closer.Close()
		}
		pf.altPanels[i] = nil
	}

	if pf.pty != nil {
		pf.pty.Close()
		pf.pty = nil
	}
	for _, pty := range pf.remotePtys {
		pty.Close()
	}
	pf.remotePtys = nil
	pf.BaseFrame.Close()
}

func (pf *PanelsFrame) setWidePanel(idx int) {
	if idx < 0 || idx > 1 {
		idx = -1
	}
	pf.widePanel = idx
	pf.wide = idx >= 0
	if idx >= 0 {
		pf.activeIdx = idx
		pf.showPanels = true
	}
	if pf.lastW > 0 && pf.lastH > 0 {
		pf.ResizeConsole(pf.lastW, pf.lastH)
	}
}

func (pf *PanelsFrame) exitWide() {
	if pf.wide {
		pf.setWidePanel(-1)
	}
}

func (pf *PanelsFrame) setPanelViewMode(idx int, mode ViewMode) {
	if idx < 0 || idx > 1 {
		return
	}
	pf.exitWide()
	if fsp, ok := pf.panels[idx].(*FileSystemPanel); ok {
		fsp.SetViewMode(mode)
	}
	pf.updateMenuCheckmarks()
}

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	pf.lastW, pf.lastH = w, h
	pf.SetPosition(0, 0, w-1, h-1) // Update hit-box for FrameManager hit-testing
	topInset := vtui.FrameManager.WorkspaceTopInset()
	pf.menuBar.SetPosition(0, topInset, w-1, topInset)

	contentY1 := topInset
	if AppConfig.AlwaysShowMenuBar && pf.showPanels {
		contentY1++
	}

	// 1. Terminal Area: Fills everything except KeyBar
	termY2 := h - 1
	if pf.shellMode == ShellModeHost {
		// The host console keeps its overlay rows *below* the mirrored grid,
		// so nothing of the child's output is ever painted over.
		pf.termView.SetPromptOverlaysLastRow(false)
		n := pf.overlayLines()
		termH := h - n
		if termH <= 0 {
			termH = 1
		}
		if pty := pf.localPTY(); pty != nil {
			// Resize the parser's grid before asking a PTY to emit its resize
			// frame. A fast ConPTY can otherwise deliver new-width absolute
			// coordinates while TerminalView still has the old width.
			pf.termView.SetPosition(0, 0, w-1, termH-1)
			pf.termView.Resize(w, termH)
			pf.ptyMutex.Lock()
			cw, ch := pf.termView.CellSize()
			{
				// The other half of every resize: what ConPTY was told. A frame
				// that later declares a different size (REFLOW_FRAME STALE) is
				// only explicable next to this line.
				vtui.DebugLog("REFLOW_PTY: outer %dx%d -> child %dx%d (cell %dx%d, host mode, overlay rows %d)", w, h, w, termH, cw, ch, n)
				setPtySize(pty, w, termH, cw, ch)
				for _, remotePty := range pf.remotePtys {
					setPtySize(remotePty, w, termH, cw, ch)
				}
			}
			pf.ptyMutex.Unlock()
		}
		if pf.isHostConsoleActive() && n > 0 {
			vtui.WritePassthrough([]byte(fmt.Sprintf("\x1b[1;%dr", termH)))
			pf.drawHostConsoleOverlay()
		}
	} else {
		// Keep the configured keybar row out of the own-terminal PTY even while
		// a command is running. The keybar and command line are hidden then, but
		// temporarily growing the PTY makes bash redraw its prompt on SIGWINCH.
		// The command line intentionally still overlaps the PTY's bottom row so
		// it paints over, rather than duplicates, the native shell prompt. The
		// view is told, because a command that ends without a newline would
		// otherwise leave its last output line on that hidden row (#863).
		pf.termView.SetPromptOverlaysLastRow(true)
		if pf.showKeyBar && !pf.termView.OnAltScreen() &&
			(pf.shellMode == ShellModeOwn || !pf.isPtyBusy()) {
			termY2 = h - 2
		}
		termH := termY2 - contentY1 + 1
		if termH < 0 {
			termH = 0
		}

		if pty := pf.localPTY(); pty != nil {
			pf.termView.SetPosition(0, contentY1, w-1, termY2)
			pf.termView.Resize(w, termH)
			pf.ptyMutex.Lock()
			cw, ch := pf.termView.CellSize()
			{
				vtui.DebugLog("REFLOW_PTY: outer %dx%d -> child %dx%d (cell %dx%d, rows %d..%d)", w, h, w, termH, cw, ch, contentY1, termY2)
				setPtySize(pty, w, termH, cw, ch)
				for _, remotePty := range pf.remotePtys {
					setPtySize(remotePty, w, termH, cw, ch)
				}
			}
			pf.ptyMutex.Unlock()
		}
	}

	// 2. Panel Area: Leaves one additional line for the f4 CommandLine.
	// leftHeightDecrement / rightHeightDecrement shrink the corresponding
	// panel from the bottom; the freed rows go to the terminal area above
	// (matches far2l's [Layout] section of the same name).
	basePanelY2 := h - 2
	if pf.showKeyBar {
		basePanelY2 = h - 3
	}
	maxHD := h - 7
	clampHD := func(hd int) int {
		if hd < 0 {
			return 0
		}
		if maxHD > 0 && hd > maxHD {
			return maxHD
		}
		return hd
	}
	leftPanelY2 := basePanelY2 - clampHD(pf.leftHeightDecrement)
	rightPanelY2 := basePanelY2 - clampHD(pf.rightHeightDecrement)
	// panelH is only used to seed the two panels on first construction;
	// after that each panel takes its own height from SetPosition below.
	panelH := basePanelY2 - contentY1 + 1
	if panelH < 0 {
		panelH = 0
	}

	// widthDecrement shifts the split between the two panels: positive
	// grows the right panel (as in far2l).
	wd := pf.widthDecrement
	if maxWD := (w / 2) - 10; maxWD > 0 {
		if wd > maxWD {
			wd = maxWD
		}
		if wd < -maxWD {
			wd = -maxWD
		}
	} else {
		wd = 0
	}
	leftW := w/2 - wd
	rightW := w - leftW
	panelX1 := [2]int{0, leftW}
	panelX2 := [2]int{leftW - 1, w - 1}
	// A hidden side is a display mode, not a request to leave a blank half.
	// Expand the remaining panel to the complete content width while keeping
	// the hidden slot's split geometry ready for the next toggle.
	if pf.showLeftPanel != pf.showRightPanel {
		visibleIdx := 0
		if !pf.showLeftPanel {
			visibleIdx = 1
		}
		panelX1[visibleIdx] = 0
		panelX2[visibleIdx] = w - 1
	}

	if pf.panels[0] == nil {
		pf.panels[0] = NewFileSystemPanel(0, contentY1, leftW, panelH, vfs.NewOSVFS("."))
		pf.panels[1] = NewFileSystemPanel(leftW, contentY1, rightW, panelH, vfs.NewOSVFS("."))
	}

	for i, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			fsp.wide = pf.wide && pf.widePanel == i
			fsp.configureCellSelection()
		}
	}

	if pf.wide {
		idx := pf.widePanel
		panelY2 := leftPanelY2
		if idx == 1 {
			panelY2 = rightPanelY2
		}
		pf.panels[idx].SetPosition(0, contentY1, w-1, panelY2)
		if fsp, ok := pf.panels[idx].(*FileSystemPanel); ok {
			fsp.Resize(w, panelY2-contentY1+1)
		}
	} else {
		for i, p := range pf.panels {
			panelY2Cur := leftPanelY2
			if i == 1 {
				panelY2Cur = rightPanelY2
			}
			p.SetPosition(panelX1[i], contentY1, panelX2[i], panelY2Cur)
			if fsp, ok := p.(*FileSystemPanel); ok {
				fsp.Resize(panelX2[i]-panelX1[i]+1, panelY2Cur-contentY1+1)
			}
		}
	}
	// Keep any active alt panels aligned with their host slot.
	if pf.wide {
		idx := pf.widePanel
		panelY2 := leftPanelY2
		if idx == 1 {
			panelY2 = rightPanelY2
		}
		if pf.altPanels[idx] != nil {
			pf.altPanels[idx].SetPosition(0, contentY1, w-1, panelY2)
		}
	} else {
		for i, alt := range pf.altPanels {
			if alt != nil {
				panelY2Cur := leftPanelY2
				if i == 1 {
					panelY2Cur = rightPanelY2
				}
				alt.SetPosition(panelX1[i], contentY1, panelX2[i], panelY2Cur)
			}
		}
	}

	cmdLineY := h - 1
	if pf.showKeyBar {
		// KeyBar on the last line
		pf.keyBar.SetPosition(0, h-1, w-1, h-1)
		pf.keyBar.SetVisible(true)
		cmdLineY = h - 2 // CommandLine is above KeyBar
	} else {
		pf.keyBar.SetVisible(false)
		// CommandLine takes the last line
	}
	// Set CommandLine's base position. Show() will override if in terminal prompt mode.
	pf.cmdLine.SetPosition(0, cmdLineY, w-1, cmdLineY)
	pf.updateMenuCheckmarks()
}

func (pf *PanelsFrame) isPtyBusy() bool {
	active := pf.getActivePTY()
	if active == nil {
		return false
	}
	if active.IsBusy() {
		return true
	}
	// Managed execution signal from OSC 133
	return pf.executing
}

// beginManagedExecution marks a command that carries its own OSC 133 C/D
// pair, wrapped around it by f4 itself. Its D marker is unambiguous: it is
// printed by the very command line we sent, so it always ends the execution.
func (pf *PanelsFrame) beginManagedExecution() {
	pf.executing = true
	pf.ignoreNextPrompt = false
}

// beginPromptDrivenExecution marks a command that carries no markers of its
// own — cmd.exe with the PROMPT f4 injects, or a VFS integration wiring the
// command for a remote peer. Completion is inferred from the next prompt
// marker, and a prompt printed at shell startup can still be in flight when
// the command is sent, so that first stale marker is discarded.
func (pf *PanelsFrame) beginPromptDrivenExecution() {
	pf.executing = true
	pf.ignoreNextPrompt = !pf.shellPromptReady
}

// endExecution is the single place where a finished command hands the
// screen back: to the panels if they were hidden for it, otherwise just
// out of the busy state.
func (pf *PanelsFrame) endExecution() {
	pf.executing = false
	pf.workspaceCommandTitle = ""
	if pf.returnToPanels {
		pf.showPanels = true
		if !pf.showLeftPanel && !pf.showRightPanel {
			pf.showLeftPanel = true
			pf.showRightPanel = true
		}
		pf.returnToPanels = false
		if pf.shellMode == ShellModeHost {
			pf.leaveHostConsole()
		}
		pf.RefreshAll()
		vtui.FrameManager.Redraw()
	}
	if next := pf.afterExecution; next != nil {
		pf.afterExecution = nil
		next()
	}
}

// logWindowsReflowRemoved says once per session that the Windows reflow
// modes are gone, and why, so a field log and the probe's mode check both
// read something truthful instead of nothing.
func logWindowsReflowRemoved() {
	vtui.DebugLog("REFLOW: F4_WIN_REFLOW=off (the Windows reflow modes were removed; ConPTY owns the viewport and the history is kept as written -- docs/CONPTY_RESEARCH.md section 7)")
}

// noteLocalShellLineSent tells the cmd session that a line was typed into
// the local shell, so that only a prompt printed after it can end it.
func (pf *PanelsFrame) noteLocalShellLineSent(pty PtyBackend) {
	if pf.cmdSession != nil && pf.isLocalPTY(pty) {
		pf.cmdSession.noteSent()
	}
}

func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	if pf.shellMode == ShellModeHost && pf.isHostConsoleActive() {
		return
	}
	isBusy := pf.isPtyBusy()

	// 1. Dynamic Layout Adjustment
	if pf.termView.OnAltScreen() != pf.lastAlt || isBusy != pf.lastBusy || pf.showPanels != pf.lastShowPanels {
		pf.lastAlt = pf.termView.OnAltScreen()
		pf.lastBusy = isBusy
		pf.lastShowPanels = pf.showPanels
		pf.ResizeConsole(pf.lastW, pf.lastH)
	}

	if !isBusy && pf.cmdSession.idle() {
		if fsp := pf.getActivePanel(); fsp != nil {
			currentPath := fsp.vfs.GetPath()
			if currentPath != pf.lastPtyPath || !sameVFSInstance(fsp.vfs, pf.lastPtyVFS) {
				if pf.syncPTYDirectory(currentPath, fsp.vfs) {
					pf.lastPtyPath = currentPath
					pf.lastPtyVFS = fsp.vfs
				}
			}
		}
	}

	now := time.Now()
	if pf.showPanels && now.Sub(pf.lastAutoRefresh) > 2*time.Second {
		pf.lastAutoRefresh = now
		for _, p := range pf.panels {
			if fsp, ok := p.(*FileSystemPanel); ok && !fsp.isLoading && !fsp.isCheckingRefresh {
				fsp.isCheckingRefresh = true
				vfsPath := fsp.vfs.GetPath()
				vfsInst := fsp.vfs
				lastKnown := fsp.lastDirMTime
				vtui.RunAsync(func(ctx *vtui.TaskContext) {
					stat, err := vfsInst.Stat(ctx.Context, vfsPath)
					ctx.RunOnUI(func() {
						fsp.isCheckingRefresh = false
						if err == nil && !stat.MTime.IsZero() {
							if !fsp.isLoading && fsp.vfs.GetPath() == vfsPath {
								if !lastKnown.IsZero() && stat.MTime != lastKnown {
									vtui.DebugLog("PANELS: Auto-refreshing %q due to MTime change", vfsPath)
									fsp.ReadDirectory()
								} else if lastKnown.IsZero() {
									fsp.lastDirMTime = stat.MTime
								}
							}
						}
					})
				})
			}
		}
	}

	if pf.showPanels && pf.wide {
		hasTerminalArea := pf.leftHeightDecrement > 0
		if pf.widePanel == 1 {
			hasTerminalArea = pf.rightHeightDecrement > 0
		}
		pf.termView.SetVisible(hasTerminalArea)
		if hasTerminalArea {
			pf.termView.Show(scr)
		}
		idx := pf.widePanel
		pf.panels[idx].SetFocus(true)
		if pf.altPanels[idx] != nil {
			pf.altPanels[idx].SetFocus(true)
			pf.altPanels[idx].Show(scr)
		} else {
			pf.panels[idx].Show(scr)
		}
	} else if pf.showPanels {
		// Keep the terminal behind a single full-width panel, or let it show
		// through below a panel that was reduced vertically (Ctrl+Up).
		if !pf.showLeftPanel || !pf.showRightPanel || pf.leftHeightDecrement > 0 || pf.rightHeightDecrement > 0 {
			pf.termView.SetVisible(true)
			pf.termView.Show(scr)
		} else {
			pf.termView.SetVisible(false)
		}
		if pf.showLeftPanel {
			panelFocused := !pf.searchFirstMode() || !pf.commandLineFocused
			pf.panels[0].SetFocus(pf.activeIdx == 0 && panelFocused)
			if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
				fsp.showInactiveCursor = pf.searchFirstMode() && pf.commandLineFocused && pf.activeIdx == 0
			}
			if pf.altPanels[0] != nil {
				pf.altPanels[0].SetFocus(pf.activeIdx == 0 && panelFocused)
				pf.altPanels[0].Show(scr)
			} else {
				pf.panels[0].Show(scr)
			}
		}
		if pf.showRightPanel {
			panelFocused := !pf.searchFirstMode() || !pf.commandLineFocused
			pf.panels[1].SetFocus(pf.activeIdx == 1 && panelFocused)
			if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
				fsp.showInactiveCursor = pf.searchFirstMode() && pf.commandLineFocused && pf.activeIdx == 1
			}
			if pf.altPanels[1] != nil {
				pf.altPanels[1].SetFocus(pf.activeIdx == 1 && panelFocused)
				pf.altPanels[1].Show(scr)
			} else {
				pf.panels[1].Show(scr)
			}
		}
	} else {
		pf.termView.SetVisible(true)
		pf.termView.Show(scr)
	}

	if AppConfig.AlwaysShowMenuBar && pf.showPanels {
		pf.menuBar.SetVisible(true)
		pf.menuBar.Show(scr)
	}

	// Command line logic depends on terminal state and editor visibility
	topType := vtui.FrameManager.GetTopFrameType()
	isFastFind := false
	if fsp := pf.getActivePanel(); fsp != nil && fsp.fastFindMode {
		isFastFind = true
	}

	if (!pf.showPanels && (pf.termView.OnAltScreen() || isBusy)) || topType == vtui.TypeUser+2 {
		pf.cmdLine.SetVisible(false)
	} else {
		isChatFocused := false
		if pf.showPanels && pf.altPanels[pf.activeIdx] != nil && pf.altPanels[pf.activeIdx].Kind() == "ai_chat" && pf.altPanels[pf.activeIdx].IsFocused() {
			isChatFocused = true
		}
		pf.cmdLine.SetVisible(true)
		pf.cmdLine.Edit.HideCursor = isFastFind || isChatFocused || (pf.searchFirstMode() && pf.showPanels && !pf.commandLineFocused)
		cmdLineY := pf.lastH - 1
		if pf.showKeyBar {
			cmdLineY = pf.lastH - 2
		}
		pf.cmdLine.SetRichPrompt(pf.buildPrompt())
		pf.cmdLine.SetPosition(0, cmdLineY, pf.lastW-1, cmdLineY)
		pf.cmdLine.Show(scr)
	}

	// KeyBar is at the bottom. It should only be hidden if a child process
	// in the terminal is running or using the alternate screen buffer.
	isTop := vtui.FrameManager.GetTopFrameType() == vtui.TypeUser+1
	if isTop { // Only the top-most user frame controls the keybar
		if pf.showKeyBar && !pf.termView.OnAltScreen() && (pf.showPanels || !isBusy) {
			vtui.FrameManager.KeyBar = pf.keyBar
		} else {
			vtui.FrameManager.KeyBar = nil
		}
	}

}

// InterceptPluginKey lets global plugin hotkeys and the active panel's
// PanelController consume a key before built-in hotkey dispatch. It is
// called from MacroManager.Filter, so plugins keep their priority over
// the default bindings.
func (pf *PanelsFrame) InterceptPluginKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	// Arkanoid easter egg: Ctrl+Alt+A
	if e.VirtualKeyCode == 'A' && alt && ctrl {
		return actionArkanoid()
	}

	// Check global hotkeys (ignoring Lock and Enhanced keys)
	for _, hk := range globalHotkeysSnapshot() {
		hkCtrl := (hk.Mods & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		hkAlt := (hk.Mods & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		hkShift := (hk.Mods & vtinput.ShiftPressed) != 0

		if e.VirtualKeyCode == hk.VK && ctrl == hkCtrl && alt == hkAlt && shift == hkShift {
			hk.Handler(pf)
			return true
		}
	}

	// Panel Controller interception (allows plugins to override default keys)
	if pf.showPanels {
		if fsp := pf.getActivePanel(); fsp != nil {
			if pc, ok := fsp.vfs.(PanelController); ok {
				if pc.ProcessPanelKey(pf, e) {
					return true
				}
			}
		}
	}

	// A remote shell owns Ctrl+C after plugins and the active PanelController
	// have had their documented first chance. This filter still runs before
	// HotkeyManager, so Panel.SplitReset cannot shadow the interrupt fallback.
	if e.VirtualKeyCode == vtinput.VK_C && ctrl && !alt && !shift && pf.interruptRemotePTY(nil) {
		return true
	}
	return false
}

// VetoActionKey reports modal input states in which the panels must see
// the key before the global hotkey dispatcher. During fast find,
// printable characters and the gray selection keys belong to the
// panel's own search input.
// commandLineOwnsSelection reports the keys that belong to the command
// line's own text selection rather than to a panel action: Ctrl+Shift+
// Left/Right walk the selection word by word there exactly as they do in
// any other edit field, whether or not the panels are visible. They fall
// back to the drive menus (far2l's Alt+F1 / Alt+F2 aliases) only while
// the line is empty and there is nothing to select — the same trade the
// EmptyCommandLine condition already makes for plain Ctrl+Left/Right.
func (pf *PanelsFrame) commandLineOwnsSelection(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	if e.VirtualKeyCode != vtinput.VK_LEFT && e.VirtualKeyCode != vtinput.VK_RIGHT {
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	if !ctrl || !shift || alt {
		return false
	}
	return pf.cmdLine != nil && pf.cmdLine.IsVisible() && !pf.cmdLine.IsEmpty()
}

// VetoActionKey reports modal input states in which the panels must see
// the key before the global hotkey dispatcher. During fast find,
// printable characters and the gray selection keys belong to the
// panel's own search input.
func (pf *PanelsFrame) VetoActionKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	// An active AutoCompleteMenu must consume Esc, arrows, and Enter
	// before any global action (such as Esc:EscToggle) can intercept it.
	if vtui.FrameManager != nil {
		if _, isAc := vtui.FrameManager.GetTopFrame().(*vtui.AutoCompleteMenu); isAc {
			return true
		}
	}
	// Checked ahead of the panels-visible guard: the command line keeps
	// its selection keys in terminal mode too, where the drive menus are
	// bound in the Terminal area.
	if pf.commandLineOwnsSelection(e) {
		return true
	}
	if !pf.showPanels {
		return false
	}
	fsp := pf.getActivePanel()
	if fsp != nil && fsp.providerOpenTask != nil {
		ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
		if alt && !ctrl && !shift && (e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) {
			// History navigation is safe while a cache-first provider restore is
			// pending: it cancels that restore and uses persistentPath, not the
			// source VFS hidden beneath the cached rows.
			return false
		}
		// Cached rows from the destination are visible immediately, but the old
		// VFS remains installed until the asynchronous provider restore succeeds.
		// Route keys through the panel so file actions cannot accidentally target
		// the old filesystem. Cursor/workspace UI remains responsive; Esc cancels.
		return true
	}
	if fsp == nil || !fsp.fastFindMode {
		// A focused alt panel gets its own keys first: e.g. F2 toggles
		// wrap in quick view and must not fire Panel.UserMenu.
		if e.VirtualKeyCode == vtinput.VK_F2 && pf.activeIdx >= 0 && pf.activeIdx < len(pf.altPanels) {
			if a := pf.altPanels[pf.activeIdx]; a != nil && a.IsFocused() {
				return true
			}
		}
		if pf.activeIdx >= 0 && pf.activeIdx < len(pf.altPanels) {
			if a := pf.altPanels[pf.activeIdx]; a != nil && a.IsFocused() && a.Kind() == "ai_chat" {
				if e.Char != 0 || e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_BACK || e.VirtualKeyCode == vtinput.VK_DELETE {
					return true
				}
			}
			// The player takes WinAmp's letter keys (Z X C V B) and +/-
			// for volume, so plain characters must not go to the
			// command line while it has the cursor.
			if a := pf.altPanels[pf.activeIdx]; a != nil && a.IsFocused() && a.Kind() == "player" {
				if e.Char != 0 || e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_DELETE {
					return true
				}
			}
		}
		return false
	}
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	if e.Char != 0 && !ctrl && !alt {
		return true
	}
	// Fast Find owns plain Esc, plain Delete, plain F2 (search mode toggle) and
	// Ctrl+Enter; the filter must not turn them into Panel.Toggle,
	// Panel.UserMenu or Panel.InsertFileName.
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && !ctrl && !alt && !shift {
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift {
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_F2 && !ctrl && !alt && !shift {
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_RETURN && ctrl && !alt {
		return true
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_ADD, vtinput.VK_SUBTRACT, vtinput.VK_MULTIPLY:
		return true
	}
	return false
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	if pf.showPanels && e.KeyDown && alt && shift && !ctrl {
		if fsp := pf.getActivePanel(); fsp != nil {
			if temp, ok := fsp.vfs.(*TempPanelVFS); ok {
				if e.VirtualKeyCode == vtinput.VK_F3 {
					temp.showSelectedOnPassive(pf)
					return true
				}
				if slot, ok := tempPanelSlotKey(e); ok {
					return temp.switchToSlot(pf, fsp, slot)
				}
			}
		}
	}

	// A keyboard action starts a new terminal interaction. Clear the old
	// mouse highlight before forwarding the key to a shell or terminal app;
	// otherwise it remains painted until another mouse click.
	if !pf.showPanels && e.Type == vtinput.KeyEventType && e.KeyDown && pf.termView.HasSelection() {
		pf.termView.ClearSelection()
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}

	// Workspace switching is global and must remain reachable while a child
	// process owns the terminal. Returning false lets FrameManager handle both
	// directions instead of forwarding the key to an AltScreen application or
	// to a busy ordinary PTY such as the Python REPL.
	if e.Type == vtinput.KeyEventType && e.VirtualKeyCode == vtinput.VK_TAB && ctrl && !alt {
		return false
	}
	// Ctrl+N normally forks the active panels into a new workspace. Terminal
	// applications may use that key themselves, so this interception is a
	// default-on preference rather than an unconditional global shortcut.
	if e.Type == vtinput.KeyEventType && e.KeyDown && !pf.showPanels &&
		e.VirtualKeyCode == vtinput.VK_N && ctrl && !alt && !shift && AppConfig.TerminalCtrlNWorkspace {
		return false
	}

	// Raw input mode check at the very top. If an interactive AltScreen app is active (e.g. mc, htop),
	// we forward all non-global keys to PTY.
	if !pf.showPanels && pf.termView.OnAltScreen() {
		if e.KeyDown || pf.termView.Win32InputMode || pf.termView.KittyFlags != 0 {
			active := pf.getActivePTY()
			if active != nil {
				if seq := TranslateInput(e, pf.termView.Win32InputMode, pf.termView.KittyFlags, pf.termView.ApplicationCursorKeys); seq != "" {
					pf.writePTY(active, []byte(seq))
				}
			}
		}
		return true
	}

	// In search-first command focus, Alt+the grave key inserts a literal
	// backtick instead of toggling focus. Always insert the shell character,
	// including when the current keyboard layout reports 'ё'.
	if pf.searchFirstMode() && pf.showPanels && pf.commandLineFocused && e.KeyDown &&
		isCommandFocusToggleKey(e) && !ctrl && alt && !shift {
		pf.cmdLine.InsertString("`")
		return true
	}

	// Quake-style physical key: ` / ~ / ё toggles the explicit input
	// target in search-first mode and is never inserted as text.
	if pf.searchFirstMode() && pf.showPanels && e.KeyDown && isCommandFocusToggleKey(e) && !ctrl && !alt && !shift {
		pf.setCommandLineFocus(!pf.commandLineFocused)
		return true
	}

	// If the active slot is showing a focused alt panel (Ctrl+L info,
	// Ctrl+Q quick-view, later Ctrl+T tree), let it consume its own
	// navigation keys first — plain arrows, PgUp/Dn, Home/End, F2
	// wrap-toggle. Anything the alt doesn't recognise falls through
	// to the normal chain, so Ctrl+L / Ctrl+Q / Tab still work.
	if pf.showPanels && pf.altPanels[pf.activeIdx] != nil && pf.altPanels[pf.activeIdx].IsFocused() {
		if pf.altPanels[pf.activeIdx].ProcessKey(e) {
			return true
		}
	}

	// Fast Find owns Esc, Delete, F2 and Ctrl+Enter before in-frame hotkeys and
	// command-line handling. Delegate them to the panel's normal handler.
	if pf.showPanels && e.KeyDown {
		if fsp := pf.getActivePanel(); fsp != nil && fsp.fastFindMode {
			isFindEnter := e.VirtualKeyCode == vtinput.VK_RETURN && ctrl && !alt
			isFindModeToggle := e.VirtualKeyCode == vtinput.VK_F2 && !ctrl && !alt && !shift
			plainEscape := e.VirtualKeyCode == vtinput.VK_ESCAPE && !ctrl && !alt && !shift
			plainDelete := e.VirtualKeyCode == vtinput.VK_DELETE && !ctrl && !alt && !shift
			if plainEscape || plainDelete || isFindEnter || isFindModeToggle {
				return fsp.ProcessKey(e)
			}
		}
	}
	// Crash test hotkey: Ctrl+Alt+C
	if e.VirtualKeyCode == vtinput.VK_C && alt && ctrl && e.KeyDown {
		panic("Manual safe crash triggered by user (Ctrl+Alt+C) for testing!")
	}

	// Ctrl+C on a remote panel that carries a PTY-integration hint
	// interrupts whatever is running on the far side, ahead of the
	// Panel.SplitReset action bound to Ctrl+C globally. A user driving
	// a remote command from the panel cmdline reaches for Ctrl+C to
	// stop it; making them switch to the terminal view first (Ctrl+O)
	// just to be heard would be worse than losing the split-reset
	// binding on remote panels.
	if e.KeyDown && ctrl && !alt && !shift && e.VirtualKeyCode == vtinput.VK_C && pf.interruptRemotePTY(nil) {
		return true
	}
	if e.Type == vtinput.FocusEventType {
		if !e.SetFocus {
			pf.cancelFastFind()
		}
		pf.SetFocus(e.SetFocus)
		// Reload macros from disk when regaining focus to share them across instances
		if e.SetFocus && MacroMgr != nil {
			MacroMgr.Load()
		}
		// Propagate application focus without losing the explicit search-first target.
		if pf.searchFirstMode() {
			pf.cmdLine.SetFocus(e.SetFocus && pf.commandLineFocused)
		} else {
			pf.cmdLine.SetFocus(e.SetFocus)
		}
		pf.termView.SetFocus(e.SetFocus)
		return true
	}

	// Handle bracketed paste for terminal apps
	if e.Type == vtinput.PasteEventType {
		pty := pf.localPTY()
		if !pf.showPanels && pf.termView.BracketedPasteMode && pty != nil {
			if e.PasteStart {
				pf.writePTY(pty, []byte("\x1b[200~"))
			} else {
				pf.writePTY(pty, []byte("\x1b[201~"))
			}
			return true
		}
		// Editor view checks paste events internally, so we let it fall through if panels are shown
	}

	// Ctrl+O (Panel.Toggle) and Esc (Panel.Toggle:EscToggle) are handled
	// by the hotkey dispatcher; F3/F4 for the terminal log are bound in
	// the Terminal area with the TerminalQuiet condition.

	// In SimpleInline mode without panels the console view comes in two
	// flavours. Far style: f4 owns the keyboard, so keys go to its command
	// line and Enter falls through to the command execution path below,
	// leaving the user in the console. mc style: view only, any keypress
	// returns to the panels.
	// Must check e.KeyDown: without it, the trailing KeyUp of the very
	// Ctrl+O press that just hid the panels (handled above by the hotkey
	// dispatcher on its KeyDown) falls through to here unfiltered and
	// immediately flips panels back on — a hide/show flicker on every
	// single Ctrl+O press, most visible under Wine where KeyUp events are
	// reliably delivered as separate events.
	if pf.shellMode == ShellModeSimpleInline && !pf.showPanels &&
		e.Type == vtinput.KeyEventType && e.KeyDown {
		if pf.consoleStyle() == ConsoleViewFar {
			vtui.DebugLog("FARKEY: char=%q vk=0x%X before cmdLine.ProcessKey", e.Char, e.VirtualKeyCode)
			if e.VirtualKeyCode != vtinput.VK_RETURN && pf.cmdLine != nil && pf.cmdLine.ProcessKey(e) {
				vtui.DebugLog("FARKEY: cmdLine.ProcessKey handled it, drawing overlay")
				pf.drawConsoleOverlay()
				return true
			}
			vtui.DebugLog("FARKEY: cmdLine.ProcessKey did NOT handle it (fell through)")
		} else {
			pf.showPanels = true
			vtui.SetAltScreen(true)
			pf.SetBusy(false)
			if vtui.FrameManager != nil {
				vtui.FrameManager.HardRefresh()
			}
			return true
		}
	}

	// In Far-style host console with an overlay, route editing keys to CommandLine first
	if !pf.showPanels && pf.shellMode == ShellModeHost && pf.overlayLines() > 0 {
		if pf.handleHostConsoleTab(e) {
			return true
		}
		if e.VirtualKeyCode != vtinput.VK_RETURN {
			if pf.cmdLine.ProcessKey(e) {
				pf.drawHostConsoleOverlay()
				return true
			}
		}
	}

	// Raw input mode fallback for active shell commands (non-AltScreen, e.g. ping),
	// and for any interactive shell session when host console mode is active.
	// We forward text and navigation to PTY, but let global shortcuts (Ctrl+O) fall through.
	if !pf.showPanels && (pf.isPtyBusy() || pf.shellMode == ShellModeHost) {
		if e.KeyDown || pf.termView.Win32InputMode || pf.termView.KittyFlags != 0 {
			active := pf.getActivePTY()
			if active != nil {
				if seq := TranslateInput(e, pf.termView.Win32InputMode, pf.termView.KittyFlags, pf.termView.ApplicationCursorKeys); seq != "" {
					pf.writePTY(active, []byte(seq))
				}
			}
		}
		return true
	}

	// Char fallbacks for terminals that report Ctrl+\ / Ctrl+[ / Ctrl+]
	// with an unexpected virtual key code: the canonical bindings live
	// in the action registry (Panel.GoRoot / Panel.InsertLeftPath /
	// Panel.InsertRightPath).
	if e.Char == '\\' && ctrl && !alt && !shift && e.KeyDown {
		if RunAction("Panel.GoRoot") {
			return true
		}
	}
	if e.Char == '[' && ctrl && !alt && !shift && e.KeyDown {
		if RunAction("Panel.InsertLeftPath") {
			return true
		}
	}
	if e.Char == ']' && ctrl && !alt && !shift && e.KeyDown {
		if RunAction("Panel.InsertRightPath") {
			return true
		}
	}

	// Folder bookmarks, far2l's hotkey scheme: [RightCtrl | Ctrl+Alt] + N
	// jumps to slot N, Ctrl+Shift+N stores the current directory there, and
	// [RightCtrl | Ctrl+Alt] + ~ goes home. The ctrl local above merges both
	// Ctrl keys, but here the side matters, so the flags are read directly.
	// Terminals that cannot tell the two Ctrls apart report LeftCtrlPressed
	// for either one — that is what the Ctrl+Alt alias (far2l offers the
	// same pair) is for.
	if e.KeyDown && !configurableHotkeyOwnsPanelBookmark(GlobalHotkeysMgr, "Shell", e) {
		rctrl := (e.ControlKeyState & vtinput.RightCtrlPressed) != 0
		lctrl := (e.ControlKeyState & vtinput.LeftCtrlPressed) != 0
		isBookmarkGoto := (rctrl && !shift && !alt) || ((lctrl || rctrl) && alt && !shift)
		isBookmarkSave := (rctrl && shift && !alt) || ((lctrl || rctrl) && alt && shift)

		if e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 && (isBookmarkGoto || isBookmarkSave) {
			slot := int(e.VirtualKeyCode - vtinput.VK_0)
			file := BookmarksFilePath()
			// Always read fresh: another f4 or far2l instance may have
			// rewritten the file since we last looked at it.
			set, err := LoadBookmarks(file)
			if err != nil {
				vtui.DebugLog("BOOKMARKS: load %q failed: %v", file, err)
				return true
			}
			if isBookmarkSave {
				if fsp := pf.getActivePanel(); fsp != nil {
					set[slot] = Bookmark{Path: fsp.vfs.GetPath()}
					if err := SaveBookmarks(file, set); err != nil {
						vtui.DebugLog("BOOKMARKS: save %q failed: %v", file, err)
					}
				}
			} else if !set[slot].IsEmpty() {
				// An unset slot is a silent no-op, as in far2l.
				if fsp := pf.getActivePanel(); fsp != nil {
					pf.navigateToBookmark(fsp, set[slot])
				}
			}
			return true
		}

		if e.VirtualKeyCode == vtinput.VK_OEM_3 && isBookmarkGoto {
			if home, _ := os.UserHomeDir(); home != "" {
				if fsp := pf.getActivePanel(); fsp != nil {
					pf.NavigateToPath(fsp, home)
				}
			}
			return true
		}
	}

	if !e.KeyDown {
		return false
	}

	// F9 opens the main menu (other F-keys are action bindings now).
	// Alt+F9 is App.ToggleWindowSize and must not fall through to this.
	if e.VirtualKeyCode == vtinput.VK_F9 && !alt {
		pos := 0 // Left
		if pf.activeIdx == 1 {
			pos = 4 // Right
		}
		pf.GetMenuBar()
		if pos >= len(pf.menuBar.Items) {
			pos = 0
		}
		pf.menuBar.Active = true
		pf.menuBar.ActivateSubMenu(pos)
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && !pf.cmdLine.IsEmpty() && (!pf.searchFirstMode() || pf.commandLineFocused) {
		pf.cmdLine.Clear()
		pf.cmdLine.Edit.HistoryPos = -1
		return true
	}
	// In classic navigation, a non-empty command line owns plain horizontal
	// arrows. With an empty line they remain panel navigation keys (including
	// Detailed view's Left/Right page mapping). Search-first uses explicit
	// focus below, while Vim retains its existing routing.
	if AppConfig.NavigationMode == NavigationClassic && pf.showPanels && !pf.cmdLine.IsEmpty() &&
		(e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && !ctrl && !alt {
		return pf.cmdLine.ProcessKey(e)
	}
	// Vim-like hotkeys
	if AppConfig.NavigationMode == NavigationVim && pf.showPanels && !alt && !ctrl && !shift && e.Char != 0 && pf.cmdLine.Edit.HistoryPos == -1 {
		isFastFind := false
		if fsp := pf.getActivePanel(); fsp != nil {
			isFastFind = fsp.fastFindMode
		}

		isChatFocused := false
		if pf.altPanels[pf.activeIdx] != nil && pf.altPanels[pf.activeIdx].Kind() == "ai_chat" && pf.altPanels[pf.activeIdx].IsFocused() {
			isChatFocused = true
		}

		// If fast find or chat is active, Vim hotkeys must be ignored to allow typing 'j', 'k', etc.
		if !isFastFind && !isChatFocused {
			now := time.Now()
			key := e.Char
			cmdLineText := pf.cmdLine.Edit.GetText()

			// Double-press actions (dd, cc, mm)
			if pf.lastKey == key && now.Sub(pf.lastKeyEvent) < 400*time.Millisecond && (cmdLineText == "" || cmdLineText == string(key)) {
				var cmd int
				switch key {
				case 'd':
					cmd = CmDelete
				case 'c':
					cmd = CmCopy
				case 'm':
					cmd = CmMove
				}
				if cmd != 0 {
					pf.cmdLine.Clear()
					vtui.FrameManager.EmitCommand(cmd, nil)
					pf.lastKey = 0
					return true
				}
			}

			// Single-key navigation (strictly only if command line is empty)
			if cmdLineText == "" {
				if fsp := pf.getActivePanel(); fsp != nil {
					if key == 'j' {
						fsp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
						return true
					}
					if key == 'k' {
						fsp.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_UP})
						return true
					}
				}
			}

			// Record current key as a potential prefix for the next event
			if key == 'd' || key == 'c' || key == 'm' {
				pf.lastKey = key
				pf.lastKeyEvent = now
			} else {
				pf.lastKey = 0
			}
		}
	}

	// Enter handling
	if e.VirtualKeyCode == vtinput.VK_RETURN {
		// The hotkey filter normally handles Ctrl+Enter and Shift+Enter.
		// Keep in-frame fallbacks because platform input and injected events
		// can bypass that filter; under no circumstances should a modified
		// Enter become plain Enter and enter the selected directory or run
		// the command line. Hidden panels are included: an AltScreen app or
		// a busy PTY has already been served by the raw-forwarding returns
		// at the top of this function, so reaching this point means f4
		// itself owns the keyboard and the panel cursor is still live.
		if ctrl && !alt && !shift {
			pf.insertSelectedFileName()
			return true
		}
		if shift && !ctrl && !alt {
			RunAction("Panel.SystemExplorer")
			return true
		}
		commandInputActive := !pf.searchFirstMode() || pf.commandLineFocused || !pf.showPanels
		if commandInputActive && !pf.cmdLine.IsEmpty() {
			cmd := pf.cmdLine.Edit.GetText()
			if commandHasUnmatchedQuote(cmd, runtime.GOOS == "windows") {
				vtui.ShowMessage(" Error ", "Unmatched quote in command. Close the quote and press Enter again.", []string{"&Ok"})
				return true
			}
			pf.addCommandHistory(cmd)
			pf.cmdLine.Edit.HistoryPos = -1

			trimmedCmd := strings.TrimSpace(cmd)
			lowerCmd := strings.ToLower(trimmedCmd)
			if dispatchCommandPrefix(pf, trimmedCmd) {
				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				return true
			}
			if lowerCmd == "exit f4" {
				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				vtui.FrameManager.EmitCommand(vtui.CmQuit, nil)
				return true
			}

			// Intercept directory changes (cd, chdir, drive letters on
			// Windows) so the panel follows them instead of the shell.
			targetPath, isDirChange := parseDirChangeCommand(trimmedCmd)
			if !isDirChange && lowerCmd == "exit" {
				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				pf.resetLocalShell()
				return true
			}

			// Intercept Far Manager output capturing commands
			if strings.HasPrefix(lowerCmd, "clip:<<") || strings.HasPrefix(lowerCmd, "view:<<") || strings.HasPrefix(lowerCmd, "edit:<<") {
				idx := strings.Index(lowerCmd, ":<<")
				action := lowerCmd[:idx]
				actualCmd := strings.TrimSpace(trimmedCmd[idx+3:])

				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				executeCapturedCommand(pf, action, actualCmd)
				return true
			}

			// Apply to panel first
			if isDirChange {
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					targetPath = expandPathEnv(targetPath)
					if pf.NavigateToPath(fsp, targetPath) {
						pf.cmdLine.Clear()
						if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
							pf.setCommandLineFocus(false)
						}

						// Sync the background PTY synchronously to satisfy tests and provide immediate state
						if pf.syncPTYDirectory(fsp.vfs.GetPath(), fsp.vfs) {
							pf.lastPtyPath = fsp.vfs.GetPath()
							pf.lastPtyVFS = fsp.vfs
						}

						return true
					}
				}
			}

			// A remote filesystem may support commands without exposing an
			// interactive PTY. Android FISH+ deliberately uses this shape over ADB
			// shell_v2: route the typed command to its job runner instead of falling
			// through to the local Windows shell. SSH-backed FISH+ keeps using its
			// PTY below, preserving the full interactive terminal experience.
			if fsp := pf.getActivePanel(); fsp != nil && !isLocalOSVFS(fsp.vfs) && !vfsHasRemotePTY(fsp.vfs) {
				if runner, ok := fsp.vfs.(vfs.CommandRunner); ok {
					pf.cmdLine.Clear()
					pf.cmdLine.Edit.HistoryPos = -1
					if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
						pf.setCommandLineFocus(false)
					}
					showRemoteCommandOutput(pf, runner, fsp.vfs.GetPath(), cmd)
					return true
				}
			}

			// Fallthrough for regular commands or if directory change failed
			if pf.shellMode == ShellModeSimpleInline {
				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				var dir string
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					dir = fsp.vfs.GetPath()
				}
				pf.runSimpleInlineCommand(dir, cmd)
				return true
			}

			if pf.shellMode == ShellModeSimpleCaptured {
				pf.cmdLine.Clear()
				if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
					pf.setCommandLineFocus(false)
				}
				var dir string
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					dir = fsp.vfs.GetPath()
				}
				pf.runSimpleCapturedCommand(dir, cmd)
				return true
			}

			activePty := pf.getActivePTY()
			if activePty != nil {
				var path string
				isWindowsShell := runtime.GOOS == "windows"
				var localShellVFS vfs.VFS
				var integration vfs.PtyShellIntegration
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					if isLocalOSVFS(fsp.vfs) {
						path = fsp.vfs.GetPath()
						localShellVFS = fsp.vfs
					} else if vfsHasRemotePTY(fsp.vfs) {
						path = fsp.vfs.GetPath()
						isWindowsShell = false
					}
					// A VFS that carries its own PTY-shell templates
					// takes over the wire-command composition. FISH+
					// against a Windows peer takes this branch so the
					// PTY (cmd.exe by default) gets syntax cmd actually
					// parses — the bash-shaped OSC-133-wrapped template
					// below would come through as literal noise.
					if integ, ok2 := fsp.vfs.(vfs.PtyShellIntegration); ok2 {
						integration = integ
					}
				}

				var fullWireCmd string
				isBackground := false
				if !isWindowsShell {
					isBackground = strings.HasSuffix(strings.TrimSpace(cmd), "&")
				}

				if isWindowsShell {
					cmd = resolveWindowsCommand(cmd)
				}

				// The local Unix PTY is a persistent shell session. Its current
				// directory must not be reset to the panel path before every
				// command: doing that discards a `cd` performed by an alias. The
				// panel still synchronizes the shell when the panel itself moves;
				// this one-shot check only covers an Enter arriving before the
				// next frame refresh.
				if localShellVFS != nil && !isWindowsShell {
					if path != pf.lastPtyPath || !sameVFSInstance(localShellVFS, pf.lastPtyVFS) {
						if pf.syncPTYDirectory(path, localShellVFS) {
							pf.lastPtyPath = path
							pf.lastPtyVFS = localShellVFS
						}
					}
					path = ""
				}

				if integration != nil {
					if seq := integration.PtyRunCommand(path, cmd); len(seq) > 0 {
						fullWireCmd = string(seq)
						pf.beginPromptDrivenExecution()
						pf.returnToPanels = pf.showPanels
					}
				} else if isWindowsShell {
					// Use a combined command for reliable excision in AnsiParser: cd /d "path" & command
					if path != "" {
						fullWireCmd = fmt.Sprintf("cd /d \"%s\" & %s\r", path, cmd)
					} else {
						fullWireCmd = fmt.Sprintf("%s\r", cmd)
					}
					pf.beginPromptDrivenExecution()
					pf.returnToPanels = pf.showPanels
				} else {
					// Unix
					if isBackground {
						if path != "" {
							sqPath := strings.ReplaceAll(path, "'", "'\\''")
							fullWireCmd = fmt.Sprintf(" set +H; cd '%s' && %s\r", sqPath, cmd)
						} else {
							fullWireCmd = " " + cmd + "\r"
						}
					} else {
						// Managed foreground command.
						//
						// The command runs through eval, and it has to. The
						// wrapper below carries the OSC 133 markers that tell
						// f4 the command is over, and the shell parses the
						// whole line before running any of it: a syntax error
						// anywhere in it -- a bare ">", an unbalanced quote --
						// makes the shell reject the group entire, markers
						// included, and f4 waits forever for a completion that
						// was never going to be printed. eval parses the user's
						// text separately, so a syntax error in it is reported
						// and returns non-zero while the wrapper around it
						// still runs. It also keeps an unterminated quote from
						// putting the shell into PS2 continuation, where it
						// would sit swallowing everything typed next.
						sqCmd := shellSingleQuote(cmd)
						if path != "" {
							sqPath := strings.ReplaceAll(path, "'", "'\\''")
							fullWireCmd = fmt.Sprintf(" set +H; cd '%s' && { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; eval %s ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqPath, sqCmd)
						} else {
							fullWireCmd = fmt.Sprintf(" { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; eval %s ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqCmd)
						}
						pf.beginManagedExecution()
						pf.returnToPanels = pf.showPanels
					}
				}

				// Print the clean command in the local terminal echo
				// so the user sees what was sent. OSC-133 mute is only
				// safe for the shells that emit the matching D marker
				// — the bash templates above do, cmd.exe and the
				// integration path do not.
				if !isWindowsShell && integration == nil {
					pf.termView.PrintCleanCommand(cmd)
					if !isBackground {
						pf.termView.SetMuted(true)
					}
				} else if integration != nil {
					pf.termView.PrintCleanCommand(cmd)
				}
				pf.workspaceCommandTitle = workspaceCommandName(trimmedCmd)
				pf.writePTY(activePty, []byte(fullWireCmd))
				if isWindowsShell && integration == nil {
					pf.noteLocalShellLineSent(activePty)
				}
			}

			pf.cmdLine.Clear()
			if pf.searchFirstMode() && !AppConfig.SearchCommandStayFocused {
				pf.setCommandLineFocus(false)
			}
			pf.showPanels = false
			if pf.shellMode == ShellModeHost {
				pf.enterHostConsole()
			}
			return true
		} else if pf.searchFirstMode() && pf.commandLineFocused && pf.showPanels {
			// An empty command line must not activate the selected panel item.
			return true
		} else if !pf.showPanels {
			activePty := pf.getActivePTY()
			if activePty != nil {
				pf.writePTY(activePty, []byte("\r"))
			}
			return true
		} else {

			// CommandLine is empty, panels are visible.
			if fsp := pf.getActivePanel(); fsp != nil && !ctrl && !alt && !shift &&
				dispatchPanelAction(pf, vfs.PanelActionActivate, selectedPanelActionPaths(fsp)) {
				return true
			}

			// 1. Try passing to panel to handle directory entry.
			handled := pf.Active().ProcessKey(e)

			// 2. If panel didn't handle it, it's a file. Execute or open it.
			if !handled {
				fsp := pf.getActivePanel()
				if fsp == nil {
					return true
				}

				name := fsp.GetSelectedName()
				if name != "" && name != ".." {
					path := fsp.vfs.Join(fsp.vfs.GetPath(), name)
					actionExecute(pf, fsp.vfs, fsp.vfs.GetPath(), name, path)
				}
			}
			return true
		}
	}

	// Selection by mask (+, -, *) typed as plain characters on an empty
	// command line. The gray numpad keys are bound to the corresponding
	// actions (Panel.SelectGroup / DeselectGroup / InvertSelection);
	// both paths are suspended while fast find is active.
	if pf.showPanels && AppConfig.NavigationMode != NavigationSearchFirst && !alt && !ctrl && pf.cmdLine.IsEmpty() {
		if e.Char == '+' || e.Char == '-' || e.Char == '*' {
			isFastFind := false
			if fsp := pf.getActivePanel(); fsp != nil && fsp.fastFindMode {
				isFastFind = true
			}
			if !isFastFind {
				switch e.Char {
				case '+':
					RunAction("Panel.SelectGroup")
				case '-':
					RunAction("Panel.DeselectGroup")
				case '*':
					RunAction("Panel.InvertSelection")
				}
				return true
			}
		}
	}
	// 2. Try global hotkeys handled by PanelsFrame
	// Ctrl+Shift+Left / Ctrl+Shift+Right open the drive menu for the
	// corresponding visual panel without changing the active panel — but
	// only while there is no text to select, see commandLineOwnsSelection.
	if (e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && ctrl && !alt && shift && e.KeyDown && pf.showPanels &&
		!pf.commandLineOwnsSelection(e) {
		panelIdx := 0
		if e.VirtualKeyCode == vtinput.VK_RIGHT {
			panelIdx = 1
		}
		pf.showDriveMenu(panelIdx)
		return true
	}

	// Tab switches panels
	if e.VirtualKeyCode == vtinput.VK_TAB && !ctrl {
		if pf.showPanels && (!pf.searchFirstMode() || !pf.commandLineFocused) {
			otherIdx := 1 - pf.activeIdx
			otherVisible := pf.wide || (otherIdx == 0 && pf.showLeftPanel) || (otherIdx == 1 && pf.showRightPanel)
			if otherVisible {
				pf.activeIdx = otherIdx
				if pf.wide {
					pf.widePanel = pf.activeIdx
					pf.ResizeConsole(pf.lastW, pf.lastH)
					pf.lastKey = 0
					return true
				}
				pf.lastKey = 0
				if pf.searchFirstMode() {
					pf.setCommandLineFocus(false)
				}
				return true
			}
		}
		if AppConfig.CommandLineAutoComplete && !pf.cmdLine.IsEmpty() {
			acMenu := vtui.NewAutoCompleteMenu(pf.cmdLine.Edit)
			if acMenu.HasMatches() {
				vtui.FrameManager.Push(acMenu)
				return true
			}
		}
		if pf.showPanels {
			return true
		}
	}

	// With a non-empty command line, Ctrl+Left/Right are word navigation
	// in the command line; the panel-split resize actions on the same
	// keys are gated by the EmptyCommandLine condition and lose.
	if (e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && ctrl && !alt && !shift && e.KeyDown && pf.showPanels && !pf.cmdLine.IsEmpty() {
		if pf.cmdLine.IsVisible() {
			pf.cmdLine.ProcessKey(e)
			return true
		}
	}

	// Ctrl+Shift+Left/Right extend that selection word by word. This has
	// to run before the injected-event hotkey lookup below, which would
	// otherwise reach the drive menu the same way the filter does.
	if pf.commandLineOwnsSelection(e) {
		pf.cmdLine.ProcessKey(e)
		return true
	}

	// 3. Try Active Panel
	if pf.showPanels && (!pf.searchFirstMode() || !pf.commandLineFocused) {
		if pf.Active().ProcessKey(e) {
			return true
		}
	} else {
		// Navigation keys in the command line use command history, whether
		// panels are hidden or search-first focus is explicitly below.
		if e.VirtualKeyCode == vtinput.VK_UP || (e.VirtualKeyCode == vtinput.VK_E && ctrl) {
			pf.cmdLine.Edit.HistoryUp()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_DOWN || (e.VirtualKeyCode == vtinput.VK_X && ctrl) {
			pf.cmdLine.Edit.HistoryDown()
			return true
		}
	}

	// 4. Injected-event fallback: synthesized key events (a KeyBar mouse
	// click, InjectEvents from a widget) bypass FrameManager.EventFilter
	// and therefore skip the hotkey manager the F-key actions moved into
	// after the KeyBind refactor. Route them through the same lookup so
	// clicking F3/F4/F5/… on the bottom bar still triggers View/Edit/Copy.
	// Real key events reach ProcessKey only when Filter already declined
	// them, so re-checking here is a no-op for those.
	if MacroMgr.LookupHotkey(e) {
		return true
	}

	// 5. Fallback: pass to CommandLine (handles text, Backspace, Delete, etc.)
	if (!pf.searchFirstMode() || pf.commandLineFocused || !pf.showPanels) && pf.cmdLine.ProcessKey(e) {
		pf.cmdLine.SetFocus(true)
		return true
	}

	return false
}
func (pf *PanelsFrame) HandleBroadcast(cmd int, args any) bool {
	if cmd == CmFileChanged {
		pf.RefreshAll()
		return true
	}
	return pf.BaseFrame.HandleBroadcast(cmd, args)
}

// hitAltPanel returns the index (0 or 1) of the alt panel whose
// on-screen area contains (mx, my), or -1 if none. Respects the
// per-side hidden flags so a click over a hidden slot doesn't
// accidentally target its ghost alt panel.
func (pf *PanelsFrame) hitAltPanel(mx, my int) int {
	for i, a := range pf.altPanels {
		if a == nil {
			continue
		}
		if pf.wide && i != pf.widePanel {
			continue
		}
		if !pf.wide && i == 0 && !pf.showLeftPanel {
			continue
		}
		if !pf.wide && i == 1 && !pf.showRightPanel {
			continue
		}
		x1, y1, x2, y2 := a.GetPosition()
		if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
			return i
		}
	}
	return -1
}

// handleTerminalMouseSelection implements xterm-style text selection
// over the terminal viewport when panels are hidden and no TUI has
// grabbed the mouse via a tracking-mode escape. LMB starts / extends
// a selection, dbl/triple-click selects word / line, Alt+LMB drag
// switches to a rectangular block, releasing LMB auto-copies to the
// clipboard, RMB pastes clipboard content into the f4 command line when it
// owns the visible input, or into the PTY otherwise. Returns true when it
// consumed the event.
func (pf *PanelsFrame) hiddenConsoleCommandLineOwnsInput() bool {
	if pf.showPanels || pf.cmdLine == nil || !pf.cmdLine.IsVisible() {
		return false
	}

	// zoin-bot: a visible f4 command line must receive mouse paste through
	// the edit control, otherwise Enter can execute text that was never drawn.
	switch pf.shellMode {
	case ShellModeHost:
		return pf.consoleStyle() == ConsoleViewFar && pf.isHostConsoleActive()
	case ShellModeSimpleInline:
		return pf.consoleStyle() == ConsoleViewFar && pf.consoleViewActive()
	default:
		return pf.termView != nil && !pf.termView.OnAltScreen() && !pf.isPtyBusy()
	}
}

func (pf *PanelsFrame) pasteHiddenConsoleText(text string) {
	if text == "" {
		return
	}
	pf.cmdLine.InsertString(text)
	pf.cmdLine.SetFocus(true)
	if pf.shellMode == ShellModeHost {
		pf.drawHostConsoleOverlay()
	} else if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (pf *PanelsFrame) handleTerminalMouseSelection(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return false
	}
	tv := pf.termView
	if tv == nil {
		return false
	}
	mx, my := int(e.MouseX), int(e.MouseY)

	// Right button: paste on button-down so the release doesn't paste a second
	// time. The visible command line is handled before terminal hit testing.
	if e.ButtonState == vtinput.RightmostButtonPressed && e.KeyDown {
		if pf.hiddenConsoleCommandLineOwnsInput() {
			pf.pasteHiddenConsoleText(tv.readClipboard())
			return true
		}
		if !tv.InTerminalArea(mx, my) {
			return false
		}
		if pty := pf.getActivePTY(); pty != nil {
			text := tv.readClipboard()
			if text != "" {
				if tv.BracketedPasteMode {
					pf.writePTY(pty, []byte("\x1b[200~"+text+"\x1b[201~"))
				} else {
					pf.writePTY(pty, []byte(text))
				}
			}
		}
		return true
	}

	// LMB fresh press — start / promote a selection. Guarded by
	// KeyDown=true and no-MouseMoved so this fires only on the
	// initial button-down (all hosts agree on that shape).
	if e.ButtonState == vtinput.FromLeft1stButtonPressed &&
		e.KeyDown && (e.MouseEventFlags&vtinput.MouseMoved) == 0 {
		if !tv.InTerminalArea(mx, my) {
			return false
		}
		alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0

		now := time.Now()
		if pf.termSelClickN > 0 &&
			now.Sub(pf.termSelClickAt) < 400*time.Millisecond &&
			pf.termSelClickX == mx && pf.termSelClickY == my {
			pf.termSelClickN++
		} else {
			pf.termSelClickN = 1
		}
		pf.termSelClickAt = now
		pf.termSelClickX, pf.termSelClickY = mx, my

		// A fresh click always drops the previous highlight, so the
		// world resets before we start whatever this click resolves
		// into (single/double/triple).
		tv.ClearSelection()
		switch pf.termSelClickN {
		case 1:
			tv.StartSelection(mx, my, alt)
			pf.termSelDragging = true
		case 2:
			tv.SelectWordAt(mx, my)
			pf.termSelDragging = true
		default: // 3 or more
			tv.SelectLineAt(my)
			pf.termSelDragging = true
			pf.termSelClickN = 0
		}
		vtui.FrameManager.Redraw()
		return true
	}

	// From here on we act only while a drag is in progress; separate
	// motion-vs-release paths so we work across every backend:
	//   * Windows console: every mouse event has KeyDown=true; release
	//     is inferred from ButtonState transitioning to 0.
	//   * Wayland: motion has KeyDown=false, ButtonState=held; release
	//     KeyDown=false, ButtonState=0.
	//   * X11/purex11: motion has KeyDown=false, ButtonState=0; release
	//     KeyDown=false, ButtonState=held (X11 leaves the button code).
	//   * tty SGR: motion KeyDown=true (button+motion bit); release
	//     KeyDown=false, ButtonState=held.
	if !pf.termSelDragging {
		return false
	}

	// Drag — extend selection while the mouse moves with LMB held.
	if (e.MouseEventFlags & vtinput.MouseMoved) != 0 {
		tv.ExtendSelection(mx, my)
		vtui.FrameManager.Redraw()
		return true
	}

	// Release — the button is no longer down under any of the four
	// shapes above.
	releasedWayland := e.ButtonState == 0
	releasedElsewhere := !e.KeyDown
	if releasedWayland || releasedElsewhere {
		pf.termSelDragging = false
		if !tv.SelectionIsEmpty() {
			text := tv.ExtractSelection()
			if text != "" {
				// SetClipboard can hang for seconds behind far2l IPC
				// or xclip/wl-copy — same treatment as the grabber.
				go tv.copySelectionToClipboard(text)
			}
		}
		vtui.FrameManager.Redraw()
		return true
	}

	return false
}

// processMiddleMouseGesture recognizes exactly one initial middle-button down
// and owns all following move/release events until the gesture is complete.
// The trigger result is true only for that first down.
func (pf *PanelsFrame) processMiddleMouseGesture(e *vtinput.InputEvent) (handled, trigger bool) {
	// Some Windows mice report wheel rotation with the middle-button bit still
	// set while the wheel is held. Rotation is never a click and must continue
	// to the regular wheel-routing path.
	if e.WheelDirection != 0 {
		return false, false
	}
	isMove := e.MouseEventFlags&vtinput.MouseMoved != 0
	if pf.middleMouseDown {
		if !isMove && (e.ButtonState&vtinput.FromLeft2ndButtonPressed == 0 || !e.KeyDown) {
			pf.middleMouseDown = false
		}
		return true, false
	}

	if e.ButtonState&vtinput.FromLeft2ndButtonPressed != 0 && e.KeyDown && !isMove {
		pf.middleMouseDown = true
		return true, true
	}
	return false, false
}

// terminalWantsMouseEvent applies the DEC mouse-tracking mode selected by
// the terminal application. SGR (1006) chooses the wire format; it does not
// itself request mouse events. In normal tracking mode (1000), applications
// receive button presses and releases only, not hover motion. Button-event
// tracking (1002) adds motion while a button is held; only any-event
// tracking (1003) reports the pointer moving with no button down. Every
// backend now delivers that hover motion (it is what underlines a URL under
// the pointer, #459), so a vim or mc that asked for 1002 must not see it.
func terminalWantsMouseEvent(mode int, e *vtinput.InputEvent) bool {
	if mode == 0 || e == nil {
		return false
	}
	if e.MouseEventFlags&vtinput.MouseMoved == 0 {
		return true
	}
	switch mode {
	case 1000:
		return false
	case 1002:
		return e.ButtonState != 0
	default:
		return true
	}
}

func (pf *PanelsFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	// If panels are hidden, route relevant mouse events to PTY immediately
	if !pf.showPanels {
		mx, my := int(e.MouseX), int(e.MouseY)
		if pf.termView != nil && e.WheelDirection == 0 {
			if changed := pf.termView.UpdateURLHover(mx, my); changed {
				vtui.FrameManager.Redraw()
			}
			if ctrlMouseClick(e) {
				if rawURL, ok := pf.termView.URLAt(mx, my); ok {
					openExternalURLAsync(rawURL)
					return true
				}
			}
		}
		active := pf.getActivePTY()
		if active != nil && terminalWantsMouseEvent(pf.termView.MouseTrackingMode, e) {
			seq := TranslateMouseInput(e)
			pf.writePTY(active, []byte(seq))
			return true
		}
		// No TUI is grabbing the mouse — treat clicks/drags in the
		// terminal viewport as text selection with auto-copy on
		// release (xterm-style, and matches far2l's AdhocQuickEdit).
		if pf.handleTerminalMouseSelection(e) {
			return true
		}
		// If tracking is off, we still swallow clicks inside AltScreen to prevent hitting hidden panels
		return e.ButtonState != 0 || e.WheelDirection != 0
	}

	mx, my := int(e.MouseX), int(e.MouseY)
	if pf.processDragOutGesture(e, mx, my) {
		return true
	}

	// A middle-button gesture that already emitted Enter owns its remaining
	// motion/release events and must not fall through to panels or scrollbars.
	if pf.middleMouseDown && e.WheelDirection == 0 {
		pf.processMiddleMouseGesture(e)
		return true
	}

	// Keep every mouse gesture routed to the panel where its initial press
	// occurred. MouseMoved is checked first because some backends report moves
	// with ButtonState=0; releases are the non-move event with no held button
	// (or KeyDown=false on tty/X11 backends).
	if pf.panelMouseCapture != nil {
		captured := pf.panelMouseCapture
		captured.ProcessMouse(e)
		isMove := e.MouseEventFlags&vtinput.MouseMoved != 0
		isRelease := !isMove && (e.ButtonState == 0 || !e.KeyDown)
		if isRelease {
			pf.panelMouseCapture = nil
		}
		return true
	}

	if pf.searchFirstMode() && e.WheelDirection == 0 && e.ButtonState != 0 && e.KeyDown && pf.cmdLine.IsVisible() {
		x1, y1, x2, y2 := pf.cmdLine.GetPosition()
		if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
			pf.setCommandLineFocus(true)
			pf.cmdLine.ProcessMouse(e)
			return true
		}
	}

	// Активация меню кликом мыши на нулевую строку (AlwaysShowMenuBar)
	if AppConfig.AlwaysShowMenuBar && pf.showPanels && my == 0 && e.WheelDirection == 0 && e.ButtonState != 0 {
		pf.menuBar.Active = true
		pf.menuBar.ProcessMouse(e)
		return true
	}

	// Alt panels (Ctrl+L info / Ctrl+Q quick view / …) share the
	// same screen slot as the file panel underneath — the file
	// panel is not drawn, but it still exists logically at the
	// same coordinates. Swallow any button event landing on an
	// alt panel so a double-click or the global middle-click →
	// Enter branch below can't launch a file the user can't see.
	// Wheel events fall through to the wheel branch and its
	// normal alt-panel-first routing.
	if pf.showPanels && e.WheelDirection == 0 && e.ButtonState != 0 {
		if i := pf.hitAltPanel(mx, my); i >= 0 {
			// Click on an alt panel activates its side, same as
			// a click on a file panel does.
			if pf.activeIdx != i {
				pf.activeIdx = i
				pf.lastKey = 0
				vtui.FrameManager.Redraw()
			}
			if pf.searchFirstMode() {
				pf.setCommandLineFocus(false)
			}
			// Give the alt panel a chance to handle it (future
			// row-picking, etc.); return true either way — the
			// event does not fall through.
			pf.altPanels[i].ProcessMouse(e)
			return true
		}
	}

	// Global middle-click (wheel click) intercept for PanelsFrame. Only the
	// initial down emits Enter; held-button MouseMoved events are consumed by
	// the gesture state above.
	if handled, trigger := pf.processMiddleMouseGesture(e); handled {
		if trigger {
			pf.ProcessKey(&vtinput.InputEvent{
				Type:           vtinput.KeyEventType,
				KeyDown:        true,
				VirtualKeyCode: vtinput.VK_RETURN,
			})
		}
		return true
	}

	// Wheel events scroll the hovered panel if panels are visible.
	// When a panel slot is covered by an alt panel (e.g. Ctrl+Q quick-view),
	// we hand the wheel to it. This matches modern GUI and far2l behavior.
	if e.WheelDirection != 0 {
		targetIdx := pf.activeIdx

		// 1. If the active slot has an active alt panel, scroll it
		if pf.showPanels && pf.altPanels[targetIdx] != nil {
			if pf.altPanels[targetIdx].ProcessMouse(e) {
				return true
			}
		}

		// 2. Otherwise, scroll the active regular panel
		if pf.showPanels && pf.panels[targetIdx] != nil {
			if pf.panels[targetIdx].ProcessMouse(e) {
				return true
			}
		}

		vk := vtinput.VK_DOWN
		if e.WheelDirection > 0 {
			vk = vtinput.VK_UP
		}
		return pf.Active().ProcessKey(&vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  uint16(vk),
			ControlKeyState: e.ControlKeyState,
		})
	}

	for i, p := range pf.panels {
		if p == nil {
			continue
		}
		if pf.wide && i != pf.widePanel {
			continue
		}
		if !pf.wide && i == 0 && !pf.showLeftPanel {
			continue
		}
		if !pf.wide && i == 1 && !pf.showRightPanel {
			continue
		}
		x1, y1, x2, y2 := p.GetPosition()
		if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
			isInitialPress := e.ButtonState != 0 && e.KeyDown && e.MouseEventFlags&vtinput.MouseMoved == 0
			// Context menus target the clicked side without activating it or
			// moving its cursor.
			if isInitialPress && e.ButtonState&vtinput.RightmostButtonPressed != 0 {
				if fsp, ok := p.(*FileSystemPanel); ok {
					if fsp.pathTitleHitTest(mx, my) {
						pf.showDriveMenu(i)
						return true
					}
					if _, header := fsp.headerSortModeAt(mx, my); header {
						actionSortMenuForPanel(pf, fsp)
						return true
					}
				}
			}
			if pf.activeIdx != i && e.ButtonState != 0 {
				pf.activeIdx = i
				pf.lastKey = 0
				vtui.FrameManager.Redraw()
			}
			if pf.searchFirstMode() && e.ButtonState != 0 {
				pf.setCommandLineFocus(false)
			}
			if isInitialPress {
				pf.panelMouseCapture = p
			}

			handled := p.ProcessMouse(e)
			if handled && e.KeyDown {
				scrollBarHandled := false
				if fsp, ok := p.(*FileSystemPanel); ok {
					scrollBarHandled = fsp.scrollMouseActive || fsp.headerMouseActive
				}
				isLeftDoubleClick := (e.MouseEventFlags&vtinput.DoubleClick) != 0 && (e.ButtonState&vtinput.FromLeft1stButtonPressed) != 0
				isMiddleClick := (e.ButtonState & vtinput.FromLeft2ndButtonPressed) != 0

				if !scrollBarHandled && (isLeftDoubleClick || isMiddleClick) {
					pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
				}
			}
			return handled || e.ButtonState != 0
		}
	}

	return false
}

func (pf *PanelsFrame) getActivePanel() *FileSystemPanel {
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		return fsp
	}
	return nil
}

func (pf *PanelsFrame) getInactivePanel() *FileSystemPanel {
	if fsp, ok := pf.Passive().(*FileSystemPanel); ok {
		return fsp
	}
	return nil
}

// cancelFastFind closes the transient search UI whenever control leaves the
// file panels. It is intentionally frame-wide: only one panel should own Fast
// Find, but clearing both slots prevents a stale inactive search from coming
// back after Tab, a panel swap, or an overlay closes.
func (pf *PanelsFrame) cancelFastFind() bool {
	cancelled := false
	for _, panel := range pf.panels {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || !fsp.fastFindMode {
			continue
		}
		fsp.fastFindMode = false
		fsp.fastFindStr = ""
		cancelled = true
	}
	return cancelled
}

func (pf *PanelsFrame) GetPaths() (string, string) {
	l, r := "", ""
	if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
		l = fsp.persistentPath()
	}
	if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
		r = fsp.persistentPath()
	}
	return l, r
}

// HandleCommand intercepts global commands (like CmQuit or CmCopy)
// sent by menus or other views.
func (pf *PanelsFrame) HandleCommand(cmd int, args any) bool {
	switch cmd {
	case vtui.CmQuit:
		active := 0
		if GlobalQueueManager != nil {
			active = GlobalQueueManager.ActiveTasksCount()
		}
		if AppConfig.ConfirmExit || active > 0 {
			msg := Msg("Quit.Confirm")
			if active > 0 {
				msg = fmt.Sprintf("There are %d active background operations!\nIf you exit, they will be aborted.\n\n%s", active, msg)
			}
			dlg := vtui.ShowMessage(Msg("Quit.Title"), msg, []string{Msg("Quit.Btn"), Msg("vtui.Cancel")})
			// When background operations would be aborted the exit is
			// genuinely destructive — flip to the WarnDialog palette.
			// Plain "confirm on exit" stays on the neutral palette.
			if active > 0 {
				dlg.IsWarning = true
			}
			dlg.OnResult = func(code int) {
				if code == 0 {
					cancelOperationsForShutdown()
					SaveSession()
					if pty := pf.takeLocalPTY(); pty != nil {
						pty.Close()
					}
					shutdownProcessEnvironmentRuntime()
					vtui.FrameManager.Shutdown()
				}
			}
		} else {
			cancelOperationsForShutdown()
			SaveSession()
			if pty := pf.takeLocalPTY(); pty != nil {
				pty.Close()
			}
			shutdownProcessEnvironmentRuntime()
			vtui.FrameManager.Shutdown()
		}
		return true

	case vtui.CmHelp:
		pf.ShowHelp()
		return true

	case CmNew:
		actionNewFile(pf)
		return true

	case CmView:
		actionViewFile(pf)
		return true

	case CmEdit:
		actionEditFile(pf)
		return true

	case CmCopy, CmMove:
		actionCopyMove(pf, cmd == CmMove)
		return true

	case CmRename:
		actionRename(pf)
		return true

	case CmMkDir:
		actionMkDir(pf)
		return true

	case CmDelete:
		actionDelete(pf)
		return true
	case CmFindFile:
		actionFindFile(pf)
		return true
	case CmSwitchToViewer:
		if ev, ok := args.(*EditorView); ok {
			doSwitch := func() {
				path := ev.filePath
				v := ev.vfs
				ev.Close()
				actionOpenViewer(pf, v, path)
			}
			if ev.modified {
				msg := "The file has been modified.\nDo you want to save it before switching?"
				dlg := vtui.ShowMessage(" Confirm ", msg, []string{"&Save", "&Don't Save", "Cancel"})
				dlg.OnResult = func(code int) {
					switch code {
					case 0:
						ev.SaveToFile(doSwitch)
					case 1:
						doSwitch()
					}
				}
			} else {
				doSwitch()
			}
			return true
		}
		return false
	case CmSwitchToEditor:
		if vv, ok := args.(*ViewerView); ok {
			path := vv.path
			v := vv.vfs
			vv.Close()
			actionOpenEditor(pf, v, path)
			return true
		}
		return false
	case CmBookmarks:
		ShowBookmarksDialog(pf)
		return true
	case CmPanelSettings:
		actionPanelSettings(pf)
		return true
	case CmEditorSettings:
		actionEditorSettings(pf)
		return true
	case CmColorerSettings:
		actionColorerSettings(pf)
		return true
	case CmAppearanceSettings:
		actionAppearanceSettings(pf)
		return true
	case CmConfirmationsSettings:
		actionConfirmationsSettings(pf)
		return true
	case CmHotkeyConfig:
		actionHotkeyConfig(pf)
		return true
	case CmLanguage:
		actionLanguage(pf)
		return true
	case CmHelpLanguage:
		actionHelpLanguage(pf)
		return true
	case CmUpdateSettings:
		actionUpdateSettings(pf)
		return true
	case CmProxySettings:
		actionProxySettings()
		return true
	case CmPlugins:
		actionManagePlugins(pf)
		return true

	case CmPlugRing:
		actionPlugRing(pf)
		return true
	case CmBackground:
		return actionBackground()
	case CmWorkspaceNew:
		return actionWorkspaceNew()
	case CmWorkspaceClose:
		return actionWorkspaceClose()
	case CmLeftDriveMenu:
		pf.showDriveMenu(0)
		return true
	case CmRightDriveMenu:
		pf.showDriveMenu(1)
		return true
	case vtui.CmResize: // Used as a hack for 'fork' command from FrameManager
		if s, ok := args.(string); ok && s == "fork" {
			clone := pf.Clone()
			// Ctrl+N means "fork the panels", including when it is invoked
			// while the terminal is visible. Copying showPanels=false creates
			// two visually identical cmd.exe workspaces and makes a successful
			// switch look like it did nothing. Keep the running terminal in the
			// original workspace and expose the cloned panels in the new one.
			if !pf.showPanels {
				clone.showPanels = true
				if clone.lastW > 0 && clone.lastH > 0 {
					clone.ResizeConsole(clone.lastW, clone.lastH)
				} else {
					clone.updateMenuCheckmarks()
				}
			}
			vtui.FrameManager.AddScreen(clone)
			return true
		}

	case CmLeftBrief:
		pf.setPanelViewMode(0, ViewModeBrief)
		return true
	case CmLeftMedium:
		pf.setPanelViewMode(0, ViewModeMedium)
		return true
	case CmLeftDetailed:
		pf.setPanelViewMode(0, ViewModeDetailed)
		return true
	case CmLeftWide:
		pf.setWidePanel(0)
		return true
	case CmRightBrief:
		pf.setPanelViewMode(1, ViewModeBrief)
		return true
	case CmRightMedium:
		pf.setPanelViewMode(1, ViewModeMedium)
		return true
	case CmRightDetailed:
		pf.setPanelViewMode(1, ViewModeDetailed)
		return true
	case CmRightWide:
		pf.setWidePanel(1)
		return true
	case CmLeftAIContext:
		if aiCmd, ok := pf.panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://ctx", false)
		}
		return true
	case CmLeftAIChat:
		if aiCmd, ok := pf.panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://chat", true)
		}
		return true
	case CmLeftAIOut:
		if aiCmd, ok := pf.panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://out", false)
		}
		return true
	case CmLeftAIMem:
		if aiCmd, ok := pf.panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://mem", false)
		}
		return true
	case CmRightAIContext:
		if aiCmd, ok := pf.panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://ctx", false)
		}
		return true
	case CmRightAIChat:
		if aiCmd, ok := pf.panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://chat", true)
		}
		return true
	case CmRightAIOut:
		if aiCmd, ok := pf.panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://out", false)
		}
		return true
	case CmRightAIMem:
		if aiCmd, ok := pf.panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://mem", false)
		}
		return true

	case CmLeftSortName:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortName)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmLeftSortExt:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortExt)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmLeftSortTime:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortTime)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmLeftSortSize:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortSize)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmLeftSortUnsorted:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightSortName:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortName)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightSortExt:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortExt)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightSortTime:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortTime)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightSortSize:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortSize)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightSortUnsorted:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmSwapPanels:
		pf.panels[0], pf.panels[1] = pf.panels[1], pf.panels[0]
		pf.activeIdx = 1 - pf.activeIdx
		if pf.wide {
			pf.widePanel = 1 - pf.widePanel
		}
		pf.ResizeConsole(pf.lastW, pf.lastH)
		return true
	case CmSortName:
		if fsp := pf.getActivePanel(); fsp != nil {
			fsp.SetSortMode(SortName)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmSortExt:
		if fsp := pf.getActivePanel(); fsp != nil {
			fsp.SetSortMode(SortExt)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmSortTime:
		if fsp := pf.getActivePanel(); fsp != nil {
			fsp.SetSortMode(SortTime)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmSortSize:
		if fsp := pf.getActivePanel(); fsp != nil {
			fsp.SetSortMode(SortSize)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmSortUnsorted:
		if fsp := pf.getActivePanel(); fsp != nil {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.updateMenuCheckmarks()
		return true
	}
	return false
}

func (pf *PanelsFrame) GetKeyLabels() *vtui.KeySet {
	area := MacroMgr.GetCurrentArea()

	f2 := Msg("KeyBar.F2")
	f7 := Msg("KeyBar.F7")
	overrideF2 := false
	if pf.showPanels && pf.activeIdx >= 0 && pf.activeIdx < len(pf.altPanels) {
		if q, ok := pf.altPanels[pf.activeIdx].(*QuickViewPanel); ok && q != nil && q.IsFocused() {
			nextCP := vfs.DisplayCodepageName(vfs.GetNextFastSwitchCodepage(q.cacheCodepage))
			return &vtui.KeySet{
				Normal: vtui.KeyBarLabels{
					Msg("KeyBar.ViewerF1"),
					func() string {
						if q.wrap {
							return Msg("KeyBar.ViewerF2")
						}
						return Msg("KeyBar.F2Wrap")
					}(),
					Msg("KeyBar.ViewerF3"), Msg("KeyBar.ViewerF4"),
					"", "", Msg("KeyBar.ViewerF7"), nextCP, "", Msg("KeyBar.ViewerF10"),
				},
				Shift: vtui.KeyBarLabels{"", "", "", "", "", "", Msg("KeyBar.ViewerF7"), Msg("Codepage.Title"), "", "", "", ""},
			}
		}
	}
	if pf.showPanels && pf.activeIdx >= 0 && pf.activeIdx < len(pf.altPanels) {
		if fsp := pf.getActivePanel(); fsp != nil {
			if _, ok := fsp.vfs.(*TempPanelVFS); ok {
				f7 = Msg("TempPanel.Remove")
			}
		}
		if a := pf.altPanels[pf.activeIdx]; a != nil && a.IsFocused() && a.Kind() == "quick_view" {
			if q, ok := a.(*QuickViewPanel); ok {
				if q.wrap {
					f2 = Msg("KeyBar.F2Unwrap")
				} else {
					f2 = Msg("KeyBar.F2Wrap")
				}
				overrideF2 = true
			}
		}
	}

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.F1"), f2, Msg("KeyBar.F3"), Msg("KeyBar.F4"),
			Msg("KeyBar.F5"), Msg("KeyBar.F6"), f7, Msg("KeyBar.F8"),
			Msg("KeyBar.F9"), Msg("KeyBar.F10"), Msg("KeyBar.F11"), Msg("KeyBar.F12"),
		},
		Shift: vtui.KeyBarLabels{"", "", "", "", "", "Rename", "", "", "Save", "", "", ""},
		Alt: vtui.KeyBarLabels{
			Msg("KeyBar.AltF1"), Msg("KeyBar.AltF2"), Msg("KeyBar.AltF3"), "",
			"", "", Msg("KeyBar.AltF7"), Msg("KeyBar.AltF8"), "", "", "", Msg("KeyBar.AltF12"),
		},
		Ctrl: vtui.KeyBarLabels{
			Msg("KeyBar.CtrlF1"), Msg("KeyBar.CtrlF2"), Msg("KeyBar.CtrlF3"), Msg("KeyBar.CtrlF4"), Msg("KeyBar.CtrlF5"), Msg("KeyBar.CtrlF6"), Msg("KeyBar.CtrlF7"), "", "", "", "Fork", "Close",
		},
	}
	res := KeyBarLabelsForArea(area, fallbacks)
	if overrideF2 {
		res.Normal[1] = f2
	}
	return res
}

func (pf *PanelsFrame) GetType() vtui.FrameType { return vtui.TypeUser + 1 }

func (pf *PanelsFrame) SetExitCode(code int) { pf.Done = true; pf.ExitCode = code }
func (pf *PanelsFrame) showDummyOpDialog() {
	msg := Msg("Op.DummyText")
	lines := vtui.WrapText(msg, 50-4)

	dlg := vtui.NewCenteredDialog(50, 11+len(lines)-1, Msg("Op.DummyTitle"))
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, (11+len(lines)-1)-4)

	for _, l := range lines {
		t := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(t)
		vbox.Add(t, vtui.Margins{}, vtui.AlignLeft)
	}

	modes := []string{Msg("Op.DummyQueue"), Msg("Op.DummyBackground"), Msg("Op.DummyForeground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(0)
	comboMode.Edit.SetText(comboMode.Menu.Items[0].Text)

	btnStart := vtui.NewButton(0, 0, Msg("Op.BtnStart"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	dlg.AddItem(btnStart)
	dlg.AddItem(btnCancel)
	dlg.AddItem(comboMode)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnStart, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	// Keep the action row above the operation-mode selector. ComboBox.Open()
	// places its popup below the field, so it cannot cover these buttons.
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	// Set default focus to Start button
	dlg.SetFocusedItem(btnStart)

	btnCancel.OnClick = func() { dlg.Close() }
	btnStart.OnClick = func() {
		mode := comboMode.Menu.SelectPos
		dlg.Close()
		go pf.ExecuteDummyOp(mode)
	}

	vtui.FrameManager.Push(dlg)
}

// RunProgressTask encapsulates the boilerplate for creating a progress dialog,
// running a background task with cancellation, and optionally forking the workspace.
func (pf *PanelsFrame) RunProgressTask(title, startMsg string, forked bool, worker func(ctx context.Context, update func(msg string, percent int)) error, onComplete func(err error)) {
	pf.runProgressTaskAfter(0, title, startMsg, forked, worker, onComplete)
}

// runProgressTaskAfter starts the worker immediately but postpones showing its
// dialog. A task that completes before delay never creates a visible screen,
// avoiding a one-frame flash for fast remote operations.
func (pf *PanelsFrame) runProgressTaskAfter(delay time.Duration, title, startMsg string, forked bool, worker func(ctx context.Context, update func(msg string, percent int)) error, onComplete func(err error)) {
	dlg := vtui.NewCenteredDialog(50, 12, title)
	dlg.AttentionSuppressed = true

	lbl := vtui.NewText(0, 0, startMsg, vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lbl)

	pb := vtui.NewProgressBar(0, 0, 46)
	dlg.AddItem(pb)

	lblHint := vtui.NewText(0, 0, Msg("Op.SwitchHint"), vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lblHint)

	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, 10-4)
	vbox.Add(lbl, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(pb, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(lblHint, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(btnCancel, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	var taskCtxMu sync.Mutex // protects taskCtx and cancelPending
	var taskCtx *vtui.TaskContext
	cancelPending := false
	btnCancel.OnClick = func() {
		dlg.SetExitCode(1)
	}
	dlg.OnResult = func(code int) {
		taskCtxMu.Lock()
		ctx := taskCtx
		if ctx == nil {
			cancelPending = true
		}
		taskCtxMu.Unlock()
		if ctx != nil {
			ctx.Cancel()
		}
	}

	done := make(chan struct{})
	dialogShown := false // accessed only from UI tasks
	uiFrames := vtui.FrameManager
	var showDialog func()
	showDialog = func() {
		if delay > 0 {
			select {
			case <-done:
				return
			default:
			}
		}
		// A worker may ask for a modal dialog (for example, an archive
		// password) before this delayed progress screen is due to appear.
		// Do not put the progress screen on a new active screen while that
		// prompt is waiting on the old one: it would hide the prompt and leave
		// the worker blocked forever. Retry after the modal dialog is dismissed.
		if top := uiFrames.GetTopFrame(); top != nil && top.IsModal() {
			time.AfterFunc(50*time.Millisecond, func() {
				uiFrames.PostTask(showDialog)
			})
			return
		}
		if forked && pf != nil {
			clone := pf.Clone()
			uiFrames.AddScreen(clone)
			uiFrames.Push(dlg)
		} else {
			uiFrames.AddScreenHeadless(dlg)
		}
		dialogShown = true
	}

	var showTimer *time.Timer
	if delay > 0 {
		// Read on the goroutine that starts this work, not inside it: the
		// work outlives the call, and reading the global from it races
		// anything that reassigns vtui.FrameManager meanwhile.
		showTimer = time.AfterFunc(delay, func() {
			uiFrames.PostTask(showDialog)
		})
	} else {
		// Preserve the existing immediate-dialog contract for callers that do
		// not opt into a delay.
		uiFrames.PostTask(showDialog)
	}

	ctx := vtui.RunAsync(func(ctx *vtui.TaskContext) {
		update := func(msg string, percent int) {
			ctx.RunOnUI(func() {
				if msg != "" {
					safeMsg := runewidth.Truncate(msg, 46, "...")
					lbl.SetText(safeMsg)
				}
				if percent >= 0 {
					pb.SetPercent(percent)
					dlg.SetProgress(percent)
				}
				vtui.FrameManager.Redraw()
			})
		}
		err := worker(ctx.Context, update)
		close(done)
		if showTimer != nil {
			showTimer.Stop()
		}
		ctx.RunOnUI(func() {
			if dialogShown {
				dlg.Close()
			}
			if onComplete != nil {
				onComplete(err)
			}
		})
	})
	taskCtxMu.Lock()
	taskCtx = ctx
	cancel := cancelPending
	taskCtxMu.Unlock()
	if cancel {
		ctx.Cancel()
	}
}
func (pf *PanelsFrame) RunAdvancedProgressTask(title string, forked bool, worker func(ctx context.Context, reporter vfs.TaskReporter) error, onComplete func(err error)) {
	dlg := NewFileOpProgressDialog(title)
	var taskCtx *vtui.TaskContext
	done := make(chan struct{})
	dialogShown := false // accessed only from UI tasks
	dlg.btnCancel.OnClick = func() { dlg.SetExitCode(1) }
	dlg.OnResult = func(code int) {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
	}

	reporter := newDialogReporter(dlg)

	uiFrames := vtui.FrameManager
	var showDialog func()
	showDialog = func() {
		select {
		case <-done:
			return
		default:
		}
		// Keep a password or other modal prompt reachable if the worker posts
		// it before this progress screen reaches the UI queue.
		if top := uiFrames.GetTopFrame(); top != nil && top.IsModal() {
			time.AfterFunc(50*time.Millisecond, func() {
				uiFrames.PostTask(showDialog)
			})
			return
		}
		if forked && pf != nil {
			clone := pf.Clone()
			uiFrames.AddScreen(clone)
			uiFrames.Push(dlg)
		} else {
			uiFrames.AddScreenHeadless(dlg)
		}
		dialogShown = true
	}
	uiFrames.PostTask(showDialog)

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
		err := worker(ctx.Context, reporter)
		close(done)
		ctx.RunOnUI(func() {
			reporter.Stop()
			if dialogShown {
				dlg.Close()
			}
			if onComplete != nil {
				onComplete(err)
			}
		})
	})
}

type progressTaskReporter struct {
	update func(msg string, percent int)
}

func (p *progressTaskReporter) UpdateScan(currentPath string, files, dirs int64) {
	p.update("Scanning...", 0)
}
func (p *progressTaskReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	p.update(fmt.Sprintf("%s: %s", action, filename), totalPct)
}
func (p *progressTaskReporter) IsCancelled() bool { return false }

func (pf *PanelsFrame) ExecuteDummyOp(mode int) {
	desc := "Dummy 5-minute operation"
	runFunc := func(ctx context.Context, reporter TaskReporter, anchor vtui.Frame) error {
		totalSteps := 300 // 5 minutes = 300 seconds
		for i := 1; i <= totalSteps; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(1 * time.Second)
			reporter.UpdateTransfer("Processing", fmt.Sprintf("File %d of %d", i, totalSteps), (i*100)/totalSteps, "Dummy", (i*100)/totalSteps, "1 item/s")
		}
		return nil
	}

	if mode == 0 {
		GlobalQueueManager.Enqueue(&QueueTask{
			Type: "Dummy",
			Desc: desc,
			Run:  runFunc,
			OnComplete: func() {
				showToast("Dummy operation finished successfully", 3*time.Second)
			},
		})
	} else {
		forked := (mode == 1)
		pf.RunProgressTask(" Processing... ", "Initializing...", forked, func(ctx context.Context, update func(msg string, percent int)) error {
			reporter := &progressTaskReporter{update: update}
			return runFunc(ctx, reporter, nil)
		}, func(err error) {
			if err == nil {
				top := vtui.FrameManager.GetTopFrame()
				vtui.ShowMessageOn(top, " Done ", "Dummy operation finished!", []string{"&Ok"})
			}
		})
	}
}

// toggleAltPanel drives the Ctrl+L / Ctrl+Q / (future) Ctrl+T
// keystrokes. Given a Kind and a factory, it closes the active-side
// alt if it already matches Kind, else closes the passive-side one
// if it matches, else creates a new alt on the passive side via the
// factory (fed the current active FileSystemPanel as source).
func (pf *PanelsFrame) toggleAltPanel(kind string, factory func(src *FileSystemPanel) AltPanel) {
	if !pf.showPanels {
		return
	}
	tryClose := func(a AltPanel) {
		if c, ok := a.(interface{ Close() }); ok {
			c.Close()
		}
	}
	opp := 1 - pf.activeIdx
	switch {
	case pf.altPanels[pf.activeIdx] != nil && pf.altPanels[pf.activeIdx].Kind() == kind:
		tryClose(pf.altPanels[pf.activeIdx])
		pf.altPanels[pf.activeIdx] = nil
	case pf.altPanels[opp] != nil && pf.altPanels[opp].Kind() == kind:
		tryClose(pf.altPanels[opp])
		pf.altPanels[opp] = nil
	default:
		if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
			if pf.altPanels[opp] != nil {
				tryClose(pf.altPanels[opp])
			}
			pf.altPanels[opp] = factory(fsp)
			// If the opposite side is currently hidden (Ctrl+F1/F2), un-hide
			// it — otherwise the alt panel installs into an invisible slot
			// and Ctrl+L / Ctrl+Q look like a no-op.
			if opp == 0 {
				pf.showLeftPanel = true
			} else {
				pf.showRightPanel = true
			}
		}
	}
	pf.ResizeConsole(pf.lastW, pf.lastH)
	vtui.FrameManager.HardRefresh()
}

func (pf *PanelsFrame) RefreshAll() {
	if pf == nil {
		return
	}
	for _, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			fsp.ReadDirectory()
		}
	}
}
func (pf *PanelsFrame) Message(title, msg string, buttons []string) int {
	resChan := make(chan int, 1)
	vtui.FrameManager.PostTask(func() {
		dlg := vtui.ShowMessage(title, msg, buttons)
		dlg.OnResult = func(code int) { resChan <- code }
	})
	return <-resChan
}

// InputBox prompts the user. The third argument is the text the field opens
// with, not a history bucket name -- matching Host.InputBox in the SDK, where
// the same value travels as InputBoxReq.Default.
func (pf *PanelsFrame) InputBox(title, prompt, defaultText string, callback func(string)) {
	vtui.FrameManager.PostTask(func() {
		vtui.InputBox(title, prompt, defaultText, callback)
	})
}

func (pf *PanelsFrame) Menu(title string, items []string, callback func(int)) {
	vtui.FrameManager.PostTask(func() {
		menu := vtui.NewVMenu(title)

		// Calculate dynamic width based on items and title
		maxW := runewidth.StringWidth(title) + 10
		for _, itm := range items {
			menu.AddItem(vtui.MenuItem{Text: itm})
			clean, _, _ := vtui.ParseAmpersandString(itm)
			w := runewidth.StringWidth(clean) + 8 // padding for markers and borders
			if w > maxW {
				maxW = w
			}
		}

		h := len(items) + 2
		maxH := 15 // Keep generic plugin menus compact on normal screens.
		if screenH := vtui.FrameManager.GetScreenHeight(); screenH > 0 && maxH > screenH {
			maxH = screenH
		}
		if maxH < 3 {
			maxH = 3
		}
		if h > maxH {
			h = maxH
		}

		// Center relative to the PanelsFrame size
		x := (pf.lastW - maxW) / 2
		y := (pf.lastH - h) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}

		menu.SetPosition(x, y, x+maxW-1, y+h-1)

		menu.OnAction = func(idx int) {
			menu.Close()
			if callback != nil {
				callback(idx)
			}
		}
		vtui.FrameManager.Push(menu)
	})
}

func (pf *PanelsFrame) syncPTYDirectory(path string, v vfs.VFS) bool {
	isWindowsShell := runtime.GOOS == "windows"
	sync := false
	if _, isOS := v.(*vfs.OSVFS); isOS {
		sync = true
	} else if vfsHasRemotePTY(v) {
		sync = true
		isWindowsShell = false
	}

	if !sync {
		return true
	}

	activePty := pf.getActivePTY()
	if activePty == nil {
		return false
	}

	// A VFS that knows what its own PTY speaks composes the sync line
	// itself. FISH+ against a Windows peer takes this branch so the
	// PTY (cmd.exe by default) sees a "cd /d \"C:\\...\" & rem f4_sync"
	// instead of the bash-shaped one, which cmd treats as a broken
	// argument and complains about at every step.
	if integ, ok := v.(vfs.PtyShellIntegration); ok {
		if seq := integ.PtyChangeDirCommand(path); len(seq) > 0 {
			pf.writePTY(activePty, seq)
		}
		return true
	}

	if isWindowsShell {
		pf.writePTY(activePty, []byte(fmt.Sprintf("cd /d \"%s\" & rem f4_sync\r", path)))
		pf.noteLocalShellLineSent(activePty)
	} else {
		sqPath := strings.ReplaceAll(path, "'", "'\\''")
		// "&& true f4_sync" instead of "# f4_sync": stock zsh ships with
		// interactive_comments off, so a trailing comment becomes extra cd
		// arguments ("cd: too many arguments"). true ignores its arguments
		// in every POSIX-ish shell including fish.
		pf.writePTY(activePty, []byte(fmt.Sprintf(" cd '%s' && true f4_sync\r", sqPath)))
	}
	return true
}

func vfsHasRemotePTY(v vfs.VFS) bool {
	if _, ok := v.(vfs.PtyProvider); !ok {
		return false
	}
	if availability, ok := v.(vfs.PtyAvailability); ok {
		return availability.PtyAvailable()
	}
	return true
}

func (pf *PanelsFrame) getActivePTYUnsafe() PtyBackend {
	if pf.remotePtys == nil {
		pf.remotePtys = make(map[vfs.VFS]PtyBackend)
	}

	var activeVfs vfs.VFS
	if fsp := pf.getActivePanel(); fsp != nil {
		activeVfs = fsp.vfs
	}

	if pp, ok := activeVfs.(vfs.PtyProvider); ok && vfsHasRemotePTY(activeVfs) {
		if pty, exists := pf.remotePtys[activeVfs]; exists {
			return pty
		}

		res, err := pp.OpenPty(pf.termView.Width, pf.termView.Height)
		if err == nil {
			pty := res.(PtyBackend)
			vtui.DebugLog("Created new remote PTY background session for VFS")
			pf.remotePtys[activeVfs] = pty

			// Give the VFS one chance to install shell settings before
			// anyone else writes to the PTY. FISH+ against a Windows
			// peer sends "prompt $E]133;D$E\$P$G" so cmd's own prompt
			// embeds an OSC 133 D marker on every command completion —
			// otherwise the panel frame never sees "command done" and
			// stays in terminal mode instead of returning to panels.
			if integ, ok := activeVfs.(vfs.PtyShellIntegration); ok {
				if init := integ.PtyInitSequence(); len(init) > 0 {
					pty.Write(init)
				}
			}

			go func() {
				buf := make([]byte, 32768) // Увеличен буфер
				for {
					n, readErr := pty.Read(buf)
					if readErr != nil {
						break
					}

					pf.ptyMutex.Lock()
					shouldProcess := (pf.getActivePTYUnsafe() == pty)
					pf.ptyMutex.Unlock()

					if shouldProcess {
						start := time.Now()
						pf.parser.Process(buf[:n])
						elapsed := time.Since(start)
						if elapsed > 10*time.Millisecond {
							vtui.DebugLog("PTY_PROFILE(Remote): Parsed %d bytes in %v", n, elapsed)
						}
						pf.terminalRedraw.request()
					}
				}
				pty.Close()
				pf.ptyMutex.Lock()
				delete(pf.remotePtys, activeVfs)
				pf.ptyMutex.Unlock()
				// The session that switched these on is gone and cannot
				// switch them off.
				pf.termView.ResetKeyboardProtocols()
			}()
			return pty
		}
	}
	return pf.pty
}

// activeReplyPTY names the shell that device queries should be answered
// into. Unlike getActivePTY it never opens a connection: a query arriving
// for a host we have no session with is dropped rather than made to dial
// one, and it must stay callable from the parser's read goroutines.
func (pf *PanelsFrame) activeReplyPTY() PtyBackend {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	var activeVfs vfs.VFS
	if fsp := pf.getActivePanel(); fsp != nil {
		activeVfs = fsp.vfs
	}
	if activeVfs != nil && vfsHasRemotePTY(activeVfs) {
		if pty, ok := pf.remotePtys[activeVfs]; ok && pty != nil {
			return pty
		}
	}
	return nil
}

func (pf *PanelsFrame) getActivePTY() PtyBackend {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	return pf.getActivePTYUnsafe()
}

// remotePTYInterruptTarget is a side-effect-free snapshot of the remote shell
// that currently owns Ctrl+C. In particular, discovering palette commands must
// not create a remote PTY merely to decide whether Interrupt is available.
type remotePTYInterruptTarget struct {
	panel    *FileSystemPanel
	pty      PtyBackend
	sequence string
}

func (target *remotePTYInterruptTarget) matches(other *remotePTYInterruptTarget) bool {
	return target != nil && other != nil &&
		target.panel == other.panel && sameRemotePTYBackend(target.pty, other.pty) &&
		target.sequence == other.sequence
}

func sameRemotePTYBackend(left, right PtyBackend) bool {
	typeOfLeft := reflect.TypeOf(left)
	return typeOfLeft != nil && typeOfLeft == reflect.TypeOf(right) && typeOfLeft.Comparable() && left == right
}

func (pf *PanelsFrame) currentRemotePTYInterruptTarget() *remotePTYInterruptTarget {
	if pf == nil || pf.closed || !pf.showPanels {
		return nil
	}
	fsp := pf.getActivePanel()
	if fsp == nil || fsp.vfs == nil || fsp.providerOpenTask != nil {
		return nil
	}
	integration, ok := fsp.vfs.(vfs.PtyShellIntegration)
	if !ok {
		return nil
	}
	sequence := integration.PtyInterrupt()
	if len(sequence) == 0 {
		return nil
	}

	pf.ptyMutex.Lock()
	pty := pf.remotePtys[fsp.vfs]
	pf.ptyMutex.Unlock()
	if pty == nil {
		return nil
	}
	return &remotePTYInterruptTarget{
		panel:    fsp,
		pty:      pty,
		sequence: string(sequence),
	}
}

// interruptRemotePTY writes the integration-defined interrupt sequence only
// when the same panel, VFS and live PTY still own the command. expected is nil
// for the physical Ctrl+C path and a discovery snapshot for palette callbacks.
func (pf *PanelsFrame) interruptRemotePTY(expected *remotePTYInterruptTarget) bool {
	target := pf.currentRemotePTYInterruptTarget()
	if target == nil || (expected != nil && !target.matches(expected)) {
		return false
	}
	_, _ = pf.writePTY(target.pty, []byte(target.sequence))
	return true
}

func (pf *PanelsFrame) GetTitle() string {
	if !pf.showPanels {
		if pf.executing {
			return "Terminal (executing)"
		}
		if pf.termView.Title != "" {
			return pf.termView.Title
		}
		return "Terminal"
	}

	path := ""
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		path = fsp.persistentPath()
		if fsp.providerOpenTask == nil {
			if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
				if prefix := tp.GetTitle(); prefix != "" {
					if !strings.HasPrefix(path, prefix+":") {
						path = prefix + ":" + path
					}
				}
			}
		}
	}

	if path != "" {
		return "Panels: " + path
	}
	return "Panels"
}

func (pf *PanelsFrame) GetWorkspaceTabTitle() string {
	if !pf.showPanels {
		title := "Terminal"
		if pf.executing && pf.workspaceCommandTitle != "" {
			title = pf.workspaceCommandTitle
		}
		return title
	}

	panelPath := func(panel Panel) string {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || fsp.vfs == nil {
			return "—"
		}
		path := fsp.persistentPath()
		if path == "" {
			return "."
		}
		if fsp.providerOpenTask == nil && fsp.vfs.IsAtRoot() {
			if provider, ok := fsp.vfs.(vfs.TitleProvider); ok {
				if title := provider.GetTitle(); title != "" {
					return title
				}
			}
			return path
		}
		if name := filepath.Base(path); name != "" && name != "." && name != string(os.PathSeparator) {
			return name
		}
		return path
	}

	return panelPath(pf.panels[0]) + " ─ " + panelPath(pf.panels[1])
}

func (pf *PanelsFrame) GetWorkspaceTabMarker() string {
	if pf.showPanels {
		return "P"
	}
	return "T"
}

// GetWorkspaceMenuInfo supplies the Screens popup with full panel paths. The
// compact tab title above intentionally uses only leaf directory names.
func (pf *PanelsFrame) GetWorkspaceMenuInfo() vtui.WorkspaceMenuInfo {
	if !pf.showPanels {
		title := "Terminal"
		if pf.executing && pf.workspaceCommandTitle != "" {
			title = pf.workspaceCommandTitle
		}
		return vtui.WorkspaceMenuInfo{Icon: "T", Primary: title}
	}

	panelPath := func(panel Panel) string {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || fsp.vfs == nil {
			return "—"
		}
		path := fsp.persistentPath()
		if fsp.providerOpenTask == nil {
			if provider, ok := fsp.vfs.(vfs.TitleProvider); ok {
				if title := strings.TrimSpace(provider.GetTitle()); title != "" {
					if path == "" || path == "." {
						return title
					}
					if strings.HasPrefix(path, title+":") {
						return path
					}
					return title + ":" + path
				}
			}
		}
		if path == "" {
			return "."
		}
		return path
	}

	return vtui.WorkspaceMenuInfo{
		Icon:      "P",
		Primary:   panelPath(pf.panels[0]),
		Secondary: panelPath(pf.panels[1]),
	}
}

// shellSingleQuote wraps s so a POSIX shell passes it through as one literal
// word, closing and reopening the quote around each embedded quote.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func workspaceCommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "Terminal"
	}

	executable := ""
	if command[0] == '"' || command[0] == '\'' {
		quote := command[0]
		if end := strings.IndexByte(command[1:], quote); end >= 0 {
			executable = command[1 : end+1]
		}
	}
	if executable == "" {
		if fields := strings.Fields(command); len(fields) > 0 {
			executable = fields[0]
		}
	}
	executable = strings.TrimSuffix(filepath.Base(executable), filepath.Ext(executable))
	switch strings.ToLower(executable) {
	case "python", "python3", "py":
		return "Python"
	}
	if executable == "" {
		return "Terminal"
	}
	return executable
}

func executeCapturedCommand(pf *PanelsFrame, action string, cmdStr string) {
	var dir string
	if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
		if _, isOS := fsp.vfs.(*vfs.OSVFS); isOS {
			dir = fsp.vfs.GetPath()
		}
	}

	if action == "clip" {
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(ctx.Context, "cmd.exe", "/c", cmdStr)
			} else {
				cmd = exec.CommandContext(ctx.Context, "sh", "-c", cmdStr)
			}
			if dir != "" {
				cmd.Dir = dir
			}

			out, err := cmd.CombinedOutput()
			ctx.RunOnUI(func() {
				if err != nil && len(out) == 0 {
					vtui.ShowMessage(" Error ", fmt.Sprintf("Execution failed:\n%v", err), []string{"&Ok"})
					return
				}
				vtui.SetClipboard(string(out))
				showToast("Command output copied to clipboard", 3*time.Second)
				pf.RefreshAll()
			})
		})
		return
	}

	pf.RunProgressTask(" Executing ", "Running: "+vtui.TruncateMiddle(cmdStr, 30), false, func(ctx context.Context, update func(msg string, percent int)) error {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd.exe", "/c", cmdStr)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
		}
		if dir != "" {
			cmd.Dir = dir
		}

		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			return err
		}

		vtui.FrameManager.PostTask(func() {
			tmpFile, err := os.CreateTemp("", "f4-capture-*.txt")
			if err != nil {
				vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
				return
			}
			tmpFile.Write(out)
			tmpPath := tmpFile.Name()
			tmpFile.Close()

			v := vfs.NewOSVFS(filepath.Dir(tmpPath))

			if action == "view" {
				vv, err := NewViewerView(context.Background(), v, tmpPath)
				if err == nil {
					vv.OnClose = func() { os.Remove(tmpPath) }
					showViewer(pf, vv, tmpPath)
				}
			} else {
				f, err := v.Open(context.Background(), tmpPath)
				if err == nil {
					showEditor(pf, v, tmpPath, f)
					if ev, _ := findOpenedEditor(v, tmpPath); ev != nil {
						ev.OnClose = func() { os.Remove(tmpPath) }
					}
				}
			}
		})
		return nil
	}, func(err error) {
		if err != nil && err != context.Canceled {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Execution failed:\n%v", err), []string{"&Ok"})
		}
		pf.RefreshAll()
	})
}

func (pf *PanelsFrame) Clone() *PanelsFrame {
	clone := NewPanelsFrame()
	if pf.lastW > 0 && pf.lastH > 0 {
		clone.ResizeConsole(pf.lastW, pf.lastH)
	}

	for i, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			cloneFsp, cloneOk := clone.panels[i].(*FileSystemPanel)
			if !cloneOk {
				clone.panels[i] = NewFileSystemPanel(fsp.X1, fsp.Y1, fsp.X2-fsp.X1+1, fsp.Y2-fsp.Y1+1, fsp.vfs.Clone())
				cloneFsp = clone.panels[i].(*FileSystemPanel)
			}
			// Stop the initial load triggered by NewPanelsFrame to prevent races
			if cloneFsp.cancelLoad != nil {
				cloneFsp.cancelLoad()
			}
			// Important: reset isLoading so the clone doesn't think it's still
			// waiting for that cancelled initial load.
			cloneFsp.isLoading = false
			cloneFsp.stopLoadingAnimation()

			cloneFsp.vfs.SetPath(fsp.vfs.GetPath())
			cloneFsp.SetViewMode(fsp.viewMode)
			cloneFsp.cursorIdx = fsp.cursorIdx
			cloneFsp.sortMode = fsp.sortMode
			cloneFsp.sortReverse = fsp.sortReverse

			cloneFsp.dirCache = make(map[dirCacheKey]dirCacheEntry)
			for k, v := range fsp.dirCache {
				cloneFsp.dirCache[k] = v
			}

			cloneFsp.selectedItems = make(map[string]bool)
			for k, v := range fsp.selectedItems {
				cloneFsp.selectedItems[k] = v
			}
			// Copying selectedItems without copying the path they
			// belong to would trip readDirectoryEx's "path changed
			// → drop selection" guard: the clone's fsp was
			// constructed against CWD, so its lastLoadedPath is
			// CWD, and the SetPath above moves it elsewhere. Bring
			// the tag over so the clone's next load recognises
			// the map as belonging to the current directory.
			cloneFsp.lastLoadedPath = fsp.lastLoadedPath

			// Copy entries immediately so the visual state is valid before async reload
			cloneFsp.entries = make([]*fileEntry, len(fsp.entries))
			for j, e := range fsp.entries {
				cloneFsp.entries[j] = &fileEntry{
					VFSItem:  e.VFSItem,
					Selected: e.Selected,
				}
			}
			cloneFsp.Refresh() // Populate table rows from copied entries

			cloneFsp.readDirectoryEx(true) // ВАЖНО: не удалять скопированные записи при первом чтении
			cloneFsp.table.SelectPos = fsp.table.SelectPos
			cloneFsp.table.SelectCol = fsp.table.SelectCol
			cloneFsp.table.TopPos = fsp.table.TopPos
		}
	}

	clone.activeIdx = pf.activeIdx
	clone.showKeyBar = pf.showKeyBar
	clone.showPanels = pf.showPanels
	clone.showLeftPanel = pf.showLeftPanel
	clone.showRightPanel = pf.showRightPanel
	clone.widePanel = pf.widePanel
	clone.wide = pf.wide
	clone.shellMode = pf.shellMode

	if pf.termView != nil && clone.termView != nil {
		clone.termView.CloneStateFrom(pf.termView)
	}
	if clone.lastW > 0 && clone.lastH > 0 {
		clone.ResizeConsole(clone.lastW, clone.lastH)
	} else {
		clone.updateMenuCheckmarks()
	}
	for i, p := range pf.panels {
		if source, ok := p.(*FileSystemPanel); ok {
			if target, ok := clone.panels[i].(*FileSystemPanel); ok {
				target.table.SelectPos = source.table.SelectPos
				target.table.SelectCol = source.table.SelectCol
				target.table.TopPos = source.table.TopPos
				target.cursorIdx = source.cursorIdx
			}
		}
	}
	return clone
}

func (pf *PanelsFrame) showPluginMenu() {
	items := pluginMenuItemsSnapshot()
	commands := pluginCommandsSnapshot(vfs.PluginCommandPanel, pf)
	if len(items) == 0 && len(commands) == 0 {
		vtui.ShowMessage(" Plugins ", "No plugins registered for F11 menu.", []string{"&Ok"})
		return
	}
	labels := make([]string, 0, len(items)+len(commands))
	for _, itm := range items {
		labels = append(labels, itm.Label)
	}
	for _, command := range commands {
		labels = append(labels, pluginCommandDisplayLabel(command))
	}
	pf.Menu(" Plugins ", labels, func(idx int) {
		switch {
		case idx >= 0 && idx < len(items):
			handler := items[idx].Handler
			vtui.FrameManager.PostTask(func() {
				handler(pf)
			})
		case idx >= len(items) && idx < len(items)+len(commands):
			commandID := commands[idx-len(items)].ID
			vtui.FrameManager.PostTask(func() {
				executeRegisteredPluginCommand(vfs.PluginCommandPanel, commandID, pf)
			})
		}
	})
}

func (pf *PanelsFrame) showDriveMenu(panelIdx int) {
	pf.showDriveMenuAt(panelIdx, pf.driveMenuDefaultPos(panelIdx))
}

// driveMenuDefaultPos returns the drive-menu row the cursor should land on
// when the menu opens. far2l positions the cursor on the drive the active
// panel currently shows; for f4 that means: if the panel is on a real
// filesystem drive that appears in the menu (e.g. a Windows drive letter),
// land on that drive, otherwise keep the historic default — the "Other panel"
// entry at row 0.
func (pf *PanelsFrame) driveMenuDefaultPos(panelIdx int) int {
	fsp, ok := pf.panels[panelIdx].(*FileSystemPanel)
	if !ok {
		return 0
	}
	osVFS, ok := fsp.vfs.(*vfs.OSVFS)
	if !ok {
		return 0
	}
	cur := osVFS.GetPath()
	for i, drv := range getPlatformDrives() {
		if driveMatchesPath(drv, cur) {
			// The "Other panel" and "Temporary panel" entries precede
			// platform drives.
			return i + 2
		}
	}
	return 0
}

// driveMatchesPath reports whether the platform drive entry drv is the one
// the path cur currently belongs to. Only the Windows drive-letter case is
// matched (the menu entries there carry letters, as the user expects); in
// posix/UNIX mode the default "Other panel" row stays selected.
func driveMatchesPath(drv DriveEntry, cur string) bool {
	if runtime.GOOS != "windows" || hostmode.Posix() {
		return false
	}
	vol := filepath.VolumeName(cur)
	if vol == "" {
		return false
	}
	return strings.HasPrefix(strings.ToUpper(drv.Name), strings.ToUpper(vol))
}

// showDriveMenuAt opens the drive menu with the cursor on selectPos. The
// bookmark keys reopen the menu at the row they acted on, the way far2l
// loops ChangeDiskMenu around its own Pos (panels/panel.cpp:168).
func (pf *PanelsFrame) showDriveMenuAt(panelIdx, selectPos int) {
	menu := vtui.NewVMenu(Msg("Drive.Title"))

	usedHotkeys := make(map[rune]bool)
	usedHotkeys['o'] = true // "Other panel"

	// 1. Other panel (focused by default)
	menu.AddItem(vtui.MenuItem{Text: Msg("Panel.Other"), UserData: func(fsp *FileSystemPanel) {
		otherFsp := pf.panels[1-panelIdx].(*FileSystemPanel)
		fsp.cancelProviderOpen()
		if fsp.vfs != nil {
			fsp.vfs.Close()
		}
		fsp.vfs = otherFsp.vfs.Clone()
		fsp.showCurrentVFSLoadingRows()
		fsp.ReadDirectory()
		pf.RefreshAll()
	}})

	// TempPanel is a native VFS panel, so it is available from the same
	// Alt+F1/Alt+F2 drive menu as far2l's plugin panels.
	menu.AddItem(vtui.MenuItem{Text: Msg("TempPanel.Drive"), UserData: func(fsp *FileSystemPanel) {
		pf.switchToVFS(fsp, newTempPanelVFS(nil, globalTempPanelStore, 0))
	}})

	// 2. Fixed platform paths (Root, Home)
	for _, drv := range getPlatformDrives() {
		factory := drv.Factory
		name := drv.Name
		if runtime.GOOS != "windows" {
			if strings.HasPrefix(name, "/") {
				name = "&" + name
				usedHotkeys['/'] = true
			} else if strings.HasPrefix(name, "~") {
				name = "&" + name
				usedHotkeys['~'] = true
			}
		} else {
			if len(name) >= 2 && name[1] == ':' {
				name = "&" + name
				usedHotkeys[unicode.ToLower(rune(name[0]))] = true
			}
		}

		menu.AddItem(vtui.MenuItem{Text: name, UserData: func(fsp *FileSystemPanel) {
			pf.switchToVFS(fsp, factory())
		}})
	}

	// 3. Folder bookmarks. far2l lists the assigned slots right here in
	// the same menu (panels/panel.cpp, AddBookmarkItems) with the slot
	// digit as the hotkey, so Alt+F1 followed by 6 lands on slot 6.
	// Unassigned slots are left out.
	bookmarkRows := map[int]int{} // menu row -> slot, for the keys below
	if set, err := LoadBookmarks(BookmarksFilePath()); err == nil {
		firstBookmark := true
		for i := range set {
			if set[i].IsEmpty() {
				continue
			}
			if firstBookmark {
				menu.AddSeparator()
				firstBookmark = false
			}
			bookmark := set[i]
			path := bookmark.Path
			usedHotkeys[rune('0'+i)] = true
			bookmarkRows[menu.GetItemCount()] = i
			menu.AddItem(vtui.MenuItem{
				Text: fmt.Sprintf("&%d  %s", i, escapeAmpersand(truncPathLeft(path, 64))),
				UserData: func(fsp *FileSystemPanel) {
					pf.navigateToBookmark(fsp, bookmark)
				},
			})
		}
	}

	// 4. Plugins & custom drives
	drives := driveRegistrySnapshot()
	if len(drives) > 0 {
		menu.AddSeparator()
		for _, drv := range drives {
			factory := drv.Factory

			// Clean name: strip existing hotkeys/numbering if any
			cleanName := drv.Name
			if idx := strings.Index(cleanName, ". "); idx != -1 {
				cleanName = cleanName[idx+2:]
			}
			cleanName = strings.ReplaceAll(cleanName, "&", "")

			// Smart hotkey assignment from clean name
			hotkeyAssigned := false
			var sb strings.Builder
			for _, r := range cleanName {
				rl := unicode.ToLower(r)
				if !hotkeyAssigned && unicode.IsLetter(r) && !usedHotkeys[rl] {
					sb.WriteRune('&')
					sb.WriteRune(r)
					usedHotkeys[rl] = true
					hotkeyAssigned = true
				} else {
					sb.WriteRune(r)
				}
			}

			menu.AddItem(vtui.MenuItem{Text: sb.String(), UserData: func(fsp *FileSystemPanel) {
				pf.switchToVFS(fsp, factory())
			}})
		}
	}

	// Обработка физических клавиш / и ~ (layout-independent)
	menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
		// far2l binds three keys on the bookmark rows of this menu
		// (panels/panel.cpp:544-600): Ins opens the bookmarks dialog, F4
		// opens it on the slot under the cursor, Del clears that slot.
		// The menu comes back afterwards, as it does there. On other rows
		// F4 and Del are left alone — far2l uses them for mount hotkeys
		// and unmounting, neither of which f4 has.
		if e.KeyDown && e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|
			vtinput.LeftAltPressed|vtinput.RightAltPressed|vtinput.ShiftPressed) == 0 {
			pos := menu.SelectPos
			slot, onBookmark := bookmarkRows[pos]
			reopen := func() { pf.showDriveMenuAt(panelIdx, pos) }

			switch e.VirtualKeyCode {
			case vtinput.VK_INSERT:
				// far2l opens the dialog from any row here, not just a
				// bookmark one, and always at the first slot.
				menu.Close()
				vtui.FrameManager.PostTask(func() { ShowBookmarksDialogAt(pf, 0, reopen) })
				return true
			case vtinput.VK_F4:
				if onBookmark {
					menu.Close()
					vtui.FrameManager.PostTask(func() { ShowBookmarksDialogAt(pf, slot, reopen) })
					return true
				}
			case vtinput.VK_DELETE:
				if onBookmark {
					pf.clearBookmarkSlot(slot, menu, reopen)
					return true
				}
			}
		}

		var targetIndex = -1
		if e.VirtualKeyCode == vtinput.VK_OEM_2 { // Клавиша /?
			for i, item := range menu.Items {
				if strings.Contains(item.Text, "/") {
					targetIndex = i
					break
				}
			}
		} else if e.VirtualKeyCode == vtinput.VK_OEM_3 { // Клавиша ~` (ё)
			for i, item := range menu.Items {
				if strings.Contains(item.Text, "~") {
					targetIndex = i
					break
				}
			}
		}

		if targetIndex != -1 {
			menu.SetSelectPos(targetIndex)
			// Симулируем Enter
			menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
			return true
		}
		return false
	}

	menu.SetSelectPos(selectPos)

	// Bookmark rows carry paths, so the box no longer fits a fixed width.
	w, h := 26, menu.GetItemCount()+2
	for _, it := range menu.Items {
		clean, _, _ := vtui.ParseAmpersandString(it.Text)
		if iw := runewidth.StringWidth(clean) + 6; iw > w {
			w = iw
		}
	}
	if pf.lastW > 0 && w > pf.lastW-4 {
		w = pf.lastW - 4
	}
	if pf.lastH > 0 && h > pf.lastH-2 {
		h = pf.lastH - 2
	}

	y := (pf.lastH - h) / 2
	x := pf.lastW/4 - w/2
	if panelIdx != 0 {
		x = pf.lastW*3/4 - w/2
	}
	if x+w > pf.lastW {
		x = pf.lastW - w
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	menu.SetPosition(x, y, x+w-1, y+h-1)

	menu.OnAction = func(idx int) {
		menu.Close()
		fsp, ok := pf.panels[panelIdx].(*FileSystemPanel)
		if !ok {
			return
		}

		if action, ok := menu.Items[idx].UserData.(func(*FileSystemPanel)); ok {
			action(fsp)
		}
	}
	vtui.FrameManager.Push(menu)
}

// clearBookmarkSlot empties one slot straight from the drive menu, which
// is what Del does there in far2l (panels/panel.cpp:594), then reopens
// the menu so the row is gone. The table is re-read first: another
// instance may have rewritten the file since the menu was built.
func (pf *PanelsFrame) clearBookmarkSlot(slot int, menu *vtui.VMenu, reopen func()) {
	file := BookmarksFilePath()
	set, err := LoadBookmarks(file)
	if err != nil {
		vtui.DebugLog("BOOKMARKS: load %q failed: %v", file, err)
		return
	}
	set.deleteAtSlot(slot)
	if err := SaveBookmarks(file, set); err != nil {
		vtui.DebugLog("BOOKMARKS: save %q failed: %v", file, err)
		return
	}
	menu.Close()
	vtui.FrameManager.PostTask(reopen)
}

func (pf *PanelsFrame) switchToVFS(fsp *FileSystemPanel, newVFS vfs.VFS) {
	if newVFS != nil {
		fsp.cancelProviderOpen()
		oldVFS := fsp.vfs
		if temp, ok := newVFS.(*TempPanelVFS); ok {
			// Keep the source VFS alive as TempPanel's parent. This is what
			// makes Ctrl+PgUp return to the exact panel directory instead of
			// reopening it, and is also important for remote VFSes whose Close
			// tears down their session.
			if temp.parent == nil && oldVFS != nil {
				temp.setParent(oldVFS, fsp.getRawSelectedName())
			}
		}
		keepOldVFS := false
		if temp, ok := newVFS.(*TempPanelVFS); ok {
			keepOldVFS = temp.parent != nil && sameVFSInstance(temp.parent, oldVFS)
		}
		if oldVFS != nil && !keepOldVFS {
			oldVFS.Close()
			pf.ptyMutex.Lock()
			if pty, ok := pf.remotePtys[oldVFS]; ok {
				pty.Close()
				delete(pf.remotePtys, oldVFS)
			}
			pf.ptyMutex.Unlock()
		}
		fsp.providerEntryName = ""
		fsp.vfs = newVFS
		fsp.showCurrentVFSLoadingRows()
		fsp.ReadDirectory()
		pf.RefreshAll()
	}
}
func (pf *PanelsFrame) navigateToBookmark(fsp *FileSystemPanel, bookmark Bookmark) bool {
	return pf.NavigateToPath(fsp, expandPathEnv(bookmark.Path))
}

func (pf *PanelsFrame) NavigateToPath(fsp *FileSystemPanel, targetPath string) bool {
	if targetPath == "" {
		return false
	}
	// An explicit command/history navigation supersedes a provider mount that
	// has not installed its child VFS yet.
	providerOpenCanceled := fsp.providerOpenTask != nil
	fsp.cancelProviderOpen()

	// 1. Handle "cd .." at the root of a nested VFS (e.g. escaping an archive)
	if targetPath == ".." && fsp.vfs.IsAtRoot() && fsp.vfs.ParentVFS() != nil {
		parent := fsp.vfs.ParentVFS()
		oldPath := fsp.vfs.GetPath()
		parentSelection := ""
		if temp, ok := fsp.vfs.(*TempPanelVFS); ok {
			parentSelection = temp.parentSelection
		}

		fsp.vfs.Close()
		pf.ptyMutex.Lock()
		if pty, ok := pf.remotePtys[fsp.vfs]; ok {
			pty.Close()
			delete(pf.remotePtys, fsp.vfs)
		}
		pf.ptyMutex.Unlock()

		fsp.vfs = parent
		fsp.showCurrentVFSLoadingRows()
		if parentSelection != "" {
			fsp.pendingSelection = parentSelection
		} else if fsp.providerEntryName != "" {
			fsp.pendingSelection = fsp.providerEntryName
			fsp.providerEntryName = ""
		} else {
			fsp.pendingSelection = fsp.vfs.Base(oldPath)
		}
		fsp.ReadDirectory()
		return true
	}

	// A provider-owned visual path (for example Account:\Folder) must be
	// restored before OS path probing. This keeps bookmarks and folder history
	// entirely user-facing while allowing the provider to translate the path to
	// its internal object identity in the asynchronous Open call.
	if fsp.vfs.IsAbs(targetPath) {
		if err := fsp.setKnownDirectoryPath(targetPath); err == nil {
			fsp.pendingSelection = ".."
			fsp.ReadDirectory()
			return true
		}
	}
	if provider := vfs.FindStandaloneProvider(context.Background(), fsp.vfs, targetPath); provider != nil {
		sourceVFS := fsp.vfs
		return fsp.openVFSAsync(
			targetPath,
			func(ctx context.Context) (vfs.VFS, error) {
				return provider.Open(ctx, sourceVFS, targetPath)
			},
			func(newVFS vfs.VFS) { pf.switchToVFS(fsp, newVFS) },
			func(err error) {
				if isArchiveProvider(provider) {
					vtui.ShowMessage(" Open Error ", fmt.Sprintf("Failed to open %s:\n%v", targetPath, err), []string{"&Ok"})
				} else {
					vtui.ShowMessage(" Connection Error ", fmt.Sprintf("Failed to open %s:\n%v", targetPath, err), []string{"&Ok"})
				}
			},
		)
	}

	// 2. Handle absolute paths. It could be an OS path, or a path deep inside an archive.
	if filepath.IsAbs(targetPath) || filepath.VolumeName(targetPath) != "" {
		// First, check if it's a regular OS directory
		st, err := os.Stat(targetPath)
		if err == nil && st.IsDir() {
			newVfs := vfs.NewOSVFS(targetPath)
			if err := newVfs.SetPath(targetPath); err == nil {
				fsp.pendingSelection = ".."
				pf.switchToVFS(fsp, newVfs)
				return true
			}
		}

		// If Stat failed with permission denied on a Windows junction (e.g. "Documents and Settings"),
		// still try SetPath — it may resolve the target via Readlink.
		if err != nil && runtime.GOOS == "windows" && os.IsPermission(err) {
			newVfs := vfs.NewOSVFS(targetPath)
			if err := newVfs.SetPath(targetPath); err == nil {
				fsp.pendingSelection = ".."
				pf.switchToVFS(fsp, newVfs)
				return true
			}
		}

		// If it is a file, it could be an archive itself!
		if err == nil && !st.IsDir() {
			osvfs := vfs.NewOSVFS(filepath.Dir(targetPath))
			if provider := vfs.FindProvider(context.Background(), osvfs, targetPath); provider != nil {
				arcVFS, err := provider.Open(context.Background(), osvfs, targetPath)
				if err == nil {
					if err := arcVFS.SetPath(targetPath); err == nil {
						pf.switchToVFS(fsp, arcVFS)
						return true
					}
					arcVFS.Close()
				}
			}
		}

		// It might be a path inside an archive. Walk up the path to find the archive file.
		current := targetPath
		for {
			parentDir := filepath.Dir(current)
			if parentDir == current || parentDir == "." || parentDir == string(filepath.Separator) || parentDir == "" {
				break
			}
			// Windows root check
			if len(current) == 3 && current[1] == ':' && current[2] == '\\' {
				break
			}
			current = parentDir

			st, err := os.Stat(current)
			if err == nil {
				if !st.IsDir() {
					// We found a file, maybe it's an archive!
					osvfs := vfs.NewOSVFS(filepath.Dir(current))
					if provider := vfs.FindProvider(context.Background(), osvfs, current); provider != nil {
						arcVFS, err := provider.Open(context.Background(), osvfs, current)
						if err == nil {
							// Successfully opened the archive, now try to set the internal path
							if err := arcVFS.SetPath(targetPath); err == nil {
								pf.switchToVFS(fsp, arcVFS)
								return true
							}
							arcVFS.Close()
						}
					}
				}
				break // We found something that exists, but if it wasn't a valid archive, the subpath is invalid.
			}
		}
	}

	// 3. Persistent virtual-file-system URIs are opened through the provider
	// registered during synchronous built-in plugin initialization. Opening is
	// asynchronous, but a recognized URI counts as accepted immediately so a
	// caller never feeds it to the current OS VFS as a fallback path.
	if provider := vfs.FindURIProvider(targetPath); provider != nil {
		sourceVFS := fsp.vfs
		return fsp.openVFSAsync(
			targetPath,
			func(ctx context.Context) (vfs.VFS, error) {
				return provider.OpenURI(ctx, sourceVFS, targetPath)
			},
			func(newVFS vfs.VFS) {
				pf.switchToVFS(fsp, newVFS)
			},
			func(err error) {
				vtui.ShowMessage(" Connection Error ", fmt.Sprintf("Failed to open %s:\n%v", targetPath, err), []string{"&Ok"})
			},
		)
	}
	if vfs.IsURIPath(targetPath) && !fsp.vfs.IsAbs(targetPath) {
		// The URI is syntactically valid but its plugin is unavailable. Do not
		// let SetPath reinterpret it as a relative path in the current VFS.
		if providerOpenCanceled {
			fsp.isLoading = false
			fsp.stopLoadingAnimation()
			fsp.updateTitle(nil)
			vtui.FrameManager.Redraw()
		}
		return false
	}

	// 4. Change path on the current VFS. Remote VFSes may take the optimistic,
	// no-I/O route here; ReadDirectory validates the target in the background
	// while a cached view can become interactive immediately.
	if err := fsp.setKnownDirectoryPath(targetPath); err == nil {
		fsp.pendingSelection = ".."
		fsp.ReadDirectory()
		return true
	}

	if providerOpenCanceled {
		// No replacement VFS/read was started, so restore the manager panel's
		// loading state after superseding its pending provider transition.
		fsp.isLoading = false
		fsp.stopLoadingAnimation()
		fsp.updateTitle(nil)
		vtui.FrameManager.Redraw()
	}
	return false
}

func sameFolderHistoryPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	uriA, aIsURI := normalizedURIIdentity(a)
	uriB, bIsURI := normalizedURIIdentity(b)
	if aIsURI || bIsURI {
		return aIsURI && bIsURI && uriA == uriB
	}
	isVisualVirtual := func(value string) bool {
		colon := strings.IndexByte(value, ':')
		return colon > 1 && len(value) > colon+1 && (value[colon+1] == '/' || value[colon+1] == '\\')
	}
	if isVisualVirtual(a) || isVisualVirtual(b) {
		if !isVisualVirtual(a) || !isVisualVirtual(b) {
			return false
		}
		normalize := func(value string) string {
			return nativeVisualCachePath(value)
		}
		return normalize(a) == normalize(b)
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// folderHistoryStep resolves a move in newest-first provider storage.
// direction < 0 means Back (towards older entries), direction > 0 means
// Forward (towards newer entries).
func folderHistoryStep(history []string, current string, pos, direction int) (int, string, bool) {
	if len(history) == 0 || direction == 0 {
		return pos, "", false
	}
	if pos < 0 || pos >= len(history) || !sameFolderHistoryPath(history[pos], current) {
		pos = -1
		for i, path := range history {
			if sameFolderHistoryPath(path, current) {
				pos = i
				break
			}
		}
	}

	target := pos - 1
	if direction < 0 {
		target = pos + 1
		if pos == -1 {
			target = 0
		}
	} else if pos == -1 {
		return pos, "", false
	}
	if target < 0 || target >= len(history) {
		return pos, "", false
	}
	return target, history[target], true
}

func (pf *PanelsFrame) folderHistoryPanelIndex(fsp *FileSystemPanel) int {
	for i, panel := range pf.panels {
		if panelFSP, ok := panel.(*FileSystemPanel); ok && panelFSP == fsp {
			return i
		}
	}
	return pf.activeIdx
}

// navigateAvailableFolderHistory tries history entries in storage-index order
// until one can actually be opened. A positive step walks towards older MRU
// entries; a negative step walks towards newer entries.
func (pf *PanelsFrame) navigateAvailableFolderHistory(fsp *FileSystemPanel, history []string, startPos, step int) bool {
	if step == 0 {
		return false
	}
	for pos := startPos; pos >= 0 && pos < len(history); pos += step {
		path := history[pos]
		if fsp == nil || path == "" {
			continue
		}
		fsp.fastFindMode = false
		fsp.fastFindStr = ""
		fsp.suppressNextFolderHistory(path)
		if !pf.NavigateToPath(fsp, path) {
			fsp.clearFolderHistorySuppression()
			continue
		}
		idx := pf.folderHistoryPanelIndex(fsp)
		if fsp.providerOpenTask != nil && sameFolderHistoryPath(fsp.providerOpenTarget, path) {
			historySnapshot := append([]string(nil), history...)
			pendingPos := pos
			fsp.providerOpenResult = func(success bool) bool {
				if success {
					if idx >= 0 && idx < len(pf.folderHistoryPos) {
						pf.folderHistoryPos[idx] = pendingPos
					}
					return false
				}
				fsp.clearFolderHistorySuppression()
				return pf.navigateAvailableFolderHistory(fsp, historySnapshot, pendingPos+step, step)
			}
			return true
		}
		if idx >= 0 && idx < len(pf.folderHistoryPos) {
			pf.folderHistoryPos[idx] = pos
		}
		return true
	}
	return false
}

func (pf *PanelsFrame) moveFolderHistory(fsp *FileSystemPanel, direction int) bool {
	if fsp == nil || vtui.GlobalHistoryProvider == nil {
		return false
	}
	history := vtui.GlobalHistoryProvider.LoadHistory("folders")
	idx := pf.folderHistoryPanelIndex(fsp)
	pos := -1
	if idx >= 0 && idx < len(pf.folderHistoryPos) {
		pos = pf.folderHistoryPos[idx]
	}
	// During a cache-first cross-provider restore, the panel already presents
	// providerOpenTarget while the source VFS remains installed until the
	// asynchronous mount succeeds. History must follow the presented/persisted
	// location; using the source VFS here skips that source entry on Alt+Left.
	current := fsp.persistentPath()
	targetPos, _, ok := folderHistoryStep(history, current, pos, direction)
	if !ok {
		return false
	}
	step := -1
	if direction < 0 {
		step = 1
	}
	return pf.navigateAvailableFolderHistory(fsp, history, targetPos, step)
}

// parseDirChangeCommand recognizes the directory-change commands the command
// line intercepts itself rather than handing to the shell: "cd <path>",
// "chdir <path>", "cd /d <path>" (Windows), "cd..", "cd\\", "cd/" and bare
// drive letters ("C:", "D:\\") on Windows. It returns the target path with
// surrounding quotes removed. The user menu uses the same test so that a
// "cd" line inside a multi-command menu item moves the panel exactly like a
// "cd" typed on the command line.
func parseDirChangeCommand(trimmedCmd string) (targetPath string, ok bool) {
	lowerCmd := strings.ToLower(trimmedCmd)

	// Drive letter changes (e.g., "C:", "D:\") on Windows
	if runtime.GOOS == "windows" && len(trimmedCmd) >= 2 && len(trimmedCmd) <= 3 && trimmedCmd[1] == ':' {
		if lowerCmd[0] >= 'a' && lowerCmd[0] <= 'z' {
			targetPath = trimmedCmd
			if len(trimmedCmd) == 2 {
				targetPath += string(os.PathSeparator)
			}
			return targetPath, true
		}
		return "", false
	}

	if strings.HasPrefix(lowerCmd, "cd ") || strings.HasPrefix(lowerCmd, "chdir ") || (runtime.GOOS == "windows" && strings.HasPrefix(lowerCmd, "cd /d ")) {
		prefixLen := 3
		if strings.HasPrefix(lowerCmd, "cd /d ") {
			prefixLen = 6
		} else if strings.HasPrefix(lowerCmd, "chdir ") {
			prefixLen = 6
		}
		targetPath = strings.TrimSpace(trimmedCmd[prefixLen:])
		// Remove quotes if user typed: cd "C:\My Folder" or cd '/tmp/a b'
		if len(targetPath) >= 2 && targetPath[0] == '\'' && targetPath[len(targetPath)-1] == '\'' {
			targetPath = targetPath[1 : len(targetPath)-1]
			targetPath = strings.ReplaceAll(targetPath, "'\\''", "'")
		} else if len(targetPath) >= 2 && targetPath[0] == '"' && targetPath[len(targetPath)-1] == '"' {
			targetPath = targetPath[1 : len(targetPath)-1]
		}
		return targetPath, true
	}
	if lowerCmd == "cd.." || lowerCmd == "cd .." {
		return "..", true
	}
	if lowerCmd == "cd\\" || lowerCmd == "cd/" {
		return string(os.PathSeparator), true
	}
	return "", false
}

func expandPathEnv(s string) string {
	s = expandEnvironmentVariables(s)
	if s != "~" && (len(s) <= 1 || s[0] != '~' || (s[1] != '/' && s[1] != '\\')) {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	if s == "~" {
		return home
	}
	return filepath.Join(home, s[2:])
}

func expandEnvironmentVariables(s string) string {
	var buf strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '$':
			start, end := i+1, i+1
			if start < len(s) && s[start] == '{' {
				start++
				if close := strings.IndexByte(s[start:], '}'); close >= 0 {
					end = start + close
					if end > start {
						if value, ok := os.LookupEnv(s[start:end]); ok {
							buf.WriteString(value)
						} else {
							buf.WriteString(s[i : end+1])
						}
						i = end + 1
						continue
					}
				}
			} else {
				for end < len(s) && isEnvironmentVariableChar(s[end]) {
					end++
				}
				if end > start {
					if value, ok := os.LookupEnv(s[start:end]); ok {
						buf.WriteString(value)
					} else {
						buf.WriteString(s[i:end])
					}
					i = end
					continue
				}
			}
		case '%':
			if close := strings.IndexByte(s[i+1:], '%'); close > 0 {
				end := i + close + 1
				if value, ok := os.LookupEnv(s[i+1 : end]); ok {
					buf.WriteString(value)
				} else {
					buf.WriteString(s[i : end+1])
				}
				i = end + 1
				continue
			}
		}
		buf.WriteByte(s[i])
		i++
	}
	return buf.String()
}

func isEnvironmentVariableChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
