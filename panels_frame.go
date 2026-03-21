package main

import (
	"fmt"
	"os"
	"github.com/unxed/vtui/piecetable"
	"os/user"
	"strings"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// PanelsFrame is the main frame of the f4 manager, containing left and right panels.
type PanelsFrame struct {
	vtui.ScreenObject
	left      Panel
	right     Panel
	activeIdx int // 0 for left, 1 for right

	menuBar   *vtui.MenuBar
	cmdLine   *CommandLine
	keyBar    *vtui.KeyBar

	showKeyBar bool
	showPanels bool
	lastW      int
	lastH      int

	// Integrated Terminal
	pty      PtyBackend
	termView *TerminalView
	parser   *AnsiParser

	done      bool
}

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 1}
	pf.SetHelp("Panels")
	pf.showKeyBar = true
	pf.showPanels = true

	pf.menuBar = vtui.NewMenuBar(nil)
	pf.menuBar.Items = []vtui.MenuBarItem{
		{Label: "&" + Msg("Menu.Left"), SubItems: []vtui.MenuItem{{Text: Msg("Menu.Exit")}}},
		{Label: "&" + Msg("Menu.Files"), SubItems: []vtui.MenuItem{{Text: "Placeholder"}}},
		{Label: "&" + Msg("Menu.Commands"), SubItems: []vtui.MenuItem{{Text: "Placeholder"}}},
		{Label: "&" + Msg("Menu.Options"), SubItems: []vtui.MenuItem{{Text: "Placeholder"}}},
		{Label: "&" + Msg("Menu.Right"), SubItems: []vtui.MenuItem{{Text: "Placeholder"}}},
	}
	pf.menuBar.OnCommand = func(menuIdx, itemIdx int) {
		if menuIdx == 0 && itemIdx == 0 {
			vtui.FrameManager.Shutdown()
		}
	}
	pf.cmdLine = NewCommandLine(Msg("Panels.Prompt"))
	pf.keyBar = vtui.NewKeyBar()

	// Initialize KeyBar labels
	pf.keyBar.Normal = vtui.KeyBarLabels{
		Msg("KeyBar.F1"), Msg("KeyBar.F2"), Msg("KeyBar.F3"), Msg("KeyBar.F4"),
		Msg("KeyBar.F5"), Msg("KeyBar.F6"), Msg("KeyBar.F7"), Msg("KeyBar.F8"),
		Msg("KeyBar.F9"), Msg("KeyBar.F10"), Msg("KeyBar.F11"), Msg("KeyBar.F12"),
	}
	pf.keyBar.Alt = vtui.KeyBarLabels{
		Msg("KeyBar.AltF1"), Msg("KeyBar.AltF2"), "", "",
		"", "", "", "", "", "", "", "",
	}

	pf.termView = NewTerminalView(80, 24)
	// Parser will be fully initialized in initPTY once pty is ready
	pf.initPTY()

	return pf
}

func (pf *PanelsFrame) buildPrompt() []vtui.CharInfo {
	var path string
	if pf.activeIdx == 0 {
		if fsp, ok := pf.left.(*FileSystemPanel); ok { path = fsp.vfs.GetPath() }
	} else {
		if fsp, ok := pf.right.(*FileSystemPanel); ok { path = fsp.vfs.GetPath() }
	}

	usr, _ := user.Current()
	username := "user"
	home := ""
	if usr != nil {
		username = usr.Username
		home = usr.HomeDir
	}

	host, _ := os.Hostname()
	if host == "" { host = "localhost" }

	displayPath := path
	if home != "" && strings.HasPrefix(displayPath, home) {
		displayPath = "~" + displayPath[len(home):]
	}

	bg := vtui.GetRGBBack(vtui.Palette[ColCommandLineUserScreen])
	// Use colors as close as possible to classic bash
	greenAttr := vtui.SetRGBBoth(0, 0x8AE234, bg) // Bright green
	blueAttr := vtui.SetRGBBoth(0, 0x729FCF, bg)  // Bright blue
	defAttr := vtui.SetRGBBoth(0, 0xFFFFFF, bg)   // White

	var prompt []vtui.CharInfo
	prompt = append(prompt, vtui.StringToCharInfo(username+"@"+host, greenAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(":", defAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo(displayPath, blueAttr)...)
	prompt = append(prompt, vtui.StringToCharInfo("$ ", defAttr)...)

	return prompt
}

func (pf *PanelsFrame) initPTY() {
	p, err := NewPTY()
	if err != nil {
		return
	}
	pf.pty = p
	pf.parser = NewAnsiParser(pf.termView, pf.pty)
	shell := GetSystemShell()
	pf.pty.Run(shell)

	// Read loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pf.pty.Read(buf)
			if err != nil {
				return
			}
			pf.parser.Process(buf[:n])
			vtui.FrameManager.Redraw()
		}
	}()
}

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	pf.lastW, pf.lastH = w, h
	pf.menuBar.SetPosition(0, 0, w-1, 0)

	contentY1 := 0

	// 1. Terminal Area: Fills everything except KeyBar
	termY2 := h - 1
	if pf.showKeyBar {
		termY2 = h - 2
	}
	termH := termY2 - contentY1 + 1
	if termH < 0 { termH = 0 }

	if pf.pty != nil {
		pf.pty.SetSize(w, termH)
		pf.termView.SetPosition(0, contentY1, w-1, termY2)
		pf.termView.Resize(w, termH)
	}

	// 2. Panel Area: Leaves one additional line for the f4 CommandLine
	panelY2 := h - 2
	if pf.showKeyBar {
		panelY2 = h - 3
	}
	panelH := panelY2 - contentY1 + 1
	if panelH < 0 { panelH = 0 }

	leftW := w / 2
	rightW := w - leftW

	if pf.left == nil {
		pf.left = NewFileSystemPanel(0, contentY1, leftW, panelH, vtui.NewOSVFS("."))
		pf.right = NewFileSystemPanel(leftW, contentY1, rightW, panelH, vtui.NewOSVFS("."))
	} else {
		pf.left.SetPosition(0, contentY1, leftW-1, panelY2)
		pf.right.SetPosition(leftW, contentY1, w-1, panelY2)

		// Special methods for column adaptation (if it's FileSystemPanel)
		if fsp, ok := pf.left.(*FileSystemPanel); ok { fsp.Resize(leftW, panelH) }
		if fsp, ok := pf.right.(*FileSystemPanel); ok { fsp.Resize(rightW, panelH) }
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
}

func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	if pf.showPanels {
		pf.termView.SetVisible(false)
		if pf.activeIdx == 0 {
			pf.left.SetFocus(true)
			pf.right.SetFocus(false)
		} else {
			pf.left.SetFocus(false)
			pf.right.SetFocus(true)
		}
		pf.left.Show(scr)
		pf.right.Show(scr)
	} else {
		pf.termView.SetVisible(true)
		pf.termView.Show(scr)
	}

	// Command line logic depends on terminal state
	if !pf.showPanels && pf.termView.UseAltScreen {
		pf.cmdLine.SetVisible(false)
	} else {
		pf.cmdLine.SetVisible(true)
		cmdLineY := pf.lastH - 1
		if pf.showKeyBar {
			cmdLineY = pf.lastH - 2
		}
		if pf.showPanels {
			pf.cmdLine.SetRichPrompt(pf.buildPrompt())
			pf.cmdLine.SetPosition(0, cmdLineY, pf.lastW-1, cmdLineY)
		} else {
			pf.cmdLine.SetRichPrompt(nil)
			pf.cmdLine.SetPrompt("")
			tx, ty := pf.termView.CursorX, pf.termView.CursorY
			_, termY1, _, _ := pf.termView.GetPosition()
			pf.cmdLine.SetPosition(tx, termY1+ty, pf.lastW-1, termY1+ty)
		}
		if pf.cmdLine.IsVisible() {
			pf.cmdLine.Show(scr)
		}
	}

	// KeyBar is always at the bottom
	if pf.showKeyBar {
		pf.keyBar.Show(scr)
	}
	// Macro Recording Indicator
	if MacroMgr != nil && MacroMgr.Recording {
		scr.Write(0, 0, vtui.StringToCharInfo(" R ", vtui.SetRGBBoth(0, 0xFFFFFF, 0xFF0000)))
	}
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {

	//shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	//alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0

	if !e.KeyDown {
		return false
	}

	// Raw input mode for interactive terminal apps (like far2l inside f4)
	if !pf.showPanels && pf.termView.UseAltScreen {
		// Guest app is interactive (Alt Screen). Forward all keys including Ctrl+O.
		if pf.pty != nil {
			pf.pty.Write([]byte(TranslateInput(e)))
		}
		return true
	}

	// F10 exits the application (global, but can be overridden by terminal raw mode)
	if e.VirtualKeyCode == vtinput.VK_F10 {
		vtui.FrameManager.Shutdown()
		return true
	}

	// F4 opens the internal editor for the selected file
	if e.VirtualKeyCode == vtinput.VK_F4 {
		var name string
		var path string
		if pf.activeIdx == 0 {
			if fsp, ok := pf.left.(*FileSystemPanel); ok {
				name = fsp.GetSelectedName()
				path = fsp.vfs.Join(fsp.vfs.GetPath(), name)
			}
		} else {
			if fsp, ok := pf.right.(*FileSystemPanel); ok {
				name = fsp.GetSelectedName()
				path = fsp.vfs.Join(fsp.vfs.GetPath(), name)
			}
		}

		if path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				// If the file does not exist, open an empty editor for creation
				data = []byte("")
			}
			pt := piecetable.New(data)
			editor := NewEditorView(pt, path)
			editor.SetPosition(0, 0, pf.lastW-1, pf.lastH-3)
			vtui.FrameManager.Push(editor)
			return true
		}
	}

	// F1 invokes help (global)
	if e.VirtualKeyCode == vtinput.VK_F1 {
		pf.ShowHelp()
		return true
	}

	// Esc clears command line if it's not empty
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && !pf.cmdLine.IsEmpty() {
		pf.cmdLine.Clear()
		return true
	}

	// Ctrl+Enter inserts selected file name
	if e.VirtualKeyCode == vtinput.VK_RETURN && ctrl {
		var name string
		if pf.activeIdx == 0 {
			name = pf.left.GetSelectedName()
		} else {
			name = pf.right.GetSelectedName()
		}
		if name != "" {
			txt := pf.cmdLine.Edit.GetText()
			// Add space if the line is empty, or if it's not empty and doesn't end with a space.
			if len(txt) == 0 || txt[len(txt)-1] != ' ' {
				pf.cmdLine.InsertString(" ")
			}
			pf.cmdLine.InsertString(name)
		}
		return true
	}


	// Ctrl+O toggles panels visibility
	if e.VirtualKeyCode == vtinput.VK_O && ctrl {
		pf.showPanels = !pf.showPanels
		return true
	}

	// Enter handling
	if e.VirtualKeyCode == vtinput.VK_RETURN {
		if !pf.cmdLine.IsEmpty() {
			cmd := pf.cmdLine.Edit.GetText()
			if pf.pty != nil {
				// 1. Determine current path of active panel
				var path string
				if pf.activeIdx == 0 {
					if fsp, ok := pf.left.(*FileSystemPanel); ok { path = fsp.vfs.GetPath() }
				} else {
					if fsp, ok := pf.right.(*FileSystemPanel); ok { path = fsp.vfs.GetPath() }
				}

				// 2. Sync PTY directory (send cd) and then the command
				if path != "" {
					pf.pty.Write([]byte(fmt.Sprintf(" cd %q\r", path)))
				}
				pf.pty.Write([]byte(cmd + "\r"))
			}
			pf.cmdLine.Clear()
			pf.showPanels = false // Auto-hide panels to show output
			return true
		} else if !pf.showPanels {
			if pf.pty != nil {
				pf.pty.Write([]byte("\r"))
			}
			return true
		}
		// If command line is empty and panels visible, Enter is passed to panels (to enter dir)
	}

	// 2. Try global hotkeys handled by PanelsFrame

	// Tab switches panels
	if e.VirtualKeyCode == vtinput.VK_TAB {
		pf.activeIdx = 1 - pf.activeIdx
		return true
	}

	// Ctrl+B toggles KeyBar
	if e.VirtualKeyCode == vtinput.VK_B && ctrl {
		pf.showKeyBar = !pf.showKeyBar
		pf.ResizeConsole(pf.lastW, pf.lastH)
		return true
	}

	// 3. Try Active Panel
	panelHandled := false
	if pf.activeIdx == 0 {
		panelHandled = pf.left.ProcessKey(e)
	} else {
		panelHandled = pf.right.ProcessKey(e)
	}

	if panelHandled {
		return true
	}

	// 4. Fallback: pass to CommandLine (handles text, Backspace, Delete, etc.)
	if pf.cmdLine.ProcessKey(e) {
		pf.cmdLine.SetFocus(true)
		return true
	}

	return false
}

func (pf *PanelsFrame) ProcessMouse(e *vtinput.InputEvent) bool {
	// Determine which panel was clicked
	mx, my := int(e.MouseX), int(e.MouseY)

	x1, y1, x2, y2 := pf.left.GetPosition()
	if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
		pf.activeIdx = 0
		return pf.left.ProcessMouse(e)
	}

	x1, y1, x2, y2 = pf.right.GetPosition()
	if mx >= x1 && mx <= x2 && my >= y1 && my <= y2 {
		pf.activeIdx = 1
		return pf.right.ProcessMouse(e)
	}

	return false
}

func (pf *PanelsFrame) GetType() vtui.FrameType { return vtui.TypeUser + 1 }
func (pf *PanelsFrame) SetExitCode(code int)     { pf.done = true }
func (pf *PanelsFrame) IsDone() bool             { return pf.done }
func (pf *PanelsFrame) IsBusy() bool             { return false }
func (pf *PanelsFrame) IsModal() bool { return false }
func (pf *PanelsFrame) GetWindowNumber() int { return 0 }
func (pf *PanelsFrame) SetWindowNumber(n int) {}
func (pf *PanelsFrame) RequestFocus() bool { return true }
func (pf *PanelsFrame) Close() { pf.done = true }
