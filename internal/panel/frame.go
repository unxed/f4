package panel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"

	"github.com/mattn/go-runewidth"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// FindPanelsFrame locates the panels frame of the active screen, if any.
func FindPanelsFrame() *PanelsFrame {
	if vtui.FrameManager == nil {
		return nil
	}
	if pf, ok := vtui.FrameManager.GetTopFrame().(*PanelsFrame); ok {
		return pf
	}
	return FindPanelsFrameAnyScreen()
}

func (pf *PanelsFrame) GetActivePanelVFS() vfs.VFS  { return pf.Active().(*FileSystemPanel).Vfs }
func (pf *PanelsFrame) GetPassivePanelVFS() vfs.VFS { return pf.Passive().(*FileSystemPanel).Vfs }
func (pf *PanelsFrame) GetSelectedNames() []string {
	return pf.Active().(*FileSystemPanel).GetSelectedNames()
}
func (pf *PanelsFrame) GetMarkedNames() []string {
	if panel := pf.GetActivePanel(); panel != nil {
		return panel.GetMarkedNames()
	}
	return nil
}
func (pf *PanelsFrame) ReplaceMarkedNames(names []string) {
	if panel := pf.GetActivePanel(); panel != nil {
		panel.ReplaceMarkedNames(names)
	}
}
func (pf *PanelsFrame) GetSelectedName() string {
	return pf.Active().(*FileSystemPanel).GetSelectedName()
}
func (pf *PanelsFrame) SetPendingSelection(name string) {
	if fsp := pf.GetActivePanel(); fsp != nil {
		fsp.PendingSelection = name
	}
}

func (pf *PanelsFrame) AddCommandHistory(cmd string) {
	pf.CmdLine.Edit.AddHistory(cmd)
	hp, isF4 := vtui.GlobalHistoryProvider.(*history.F4HistoryProvider)
	if !isF4 {
		if vtui.GlobalHistoryProvider != nil {
			vtui.GlobalHistoryProvider.SaveHistory("cmdline", pf.CmdLine.Edit.History)
		}
		return
	}

	rich := hp.LoadRichHistory("cmdline")
	if len(rich) == 0 {
		rich = history.RecordsFromNames(pf.CmdLine.Edit.History)
	}
	var newRich []history.HistoryRecord
	curDir := ""
	if fsp := pf.GetActivePanel(); fsp != nil {
		curDir = fsp.Vfs.GetPath()
	}

	newRich = append(newRich, history.HistoryRecord{
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

	limit := pf.CmdLine.Edit.HistoryLimit
	if limit <= 0 {
		limit = 100
	}
	newRich = history.LimitRichHistory(newRich, limit)
	hp.SaveRichHistory("cmdline", newRich)

	var strHist []string
	for _, r := range newRich {
		strHist = append(strHist, r.Name)
	}
	pf.CmdLine.Edit.History = strHist
}
func (pf *PanelsFrame) InsertPathToCmdLine(path string) {
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
		txt := pf.CmdLine.Edit.GetText()
		if len(txt) > 0 && txt[len(txt)-1] != ' ' {
			pf.CmdLine.InsertString(" ")
		}
		pf.CmdLine.InsertString(path)
	}
}

// HandlePanelPathEditHotkey inserts a panel path into the focused dialog edit.
// It lives in the frame-level event filter because modal dialogs otherwise
// consume Ctrl+[ and Ctrl+] before PanelsFrame can see them.
func HandlePanelPathEditHotkey(e *vtinput.InputEvent) bool {
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
		panel = pf.VisualLeftFSP()
	} else {
		panel = pf.VisualRightFSP()
	}
	if panel == nil || panel.Vfs == nil || panel.Vfs.GetPath() == "" {
		return false
	}
	edit.InsertString(panel.Vfs.GetPath())
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
	Panels  [2]Panel
	DragOut dragOutState
	// externalUIRunner is normally nil, which selects the real desktop
	// launcher. Tests install a per-frame recorder instead of spawning native
	// Explorer/association windows.
	ExternalUIRunner externalUICommandRunner
	// altPanels[i] holds an alternate view (Info / Quick view / Tree)
	// covering slot i's file panel. When non-nil it's rendered in
	// place of panels[i]; panels[i] stays alive underneath and is
	// still the "logical" panel for command dispatch. Alt panels
	// never take focus (see AltPanel in info_panel.go).
	AltPanels             [2]AltPanel
	ActiveIdx             int    // 0 for left, 1 for right
	FolderHistoryPos      [2]int // position in provider's newest-first folder history
	Executing             bool
	afterExecution        func() // run once by endExecution; queues the next user-menu step
	ShellPromptReady      bool
	ignoreNextPrompt      bool
	ReturnToPanels        bool
	CmdSession            *cmdShellSession // local cmd.exe completion tracking (Windows)
	workspaceCommandTitle string

	MenuBar *vtui.MenuBar
	CmdLine *cmdline.CommandLine
	KeyBar  *vtui.KeyBar

	ShowKeyBar     bool
	ShowPanels     bool
	ShowLeftPanel  bool
	ShowRightPanel bool
	Wide           bool
	WidePanel      int // -1 for normal split, 0/1 for the slot occupying the full width
	// panelMouseCapture owns a complete button-down/move/release gesture.
	// Without it, dragging a row across the split can accidentally start the
	// other panel's scrollbar (or vice versa).
	PanelMouseCapture Panel
	middleMouseDown   bool
	LastW             int
	LastH             int

	// Panel geometry offsets, adjusted by Ctrl+Left/Right (width) and
	// Ctrl+Up/Down (height). Names and semantics match far2l's [Layout]:
	// widthDecrement > 0 grows the right panel, < 0 grows the left;
	// leftHeightDecrement / rightHeightDecrement >= 0 shrink the
	// corresponding panel from the bottom, growing the terminal area
	// above it. Ctrl+Up/Down bumps both symmetrically for now — the
	// asymmetric Ctrl+Shift+Up/Down handler will come as a follow-up
	// PR, which is why the fields are split already.
	WidthDecrement       int
	LeftHeightDecrement  int
	RightHeightDecrement int

	// Integrated Terminal
	Pty            terminal.PtyBackend
	RemotePtys     map[vfs.VFS]terminal.PtyBackend
	PtyMutex       sync.Mutex
	TermView       *terminal.TerminalView
	Parser         *terminal.AnsiParser
	terminalRedraw *terminal.TerminalRedrawScheduler

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

	LastAutoRefresh    time.Time
	LastKey            rune
	lastKeyEvent       time.Time
	CommandLineFocused bool

	LastPtyPath string
	LastPtyVFS  vfs.VFS
	Closed      bool

	ShellMode         terminal.ShellMode
	HostConsoleActive bool
	hostConsoleMu     sync.Mutex
	lastOverlayDraw   time.Time

	// Terminal mouse-selection state. Kept in PanelsFrame because
	// mouse routing lives here; the highlight and text extraction
	// live on the terminal.TerminalView itself.
	termSelDragging bool      // LMB gesture is waiting for its release
	termSelEscHeld  bool      // Esc key-down dismissed a highlight; swallow its key-up
	termSelClickN   int       // 1 / 2 / 3 for triple-click detection
	termSelClickAt  time.Time // time of the last click
	termSelClickX   int
	termSelClickY   int
}

func (pf *PanelsFrame) Left() Panel  { return pf.Panels[0] }
func (pf *PanelsFrame) Right() Panel { return pf.Panels[1] }

// visualLeftFSP / visualRightFSP return the file panels resolved by
// their on-screen X-position rather than slot index. The Ctrl+U
// panel swap re-assigns pf.panels[0]/[1] but keeps the two frames
// where the user sees them, so index-based routing sends Ctrl+[/]
// to the wrong side after a swap. In single-panel mode the visible
// panel occupies both visual sides, so both resolvers return it.
func (pf *PanelsFrame) VisualLeftFSP() *FileSystemPanel {
	a, _ := pf.Panels[0].(*FileSystemPanel)
	b, _ := pf.Panels[1].(*FileSystemPanel)
	if pf.ShowPanels && pf.ShowLeftPanel != pf.ShowRightPanel {
		if pf.ShowLeftPanel {
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

func (pf *PanelsFrame) VisualRightFSP() *FileSystemPanel {
	a, _ := pf.Panels[0].(*FileSystemPanel)
	b, _ := pf.Panels[1].(*FileSystemPanel)
	if pf.ShowPanels && pf.ShowLeftPanel != pf.ShowRightPanel {
		if pf.ShowLeftPanel {
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
func (pf *PanelsFrame) Active() Panel  { return pf.Panels[pf.ActiveIdx] }
func (pf *PanelsFrame) Passive() Panel { return pf.Panels[1-pf.ActiveIdx] }

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{ActiveIdx: 1, WidePanel: -1, FolderHistoryPos: [2]int{-1, -1}}
	pf.terminalRedraw = terminal.NewTerminalRedrawScheduler(func() { vtui.FrameManager.Redraw() })
	pf.SetHelp("Panels")
	pf.ShowKeyBar = true
	pf.ShowPanels = true
	pf.lastShowPanels = true
	pf.ShowLeftPanel = true
	pf.ShowRightPanel = true
	pf.WidthDecrement = config.App.WidthDecrement
	pf.LeftHeightDecrement = config.App.LeftHeightDecrement
	pf.RightHeightDecrement = config.App.RightHeightDecrement
	pf.ShellMode = terminal.ResolveShellMode(terminal.ShellModeConfig{
		ConsoleMode:      config.App.ConsoleMode,
		ConsoleOverlayUI: config.App.ConsoleOverlayUI,
	})
	vtui.DebugLog("SHELL: mode=%s cfg.ConsoleMode=%q cfg.ConsoleOverlayUI=%v view=%s backend=%q",
		pf.ShellMode, config.App.ConsoleMode, config.App.ConsoleOverlayUI,
		terminal.ConsoleViewStyleFor(pf.ShellMode), terminal.SelectedTTYBackend)

	pf.MenuBar = vtui.NewMenuBar(nil)
	pf.MenuBar.SetOwner(pf)
	pf.MenuBar.Items = pf.BuildMenuItems()
	// We no longer need pf.menuBar.OnCommand for routing!
	pf.CmdLine = cmdline.NewCommandLine(i18n.Msg("Panels.Prompt"))
	if config.App.NavigationMode == config.NavigationSearchFirst {
		pf.CmdLine.SetFocus(false)
	}
	pf.CmdLine.Edit.HistoryID = "cmdline"
	if vtui.GlobalHistoryProvider != nil {
		pf.CmdLine.Edit.History = vtui.GlobalHistoryProvider.LoadHistory("cmdline")
	}
	pf.KeyBar = vtui.NewKeyBar()
	pf.KeyBar.SetOwner(pf)

	pf.TermView = terminal.NewTerminalView(80, 24)
	pf.TermView.OnBusyChange = func(busy bool) {
		localShell := pf.localShellIsActive()
		if localShell {
			pf.noteLocalShellBusy(busy)
		}
		// Use PostTask to ensure state changes happen on the UI thread
		vtui.FrameManager.PostTask(func() {
			if busy {
				pf.Executing = true
			} else {
				pf.ShellPromptReady = true
				ignoredPrompt := pf.Executing && pf.ignoreNextPrompt
				if ignoredPrompt {
					// A command can be entered before the initial prompt has
					// finished crossing ConPTY. That prompt belongs to shell
					// startup, not to the command just sent; consuming it as
					// completion would return to panels while a batch file is
					// still running.
					pf.ignoreNextPrompt = false
				}
				if pf.Executing && !ignoredPrompt {
					pf.endExecution()
				}
			}
			if localShell && !busy {
				pf.catchUpProcessEnvironment(true)
			}
		})
	}
	if runtime.GOOS == "windows" {
		pf.CmdSession = newCmdShellSession(pf)
		pf.TermView.OnShellMark = func(mark string, snap terminal.PromptSnapshot) {
			if pf.localShellIsActive() {
				pf.CmdSession.handleMark(mark, snap)
			}
		}
		logWindowsReflowRemoved()
	}
	// Parser will be fully initialized in initPTY once pty is ready
	pf.InitPTY()
	pf.TermView.Pty = pf.Pty
	installPanelDropTarget(pf)

	return pf
}

func (pf *PanelsFrame) SearchFirstMode() bool {
	return config.App.NavigationMode == config.NavigationSearchFirst
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
func (pf *PanelsFrame) SetCommandLineFocus(focused bool) {
	if !pf.SearchFirstMode() {
		return
	}
	pf.CommandLineFocused = focused
	pf.CmdLine.SetFocus(focused)
	for i, panel := range pf.Panels {
		if panel == nil {
			continue
		}
		panel.SetFocus(i == pf.ActiveIdx && !focused)
		if fsp, ok := panel.(*FileSystemPanel); ok {
			fsp.showInactiveCursor = i == pf.ActiveIdx && focused
		}
		if pf.AltPanels[i] != nil {
			pf.AltPanels[i].SetFocus(i == pf.ActiveIdx && !focused)
		}
	}
	vtui.FrameManager.Redraw()
}

// insertSelectedFileName is shared by the bindable Ctrl+Enter action and the
// frame-level safety net for a key event that bypasses hotkey dispatch. Keeping
// the operation here prevents a modified Enter from ever degrading into plain
// directory activation.
func (pf *PanelsFrame) InsertSelectedFileName() bool {
	fsp := pf.GetActivePanel()
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
	pf.CmdLine.InsertString(name)
	return true
}

// applyNavigationMode resets transient focus when the setting is changed.
func (pf *PanelsFrame) ApplyNavigationMode() {
	pf.CommandLineFocused = false
	if pf.SearchFirstMode() {
		pf.CmdLine.SetFocus(false)
		pf.SetCommandLineFocus(false)
		return
	}
	pf.CmdLine.SetFocus(true)
	for i, panel := range pf.Panels {
		if panel != nil {
			panel.SetFocus(i == pf.ActiveIdx)
		}
		if fsp, ok := panel.(*FileSystemPanel); ok {
			fsp.showInactiveCursor = false
		}
	}
}
func IsAIPanel(panel Panel) bool {
	if fsp, ok := panel.(*FileSystemPanel); ok && fsp != nil && fsp.Vfs != nil {
		if tp, ok := fsp.Vfs.(vfs.TitleProvider); ok {
			return tp.GetTitle() == "ai"
		}
	}
	return false
}

// leftMenu builds the custom side menu for the left panel. View and
// sort modes act on a fixed side through Cm commands, so they stay
// command-routed rather than generated from the action registry.
func (pf *PanelsFrame) LeftMenu() vtui.MenuBarItem {
	if IsAIPanel(pf.Panels[0]) {
		return vtui.MenuBarItem{Label: "&" + i18n.Msg("Menu.Left"), SubItems: []vtui.MenuItem{
			{Text: "&1. " + i18n.Msg("Action.AI.ViewContext"), Command: appcmd.CmLeftAIContext, Shortcut: "Ctrl+1"},
			{Text: "&2. " + i18n.Msg("Action.AI.ViewChat"), Command: appcmd.CmLeftAIChat, Shortcut: "Ctrl+2"},
			{Text: "&3. " + i18n.Msg("Action.AI.ViewOut"), Command: appcmd.CmLeftAIOut, Shortcut: "Ctrl+3"},
			{Text: "&4. " + i18n.Msg("Action.AI.ViewMem"), Command: appcmd.CmLeftAIMem, Shortcut: "Ctrl+4"},
			{Separator: true},
			{Text: i18n.Msg("Menu.Left.DriveMenu"), Command: appcmd.CmLeftDriveMenu, Shortcut: "Alt+F1"},
			{Separator: true},
			{Text: i18n.Msg("FileOp.BtnBackground"), Command: appcmd.CmBackground},
			{Text: i18n.Msg("Action.Workspace.New"), Command: appcmd.CmWorkspaceNew, Shortcut: "Ctrl+N"},
			{Text: i18n.Msg("Action.Workspace.NewTerminal"), Command: appcmd.CmWorkspaceNewTerminal, Shortcut: "Ctrl+Shift+O"},
			{Text: i18n.Msg("Action.Workspace.Close"), Command: appcmd.CmWorkspaceClose, Shortcut: "Ctrl+W"},
			{Text: i18n.Msg("Menu.Exit"), Command: vtui.CmQuit},
		}}
	}
	return vtui.MenuBarItem{Label: "&" + i18n.Msg("Menu.Left"), SubItems: []vtui.MenuItem{
		{Text: "&" + i18n.Msg("Menu.Left.Brief"), Command: appcmd.CmLeftBrief},
		{Text: "&" + i18n.Msg("Menu.Left.Medium"), Command: appcmd.CmLeftMedium},
		{Text: "&" + i18n.Msg("Menu.Left.Detailed"), Command: appcmd.CmLeftDetailed},
		{Text: "&" + i18n.Msg("Menu.Left.Wide"), Command: appcmd.CmLeftWide},
		{Separator: true},
		{Text: "&" + i18n.Msg("Menu.SortName"), Command: appcmd.CmLeftSortName},
		{Text: "&" + i18n.Msg("Menu.SortExt"), Command: appcmd.CmLeftSortExt},
		{Text: "&" + i18n.Msg("Menu.SortTime"), Command: appcmd.CmLeftSortTime},
		{Text: "&" + i18n.Msg("Menu.SortSize"), Command: appcmd.CmLeftSortSize},
		{Text: "&" + i18n.Msg("Menu.SortUnsorted"), Command: appcmd.CmLeftSortUnsorted},
		{Text: "&" + i18n.Msg("Menu.SortUseGroups"), Command: appcmd.CmLeftSortGroups},
		{Separator: true},
		{Text: i18n.Msg("Menu.Left.DriveMenu"), Command: appcmd.CmLeftDriveMenu, Shortcut: "Alt+F1"},
		{Separator: true},
		{Text: i18n.Msg("FileOp.BtnBackground"), Command: appcmd.CmBackground},
		{Text: i18n.Msg("Action.Workspace.New"), Command: appcmd.CmWorkspaceNew, Shortcut: "Ctrl+N"},
		{Text: i18n.Msg("Action.Workspace.NewTerminal"), Command: appcmd.CmWorkspaceNewTerminal, Shortcut: "Ctrl+Shift+O"},
		{Text: i18n.Msg("Action.Workspace.Close"), Command: appcmd.CmWorkspaceClose, Shortcut: "Ctrl+W"},
		{Text: i18n.Msg("Menu.Exit"), Command: vtui.CmQuit},
	}}
}

// rightMenu builds the custom side menu for the right panel.
func (pf *PanelsFrame) RightMenu() vtui.MenuBarItem {
	if IsAIPanel(pf.Panels[1]) {
		return vtui.MenuBarItem{Label: "&" + i18n.Msg("Menu.Right"), SubItems: []vtui.MenuItem{
			{Text: "&1. " + i18n.Msg("Action.AI.ViewContext"), Command: appcmd.CmRightAIContext, Shortcut: "Ctrl+1"},
			{Text: "&2. " + i18n.Msg("Action.AI.ViewChat"), Command: appcmd.CmRightAIChat, Shortcut: "Ctrl+2"},
			{Text: "&3. " + i18n.Msg("Action.AI.ViewOut"), Command: appcmd.CmRightAIOut, Shortcut: "Ctrl+3"},
			{Text: "&4. " + i18n.Msg("Action.AI.ViewMem"), Command: appcmd.CmRightAIMem, Shortcut: "Ctrl+4"},
			{Separator: true},
			{Text: i18n.Msg("Menu.Right.DriveMenu"), Command: appcmd.CmRightDriveMenu, Shortcut: "Alt+F2"},
		}}
	}
	return vtui.MenuBarItem{Label: "&" + i18n.Msg("Menu.Right"), SubItems: []vtui.MenuItem{
		{Text: "&" + i18n.Msg("Menu.Left.Brief"), Command: appcmd.CmRightBrief},
		{Text: "&" + i18n.Msg("Menu.Left.Medium"), Command: appcmd.CmRightMedium},
		{Text: "&" + i18n.Msg("Menu.Left.Detailed"), Command: appcmd.CmRightDetailed},
		{Text: "&" + i18n.Msg("Menu.Left.Wide"), Command: appcmd.CmRightWide},
		{Separator: true},
		{Text: "&" + i18n.Msg("Menu.SortName"), Command: appcmd.CmRightSortName},
		{Text: "&" + i18n.Msg("Menu.SortExt"), Command: appcmd.CmRightSortExt},
		{Text: "&" + i18n.Msg("Menu.SortTime"), Command: appcmd.CmRightSortTime},
		{Text: "&" + i18n.Msg("Menu.SortSize"), Command: appcmd.CmRightSortSize},
		{Text: "&" + i18n.Msg("Menu.SortUnsorted"), Command: appcmd.CmRightSortUnsorted},
		{Text: "&" + i18n.Msg("Menu.SortUseGroups"), Command: appcmd.CmRightSortGroups},
		{Separator: true},
		{Text: i18n.Msg("Menu.Right.DriveMenu"), Command: appcmd.CmRightDriveMenu, Shortcut: "Alt+F2"},
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

	filesLabel := i18n.Msg("Menu.Shell.Files")
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
func (pf *PanelsFrame) BuildMenuItems() []vtui.MenuBarItem {
	if !pf.ShowPanels {
		return appendTerminalMenuItems(BuildMenuBarItems("Shell"))
	}
	items := []vtui.MenuBarItem{pf.LeftMenu()}
	items = append(items, BuildMenuBarItems("Shell")...)
	return append(items, pf.RightMenu())
}

// GetMenuBar returns the main menu bar. Items are rebuilt on every
// call, so shortcuts and checkmarks always follow the active bindings
// and the current panel state.
func (pf *PanelsFrame) GetMenuBar() *vtui.MenuBar {
	pf.MenuBar.Items = pf.BuildMenuItems()
	pf.UpdateMenuCheckmarks()
	return pf.MenuBar
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

// getToggleMenuText marks an on/off menu row the same way the mode rows are
// marked, so the side menus stay visually consistent.
func getToggleMenuText(on bool, label string) string {
	if on {
		return "√" + label
	}
	return " " + label
}

var CommandToActionName = map[int]string{
	appcmd.CmLeftBrief:             "Panel.Left.ViewBrief",
	appcmd.CmLeftMedium:            "Panel.Left.ViewMedium",
	appcmd.CmLeftDetailed:          "Panel.Left.ViewDetailed",
	appcmd.CmLeftWide:              "Panel.Left.ViewWide",
	appcmd.CmRightBrief:            "Panel.Right.ViewBrief",
	appcmd.CmRightMedium:           "Panel.Right.ViewMedium",
	appcmd.CmRightDetailed:         "Panel.Right.ViewDetailed",
	appcmd.CmRightWide:             "Panel.Right.ViewWide",
	appcmd.CmLeftSortName:          "Panel.Left.SortByName",
	appcmd.CmLeftSortExt:           "Panel.Left.SortByExt",
	appcmd.CmLeftSortTime:          "Panel.Left.SortByTime",
	appcmd.CmLeftSortSize:          "Panel.Left.SortBySize",
	appcmd.CmLeftSortUnsorted:      "Panel.Left.SortUnsorted",
	appcmd.CmLeftSortGroups:        "Panel.Left.SortUseGroups",
	appcmd.CmRightSortName:         "Panel.Right.SortByName",
	appcmd.CmRightSortExt:          "Panel.Right.SortByExt",
	appcmd.CmRightSortTime:         "Panel.Right.SortByTime",
	appcmd.CmRightSortSize:         "Panel.Right.SortBySize",
	appcmd.CmRightSortUnsorted:     "Panel.Right.SortUnsorted",
	appcmd.CmRightSortGroups:       "Panel.Right.SortUseGroups",
	appcmd.CmLeftAIContext:         "AI.Left.ViewContext",
	appcmd.CmLeftAIChat:            "AI.Left.ViewChat",
	appcmd.CmLeftAIOut:             "AI.Left.ViewOut",
	appcmd.CmLeftAIMem:             "AI.Left.ViewMem",
	appcmd.CmRightAIContext:        "AI.Right.ViewContext",
	appcmd.CmRightAIChat:           "AI.Right.ViewChat",
	appcmd.CmRightAIOut:            "AI.Right.ViewOut",
	appcmd.CmRightAIMem:            "AI.Right.ViewMem",
	appcmd.CmBackground:            "App.Background",
	appcmd.CmWorkspaceNew:          "Workspace.New",
	appcmd.CmWorkspaceNewTerminal:  "Workspace.NewTerminal",
	appcmd.CmWorkspaceClose:        "Workspace.Close",
	appcmd.CmLeftDriveMenu:         "Panel.LeftDriveMenu",
	appcmd.CmRightDriveMenu:        "Panel.RightDriveMenu",
	vtui.CmQuit:                    "App.Quit",
	appcmd.CmView:                  "File.View",
	appcmd.CmEdit:                  "File.Edit",
	appcmd.CmCopy:                  "File.Copy",
	appcmd.CmMove:                  "File.Move",
	appcmd.CmMkDir:                 "File.MakeDir",
	appcmd.CmDelete:                "File.Delete",
	appcmd.CmFindFile:              "File.Find",
	appcmd.CmBookmarks:             "Panel.Bookmarks",
	appcmd.CmPanelSettings:         "Settings.Panel",
	appcmd.CmEditorSettings:        "Settings.Editor",
	appcmd.CmColorerSettings:       "Settings.Colorer",
	appcmd.CmAppearanceSettings:    "Settings.Appearance",
	appcmd.CmConfirmationsSettings: "Settings.Confirmations",
	appcmd.CmLanguage:              "Settings.Language",
	appcmd.CmHelpLanguage:          "Settings.HelpLanguage",
	appcmd.CmPlugins:               "Settings.Plugins",
}

// Fixed-side menu commands intentionally have exact action IDs above so every
// user-invokable item is represented in the registry. Their shortcut column,
// however, keeps showing the active-panel bindings used by Ctrl+1..4 and
// Ctrl+F3..F7; the fixed-side actions themselves do not claim extra keys.
var commandShortcutActionName = map[int]string{
	appcmd.CmLeftBrief:         "Panel.ViewBrief",
	appcmd.CmLeftMedium:        "Panel.ViewMedium",
	appcmd.CmLeftDetailed:      "Panel.ViewDetailed",
	appcmd.CmLeftWide:          "Panel.ViewWide",
	appcmd.CmRightBrief:        "Panel.ViewBrief",
	appcmd.CmRightMedium:       "Panel.ViewMedium",
	appcmd.CmRightDetailed:     "Panel.ViewDetailed",
	appcmd.CmRightWide:         "Panel.ViewWide",
	appcmd.CmLeftSortName:      "Panel.SortByName",
	appcmd.CmLeftSortExt:       "Panel.SortByExt",
	appcmd.CmLeftSortTime:      "Panel.SortByTime",
	appcmd.CmLeftSortSize:      "Panel.SortBySize",
	appcmd.CmLeftSortUnsorted:  "Panel.SortUnsorted",
	appcmd.CmLeftSortGroups:    "Panel.SortUseGroups",
	appcmd.CmRightSortName:     "Panel.SortByName",
	appcmd.CmRightSortExt:      "Panel.SortByExt",
	appcmd.CmRightSortTime:     "Panel.SortByTime",
	appcmd.CmRightSortSize:     "Panel.SortBySize",
	appcmd.CmRightSortUnsorted: "Panel.SortUnsorted",
	appcmd.CmRightSortGroups:   "Panel.SortUseGroups",
}

func (pf *PanelsFrame) UpdateMenuCheckmarks() {
	if pf.Panels[0] == nil || pf.Panels[1] == nil || pf.MenuBar == nil || len(pf.MenuBar.Items) < 5 {
		return
	}
	if len(pf.MenuBar.Items[0].SubItems) < 10 || len(pf.MenuBar.Items[4].SubItems) < 10 {
		return
	}

	lMode, rMode := ViewModeMedium, ViewModeMedium
	lSort, rSort := SortName, SortName
	lGroups, rGroups := false, false
	if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
		lMode = fsp.ViewMode
		lSort = fsp.SortMode
		lGroups = fsp.UseSortGroups
	}
	if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
		rMode = fsp.ViewMode
		rSort = fsp.SortMode
		rGroups = fsp.UseSortGroups
	}

	if pf.Wide && pf.WidePanel == 0 {
		lMode = ViewModeWide
	}
	if pf.Wide && pf.WidePanel == 1 {
		rMode = ViewModeWide
	}
	modeItems := []struct {
		mode ViewMode
		key  string
	}{{ViewModeBrief, "Brief"}, {ViewModeMedium, "Medium"}, {ViewModeDetailed, "Detailed"}, {ViewModeWide, "Wide"}}
	for i, item := range modeItems {
		pf.MenuBar.Items[0].SubItems[i].Text = getMenuText(lMode, item.mode, "&"+i18n.Msg("Menu.Left."+item.key))
		pf.MenuBar.Items[4].SubItems[i].Text = getMenuText(rMode, item.mode, "&"+i18n.Msg("Menu.Left."+item.key))
	}
	for i, item := range []struct {
		mode SortMode
		key  string
	}{{SortName, "SortName"}, {SortExt, "SortExt"}, {SortTime, "SortTime"}, {SortSize, "SortSize"}, {SortUnsorted, "SortUnsorted"}} {
		pf.MenuBar.Items[0].SubItems[i+5].Text = getSortMenuText(lSort, item.mode, "&"+i18n.Msg("Menu."+item.key))
		pf.MenuBar.Items[4].SubItems[i+5].Text = getSortMenuText(rSort, item.mode, "&"+i18n.Msg("Menu."+item.key))
	}

	// The sort-group toggle sits right after the sort modes; a mock menu bar
	// built with fewer rows (tests) simply keeps its own text.
	if len(pf.MenuBar.Items[0].SubItems) > 10 && len(pf.MenuBar.Items[4].SubItems) > 10 {
		groupLabel := "&" + i18n.Msg("Menu.SortUseGroups")
		pf.MenuBar.Items[0].SubItems[10].Text = getToggleMenuText(lGroups, groupLabel)
		pf.MenuBar.Items[4].SubItems[10].Text = getToggleMenuText(rGroups, groupLabel)
	}

	// Update shortcuts dynamically from the action registry. Framework-owned
	// native keys are intentionally absent from keymap.HotkeyManager defaults, but
	// custom side menus must still advertise them (issue #651).
	if hm := keymap.GlobalHotkeysMgr; hm != nil {
		area := "Shell"
		for i := range pf.MenuBar.Items {
			for j := range pf.MenuBar.Items[i].SubItems {
				sub := &pf.MenuBar.Items[i].SubItems[j]
				if actName, ok := CommandToActionName[sub.Command]; ok {
					if shortcutAction, exists := commandShortcutActionName[sub.Command]; exists {
						actName = shortcutAction
					}
					sub.Shortcut = keymap.MenuShortcutsForAction(area, actName)
				}
			}
		}
	}
}

// osHostname is os.Hostname behind a seam so prompt tests can pin a value:
// real hostnames range from "mac" to a CI runner's 60-character UUID soup.
var osHostname = os.Hostname

func (pf *PanelsFrame) BuildPrompt() []vtui.CharInfo {
	var path string
	var vfsTitle string
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		path = fsp.PersistentPath()
		if fsp.ProviderOpenTask != nil {
			if colon := strings.IndexByte(path, ':'); colon > 0 {
				vfsTitle = path[:colon]
			}
		} else if tp, ok := fsp.Vfs.(vfs.TitleProvider); ok {
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

	maxPromptLen := pf.LastW / 2
	if maxPromptLen < 30 {
		maxPromptLen = pf.LastW - 15
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

	if pf.SearchFirstMode() && pf.ShowPanels && !pf.CommandLineFocused {
		plainPrompt := userHostStr + sepStr + displayPath + suffixStr
		return vtui.StringToCharInfo(plainPrompt, vtui.Palette[theme.ColCommandLineInactivePrompt])
	}

	baseAttr := vtui.Palette[theme.ColCommandLinePrompt]
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

// localPTY reads the local terminal.PTY under the mutex initPTY publishes it with.
// The read has to be locked and not merely nil checked: an interface value
// is two words wide, and a racing reader can see the type word of a *terminal.PTY
// with the data word still zero. Such a value passes an "!= nil" guard and
// then calls the method on a nil receiver, which is how F10 pressed in the
// first milliseconds of a session used to crash inside terminal.PTY.Close while the
// shell was still being spawned.
func (pf *PanelsFrame) localPTY() terminal.PtyBackend {
	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	return pf.Pty
}

// takeLocalPTY hands the local terminal.PTY to the caller and clears the field, so a
// shutdown path owns it outright: whoever gets it closes it, and a second
// path finds nothing left to close twice.
func (pf *PanelsFrame) takeLocalPTY() terminal.PtyBackend {
	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	pty := pf.Pty
	pf.Pty = nil
	return pty
}

// SpawnLocalShellPTY gates initPTY's fork of the user's shell. Tests turn
// it off in TestMain: 185 frames spawned per test run each forked a real
// shell, and the leaked ptys exhausted macOS's ptmx_max (511), killing
// unrelated terminal.PTY tests with ENXIO ("device not configured").
var SpawnLocalShellPTY = true

// newLocalPTY is a seam for the session lifecycle tests. Production always
// uses the platform terminal.PTY implementation; tests can provide a controllable
// backend without allocating a real terminal.
var newLocalPTY = func() (terminal.PtyBackend, error) {
	return terminal.NewPTY()
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
// (resetLocalShell) or taken by shutdown is no longer the local terminal.PTY by the
// time its read loop ends, and is left alone.
func (pf *PanelsFrame) localShellGone(p terminal.PtyBackend) {
	if !pf.isLocalPTY(p) {
		return
	}
	vtui.DebugLog("PTY: local shell exited on its own; ending the command and starting a fresh shell")
	// Whatever was queued behind the running command belongs to the shell
	// that just died; it is not replayed into the fresh one.
	pf.afterExecution = nil
	if pf.Executing {
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

	pf.PtyMutex.Lock()
	if pf.Pty != pty {
		pf.PtyMutex.Unlock()
		pf.processEnvironmentWriteMu.Unlock()
		return false
	}
	pf.Pty = nil
	pf.PtyMutex.Unlock()
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
		pf.pendingProcessEnvironment = terminal.CoalesceProcessEnvironmentChanges(append(
			terminal.CloneProcessEnvironmentChanges(inFlight.changes),
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
	if inFlight != nil && inFlight.muted && pf.TermView != nil {
		pf.TermView.SetMuted(false)
	}

	_ = pty.Close()

	pf.Executing = false
	pf.afterExecution = nil
	pf.TermView.ResetKeyboardProtocols()
	pf.ShellPromptReady = false
	pf.ignoreNextPrompt = false
	pf.ReturnToPanels = false
	pf.workspaceCommandTitle = ""
	pf.LastPtyPath = ""
	if pf.CmdSession != nil {
		pf.CmdSession.close()
		pf.CmdSession = newCmdShellSession(pf)
	}
	pf.LastPtyVFS = nil
	if pf.TermView != nil {
		pf.TermView.SetMuted(false)
		pf.TermView.Pty = nil
		if keepScreen {
			if pf.TermView.UseAltScreen {
				pf.TermView.ResetBuffer(pf.TermView.Width, pf.TermView.Height)
			} else if pf.TermView.CursorX != 0 {
				parser := terminal.NewAnsiParser(pf.TermView, nil)
				parser.Process([]byte("\r\n"))
			}
		} else {
			pf.TermView.ResetBuffer(pf.TermView.Width, pf.TermView.Height)
		}
	}
	pf.Parser = terminal.NewAnsiParser(pf.TermView, nil)
	pf.Parser.ReplyTo = pf.activeReplyPTY
	pf.InitPTY()
	return true
}

func (pf *PanelsFrame) InitPTY() {
	// Always initialize the parser to prevent nil dereference
	pf.Parser = terminal.NewAnsiParser(pf.TermView, nil)
	pf.Parser.ReplyTo = pf.activeReplyPTY

	if !SpawnLocalShellPTY {
		return
	}

	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	uiFrames := vtui.FrameManager
	go func() {
		pf.PtyMutex.Lock()
		if pf.Closed {
			pf.PtyMutex.Unlock()
			return
		}
		p := pf.Pty
		pf.PtyMutex.Unlock()

		if p == nil {
			var err error
			p, err = newLocalPTY()
			if err != nil {
				vtui.DebugLog("PTY: Failed to allocate local term.PTY: %v", err)
				terminal.LogPTYDiagnostics()
				pf.reportLocalPTYFailure(err)
				return
			}

			if runtime.GOOS == "windows" {
				os.Setenv("PROMPT", windowsShellPrompt)
			}
			inheritedEnvironmentGeneration := terminal.GlobalProcessEnvironment.CurrentGeneration()

			shell := terminal.GetSystemShell()
			if err := p.Run(shell); err != nil {
				vtui.DebugLog("PTY: Failed to run shell: %v", err)
				p.Close()
				return
			}

			pf.PtyMutex.Lock()
			if pf.Closed {
				pf.PtyMutex.Unlock()
				p.Close()
				return
			}
			pf.Pty = p
			serializedPTY := &processEnvironmentSerializedPTY{owner: pf, Backend: p}
			if pf.ShellMode == terminal.ShellModeHost {
				muted := MutedPTY{Backend: serializedPTY}
				pf.Parser.Pty = muted
				pf.TermView.Pty = muted
			} else {
				pf.Parser.Pty = serializedPTY
				pf.TermView.Pty = serializedPTY
			}
			pf.PtyMutex.Unlock()
			pf.localShellStarted(inheritedEnvironmentGeneration)

			uiFrames.PostTask(func() {
				pf.ResizeConsole(pf.LastW, pf.LastH)
				pf.RefreshAll()
				uiFrames.Redraw()
			})
		}

		// Local terminal.PTY has its own dedicated read loop.
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

// consumeLocalOutput routes one read from the local terminal.PTY: to the reflow
// oracle's scratch parser while one of its passes is in flight, otherwise to
// the display's parser (and the host console in passthrough mode). Tests
// drive it directly with a fake terminal.PTY, so it must contain everything the read
// loop does with the bytes.
func (pf *PanelsFrame) consumeLocalOutput(p terminal.PtyBackend, data []byte) {
	pf.processEnvironmentShellOutput(data)

	pf.PtyMutex.Lock()
	shouldProcess := (pf.getActivePTYUnsafe() == p)
	pf.PtyMutex.Unlock()

	pf.displayLocalOutput(shouldProcess, data)
}

// displayLocalOutput is what consumeLocalOutput does with bytes meant for
// the display; split out so route can hand them over a piece at a time.
func (pf *PanelsFrame) displayLocalOutput(shouldProcess bool, data []byte) {
	if !shouldProcess {
		return
	}
	if pf.ShellMode == terminal.ShellModeHost && pf.IsHostConsoleActive() {
		vtui.WritePassthrough(data)
		pf.Parser.Process(data)
		if pf.OverlayLines() > 0 && time.Since(pf.lastOverlayDraw) > 30*time.Millisecond {
			pf.drawHostConsoleOverlay()
			pf.lastOverlayDraw = time.Now()
		}
		return
	}
	pf.Parser.Process(data)
	pf.terminalRedraw.Request()
}

// reportLocalPTYFailure surfaces a terminal.NewPTY() failure to the person instead of
// leaving it only in the debug log. Without this, a platform where terminal.PTY
// allocation fails (see issue #444, FreeBSD and illumos before their
// backends were fixed) looked identical to a healthy f4 whose terminal
// silently ignores every keystroke: panels, menus and the viewer all work,
// because none of them touch the terminal.PTY, so the only visible symptom was an
// empty terminal and no error anywhere the person could see without
// starting f4 with --debug.
func localPTYFailureMessage(err error) string {
	return fmt.Sprintf(i18n.Msg("Terminal.PTYAllocFailed"), err)
}

func (pf *PanelsFrame) reportLocalPTYFailure(err error) {
	if vtui.FrameManager == nil || pf.ShellMode == terminal.ShellModeSimpleInline || pf.ShellMode == terminal.ShellModeSimpleCaptured {
		return
	}
	vtui.FrameManager.PostTask(func() {
		toast.Show(localPTYFailureMessage(err), 8*time.Second)
	})
}

// ConfirmClose prevents the last workspace containing file panels from being
// removed while editor/viewer workspaces remain. FrameManager asks this vetoer
// before every close path, including its native Ctrl+W fallback; without it,
// the final panel-less workspace could emit CmQuit without PanelsFrame's exit
// confirmation policy (issue #531).
func (pf *PanelsFrame) ConfirmClose() bool {
	if pf == nil || pf.Closed || vtui.FrameManager == nil {
		return true
	}
	for _, screen := range vtui.FrameManager.Screens {
		if workspaceHasOpenPanels(screen) && workspaceContainsPanelsFrame(screen, pf) {
			return !IsOnlyPanelsWorkspace(screen)
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
		pf.terminalRedraw.Stop()
	}
	pf.CmdSession.close()
	if pf.ShellMode == terminal.ShellModeHost && pf.IsHostConsoleActive() {
		pf.LeaveHostConsole()
	}
	if pf.Wide && pf.WidePanel >= 0 && pf.WidePanel < 2 {
		if IsAIPanel(pf.Panels[pf.WidePanel]) {
			pf.ExitWide()
		}
	}
	pf.closeProcessEnvironmentShell()

	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	pf.Closed = true

	for _, p := range pf.Panels {
		if fsp, ok := p.(*FileSystemPanel); ok && fsp != nil {
			fsp.cancelProviderOpen()
			if fsp.CancelLoad != nil {
				fsp.CancelLoad()
			}
			fsp.StopLoadingAnimation()
		}
	}
	for i, alt := range pf.AltPanels {
		if closer, ok := alt.(interface{ Close() }); ok {
			closer.Close()
		}
		pf.AltPanels[i] = nil
	}

	if pf.Pty != nil {
		_ = pf.Pty.Close()
		pf.Pty = nil
	}
	for _, pty := range pf.RemotePtys {
		pty.Close()
	}
	pf.RemotePtys = nil
	pf.BaseFrame.Close()
}

func (pf *PanelsFrame) SetWidePanel(idx int) {
	if idx < 0 || idx > 1 {
		idx = -1
	}
	pf.WidePanel = idx
	pf.Wide = idx >= 0
	if idx >= 0 {
		pf.ActiveIdx = idx
		pf.ShowPanels = true
	}
	if pf.LastW > 0 && pf.LastH > 0 {
		pf.ResizeConsole(pf.LastW, pf.LastH)
	}
}

func (pf *PanelsFrame) ExitWide() {
	if pf.Wide {
		pf.SetWidePanel(-1)
	}
}

func (pf *PanelsFrame) SetPanelViewMode(idx int, mode ViewMode) {
	if idx < 0 || idx > 1 {
		return
	}
	pf.ExitWide()
	if fsp, ok := pf.Panels[idx].(*FileSystemPanel); ok {
		fsp.SetViewMode(mode)
	}
	pf.UpdateMenuCheckmarks()
}

// syncMenuBarGeometry keeps the bar on the row it occupies on screen even while
// it is not painted. vtui's MenuBar.ActivateSubMenu positions the dropdown from
// the bar's own Y1 at the moment of activation and never moves it afterwards,
// so a bar parked off-screen produced a dropdown one row above the visible area
// (issues #1129 and #1149) whichever path opened it -- f4's F9 handler, the
// action table, vtui's own F9 fallback or an Alt hotkey.
//
// A bar that must not be painted is collapsed to an empty column span instead.
// vtui's MenuBar.HitTest is geometry-only, and an empty span matches no column,
// so the hidden bar still cannot swallow a click on the terminal's first row
// (issue #1093).
func (pf *PanelsFrame) syncMenuBarGeometry() {
	if pf.MenuBar == nil {
		return
	}
	menuY := vtui.FrameManager.WorkspaceTopInset()
	// The AlwaysShowMenuBar setting pins the bar above the panels; F9 and the
	// menu hotkeys raise it temporarily over whatever the workspace shows,
	// panels or the terminal.
	painted := pf.MenuBar.Active || (config.App.AlwaysShowMenuBar && pf.ShowPanels)
	x2 := pf.LastW - 1
	if !painted {
		x2 = -1
	}
	pf.MenuBar.SetPosition(0, menuY, x2, menuY)
	pf.MenuBar.SetVisible(painted)
}

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	pf.LastW, pf.LastH = w, h
	pf.SetPosition(0, 0, w-1, h-1) // Update hit-box for FrameManager hit-testing
	topInset := vtui.FrameManager.WorkspaceTopInset()
	pf.syncMenuBarGeometry()

	contentY1 := topInset
	if config.App.AlwaysShowMenuBar && pf.ShowPanels {
		contentY1++
	}

	// 1. Terminal Area: Fills everything except KeyBar
	termY2 := h - 1
	if pf.ShellMode == terminal.ShellModeHost {
		// The host console keeps its overlay rows *below* the mirrored grid,
		// so nothing of the child's output is ever painted over.
		pf.TermView.SetPromptOverlaysLastRow(false)
		n := pf.OverlayLines()
		termH := h - n
		if termH <= 0 {
			termH = 1
		}
		if pty := pf.localPTY(); pty != nil {
			// Resize the parser's grid before asking a terminal.PTY to emit its resize
			// frame. A fast ConPTY can otherwise deliver new-width absolute
			// coordinates while terminal.TerminalView still has the old width.
			pf.TermView.SetPosition(0, 0, w-1, termH-1)
			pf.TermView.Resize(w, termH)
			pf.PtyMutex.Lock()
			cw, ch := pf.TermView.CellSize()
			{
				// The other half of every resize: what ConPTY was told. A frame
				// that later declares a different size (REFLOW_FRAME STALE) is
				// only explicable next to this line.
				vtui.DebugLog("REFLOW_PTY: outer %dx%d -> child %dx%d (cell %dx%d, host mode, overlay rows %d)", w, h, w, termH, cw, ch, n)
				terminal.SetPtySize(pty, w, termH, cw, ch)
				for _, remotePty := range pf.RemotePtys {
					terminal.SetPtySize(remotePty, w, termH, cw, ch)
				}
			}
			pf.PtyMutex.Unlock()
		}
		if pf.IsHostConsoleActive() && n > 0 {
			vtui.WritePassthrough([]byte(fmt.Sprintf("\x1b[1;%dr", termH)))
			pf.drawHostConsoleOverlay()
		}
	} else {
		// Keep the configured keybar row out of the own-terminal terminal.PTY even while
		// a command is running. The keybar and command line are hidden then, but
		// temporarily growing the terminal.PTY makes bash redraw its prompt on SIGWINCH.
		// The command line intentionally still overlaps the terminal.PTY's bottom row so
		// it paints over, rather than duplicates, the native shell prompt. The
		// view is told, because a command that ends without a newline would
		// otherwise leave its last output line on that hidden row (#863).
		pf.TermView.SetPromptOverlaysLastRow(true)
		if pf.ShowKeyBar && !pf.TermView.OnAltScreen() &&
			(pf.ShellMode == terminal.ShellModeOwn || !pf.IsPtyBusy()) {
			termY2 = h - 2
		}
		termH := termY2 - contentY1 + 1
		if termH < 0 {
			termH = 0
		}

		if pty := pf.localPTY(); pty != nil {
			pf.TermView.SetPosition(0, contentY1, w-1, termY2)
			pf.TermView.Resize(w, termH)
			pf.PtyMutex.Lock()
			cw, ch := pf.TermView.CellSize()
			{
				vtui.DebugLog("REFLOW_PTY: outer %dx%d -> child %dx%d (cell %dx%d, rows %d..%d)", w, h, w, termH, cw, ch, contentY1, termY2)
				terminal.SetPtySize(pty, w, termH, cw, ch)
				for _, remotePty := range pf.RemotePtys {
					terminal.SetPtySize(remotePty, w, termH, cw, ch)
				}
			}
			pf.PtyMutex.Unlock()
		}
	}

	// 2. Panel Area: Leaves one additional line for the f4 CommandLine.
	// leftHeightDecrement / rightHeightDecrement shrink the corresponding
	// panel from the bottom; the freed rows go to the terminal area above
	// (matches far2l's [Layout] section of the same name).
	basePanelY2 := h - 2
	if pf.ShowKeyBar {
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
	leftPanelY2 := basePanelY2 - clampHD(pf.LeftHeightDecrement)
	rightPanelY2 := basePanelY2 - clampHD(pf.RightHeightDecrement)
	// panelH is only used to seed the two panels on first construction;
	// after that each panel takes its own height from SetPosition below.
	panelH := basePanelY2 - contentY1 + 1
	if panelH < 0 {
		panelH = 0
	}

	// widthDecrement shifts the split between the two panels: positive
	// grows the right panel (as in far2l).
	wd := pf.WidthDecrement
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
	if pf.ShowLeftPanel != pf.ShowRightPanel {
		visibleIdx := 0
		if !pf.ShowLeftPanel {
			visibleIdx = 1
		}
		panelX1[visibleIdx] = 0
		panelX2[visibleIdx] = w - 1
	}

	if pf.Panels[0] == nil {
		pf.Panels[0] = NewFileSystemPanel(0, contentY1, leftW, panelH, vfs.NewOSVFS("."))
		pf.Panels[1] = NewFileSystemPanel(leftW, contentY1, rightW, panelH, vfs.NewOSVFS("."))
	}

	for i, p := range pf.Panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			fsp.Wide = pf.Wide && pf.WidePanel == i
			fsp.configureCellSelection()
		}
	}

	if pf.Wide {
		idx := pf.WidePanel
		panelY2 := leftPanelY2
		if idx == 1 {
			panelY2 = rightPanelY2
		}
		pf.Panels[idx].SetPosition(0, contentY1, w-1, panelY2)
		if fsp, ok := pf.Panels[idx].(*FileSystemPanel); ok {
			fsp.Resize(w, panelY2-contentY1+1)
		}
	} else {
		for i, p := range pf.Panels {
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
	if pf.Wide {
		idx := pf.WidePanel
		panelY2 := leftPanelY2
		if idx == 1 {
			panelY2 = rightPanelY2
		}
		if pf.AltPanels[idx] != nil {
			pf.AltPanels[idx].SetPosition(0, contentY1, w-1, panelY2)
		}
	} else {
		for i, alt := range pf.AltPanels {
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
	if pf.ShowKeyBar {
		// KeyBar on the last line
		pf.KeyBar.SetPosition(0, h-1, w-1, h-1)
		pf.KeyBar.SetVisible(true)
		cmdLineY = h - 2 // CommandLine is above KeyBar
	} else {
		pf.KeyBar.SetVisible(false)
		// CommandLine takes the last line
	}
	// Set CommandLine's base position. Show() will override if in terminal prompt mode.
	pf.CmdLine.SetPosition(0, cmdLineY, w-1, cmdLineY)
	pf.UpdateMenuCheckmarks()
}

func (pf *PanelsFrame) IsPtyBusy() bool {
	active := pf.GetActivePTY()
	if active == nil {
		return false
	}
	if active.IsBusy() {
		return true
	}
	// Managed execution signal from OSC 133
	return pf.Executing
}

// beginManagedExecution marks a command that carries its own OSC 133 C/D
// pair, wrapped around it by f4 itself. Its D marker is unambiguous: it is
// printed by the very command line we sent, so it always ends the execution.
func (pf *PanelsFrame) BeginManagedExecution() {
	pf.Executing = true
	pf.ignoreNextPrompt = false
}

// beginPromptDrivenExecution marks a command that carries no markers of its
// own — cmd.exe with the PROMPT f4 injects, or a VFS integration wiring the
// command for a remote peer. Completion is inferred from the next prompt
// marker, and a prompt printed at shell startup can still be in flight when
// the command is sent, so that first stale marker is discarded.
func (pf *PanelsFrame) BeginPromptDrivenExecution() {
	pf.Executing = true
	pf.ignoreNextPrompt = !pf.ShellPromptReady
}

// endExecution is the single place where a finished command hands the
// screen back: to the panels if they were hidden for it, otherwise just
// out of the busy state.
func (pf *PanelsFrame) endExecution() {
	pf.Executing = false
	pf.workspaceCommandTitle = ""
	if pf.ReturnToPanels {
		pf.ShowPanels = true
		if !pf.ShowLeftPanel && !pf.ShowRightPanel {
			pf.ShowLeftPanel = true
			pf.ShowRightPanel = true
		}
		pf.ReturnToPanels = false
		if pf.ShellMode == terminal.ShellModeHost {
			pf.LeaveHostConsole()
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
func (pf *PanelsFrame) NoteLocalShellLineSent(pty terminal.PtyBackend) {
	if pf.CmdSession != nil && pf.isLocalPTY(pty) {
		pf.CmdSession.noteSent()
	}
}

func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	if pf.ShellMode == terminal.ShellModeHost && pf.IsHostConsoleActive() {
		return
	}
	isBusy := pf.IsPtyBusy()

	// 1. Dynamic Layout Adjustment
	if pf.TermView.OnAltScreen() != pf.lastAlt || isBusy != pf.lastBusy || pf.ShowPanels != pf.lastShowPanels {
		pf.lastAlt = pf.TermView.OnAltScreen()
		pf.lastBusy = isBusy
		pf.lastShowPanels = pf.ShowPanels
		pf.ResizeConsole(pf.LastW, pf.LastH)
	}

	if !isBusy && pf.CmdSession.idle() {
		if fsp := pf.GetActivePanel(); fsp != nil {
			currentPath := fsp.Vfs.GetPath()
			if currentPath != pf.LastPtyPath || !fileops.SameVFSInstance(fsp.Vfs, pf.LastPtyVFS) {
				if pf.syncPTYDirectory(currentPath, fsp.Vfs) {
					pf.LastPtyPath = currentPath
					pf.LastPtyVFS = fsp.Vfs
				}
			}
		}
	}

	now := time.Now()
	if pf.ShowPanels && now.Sub(pf.LastAutoRefresh) > 2*time.Second {
		pf.LastAutoRefresh = now
		for _, p := range pf.Panels {
			if fsp, ok := p.(*FileSystemPanel); ok && !fsp.IsLoading && !fsp.isCheckingRefresh {
				fsp.isCheckingRefresh = true
				vfsPath := fsp.Vfs.GetPath()
				vfsInst := fsp.Vfs
				lastKnown := fsp.lastDirMTime
				vtui.RunAsync(func(ctx *vtui.TaskContext) {
					stat, err := vfsInst.Stat(ctx.Context, vfsPath)
					ctx.RunOnUI(func() {
						fsp.isCheckingRefresh = false
						if err == nil && !stat.MTime.IsZero() {
							if !fsp.IsLoading && fsp.Vfs.GetPath() == vfsPath {
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

	if pf.ShowPanels && pf.Wide {
		hasTerminalArea := pf.LeftHeightDecrement > 0
		if pf.WidePanel == 1 {
			hasTerminalArea = pf.RightHeightDecrement > 0
		}
		pf.TermView.SetVisible(hasTerminalArea)
		if hasTerminalArea {
			pf.TermView.Show(scr)
		}
		idx := pf.WidePanel
		pf.Panels[idx].SetFocus(true)
		if pf.AltPanels[idx] != nil {
			pf.AltPanels[idx].SetFocus(true)
			pf.AltPanels[idx].Show(scr)
		} else {
			pf.Panels[idx].Show(scr)
		}
	} else if pf.ShowPanels {
		// Keep the terminal behind a single full-width panel, or let it show
		// through below a panel that was reduced vertically (Ctrl+Up).
		if !pf.ShowLeftPanel || !pf.ShowRightPanel || pf.LeftHeightDecrement > 0 || pf.RightHeightDecrement > 0 {
			pf.TermView.SetVisible(true)
			pf.TermView.Show(scr)
		} else {
			pf.TermView.SetVisible(false)
		}
		if pf.ShowLeftPanel {
			panelFocused := !pf.SearchFirstMode() || !pf.CommandLineFocused
			pf.Panels[0].SetFocus(pf.ActiveIdx == 0 && panelFocused)
			if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
				fsp.showInactiveCursor = pf.SearchFirstMode() && pf.CommandLineFocused && pf.ActiveIdx == 0
			}
			if pf.AltPanels[0] != nil {
				pf.AltPanels[0].SetFocus(pf.ActiveIdx == 0 && panelFocused)
				pf.AltPanels[0].Show(scr)
			} else {
				pf.Panels[0].Show(scr)
			}
		}
		if pf.ShowRightPanel {
			panelFocused := !pf.SearchFirstMode() || !pf.CommandLineFocused
			pf.Panels[1].SetFocus(pf.ActiveIdx == 1 && panelFocused)
			if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
				fsp.showInactiveCursor = pf.SearchFirstMode() && pf.CommandLineFocused && pf.ActiveIdx == 1
			}
			if pf.AltPanels[1] != nil {
				pf.AltPanels[1].SetFocus(pf.ActiveIdx == 1 && panelFocused)
				pf.AltPanels[1].Show(scr)
			} else {
				pf.Panels[1].Show(scr)
			}
		}
	} else {
		pf.TermView.SetVisible(true)
		pf.TermView.Show(scr)
	}

	pf.syncMenuBarGeometry()
	if pf.MenuBar != nil && pf.MenuBar.IsVisible() {
		pf.MenuBar.Show(scr)
	}

	// Command line logic depends on terminal state and editor visibility
	topType := vtui.FrameManager.GetTopFrameType()
	isFastFind := false
	if fsp := pf.GetActivePanel(); fsp != nil && fsp.FastFindMode {
		isFastFind = true
	}

	if (!pf.ShowPanels && (pf.TermView.OnAltScreen() || isBusy)) || topType == vtui.TypeUser+2 {
		pf.CmdLine.SetVisible(false)
	} else {
		isChatFocused := false
		if pf.ShowPanels && pf.AltPanels[pf.ActiveIdx] != nil && pf.AltPanels[pf.ActiveIdx].Kind() == "ai_chat" && pf.AltPanels[pf.ActiveIdx].IsFocused() {
			isChatFocused = true
		}
		pf.CmdLine.SetVisible(true)
		pf.CmdLine.Edit.HideCursor = isFastFind || isChatFocused || (pf.SearchFirstMode() && pf.ShowPanels && !pf.CommandLineFocused)
		cmdLineY := pf.LastH - 1
		if pf.ShowKeyBar {
			cmdLineY = pf.LastH - 2
		}
		pf.CmdLine.SetRichPrompt(pf.BuildPrompt())
		pf.CmdLine.SetPosition(0, cmdLineY, pf.LastW-1, cmdLineY)
		pf.CmdLine.Show(scr)
	}

	// KeyBar is at the bottom. It should only be hidden if a child process
	// in the terminal is running or using the alternate screen buffer.
	isTop := vtui.FrameManager.GetTopFrameType() == vtui.TypeUser+1
	if isTop { // Only the top-most user frame controls the keybar
		if pf.ShowKeyBar && !pf.TermView.OnAltScreen() && (pf.ShowPanels || !isBusy) {
			vtui.FrameManager.KeyBar = pf.KeyBar
		} else {
			vtui.FrameManager.KeyBar = nil
		}
	}

}

// InterceptPluginKey lets global plugin hotkeys and the active panel's
// PanelController consume a key before built-in hotkey dispatch. It is
// called from macro.MacroManager.Filter, so plugins keep their priority over
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
		// The matching plugin gesture owns the event even when its handler
		// cannot open the game. Letting a failed launch fall through exposes
		// the same key to the frame dispatcher and can leave a stale overlay
		// behind (#983).
		Arkanoid()
		return true
	}

	// Check global hotkeys (ignoring Lock and Enhanced keys)
	for _, hk := range plughost.GlobalHotkeysSnapshot() {
		hkCtrl := (hk.Mods & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		hkAlt := (hk.Mods & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		hkShift := (hk.Mods & vtinput.ShiftPressed) != 0

		if e.VirtualKeyCode == hk.VK && ctrl == hkCtrl && alt == hkAlt && shift == hkShift {
			hk.Handler(pf)
			return true
		}
	}

	// Panel Controller interception (allows plugins to override default keys)
	if pf.ShowPanels {
		if fsp := pf.GetActivePanel(); fsp != nil {
			if pc, ok := fsp.Vfs.(PanelController); ok {
				if pc.ProcessPanelKey(pf, e) {
					return true
				}
			}
		}
	}

	// A remote shell owns Ctrl+C after plugins and the active PanelController
	// have had their documented first chance. This filter still runs before
	// keymap.HotkeyManager, so Panel.SplitReset cannot shadow the interrupt fallback.
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
	return pf.CmdLine != nil && pf.CmdLine.IsVisible() && !pf.CmdLine.IsEmpty()
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
	if !pf.ShowPanels {
		return false
	}
	fsp := pf.GetActivePanel()
	if fsp != nil && fsp.ProviderOpenTask != nil {
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
	if fsp == nil || !fsp.FastFindMode {
		// A focused alt panel gets its own keys first: e.g. F2 toggles
		// wrap in quick view and must not fire Panel.UserMenu.
		if e.VirtualKeyCode == vtinput.VK_F2 && pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
			if a := pf.AltPanels[pf.ActiveIdx]; a != nil && a.IsFocused() {
				return true
			}
		}
		if pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
			if a := pf.AltPanels[pf.ActiveIdx]; a != nil && a.IsFocused() && a.Kind() == "ai_chat" {
				if e.Char != 0 || e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_BACK || e.VirtualKeyCode == vtinput.VK_DELETE {
					return true
				}
			}
			// The player takes WinAmp's letter keys (Z X C V B) and +/-
			// for volume, so plain characters must not go to the
			// command line while it has the cursor.
			if a := pf.AltPanels[pf.ActiveIdx]; a != nil && a.IsFocused() && a.Kind() == "player" {
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
	if pf.ShowPanels && e.KeyDown && alt && shift && !ctrl {
		if fsp := pf.GetActivePanel(); fsp != nil {
			if temp, ok := fsp.Vfs.(*TempPanelVFS); ok {
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

	// Plain Esc on a highlighted mouse selection only dismisses the
	// highlight (#881). The key is consumed instead of reaching the shell,
	// so a selection can be backed out of without the running command
	// seeing an escape (readline/zsh treat it as a line reset, much like
	// Ctrl+C). The matching key-up is swallowed too, so win32-input-mode
	// and kitty-protocol apps never receive a release without its press.
	if !pf.ShowPanels && e.Type == vtinput.KeyEventType &&
		e.VirtualKeyCode == vtinput.VK_ESCAPE && !ctrl && !alt && !shift {
		if e.KeyDown && pf.TermView.HasSelection() {
			pf.TermView.ClearSelection()
			pf.termSelEscHeld = true
			if vtui.FrameManager != nil {
				vtui.FrameManager.Redraw()
			}
			return true
		}
		if !e.KeyDown && pf.termSelEscHeld {
			pf.termSelEscHeld = false
			return true
		}
	}

	// A keyboard action starts a new terminal interaction. Clear the old
	// mouse highlight before forwarding the key to a shell or terminal app;
	// otherwise it remains painted until another mouse click.
	if !pf.ShowPanels && e.Type == vtinput.KeyEventType && e.KeyDown && pf.TermView.HasSelection() {
		pf.TermView.ClearSelection()
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}

	// Workspace switching is global and must remain reachable while a child
	// process owns the terminal. Returning false lets FrameManager handle both
	// directions instead of forwarding the key to an AltScreen application or
	// to a busy ordinary terminal.PTY such as the Python REPL.
	if e.Type == vtinput.KeyEventType && e.VirtualKeyCode == vtinput.VK_TAB && ctrl && !alt {
		return false
	}
	// Ctrl+N normally forks the active panels into a new workspace. Terminal
	// applications may use that key themselves, so this interception is a
	// default-on preference rather than an unconditional global shortcut.
	if e.Type == vtinput.KeyEventType && e.KeyDown && !pf.ShowPanels &&
		e.VirtualKeyCode == vtinput.VK_N && ctrl && !alt && !shift && config.App.TerminalCtrlNWorkspace {
		return false
	}

	// Raw input mode check at the very top. If an interactive AltScreen app is active (e.g. mc, htop),
	// we forward all non-global keys to terminal.PTY.
	if !pf.ShowPanels && pf.TermView.OnAltScreen() {
		if e.KeyDown || pf.TermView.Win32InputMode || pf.TermView.KittyFlags != 0 {
			active := pf.GetActivePTY()
			if active != nil {
				if seq := keymap.TranslateInput(e, pf.TermView.Win32InputMode, pf.TermView.KittyFlags, pf.TermView.ApplicationCursorKeys); seq != "" {
					_, _ = pf.WritePTY(active, []byte(seq))
				}
			}
		}
		return true
	}

	// In search-first command focus, Alt+the grave key inserts a literal
	// backtick instead of toggling focus. Always insert the shell character,
	// including when the current keyboard layout reports 'ё'.
	if pf.SearchFirstMode() && pf.ShowPanels && pf.CommandLineFocused && e.KeyDown &&
		isCommandFocusToggleKey(e) && !ctrl && alt && !shift {
		pf.CmdLine.InsertString("`")
		return true
	}

	// Quake-style physical key: ` / ~ / ё toggles the explicit input
	// target in search-first mode and is never inserted as text.
	if pf.SearchFirstMode() && pf.ShowPanels && e.KeyDown && isCommandFocusToggleKey(e) && !ctrl && !alt && !shift {
		pf.SetCommandLineFocus(!pf.CommandLineFocused)
		return true
	}

	// If the active slot is showing a focused alt panel (Ctrl+L info,
	// Ctrl+Q quick-view, later Ctrl+T tree), let it consume its own
	// navigation keys first — plain arrows, PgUp/Dn, Home/End, F2
	// wrap-toggle. Anything the alt doesn't recognise falls through
	// to the normal chain, so Ctrl+L / Ctrl+Q / Tab still work.
	if pf.ShowPanels && pf.AltPanels[pf.ActiveIdx] != nil && pf.AltPanels[pf.ActiveIdx].IsFocused() {
		if pf.AltPanels[pf.ActiveIdx].ProcessKey(e) {
			return true
		}
	}

	// Fast Find owns Esc, Delete, F2 and Ctrl+Enter before in-frame hotkeys and
	// command-line handling. Delegate them to the panel's normal handler.
	if pf.ShowPanels && e.KeyDown {
		if fsp := pf.GetActivePanel(); fsp != nil && fsp.FastFindMode {
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

	// Ctrl+C on a remote panel that carries a term.PTY-integration hint
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
			pf.CancelFastFind()
		}
		pf.SetFocus(e.SetFocus)
		// Reload macros from disk when regaining focus to share them across instances
		if e.SetFocus && macro.MacroMgr != nil {
			macro.MacroMgr.Load()
		}
		// Propagate application focus without losing the explicit search-first target.
		if pf.SearchFirstMode() {
			pf.CmdLine.SetFocus(e.SetFocus && pf.CommandLineFocused)
		} else {
			pf.CmdLine.SetFocus(e.SetFocus)
		}
		pf.TermView.SetFocus(e.SetFocus)
		return true
	}

	// Handle bracketed paste for terminal apps
	if e.Type == vtinput.PasteEventType {
		pty := pf.localPTY()
		if !pf.ShowPanels && pf.TermView.BracketedPasteMode && pty != nil {
			if e.PasteStart {
				_, _ = pf.WritePTY(pty, []byte("\x1b[200~"))
			} else {
				_, _ = pf.WritePTY(pty, []byte("\x1b[201~"))
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
	if pf.ShellMode == terminal.ShellModeSimpleInline && !pf.ShowPanels &&
		e.Type == vtinput.KeyEventType && e.KeyDown {
		if pf.consoleStyle() == terminal.ConsoleViewFar {
			vtui.DebugLog("FARKEY: char=%q vk=0x%X before cmdLine.ProcessKey", e.Char, e.VirtualKeyCode)
			if e.VirtualKeyCode != vtinput.VK_RETURN && pf.CmdLine != nil && pf.CmdLine.ProcessKey(e) {
				vtui.DebugLog("FARKEY: cmdLine.ProcessKey handled it, drawing overlay")
				pf.DrawConsoleOverlay()
				return true
			}
			vtui.DebugLog("FARKEY: cmdLine.ProcessKey did NOT handle it (fell through)")
		} else {
			pf.ShowPanels = true
			vtui.SetAltScreen(true)
			pf.SetBusy(false)
			if vtui.FrameManager != nil {
				vtui.FrameManager.HardRefresh()
			}
			return true
		}
	}

	// In Far-style host console with an overlay, route editing keys to CommandLine first
	if !pf.ShowPanels && pf.ShellMode == terminal.ShellModeHost && pf.OverlayLines() > 0 {
		if pf.handleHostConsoleTab(e) {
			return true
		}
		if e.VirtualKeyCode != vtinput.VK_RETURN {
			if pf.CmdLine.ProcessKey(e) {
				pf.drawHostConsoleOverlay()
				return true
			}
		}
	}

	// Char fallbacks for terminals that report Ctrl+\\ / Ctrl+[ / Ctrl+]
	// with an unexpected character or virtual key code. The canonical bindings
	// live in the action registry (Panel.GoRoot / Panel.InsertLeftPath /
	// Panel.InsertRightPath). Keep these before raw terminal forwarding so the
	// f4-owned command line still receives the shortcuts while panels are
	// hidden; a busy terminal process keeps ownership of the key.
	if pf.ShowPanels || !pf.IsPtyBusy() {
		if (e.VirtualKeyCode == vtinput.VK_OEM_5 || e.Char == '\\') && ctrl && !alt && !shift && e.KeyDown {
			if RunAction("Panel.GoRoot") {
				return true
			}
		}
		if (e.VirtualKeyCode == vtinput.VK_OEM_4 || e.Char == '[') && ctrl && !alt && !shift && e.KeyDown {
			if RunAction("Panel.InsertLeftPath") {
				return true
			}
		}
		if (e.VirtualKeyCode == vtinput.VK_OEM_6 || e.Char == ']') && ctrl && !alt && !shift && e.KeyDown {
			if RunAction("Panel.InsertRightPath") {
				return true
			}
		}
	}

	// Raw input mode fallback for active shell commands (non-AltScreen, e.g. ping),
	// and for any interactive shell session when host console mode is active.
	// We forward text and navigation to term.PTY, but let global shortcuts (Ctrl+O) fall through.
	if !pf.ShowPanels && (pf.IsPtyBusy() || pf.ShellMode == terminal.ShellModeHost) {
		if e.KeyDown || pf.TermView.Win32InputMode || pf.TermView.KittyFlags != 0 {
			active := pf.GetActivePTY()
			if active != nil {
				if seq := keymap.TranslateInput(e, pf.TermView.Win32InputMode, pf.TermView.KittyFlags, pf.TermView.ApplicationCursorKeys); seq != "" {
					_, _ = pf.WritePTY(active, []byte(seq))
				}
			}
		}
		return true
	}

	// Char fallbacks for terminals that report Ctrl+\ / Ctrl+[ / Ctrl+]
	// with an unexpected virtual key code: the canonical bindings live
	// in the action registry (Panel.GoRoot / Panel.InsertLeftPath /
	// Panel.InsertRightPath).
	// Folder bookmarks, far2l's hotkey scheme: [RightCtrl | Ctrl+Alt] + N
	// jumps to slot N, Ctrl+Shift+N stores the current directory there, and
	// [RightCtrl | Ctrl+Alt] + ~ goes home. The ctrl local above merges both
	// Ctrl keys, but here the side matters, so the flags are read directly.
	// Terminals that cannot tell the two Ctrls apart report LeftCtrlPressed
	// for either one — that is what the Ctrl+Alt alias (far2l offers the
	// same pair) is for.
	if e.KeyDown && !keymap.ConfigurableHotkeyOwnsPanelBookmark(keymap.GlobalHotkeysMgr, "Shell", e) {
		rctrl := (e.ControlKeyState & vtinput.RightCtrlPressed) != 0
		lctrl := (e.ControlKeyState & vtinput.LeftCtrlPressed) != 0
		isBookmarkGoto := (rctrl && !shift && !alt) || ((lctrl || rctrl) && alt && !shift)
		isBookmarkSave := (rctrl && shift && !alt) || ((lctrl || rctrl) && alt && shift)

		if e.VirtualKeyCode >= vtinput.VK_0 && e.VirtualKeyCode <= vtinput.VK_9 && (isBookmarkGoto || isBookmarkSave) {
			slot := int(e.VirtualKeyCode - vtinput.VK_0)
			File := BookmarksFilePath()
			// Always read fresh: another f4 or far2l instance may have
			// rewritten the file since we last looked at it.
			set, err := LoadBookmarks(File)
			if err != nil {
				vtui.DebugLog("BOOKMARKS: load %q failed: %v", File, err)
				return true
			}
			if isBookmarkSave {
				if fsp := pf.GetActivePanel(); fsp != nil {
					set[slot] = Bookmark{Path: fsp.Vfs.GetPath()}
					if err := SaveBookmarks(File, set); err != nil {
						vtui.DebugLog("BOOKMARKS: save %q failed: %v", File, err)
					}
				}
			} else if !set[slot].IsEmpty() {
				// An unset slot is a silent no-op, as in far2l.
				if fsp := pf.GetActivePanel(); fsp != nil {
					pf.NavigateToBookmark(fsp, set[slot])
				}
			}
			return true
		}

		if e.VirtualKeyCode == vtinput.VK_OEM_3 && isBookmarkGoto {
			if home, _ := os.UserHomeDir(); home != "" {
				if fsp := pf.GetActivePanel(); fsp != nil {
					pf.NavigateToPath(fsp, home)
				}
			}
			return true
		}
	}

	if !e.KeyDown {
		return false
	}

	// F9 raises the main menu bar (other F-keys are action bindings now).
	// Alt+F9 is term.App.ToggleWindowSize and must not fall through to this.
	//
	// Only the bar comes up, with the active panel's fixed side selected and
	// no dropdown: far2l's FilePanels::ProcessKey calls ShellOptions(0), and
	// only the LastCommand path -- Shift+F10, ShellOptions(1) -- follows the
	// Show() with ProcessKey(KEY_DOWN) (far2l/src/options.cpp). Down, Enter,
	// a letter hotkey or a click opens the dropdown from there, all of which
	// vtui's MenuBar already handles while Active (f4 issue #1144).
	if e.VirtualKeyCode == vtinput.VK_F9 && !alt {
		pos := 0 // Left
		if pf.ActiveIdx == 1 {
			pos = 4 // Right
		}
		pf.GetMenuBar()
		if len(pf.MenuBar.Items) == 0 {
			// An empty bar has nothing to raise, and vtui indexes Items
			// unconditionally once one is activated.
			return true
		}
		if pos >= len(pf.MenuBar.Items) {
			pos = 0
		}
		pf.MenuBar.Active = true
		pf.MenuBar.SelectPos = pos
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && !pf.CmdLine.IsEmpty() && (!pf.SearchFirstMode() || pf.CommandLineFocused) {
		pf.CmdLine.Clear()
		pf.CmdLine.Edit.HistoryPos = -1
		return true
	}
	// In classic navigation, a non-empty command line owns plain horizontal
	// arrows. With an empty line they remain panel navigation keys (including
	// Detailed view's Left/Right page mapping). Search-first uses explicit
	// focus below, while Vim retains its existing routing.
	if config.App.NavigationMode == config.NavigationClassic && pf.ShowPanels && !pf.CmdLine.IsEmpty() &&
		(e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && !ctrl && !alt {
		return pf.CmdLine.ProcessKey(e)
	}
	// Vim-like hotkeys
	if config.App.NavigationMode == config.NavigationVim && pf.ShowPanels && !alt && !ctrl && !shift && e.Char != 0 && pf.CmdLine.Edit.HistoryPos == -1 {
		isFastFind := false
		if fsp := pf.GetActivePanel(); fsp != nil {
			isFastFind = fsp.FastFindMode
		}

		isChatFocused := false
		if pf.AltPanels[pf.ActiveIdx] != nil && pf.AltPanels[pf.ActiveIdx].Kind() == "ai_chat" && pf.AltPanels[pf.ActiveIdx].IsFocused() {
			isChatFocused = true
		}

		// If fast find or chat is active, Vim hotkeys must be ignored to allow typing 'j', 'k', etc.
		if !isFastFind && !isChatFocused {
			now := time.Now()
			key := e.Char
			cmdLineText := pf.CmdLine.Edit.GetText()

			// Double-press actions (dd, cc, mm)
			if pf.LastKey == key && now.Sub(pf.lastKeyEvent) < 400*time.Millisecond && (cmdLineText == "" || cmdLineText == string(key)) {
				var cmd int
				switch key {
				case 'd':
					cmd = appcmd.CmDelete
				case 'c':
					cmd = appcmd.CmCopy
				case 'm':
					cmd = appcmd.CmMove
				}
				if cmd != 0 {
					pf.CmdLine.Clear()
					vtui.FrameManager.EmitCommand(cmd, nil)
					pf.LastKey = 0
					return true
				}
			}

			// Single-key navigation (strictly only if command line is empty)
			if cmdLineText == "" {
				if fsp := pf.GetActivePanel(); fsp != nil {
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
				pf.LastKey = key
				pf.lastKeyEvent = now
			} else {
				pf.LastKey = 0
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
		// a busy term.PTY has already been served by the raw-forwarding returns
		// at the top of this function, so reaching this point means f4
		// itself owns the keyboard and the panel cursor is still live.
		if ctrl && !alt && !shift {
			pf.InsertSelectedFileName()
			return true
		}
		if shift && !ctrl && !alt {
			RunAction("Panel.SystemExplorer")
			return true
		}
		commandInputActive := !pf.SearchFirstMode() || pf.CommandLineFocused || !pf.ShowPanels
		if commandInputActive && !pf.CmdLine.IsEmpty() {
			cmd := pf.CmdLine.Edit.GetText()
			if cmdline.CommandHasUnmatchedQuote(cmd, runtime.GOOS == "windows") {
				vtui.ShowMessage(" Error ", "Unmatched quote in command. Close the quote and press Enter again.", []string{"&Ok"})
				return true
			}
			pf.AddCommandHistory(cmd)
			pf.CmdLine.Edit.HistoryPos = -1

			trimmedCmd := strings.TrimSpace(cmd)
			lowerCmd := strings.ToLower(trimmedCmd)
			if DispatchCommandPrefix(pf, trimmedCmd) {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				return true
			}
			if lowerCmd == "exit f4" {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				vtui.FrameManager.EmitCommand(vtui.CmQuit, nil)
				return true
			}

			// Intercept directory changes (cd, chdir, drive letters on
			// Windows) so the panel follows them instead of the shell.
			targetPath, isDirChange := parseDirChangeCommand(trimmedCmd)
			if !isDirChange && lowerCmd == "exit" {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				pf.resetLocalShell()
				return true
			}

			// Intercept Far Manager output capturing commands
			if strings.HasPrefix(lowerCmd, "clip:<<") || strings.HasPrefix(lowerCmd, "view:<<") || strings.HasPrefix(lowerCmd, "edit:<<") {
				idx := strings.Index(lowerCmd, ":<<")
				action := lowerCmd[:idx]
				actualCmd := strings.TrimSpace(trimmedCmd[idx+3:])

				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				executeCapturedCommand(pf, action, actualCmd)
				return true
			}

			// A plain edit:<path> opens the named file. Keep this after
			// edit:<< so captured command output keeps its existing meaning.
			if editPath, ok := parsePlainEditCommand(trimmedCmd); ok {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				editPath = ExpandPathEnv(editPath)
				if fsp := pf.GetActivePanel(); fsp != nil && fileops.IsLocalOSVFS(fsp.Vfs) && !filepath.IsAbs(editPath) {
					editPath = fsp.Vfs.Join(fsp.Vfs.GetPath(), editPath)
				}
				OpenEditFileIn(pf, editPath)
				return true
			}

			// Apply to panel first
			if isDirChange {
				if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
					targetPath = ExpandPathEnv(targetPath)
					if pf.NavigateToPath(fsp, targetPath) {
						pf.CmdLine.Clear()
						if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
							pf.SetCommandLineFocus(false)
						}

						// Sync the background term.PTY synchronously to satisfy tests and provide immediate state
						if pf.syncPTYDirectory(fsp.Vfs.GetPath(), fsp.Vfs) {
							pf.LastPtyPath = fsp.Vfs.GetPath()
							pf.LastPtyVFS = fsp.Vfs
						}

						return true
					}
				}
			}

			// A remote filesystem may support commands without exposing an
			// interactive term.PTY. Android FISH+ deliberately uses this shape over ADB
			// shell_v2: route the typed command to its job runner instead of falling
			// through to the local Windows shell. SSH-backed FISH+ keeps using its
			// term.PTY below, preserving the full interactive terminal experience.
			if fsp := pf.GetActivePanel(); fsp != nil && !fileops.IsLocalOSVFS(fsp.Vfs) && !vfsHasRemotePTY(fsp.Vfs) {
				if runner, ok := fsp.Vfs.(vfs.CommandRunner); ok {
					pf.CmdLine.Clear()
					pf.CmdLine.Edit.HistoryPos = -1
					if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
						pf.SetCommandLineFocus(false)
					}
					showRemoteCommandOutput(pf, runner, fsp.Vfs.GetPath(), cmd)
					return true
				}
			}

			// Fallthrough for regular commands or if directory change failed
			if pf.ShellMode == terminal.ShellModeSimpleInline {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				var dir string
				if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
					dir = fsp.Vfs.GetPath()
				}
				pf.RunSimpleInlineCommand(dir, cmd)
				return true
			}

			if pf.ShellMode == terminal.ShellModeSimpleCaptured {
				pf.CmdLine.Clear()
				if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
					pf.SetCommandLineFocus(false)
				}
				var dir string
				if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
					dir = fsp.Vfs.GetPath()
				}
				pf.RunSimpleCapturedCommand(dir, cmd)
				return true
			}

			activePty := pf.GetActivePTY()
			if activePty != nil {
				var path string
				isWindowsShell := runtime.GOOS == "windows"
				var localShellVFS vfs.VFS
				var integration vfs.PtyShellIntegration
				if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
					if fileops.IsLocalOSVFS(fsp.Vfs) {
						path = fsp.Vfs.GetPath()
						localShellVFS = fsp.Vfs
					} else if vfsHasRemotePTY(fsp.Vfs) {
						path = fsp.Vfs.GetPath()
						isWindowsShell = false
					}
					// A VFS that carries its own term.PTY-shell templates
					// takes over the wire-command composition. FISH+
					// against a Windows peer takes this branch so the
					// term.PTY (cmd.exe by default) gets syntax cmd actually
					// parses — the bash-shaped OSC-133-wrapped template
					// below would come through as literal noise.
					if integ, ok2 := fsp.Vfs.(vfs.PtyShellIntegration); ok2 {
						integration = integ
					}
				}

				var fullWireCmd string
				isBackground := false
				if !isWindowsShell {
					isBackground = strings.HasSuffix(strings.TrimSpace(cmd), "&")
				}

				if isWindowsShell {
					cmd = cmdline.ResolveWindowsCommand(cmd)
				}

				// The local Unix term.PTY is a persistent shell session. Its current
				// directory must not be reset to the panel path before every
				// command: doing that discards a `cd` performed by an alias. The
				// panel still synchronizes the shell when the panel itself moves;
				// this one-shot check only covers an Enter arriving before the
				// next frame refresh.
				if localShellVFS != nil && !isWindowsShell {
					if path != pf.LastPtyPath || !fileops.SameVFSInstance(localShellVFS, pf.LastPtyVFS) {
						if pf.syncPTYDirectory(path, localShellVFS) {
							pf.LastPtyPath = path
							pf.LastPtyVFS = localShellVFS
						}
					}
					path = ""
				}

				if integration != nil {
					if seq := integration.PtyRunCommand(path, cmd); len(seq) > 0 {
						fullWireCmd = string(seq)
						pf.BeginPromptDrivenExecution()
						pf.ReturnToPanels = pf.ShowPanels
					}
				} else if isWindowsShell {
					// Use a combined command for reliable excision in term.AnsiParser: cd /d "path" & command
					if path != "" {
						fullWireCmd = fmt.Sprintf("cd /d \"%s\" & %s\r", path, cmd)
					} else {
						fullWireCmd = fmt.Sprintf("%s\r", cmd)
					}
					pf.BeginPromptDrivenExecution()
					pf.ReturnToPanels = pf.ShowPanels
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
						sqCmd := ShellSingleQuote(cmd)
						if path != "" {
							sqPath := strings.ReplaceAll(path, "'", "'\\''")
							fullWireCmd = fmt.Sprintf(" set +H; cd '%s' && { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; eval %s ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqPath, sqCmd)
						} else {
							fullWireCmd = fmt.Sprintf(" { trap \"printf ''\" INT; printf \"\\033]133;C\\007\"; eval %s ; FARVTRESULT=$?; printf \"\\033]133;D\\007\"; trap - INT; (exit $FARVTRESULT); }\r", sqCmd)
						}
						pf.BeginManagedExecution()
						pf.ReturnToPanels = pf.ShowPanels
					}
				}

				// Print the clean command in the local terminal echo
				// so the user sees what was sent. OSC-133 mute is only
				// safe for the shells that emit the matching D marker
				// — the bash templates above do, cmd.exe and the
				// integration path do not.
				if !isWindowsShell && integration == nil {
					pf.TermView.PrintCleanCommand(cmd)
					if !isBackground {
						pf.TermView.SetMuted(true)
					}
				} else if integration != nil {
					pf.TermView.PrintCleanCommand(cmd)
				}
				pf.workspaceCommandTitle = workspaceCommandName(trimmedCmd)
				_, _ = pf.WritePTY(activePty, []byte(fullWireCmd))
				if isWindowsShell && integration == nil {
					if cmdline.IsBatchCommand(cmd) {
						pf.CmdSession.NoteBatchExecution()
					}
					pf.NoteLocalShellLineSent(activePty)
				}
			}

			pf.CmdLine.Clear()
			if pf.SearchFirstMode() && !config.App.SearchCommandStayFocused {
				pf.SetCommandLineFocus(false)
			}
			pf.ShowPanels = false
			if pf.ShellMode == terminal.ShellModeHost {
				pf.EnterHostConsole()
			}
			return true
		} else if pf.SearchFirstMode() && pf.CommandLineFocused && pf.ShowPanels {
			// An empty command line must not activate the selected panel item.
			return true
		} else if !pf.ShowPanels {
			activePty := pf.GetActivePTY()
			if activePty != nil {
				_, _ = pf.WritePTY(activePty, []byte("\r"))
			}
			return true
		} else {

			// CommandLine is empty, panels are visible.
			if fsp := pf.GetActivePanel(); fsp != nil && !ctrl && !alt && !shift &&
				DispatchPanelAction(pf, vfs.PanelActionActivate, SelectedPanelActionPaths(fsp)) {
				return true
			}

			// 1. Try passing to panel to handle directory entry.
			handled := pf.Active().ProcessKey(e)

			// 2. If panel didn't handle it, it's a file. Execute or open it.
			if !handled {
				fsp := pf.GetActivePanel()
				if fsp == nil {
					return true
				}

				name := fsp.GetSelectedName()
				if name != "" && name != ".." {
					path := fsp.Vfs.Join(fsp.Vfs.GetPath(), name)
					Execute(pf, fsp.Vfs, fsp.Vfs.GetPath(), name, path)
				}
			}
			return true
		}
	}

	// Selection by mask (+, -, *) typed as plain characters on an empty
	// command line. The gray numpad keys are bound to the corresponding
	// actions (Panel.SelectGroup / DeselectGroup / InvertSelection);
	// both paths are suspended while fast find is active.
	if pf.ShowPanels && config.App.NavigationMode != config.NavigationSearchFirst && !alt && !ctrl && pf.CmdLine.IsEmpty() {
		if e.Char == '+' || e.Char == '-' || e.Char == '*' {
			isFastFind := false
			if fsp := pf.GetActivePanel(); fsp != nil && fsp.FastFindMode {
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
	if (e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && ctrl && !alt && shift && e.KeyDown && pf.ShowPanels &&
		!pf.commandLineOwnsSelection(e) {
		panelIdx := 0
		if e.VirtualKeyCode == vtinput.VK_RIGHT {
			panelIdx = 1
		}
		pf.ShowDriveMenu(panelIdx)
		return true
	}

	// Tab switches panels
	if e.VirtualKeyCode == vtinput.VK_TAB && !ctrl {
		if pf.ShowPanels && (!pf.SearchFirstMode() || !pf.CommandLineFocused) {
			otherIdx := 1 - pf.ActiveIdx
			otherVisible := pf.Wide || (otherIdx == 0 && pf.ShowLeftPanel) || (otherIdx == 1 && pf.ShowRightPanel)
			if otherVisible {
				pf.ActiveIdx = otherIdx
				if pf.Wide {
					pf.WidePanel = pf.ActiveIdx
					pf.ResizeConsole(pf.LastW, pf.LastH)
					pf.LastKey = 0
					return true
				}
				pf.LastKey = 0
				if pf.SearchFirstMode() {
					pf.SetCommandLineFocus(false)
				}
				return true
			}
		}
		if config.App.CommandLineAutoComplete && !pf.CmdLine.IsEmpty() {
			acMenu := vtui.NewAutoCompleteMenu(pf.CmdLine.Edit)
			if acMenu.HasMatches() {
				vtui.FrameManager.Push(acMenu)
				return true
			}
		}
		if pf.ShowPanels {
			return true
		}
	}

	// With a non-empty command line, Ctrl+Left/Right are word navigation
	// in the command line; the panel-split resize actions on the same
	// keys are gated by the EmptyCommandLine condition and lose.
	if (e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT) && ctrl && !alt && !shift && e.KeyDown && pf.ShowPanels && !pf.CmdLine.IsEmpty() {
		if pf.CmdLine.IsVisible() {
			pf.CmdLine.ProcessKey(e)
			return true
		}
	}

	// Ctrl+Shift+Left/Right extend that selection word by word. This has
	// to run before the injected-event hotkey lookup below, which would
	// otherwise reach the drive menu the same way the filter does.
	if pf.commandLineOwnsSelection(e) {
		pf.CmdLine.ProcessKey(e)
		return true
	}

	// 3. Try Active Panel
	if pf.ShowPanels && (!pf.SearchFirstMode() || !pf.CommandLineFocused) {
		if pf.Active().ProcessKey(e) {
			return true
		}
	} else {
		// Navigation keys in the command line use command history, whether
		// panels are hidden or search-first focus is explicitly below.
		if e.VirtualKeyCode == vtinput.VK_UP || (e.VirtualKeyCode == vtinput.VK_E && ctrl) {
			pf.CmdLine.Edit.HistoryUp()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_DOWN || (e.VirtualKeyCode == vtinput.VK_X && ctrl) {
			pf.CmdLine.Edit.HistoryDown()
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
	if MacroHotkey(e) {
		return true
	}

	// 5. Fallback: pass to CommandLine (handles text, Backspace, Delete, etc.)
	if (!pf.SearchFirstMode() || pf.CommandLineFocused || !pf.ShowPanels) && pf.CmdLine.ProcessKey(e) {
		pf.CmdLine.SetFocus(true)
		return true
	}

	return false
}
func (pf *PanelsFrame) HandleBroadcast(cmd int, args any) bool {
	if cmd == appcmd.CmFileChanged {
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
	for i, a := range pf.AltPanels {
		if a == nil {
			continue
		}
		if pf.Wide && i != pf.WidePanel {
			continue
		}
		if !pf.Wide && i == 0 && !pf.ShowLeftPanel {
			continue
		}
		if !pf.Wide && i == 1 && !pf.ShowRightPanel {
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
// owns the visible input, or into the term.PTY otherwise. Returns true when it
// consumed the event.
func (pf *PanelsFrame) hiddenConsoleCommandLineOwnsInput() bool {
	if pf.ShowPanels || pf.CmdLine == nil || !pf.CmdLine.IsVisible() {
		return false
	}

	// zoin-bot: a visible f4 command line must receive mouse paste through
	// the edit control, otherwise Enter can execute text that was never drawn.
	switch pf.ShellMode {
	case terminal.ShellModeHost:
		return pf.consoleStyle() == terminal.ConsoleViewFar && pf.IsHostConsoleActive()
	case terminal.ShellModeSimpleInline:
		return pf.consoleStyle() == terminal.ConsoleViewFar && pf.ConsoleViewActive()
	default:
		return pf.TermView != nil && !pf.TermView.OnAltScreen() && !pf.IsPtyBusy()
	}
}

func (pf *PanelsFrame) pasteHiddenConsoleText(text string) {
	if text == "" {
		return
	}
	pf.CmdLine.InsertString(text)
	pf.CmdLine.SetFocus(true)
	if pf.ShellMode == terminal.ShellModeHost {
		pf.drawHostConsoleOverlay()
	} else if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}

func (pf *PanelsFrame) handleTerminalMouseSelection(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return false
	}
	tv := pf.TermView
	if tv == nil {
		return false
	}
	mx, my := int(e.MouseX), int(e.MouseY)

	// Right button: paste on button-down so the release doesn't paste a second
	// time. The visible command line is handled before terminal hit testing.
	if e.ButtonState == vtinput.RightmostButtonPressed && e.KeyDown {
		if pf.hiddenConsoleCommandLineOwnsInput() {
			pf.pasteHiddenConsoleText(tv.ReadClipboard())
			return true
		}
		if !tv.InTerminalArea(mx, my) {
			return false
		}
		if pty := pf.GetActivePTY(); pty != nil {
			text := tv.ReadClipboard()
			if text != "" {
				if tv.BracketedPasteMode {
					_, _ = pf.WritePTY(pty, []byte("\x1b[200~"+text+"\x1b[201~"))
				} else {
					_, _ = pf.WritePTY(pty, []byte(text))
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
				go tv.CopySelectionToClipboard(text)
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
	// If panels are hidden, route relevant mouse events to term.PTY immediately
	if !pf.ShowPanels {
		mx, my := int(e.MouseX), int(e.MouseY)
		if pf.TermView != nil && e.WheelDirection == 0 {
			if changed := pf.TermView.UpdateURLHover(mx, my); changed {
				vtui.FrameManager.Redraw()
			}
			if viewer.CtrlMouseClick(e) {
				if rawURL, ok := pf.TermView.URLAt(mx, my); ok {
					viewer.OpenExternalURLAsync(rawURL)
					return true
				}
			}
		}
		active := pf.GetActivePTY()
		if active != nil && terminalWantsMouseEvent(pf.TermView.MouseTrackingMode, e) {
			seq := keymap.TranslateMouseInput(keymap.RebaseTerminalMouseEvent(
				e,
				pf.TermView.X1, pf.TermView.Y1,
				pf.TermView.Width, pf.TermView.Height,
			))
			_, _ = pf.WritePTY(active, []byte(seq))
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
	if pf.ProcessDragOutGesture(e, mx, my) {
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
	if pf.PanelMouseCapture != nil {
		captured := pf.PanelMouseCapture
		captured.ProcessMouse(e)
		isMove := e.MouseEventFlags&vtinput.MouseMoved != 0
		isRelease := !isMove && (e.ButtonState == 0 || !e.KeyDown)
		if isRelease {
			pf.PanelMouseCapture = nil
		}
		return true
	}

	if pf.SearchFirstMode() && e.WheelDirection == 0 && e.ButtonState != 0 && e.KeyDown && pf.CmdLine.IsVisible() {
		x1, y1, x2, y2 := pf.CmdLine.GetPosition()
		if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
			pf.SetCommandLineFocus(true)
			pf.CmdLine.ProcessMouse(e)
			return true
		}
	}

	// Активация меню кликом мыши на нулевую строку (AlwaysShowMenuBar)
	if config.App.AlwaysShowMenuBar && pf.ShowPanels && my == 0 && e.WheelDirection == 0 && e.ButtonState != 0 {
		pf.MenuBar.Active = true
		pf.MenuBar.ProcessMouse(e)
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
	if pf.ShowPanels && e.WheelDirection == 0 && e.ButtonState != 0 {
		if i := pf.hitAltPanel(mx, my); i >= 0 {
			// Click on an alt panel activates its side, same as
			// a click on a file panel does.
			if pf.ActiveIdx != i {
				pf.ActiveIdx = i
				pf.LastKey = 0
				vtui.FrameManager.Redraw()
			}
			if pf.SearchFirstMode() {
				pf.SetCommandLineFocus(false)
			}
			// Give the alt panel a chance to handle it (future
			// row-picking, etc.); return true either way — the
			// event does not fall through.
			pf.AltPanels[i].ProcessMouse(e)
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
		targetIdx := pf.ActiveIdx

		// 1. If the active slot has an active alt panel, scroll it
		if pf.ShowPanels && pf.AltPanels[targetIdx] != nil {
			if pf.AltPanels[targetIdx].ProcessMouse(e) {
				return true
			}
		}

		// 2. Otherwise, scroll the active regular panel
		if pf.ShowPanels && pf.Panels[targetIdx] != nil {
			if pf.Panels[targetIdx].ProcessMouse(e) {
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

	for i, p := range pf.Panels {
		if p == nil {
			continue
		}
		if pf.Wide && i != pf.WidePanel {
			continue
		}
		if !pf.Wide && i == 0 && !pf.ShowLeftPanel {
			continue
		}
		if !pf.Wide && i == 1 && !pf.ShowRightPanel {
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
						pf.ShowDriveMenu(i)
						return true
					}
					if _, header := fsp.headerSortModeAt(mx, my); header {
						SortMenuForPanel(pf, fsp)
						return true
					}
				}
			}
			if pf.ActiveIdx != i && e.ButtonState != 0 {
				pf.ActiveIdx = i
				pf.LastKey = 0
				vtui.FrameManager.Redraw()
			}
			if pf.SearchFirstMode() && e.ButtonState != 0 {
				pf.SetCommandLineFocus(false)
			}
			if isInitialPress {
				pf.PanelMouseCapture = p
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

func (pf *PanelsFrame) GetActivePanel() *FileSystemPanel {
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		return fsp
	}
	return nil
}

func (pf *PanelsFrame) GetInactivePanel() *FileSystemPanel {
	if fsp, ok := pf.Passive().(*FileSystemPanel); ok {
		return fsp
	}
	return nil
}

// cancelFastFind closes the transient search UI whenever control leaves the
// file panels. It is intentionally frame-wide: only one panel should own Fast
// Find, but clearing both slots prevents a stale inactive search from coming
// back after Tab, a panel swap, or an overlay closes.
func (pf *PanelsFrame) CancelFastFind() bool {
	cancelled := false
	for _, panel := range pf.Panels {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || !fsp.FastFindMode {
			continue
		}
		fsp.FastFindMode = false
		fsp.FastFindStr = ""
		cancelled = true
	}
	return cancelled
}

func (pf *PanelsFrame) GetPaths() (string, string) {
	l, r := "", ""
	if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
		l = fsp.PersistentPath()
	}
	if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
		r = fsp.PersistentPath()
	}
	return l, r
}

// HandleCommand intercepts global commands (like CmQuit or appcmd.CmCopy)
// sent by menus or other views.
func (pf *PanelsFrame) HandleCommand(cmd int, args any) bool {
	switch cmd {
	case vtui.CmQuit:
		active := 0
		if fileops.GlobalQueueManager != nil {
			active = fileops.GlobalQueueManager.ActiveTasksCount()
		}
		if config.App.ConfirmExit || active > 0 {
			msg := i18n.Msg("Quit.Confirm")
			if active > 0 {
				msg = fmt.Sprintf("There are %d active background operations!\nIf you exit, they will be aborted.\n\n%s", active, msg)
			}
			dlg := vtui.ShowMessage(i18n.Msg("Quit.Title"), msg, []string{i18n.Msg("Quit.Btn"), i18n.Msg("vtui.Cancel")})
			// When background operations would be aborted the exit is
			// genuinely destructive — flip to the WarnDialog palette.
			// Plain "confirm on exit" stays on the neutral palette.
			if active > 0 {
				dlg.IsWarning = true
			}
			dlg.OnResult = func(code int) {
				if code == 0 {
					CancelOperationsForShutdown()
					SaveSession()
					if pty := pf.takeLocalPTY(); pty != nil {
						pty.Close()
					}
					ShutdownProcessEnvironmentRuntime()
					vtui.FrameManager.Shutdown()
				}
			}
		} else {
			CancelOperationsForShutdown()
			SaveSession()
			if pty := pf.takeLocalPTY(); pty != nil {
				pty.Close()
			}
			ShutdownProcessEnvironmentRuntime()
			vtui.FrameManager.Shutdown()
		}
		return true

	case vtui.CmHelp:
		pf.ShowHelp()
		return true

	case appcmd.CmNew:
		return AppCommand(pf, cmd, args)

	case appcmd.CmView:
		return AppCommand(pf, cmd, args)

	case appcmd.CmEdit:
		return AppCommand(pf, cmd, args)

	case appcmd.CmCopy, appcmd.CmMove:
		return AppCommand(pf, cmd, args)

	case appcmd.CmRename:
		return AppCommand(pf, cmd, args)

	case appcmd.CmMkDir:
		return AppCommand(pf, cmd, args)

	case appcmd.CmDelete:
		return AppCommand(pf, cmd, args)
	case appcmd.CmFindFile:
		return AppCommand(pf, cmd, args)
	case appcmd.CmSwitchToViewer:
		if ev, ok := args.(*editor.EditorView); ok {
			doSwitch := func() {
				path := ev.FilePath
				v := ev.Vfs
				ev.Close()
				OpenViewer(pf, v, path)
			}
			if ev.Modified {
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
	case appcmd.CmSwitchToEditor:
		if vv, ok := args.(*viewer.ViewerView); ok {
			path := vv.Path
			v := vv.VFS
			vv.Close()
			OpenEditor(pf, v, path)
			return true
		}
		return false
	case appcmd.CmBookmarks:
		ShowBookmarksDialog(pf)
		return true
	case appcmd.CmPanelSettings, appcmd.CmEditorSettings, appcmd.CmColorerSettings,
		appcmd.CmAppearanceSettings, appcmd.CmConfirmationsSettings, appcmd.CmHotkeyConfig,
		appcmd.CmLanguage, appcmd.CmHelpLanguage, appcmd.CmUpdateSettings, appcmd.CmProxySettings,
		appcmd.CmPlugins, appcmd.CmPlugRing:
		return AppCommand(pf, cmd, args)
	case appcmd.CmRightDriveMenu:
		pf.ShowDriveMenu(1)
		return true
	case vtui.CmResize: // Used as a hack for 'fork' command from FrameManager
		if s, ok := args.(string); ok && s == "fork" {
			vtui.FrameManager.AddScreen(pf.forkPanelsClone())
			return true
		}

	case appcmd.CmLeftBrief:
		pf.SetPanelViewMode(0, ViewModeBrief)
		return true
	case appcmd.CmLeftMedium:
		pf.SetPanelViewMode(0, ViewModeMedium)
		return true
	case appcmd.CmLeftDetailed:
		pf.SetPanelViewMode(0, ViewModeDetailed)
		return true
	case appcmd.CmLeftWide:
		pf.SetWidePanel(0)
		return true
	case appcmd.CmRightBrief:
		pf.SetPanelViewMode(1, ViewModeBrief)
		return true
	case appcmd.CmRightMedium:
		pf.SetPanelViewMode(1, ViewModeMedium)
		return true
	case appcmd.CmRightDetailed:
		pf.SetPanelViewMode(1, ViewModeDetailed)
		return true
	case appcmd.CmRightWide:
		pf.SetWidePanel(1)
		return true
	case appcmd.CmLeftAIContext:
		if aiCmd, ok := pf.Panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://ctx", false)
		}
		return true
	case appcmd.CmLeftAIChat:
		if aiCmd, ok := pf.Panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://chat", true)
		}
		return true
	case appcmd.CmLeftAIOut:
		if aiCmd, ok := pf.Panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://out", false)
		}
		return true
	case appcmd.CmLeftAIMem:
		if aiCmd, ok := pf.Panels[0].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://mem", false)
		}
		return true
	case appcmd.CmRightAIContext:
		if aiCmd, ok := pf.Panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://ctx", false)
		}
		return true
	case appcmd.CmRightAIChat:
		if aiCmd, ok := pf.Panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://chat", true)
		}
		return true
	case appcmd.CmRightAIOut:
		if aiCmd, ok := pf.Panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://out", false)
		}
		return true
	case appcmd.CmRightAIMem:
		if aiCmd, ok := pf.Panels[1].(interface{ AiSetViewMode(string, bool) }); ok {
			aiCmd.AiSetViewMode("ai://mem", false)
		}
		return true

	case appcmd.CmLeftSortName:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortName)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmLeftSortExt:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortExt)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmLeftSortTime:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortTime)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmLeftSortSize:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortSize)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmLeftSortUnsorted:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortName:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortName)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortExt:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortExt)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortTime:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortTime)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortSize:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortSize)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortUnsorted:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmLeftSortGroups:
		if fsp, ok := pf.Panels[0].(*FileSystemPanel); ok {
			fsp.ToggleSortGroups()
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmRightSortGroups:
		if fsp, ok := pf.Panels[1].(*FileSystemPanel); ok {
			fsp.ToggleSortGroups()
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSwapPanels:
		pf.Panels[0], pf.Panels[1] = pf.Panels[1], pf.Panels[0]
		pf.ActiveIdx = 1 - pf.ActiveIdx
		if pf.Wide {
			pf.WidePanel = 1 - pf.WidePanel
		}
		pf.ResizeConsole(pf.LastW, pf.LastH)
		return true
	case appcmd.CmSortName:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.SetSortMode(SortName)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSortExt:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.SetSortMode(SortExt)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSortTime:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.SetSortMode(SortTime)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSortSize:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.SetSortMode(SortSize)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSortUnsorted:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.SetSortMode(SortUnsorted)
		}
		pf.UpdateMenuCheckmarks()
		return true
	case appcmd.CmSortGroups:
		if fsp := pf.GetActivePanel(); fsp != nil {
			fsp.ToggleSortGroups()
		}
		pf.UpdateMenuCheckmarks()
		return true
	}
	return false
}

func (pf *PanelsFrame) GetKeyLabels() *vtui.KeySet {
	area := CurrentArea()

	f2 := i18n.Msg("KeyBar.F2")
	f7 := i18n.Msg("KeyBar.F7")
	overrideF2 := false
	if pf.ShowPanels && pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
		if q, ok := pf.AltPanels[pf.ActiveIdx].(*QuickViewPanel); ok && q != nil && q.IsFocused() {
			nextCP := vfs.DisplayCodepageName(vfs.GetNextFastSwitchCodepage(q.cacheCodepage))
			return &vtui.KeySet{
				Normal: vtui.KeyBarLabels{
					i18n.Msg("KeyBar.ViewerF1"),
					func() string {
						if q.Wrap {
							return i18n.Msg("KeyBar.ViewerF2")
						}
						return i18n.Msg("KeyBar.F2Wrap")
					}(),
					i18n.Msg("KeyBar.ViewerF3"), i18n.Msg("KeyBar.ViewerF4"),
					"", "", i18n.Msg("KeyBar.ViewerF7"), nextCP, "", i18n.Msg("KeyBar.ViewerF10"),
				},
				Shift: vtui.KeyBarLabels{"", "", "", "", "", "", i18n.Msg("KeyBar.ViewerF7"), i18n.Msg("Codepage.Title"), "", "", "", ""},
			}
		}
	}
	if pf.ShowPanels && pf.ActiveIdx >= 0 && pf.ActiveIdx < len(pf.AltPanels) {
		if fsp := pf.GetActivePanel(); fsp != nil {
			if _, ok := fsp.Vfs.(*TempPanelVFS); ok {
				f7 = i18n.Msg("TempPanel.Remove")
			}
		}
		if a := pf.AltPanels[pf.ActiveIdx]; a != nil && a.IsFocused() && a.Kind() == "quick_view" {
			if q, ok := a.(*QuickViewPanel); ok {
				if q.Wrap {
					f2 = i18n.Msg("KeyBar.F2Unwrap")
				} else {
					f2 = i18n.Msg("KeyBar.F2Wrap")
				}
				overrideF2 = true
			}
		}
	}

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			i18n.Msg("KeyBar.F1"), f2, i18n.Msg("KeyBar.F3"), i18n.Msg("KeyBar.F4"),
			i18n.Msg("KeyBar.F5"), i18n.Msg("KeyBar.F6"), f7, i18n.Msg("KeyBar.F8"),
			i18n.Msg("KeyBar.F9"), i18n.Msg("KeyBar.F10"), i18n.Msg("KeyBar.F11"), i18n.Msg("KeyBar.F12"),
		},
		Shift: vtui.KeyBarLabels{"", "", "", "", "", "Rename", "", "", "Save", "", "", ""},
		Alt: vtui.KeyBarLabels{
			i18n.Msg("KeyBar.AltF1"), i18n.Msg("KeyBar.AltF2"), i18n.Msg("KeyBar.AltF3"), "",
			"", "", i18n.Msg("KeyBar.AltF7"), i18n.Msg("KeyBar.AltF8"), "", "", "", i18n.Msg("KeyBar.AltF12"),
		},
		Ctrl: vtui.KeyBarLabels{
			i18n.Msg("KeyBar.CtrlF1"), i18n.Msg("KeyBar.CtrlF2"), i18n.Msg("KeyBar.CtrlF3"), i18n.Msg("KeyBar.CtrlF4"), i18n.Msg("KeyBar.CtrlF5"), i18n.Msg("KeyBar.CtrlF6"), i18n.Msg("KeyBar.CtrlF7"), "", "", "", "Fork", "Close",
		},
	}
	res := keymap.KeyBarLabelsForArea(area, fallbacks)
	if overrideF2 {
		res.Normal[1] = f2
	}
	return res
}

func (pf *PanelsFrame) GetType() vtui.FrameType { return vtui.TypeUser + 1 }

func (pf *PanelsFrame) SetExitCode(code int) { pf.Done = true; pf.ExitCode = code }
func (pf *PanelsFrame) ShowDummyOpDialog() {
	msg := i18n.Msg("Op.DummyText")
	lines := vtui.WrapText(msg, 50-4)

	dlg := vtui.NewCenteredDialog(50, 11+len(lines)-1, i18n.Msg("Op.DummyTitle"))
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, (11+len(lines)-1)-4)

	for _, l := range lines {
		t := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(t)
		vbox.Add(t, vtui.Margins{}, vtui.AlignLeft)
	}

	modes := []string{i18n.Msg("Op.DummyQueue"), i18n.Msg("Op.DummyBackground"), i18n.Msg("Op.DummyForeground")}
	comboMode := vtui.NewComboBox(0, 0, 32, modes)
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(0)
	comboMode.Edit.SetText(comboMode.Menu.Items[0].Text)

	btnStart := vtui.NewButton(0, 0, i18n.Msg("Op.BtnStart"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
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
	pf.RunProgressTaskAfter(0, title, startMsg, forked, worker, onComplete)
}

// runProgressTaskAfter starts the worker immediately but postpones showing its
// dialog. A task that completes before delay never creates a visible screen,
// avoiding a one-frame flash for fast remote operations.
// progressBlockedByModal reports whether a modal frame currently owns the
// active screen, so a progress screen must not be placed over it.
func progressBlockedByModal(frames interface{ GetTopFrame() vtui.Frame }) bool {
	top := frames.GetTopFrame()
	return top != nil && top.IsModal()
}

func (pf *PanelsFrame) RunProgressTaskAfter(delay time.Duration, title, startMsg string, forked bool, worker func(ctx context.Context, update func(msg string, percent int)) error, onComplete func(err error)) {
	dlg := vtui.NewCenteredDialog(50, 12, title)
	dlg.AttentionSuppressed = true

	lbl := vtui.NewText(0, 0, startMsg, vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lbl)

	pb := vtui.NewProgressBar(0, 0, 46)
	dlg.AddItem(pb)

	lblHint := vtui.NewText(0, 0, i18n.Msg("Op.SwitchHint"), vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lblHint)

	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
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
		// the worker blocked forever. The prompt cycle is also held by the
		// worker itself (vfs.HoldInteractivePrompt), which covers the gap
		// between a rejected answer and the next dialog, when no modal frame
		// is on screen yet. Retry after the prompt is over.
		if vfs.InteractivePromptPending() || progressBlockedByModal(uiFrames) {
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
	dlg := fileops.NewFileOpProgressDialog(title)
	var taskCtx *vtui.TaskContext
	done := make(chan struct{})
	dialogShown := false // accessed only from UI tasks
	dlg.SetOnCancel(func() { dlg.SetExitCode(1) })
	dlg.OnResult = func(code int) {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
	}

	reporter := fileops.NewDialogReporter(dlg)

	uiFrames := vtui.FrameManager
	var showDialog func()
	showDialog = func() {
		select {
		case <-done:
			return
		default:
		}
		// Keep a password or other modal prompt reachable if the worker posts
		// it before this progress screen reaches the UI queue, and stay away
		// while the worker is between two prompts of the same cycle.
		if vfs.InteractivePromptPending() || progressBlockedByModal(uiFrames) {
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
	runFunc := func(ctx context.Context, reporter fileops.TaskReporter, anchor vtui.Frame) error {
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
		fileops.GlobalQueueManager.Enqueue(&fileops.QueueTask{
			Type: "Dummy",
			Desc: desc,
			Run:  runFunc,
			OnComplete: func() {
				toast.Show("Dummy operation finished successfully", 3*time.Second)
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
func (pf *PanelsFrame) ToggleAltPanel(kind string, factory func(src *FileSystemPanel) AltPanel) {
	if !pf.ShowPanels {
		return
	}
	TryClose := func(a AltPanel) {
		if c, ok := a.(interface{ Close() }); ok {
			c.Close()
		}
	}
	opp := 1 - pf.ActiveIdx
	switch {
	case pf.AltPanels[pf.ActiveIdx] != nil && pf.AltPanels[pf.ActiveIdx].Kind() == kind:
		TryClose(pf.AltPanels[pf.ActiveIdx])
		pf.AltPanels[pf.ActiveIdx] = nil
	case pf.AltPanels[opp] != nil && pf.AltPanels[opp].Kind() == kind:
		TryClose(pf.AltPanels[opp])
		pf.AltPanels[opp] = nil
	default:
		if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
			if pf.AltPanels[opp] != nil {
				TryClose(pf.AltPanels[opp])
			}
			pf.AltPanels[opp] = factory(fsp)
			// If the opposite side is currently hidden (Ctrl+F1/F2), un-hide
			// it — otherwise the alt panel installs into an invisible slot
			// and Ctrl+L / Ctrl+Q look like a no-op.
			if opp == 0 {
				pf.ShowLeftPanel = true
			} else {
				pf.ShowRightPanel = true
			}
		}
	}
	pf.ResizeConsole(pf.LastW, pf.LastH)
	vtui.FrameManager.HardRefresh()
}

func (pf *PanelsFrame) RefreshAll() {
	if pf == nil {
		return
	}
	for _, p := range pf.Panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			fsp.ReadDirectory()
		}
	}
}
func (pf *PanelsFrame) Message(title, msg string, buttons []string) int {
	resChan := make(chan int, 1)
	vtui.FrameManager.PostTask(func() {
		dlg := vtui.ShowMessageOn(pf, title, msg, buttons)
		dlg.OnResult = func(code int) { resChan <- code }
	})
	return <-resChan
}

// InputBox prompts the user. The third argument is the text the field opens
// with, not a history bucket name -- matching Host.InputBox in the SDK, where
// the same value travels as InputBoxReq.Default.
func (pf *PanelsFrame) InputBox(title, prompt, defaultText string, callback func(string)) {
	vtui.FrameManager.PostTask(func() {
		vtui.InputBoxOn(pf, title, prompt, defaultText, callback)
	})
}

type menuKeyLabelsFrame struct {
	*vtui.VMenu
	keyLabels *vtui.KeySet
}

func (f *menuKeyLabelsFrame) GetKeyLabels() *vtui.KeySet { return f.keyLabels }

func (f *menuKeyLabelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	handled := f.VMenu.ProcessKey(e)
	// VMenu compares FrameManager.GetTopFrame with its embedded menu when
	// deciding whether Esc/F10 is consumed. The wrapper is the actual frame,
	// so complete the same contract after forwarding the event.
	if e != nil && e.KeyDown && f.IsDone() &&
		(e.VirtualKeyCode == vtinput.VK_ESCAPE || e.VirtualKeyCode == vtinput.VK_F10) {
		return true
	}
	return handled
}

func (pf *PanelsFrame) Menu(title string, items []string, callback func(int)) {
	menuItems := make([]vtui.MenuItem, 0, len(items))
	for _, item := range items {
		menuItems = append(menuItems, vtui.MenuItem{Text: item})
	}
	pf.menuItems(title, menuItems, nil, callback)
}

func (pf *PanelsFrame) menuItems(title string, items []vtui.MenuItem, onKeyDown func(*vtui.VMenu, *vtinput.InputEvent) bool, callback func(int)) {
	pf.menuItemsWithKeyLabels(title, items, onKeyDown, callback, nil)
}

func (pf *PanelsFrame) menuItemsWithKeyLabels(title string, items []vtui.MenuItem, onKeyDown func(*vtui.VMenu, *vtinput.InputEvent) bool, callback func(int), keyLabels *vtui.KeySet) {
	vtui.FrameManager.PostTask(func() {
		menu := vtui.NewVMenu(title)

		// Calculate dynamic width based on items and title
		maxW := runewidth.StringWidth(title) + 10
		for _, item := range items {
			menu.AddItem(item)
			clean, _, _ := vtui.ParseAmpersandString(item.Text)
			w := runewidth.StringWidth(clean) + runewidth.StringWidth(item.Shortcut) + 8 // padding for markers and borders
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
		x := (pf.LastW - maxW) / 2
		y := (pf.LastH - h) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}

		menu.SetPosition(x, y, x+maxW-1, y+h-1)
		if onKeyDown != nil {
			menu.OnKeyDown = func(e *vtinput.InputEvent) bool {
				return onKeyDown(menu, e)
			}
		}

		menu.OnAction = func(idx int) {
			menu.Close()
			if callback != nil {
				callback(idx)
			}
		}
		if keyLabels != nil {
			vtui.FrameManager.PushToFrameScreen(pf, &menuKeyLabelsFrame{VMenu: menu, keyLabels: keyLabels})
		} else {
			vtui.FrameManager.PushToFrameScreen(pf, menu)
		}
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

	activePty := pf.GetActivePTY()
	if activePty == nil {
		return false
	}

	// A VFS that knows what its own term.PTY speaks composes the sync line
	// itself. FISH+ against a Windows peer takes this branch so the
	// term.PTY (cmd.exe by default) sees a "cd /d \"C:\\...\" & rem f4_sync"
	// instead of the bash-shaped one, which cmd treats as a broken
	// argument and complains about at every step.
	if integ, ok := v.(vfs.PtyShellIntegration); ok {
		if seq := integ.PtyChangeDirCommand(path); len(seq) > 0 {
			_, _ = pf.WritePTY(activePty, seq)
		}
		return true
	}

	if isWindowsShell {
		_, _ = pf.WritePTY(activePty, []byte(fmt.Sprintf("cd /d \"%s\" & rem f4_sync\r", path)))
		pf.NoteLocalShellLineSent(activePty)
	} else {
		sqPath := strings.ReplaceAll(path, "'", "'\\''")
		// "&& true f4_sync" instead of "# f4_sync": stock zsh ships with
		// interactive_comments off, so a trailing comment becomes extra cd
		// arguments ("cd: too many arguments"). true ignores its arguments
		// in every POSIX-ish shell including fish.
		_, _ = pf.WritePTY(activePty, []byte(fmt.Sprintf(" cd '%s' && true f4_sync\r", sqPath)))
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

func (pf *PanelsFrame) getActivePTYUnsafe() terminal.PtyBackend {
	if pf.RemotePtys == nil {
		pf.RemotePtys = make(map[vfs.VFS]terminal.PtyBackend)
	}

	var activeVfs vfs.VFS
	if fsp := pf.GetActivePanel(); fsp != nil {
		activeVfs = fsp.Vfs
	}

	if pp, ok := activeVfs.(vfs.PtyProvider); ok && vfsHasRemotePTY(activeVfs) {
		if pty, exists := pf.RemotePtys[activeVfs]; exists {
			return pty
		}

		res, err := pp.OpenPty(pf.TermView.Width, pf.TermView.Height)
		if err == nil {
			pty := res.(terminal.PtyBackend)
			vtui.DebugLog("Created new remote term.PTY background session for VFS")
			pf.RemotePtys[activeVfs] = pty

			// Give the VFS one chance to install shell settings before
			// anyone else writes to the term.PTY. FISH+ against a Windows
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

					pf.PtyMutex.Lock()
					shouldProcess := (pf.getActivePTYUnsafe() == pty)
					pf.PtyMutex.Unlock()

					if shouldProcess {
						start := time.Now()
						pf.Parser.Process(buf[:n])
						elapsed := time.Since(start)
						if elapsed > 10*time.Millisecond {
							vtui.DebugLog("PTY_PROFILE(Remote): Parsed %d bytes in %v", n, elapsed)
						}
						pf.terminalRedraw.Request()
					}
				}
				pty.Close()
				pf.PtyMutex.Lock()
				delete(pf.RemotePtys, activeVfs)
				pf.PtyMutex.Unlock()
				// The session that switched these on is gone and cannot
				// switch them off.
				pf.TermView.ResetKeyboardProtocols()
			}()
			return pty
		}
	}
	return pf.Pty
}

// activeReplyPTY names the shell that device queries should be answered
// into. Unlike getActivePTY it never opens a connection: a query arriving
// for a host we have no session with is dropped rather than made to dial
// one, and it must stay callable from the parser's read goroutines.
func (pf *PanelsFrame) activeReplyPTY() terminal.PtyBackend {
	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	var activeVfs vfs.VFS
	if fsp := pf.GetActivePanel(); fsp != nil {
		activeVfs = fsp.Vfs
	}
	if activeVfs != nil && vfsHasRemotePTY(activeVfs) {
		if pty, ok := pf.RemotePtys[activeVfs]; ok && pty != nil {
			return pty
		}
	}
	return nil
}

func (pf *PanelsFrame) GetActivePTY() terminal.PtyBackend {
	pf.PtyMutex.Lock()
	defer pf.PtyMutex.Unlock()
	return pf.getActivePTYUnsafe()
}

// remotePTYInterruptTarget is a side-effect-free snapshot of the remote shell
// that currently owns Ctrl+C. In particular, discovering palette commands must
// not create a remote term.PTY merely to decide whether Interrupt is available.
type remotePTYInterruptTarget struct {
	Panel    *FileSystemPanel
	Pty      terminal.PtyBackend
	sequence string
}

func (target *remotePTYInterruptTarget) Matches(other *remotePTYInterruptTarget) bool {
	return target != nil && other != nil &&
		target.Panel == other.Panel && sameRemotePTYBackend(target.Pty, other.Pty) &&
		target.sequence == other.sequence
}

func sameRemotePTYBackend(left, right terminal.PtyBackend) bool {
	typeOfLeft := reflect.TypeOf(left)
	return typeOfLeft != nil && typeOfLeft == reflect.TypeOf(right) && typeOfLeft.Comparable() && left == right
}

func (pf *PanelsFrame) CurrentRemotePTYInterruptTarget() *remotePTYInterruptTarget {
	if pf == nil || pf.Closed || !pf.ShowPanels {
		return nil
	}
	fsp := pf.GetActivePanel()
	if fsp == nil || fsp.Vfs == nil || fsp.ProviderOpenTask != nil {
		return nil
	}
	integration, ok := fsp.Vfs.(vfs.PtyShellIntegration)
	if !ok {
		return nil
	}
	sequence := integration.PtyInterrupt()
	if len(sequence) == 0 {
		return nil
	}

	pf.PtyMutex.Lock()
	pty := pf.RemotePtys[fsp.Vfs]
	pf.PtyMutex.Unlock()
	if pty == nil {
		return nil
	}
	return &remotePTYInterruptTarget{
		Panel:    fsp,
		Pty:      pty,
		sequence: string(sequence),
	}
}

// interruptRemotePTY writes the integration-defined interrupt sequence only
// when the same panel, VFS and live term.PTY still own the command. expected is nil
// for the physical Ctrl+C path and a discovery snapshot for palette callbacks.
func (pf *PanelsFrame) interruptRemotePTY(expected *remotePTYInterruptTarget) bool {
	target := pf.CurrentRemotePTYInterruptTarget()
	if target == nil || (expected != nil && !target.Matches(expected)) {
		return false
	}
	_, _ = pf.WritePTY(target.Pty, []byte(target.sequence))
	return true
}

func (pf *PanelsFrame) GetTitle() string {
	if !pf.ShowPanels {
		if pf.Executing {
			return "Terminal (executing)"
		}
		if pf.TermView.Title != "" {
			return pf.TermView.Title
		}
		return "Terminal"
	}

	path := ""
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		path = fsp.PersistentPath()
		if fsp.ProviderOpenTask == nil {
			if tp, ok := fsp.Vfs.(vfs.TitleProvider); ok {
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
	if !pf.ShowPanels {
		title := "Terminal"
		if pf.Executing && pf.workspaceCommandTitle != "" {
			title = pf.workspaceCommandTitle
		}
		return title
	}

	panelPath := func(panel Panel) string {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || fsp.Vfs == nil {
			return "—"
		}
		path := fsp.PersistentPath()
		if path == "" {
			return "."
		}
		if fsp.ProviderOpenTask == nil && fsp.Vfs.IsAtRoot() {
			if provider, ok := fsp.Vfs.(vfs.TitleProvider); ok {
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

	return panelPath(pf.Panels[0]) + " ─ " + panelPath(pf.Panels[1])
}

func (pf *PanelsFrame) GetWorkspaceTabMarker() string {
	if pf.ShowPanels {
		return "P"
	}
	return "T"
}

// GetWorkspaceMenuInfo supplies the Screens popup with full panel paths. The
// compact tab title above intentionally uses only leaf directory names.
func (pf *PanelsFrame) GetWorkspaceMenuInfo() vtui.WorkspaceMenuInfo {
	if !pf.ShowPanels {
		title := "Terminal"
		if pf.Executing && pf.workspaceCommandTitle != "" {
			title = pf.workspaceCommandTitle
		}
		return vtui.WorkspaceMenuInfo{Icon: "T", Primary: title}
	}

	panelPath := func(panel Panel) string {
		fsp, ok := panel.(*FileSystemPanel)
		if !ok || fsp.Vfs == nil {
			return "—"
		}
		path := fsp.PersistentPath()
		if fsp.ProviderOpenTask == nil {
			if provider, ok := fsp.Vfs.(vfs.TitleProvider); ok {
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
		Primary:   panelPath(pf.Panels[0]),
		Secondary: panelPath(pf.Panels[1]),
	}
}

// shellSingleQuote wraps s so a POSIX shell passes it through as one literal
// word, closing and reopening the quote around each embedded quote.
func ShellSingleQuote(s string) string {
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
	if fsp, ok := pf.Panels[pf.ActiveIdx].(*FileSystemPanel); ok {
		if _, isOS := fsp.Vfs.(*vfs.OSVFS); isOS {
			dir = fsp.Vfs.GetPath()
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
				terminal.SetF4Clipboard(string(out))
				toast.Show("Command output copied to clipboard", 3*time.Second)
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
				vv, err := viewer.NewViewerView(context.Background(), v, tmpPath)
				if err == nil {
					vv.OnClose = func() { os.Remove(tmpPath) }
					ShowViewer(pf, vv, tmpPath)
				}
			} else {
				f, err := v.Open(context.Background(), tmpPath)
				if err == nil {
					ShowEditor(pf, v, tmpPath, f)
					if ev, _ := FindOpenedEditor(v, tmpPath); ev != nil {
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

// forkPanelsClone builds the copy of these panels that gets handed to
// AddScreen as a new workspace. Both Ctrl+N and the terminal-workspace action
// go through it, so the two agree on what a fork looks like.
func (pf *PanelsFrame) forkPanelsClone() *PanelsFrame {
	clone := pf.Clone()
	// Ctrl+N means "fork the panels", including when it is invoked
	// while the terminal is visible. Copying showPanels=false creates
	// two visually identical cmd.exe workspaces and makes a successful
	// switch look like it did nothing. Keep the running terminal in the
	// original workspace and expose the cloned panels in the new one.
	if !pf.ShowPanels {
		clone.ShowPanels = true
		if clone.LastW > 0 && clone.LastH > 0 {
			clone.ResizeConsole(clone.LastW, clone.LastH)
		} else {
			clone.UpdateMenuCheckmarks()
		}
	}
	return clone
}

func (pf *PanelsFrame) Clone() *PanelsFrame {
	clone := NewPanelsFrame()
	if pf.LastW > 0 && pf.LastH > 0 {
		clone.ResizeConsole(pf.LastW, pf.LastH)
	}

	for i, p := range pf.Panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			cloneFsp, cloneOk := clone.Panels[i].(*FileSystemPanel)
			if !cloneOk {
				clone.Panels[i] = NewFileSystemPanel(fsp.X1, fsp.Y1, fsp.X2-fsp.X1+1, fsp.Y2-fsp.Y1+1, fsp.Vfs.Clone())
				cloneFsp = clone.Panels[i].(*FileSystemPanel)
			}
			// Stop the initial load triggered by NewPanelsFrame to prevent races
			if cloneFsp.CancelLoad != nil {
				cloneFsp.CancelLoad()
			}
			// Important: reset isLoading so the clone doesn't think it's still
			// waiting for that cancelled initial load.
			cloneFsp.IsLoading = false
			cloneFsp.StopLoadingAnimation()

			_ = cloneFsp.Vfs.SetPath(fsp.Vfs.GetPath())
			cloneFsp.SetViewMode(fsp.ViewMode)
			cloneFsp.CursorIdx = fsp.CursorIdx
			cloneFsp.SortMode = fsp.SortMode
			cloneFsp.SortReverse = fsp.SortReverse

			cloneFsp.DirCache = make(map[dirCacheKey]DirCacheEntry)
			for k, v := range fsp.DirCache {
				cloneFsp.DirCache[k] = v
			}

			cloneFsp.SelectedItems = make(map[string]bool)
			for k, v := range fsp.SelectedItems {
				cloneFsp.SelectedItems[k] = v
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
			cloneFsp.Entries = make([]*FileEntry, len(fsp.Entries))
			for j, e := range fsp.Entries {
				cloneFsp.Entries[j] = &FileEntry{
					VFSItem:  e.VFSItem,
					Selected: e.Selected,
				}
			}
			cloneFsp.Refresh() // Populate table rows from copied entries

			cloneFsp.readDirectoryEx(true) // ВАЖНО: не удалять скопированные записи при первом чтении
			cloneFsp.Table.SelectPos = fsp.Table.SelectPos
			cloneFsp.Table.SelectCol = fsp.Table.SelectCol
			cloneFsp.Table.TopPos = fsp.Table.TopPos
		}
	}

	clone.ActiveIdx = pf.ActiveIdx
	clone.ShowKeyBar = pf.ShowKeyBar
	clone.ShowPanels = pf.ShowPanels
	clone.ShowLeftPanel = pf.ShowLeftPanel
	clone.ShowRightPanel = pf.ShowRightPanel
	clone.WidePanel = pf.WidePanel
	clone.Wide = pf.Wide
	clone.ShellMode = pf.ShellMode

	if pf.TermView != nil && clone.TermView != nil {
		clone.TermView.CloneStateFrom(pf.TermView)
	}
	if clone.LastW > 0 && clone.LastH > 0 {
		clone.ResizeConsole(clone.LastW, clone.LastH)
	} else {
		clone.UpdateMenuCheckmarks()
	}
	for i, p := range pf.Panels {
		if source, ok := p.(*FileSystemPanel); ok {
			if target, ok := clone.Panels[i].(*FileSystemPanel); ok {
				target.Table.SelectPos = source.Table.SelectPos
				target.Table.SelectCol = source.Table.SelectCol
				target.Table.TopPos = source.Table.TopPos
				target.CursorIdx = source.CursorIdx
			}
		}
	}
	return clone
}

func (pf *PanelsFrame) ShowPluginMenu() {
	items := plughost.PluginMenuItemsSnapshot()
	commands := plughost.PluginCommandsSnapshot(vfs.PluginCommandPanel, pf)
	if len(items) == 0 && len(commands) == 0 {
		vtui.ShowMessage(" Plugins ", "No plugins registered for F11 menu.", []string{"&Ok"})
		return
	}
	entries := buildPluginMenuEntries(items, commands)
	shortcutWidth := pluginMenuShortcutWidth(entries)
	menuItems := make([]vtui.MenuItem, 0, len(entries))
	for _, entry := range entries {
		menuItems = append(menuItems, vtui.MenuItem{
			Text: PluginMenuItemText(entry.Label, entry.Shortcut(), shortcutWidth),
		})
	}

	// Assigning or dropping a letter moves it between rows, so the whole menu
	// is rebuilt rather than the single row F4 was pressed on. The hot key
	// lives in the left-hand column only: MenuItem.Shortcut would print it a
	// second time on the right.
	refresh := func(menu *vtui.VMenu) {
		if menu == nil {
			return
		}
		RefreshPluginMenuEntries(entries)
		for i := range entries {
			if i >= len(menu.Items) {
				break
			}
			menu.Items[i].Text = PluginMenuItemText(entries[i].Label, entries[i].Shortcut(), shortcutWidth)
			menu.Items[i].Shortcut = ""
		}
		if vtui.FrameManager != nil {
			vtui.FrameManager.Redraw()
		}
	}

	pf.menuItemsWithKeyLabels(" Plugins ", menuItems, func(menu *vtui.VMenu, e *vtinput.InputEvent) bool {
		if !e.KeyDown {
			return false
		}
		idx := menu.SelectPos
		switch e.VirtualKeyCode {
		case vtinput.VK_F4:
			if idx >= 0 && idx < len(entries) {
				assignPluginHotkey(entries[idx].ActionName, entries[idx].Label, func() { refresh(menu) })
			}
			return true
		case vtinput.VK_DELETE:
			// Del always means "take this hot key back", which is why it can
			// never be assigned as one.
			if idx < 0 || idx >= len(entries) {
				return true
			}
			area, key := keymap.ConfiguredHotkeyBinding(keymap.GlobalHotkeysMgr, entries[idx].ActionName)
			if area == "" || key == "" {
				return true
			}
			question := pluginHotkeyDeleteQuestion(key, entries[idx].Label)
			buttons := []string{i18n.Msg("Plugins.HotkeyRemoveBtn"), i18n.Msg("Plugins.HotkeyKeepBtn")}
			vtui.ShowMessageOn(menu, i18n.Msg("Plugins.HotkeyRemoveTitle"), question, buttons).OnResult = func(choice int) {
				if choice != 0 || keymap.GlobalHotkeysMgr == nil {
					return
				}
				if !keymap.DeletePluginHotkey(keymap.GlobalHotkeysMgr, area, key) {
					return
				}
				refresh(menu)
			}
			return true
		}
		return false
	}, func(idx int) {
		switch {
		case idx >= 0 && idx < len(items):
			handler := items[idx].Handler
			vtui.FrameManager.PostTask(func() {
				handler(pf)
			})
		case idx >= len(items) && idx < len(items)+len(commands):
			commandID := commands[idx-len(items)].ID
			vtui.FrameManager.PostTask(func() {
				plughost.ExecutePluginCommand(vfs.PluginCommandPanel, commandID, pf)
			})
		}
	}, PluginMenuKeyLabels(pf))
}

func (pf *PanelsFrame) ShowDriveMenu(panelIdx int) {
	pf.showDriveMenuAt(panelIdx, pf.driveMenuDefaultPos(panelIdx))
}

// driveMenuDefaultPos returns the drive-menu row the cursor should land on
// when the menu opens. far2l positions the cursor on the drive the active
// panel currently shows; for f4 that means: if the panel is on a real
// filesystem drive that appears in the menu (e.g. a Windows drive letter),
// land on that drive, otherwise keep the historic default — the "Other panel"
// entry at row 0.
func (pf *PanelsFrame) driveMenuDefaultPos(panelIdx int) int {
	fsp, ok := pf.Panels[panelIdx].(*FileSystemPanel)
	if !ok {
		return 0
	}
	osVFS, ok := fsp.Vfs.(*vfs.OSVFS)
	if !ok {
		return 0
	}
	cur := osVFS.GetPath()
	for i, drv := range sysinfo.GetPlatformDrives() {
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
func driveMatchesPath(drv sysinfo.DriveEntry, cur string) bool {
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
	menu := vtui.NewVMenu(i18n.Msg("Drive.Title"))

	usedHotkeys := make(map[rune]bool)
	usedHotkeys['o'] = true // "Other panel"

	// 1. Other panel (focused by default)
	menu.AddItem(vtui.MenuItem{Text: i18n.Msg("Panel.Other"), UserData: func(fsp *FileSystemPanel) {
		otherFsp := pf.Panels[1-panelIdx].(*FileSystemPanel)
		fsp.cancelProviderOpen()
		if fsp.Vfs != nil {
			_ = fsp.Vfs.Close()
		}
		fsp.Vfs = otherFsp.Vfs.Clone()
		fsp.showCurrentVFSLoadingRows()
		fsp.ReadDirectory()
		pf.RefreshAll()
	}})

	// TempPanel is a native VFS panel, so it is available from the same
	// Alt+F1/Alt+F2 drive menu as far2l's plugin panels.
	menu.AddItem(vtui.MenuItem{Text: i18n.Msg("TempPanel.Drive"), UserData: func(fsp *FileSystemPanel) {
		pf.SwitchToVFS(fsp, NewTempPanelVFS(nil, GlobalTempPanelStore, 0))
	}})

	// 2. Fixed platform paths (Root, Home, physical disks). The metadata is
	// rendered at menu-open time, just like Far's ChangeDiskMenu, so labels,
	// filesystem types and free space reflect the current state. Collect all
	// rows first: the formatter needs the whole list to align its columns.
	driveMenuOptions := config.App.DriveMenuOptions
	platformDrives := make([]sysinfo.DriveEntry, 0)
	for _, drv := range sysinfo.GetPlatformDrives() {
		if !driveMenuPlatformItemVisible(drv, driveMenuOptions) {
			continue
		}
		platformDrives = append(platformDrives, drv)
	}
	platformNames := DriveMenuPlatformRowsText(func() []DriveMenuPlatformRow {
		rows := make([]DriveMenuPlatformRow, len(platformDrives))
		for i, drv := range platformDrives {
			rows[i] = driveMenuPlatformRowFor(drv, driveMenuOptions)
		}
		return rows
	}(), driveMenuOptions)
	for i, drv := range platformDrives {
		factory := drv.Factory
		name := platformNames[i]
		if runtime.GOOS != "windows" {
			if strings.HasPrefix(driveMenuNameWithoutMarker(drv.Name), "/") {
				name = "&" + name
				usedHotkeys['/'] = true
			} else if strings.HasPrefix(driveMenuNameWithoutMarker(drv.Name), "~") {
				name = "&" + name
				usedHotkeys['~'] = true
			}
		} else {
			cleanName := driveMenuNameWithoutMarker(drv.Name)
			if len(cleanName) >= 2 && cleanName[1] == ':' {
				name = "&" + name
				usedHotkeys[unicode.ToLower(rune(cleanName[0]))] = true
			}
		}

		menu.AddItem(vtui.MenuItem{Text: name, UserData: func(fsp *FileSystemPanel) {
			pf.SwitchToVFS(fsp, factory())
		}})
	}

	// 3. Folder bookmarks. far2l lists the assigned slots right here in
	// the same menu (panels/panel.cpp, AddBookmarkItems) with the slot
	// digit as the hotkey, so Alt+F1 followed by 6 lands on slot 6.
	// Unassigned slots are left out.
	bookmarkRows := map[int]int{} // menu row -> slot, for the keys below
	if driveMenuOptionEnabled(driveMenuOptions, config.DriveMenuShowBookmarks) {
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
					Text: fmt.Sprintf("&%d  %s", i, dialog.EscapeAmpersand(dialog.TruncPathLeft(path, 64))),
					UserData: func(fsp *FileSystemPanel) {
						pf.NavigateToBookmark(fsp, bookmark)
					},
				})
			}
		}
	}

	// 4. Plugins & custom drives
	drives := []sysinfo.DriveEntry(nil)
	if driveMenuOptionEnabled(driveMenuOptions, config.DriveMenuShowPlugins) {
		drives = sysinfo.DriveRegistrySnapshot()
		if driveMenuOptionEnabled(driveMenuOptions, config.DriveMenuSortPluginsByHotkey) {
			sort.SliceStable(drives, func(i, j int) bool {
				return strings.ToLower(driveMenuNameWithoutMarker(drives[i].Name)) <
					strings.ToLower(driveMenuNameWithoutMarker(drives[j].Name))
			})
		}
	}
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
				pf.SwitchToVFS(fsp, factory())
			}})
		}
	}

	// 5. Named folder links. These are deliberately separate from
	// bookmarks.ini: the latter is far2l's ten-slot RCtrl bookmark table,
	// while this list is the DiskMenuEditor-style, unbounded drive-menu list.
	driveBookmarkRows := map[int]int{} // menu row -> named bookmark index
	driveBookmarks := []DriveBookmark(nil)
	headerRow := -1
	if driveMenuOptionEnabled(driveMenuOptions, config.DriveMenuShowBookmarks) {
		var err error
		driveBookmarks, err = LoadDriveBookmarks(DriveBookmarksFilePath())
		if err != nil {
			vtui.DebugLog("DRIVE BOOKMARKS: load %q failed: %v", DriveBookmarksFilePath(), err)
			driveBookmarks = nil
		}
		menu.AddSeparator()
		headerRow = menu.GetItemCount()
		menu.AddItem(vtui.MenuItem{Text: i18n.Msg("Drive.Links"), Command: appcmd.CmDriveBookmarksHeader})
		for index, bookmark := range driveBookmarks {
			bookmark := bookmark
			driveBookmarkRows[menu.GetItemCount()] = index
			menu.AddItem(vtui.MenuItem{
				Text: DriveBookmarkMenuText(bookmark),
				UserData: func(fsp *FileSystemPanel) {
					pf.NavigateToBookmark(fsp, Bookmark{Path: bookmark.Path})
				},
			})
		}
		vtui.FrameManager.DisabledCommands.Disable(appcmd.CmDriveBookmarksHeader)
	}
	oldSelectable := menu.IsSelectable
	menu.IsSelectable = func(index int) bool {
		return index != headerRow && oldSelectable(index)
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
			driveBookmarkIndex, onDriveBookmark := driveBookmarkRows[pos]
			slot, onBookmark := bookmarkRows[pos]
			reopen := func() { pf.showDriveMenuAt(panelIdx, pos) }

			switch e.VirtualKeyCode {
			case vtinput.VK_F9:
				// Far uses F9 for the drive-menu options dialog. Consume it
				// here so the global F9 main-menu action never sees it.
				pf.openDriveMenuOptions(panelIdx, menu)
				return true
			case vtinput.VK_INSERT:
				// Ins adds a named drive-menu link from any row. The path
				// defaults to the panel directory, while the user chooses
				// the name and optional shortcut in the dialog.
				pf.openDriveBookmarkEditor(panelIdx, menu, driveBookmarks, -1, reopen)
				return true
			case vtinput.VK_F4:
				if onDriveBookmark {
					pf.openDriveBookmarkEditor(panelIdx, menu, driveBookmarks, driveBookmarkIndex, reopen)
					return true
				}
				if onBookmark {
					menu.Close()
					vtui.FrameManager.PostTask(func() { ShowBookmarksDialogAt(pf, slot, reopen) })
					return true
				}
			case vtinput.VK_DELETE:
				if onDriveBookmark {
					pf.deleteDriveBookmark(menu, driveBookmarks, driveBookmarkIndex, reopen)
					return true
				}
				if onBookmark {
					pf.clearBookmarkSlot(slot, menu, reopen)
					return true
				}
			}

			// Chords and non-Latin keys cannot be represented by VMenu's
			// ampersand accelerator. Match them against the same Far-style
			// spelling captured by the editor.
			key := keymap.EventToHotkeyString(e)
			for row := 0; row < len(menu.Items); row++ {
				index, ok := driveBookmarkRows[row]
				if !ok {
					continue
				}
				if index < len(driveBookmarks) && DriveBookmarkKeyMatches(driveBookmarks[index], key) {
					menu.SetSelectPos(row)
					menu.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
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
	if pf.LastW > 0 && w > pf.LastW-4 {
		w = pf.LastW - 4
	}
	if pf.LastH > 0 && h > pf.LastH-2 {
		h = pf.LastH - 2
	}

	y := (pf.LastH - h) / 2
	x := pf.LastW/4 - w/2
	if panelIdx != 0 {
		x = pf.LastW*3/4 - w/2
	}
	if x+w > pf.LastW {
		x = pf.LastW - w
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
		fsp, ok := pf.Panels[panelIdx].(*FileSystemPanel)
		if !ok {
			return
		}

		if action, ok := menu.Items[idx].UserData.(func(*FileSystemPanel)); ok {
			action(fsp)
		}
	}
	vtui.FrameManager.Push(&DriveMenuFrame{VMenu: menu, bottomHint: i18n.Msg("Drive.BottomHint")})
}

// clearBookmarkSlot empties one slot straight from the drive menu, which
// is what Del does there in far2l (panels/panel.cpp:594), then reopens
// the menu so the row is gone. The table is re-read first: another
// instance may have rewritten the file since the menu was built.
func (pf *PanelsFrame) clearBookmarkSlot(slot int, menu *vtui.VMenu, reopen func()) {
	File := BookmarksFilePath()
	set, err := LoadBookmarks(File)
	if err != nil {
		vtui.DebugLog("BOOKMARKS: load %q failed: %v", File, err)
		return
	}
	set.DeleteAtSlot(slot)
	if err := SaveBookmarks(File, set); err != nil {
		vtui.DebugLog("BOOKMARKS: save %q failed: %v", File, err)
		return
	}
	menu.Close()
	vtui.FrameManager.PostTask(reopen)
}

func (pf *PanelsFrame) SwitchToVFS(fsp *FileSystemPanel, newVFS vfs.VFS) {
	if newVFS != nil {
		fsp.cancelProviderOpen()
		oldVFS := fsp.Vfs
		if temp, ok := newVFS.(*TempPanelVFS); ok {
			// Keep the source VFS alive as TempPanel's parent. This is what
			// makes Ctrl+PgUp return to the exact panel directory instead of
			// reopening it, and is also important for remote VFSes whose Close
			// tears down their session.
			if temp.parent == nil && oldVFS != nil {
				temp.setParent(oldVFS, fsp.GetRawSelectedName())
			}
		}
		keepOldVFS := false
		if temp, ok := newVFS.(*TempPanelVFS); ok {
			keepOldVFS = temp.parent != nil && fileops.SameVFSInstance(temp.parent, oldVFS)
		}
		if oldVFS != nil && !keepOldVFS {
			oldVFS.Close()
			pf.PtyMutex.Lock()
			if pty, ok := pf.RemotePtys[oldVFS]; ok {
				pty.Close()
				delete(pf.RemotePtys, oldVFS)
			}
			pf.PtyMutex.Unlock()
		}
		fsp.ProviderEntryName = ""
		fsp.Vfs = newVFS
		fsp.showCurrentVFSLoadingRows()
		fsp.ReadDirectory()
		pf.RefreshAll()
	}
}
func (pf *PanelsFrame) NavigateToBookmark(fsp *FileSystemPanel, bookmark Bookmark) bool {
	return pf.NavigateToPath(fsp, ExpandPathEnv(bookmark.Path))
}

// syncPassivePanel opens the active panel's directory in the passive panel,
// like mc's Alt+I (#892). It goes through NavigateToPath so archives, remote
// and provider paths take the same route as folder history and bookmarks.
func (pf *PanelsFrame) SyncPassivePanel() bool {
	src := pf.GetActivePanel()
	dst := pf.GetInactivePanel()
	if src == nil || dst == nil || IsAIPanel(src) || IsAIPanel(dst) {
		return false
	}
	target := src.PersistentPath()
	if target == "" || SameFolderHistoryPath(target, dst.PersistentPath()) {
		return false
	}
	return pf.NavigateToPath(dst, target)
}

func (pf *PanelsFrame) NavigateToPath(fsp *FileSystemPanel, targetPath string) bool {
	if targetPath == "" {
		return false
	}
	// An explicit command/history navigation supersedes a provider mount that
	// has not installed its child VFS yet.
	providerOpenCanceled := fsp.ProviderOpenTask != nil
	fsp.cancelProviderOpen()

	// 1. Handle "cd .." at the root of a nested VFS (e.g. escaping an archive)
	if targetPath == ".." && fsp.Vfs.IsAtRoot() && fsp.Vfs.ParentVFS() != nil {
		parent := fsp.Vfs.ParentVFS()
		oldPath := fsp.Vfs.GetPath()
		parentSelection := ""
		if temp, ok := fsp.Vfs.(*TempPanelVFS); ok {
			parentSelection = temp.parentSelection
		}

		_ = fsp.Vfs.Close()
		pf.PtyMutex.Lock()
		if pty, ok := pf.RemotePtys[fsp.Vfs]; ok {
			pty.Close()
			delete(pf.RemotePtys, fsp.Vfs)
		}
		pf.PtyMutex.Unlock()

		fsp.Vfs = parent
		fsp.showCurrentVFSLoadingRows()
		if parentSelection != "" {
			fsp.PendingSelection = parentSelection
		} else if fsp.ProviderEntryName != "" {
			fsp.PendingSelection = fsp.ProviderEntryName
			fsp.ProviderEntryName = ""
		} else {
			fsp.PendingSelection = fsp.Vfs.Base(oldPath)
		}
		fsp.ReadDirectory()
		return true
	}

	// A provider-owned visual path (for example Account:\Folder) must be
	// restored before OS path probing. This keeps bookmarks and folder history
	// entirely user-facing while allowing the provider to translate the path to
	// its internal object identity in the asynchronous Open call.
	if fsp.Vfs.IsAbs(targetPath) {
		if err := fsp.SetKnownDirectoryPath(targetPath); err == nil {
			fsp.PendingSelection = ".."
			fsp.ReadDirectory()
			return true
		}
	}
	if provider := vfs.FindStandaloneProvider(context.Background(), fsp.Vfs, targetPath); provider != nil {
		sourceVFS := fsp.Vfs
		return fsp.openVFSAsync(
			targetPath,
			func(ctx context.Context) (vfs.VFS, error) {
				return provider.Open(ctx, sourceVFS, targetPath)
			},
			func(newVFS vfs.VFS) { pf.SwitchToVFS(fsp, newVFS) },
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
				fsp.PendingSelection = ".."
				pf.SwitchToVFS(fsp, newVfs)
				return true
			}
		}

		// If Stat failed with permission denied on a Windows junction (e.g. "Documents and Settings"),
		// still try SetPath — it may resolve the target via Readlink.
		if err != nil && runtime.GOOS == "windows" && os.IsPermission(err) {
			newVfs := vfs.NewOSVFS(targetPath)
			if err := newVfs.SetPath(targetPath); err == nil {
				fsp.PendingSelection = ".."
				pf.SwitchToVFS(fsp, newVfs)
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
						pf.SwitchToVFS(fsp, arcVFS)
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
								pf.SwitchToVFS(fsp, arcVFS)
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
		sourceVFS := fsp.Vfs
		return fsp.openVFSAsync(
			targetPath,
			func(ctx context.Context) (vfs.VFS, error) {
				return provider.OpenURI(ctx, sourceVFS, targetPath)
			},
			func(newVFS vfs.VFS) {
				pf.SwitchToVFS(fsp, newVFS)
			},
			func(err error) {
				vtui.ShowMessage(" Connection Error ", fmt.Sprintf("Failed to open %s:\n%v", targetPath, err), []string{"&Ok"})
			},
		)
	}
	if vfs.IsURIPath(targetPath) && !fsp.Vfs.IsAbs(targetPath) {
		// The URI is syntactically valid but its plugin is unavailable. Do not
		// let SetPath reinterpret it as a relative path in the current VFS.
		if providerOpenCanceled {
			fsp.IsLoading = false
			fsp.StopLoadingAnimation()
			fsp.updateTitle(nil)
			vtui.FrameManager.Redraw()
		}
		return false
	}

	// 4. Change path on the current VFS. Remote VFSes may take the optimistic,
	// no-I/O route here; ReadDirectory validates the target in the background
	// while a cached view can become interactive immediately.
	if err := fsp.SetKnownDirectoryPath(targetPath); err == nil {
		fsp.PendingSelection = ".."
		fsp.ReadDirectory()
		return true
	}

	if providerOpenCanceled {
		// No replacement VFS/read was started, so restore the manager panel's
		// loading state after superseding its pending provider transition.
		fsp.IsLoading = false
		fsp.StopLoadingAnimation()
		fsp.updateTitle(nil)
		vtui.FrameManager.Redraw()
	}
	return false
}

func SameFolderHistoryPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	uriA, aIsURI := fileops.NormalizedURIIdentity(a)
	uriB, bIsURI := fileops.NormalizedURIIdentity(b)
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
func FolderHistoryStep(history []string, current string, pos, direction int) (int, string, bool) {
	if len(history) == 0 || direction == 0 {
		return pos, "", false
	}
	if pos < 0 || pos >= len(history) || !SameFolderHistoryPath(history[pos], current) {
		pos = -1
		for i, path := range history {
			if SameFolderHistoryPath(path, current) {
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
	for i, panel := range pf.Panels {
		if panelFSP, ok := panel.(*FileSystemPanel); ok && panelFSP == fsp {
			return i
		}
	}
	return pf.ActiveIdx
}

// navigateAvailableFolderHistory tries history entries in storage-index order
// until one can actually be opened. A positive step walks towards older MRU
// entries; a negative step walks towards newer entries.
func (pf *PanelsFrame) NavigateAvailableFolderHistory(fsp *FileSystemPanel, history []string, startPos, step int) bool {
	if step == 0 {
		return false
	}
	for pos := startPos; pos >= 0 && pos < len(history); pos += step {
		path := history[pos]
		if fsp == nil || path == "" {
			continue
		}
		fsp.FastFindMode = false
		fsp.FastFindStr = ""
		fsp.SuppressNextFolderHistory(path)
		if !pf.NavigateToPath(fsp, path) {
			fsp.clearFolderHistorySuppression()
			continue
		}
		idx := pf.folderHistoryPanelIndex(fsp)
		if fsp.ProviderOpenTask != nil && SameFolderHistoryPath(fsp.ProviderOpenTarget, path) {
			historySnapshot := append([]string(nil), history...)
			pendingPos := pos
			fsp.providerOpenResult = func(success bool) bool {
				if success {
					if idx >= 0 && idx < len(pf.FolderHistoryPos) {
						pf.FolderHistoryPos[idx] = pendingPos
					}
					return false
				}
				fsp.clearFolderHistorySuppression()
				return pf.NavigateAvailableFolderHistory(fsp, historySnapshot, pendingPos+step, step)
			}
			return true
		}
		if idx >= 0 && idx < len(pf.FolderHistoryPos) {
			pf.FolderHistoryPos[idx] = pos
		}
		return true
	}
	return false
}

func (pf *PanelsFrame) MoveFolderHistory(fsp *FileSystemPanel, direction int) bool {
	if fsp == nil || vtui.GlobalHistoryProvider == nil {
		return false
	}
	history := vtui.GlobalHistoryProvider.LoadHistory("folders")
	idx := pf.folderHistoryPanelIndex(fsp)
	pos := -1
	if idx >= 0 && idx < len(pf.FolderHistoryPos) {
		pos = pf.FolderHistoryPos[idx]
	}
	// During a cache-first cross-provider restore, the panel already presents
	// providerOpenTarget while the source VFS remains installed until the
	// asynchronous mount succeeds. History must follow the presented/persisted
	// location; using the source VFS here skips that source entry on Alt+Left.
	current := fsp.PersistentPath()
	TargetPos, _, ok := FolderHistoryStep(history, current, pos, direction)
	if !ok {
		return false
	}
	step := -1
	if direction < 0 {
		step = 1
	}
	return pf.NavigateAvailableFolderHistory(fsp, history, TargetPos, step)
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

// parsePlainEditCommand recognizes the file-opening form of the edit:
// command. The edit:<< capture form is handled before this helper, but it is
// excluded here as well so the two forms cannot drift into one another.
func parsePlainEditCommand(trimmedCmd string) (path string, ok bool) {
	const prefix = "edit:"
	trimmedCmd = strings.TrimSpace(trimmedCmd)
	if len(trimmedCmd) <= len(prefix) || !strings.EqualFold(trimmedCmd[:len(prefix)], prefix) {
		return "", false
	}

	path = strings.TrimSpace(trimmedCmd[len(prefix):])
	if path == "" || strings.HasPrefix(path, "<<") {
		return "", false
	}
	return path, true
}

func ExpandPathEnv(s string) string {
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
