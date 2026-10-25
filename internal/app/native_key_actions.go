package app

import (
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtui"
)

// The actions of the keys the panels, the drive menu and vtui handle
// themselves. Each declares its keys as NativeKeys, which makes them read-only
// rows of the Hotkey Configurator: there the user sees every key f4 reacts to,
// even one no binding can move, and so does a feature that picks a key of its
// own and has to know what is taken (cmd/f4/key_inventory_test.go). The action
// also brings the key's work to the command palette and to macros, as every
// action does (docs/UX_GUIDELINES.md, "One Action, Three Faces").

// nativeKeyAction describes one such action. Its message keys are
// "Action.<name>" and "Action.<name>.Desc".
type nativeKeyAction struct {
	name, area  string
	label, desc string // English fallbacks
	keys        []string
	// aliases are keys that do the same without being worth a mention in
	// menus and the palette (action.Action.NativeKeyAliases).
	aliases []string
	// searchKeys are message keys the palette also finds the action by
	// (action.Action.SearchKeys).
	searchKeys []string
	// condition gates the action when its keys only work in some state
	// (keymap.ConditionTrue): the palette offers it only then, and it does
	// nothing otherwise. Empty for keys that always work.
	condition    string
	defaultAreas []string
	// handler does the work; nil presses the first key in the panels.
	handler func() bool
	// hidden keeps the action out of the command palette.
	hidden bool
	// bindingWins: a binding the user puts on one of the keys runs instead
	// of the key's own work (the numbered bookmarks, vtui's workspace
	// digits). Otherwise the frame takes the keys first, in their state
	// (action.Action.NativeKeysFirst).
	bindingWins bool
}

func registerNativeKeyAction(spec nativeKeyAction) {
	a := action.Action{
		Name:             spec.name,
		Area:             spec.area,
		Label:            spec.label,
		LabelKey:         "Action." + spec.name,
		Description:      spec.desc,
		DescKey:          "Action." + spec.name + ".Desc",
		SearchKeys:       spec.searchKeys,
		NativeKeys:       spec.keys,
		NativeKeyAliases: spec.aliases,
		NativeKeysFirst:  !spec.bindingWins,
		DefaultAreas:     spec.defaultAreas,
		Handler:          spec.handler,
	}
	if a.Handler == nil {
		key, _, _ := strings.Cut(spec.keys[0], ":")
		a.Handler = pressInPanels(key)
	}
	if spec.condition != "" {
		holds := conditionHolds(spec.condition)
		a.Visible = holds
		a.Enabled = holds
	}
	if spec.hidden {
		a.Visible = func() bool { return false }
	}
	registerAction(a)
}

// pressInPanels does what key does in the panels (PanelsFrame.PressNativeKey).
func pressInPanels(key string) func() bool {
	return func() bool {
		pf := panel.FindPanelsFrameAnyScreen()
		return pf != nil && pf.PressNativeKey(key)
	}
}

// pressInDriveMenu does what key does in the open drive menu.
func pressInDriveMenu(key string) func() bool {
	return func() bool { return panel.PressDriveMenuKey(key) }
}

func conditionHolds(name string) func() bool {
	return func() bool { return keymap.ConditionTrue(name) }
}

// switchToWorkspaceNumber switches to the workspace that shows number n, as
// Alt and the digit do in vtui.
func switchToWorkspaceNumber(n int) func() bool {
	return func() bool {
		fm := vtui.FrameManager
		if fm == nil {
			return false
		}
		for idx, screen := range fm.Screens {
			if screen != nil && screen.Number == n {
				fm.SwitchScreen(idx)
				return true
			}
		}
		return false
	}
}

func withPanels(do func(pf *panel.PanelsFrame) bool) func() bool {
	return func() bool {
		pf := panel.FindPanelsFrameAnyScreen()
		return pf != nil && do(pf)
	}
}

// playerCommand does cmd in the focused player (PlayerPanel.Do).
func playerCommand(cmd panel.PlayerCommand) func() bool {
	return withPanels(func(pf *panel.PanelsFrame) bool {
		player := pf.FocusedPlayer()
		if player == nil || !player.Do(cmd) {
			return false
		}
		vtui.FrameManager.Redraw()
		return true
	})
}

// playerJump moves the track of the focused player to tenth tenths of it.
func playerJump(tenth int) func() bool {
	return withPanels(func(pf *panel.PanelsFrame) bool {
		player := pf.FocusedPlayer()
		if player == nil || !player.JumpToTenth(tenth) {
			return false
		}
		vtui.FrameManager.Redraw()
		return true
	})
}

func init() {
	for _, spec := range nativeKeyActions() {
		registerNativeKeyAction(spec)
	}
}

func nativeKeyActions() []nativeKeyAction {
	actions := []nativeKeyAction{
		// The panels themselves (PanelsFrame.ProcessKey).
		{name: "Panel.Open", area: "Shell", label: "Open",
			desc: "Open the folder or file under the cursor, or run the command line",
			keys: []string{"Enter"}},
		{name: "Panel.SwitchActive", area: "Shell", label: "Switch Panels",
			desc: "Make the other panel the active one",
			keys: []string{"Tab"}, aliases: []string{"ShiftTab"}},
		{name: "Debug.Crash", area: "Shell", label: "Test Crash",
			desc: "Make f4 crash on purpose, to test the crash report",
			keys: []string{"CtrlAltC"}, hidden: true},
		{name: "Panel.InterruptRemoteCommand", area: "Shell", label: "Interrupt Remote Command",
			desc: "Interrupt the command running on the remote side of the panel",
			keys: []string{"CtrlC:RemoteCommand"}, condition: "RemoteCommand"},
		{name: "Panel.ClosePluginPanel", area: "Shell", label: "Close Plugin Panel",
			desc: "Close the plugin panel and return to the file panel",
			keys: []string{"F10:PluginPanel", "CtrlPgUp:PluginPanel", "Esc:PluginPanel"}, condition: "PluginPanel"},

		// The command line.
		{name: "CommandLine.Clear", area: "Shell", label: "Clear Command Line",
			desc: "Erase the text of the command line",
			keys: []string{"CtrlY", "Esc:CommandLineNotEmpty"}},
		// With the panels hidden, or the command line focused in the
		// search-first navigation, the plain arrows walk the history too.
		{name: "CommandLine.HistoryPrevious", area: "Shell", label: "Previous Command",
			desc: "Put the previous command of the history into the command line",
			keys: []string{"CtrlE"}, aliases: []string{"Up:TerminalQuiet"},
			defaultAreas: []string{"Terminal"}},
		{name: "CommandLine.HistoryNext", area: "Shell", label: "Next Command",
			desc: "Put the next command of the history into the command line",
			keys: []string{"CtrlX"}, aliases: []string{"Down:TerminalQuiet"},
			defaultAreas: []string{"Terminal"}},
		{name: "CommandLine.Complete", area: "Shell", label: "Complete Command",
			desc: "Offer completions for the text of the command line",
			keys: []string{"Tab:CommandLineNotEmpty"}, condition: "CommandLineNotEmpty",
			defaultAreas: []string{"Terminal"},
			handler:      withPanels(func(pf *panel.PanelsFrame) bool { return pf.CompleteCommandLine() })},
		{name: "CommandLine.WordLeft", area: "Shell", label: "Word Left in Command Line",
			desc: "Move the cursor of the command line one word to the left",
			keys: []string{"CtrlLeft:CommandLineNotEmpty"}, condition: "CommandLineNotEmpty"},
		{name: "CommandLine.WordRight", area: "Shell", label: "Word Right in Command Line",
			desc: "Move the cursor of the command line one word to the right",
			keys: []string{"CtrlRight:CommandLineNotEmpty"}, condition: "CommandLineNotEmpty"},
		{name: "CommandLine.SelectWordLeft", area: "Shell", label: "Select Word Left in Command Line",
			desc: "Extend the selection of the command line one word to the left",
			keys: []string{"CtrlShiftLeft:CommandLineNotEmpty"}, condition: "CommandLineNotEmpty"},
		{name: "CommandLine.SelectWordRight", area: "Shell", label: "Select Word Right in Command Line",
			desc: "Extend the selection of the command line one word to the right",
			keys: []string{"CtrlShiftRight:CommandLineNotEmpty"}, condition: "CommandLineNotEmpty"},

		// The quick search and the autofilter of the file panel.
		{name: "Panel.FastFind.Next", area: "Shell", label: "Quick Search: Next Match",
			desc: "Move the cursor to the next name that matches the quick search",
			keys: []string{"CtrlEnter:FastFind"}, condition: "FastFind"},
		{name: "Panel.FastFind.Previous", area: "Shell", label: "Quick Search: Previous Match",
			desc: "Move the cursor to the previous name that matches the quick search",
			keys: []string{"CtrlShiftEnter:FastFind"}, condition: "FastFind"},
		{name: "Panel.FastFind.Anywhere", area: "Shell", label: "Quick Search: Match Anywhere",
			desc: "Switch the quick search between the start of a name and anywhere in it",
			keys: []string{"F2:FastFind"}, condition: "FastFind"},
		{name: "Panel.AutoFilter.ToggleExact", area: "Shell", label: "Autofilter: Exact Match",
			desc: "Turn the exact match of the autofilter on or off",
			keys: []string{"CtrlE:AutoFilter"}, condition: "AutoFilter"},

		// The temporary panel.
		{name: "TempPanel.ShowOnPassive", area: "Shell", label: "Temporary Panel: Show in Passive Panel",
			desc: "Open the place of the item under the cursor in the passive panel",
			keys: []string{"AltShiftF3:TempPanel"}, condition: "TempPanel"},

		// The info panel and the quick view (Ctrl+L, Ctrl+Q).
		{name: "Panel.AltPanelCopy", area: "Shell", label: "Copy Line",
			desc: "Copy the marked lines of the info panel or the quick view, or the line under the cursor",
			keys: []string{"C:AltPanelFocused"}, condition: "AltPanelFocused",
			searchKeys: []string{"CommandPalette.QueryQuickView"}},
		{name: "QuickView.Wrap", area: "Shell", label: "Quick View: Wrap Lines",
			desc: "Wrap the long lines of the quick view, or stop wrapping them",
			keys: []string{"F2:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.OpenViewer", area: "Shell", label: "Quick View: Open in Viewer",
			desc: "Open the file of the quick view in the viewer",
			keys: []string{"F3:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.Hex", area: "Shell", label: "Quick View: Hex Mode",
			desc: "Show the file of the quick view in hexadecimal, or as text again",
			keys: []string{"F4:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.Search", area: "Shell", label: "Quick View: Search",
			desc: "Search for text in the quick view",
			keys: []string{"F7:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.SearchNext", area: "Shell", label: "Quick View: Search Again",
			desc: "Find the next occurrence of the last search in the quick view",
			keys: []string{"ShiftF7:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.NextCodepage", area: "Shell", label: "Quick View: Next Code Page",
			desc: "Switch the quick view to the next code page",
			keys: []string{"F8:QuickViewFocused"}, condition: "QuickViewFocused"},
		{name: "QuickView.Codepage", area: "Shell", label: "Quick View: Choose Code Page",
			desc: "Choose the code page of the quick view",
			keys: []string{"ShiftF8:QuickViewFocused"}, condition: "QuickViewFocused"},

		// The AI chat and the AI panel.
		{name: "AI.CopyLastResponse", area: "Shell", label: "Copy Last AI Response",
			desc: "Copy the last answer of the AI to the clipboard",
			keys: []string{"RCtrlC:AIChatFocused"}, condition: "AIChatFocused"},
		{name: "AI.ReviewPatch", area: "Shell", label: "Review AP Patch",
			desc: "Open the patch the AI proposed, to read it before applying it",
			keys: []string{"F3:AIChatFocused"}, condition: "AIChatFocused",
			handler: withPanels(aiReviewPatch)},
		{name: "AI.ViewContext", area: "Shell", label: "AI View: Context",
			desc: "Switch AI panel to context view",
			keys: []string{"Ctrl1:AIPanel"}, condition: "AIPanel"},
		{name: "AI.ViewChat", area: "Shell", label: "AI View: Chat",
			desc: "Switch AI panel to chat view",
			keys: []string{"Ctrl2:AIPanel"}, condition: "AIPanel"},
		{name: "AI.ViewOut", area: "Shell", label: "AI View: Artifacts",
			desc: "Switch AI panel to artifacts view",
			keys: []string{"Ctrl3:AIPanel"}, condition: "AIPanel"},
		{name: "AI.ViewMem", area: "Shell", label: "AI View: Memory",
			desc: "Switch AI panel to memory view",
			keys: []string{"Ctrl4:AIPanel"}, condition: "AIPanel"},

		// The player (Ctrl+Shift+M). Which of its letter keys work depends
		// on the scheme picked in the settings (WinAmp, YouTube or both), so
		// the palette and macros run its commands instead of pressing a key
		// that may be off.
		{name: "Panel.Player.Previous", area: "Shell", label: "Player: Previous Track",
			desc: "Play the previous track of the playlist",
			keys: []string{"Z:PlayerFocused", "ShiftP:PlayerFocused"}, condition: "PlayerFocused",
			handler: playerCommand(panel.PlayerPrevious)},
		{name: "Panel.Player.Play", area: "Shell", label: "Player: Play",
			desc: "Start playing",
			keys: []string{"X:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerPlay)},
		{name: "Panel.Player.Pause", area: "Shell", label: "Player: Pause",
			desc: "Pause playing, or go on playing",
			keys: []string{"C:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerPause)},
		{name: "Panel.Player.Stop", area: "Shell", label: "Player: Stop",
			desc: "Stop playing",
			keys: []string{"V:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerStop)},
		{name: "Panel.Player.Next", area: "Shell", label: "Player: Next Track",
			desc: "Play the next track of the playlist",
			keys: []string{"B:PlayerFocused", "ShiftN:PlayerFocused"}, condition: "PlayerFocused",
			handler: playerCommand(panel.PlayerNext)},
		{name: "Panel.Player.VolumeUp", area: "Shell", label: "Player: Volume Up",
			desc: "Make the player louder",
			keys: []string{"VK_BB:PlayerFocused"}, aliases: []string{"ShiftVK_BB:PlayerFocused", "Add:PlayerFocused"},
			condition: "PlayerFocused", handler: playerCommand(panel.PlayerVolumeUp)},
		{name: "Panel.Player.VolumeDown", area: "Shell", label: "Player: Volume Down",
			desc: "Make the player quieter",
			keys: []string{"VK_BD:PlayerFocused"}, aliases: []string{"ShiftVK_BD:PlayerFocused", "Subtract:PlayerFocused"},
			condition: "PlayerFocused", handler: playerCommand(panel.PlayerVolumeDown)},
		{name: "Panel.Player.Mute", area: "Shell", label: "Player: Mute",
			desc: "Turn the sound of the player off, or back on",
			keys: []string{"M:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerMute)},
		{name: "Panel.Player.SeekBack10", area: "Shell", label: "Player: Back 10 Seconds",
			desc: "Move the track ten seconds back",
			keys: []string{"J:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerSeekBack10)},
		{name: "Panel.Player.SeekForward10", area: "Shell", label: "Player: Forward 10 Seconds",
			desc: "Move the track ten seconds forward",
			keys: []string{"L:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerSeekForward10)},
		{name: "Panel.Player.JumpToStart", area: "Shell", label: "Player: Back to the Start",
			desc: "Play the track from its start",
			keys: []string{"0:PlayerFocused"}, condition: "PlayerFocused", handler: playerJump(0)},
		// 1 to 9 go to that tenth of the track, which a palette entry cannot
		// ask for, so it is not offered there. Run on its own (a macro) it
		// goes to the first tenth, like its other commands without a key.
		{name: "Panel.Player.JumpToTenth", area: "Shell", label: "Player: Jump to a Tenth of the Track",
			desc: "Move the track to that tenth of its length: 1 to a tenth, 5 to the middle, 9 near the end",
			keys: []string{"1:PlayerFocused", "2:PlayerFocused", "3:PlayerFocused", "4:PlayerFocused", "5:PlayerFocused",
				"6:PlayerFocused", "7:PlayerFocused", "8:PlayerFocused", "9:PlayerFocused"},
			condition: "PlayerFocused", hidden: true, handler: playerJump(1)},
		{name: "Panel.Player.Activate", area: "Shell", label: "Player: Play Item or Open Folder",
			desc: "Play the playlist item under the cursor, open or close a playlist folder, or press the button under the cursor",
			keys: []string{"Enter:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.TogglePause", area: "Shell", label: "Player: Play or Pause",
			desc: "Pause or go on playing, or play the item under the cursor when nothing plays",
			keys: []string{"Space:PlayerFocused", "K:PlayerFocused"}, condition: "PlayerFocused",
			handler: playerCommand(panel.PlayerPlayPause)},
		{name: "Panel.Player.RemoveItem", area: "Shell", label: "Player: Remove from Playlist",
			desc: "Remove the item under the cursor from the playlist",
			keys: []string{"Del:PlayerFocused", "F8:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.ToggleKeyHints", area: "Shell", label: "Player: Show Keys",
			desc: "Show the keys of the player on its buttons and in its bottom border, or hide them",
			keys: []string{"H:PlayerFocused"}, condition: "PlayerFocused", handler: playerCommand(panel.PlayerToggleKeyHints)},
		{name: "Panel.Player.NewFolder", area: "Shell", label: "Player: New Playlist Folder",
			desc: "Create a folder in the playlist",
			keys: []string{"F7:PlayerFocused"}, condition: "PlayerFocused"},
		// Shift with the arrows works everywhere; Ctrl with them, which the
		// player had first, never reaches f4 on macOS (Mission Control).
		{name: "Panel.Player.MoveItemUp", area: "Shell", label: "Player: Move Item Up",
			desc: "Move the playlist item under the cursor one place up",
			keys: []string{"ShiftUp:PlayerFocused"}, aliases: []string{"CtrlUp:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.MoveItemDown", area: "Shell", label: "Player: Move Item Down",
			desc: "Move the playlist item under the cursor one place down",
			keys: []string{"ShiftDown:PlayerFocused"}, aliases: []string{"CtrlDown:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.MoveOut", area: "Shell", label: "Player: Move out of Folder",
			desc: "Move the playlist item under the cursor out of its folder",
			keys: []string{"ShiftLeft:PlayerFocused"}, aliases: []string{"CtrlLeft:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.MoveInto", area: "Shell", label: "Player: Move into Folder",
			desc: "Move the playlist item under the cursor into the folder above it",
			keys: []string{"ShiftRight:PlayerFocused"}, aliases: []string{"CtrlRight:PlayerFocused"}, condition: "PlayerFocused"},
		{name: "Panel.Player.Controls", area: "Shell", label: "Player: Go to Buttons",
			desc: "Move the cursor from the playlist to the buttons of the player",
			keys: []string{"CtrlHome:PlayerFocused"}, condition: "PlayerFocused"},

		// The key router (macroFilter).
		{name: "Macro.Record", area: "Common", label: "Record Macro",
			desc: "Start recording a macro, or stop and assign it to a key",
			// The key router takes "." with Ctrl held, whatever else is.
			keys: []string{"CtrlVK_BE"}, aliases: []string{"CtrlShiftVK_BE", "CtrlAltVK_BE", "CtrlAltShiftVK_BE"},
			handler: func() bool {
				return macro.MacroMgr != nil && macro.MacroMgr.ToggleRecording(macroCurrentArea())
			}},

		// The drive menu (Alt+F1, Alt+F2).
		{name: "DriveMenu.AddLink", area: "Disks", label: "Add Link",
			desc: "Add a named link to the drive menu",
			keys: []string{"Ins", "CtrlN"}, handler: pressInDriveMenu("Ins")},
		{name: "DriveMenu.EditItem", area: "Disks", label: "Edit",
			desc: "Edit the link or folder bookmark under the cursor, or the hot key of a tool",
			keys: []string{"F4"}, handler: pressInDriveMenu("F4")},
		{name: "DriveMenu.DeleteItem", area: "Disks", label: "Delete",
			desc: "Delete the link or folder bookmark under the cursor, or unmount the drive",
			keys: []string{"Del"}, handler: pressInDriveMenu("Del")},
		{name: "DriveMenu.MoveUp", area: "Disks", label: "Move Up",
			desc: "Move the link, folder bookmark or tool under the cursor one place up",
			keys: []string{"ShiftUp"}, handler: pressInDriveMenu("ShiftUp")},
		{name: "DriveMenu.MoveDown", area: "Disks", label: "Move Down",
			desc: "Move the link, folder bookmark or tool under the cursor one place down",
			keys: []string{"ShiftDown"}, handler: pressInDriveMenu("ShiftDown")},
		{name: "DriveMenu.Settings", area: "Disks", label: "Drive Menu Settings",
			desc: "Open the settings of the drive menu",
			keys: []string{"F9"}, handler: pressInDriveMenu("F9")},
		{name: "DriveMenu.Root", area: "Disks", label: "Root Folder",
			desc: "Go to the root folder",
			keys: []string{"VK_BF"}, handler: pressInDriveMenu("VK_BF")},
		{name: "DriveMenu.Home", area: "Disks", label: "Home Folder",
			desc: "Go to the home folder",
			keys: []string{"VK_C0"}, handler: pressInDriveMenu("VK_C0")},
	}

	// The numbered folder bookmarks: far2l's [RightCtrl | Ctrl+Alt] and a
	// digit, with Shift to store.
	for slot := 0; slot <= 9; slot++ {
		n := slot
		actions = append(actions,
			nativeKeyAction{name: fmt.Sprintf("Panel.Bookmark.Go%d", n), area: "Shell",
				label: fmt.Sprintf("Go to Folder Bookmark %d", n),
				desc:  fmt.Sprintf("Go to the folder stored in bookmark %d", n),
				keys:  []string{fmt.Sprintf("RCtrl%d", n), fmt.Sprintf("CtrlAlt%d", n)},
				// A key bound on purpose takes over (ConfigurableHotkeyOwnsPanelBookmark).
				bindingWins: true,
				handler: withPanels(func(pf *panel.PanelsFrame) bool {
					return pf.GoToBookmarkSlot(n)
				})},
			nativeKeyAction{name: fmt.Sprintf("Panel.Bookmark.Save%d", n), area: "Shell",
				label:       fmt.Sprintf("Save Folder Bookmark %d", n),
				desc:        fmt.Sprintf("Store the folder of the active panel in bookmark %d", n),
				keys:        []string{fmt.Sprintf("RCtrlShift%d", n), fmt.Sprintf("CtrlAltShift%d", n)},
				bindingWins: true,
				handler: withPanels(func(pf *panel.PanelsFrame) bool {
					return pf.SaveBookmarkSlot(n)
				})},
			nativeKeyAction{name: fmt.Sprintf("TempPanel.List%d", n), area: "Shell",
				label:     fmt.Sprintf("Temporary Panel: List %d", n),
				desc:      fmt.Sprintf("Switch the temporary panel to its saved list %d", n),
				keys:      []string{fmt.Sprintf("AltShift%d:TempPanel", n)},
				condition: "TempPanel"},
		)
	}
	// vtui switches to the workspace numbered as the digit with Alt.
	for number := 1; number <= 9; number++ {
		actions = append(actions, nativeKeyAction{
			name: fmt.Sprintf("Workspace.Go%d", number), area: "Common",
			label:       fmt.Sprintf("Workspace %d", number),
			desc:        fmt.Sprintf("Switch to workspace number %d", number),
			keys:        []string{fmt.Sprintf("Alt%d:WorkspaceAltNumbers", number)},
			condition:   "WorkspaceAltNumbers",
			bindingWins: true,
			handler:     switchToWorkspaceNumber(number),
		})
	}
	return actions
}

// aiReviewPatch opens the patch the AI proposed in the viewer, which F3 does
// on the AI chat's patch bar.
func aiReviewPatch(pf *panel.PanelsFrame) bool {
	if pf.ActiveIdx < 0 || pf.ActiveIdx >= len(pf.AltPanels) {
		return false
	}
	chat, ok := pf.AltPanels[pf.ActiveIdx].(*AIChatPanel)
	if !ok {
		return false
	}
	chat.navigateToTarget("ai://out/afix.ap")
	return true
}
