package main

import (
	"fmt"
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
	menuActive bool
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

	pf.menuBar = vtui.NewMenuBar([]string{
		Msg("Menu.Left"), Msg("Menu.Files"), Msg("Menu.Commands"),
		Msg("Menu.Options"), Msg("Menu.Right"),
	})
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

	// Calculate content area (panels or terminal)
	contentY1 := 0
	contentY2 := h - 2 // One line for CommandLine, the rest for content
	if pf.showKeyBar {
		contentY2 = h - 3 // One for CommandLine, one for KeyBar
	}
	contentH := contentY2 - contentY1 + 1
	if contentH < 0 {
		contentH = 0
	}

	// Resize PTY and TerminalView
	if pf.pty != nil {
		pf.pty.SetSize(w, contentH)
		pf.termView.SetPosition(0, contentY1, w-1, contentY2)
		pf.termView.Resize(w, contentH)
	}

	// Resize Panels
	panelH := contentH
	leftW := w / 2
	rightW := w - leftW

	if pf.left == nil {
		pf.left = NewFileSystemPanel(0, contentY1, leftW, panelH, ".")
		pf.right = NewFileSystemPanel(leftW, contentY1, rightW, panelH, ".")
	} else {
		pf.left.SetPosition(0, contentY1, leftW-1, contentY2)
		pf.right.SetPosition(leftW, contentY1, w-1, contentY2)

		// Special methods for column adaptation (if it's FileSystemPanel)
		if fsp, ok := pf.left.(*FileSystemPanel); ok { fsp.Resize(leftW, panelH) }
		if fsp, ok := pf.right.(*FileSystemPanel); ok { fsp.Resize(rightW, panelH) }
	}

	if pf.showKeyBar {
		// CommandLine on penultimate line, KeyBar on the last one
		pf.cmdLine.SetPosition(0, h-2, w-1, h-2)
		pf.keyBar.SetPosition(0, h-1, w-1, h-1)
		pf.keyBar.SetVisible(true)
	} else {
		// CommandLine on the very last line
		pf.cmdLine.SetPosition(0, h-1, w-1, h-1)
		pf.keyBar.SetVisible(false)
	}
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
		if pf.showPanels {
			// Regular command line position
			y := pf.lastH - 1
			if pf.showKeyBar {
				y--
			}
			pf.cmdLine.SetPrompt(Msg("Panels.Prompt"))
			pf.cmdLine.SetPosition(0, y, pf.lastW-1, y)
		} else {
			// Terminal command line position
			pf.cmdLine.SetPrompt("")
			tx, ty := pf.termView.CursorX, pf.termView.CursorY
			// Adjust for terminal's own coordinates
			_, termY1, _, _ := pf.termView.GetPosition()
			pf.cmdLine.SetPosition(tx, termY1+ty, pf.lastW-1, termY1+ty)
		}
	}
	if pf.cmdLine.IsVisible() {
		pf.cmdLine.Show(scr)
	}

	// KeyBar is always at the bottom
	if pf.showKeyBar {
		pf.keyBar.Show(scr)
	}

	// Menu must be drawn LAST to appear on top of panels
	if pf.menuActive {
		pf.menuBar.SetVisible(true)
		pf.menuBar.Show(scr)
	} else {
		pf.menuBar.SetVisible(false)
	}
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	pf.keyBar.SetModifiers(shift, ctrl, alt)

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

	// F1 invokes help (global)
	if e.VirtualKeyCode == vtinput.VK_F1 {
		pf.ShowHelp()
		return true
	}

	// F9 toggles MenuBar
	if e.VirtualKeyCode == vtinput.VK_F9 {
		pf.menuActive = !pf.menuActive
		pf.menuBar.Active = pf.menuActive
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
			// Add space if needed
			if txt != "" && txt[len(txt)-1] != ' ' {
				pf.cmdLine.InsertString(" ")
			}
			pf.cmdLine.InsertString(name)
		}
		return true
	}

	// If menu is active, it takes all input
	if pf.menuActive {
		if e.VirtualKeyCode == vtinput.VK_ESCAPE {
			pf.menuActive = false
			pf.menuBar.Active = false
			return true
		}
		// Enter or Down opens the submenu
		if e.VirtualKeyCode == vtinput.VK_RETURN || e.VirtualKeyCode == vtinput.VK_DOWN {
			pf.openSubMenu(pf.menuBar.SelectPos)
			return true
		}
		return pf.menuBar.ProcessKey(e)
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
					if fsp, ok := pf.left.(*FileSystemPanel); ok { path = fsp.path }
				} else {
					if fsp, ok := pf.right.(*FileSystemPanel); ok { path = fsp.path }
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

func (pf *PanelsFrame) GetType() vtui.FrameType { return vtui.TypePanels }
func (pf *PanelsFrame) SetExitCode(code int)     { pf.done = true }
func (pf *PanelsFrame) openSubMenu(index int) {
	menu := vtui.NewVMenu(pf.menuBar.Items[index].Label)
	menu.AddItem(Msg("Menu.Exit"))

	x := pf.menuBar.GetItemX(index)
	menu.SetPosition(x, 1, x+15, 3)

	// Keep pf.menuActive = true so MenuBar stays visible under the VMenu.
	// But set Active = false for visual state (selection doesn't move while submenu is open).
	pf.menuBar.Active = false

	menu.OnSelect = func(selected int) {
		if selected == 0 { // "Exit"
			vtui.FrameManager.Shutdown()
		}
	}

	// If we close the menu with Esc, we return focus to the MenuBar
	menu.OnClose = func() {
		pf.menuBar.Active = true
	}

	// Handle switching between top menu items with Left/Right arrows
	menu.OnLeft = func() {
		pf.menuBar.Active = true
		// Simulate Left key on MenuBar to update its SelectPos
		pf.menuBar.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT})
		pf.openSubMenu(pf.menuBar.SelectPos)
	}

	menu.OnRight = func() {
		pf.menuBar.Active = true
		// Simulate Right key on MenuBar
		pf.menuBar.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})
		pf.openSubMenu(pf.menuBar.SelectPos)
	}

	vtui.FrameManager.Push(menu)
}
func (pf *PanelsFrame) IsDone() bool             { return pf.done }
