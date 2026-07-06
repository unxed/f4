package main

import (
	"context"
	"fmt"
	"github.com/unxed/f4/vfs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type DriveEntry struct {
	Name    string
	Factory func() vfs.VFS
}

var DriveRegistry []DriveEntry

func RegisterDrive(name string, factory func() vfs.VFS) {
	DriveRegistry = append(DriveRegistry, DriveEntry{Name: name, Factory: factory})
}

type HotkeyEntry struct {
	VK      uint16
	Mods    vtinput.ControlKeyState
	Handler func(app vfs.App)
}

var GlobalHotkeys []HotkeyEntry

func RegisterGlobalHotkey(vk uint16, mods vtinput.ControlKeyState, handler func(app vfs.App)) {
	GlobalHotkeys = append(GlobalHotkeys, HotkeyEntry{VK: vk, Mods: mods, Handler: handler})
}
func (pf *PanelsFrame) GetActivePanelVFS() vfs.VFS  { return pf.Active().(*FileSystemPanel).vfs }
func (pf *PanelsFrame) GetPassivePanelVFS() vfs.VFS { return pf.Passive().(*FileSystemPanel).vfs }
func (pf *PanelsFrame) GetSelectedNames() []string {
	return pf.Active().(*FileSystemPanel).GetSelectedNames()
}
func (pf *PanelsFrame) GetSelectedName() string {
	return pf.Active().(*FileSystemPanel).GetSelectedName()
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
	panels         [2]Panel
	activeIdx      int // 0 for left, 1 for right
	executing      bool
	returnToPanels bool

	menuBar *vtui.MenuBar
	cmdLine *CommandLine
	keyBar  *vtui.KeyBar

	showKeyBar     bool
	showPanels     bool
	showLeftPanel  bool
	showRightPanel bool
	lastW          int
	lastH          int

	// Integrated Terminal
	pty        PtyBackend
	remotePtys map[vfs.VFS]PtyBackend
	ptyMutex   sync.Mutex
	termView   *TerminalView
	parser     *AnsiParser

	lastAlt  bool
	lastBusy bool

	lastAutoRefresh time.Time
	lastKey         rune
	lastKeyEvent    time.Time

	lastPtyPath string
	lastPtyVFS  vfs.VFS
}

func (pf *PanelsFrame) Left() Panel    { return pf.panels[0] }
func (pf *PanelsFrame) Right() Panel   { return pf.panels[1] }
func (pf *PanelsFrame) Active() Panel  { return pf.panels[pf.activeIdx] }
func (pf *PanelsFrame) Passive() Panel { return pf.panels[1-pf.activeIdx] }

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 1}
	pf.SetHelp("Panels")
	pf.showKeyBar = true
	pf.showPanels = true
	pf.showLeftPanel = true
	pf.showRightPanel = true

	pf.menuBar = vtui.NewMenuBar(nil)
	pf.menuBar.SetOwner(pf)
	pf.menuBar.Items = []vtui.MenuBarItem{
		// Using Command routing (TV style) instead of hardcoded indices
		{Label: "&" + Msg("Menu.Left"), SubItems: []vtui.MenuItem{
			{Text: "&" + Msg("Menu.Left.Medium"), Command: CmLeftMedium},
			{Text: "&" + Msg("Menu.Left.Detailed"), Command: CmLeftDetailed},
			{Separator: true},
			{Text: "&" + Msg("Menu.SortName"), Shortcut: "Ctrl+F3", Command: CmLeftSortName},
			{Text: "&" + Msg("Menu.SortExt"), Shortcut: "Ctrl+F4", Command: CmLeftSortExt},
			{Text: "&" + Msg("Menu.SortTime"), Shortcut: "Ctrl+F5", Command: CmLeftSortTime},
			{Text: "&" + Msg("Menu.SortSize"), Shortcut: "Ctrl+F6", Command: CmLeftSortSize},
			{Text: "&" + Msg("Menu.SortUnsorted"), Shortcut: "Ctrl+F7", Command: CmLeftSortUnsorted},
			{Separator: true},
			{Text: "Bac&kground", Command: CmBackground},
			{Text: Msg("Menu.Exit"), Command: vtui.CmQuit},
		}},
		{Label: "&" + Msg("Menu.Files"), SubItems: []vtui.MenuItem{
			{Text: "&" + Msg("Menu.Files.View"), Shortcut: "F3", Command: CmView},
			{Text: "&" + Msg("Menu.Files.Edit"), Shortcut: "F4", Command: CmEdit},
			{Text: "&" + Msg("Menu.Files.Copy"), Shortcut: "F5", Command: CmCopy},
			{Text: "&" + Msg("Menu.Files.RenMov"), Shortcut: "F6", Command: CmMove},
			{Text: "&" + Msg("Menu.Files.MkDir"), Shortcut: "F7", Command: CmMkDir},
			{Text: "&" + Msg("Menu.Files.Delete"), Shortcut: "F8", Command: CmDelete},
		}},
		{Label: "&" + Msg("Menu.Commands"), SubItems: []vtui.MenuItem{
			{Text: "&" + Msg("Menu.Commands.FindFile"), Shortcut: "Alt+F7", Command: CmFindFile},
		}},
		// Uncomment to test crash logging
		//{Label: "&" + Msg("Menu.Options"), SubItems: []vtui.MenuItem{{Text: "Placeholder"}}},
		{Label: "&" + Msg("Menu.Options"), SubItems: []vtui.MenuItem{
			{Text: "&" + Msg("Menu.PanelSettings"), Command: CmPanelSettings},
			{Text: "&" + Msg("Menu.EditorSettings"), Command: CmEditorSettings},
			{Text: "&" + Msg("Menu.ConfirmationsSettings"), Command: CmConfirmationsSettings},
			{Separator: true},
			{Text: "&" + Msg("Menu.Options.Plugins"), Command: CmPlugins},
		}},
		{Label: "&" + Msg("Menu.Right"), SubItems: []vtui.MenuItem{
			{Text: "&" + Msg("Menu.Left.Medium"), Command: CmRightMedium},
			{Text: "&" + Msg("Menu.Left.Detailed"), Command: CmRightDetailed},
			{Separator: true},
			{Text: "&" + Msg("Menu.SortName"), Shortcut: "Ctrl+F3", Command: CmRightSortName},
			{Text: "&" + Msg("Menu.SortExt"), Shortcut: "Ctrl+F4", Command: CmRightSortExt},
			{Text: "&" + Msg("Menu.SortTime"), Shortcut: "Ctrl+F5", Command: CmRightSortTime},
			{Text: "&" + Msg("Menu.SortSize"), Shortcut: "Ctrl+F6", Command: CmRightSortSize},
			{Text: "&" + Msg("Menu.SortUnsorted"), Shortcut: "Ctrl+F7", Command: CmRightSortUnsorted},
		}},
	}
	// We no longer need pf.menuBar.OnCommand for routing!
	pf.cmdLine = NewCommandLine(Msg("Panels.Prompt"))
	pf.cmdLine.Edit.HistoryID = "cmdline"
	if vtui.GlobalHistoryProvider != nil {
		pf.cmdLine.Edit.History = vtui.GlobalHistoryProvider.LoadHistory("cmdline")
	}
	pf.keyBar = vtui.NewKeyBar()
	pf.keyBar.SetOwner(pf)

	pf.termView = NewTerminalView(80, 24)
	pf.termView.OnBusyChange = func(busy bool) {
		// Use PostTask to ensure state changes happen on the UI thread
		vtui.FrameManager.PostTask(func() {
			if busy {
				pf.executing = true
			} else {
				if pf.executing {
					pf.executing = false
					if pf.returnToPanels {
						pf.showPanels = true
						pf.returnToPanels = false
						pf.RefreshAll()
						vtui.FrameManager.Redraw()
					}
				}
			}
		})
	}
	// Parser will be fully initialized in initPTY once pty is ready
	pf.initPTY()
	pf.termView.pty = pf.pty

	return pf
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

func (pf *PanelsFrame) updateMenuCheckmarks() {
	if pf.panels[0] == nil || pf.panels[1] == nil || pf.menuBar == nil || len(pf.menuBar.Items) < 5 {
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

	pf.menuBar.Items[0].SubItems[0].Text = getMenuText(lMode, ViewModeMedium, "&"+Msg("Menu.Left.Medium"))
	pf.menuBar.Items[0].SubItems[1].Text = getMenuText(lMode, ViewModeDetailed, "&"+Msg("Menu.Left.Detailed"))
	pf.menuBar.Items[0].SubItems[3].Text = getSortMenuText(lSort, SortName, "&"+Msg("Menu.SortName"))
	pf.menuBar.Items[0].SubItems[4].Text = getSortMenuText(lSort, SortExt, "&"+Msg("Menu.SortExt"))
	pf.menuBar.Items[0].SubItems[5].Text = getSortMenuText(lSort, SortTime, "&"+Msg("Menu.SortTime"))
	pf.menuBar.Items[0].SubItems[6].Text = getSortMenuText(lSort, SortSize, "&"+Msg("Menu.SortSize"))
	pf.menuBar.Items[0].SubItems[7].Text = getSortMenuText(lSort, SortUnsorted, "&"+Msg("Menu.SortUnsorted"))

	pf.menuBar.Items[4].SubItems[0].Text = getMenuText(rMode, ViewModeMedium, "&"+Msg("Menu.Left.Medium"))
	pf.menuBar.Items[4].SubItems[1].Text = getMenuText(rMode, ViewModeDetailed, "&"+Msg("Menu.Left.Detailed"))
	pf.menuBar.Items[4].SubItems[3].Text = getSortMenuText(rSort, SortName, "&"+Msg("Menu.SortName"))
	pf.menuBar.Items[4].SubItems[4].Text = getSortMenuText(rSort, SortExt, "&"+Msg("Menu.SortExt"))
	pf.menuBar.Items[4].SubItems[5].Text = getSortMenuText(rSort, SortTime, "&"+Msg("Menu.SortTime"))
	pf.menuBar.Items[4].SubItems[6].Text = getSortMenuText(rSort, SortSize, "&"+Msg("Menu.SortSize"))
	pf.menuBar.Items[4].SubItems[7].Text = getSortMenuText(rSort, SortUnsorted, "&"+Msg("Menu.SortUnsorted"))
}

func (pf *PanelsFrame) buildPrompt() []vtui.CharInfo {
	var path string
	var vfsTitle string
	if fsp, ok := pf.Active().(*FileSystemPanel); ok {
		path = fsp.vfs.GetPath()
		if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
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

	host, _ := os.Hostname()
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

	maxPromptLen := pf.lastW / 2
	if maxPromptLen < 30 {
		maxPromptLen = pf.lastW - 15
	}
	if maxPromptLen < 15 {
		maxPromptLen = 15
	}

	maxPathLen := maxPromptLen - runewidth.StringWidth(userHostStr) - runewidth.StringWidth(sepStr) - runewidth.StringWidth(suffixStr)
	if maxPathLen < 10 {
		maxPathLen = 10
	}

	if runewidth.StringWidth(displayPath) > maxPathLen {
		displayPath = vtui.TruncateMiddle(displayPath, maxPathLen)
	}

	baseAttr := vtui.Palette[ColCommandLineUserScreen]
	// Use colors as close as possible to classic bash, while keeping the base background
	greenAttr := vtui.SetRGBFore(baseAttr, 0x8AE234) // Bright green
	blueAttr := vtui.SetRGBFore(baseAttr, 0x729FCF)  // Bright blue
	defAttr := vtui.SetRGBFore(baseAttr, 0xFFFFFF)   // White

	var prompt []vtui.CharInfo
	prompt = append(prompt, vtui.StringToCharInfo(userHostStr, greenAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(sepStr, defAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(displayPath, blueAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(suffixStr, defAttr)...)

	return prompt
}

func (pf *PanelsFrame) initPTY() {
	// Always initialize the parser to prevent nil dereference
	pf.parser = NewAnsiParser(pf.termView, nil)

	go func() {
		pf.ptyMutex.Lock()
		p := pf.pty
		pf.ptyMutex.Unlock()

		if p == nil {
			var err error
			p, err = NewPTY()
			if err != nil {
				vtui.DebugLog("PTY: Failed to allocate local PTY: %v", err)
				return
			}

			if runtime.GOOS == "windows" {
				os.Setenv("PROMPT", "$E]133;D$E\\$P$G")
			}

			shell := GetSystemShell()
			if err := p.Run(shell); err != nil {
				vtui.DebugLog("PTY: Failed to run shell: %v", err)
				p.Close()
				return
			}

			pf.ptyMutex.Lock()
			pf.pty = p
			pf.parser.pty = p
			pf.termView.pty = p
			pf.ptyMutex.Unlock()

			vtui.FrameManager.PostTask(func() {
				pf.ResizeConsole(pf.lastW, pf.lastH)
				pf.RefreshAll()
				vtui.FrameManager.Redraw()
			})
		}

		// Local PTY has its own dedicated read loop.
		buf := make([]byte, 32768)
		for {
			n, err := p.Read(buf)
			if err != nil {
				vtui.DebugLog("PTY: Local read loop exited: %v", err)
				return
			}

			pf.ptyMutex.Lock()
			shouldProcess := (pf.getActivePTYUnsafe() == p)
			pf.ptyMutex.Unlock()

			if shouldProcess {
				pf.parser.Process(buf[:n])
				vtui.FrameManager.Redraw()
			}
		}
	}()
}

func (pf *PanelsFrame) Close() {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()

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

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	pf.lastW, pf.lastH = w, h
	pf.SetPosition(0, 0, w-1, h-1) // Update hit-box for FrameManager hit-testing
	pf.menuBar.SetPosition(0, 0, w-1, 0)

	contentY1 := 0

	// 1. Terminal Area: Fills everything except KeyBar
	termY2 := h - 1
	// KeyBar only takes space if it's actually visible (not in AltScreen and not busy)
	if pf.showKeyBar && !pf.termView.UseAltScreen && !pf.isPtyBusy() {
		termY2 = h - 2
	}
	termH := termY2 - contentY1 + 1
	if termH < 0 {
		termH = 0
	}

	if pf.pty != nil {
		pf.ptyMutex.Lock()
		pf.pty.SetSize(w, termH)
		for _, remotePty := range pf.remotePtys {
			remotePty.SetSize(w, termH)
		}
		pf.ptyMutex.Unlock()

		pf.termView.SetPosition(0, contentY1, w-1, termY2)
		pf.termView.Resize(w, termH)
	}

	// 2. Panel Area: Leaves one additional line for the f4 CommandLine
	panelY2 := h - 2
	if pf.showKeyBar {
		panelY2 = h - 3
	}
	panelH := panelY2 - contentY1 + 1
	if panelH < 0 {
		panelH = 0
	}

	leftW := w / 2
	rightW := w - leftW

	if pf.panels[0] == nil {
		pf.panels[0] = NewFileSystemPanel(0, contentY1, leftW, panelH, vfs.NewOSVFS("."))
		pf.panels[1] = NewFileSystemPanel(leftW, contentY1, rightW, panelH, vfs.NewOSVFS("."))
	} else {
		pf.panels[0].SetPosition(0, contentY1, leftW-1, panelY2)
		pf.panels[1].SetPosition(leftW, contentY1, w-1, panelY2)

		for i, p := range pf.panels {
			width := leftW
			if i == 1 {
				width = rightW
			}
			if fsp, ok := p.(*FileSystemPanel); ok {
				fsp.Resize(width, panelH)
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
func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	isBusy := pf.isPtyBusy()

	// 1. Dynamic Layout Adjustment
	if pf.termView.UseAltScreen != pf.lastAlt || isBusy != pf.lastBusy {
		pf.lastAlt = pf.termView.UseAltScreen
		pf.lastBusy = isBusy
		pf.ResizeConsole(pf.lastW, pf.lastH)
	}

	if !isBusy {
		if fsp := pf.getActivePanel(); fsp != nil {
			currentPath := fsp.vfs.GetPath()
			if currentPath != pf.lastPtyPath || fsp.vfs != pf.lastPtyVFS {
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

	if pf.showPanels {
		// Если одна из панелей скрыта — показываем терминал под видимой панелью
		if !pf.showLeftPanel || !pf.showRightPanel {
			pf.termView.SetVisible(true)
			pf.termView.Show(scr)
		} else {
			pf.termView.SetVisible(false)
		}
		if pf.showLeftPanel {
			pf.panels[0].SetFocus(pf.activeIdx == 0)
			pf.panels[0].Show(scr)
		}
		if pf.showRightPanel {
			pf.panels[1].SetFocus(pf.activeIdx == 1)
			pf.panels[1].Show(scr)
		}
	} else {
		pf.termView.SetVisible(true)
		pf.termView.Show(scr)
	}

	// Command line logic depends on terminal state and editor visibility
	topType := vtui.FrameManager.GetTopFrameType()
	isFastFind := false
	if fsp := pf.getActivePanel(); fsp != nil && fsp.fastFindMode {
		isFastFind = true
	}

	if (!pf.showPanels && (pf.termView.UseAltScreen || isBusy)) || topType == vtui.TypeUser+2 {
		pf.cmdLine.SetVisible(false)
	} else {
		pf.cmdLine.SetVisible(true)
		pf.cmdLine.Edit.HideCursor = isFastFind
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
		if pf.showKeyBar && !pf.termView.UseAltScreen && (pf.showPanels || !isBusy) {
			vtui.FrameManager.KeyBar = pf.keyBar
		} else {
			vtui.FrameManager.KeyBar = nil
		}
	}

	// Macro Recording Indicator
	if MacroMgr != nil && MacroMgr.Recording {
		scr.Write(0, 0, vtui.StringToCharInfo(" R ", vtui.SetRGBBoth(0, 0xFFFFFF, 0xFF0000)))
	}
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

	// Check global hotkeys (ignoring Lock and Enhanced keys)
	for _, hk := range GlobalHotkeys {
		hkCtrl := (hk.Mods & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		hkAlt := (hk.Mods & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		hkShift := (hk.Mods & vtinput.ShiftPressed) != 0

		if e.VirtualKeyCode == hk.VK && e.KeyDown && ctrl == hkCtrl && alt == hkAlt && shift == hkShift {
			hk.Handler(pf)
			return true
		}
	}

	// Panel Controller interception (allows plugins to override default keys)
	if pf.showPanels {
		fsp := pf.getActivePanel()
		if fsp != nil {
			if pc, ok := fsp.vfs.(PanelController); ok {
				if pc.ProcessPanelKey(pf, e) {
					return true
				}
			}
		}
	}

	// Arkanoid easter egg: Ctrl+Alt+A
	if e.VirtualKeyCode == 'A' && alt && ctrl && e.KeyDown {
		for i, s := range vtui.FrameManager.Screens {
			for _, f := range s.Frames {
				if f.GetTitle() == "Arkanoid" {
					vtui.FrameManager.SwitchScreen(i)
					return true
				}
			}
		}
		// Запускаем без десктопа, чтобы воркспейс был прозрачным
		vtui.FrameManager.AddScreenHeadless(NewArkanoidFrame())
		return true
	}
	// Crash test hotkey: Ctrl+Alt+C
	if e.VirtualKeyCode == vtinput.VK_C && alt && ctrl && e.KeyDown {
		panic("Manual safe crash triggered by user (Ctrl+Alt+C) for testing!")
	}
	if e.Type == vtinput.FocusEventType {
		pf.SetFocus(e.SetFocus)
		// Reload macros from disk when regaining focus to share them across instances
		if e.SetFocus && MacroMgr != nil {
			MacroMgr.Load()
		}
		// Propagate focus to command line so its cursor state stays in sync
		pf.cmdLine.SetFocus(e.SetFocus)
		pf.termView.SetFocus(e.SetFocus)
		return true
	}

	// Handle bracketed paste for terminal apps
	if e.Type == vtinput.PasteEventType {
		if !pf.showPanels && pf.termView.BracketedPasteMode && pf.pty != nil {
			if e.PasteStart {
				pf.pty.Write([]byte("\x1b[200~"))
			} else {
				pf.pty.Write([]byte("\x1b[201~"))
			}
			return true
		}
		// Editor view checks paste events internally, so we let it fall through if panels are shown
	}

	// Intercept F3/F4 for terminal log before raw input mode
	if !pf.showPanels && e.KeyDown {
		isCtrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		isShift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
		isAlt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
		isAppRunning := pf.termView.UseAltScreen || pf.isPtyBusy()

		if !isAlt {
			if e.VirtualKeyCode == vtinput.VK_F3 {
				if isCtrl && isShift {
					actionViewTerminalLog(pf)
					return true
				} else if !isCtrl && !isShift && !isAppRunning {
					actionViewTerminalLog(pf)
					return true
				}
			}
			if e.VirtualKeyCode == vtinput.VK_F4 {
				if isCtrl && isShift {
					actionEditTerminalLog(pf)
					return true
				} else if !isCtrl && !isShift && !isAppRunning {
					actionEditTerminalLog(pf)
					return true
				}
			}
		}
	}

	// Ctrl+O toggles panels visibility (must intercept before raw input mode)
	if e.VirtualKeyCode == vtinput.VK_O && ctrl && !alt && !shift && e.KeyDown {
		pf.showPanels = !pf.showPanels
		pf.showLeftPanel = pf.showPanels
		pf.showRightPanel = pf.showPanels
		vtui.FrameManager.HardRefresh()
		if pf.showPanels {
			pf.RefreshAll()
		}
		return true
	}

	// Ctrl+F1 toggles left panel, Ctrl+F2 toggles right panel
	if e.VirtualKeyCode == vtinput.VK_F1 && ctrl && !alt && !shift && e.KeyDown {
		pf.showLeftPanel = !pf.showLeftPanel
		pf.showPanels = pf.showLeftPanel || pf.showRightPanel
		vtui.FrameManager.HardRefresh()
		if pf.showPanels {
			pf.RefreshAll()
		}
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_F2 && ctrl && !alt && !shift && e.KeyDown {
		pf.showRightPanel = !pf.showRightPanel
		pf.showPanels = pf.showLeftPanel || pf.showRightPanel
		vtui.FrameManager.HardRefresh()
		if pf.showPanels {
			pf.RefreshAll()
		}
		return true
	}

	// Raw input mode for interactive terminal apps or active shell commands
	if !pf.showPanels && (pf.termView.UseAltScreen || pf.isPtyBusy()) {
		isCtrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
		isShift := (e.ControlKeyState & vtinput.ShiftPressed) != 0

		if e.VirtualKeyCode == vtinput.VK_TAB && isCtrl {
			if isShift {
				return false
			}
			isAdvanced := pf.termView.Win32InputMode || pf.termView.KittyFlags != 0
			if !isAdvanced {
				return false
			}
		}

		if e.KeyDown || pf.termView.Win32InputMode || pf.termView.KittyFlags != 0 {
			active := pf.getActivePTY()
			if active != nil {
				if seq := TranslateInput(e, pf.termView.Win32InputMode, pf.termView.KittyFlags, pf.termView.ApplicationCursorKeys); seq != "" {
					active.Write([]byte(seq))
				}
			}
		}
		return true
	}

	// Drive menus (Only if terminal is NOT busy)

	if e.VirtualKeyCode == vtinput.VK_F1 && alt && !ctrl && !shift && e.KeyDown {
		pf.showDriveMenu(0)
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_F2 && alt && !ctrl && !shift && e.KeyDown {
		pf.showDriveMenu(1)
		return true
	}

	// F2: user menu (FarMenu.ini local → near binary → main_menu.ini).
	if e.VirtualKeyCode == vtinput.VK_F2 && !alt && !ctrl && !shift && e.KeyDown {
		ShowUserMenu(pf)
		return true
	}

	// Ctrl+A: Attributes
	if e.VirtualKeyCode == vtinput.VK_A && ctrl && !alt && !shift && e.KeyDown {
		actionFileAttributes(pf)
		return true
	}

	// Alt+F5: Dummy Long Operation for debugging
	if e.VirtualKeyCode == vtinput.VK_F5 && alt && !ctrl && e.KeyDown {
		pf.showDummyOpDialog()
		return true
	}

	// Alt+F7: Find file
	if e.VirtualKeyCode == vtinput.VK_F7 && alt && !ctrl && !shift && e.KeyDown {
		return vtui.FrameManager.EmitCommand(CmFindFile, nil)
	}

	// Alt+F8: Command History
	if e.VirtualKeyCode == vtinput.VK_F8 && alt && !ctrl && !shift && e.KeyDown {
		if len(pf.cmdLine.Edit.History) > 0 {
			pf.cmdLine.Edit.OpenHistory()
		} else {
			vtui.ShowMessage(" History ", "Command history is empty.", []string{"&Ok"})
		}
		return true
	}

	// Alt+F12: Folders History
	if e.VirtualKeyCode == vtinput.VK_F12 && alt && !ctrl && !shift && e.KeyDown {
		actionFoldersHistory(pf)
		return true
	}

	// F11: Plugin Menu
	if e.VirtualKeyCode == vtinput.VK_F11 && !alt && !ctrl && !shift && e.KeyDown {
		pf.showPluginMenu()
		return true
	}

	if !e.KeyDown {
		return false
	}

	// Ctrl+F or Ctrl+Ins on panels: copy to clipboard
	if pf.showPanels && pf.cmdLine.IsEmpty() && ctrl && !alt && !shift && e.KeyDown {
		if fsp := pf.getActivePanel(); fsp != nil {
			idx := fsp.GetCursorIndex()
			if idx >= 0 && idx < len(fsp.entries) {
				entry := fsp.entries[idx]
				if entry.Name != ".." {
					if e.VirtualKeyCode == 'F' {
						fullPath := fsp.vfs.Join(fsp.vfs.GetPath(), entry.Name)
						vtui.SetClipboard(fullPath)
						return true
					}
					if e.VirtualKeyCode == vtinput.VK_INSERT {
						vtui.SetClipboard(entry.Name)
						return true
					}
				}
			}
		}
	}
	// Standard keys for file operations
	switch e.VirtualKeyCode {
	case vtinput.VK_F1:
		return vtui.FrameManager.EmitCommand(vtui.CmHelp, nil)
	case vtinput.VK_F3:
		if ctrl {
			return vtui.FrameManager.EmitCommand(CmSortName, nil)
		}
		return vtui.FrameManager.EmitCommand(CmView, nil)
	case vtinput.VK_F4:
		if ctrl {
			return vtui.FrameManager.EmitCommand(CmSortExt, nil)
		}
		if shift {
			return vtui.FrameManager.EmitCommand(CmNew, nil)
		}
		return vtui.FrameManager.EmitCommand(CmEdit, nil)
	case vtinput.VK_F5:
		if ctrl {
			return vtui.FrameManager.EmitCommand(CmSortTime, nil)
		}
		return vtui.FrameManager.EmitCommand(CmCopy, nil)
	case vtinput.VK_F6:
		if shift {
			return vtui.FrameManager.EmitCommand(CmRename, nil)
		}
		if ctrl {
			return vtui.FrameManager.EmitCommand(CmSortSize, nil)
		}
		return vtui.FrameManager.EmitCommand(CmMove, nil)
	case vtinput.VK_F7:
		if ctrl {
			return vtui.FrameManager.EmitCommand(CmSortUnsorted, nil)
		}
		return vtui.FrameManager.EmitCommand(CmMkDir, nil)
	case vtinput.VK_F8:
		return vtui.FrameManager.EmitCommand(CmDelete, nil)
	case vtinput.VK_F10:
		return vtui.FrameManager.EmitCommand(vtui.CmQuit, nil)
	case vtinput.VK_F9:
		pos := 0 // Left
		if pf.activeIdx == 1 {
			pos = 4 // Right
		}
		pf.menuBar.Active = true
		pf.menuBar.ActivateSubMenu(pos)
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && !pf.cmdLine.IsEmpty() {
		pf.cmdLine.Clear()
		pf.cmdLine.Edit.HistoryPos = -1
		return true
	}
	// Vim-like hotkeys
	if AppConfig.VimHotkeys && pf.showPanels && !alt && !ctrl && !shift && e.Char != 0 && pf.cmdLine.Edit.HistoryPos == -1 {
		isFastFind := false
		if fsp := pf.getActivePanel(); fsp != nil {
			isFastFind = fsp.fastFindMode
		}

		// If fast find is active, Vim hotkeys must be ignored to allow searching by 'j', 'k', etc.
		if !isFastFind {
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

	// Ctrl+Enter inserts selected file name
	if e.VirtualKeyCode == vtinput.VK_RETURN && ctrl {
		name := pf.Active().GetSelectedName()
		if name != "" {
			// Escape spaces and special characters for shell commands
			if strings.ContainsAny(name, " &|;<>()$`\\\"'") {
				if runtime.GOOS == "windows" {
					if !strings.HasPrefix(name, "\"") {
						name = "\"" + name + "\""
					}
				} else {
					if !strings.HasPrefix(name, "'") {
						name = "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
					}
				}
			}
			txt := pf.cmdLine.Edit.GetText()
			// Add space if the line is not empty and doesn't end with a space.
			if len(txt) > 0 && txt[len(txt)-1] != ' ' {
				pf.cmdLine.InsertString(" ")
			}
			pf.cmdLine.InsertString(name)
		}
		return true
	}

	// Ctrl+R forces panels refresh
	if e.VirtualKeyCode == vtinput.VK_R && ctrl && !alt && !shift {
		pf.RefreshAll()
		return true
	}

	// Ctrl+U swaps panels
	if e.VirtualKeyCode == vtinput.VK_U && ctrl {
		return vtui.FrameManager.EmitCommand(CmSwapPanels, nil)
	}

	// Enter handling
	if e.VirtualKeyCode == vtinput.VK_RETURN {
		if !pf.cmdLine.IsEmpty() {
			cmd := pf.cmdLine.Edit.GetText()
			pf.cmdLine.Edit.AddHistory(cmd)
			pf.cmdLine.Edit.HistoryPos = -1

			trimmedCmd := strings.TrimSpace(cmd)
			lowerCmd := strings.ToLower(trimmedCmd)
			isDirChange := false
			targetPath := ""

			// Intercept drive letter changes (e.g., "C:", "D:\") on Windows
			if runtime.GOOS == "windows" && len(trimmedCmd) >= 2 && len(trimmedCmd) <= 3 && trimmedCmd[1] == ':' {
				if lowerCmd[0] >= 'a' && lowerCmd[0] <= 'z' {
					isDirChange = true
					targetPath = trimmedCmd
					if len(trimmedCmd) == 2 {
						targetPath += string(os.PathSeparator)
					}
				}
				// Intercept standard 'cd' commands
			} else if strings.HasPrefix(lowerCmd, "cd ") || strings.HasPrefix(lowerCmd, "chdir ") || (runtime.GOOS == "windows" && strings.HasPrefix(lowerCmd, "cd /d ")) {
				prefixLen := 3
				if strings.HasPrefix(lowerCmd, "cd /d ") {
					prefixLen = 6
				} else if strings.HasPrefix(lowerCmd, "chdir ") {
					prefixLen = 6
				}
				isDirChange = true
				targetPath = strings.TrimSpace(trimmedCmd[prefixLen:])
				// Remove quotes if user typed: cd "C:\My Folder" or cd '/tmp/a b'
				if len(targetPath) >= 2 && targetPath[0] == '\'' && targetPath[len(targetPath)-1] == '\'' {
					targetPath = targetPath[1 : len(targetPath)-1]
					targetPath = strings.ReplaceAll(targetPath, "'\\''", "'")
				} else if len(targetPath) >= 2 && targetPath[0] == '"' && targetPath[len(targetPath)-1] == '"' {
					targetPath = targetPath[1 : len(targetPath)-1]
				}
			} else if lowerCmd == "cd.." || lowerCmd == "cd .." {
				isDirChange = true
				targetPath = ".."
			} else if lowerCmd == "cd\\" || lowerCmd == "cd/" {
				isDirChange = true
				targetPath = string(os.PathSeparator)
			} else if lowerCmd == "exit" {
				pf.cmdLine.Clear()
				return true
			}

			// Apply to panel first
			if isDirChange {
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					targetPath = expandPathEnv(targetPath)
					if pf.NavigateToPath(fsp, targetPath) {
						pf.cmdLine.Clear()

						// Sync the background PTY synchronously to satisfy tests and provide immediate state
						if pf.syncPTYDirectory(fsp.vfs.GetPath(), fsp.vfs) {
							pf.lastPtyPath = fsp.vfs.GetPath()
							pf.lastPtyVFS = fsp.vfs
						}

						return true
					}
				}
			}

			// Fallthrough for regular commands or if directory change failed (to show error in terminal)
			activePty := pf.getActivePTY()
			if activePty != nil {
				var path string
				isWindowsShell := runtime.GOOS == "windows"
				if fsp, ok := pf.panels[pf.activeIdx].(*FileSystemPanel); ok {
					if _, isOS := fsp.vfs.(*vfs.OSVFS); isOS {
						path = fsp.vfs.GetPath()
					} else if _, isPty := fsp.vfs.(vfs.PtyProvider); isPty {
						path = fsp.vfs.GetPath()
						isWindowsShell = false
					}
				}

				var fullWireCmd string
				isBackground := false
				if !isWindowsShell {
					isBackground = strings.HasSuffix(strings.TrimSpace(cmd), "&")
				}

				if isWindowsShell {
					// Use a combined command for reliable excision in AnsiParser: cd /d "path" & command
					if path != "" {
						fullWireCmd = fmt.Sprintf("cd /d \"%s\" & %s\r", path, cmd)
					} else {
						fullWireCmd = fmt.Sprintf("%s\r", cmd)
					}
					pf.executing = true
					pf.returnToPanels = pf.showPanels
				} else {
					// Unix
					if isBackground {
						if path != "" {
							sqPath := strings.ReplaceAll(path, "'", "'\\''")
							fullWireCmd = fmt.Sprintf("set +H; cd '%s' && %s\r", sqPath, cmd)
						} else {
							fullWireCmd = cmd + "\r"
						}
					} else {
						// Managed foreground command
						if path != "" {
							sqPath := strings.ReplaceAll(path, "'", "'\\''")
							fullWireCmd = fmt.Sprintf("set +H; cd '%s' && { printf \"\\033]133;C\\007\"; %s ; printf \"\\033]133;D\\007\"; }\r", sqPath, cmd)
						} else {
							fullWireCmd = fmt.Sprintf("{ printf \"\\033]133;C\\007\"; %s ; printf \"\\033]133;D\\007\"; }\r", cmd)
						}
						pf.executing = true
						pf.returnToPanels = pf.showPanels
					}
				}

				if !isWindowsShell {
					pf.termView.PrintCleanCommand(cmd)
					if !isBackground {
						pf.termView.SetMuted(true)
					}
				}
				activePty.Write([]byte(fullWireCmd))
			}

			pf.cmdLine.Clear()
			pf.showPanels = false
			return true
		} else if !pf.showPanels {
			activePty := pf.getActivePTY()
			if activePty != nil {
				activePty.Write([]byte("\r"))
			}
			return true
		} else {

			// CommandLine is empty, panels are visible.

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

	// Selection by mask (+, -, *) logic
	// Intercepted only if fastFind is not active
	if pf.showPanels && !alt && !ctrl {
		isFastFind := false
		if fsp := pf.getActivePanel(); fsp != nil && fsp.fastFindMode {
			isFastFind = true
		}

		if !isFastFind {
			isSelectKey := false
			var selectChar rune

			switch e.VirtualKeyCode {
			case vtinput.VK_ADD:
				isSelectKey = true
				selectChar = '+'
			case vtinput.VK_SUBTRACT:
				isSelectKey = true
				selectChar = '-'
			case vtinput.VK_MULTIPLY:
				isSelectKey = true
				selectChar = '*'
			default:
				if pf.cmdLine.IsEmpty() {
					if e.Char == '+' || e.Char == '-' || e.Char == '*' {
						isSelectKey = true
						selectChar = e.Char
					}
				}
			}

			if isSelectKey {
				fsp := pf.getActivePanel()
				if fsp != nil {
					switch selectChar {
					case '*':
						fsp.InvertSelection()
					case '+':
						vtui.InputBox(Msg("Select.Title"), Msg("Select.Mask"), "*", func(mask string) {
							fsp.ApplyMaskSelection(mask, true)
						})
					case '-':
						vtui.InputBox(Msg("Deselect.Title"), Msg("Select.Mask"), "*", func(mask string) {
							fsp.ApplyMaskSelection(mask, false)
						})
					}
					return true
				}
			}
		}
	}
	// 2. Try global hotkeys handled by PanelsFrame

	// Tab switches panels
	if e.VirtualKeyCode == vtinput.VK_TAB && !ctrl {
		if pf.showPanels {
			pf.activeIdx = 1 - pf.activeIdx
			pf.lastKey = 0
			return true
		}
	}

	// Ctrl+B toggles KeyBar
	if e.VirtualKeyCode == vtinput.VK_B && ctrl {
		pf.showKeyBar = !pf.showKeyBar
		pf.ResizeConsole(pf.lastW, pf.lastH)
		return true
	}

	// 3. Try Active Panel
	if pf.showPanels {
		if pf.Active().ProcessKey(e) {
			return true
		}
	} else {
		// Navigation keys when panels are hidden (Terminal is visible)
		if e.VirtualKeyCode == vtinput.VK_UP || (e.VirtualKeyCode == vtinput.VK_E && ctrl) {
			pf.cmdLine.Edit.HistoryUp()
			return true
		}
		if e.VirtualKeyCode == vtinput.VK_DOWN || (e.VirtualKeyCode == vtinput.VK_X && ctrl) {
			pf.cmdLine.Edit.HistoryDown()
			return true
		}
	}

	// 4. Fallback: pass to CommandLine (handles text, Backspace, Delete, etc.)
	if pf.cmdLine.ProcessKey(e) {
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

func (pf *PanelsFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	// Wheel events always scroll the active panel, regardless of mouse position.
	// This matches classic Far Manager / far2l behavior.
	if e.WheelDirection != 0 {
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

	mx, my := int(e.MouseX), int(e.MouseY)

	for i, p := range pf.panels {
		if p == nil {
			continue
		}
		x1, y1, x2, y2 := p.GetPosition()
		if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
			if pf.activeIdx != i && e.ButtonState != 0 {
				pf.activeIdx = i
				pf.lastKey = 0
				vtui.FrameManager.Redraw()
			}

			handled := p.ProcessMouse(e)
			if handled && (e.MouseEventFlags&vtinput.DoubleClick) != 0 && e.ButtonState == vtinput.FromLeft1stButtonPressed {
				pf.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
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

func (pf *PanelsFrame) GetPaths() (string, string) {
	l, r := "", ""
	if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
		l = fsp.vfs.GetPath()
	}
	if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
		r = fsp.vfs.GetPath()
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
			dlg.OnResult = func(code int) {
				if code == 0 {
					SaveSession()
					if pf.pty != nil {
						pf.pty.Close()
					}
					vtui.FrameManager.Shutdown()
				}
			}
		} else {
			SaveSession()
			if pf.pty != nil {
				pf.pty.Close()
			}
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
	case CmPanelSettings:
		actionPanelSettings(pf)
		return true
	case CmEditorSettings:
		actionEditorSettings(pf)
		return true
	case CmConfirmationsSettings:
		actionConfirmationsSettings(pf)
		return true
	case CmPlugins:
		actionManagePlugins(pf)
		return true

	case CmBackground:
		if !SupportsBackgrounding() {
			vtui.ShowMessage(" Background ", "Backgrounding is not supported on this OS.", []string{"&Ok"})
			return true
		}
		vtui.FrameManager.Stop() // Clean exit from the main loop
		return true

	case vtui.CmResize: // Used as a hack for 'fork' command from FrameManager
		if s, ok := args.(string); ok && s == "fork" {
			vtui.FrameManager.AddScreen(pf.Clone())
			return true
		}

	case CmLeftMedium:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetViewMode(ViewModeMedium)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmLeftDetailed:
		if fsp, ok := pf.panels[0].(*FileSystemPanel); ok {
			fsp.SetViewMode(ViewModeDetailed)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightMedium:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetViewMode(ViewModeMedium)
		}
		pf.updateMenuCheckmarks()
		return true
	case CmRightDetailed:
		if fsp, ok := pf.panels[1].(*FileSystemPanel); ok {
			fsp.SetViewMode(ViewModeDetailed)
		}
		pf.updateMenuCheckmarks()
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
	return &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.F1"), Msg("KeyBar.F2"), Msg("KeyBar.F3"), Msg("KeyBar.F4"),
			Msg("KeyBar.F5"), Msg("KeyBar.F6"), Msg("KeyBar.F7"), Msg("KeyBar.F8"),
			Msg("KeyBar.F9"), Msg("KeyBar.F10"), Msg("KeyBar.F11"), Msg("KeyBar.F12"),
		},
		Shift: vtui.KeyBarLabels{
			"", "", "", "", "", "Rename", "", "", "", "", "", "",
		},
		Alt: vtui.KeyBarLabels{
			Msg("KeyBar.AltF1"), Msg("KeyBar.AltF2"), Msg("KeyBar.AltF3"), "",
			"", "", Msg("KeyBar.AltF7"), Msg("KeyBar.AltF8"), "", "", "", Msg("KeyBar.AltF12"),
		},
		Ctrl: vtui.KeyBarLabels{
			Msg("KeyBar.CtrlF1"), Msg("KeyBar.CtrlF2"), Msg("KeyBar.CtrlF3"), Msg("KeyBar.CtrlF4"), Msg("KeyBar.CtrlF5"), Msg("KeyBar.CtrlF6"), Msg("KeyBar.CtrlF7"), "", "", "", "Fork", "Close",
		},
	}
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

	comboMode := vtui.NewComboBox(0, 0, 32, []string{"Queue (Default)", "Background panel clone", "Foreground lock"})
	comboMode.DropdownOnly = true
	comboMode.Menu.SetSelectPos(0)
	comboMode.Edit.SetText(comboMode.Menu.Items[0].Text)
	dlg.AddItem(comboMode)

	btnStart := vtui.NewButton(0, 0, "&Start")
	btnCancel := vtui.NewButton(0, 0, "&Cancel")
	dlg.AddItem(btnStart)
	dlg.AddItem(btnCancel)

	vbox.Add(comboMode, vtui.Margins{Top: 1}, vtui.AlignCenter)

	hbox := vtui.NewHBoxLayout(0, 0, 50-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnStart, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
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
	dlg := vtui.NewCenteredDialog(50, 12, title)
	dlg.AttentionSuppressed = true

	lbl := vtui.NewText(0, 0, startMsg, vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lbl)

	pb := vtui.NewProgressBar(0, 0, 46)
	dlg.AddItem(pb)

	lblHint := vtui.NewText(0, 0, Msg("Op.SwitchHint"), vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lblHint)

	btnCancel := vtui.NewButton(0, 0, "&Cancel")
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, 10-4)
	vbox.Add(lbl, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(pb, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(lblHint, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Add(btnCancel, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	var taskCtx *vtui.TaskContext
	btnCancel.OnClick = func() {
		dlg.SetExitCode(1)
	}
	dlg.OnResult = func(code int) {
		if taskCtx != nil {
			taskCtx.Cancel()
		}
	}

	vtui.FrameManager.PostTask(func() {
		if forked && pf != nil {
			clone := pf.Clone()
			vtui.FrameManager.AddScreen(clone)
			vtui.FrameManager.Push(dlg)
		} else {
			vtui.FrameManager.AddScreenHeadless(dlg)
		}
	})

	taskCtx = vtui.RunAsync(func(ctx *vtui.TaskContext) {
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
		ctx.RunOnUI(func() {
			dlg.Close()
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
				vtui.ShowToast("Dummy operation finished successfully", 3*time.Second)
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

func (pf *PanelsFrame) InputBox(title, prompt, history string, callback func(string)) {
	vtui.FrameManager.PostTask(func() {
		vtui.InputBox(title, prompt, history, callback)
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
		if h > 15 {
			h = 15
		} // Max height limit

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
	} else if _, isPty := v.(vfs.PtyProvider); isPty {
		sync = true
		isWindowsShell = false
	}

	if !sync {
		return true
	}

	activePty := pf.getActivePTY()
	if activePty != nil {
		if isWindowsShell {
			activePty.Write([]byte(fmt.Sprintf("cd /d \"%s\" & rem f4_sync\r", path)))
		} else {
			sqPath := strings.ReplaceAll(path, "'", "'\\''")
			activePty.Write([]byte(fmt.Sprintf(" cd '%s' # f4_sync\r", sqPath)))
		}
		return true
	}
	return false
}

func (pf *PanelsFrame) getActivePTYUnsafe() PtyBackend {
	if pf.remotePtys == nil {
		pf.remotePtys = make(map[vfs.VFS]PtyBackend)
	}

	var activeVfs vfs.VFS
	if fsp := pf.getActivePanel(); fsp != nil {
		activeVfs = fsp.vfs
	}

	if pp, ok := activeVfs.(vfs.PtyProvider); ok {
		if pty, exists := pf.remotePtys[activeVfs]; exists {
			return pty
		}

		res, err := pp.OpenPty(pf.termView.Width, pf.termView.Height)
		if err == nil {
			pty := res.(PtyBackend)
			vtui.DebugLog("Created new remote PTY background session for VFS")
			pf.remotePtys[activeVfs] = pty

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
						vtui.FrameManager.Redraw()
					}
				}
				pty.Close()
				pf.ptyMutex.Lock()
				delete(pf.remotePtys, activeVfs)
				pf.ptyMutex.Unlock()
			}()
			return pty
		}
	}
	return pf.pty
}

func (pf *PanelsFrame) getActivePTY() PtyBackend {
	pf.ptyMutex.Lock()
	defer pf.ptyMutex.Unlock()
	return pf.getActivePTYUnsafe()
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
		path = fsp.vfs.GetPath()
		if tp, ok := fsp.vfs.(vfs.TitleProvider); ok {
			if prefix := tp.GetTitle(); prefix != "" {
				path = prefix + ":" + path
			}
		}
	}

	if path != "" {
		return "Panels: " + path
	}
	return "Panels"
}

func (pf *PanelsFrame) Clone() *PanelsFrame {
	clone := NewPanelsFrame()
	if pf.lastW > 0 && pf.lastH > 0 {
		clone.ResizeConsole(pf.lastW, pf.lastH)
	}

	for i, p := range pf.panels {
		if fsp, ok := p.(*FileSystemPanel); ok {
			cloneFsp := clone.panels[i].(*FileSystemPanel)
			// Stop the initial load triggered by NewPanelsFrame to prevent races
			if cloneFsp.cancelLoad != nil {
				cloneFsp.cancelLoad()
			}
			// Important: reset isLoading so the clone doesn't think it's still
			// waiting for that cancelled initial load.
			cloneFsp.isLoading = false
			if cloneFsp.loadingTimer != nil {
				cloneFsp.loadingTimer.Stop()
			}

			cloneFsp.vfs.SetPath(fsp.vfs.GetPath())
			cloneFsp.SetViewMode(fsp.viewMode)
			cloneFsp.cursorIdx = fsp.cursorIdx
			cloneFsp.sortMode = fsp.sortMode
			cloneFsp.sortReverse = fsp.sortReverse

			cloneFsp.dirCache = make(map[string]dirCacheEntry)
			for k, v := range fsp.dirCache {
				cloneFsp.dirCache[k] = v
			}

			cloneFsp.selectedItems = make(map[string]bool)
			for k, v := range fsp.selectedItems {
				cloneFsp.selectedItems[k] = v
			}

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

	if pf.termView != nil && clone.termView != nil {
		clone.termView.CloneStateFrom(pf.termView)
	}
	clone.updateMenuCheckmarks()
	return clone
}

func (pf *PanelsFrame) showPluginMenu() {
	if len(PluginMenuItems) == 0 {
		vtui.ShowMessage(" Plugins ", "No plugins registered for F11 menu.", []string{"&Ok"})
		return
	}
	var labels []string
	for _, itm := range PluginMenuItems {
		labels = append(labels, itm.Label)
	}
	pf.Menu(" Plugins ", labels, func(idx int) {
		if idx >= 0 && idx < len(PluginMenuItems) {
			PluginMenuItems[idx].Handler(pf)
		}
	})
}

func (pf *PanelsFrame) showDriveMenu(panelIdx int) {
	menu := vtui.NewVMenu(" Drive ")

	usedHotkeys := make(map[rune]bool)
	usedHotkeys['o'] = true // "Other panel"

	// 1. Other panel (focused by default)
	menu.AddItem(vtui.MenuItem{Text: Msg("Panel.Other"), UserData: func(fsp *FileSystemPanel) {
		otherFsp := pf.panels[1-panelIdx].(*FileSystemPanel)
		if fsp.vfs != nil {
			fsp.vfs.Close()
		}
		fsp.vfs = otherFsp.vfs.Clone()
		fsp.ReadDirectory()
		pf.RefreshAll()
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

	// 3. Plugins & custom drives
	if len(DriveRegistry) > 0 {
		menu.AddSeparator()
		for _, drv := range DriveRegistry {
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

	menu.SetSelectPos(0)

	w, h := 26, menu.GetItemCount()+2
	x := (pf.lastW - w) / 2
	y := (pf.lastH - h) / 2
	if panelIdx == 0 {
		x = pf.lastW/4 - w/2
	} else {
		x = pf.lastW*3/4 - w/2
	}
	if x < 0 {
		x = 0
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

func (pf *PanelsFrame) switchToVFS(fsp *FileSystemPanel, newVFS vfs.VFS) {
	if newVFS != nil {
		if fsp.vfs != nil {
			fsp.vfs.Close()
			pf.ptyMutex.Lock()
			if pty, ok := pf.remotePtys[fsp.vfs]; ok {
				pty.Close()
				delete(pf.remotePtys, fsp.vfs)
			}
			pf.ptyMutex.Unlock()
		}
		fsp.dirCache = make(map[string]dirCacheEntry)
		fsp.vfs = newVFS
		fsp.ReadDirectory()
		pf.RefreshAll()
	}
}
func (pf *PanelsFrame) NavigateToPath(fsp *FileSystemPanel, targetPath string) bool {
	if targetPath == "" {
		return false
	}

	// 1. Handle "cd .." at the root of a nested VFS (e.g. escaping an archive)
	if targetPath == ".." && fsp.vfs.IsAtRoot() && fsp.vfs.ParentVFS() != nil {
		parent := fsp.vfs.ParentVFS()
		oldPath := fsp.vfs.GetPath()

		fsp.vfs.Close()
		pf.ptyMutex.Lock()
		if pty, ok := pf.remotePtys[fsp.vfs]; ok {
			pty.Close()
			delete(pf.remotePtys, fsp.vfs)
		}
		pf.ptyMutex.Unlock()

		fsp.dirCache = make(map[string]dirCacheEntry)
		fsp.vfs = parent
		fsp.pendingSelection = fsp.vfs.Base(oldPath)
		fsp.ReadDirectory()
		return true
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

	// 3. Try simple SetPath on current VFS (handles relative paths and absolute paths within same VFS)
	if err := fsp.vfs.SetPath(targetPath); err == nil {
		fsp.pendingSelection = ".."
		fsp.ReadDirectory()
		return true
	}

	return false
}

func expandPathEnv(s string) string {
	if runtime.GOOS == "windows" {
		var buf strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] == '%' {
				end := strings.IndexByte(s[i+1:], '%')
				if end <= 0 {
					buf.WriteByte(s[i])
					continue
				}
				name := s[i+1 : i+1+end]
				if v := os.Getenv(name); v != "" {
					buf.WriteString(v)
				} else {
					buf.WriteString(s[i : i+end+2])
				}
				i += end + 1
			} else {
				buf.WriteByte(s[i])
			}
		}
		return buf.String()
	}
	return os.ExpandEnv(s)
}
