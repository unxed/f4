package main

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// PanelsFrame — главный фрейм менеджера f4, содержит левую и правую панели.
type PanelsFrame struct {
	vtui.ScreenObject
	left      Panel
	right     Panel
	activeIdx int // 0 для левой, 1 для правой

	done      bool
}

func NewPanelsFrame() *PanelsFrame {
	pf := &PanelsFrame{activeIdx: 0}
	return pf
}

func (pf *PanelsFrame) ResizeConsole(w, h int) {
	panelH := h - 2 // Оставляем место под командную строку и статус
	leftW := w / 2
	rightW := w - leftW

	if pf.left == nil {
		pf.left = NewFileSystemPanel(0, 0, leftW, panelH, ".")
		pf.right = NewFileSystemPanel(leftW, 0, rightW, panelH, ".")
	} else {
		pf.left.SetPosition(0, 0, leftW-1, panelH-1)
		pf.right.SetPosition(leftW, 0, w-1, panelH-1)

		// Специальные методы для адаптации колонок (если это FileSystemPanel)
		if fsp, ok := pf.left.(*FileSystemPanel); ok { fsp.Resize(leftW, panelH) }
		if fsp, ok := pf.right.(*FileSystemPanel); ok { fsp.Resize(rightW, panelH) }
	}
}

func (pf *PanelsFrame) Show(scr *vtui.ScreenBuf) {
	// При ресайзе нужно будет обновлять координаты
	if pf.activeIdx == 0 {
		pf.left.SetFocus(true)
		pf.right.SetFocus(false)
	} else {
		pf.left.SetFocus(false)
		pf.right.SetFocus(true)
	}

	pf.left.Show(scr)
	pf.right.Show(scr)

	// Командная строка (заглушка)
	scr.Write(0, scr.Height()-1, vtui.StringToCharInfo("> ", vtui.SetRGBFore(0, 0xFFFFFF)))
}

func (pf *PanelsFrame) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown { return false }

	// Tab переключает панели
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
	// Определяем, в какую панель попал клик
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