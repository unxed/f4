package main

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// PanelsFrame is the main frame of the f4 manager, containing left and right panels.
type PanelsFrame struct {
	vtui.ScreenObject
	left      Panel
	right     Panel
	activeIdx int // 0 for left, 1 for right

	cmdLine   *CommandLine
	keyBar    *vtui.KeyBar

	done      bool
}

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 0}
	pf.SetHelp("Panels")

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

	return pf
}

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	panelH := h - 2 // Leave space for command line and keybar
	leftW := w / 2
	rightW := w - leftW

	if pf.left == nil {
		pf.left = NewFileSystemPanel(0, 0, leftW, panelH, ".")
		pf.right = NewFileSystemPanel(leftW, 0, rightW, panelH, ".")
	} else {
		pf.left.SetPosition(0, 0, leftW-1, panelH-1)
		pf.right.SetPosition(leftW, 0, w-1, panelH-1)

		// Special methods for column adaptation (if it's FileSystemPanel)
		if fsp, ok := pf.left.(*FileSystemPanel); ok { fsp.Resize(leftW, panelH) }
		if fsp, ok := pf.right.(*FileSystemPanel); ok { fsp.Resize(rightW, panelH) }
	}

	pf.cmdLine.SetPosition(0, h-2, w-1, h-2)
	pf.keyBar.SetPosition(0, h-1, w-1, h-1)
}

func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	if pf.activeIdx == 0 {
		pf.left.SetFocus(true)
		pf.right.SetFocus(false)
	} else {
		pf.left.SetFocus(false)
		pf.right.SetFocus(true)
	}

	pf.left.Show(scr)
	pf.right.Show(scr)

	pf.cmdLine.Show(scr)
	pf.keyBar.Show(scr)
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	// Update KeyBar modifier state regardless of KeyDown (to catch holding Shift/Alt)
	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	pf.keyBar.SetModifiers(shift, ctrl, alt)

	if !e.KeyDown { return false }

	// Orchestration: who gets the input?
	// 1. Text input always goes to CommandLine (if no Alt/Ctrl modifiers that panels use)
	if e.Char != 0 && !alt && !ctrl {
		pf.cmdLine.SetFocus(true)
		return pf.cmdLine.ProcessKey(e)
	}

	// 2. F1 invokes help
	if e.VirtualKeyCode == vtinput.VK_F1 {
		pf.ShowHelp()
		return true
	}

	// Tab switches panels
	if e.VirtualKeyCode == vtinput.VK_TAB {
		pf.activeIdx = 1 - pf.activeIdx
		return true
	}

	if pf.activeIdx == 0 {
		return pf.left.ProcessKey(e)
	}
	return pf.right.ProcessKey(e)
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
func (pf *PanelsFrame) IsDone() bool             { return pf.done }