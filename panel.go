package main

import (
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Panel — это интерфейс любого контента, который может быть помещен в "половинку" менеджера.
// Это может быть список файлов, дерево папок или даже панель быстрого просмотра (Viewer).
type Panel interface {
	Show(scr *vtui.ScreenBuf)
	ProcessKey(e *vtinput.InputEvent) bool
	ProcessMouse(e *vtinput.InputEvent) bool
	SetFocus(f bool)
	IsFocused() bool
	SetPosition(x1, y1, x2, y2 int)
	GetPosition() (int, int, int, int)
}