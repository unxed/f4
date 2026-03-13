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
	pf.ResizeConsole()
	return pf
}

func (pf *PanelsFrame) ResizeConsole() {
	// Временное решение: берем стандартные 80x24 если терминал еще не инициализирован
	w, h := 80, 24
	// В реальности эти данные придут из ResizeConsole() через FrameManager

	// Разделяем экран пополам
	pf.left = NewFileSystemPanel(0, 0, w/2, h-2, ".")
	pf.right = NewFileSystemPanel(w/2, 0, w-w/2, h-2, ".")

	pf.left.SetFocus(true)
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