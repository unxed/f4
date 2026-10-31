package app

import (
	"fmt"
	"github.com/unxed/f4/internal/panel"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The key router. It asks the macro engine first, which is why it used to live
// with it, but everything it asks afterwards — the hotkey manager, the action
// registry, the plugin actions, the panels — is the application's. The engine
// keeps the recording and playback; the dispatching is here.

func macroCurrentArea() string {
	if vtui.FrameManager == nil {
		return "Common"
	}
	top := vtui.FrameManager.GetTopFrame()
	if top == nil {
		return "Common"
	}
	if menuBarRaisedOver(top) {
		// F9 over the panels raises the bar with no dropdown, so the panels
		// stay on top. The keyboard is the bar's all the same, and a dropdown
		// would have made this area "Menu"; without that, Shell's own
		// bindings ran first and the bar never saw the key: Esc toggled the
		// panels and F10 asked to quit while the bar stayed up (#1144).
		return "Menu"
	}
	switch top.GetType() {
	case vtui.TypeDialog:
		return "Dialog"
	case vtui.TypeMenu:
		if menu, ok := top.(*vtui.VMenu); ok {
			if strings.Contains(menu.GetTitle(), "Drive") {
				return "Disks"
			}
		}
		return "Menu"
	case vtui.TypeUser + 1:
		if pf, ok := top.(*panel.PanelsFrame); ok {
			if !pf.ShowPanels {
				return "Terminal"
			}
		}
		return "Shell"
	case vtui.TypeUser + 2:
		return "Editor"
	case vtui.TypeUser + 3:
		return "Viewer"
	}
	return "Other"
}

// pluginPanelStandsDown reports whether the hotkey that resolved to
// actionName must yield to a panel plugin owning the keyboard in the panels
// frame on top (panel.PanelsFrame.PluginPanelStandsDown).
func pluginPanelStandsDown(actionName string) bool {
	if vtui.FrameManager == nil {
		return false
	}
	pf, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame)
	return ok && pf.PluginPanelStandsDown(actionName)
}

// menuBarRaisedOver reports a main menu bar that is up without a dropdown over
// top, the frame that shows it. vtui's frame manager hands such a bar every key
// the EventFilter lets through (the menu interception in dispatchEvent, under
// the same condition), so for the key router the bar is on top, not the frame.
func menuBarRaisedOver(top vtui.Frame) bool {
	if top == nil || top.GetType() == vtui.TypeMenu || vtui.FrameManager == nil {
		return false
	}
	menu := vtui.FrameManager.GetActiveMenuBar()
	if menu == nil || !menu.Active {
		return false
	}
	return !top.IsModal() || top.GetMenuBar() == menu
}

// panelsWithFastFind is the panels frame on top while the quick search or the
// autofilter of its active panel is open, or nil.
func panelsWithFastFind() *panel.PanelsFrame {
	if vtui.FrameManager == nil {
		return nil
	}
	pf, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame)
	if !ok || !pf.ShowPanels {
		return nil
	}
	if fsp := pf.GetActivePanel(); fsp == nil || !fsp.FastFindMode {
		return nil
	}
	return pf
}

// hotkeyKeepsFastFind reports whether e runs an action that leaves an open
// quick search open (actionKeepsFastFind), so the router must not close the
// search before the action runs.
func hotkeyKeepsFastFind(area string, e *vtinput.InputEvent) bool {
	hm := keymap.GlobalHotkeysMgr
	if hm == nil {
		return false
	}
	return actionKeepsFastFind(keymap.ConfiguredHotkeyAction(hm, area, keymap.EventToHotkeyString(e)))
}

func macroCommandLine() string {
	area := macroCurrentArea()
	if area != "Shell" && area != "Terminal" {
		return ""
	}
	pf := panel.FindPanelsFrameAnyScreen()
	if pf == nil || pf.CmdLine == nil {
		return ""
	}
	return pf.CmdLine.Edit.GetText()
}

// macroCommandLineOwnsDeletion reports whether e is a plain Backspace or Delete
// that the command line of the panels takes because it holds text.
func macroCommandLineOwnsDeletion(area string, e *vtinput.InputEvent) bool {
	if area != "Shell" && area != "Terminal" {
		return false
	}
	pf := panel.FindPanelsFrameAnyScreen()
	return pf != nil && pf.CommandLineOwnsDeletion(e)
}

// macroPasting is true between the marks of a bracketed paste.
var macroPasting bool

func macroFilter(m *macro.MacroManager, e *vtinput.InputEvent) bool {
	if e.Type == vtinput.MouseEventType {
		if panel.PlayerIndicatorClick(e) {
			return true
		}
		return macroMouseWheel(m, e)
	}
	if e.Type == vtinput.PasteEventType {
		macroPasting = e.PasteStart
		return false
	}
	if e.Type != vtinput.KeyEventType {
		return false
	}

	// The keys of a paste are text: no key remap, no macro and no command
	// palette chord applies to them (a macro on the letter "a" must not run
	// for every "a" of a pasted text), and looking them up for each of a
	// million keys took a third of the time of a paste of 1 MiB into the
	// editor (unxed/f4#2194). A recording still sees them.
	if macroPasting && !m.Recording && !m.Assigning {
		return false
	}

	// Wayland reports the translated keysym for the active layout. Restore the
	// physical Latin VK before remaps and native handlers inspect the event, so
	// Ctrl+N remains Ctrl+N when the Russian layout produces Ctrl+т.
	keymap.NormalizeLayoutShortcut(e)

	// A field that captures a key -- the global hot key of a drive-menu link
	// (f4#2171) -- gets every key before anything here acts on it: Ctrl+.
	// would start a macro recording, Ctrl+Shift+P open the palette, a macro
	// or a hotkey run, and vtui would switch workspaces on Alt and a digit
	// after this filter. The frame it sits in gets the key, so the keys the
	// field leaves alone (Tab, Enter, Esc) still move the focus or close it.
	// The key is the one pressed, before keymap.ini: the link dialog says when
	// keymap.ini would turn it into another.
	if vtui.FrameManager != nil {
		if top := vtui.FrameManager.GetTopFrame(); top != nil && keymap.FocusCapturesKeys(top) {
			top.ProcessKey(e)
			return true
		}
	}

	// The user's key remap (keymap.ini) substitutes the key before anything
	// else sees the event, so macros, plugin interception, configurable
	// hotkeys and the frames themselves all agree on which key was pressed.
	if !keymap.ApplyKeyRemap(macroCurrentArea(), e) {
		// The built-in Mac layout only sees what keymap.ini left behind, so
		// a rule the user wrote by hand still wins over it.
		keymap.ApplyMacKeys(macroCurrentArea(), e)
	}

	// Ctrl+. toggles recording. We check both VK and Char for better terminal compatibility.
	isCtrlDot := (e.VirtualKeyCode == vtinput.VK_OEM_PERIOD || e.Char == '.') &&
		(e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0

	if isCtrlDot {
		if !e.KeyDown {
			return true // Consume KeyUp of the trigger
		}
		m.ToggleRecording(macroCurrentArea())
		return true // Trigger is ALWAYS consumed
	}

	// The command palette is the application-wide escape hatch for finding
	// commands. Resolve it before recording/assignment so the opening chord is
	// neither captured in the macro buffer nor shadowed by macro playback.
	if e.KeyDown {
		currentArea := macroCurrentArea()
		hotkeyStr := keymap.EventToHotkeyString(e)
		if hm := keymap.GlobalHotkeysMgr; hm != nil {
			if actionName := keymap.ConfiguredHotkeyAction(hm, currentArea, hotkeyStr); strings.EqualFold(actionName, commandPaletteActionName) {
				RunAction(actionName)
				return true
			}
		}
		if commandPaletteLegacyShortcut(currentArea, e) {
			RunAction(commandPaletteActionName)
			return true
		}
	}

	// Once the palette is open, its remaining query and navigation keys belong
	// to the dialog. In particular, do not record them into a macro that the
	// user may be stopping from the palette itself. The palette chord itself was
	// handled above, so pressing it again is still consumed without adding a
	// second dialog.
	if vtui.FrameManager != nil {
		if _, open := vtui.FrameManager.GetTopFrame().(*commandPaletteDialog); open {
			return false
		}
	}

	if m.Recording || m.Assigning {
		if e.KeyDown && m.Recording {
			switch e.VirtualKeyCode {
			case vtinput.VK_SHIFT, vtinput.VK_LSHIFT, vtinput.VK_RSHIFT,
				vtinput.VK_CONTROL, vtinput.VK_LCONTROL, vtinput.VK_RCONTROL,
				vtinput.VK_MENU, vtinput.VK_LMENU, vtinput.VK_RMENU,
				vtinput.VK_CAPITAL, vtinput.VK_NUMLOCK, vtinput.VK_SCROLL:
				// Ignore standalone modifier keys
			default:
				m.Buffer = append(m.Buffer, e)
			}
		}
		return false // Let it pass to the UI so user sees what they type / dialog catches it
	}

	if !e.KeyDown {
		return false
	}

	currentArea := macroCurrentArea()
	if pf := panelsWithFastFind(); currentArea == "Shell" && pf != nil {
		// The open quick search or autofilter has the first say on a key
		// (panel's fastfind_keys.go, #2186). The keys it keeps go straight to
		// the panels frame: no macro or hotkey may take a character, Esc or
		// Del from it. Any other key closes it and then goes on as if no
		// search had been open, unless the key runs an action that leaves
		// the search open.
		if !pf.FastFindClosesOn(e) {
			return false
		}
		if !hotkeyKeepsFastFind(currentArea, e) && pf.CancelFastFind() {
			vtui.FrameManager.Redraw()
		}
	}
	if currentArea == "Shell" &&
		keymap.IsPanelBookmarkHotkey(e) && !keymap.ConfigurableHotkeyOwnsPanelBookmark(keymap.GlobalHotkeysMgr, currentArea, e) {
		return false
	}

	// A drive-menu link's global hot key (f4#2171) opens the link ahead of
	// the macros, the plugin keys and the hotkey table: the link dialog asked
	// whether to take the key from them when the key was given to the link
	// (driveLinkRouterKeyOwners names what comes before this point).
	if currentArea == "Shell" {
		if pf, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame); ok && pf.OpenDriveLinkForKey(e) {
			return true
		}
	}

	// Check if this key triggers a macro
	keyStr := keymap.EventToFarString(e)

	// Backspace and Delete edit a command line that holds text before any
	// remapped hotkey, and a recorded macro on the same key is no exception:
	// a user who bound Backspace to "up one folder" for an empty command line
	// otherwise lost it for editing as soon as the macro was played back
	// from the key (f4#1797).
	commandLineEdits := macroCommandLineOwnsDeletion(currentArea, e)

	if areaMacros, ok := m.Macros[currentArea]; ok && !commandLineEdits {
		if seq, ok := areaMacros[keyStr]; ok {
			vtui.DebugLog("MACRO: Playing back macro for %s in area %s", keyStr, currentArea)
			vtui.FrameManager.InjectEvents(seq)
			return true
		}
	}

	if commonMacros, ok := m.Macros["Common"]; ok && !commandLineEdits {
		if seq, ok := commonMacros[keyStr]; ok {
			vtui.DebugLog("MACRO: Playing back macro for %s in area Common", keyStr)
			vtui.FrameManager.InjectEvents(seq)
			return true
		}
	}

	// A macro that hit its deadline left the interpreter unusable; build a new
	// one before offering it this key.
	m.RefreshInterruptedLua()

	// Recorded macros win over scripted ones, as they do in Far.
	if m.Lua != nil && m.Lua.TriggerWithCommandLine(currentArea, e, macroCommandLine()) {
		vtui.DebugLog("MACRO: Running Lua macro for %s in area %s", keyStr, currentArea)
		return true
	}

	// Plugin key interception: global plugin hotkeys and the active
	// panel's PanelController may override built-in hotkeys, so they
	// are consulted before the hotkey manager.
	// Both this and the veto below belong to the frame on top, which has
	// lost the keyboard to its own menu bar while that bar is raised.
	if vtui.FrameManager != nil {
		if top := vtui.FrameManager.GetTopFrame(); top != nil && !menuBarRaisedOver(top) {
			if pi, ok := top.(interface {
				InterceptPluginKey(*vtinput.InputEvent) bool
			}); ok && pi.InterceptPluginKey(e) {
				return true
			}
		}
	}

	// A frame in a modal input state (e.g. editor autocomplete, panel
	// fast-find) may veto system hotkey dispatch: the key then falls
	// through to the frame's own ProcessKey, which handles such states
	// before anything else.
	if vtui.FrameManager != nil {
		if top := vtui.FrameManager.GetTopFrame(); top != nil && !menuBarRaisedOver(top) {
			if v, ok := top.(interface {
				VetoActionKey(*vtinput.InputEvent) bool
			}); ok && v.VetoActionKey(e) {
				return false
			}
		}
	}

	// Hotkey Manager evaluation (System actions)
	if hm := keymap.GlobalHotkeysMgr; hm != nil {
		keyStr := keymap.EventToHotkeyString(e)
		if actionName := keymap.ConfiguredHotkeyAction(hm, currentArea, keyStr); actionName != "" {
			// F4-assigned plugin shortcuts are menu accelerators, matching
			// far2l: the F11 plugin menu renders them as ampersand hotkeys.
			// They must not steal printable characters from the panel command
			// line while remaining persisted for that menu and its display.
			// A chord assigned in the hotkey dialog (Ctrl+F9 and friends) is a
			// real hotkey and keeps being dispatched here.
			if isMenuAcceleratorAction(actionName, keyStr) {
				return false
			}
			if strings.EqualFold(actionName, "none") {
				return true // Intercept and silence (explicitly unbound)
			}
			// A panel plugin owns the keyboard and this binding acts on the
			// file panel hidden under it: hand the key to the frame, which
			// routes it to the plugin's ProcessKey (vfs.PanelKeyProvider).
			if pluginPanelStandsDown(actionName) {
				return false
			}
			vtui.DebugLog("HOTKEY: Executing action %s for %s in area %s", actionName, keyStr, currentArea)
			// A configured binding owns the event even when its action cannot
			// currently run. Letting the same key fall through to the frame
			// dispatcher can trigger an unrelated command and leave a stale
			// overlay behind (for example, when the selected panel entry is
			// the parent directory and an archive command has no target).
			RunAction(actionName)
			return true
		}
	}

	// A held Ctrl+W auto-repeats while the exit confirmation dialog is up and
	// the repeats pile up in the event channel. They must die here: the
	// close-active-screen fallback below this filter would re-emit CmQuit and
	// stack another copy of the dialog (one Cancel per queued repeat), and
	// with several workspaces it would close one behind the modal dialog.
	if e.VirtualKeyCode == vtinput.VK_W &&
		(e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed)) != 0 &&
		panel.QuitConfirmationOpen() {
		return true
	}

	return false
}

// ToggleRecording is shared by the native Ctrl+. handler and the command
// palette. Stopping remains deferred so the assignment dialog is pushed only
// after the current input dispatch (or palette close) has completed.
// macroMouseWheel plays the recorded macro bound to a turn of the mouse
// wheel, MsWheelUp or MsWheelDown with the modifiers held (f4#1870). While a
// macro is recorded or assigned the wheel scrolls as usual.
func macroMouseWheel(m *macro.MacroManager, e *vtinput.InputEvent) bool {
	key := keymap.MouseWheelToFarString(e)
	if key == "" || m.Recording || m.Assigning {
		return false
	}
	for _, area := range []string{macroCurrentArea(), "Common"} {
		if seq, ok := m.Macros[area][key]; ok {
			vtui.DebugLog("MACRO: Playing back macro for %s in area %s", key, area)
			vtui.FrameManager.InjectEvents(seq)
			return true
		}
	}
	return false
}

func macroLookupHotkey(m *macro.MacroManager, e *vtinput.InputEvent) bool {
	if m == nil || e == nil || e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	hm := keymap.GlobalHotkeysMgr
	if hm == nil {
		return false
	}
	keyStr := keymap.EventToHotkeyString(e)
	if keyStr == "" {
		return false
	}
	area := macroCurrentArea()
	actionName := keymap.ConfiguredHotkeyAction(hm, area, keyStr)
	if actionName == "" {
		return false
	}
	// Plugin menu accelerators are activated by the F11 menu's ampersand
	// hotkeys. They are deliberately not global Shell hotkeys, so an injected
	// key (for example from a key-bar click) must not bypass that rule either.
	if isMenuAcceleratorAction(actionName, keyStr) {
		return false
	}
	if strings.EqualFold(actionName, "none") {
		return true
	}
	// Same stand-down as in Filter: an injected key (a keybar click) must
	// not reach a file action the plugin panel has hidden either.
	if pluginPanelStandsDown(actionName) {
		return false
	}
	vtui.DebugLog("HOTKEY: Injected %s → action %s in area %s", keyStr, actionName, area)
	// The injected path has the same ownership rule as Filter: once a
	// configured action matched, its result must not expose the key to the
	// frame below it.
	RunAction(actionName)
	return true
}

// driveLinkRouterKeyOwners is the application's part of what a drive-menu
// link's global hot key would take its key from (panel.DriveLinkExtraKeyOwners,
// f4#2171): what macroFilter does with a key before it asks the links, and the
// macros it would no longer play in the panels.
func driveLinkRouterKeyOwners(m *macro.MacroManager, key string) []panel.DriveLinkKeyOwner {
	var owners []panel.DriveLinkKeyOwner
	const ctrl = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed
	// Ctrl+. starts and stops a macro recording before anything else.
	if e := keymap.ParseFarKey(key); e != nil && e.ControlKeyState&ctrl != 0 &&
		(e.VirtualKeyCode == vtinput.VK_OEM_PERIOD || e.Char == '.') {
		owners = append(owners, panel.DriveLinkKeyOwner{Name: i18n.Msg("DriveLink.OwnerMacroRecording"), Fixed: true})
	}
	// The command palette's own Ctrl+Alt+P, while no command has the key.
	// "None" in Shell is a binding, so the fallback stands down there.
	if hm := keymap.GlobalHotkeysMgr; hm != nil && key == commandPaletteLegacyKey &&
		keymap.ConfiguredHotkeyAction(hm, "Shell", key) == "" {
		name := commandPaletteActionName
		if a, ok := keymap.LookupAction(name); ok {
			name = action.PlainLabel(a.DisplayLabel())
		}
		owners = append(owners, panel.DriveLinkKeyOwner{
			Name: fmt.Sprintf(i18n.Msg("DriveLink.OwnerCommand"), name),
			Release: func() error {
				hm.Bind("Shell", key, "None")
				return hm.SaveError()
			},
		})
	}
	// A macro plays on either Ctrl: its key is kept in Far's spelling, which
	// has no right Ctrl (keymap.EventToFarString). The links are asked before
	// the macros, so an RCtrl link takes the right Ctrl from a macro on Ctrl;
	// the macro keeps the left one.
	macroKey := key
	if strings.HasPrefix(key, "RCtrl") {
		macroKey = "Ctrl" + key[len("RCtrl"):]
	}
	if m.PlaysOn("Shell", macroKey) {
		owners = append(owners, panel.DriveLinkKeyOwner{Name: i18n.Msg("DriveLink.OwnerMacro")})
	}
	return owners
}

func isMenuAcceleratorAction(actionName, key string) bool {
	return (keymap.IsPluginActionName(actionName) || keymap.IsDriveMenuActionName(actionName)) &&
		panel.IsPluginMenuHotkey(key)
}
