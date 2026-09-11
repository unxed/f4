package app

import (
	"github.com/unxed/f4/internal/appcmd"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/settings"
)

// handlePanelsAppCommand answers the frame commands the panels raise and do not
// serve themselves: every one of them opens a dialog or runs an action, which
// is the application's half of the frame. The order of the cases is the order
// PanelsFrame.HandleCommand had them in, because a command can reach a frame
// that is not the active one and the switch is what decides which panels
// answer.
func handlePanelsAppCommand(pf *panel.PanelsFrame, cmd int, args any) bool {
	switch cmd {
	case appcmd.CmNew:
		actionNewFile(pf)
		return true
	case appcmd.CmView:
		actionViewFile(pf)
		return true
	case appcmd.CmEdit:
		actionEditFile(pf)
		return true
	case appcmd.CmCopy, appcmd.CmMove:
		actionCopyMove(pf, cmd == appcmd.CmMove)
		return true
	case appcmd.CmRename:
		actionRename(pf)
		return true
	case appcmd.CmMkDir:
		actionMkDir(pf)
		return true
	case appcmd.CmDelete:
		actionDelete(pf)
		return true
	case appcmd.CmFindFile:
		actionFindFile(pf)
		return true
	case appcmd.CmPanelSettings:
		settings.Open("panels")
		return true
	case appcmd.CmEditorSettings:
		settings.Open("editor")
		return true
	case appcmd.CmColorerSettings:
		settings.Open("syntax")
		return true
	case appcmd.CmAppearanceSettings:
		settings.Open("appearance")
		return true
	case appcmd.CmConfirmationsSettings:
		settings.Open("operations")
		return true
	case appcmd.CmHotkeyConfig:
		settings.Open("hotkeys")
		return true
	case appcmd.CmLanguage:
		settings.Open("appearance")
		return true
	case appcmd.CmHelpLanguage:
		settings.Open("appearance")
		return true
	case appcmd.CmUpdateSettings:
		settings.Open("updates")
		return true
	case appcmd.CmProxySettings:
		return settings.Open("network")
	case appcmd.CmPlugins:
		settings.Open("plugins")
		return true
	case appcmd.CmPlugRing:
		settings.Open("plugins")
		return true
	case appcmd.CmBackground:
		return actionBackground()
	case appcmd.CmWorkspaceNew:
		return actionWorkspaceNew()
	}
	return false
}
