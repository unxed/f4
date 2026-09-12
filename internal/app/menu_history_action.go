package app

import (
	"github.com/unxed/f4/internal/history"

	"github.com/unxed/vtui"
)

// actionSelectLastMenuItem is the configurable Shift+F10 action. It is kept
// separate from the observer below so an explicit user unbind really silences
// the key instead of being bypassed by a physical-key fallback.
func actionSelectLastMenuItem() bool {
	if vtui.FrameManager == nil {
		return false
	}
	if menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); ok {
		history.HookMenuHistory(menu)
		return history.SelectLastMenuItem(menu)
	}

	menuBar := vtui.FrameManager.GetActiveMenuBar()
	if !activateMainMenuAt(history.LastMainMenuPosition(menuBar), true) {
		return false
	}
	if menu, ok := vtui.FrameManager.GetTopFrame().(*vtui.VMenu); ok {
		history.HookMenuHistory(menu)
		history.SelectLastMenuItem(menu)
	}
	return true
}
