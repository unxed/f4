package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
)

// The key inventory: every key f4 reacts to is known to the Hotkey
// Configurator. A configurable key is a binding there; a key some frame
// handles itself is a NativeKeys entry of an action, shown in the configurator
// as a read-only row. What neither declares is a key the user cannot see, and
// a feature that picks a key for itself, a drive-menu link's global hot key
// above all, cannot know it is taken (f4#2171).
//
// The test cannot prove from the source which chord a function reacts to, so
// it works the way the command-palette audit does: it takes an exact AST
// inventory of the keys each production function compares an event against,
// and every function in that inventory needs an explicit audit. An audited
// function lists the chords it handles, each of which must be declared in the
// action registry for the areas the function works in, and the keys it only
// uses as widget navigation. The keys of the two together must be exactly the
// keys the source compares against, so a key added to a function, or taken
// out of it, fails the test until the audit says what it does.
//
// Functions not audited yet are frozen in tools/key_inventory_baseline.txt.
// That list may only shrink: a function in it may lose keys or disappear,
// never gain a key, and a function that is not in it must be audited.

const keyInventoryBaselinePath = "tools/key_inventory_baseline.txt"

const keyInventoryUpdateHint = "Regenerate the baseline with:\n" +
	"    F4_UPDATE_KEY_INVENTORY_BASELINE=1 go test -run TestKeyInventory ./cmd/f4\n" +
	"It only drops what the source no longer has; a new key or function goes into keyInventoryAudit instead."

// keyInventoryEntry is the audit of one function.
type keyInventoryEntry struct {
	// areas are the Hotkey Configurator areas the function's keys act in.
	// A chord counts as declared when an action of one of these areas, or
	// of Common, lists it in DefaultKeys or NativeKeys.
	areas []string
	// chords are the key combinations the function handles itself, in the
	// spelling of keymap.EventToHotkeyString, with an optional ":Condition"
	// the way the registry writes them.
	chords []string
	// nav are keys the function only uses to move inside its own widget, or
	// that it inspects without acting on them (a modifier key's own event, a
	// key it passes on): the names of the vtinput.VK_ constants without the
	// prefix ("UP", "RETURN").
	nav []string
	// infrastructure marks a function whose keys are data rather than
	// shortcuts: it translates or names keys for everyone (keymap), turns
	// another input into a keystroke (a mouse click, a paste, a semantic
	// GUI request), or reads a key's modifiers. Its keys are not compared
	// with the audit.
	infrastructure bool
	// rationale says why the function handles keys and what the nav keys
	// are for.
	rationale string
}

// keyInventoryAudit lists the audited functions, by the identity the
// command-palette audit uses: package, then receiver and name.
var keyInventoryAudit = map[string]keyInventoryEntry{
	// The panels: everything the panels frame does with a key itself.
	"panel.(*PanelsFrame).ProcessKey": {
		areas: []string{"Shell", "Terminal"},
		chords: append(keyInventoryBookmarkChords(),
			"RCtrlVK_C0", "CtrlAltVK_C0", "VK_C0", "AltShiftF3", "CtrlAltC", "CtrlC",
			"CtrlE", "CtrlX", "Up", "Down", "Esc", "Del", "F2", "F9", "CtrlJ",
			"CtrlLeft", "CtrlRight", "CtrlShiftLeft", "CtrlShiftRight", "CtrlN",
			"CtrlVK_DB", "CtrlVK_DC", "CtrlVK_DD",
			"Enter", "CtrlEnter", "CtrlShiftEnter", "ShiftEnter", "Tab", "CtrlTab", "CtrlShiftTab"),
		nav: []string{"LEFT", "RIGHT", "OEM_PLUS", "OEM_MINUS", "Char '*'"},
		rationale: "the panels frame: Enter, Tab, the numbered bookmarks, the command line's keys, the quick search, the temporary panel, " +
			"the terminal's workspace keys. Plain Left/Right edit a non-empty command line; '+', '-' and '*' are the characters of the gray " +
			"keys of Panel.SelectGroup/DeselectGroup/InvertSelection on terminals that send no key",
	},
	"panel.(*PanelsFrame).InterceptPluginKey": {
		areas:  []string{"Shell"},
		chords: []string{"CtrlAltA", "CtrlC", "Esc"},
		rationale: "keys ahead of the hotkey table: the Arkanoid easter egg, Ctrl+C of a remote command, Esc out of a plugin panel. " +
			"Plugin hotkeys and panel plugin keys are the plugins' own (plughost, vfs.PanelKeyProvider)",
	},
	"panel.(*PanelsFrame).VetoActionKey": {
		nav: []string{"F2", "LEFT", "RIGHT"},
		rationale: "routes keys past the hotkey table to the frame while the quick search, an alternative panel or the command line " +
			"owns them; what the keys do is audited where they are handled",
	},
	"panel.(*PlayerPanel).OwnsKey": {
		areas: []string{"Shell"},
		chords: []string{"Enter", "Del", "ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight",
			"CtrlUp", "CtrlDown", "CtrlLeft", "CtrlRight", "CtrlHome"},
		rationale: "the focused player takes its keys ahead of the hotkey table (Panel.Player.*)",
	},
	"panel.playerKeyCommand": {
		areas: []string{"Shell"},
		chords: []string{"H", "I", "Z", "X", "C", "V", "B", "K", "J", "L", "M", "ShiftP", "ShiftN",
			"VK_BB", "VK_BD", "Add", "Subtract"},
		rationale: "the player's WinAmp and YouTube letter keys, its volume, H, which shows its keys, and I, which closes it and leaves the music playing, wherever its cursor is (Panel.Player.*)",
	},
	"panel.playerDigit": {
		areas:     []string{"Shell"},
		chords:    []string{"0", "9"},
		rationale: "the player's digits, 0 to 9, jump to that tenth of the track (Panel.Player.JumpToStart, Panel.Player.JumpToTenth); the source compares the two ends of the range",
	},
	"panel.playerLetterKey": {
		infrastructure: true, rationale: "tells the letter key of a keystroke whatever the layout, for the player's WinAmp keys",
	},
	"app.(*AIChatPanel).OwnsKey": {
		areas:     []string{"Shell"},
		chords:    []string{"RCtrlC", "CtrlZ"},
		nav:       []string{"BACK", "DELETE", "RETURN"},
		rationale: "the focused AI chat takes its keys ahead of the hotkey table; Backspace, Delete and Enter edit and send its input line",
	},
	"panel.(*PanelsFrame).autoplayPlayerForPanel": {
		nav:       []string{"DOWN", "LEFT", "RIGHT", "UP"},
		rationale: "follows the cursor of the file panel with the player",
	},
	"panel.(*PanelsFrame).commandLineOwnsDeletion": {
		nav:       []string{"BACK", "DELETE"},
		rationale: "plain Backspace and Delete edit a non-empty command line",
	},
	"panel.(*PanelsFrame).commandLineOwnsSelection": {
		areas:     []string{"Shell"},
		chords:    []string{"CtrlShiftLeft", "CtrlShiftRight"},
		rationale: "Ctrl+Shift+Left/Right select words of a non-empty command line (CommandLine.SelectWordLeft/Right)",
	},
	"panel.(*PanelsFrame).handleHostConsoleTab": {
		areas:     []string{"Terminal"},
		chords:    []string{"Tab"},
		rationale: "Tab completes the command line over a host console (CommandLine.Complete)",
	},
	"panel.(*PanelsFrame).pluginPanelKeyLabels": {
		nav:       []string{"F1", "F10", "F12"},
		rationale: "builds the key bar of a panel plugin from the keys it declares",
	},
	"panel.(*PanelsFrame).showDriveMenuAt": {
		areas:     []string{"Disks"},
		chords:    []string{"Del", "F4", "F9", "VK_BF", "VK_C0", "ShiftUp"},
		nav:       []string{"RETURN"},
		rationale: "the drive menu's own keys (DriveMenu.*); Enter is what picking a row sends to the menu",
	},
	"panel.(*FileSystemPanel).processKey": {
		areas: []string{"Shell"},
		chords: []string{"Enter", "Esc", "Del", "F2", "CtrlE", "CtrlEnter", "CtrlShiftEnter", "Ins",
			"CtrlPgUp", "CtrlPgDn", "ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight", "ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd"},
		nav:       []string{"BACK", "DOWN", "END", "HOME", "LEFT", "NEXT", "PRIOR", "RIGHT", "UP"},
		rationale: "the file panel: Enter opens, Ins and Shift with a cursor key mark, the quick search's keys; the plain cursor keys move, Backspace erases the quick search",
	},
	"panel.(*FileSystemPanel).fastFindKeeps": {
		nav: []string{"BACK", "DELETE", "DOWN", "E", "END", "ESCAPE", "F2", "HOME", "INSERT", "LEFT", "NEXT", "PRIOR", "RETURN", "RIGHT", "UP"},
		rationale: "routes to an open quick search or autofilter the keys it keeps ahead of every other owner (#2186); " +
			"what they do is audited in processKey",
	},
	"panel.(*FileSystemPanel).fastFindClosesOn": {
		nav: []string{"CAPITAL", "CONTROL", "LCONTROL", "LMENU", "LSHIFT", "LWIN", "MENU", "NUMLOCK",
			"RCONTROL", "RMENU", "RSHIFT", "RWIN", "SCROLL", "SHIFT"},
		rationale: "any key an open quick search does not keep closes it, except a modifier key's own event",
	},
	"panel.isFastFindClearKey": {
		areas:     []string{"Shell"},
		chords:    []string{"CtrlY:FastFind"},
		rationale: "Ctrl+Y erases the text of an open quick search or autofilter (Panel.FastFind.Clear)",
	},
	"panel.(*FileSystemPanel).groupNavigationTarget": {
		nav:       []string{"DOWN", "END", "HOME", "LEFT", "NEXT", "PRIOR", "RIGHT", "UP"},
		rationale: "where the cursor keys move to in a grouped panel",
	},
	"panel.(*InfoPanel).ProcessKey": {
		areas:     []string{"Shell"},
		chords:    []string{"C", "Ins", "ShiftUp", "ShiftDown", "ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd"},
		nav:       []string{"DOWN", "END", "HOME", "NEXT", "PRIOR", "UP"},
		rationale: "the info panel (Ctrl+L) when focused: C copies, Ins and Shift with a cursor key mark lines",
	},
	"panel.(*QuickViewPanel).ProcessKey": {
		areas: []string{"Shell"},
		chords: []string{"C", "Ins", "F2", "F3", "F4", "F7", "ShiftF7", "F8", "ShiftF8",
			"ShiftUp", "ShiftDown", "ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd"},
		nav:       []string{"DOWN", "END", "HOME", "LEFT", "NEXT", "PRIOR", "RIGHT", "UP"},
		rationale: "the quick view (Ctrl+Q) when focused (QuickView.*, Panel.AltPanelCopy)",
	},
	"panel.(*TreePanel).processKey": {
		areas:     []string{"Shell"},
		chords:    []string{"Enter"},
		nav:       []string{"DOWN", "LEFT", "RIGHT", "UP"},
		rationale: "the folder tree: Enter opens the folder; Left and Right fold and unfold",
	},
	"panel.(*PlayerPanel).controlKey": {
		areas:     []string{"Shell"},
		chords:    []string{"Left", "Right", "Enter", "Space", "F7"},
		nav:       []string{"DOWN", "END", "HOME", "NEXT", "UP"},
		rationale: "the player's button row: the arrows move between buttons, Enter and Space press one, F7 makes a playlist folder",
	},
	"panel.(*PlayerPanel).barKey": {
		areas:     []string{"Shell"},
		chords:    []string{"Left", "Right", "Enter", "Space", "F7"},
		nav:       []string{"DOWN", "NEXT", "PRIOR", "UP"},
		rationale: "the player's playback bar: the arrows seek, Enter and Space play or pause, F7 makes a playlist folder",
	},
	"panel.(*PlayerPanel).playlistKey": {
		areas: []string{"Shell"},
		chords: []string{"Left", "Right", "ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight",
			"CtrlUp", "CtrlDown", "CtrlHome", "CtrlLeft", "CtrlRight", "Enter", "Space", "Del", "F7", "F8"},
		nav:       []string{"DOWN", "END", "HOME", "NEXT", "PRIOR", "UP"},
		rationale: "the player's playlist (Panel.Player.*)",
	},
	// f4#2171, the global hot key of a drive-menu link.
	"panel.(*GlobalHotkeyEdit).ProcessKey": {
		nav: []string{"BACK", "CAPITAL", "CONTROL", "DELETE", "DOWN", "END", "ESCAPE", "HOME", "INSERT", "LCONTROL", "LEFT",
			"LMENU", "LSHIFT", "LWIN", "MENU", "NEXT", "NUMLOCK", "PRIOR", "RCONTROL", "RETURN", "RIGHT", "RMENU", "RSHIFT",
			"RWIN", "SCROLL", "SHIFT", "TAB", "UP"},
		rationale: "the field captures whatever key is pressed as a link's global hot key; it leaves Tab, Enter, Esc and the plain " +
			"vertical keys to the dialog, swallows the plain horizontal ones and Ins, clears on Del and Backspace, and skips lone modifiers",
	},
	"panel.(*GlobalHotkeyEdit).Show": {
		infrastructure: true, rationale: "a synthetic Home keeps the start of the shown key in view",
	},
	"panel.driveLinkBookmarkKeyName": {
		infrastructure: true, rationale: "names the folder bookmark key a link's global hot key would take",
	},
	"app.driveLinkRouterKeyOwners": {
		infrastructure: true, rationale: "names Ctrl+. (Macro.Record), which the key router handles before a link's global hot key",
	},
	"panel.isFilePanelOnlyKey": {
		nav: []string{"DELETE", "F3", "F4", "F5", "F6", "F7", "F8", "INSERT"},
		rationale: "the file keys no focused alternative panel without a file list (info, quick view, player, AI chat) lets " +
			"reach the file panel hidden behind it",
	},
	"panel.(*PluginPanelInstance).ProcessKey": {
		areas:     []string{"Shell"},
		chords:    []string{"Esc", "CtrlPgUp"},
		rationale: "a panel plugin closes on Esc and Ctrl+PgUp (Panel.ClosePluginPanel)",
	},
	"panel.isPluginPanelCloseKey": {
		areas:     []string{"Shell"},
		chords:    []string{"F10", "CtrlPgUp"},
		rationale: "the keys that close a panel plugin (Panel.ClosePluginPanel)",
	},
	"panel.(*DriveMenuFrame).ProcessKey": {
		nav:       []string{"ESCAPE", "F10"},
		rationale: "Esc and F10 close the drive menu, as they close every menu",
	},
	"panel.isAddItemKey": {
		areas:     []string{"Disks"},
		chords:    []string{"Ins", "CtrlN"},
		rationale: "Ins and Ctrl+N add a row to a list (DriveMenu.AddLink in the drive menu; the other lists are not audited yet)",
	},
	"panel.isCommandFocusToggleKey": {
		areas:     []string{"Shell"},
		chords:    []string{"VK_C0"},
		nav:       []string{"Char 'ё'"},
		rationale: "the grave key moves the focus in the search-first navigation (Panel.ToggleCommandLineFocus); ё is that key on a Russian layout",
	},
	"panel.isDriveMenuMoveKey": {
		areas:     []string{"Disks"},
		chords:    []string{"ShiftUp", "ShiftDown"},
		rationale: "Shift+Up/Down move a row of the drive menu (DriveMenu.MoveUp/Down)",
	},
	"panel.isShiftSelectNavKey": {
		areas:     []string{"Shell"},
		chords:    []string{"ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight", "ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd"},
		rationale: "Shift with a cursor key marks files (Panel.SelectNavigation)",
	},
	"panel.shiftSelectDirection": {
		areas:     []string{"Shell"},
		chords:    []string{"ShiftUp", "ShiftDown", "ShiftLeft", "ShiftRight", "ShiftPgUp", "ShiftPgDn", "ShiftHome", "ShiftEnd"},
		rationale: "which way a Shift-marking key goes (Panel.SelectNavigation)",
	},
	"panel.maskCharFallbackApplies": {
		areas:     []string{"Shell"},
		chords:    []string{"Add", "Subtract", "Multiply"},
		rationale: "the gray +, - and * select by mask (Panel.SelectGroup, DeselectGroup, InvertSelection)",
	},
	"panel.tempPanelSlotKey": {
		areas: []string{"Shell"},
		chords: []string{"AltShift0", "AltShift1", "AltShift2", "AltShift3", "AltShift4",
			"AltShift5", "AltShift6", "AltShift7", "AltShift8", "AltShift9"},
		nav:       []string{"NUMPAD0", "NUMPAD9"},
		rationale: "Alt+Shift and a digit switch the lists of the temporary panel (TempPanel.List0-9); the keypad's digits do the same",
	},
	"app.(*AIChatPanel).ProcessKey": {
		areas:     []string{"Shell"},
		chords:    []string{"RCtrlC", "RCtrlP", "CtrlZ"},
		rationale: "the AI chat: copy the last answer, apply and undo the patch",
	},
	"app.NewAIChatPanel": {
		areas:     []string{"Shell"},
		chords:    []string{"F3"},
		rationale: "F3 on the AI chat's patch bar opens the patch (AI.ReviewPatch)",
	},
	"app.(*aiVFSWrapper).ProcessPanelKey": {
		areas:     []string{"Shell"},
		chords:    []string{"Ctrl1", "Ctrl2", "Ctrl3", "Ctrl4"},
		rationale: "Ctrl and a digit switch the views of the AI panel (AI.View*)",
	},
	"app.macroFilter": {
		areas:  []string{"Common"},
		chords: []string{"CtrlVK_BE", "CtrlW"},
		nav: []string{"CAPITAL", "CONTROL", "LCONTROL", "LMENU", "LSHIFT", "MENU", "NUMLOCK",
			"RCONTROL", "RMENU", "RSHIFT", "SCROLL", "SHIFT"},
		rationale: "the key router: Ctrl+. records a macro (Macro.Record), held Ctrl+W repeats die at the exit question; " +
			"a modifier key alone is never recorded",
	},
	"cmdline.(*CommandLine).ProcessKey": {
		areas:     []string{"Shell"},
		chords:    []string{"CtrlE", "CtrlX"},
		nav:       []string{"BACK", "DELETE", "END", "HOME", "LEFT", "RIGHT"},
		rationale: "the command line: Ctrl+E and Ctrl+X walk the history, the rest is editing",
	},
	"cmdline.isCtrlY": {
		areas:     []string{"Shell"},
		chords:    []string{"CtrlY"},
		rationale: "Ctrl+Y clears the command line (CommandLine.Clear)",
	},
	"cmdline.(*CompletionMenu).ProcessKey": {
		nav:       []string{"RETURN", "TAB"},
		rationale: "the completion list of the command line: Enter and Tab pick a completion",
	},

	// Keys as data: translation, naming, other input turned into keys.
	"keymap.(*KeyRemap).Apply":         {infrastructure: true, rationale: "keymap.ini: leaves a modifier key alone"},
	"keymap.ApplyMacKeys":              {infrastructure: true, rationale: "the Mac layout: leaves a modifier key alone"},
	"keymap.EventToFarString":          {infrastructure: true, rationale: "names a keystroke"},
	"keymap.IsPanelBookmarkHotkey":     {infrastructure: true, rationale: "tells the numbered bookmark keys, declared as Panel.Bookmark.*, to the key router"},
	"keymap.ParseFarKey":               {infrastructure: true, rationale: "turns a key name into a keystroke"},
	"keymap.TranslateInput":            {infrastructure: true, rationale: "encodes a keystroke for a program in the terminal"},
	"keymap.TranslateKeyToKitty":       {infrastructure: true, rationale: "encodes a keystroke in the kitty keyboard protocol"},
	"keymap.TranslateLegacySpecialKey": {infrastructure: true, rationale: "encodes a keystroke for a legacy terminal"},
	"keymap.ctrlCharFromVK":            {infrastructure: true, rationale: "the control character of a Ctrl key"},
	"keymap.LayoutLetterVK":            {infrastructure: true, rationale: "maps a non-Latin layout back to the Latin key"},
	"keymap.var farKeyNames":           {infrastructure: true, rationale: "the table of key names"},
	"keymap.var vkSpelledHotkeys":      {infrastructure: true, rationale: "the keys named after their virtual key"},
	"keymap.win32UnicodeChar":          {infrastructure: true, rationale: "the character of a win32-input-mode keystroke"},
	"panel.(*PanelsFrame).updateConsoleOverlayModifiers": {
		infrastructure: true, rationale: "tracks the modifiers held over the console overlay",
	},
	"panel.(*PanelsFrame).consumeHostConsoleReply": {
		infrastructure: true, rationale: "reads a reply of the host console, which arrives as keystrokes",
	},
	"panel.isPasteLineBreak": {
		infrastructure: true, rationale: "a line break inside a paste",
	},
	"panel.(*PanelsFrame).ProcessMouse": {
		infrastructure: true, rationale: "turns the mouse wheel and a click into the keys they stand for",
	},
	"panel.middleClickKey": {
		infrastructure: true, rationale: "the key a middle click stands for",
	},
	"panel.(*PanelsFrame).HandleSemanticAction": {
		infrastructure: true, rationale: "a request of the semantic GUI protocol, carried out as the key it stands for",
	},
	"panel.(*FileSystemPanel).EnterSelectedFromAction": {
		infrastructure: true, rationale: "Panel.Open and its kin run Enter on the file panel",
	},
	"panel.isAltKey": {
		infrastructure: true, rationale: "tells the Alt key's own event, for the lone Alt",
	},
	"panel.nativeKeyEvent": {
		infrastructure: true, rationale: "builds the keystroke a native key's action presses",
	},
	"app.handleSemanticElementAction": {
		infrastructure: true, rationale: "a request of the semantic GUI protocol, carried out as the key it stands for",
	},
	"app.handleSemanticFrameAction": {
		infrastructure: true, rationale: "a request of the semantic GUI protocol, carried out as the key it stands for",
	},
	"app.init": {
		infrastructure: true, rationale: "Editor.DeleteWordForward runs Ctrl+Del in the editor",
	},
	"terminal.(*TerminalView).rowHasText": {
		infrastructure: true, rationale: "compares the character of a screen cell, not a key",
	},
	"vfs.PanelHelpKey": {
		infrastructure: true, rationale: "names the help key a panel plugin's key bar shows",
	},
}

// keyInventoryBookmarkChords are far2l's numbered bookmark keys: Right Ctrl or
// Ctrl+Alt and a digit go to the slot, with Shift they store it
// (Panel.Bookmark.Go0-9, Save0-9).
func keyInventoryBookmarkChords() []string {
	var chords []string
	for digit := 0; digit <= 9; digit++ {
		chords = append(chords,
			fmt.Sprintf("RCtrl%d", digit), fmt.Sprintf("CtrlAlt%d", digit),
			fmt.Sprintf("RCtrlShift%d", digit), fmt.Sprintf("CtrlAltShift%d", digit))
	}
	return chords
}

// keyInventoryVTUIAudit lists the functions of vtui, the framework under f4,
// whose keys reach f4's areas: the frame manager's own shortcuts, and the
// input line that is f4's command line. The rest of vtui is widget internals.
var keyInventoryVTUIAudit = map[string]keyInventoryEntry{
	"vtui.(*frameManager).dispatchEvent": {
		areas: []string{"Common"},
		chords: []string{"CtrlTab", "CtrlShiftTab", "CtrlAltTab", "CtrlAltShiftTab", "CtrlShiftP", "CtrlAltShiftP",
			"CtrlN", "CtrlW", "F1", "F12", "F9",
			"Alt1", "Alt2", "Alt3", "Alt4", "Alt5", "Alt6", "Alt7", "Alt8", "Alt9"},
		nav: []string{"0", "ESCAPE", "F10", "RETURN",
			"CONTROL", "LCONTROL", "RCONTROL", "MENU", "LMENU", "RMENU", "SHIFT", "LSHIFT", "RSHIFT"},
		rationale: "vtui's own shortcuts: the workspaces (Workspace.*), help, the main menu, the screen dump. Esc and F10 close the menu " +
			"bar, Enter is what a click beside a dialog sends, the modifier keys reveal the workspace tabs",
	},
	"vtui.(*Edit).ProcessKey": {
		areas:  []string{"Shell"},
		chords: []string{"CtrlA", "CtrlE", "CtrlX", "CtrlC", "CtrlIns", "CtrlV", "ShiftDel", "ShiftIns", "Enter"},
		nav:    []string{"BACK", "DELETE", "DOWN", "END", "HOME", "INSERT", "LEFT", "RIGHT", "Char ' '", "Char '\\n'", "Char '\\r'"},
		rationale: "the input line under the command line; in the panels the hotkey table takes Ctrl+A, Ctrl+C, Ctrl+V and the clipboard keys before it. " +
			"A space and the line breaks are characters of a paste or of typing",
	},
}

func TestKeyInventory(t *testing.T) {
	root := testutil.ModuleRootDir(t)
	discovered := map[string][]string{}
	for _, source := range commandPaletteParseProductionGo(t) {
		for id, keys := range keyInventoryOfFile(t, source.pkg, source.File, source.fset) {
			discovered[id] = keyInventoryUnion(discovered[id], keys)
		}
	}
	if path := os.Getenv("F4_KEY_INVENTORY_DUMP"); path != "" {
		keyInventoryDump(t, path, discovered)
	}

	baselinePath := filepath.Join(root, keyInventoryBaselinePath)
	baseline, err := keyInventoryLoadBaseline(baselinePath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", keyInventoryBaselinePath, err)
	}
	baselineMissing := os.IsNotExist(err)

	var failures []string
	unaudited := map[string][]string{}
	for id, keys := range discovered {
		if entry, ok := keyInventoryAudit[id]; ok {
			failures = append(failures, keyInventoryCheckEntry(id, keys, entry)...)
			continue
		}
		unaudited[id] = keys
	}
	for id := range keyInventoryAudit {
		if _, ok := discovered[id]; !ok {
			failures = append(failures, fmt.Sprintf("%s: audited, but the source no longer compares any key there; drop the entry", id))
		}
	}

	update := os.Getenv("F4_UPDATE_KEY_INVENTORY_BASELINE") == "1"
	var grown, stale []string
	for id, keys := range unaudited {
		frozen, ok := baseline[id]
		if !ok && !baselineMissing {
			grown = append(grown, fmt.Sprintf("%s: a new function compares %s; audit it in keyInventoryAudit", id, strings.Join(keys, ",")))
			continue
		}
		if extra := keyInventoryMinus(keys, frozen); len(extra) != 0 && !baselineMissing {
			grown = append(grown, fmt.Sprintf("%s: new key(s) %s in a function of the baseline; audit the function in keyInventoryAudit", id, strings.Join(extra, ",")))
			continue
		}
		if len(keyInventoryMinus(frozen, keys)) != 0 {
			stale = append(stale, id)
		}
	}
	for id := range baseline {
		if _, ok := unaudited[id]; !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(grown)
	sort.Strings(stale)
	failures = append(failures, grown...)

	if update || baselineMissing {
		if len(grown) != 0 {
			t.Fatalf("the baseline may only shrink:\n%s", strings.Join(grown, "\n"))
		}
		if err := keyInventoryWriteBaseline(baselinePath, unaudited); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s written with %d functions; commit it", keyInventoryBaselinePath, len(unaudited))
	} else if len(stale) != 0 {
		failures = append(failures, fmt.Sprintf("the baseline lists keys or functions the source no longer has unaudited: %s\n%s", strings.Join(stale, ", "), keyInventoryUpdateHint))
	}

	failures = append(failures, keyInventoryCheckVTUI(t)...)
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("the key inventory and the source disagree:\n%s", strings.Join(failures, "\n"))
	}
}

// keyInventoryOfFile returns, for every function of file that compares a key
// event against a key, the keys it compares against. Function literals belong
// to the function or package-level variable they are written in.
func keyInventoryOfFile(t *testing.T, pkg string, file *ast.File, fset *token.FileSet) map[string][]string {
	t.Helper()
	alias, ok := keyInventoryImportName(file, "github.com/unxed/vtinput")
	if !ok {
		// Without vtinput a file has no key event to look at.
		return nil
	}
	out := map[string][]string{}
	for _, declaration := range file.Decls {
		switch d := declaration.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			if keys := keyInventoryKeysIn(d.Body, alias); len(keys) != 0 {
				id := pkg + "." + commandPaletteFunctionIdentity(t, fset, d)
				out[id] = keyInventoryUnion(out[id], keys)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) {
						break
					}
					if keys := keyInventoryKeysIn(value.Values[i], alias); len(keys) != 0 {
						id := pkg + ".var " + name.Name
						out[id] = keyInventoryUnion(out[id], keys)
					}
				}
			}
		}
	}
	return out
}

func keyInventoryImportName(file *ast.File, path string) (string, bool) {
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil || imported != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, spec.Name.Name != "_"
		}
		return filepath.Base(path), true
	}
	return "", false
}

// keyInventoryKeysIn collects the keys node compares a key event against: a
// vtinput.VK_ constant wherever it appears, a character or number compared
// with an event's VirtualKeyCode, and a character compared with its Char,
// named after the virtual key that types it when there is one.
func keyInventoryKeysIn(node ast.Node, alias string) []string {
	set := map[string]bool{}
	literal := func(lit *ast.BasicLit, char bool) {
		if name := keyInventoryLiteralKey(lit, char); name != "" {
			set[name] = true
		}
	}
	field := func(expr ast.Expr) string {
		if selector, ok := expr.(*ast.SelectorExpr); ok {
			switch selector.Sel.Name {
			case "VirtualKeyCode", "Char":
				return selector.Sel.Name
			}
		}
		return ""
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == alias && strings.HasPrefix(x.Sel.Name, "VK_") {
				set[strings.TrimPrefix(x.Sel.Name, "VK_")] = true
			}
		case *ast.BinaryExpr:
			switch x.Op {
			case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			default:
				return true
			}
			if lit, ok := x.Y.(*ast.BasicLit); ok && field(x.X) != "" {
				literal(lit, field(x.X) == "Char")
			}
			if lit, ok := x.X.(*ast.BasicLit); ok && field(x.Y) != "" {
				literal(lit, field(x.Y) == "Char")
			}
		case *ast.SwitchStmt:
			kind := field(x.Tag)
			if kind == "" {
				return true
			}
			for _, stmt := range x.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, value := range clause.List {
					if lit, ok := value.(*ast.BasicLit); ok {
						literal(lit, kind == "Char")
					}
				}
			}
		}
		return true
	})
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// keyInventoryLiteralKey names the key a literal stands for: 'A' and 0x41
// compared with a VirtualKeyCode are "A"; '\\' compared with a Char is the key
// that types it, "OEM_5"; a character no key spells by itself keeps its own
// name, "Char '*'".
func keyInventoryLiteralKey(lit *ast.BasicLit, char bool) string {
	var value rune
	switch lit.Kind {
	case token.CHAR:
		r, _, _, err := strconv.UnquoteChar(strings.Trim(lit.Value, "'"), '\'')
		if err != nil {
			return ""
		}
		value = r
	case token.INT:
		n, err := strconv.ParseInt(lit.Value, 0, 32)
		if err != nil {
			return ""
		}
		value = rune(n)
	default:
		return ""
	}
	if value == 0 {
		return ""
	}
	if char {
		if value >= 'a' && value <= 'z' {
			value -= 'a' - 'A'
		}
		if e := keymap.ParseFarKey(string(value)); e != nil && e.VirtualKeyCode != 0 {
			return keyInventoryVKName(e.VirtualKeyCode)
		}
		return "Char " + strconv.QuoteRune(value)
	}
	return keyInventoryVKName(uint16(value)) // #nosec G115 -- a virtual key code literal.
}

func keyInventoryVKName(vk uint16) string {
	return strings.TrimPrefix(vtinput.VKString(vk), "VK_")
}

// keyInventoryChordKey is the key a chord ends in, under the name the
// inventory gives keys.
func keyInventoryChordKey(chord string) (string, error) {
	spec, _, _ := strings.Cut(chord, ":")
	spec = strings.TrimSpace(spec)
	e := keymap.ParseFarKey(spec)
	if e == nil || e.VirtualKeyCode == 0 {
		return "", fmt.Errorf("%q is not a key", chord)
	}
	if !strings.EqualFold(keymap.EventToHotkeyString(e), spec) {
		return "", fmt.Errorf("%q is not written the way the hotkey table spells it (%s)", chord, keymap.EventToHotkeyString(e))
	}
	return keyInventoryVKName(e.VirtualKeyCode), nil
}

// keyInventoryCheckEntry compares an audited function with its source.
func keyInventoryCheckEntry(id string, keys []string, entry keyInventoryEntry) []string {
	var failures []string
	if strings.TrimSpace(entry.rationale) == "" {
		failures = append(failures, id+": the audit has no rationale")
	}
	if entry.infrastructure {
		if len(entry.chords) != 0 || len(entry.nav) != 0 {
			failures = append(failures, id+": an infrastructure audit lists no keys")
		}
		return failures
	}
	if len(entry.chords) != 0 && len(entry.areas) == 0 {
		failures = append(failures, id+": the audit lists chords but no area")
	}
	declared := keyInventoryDeclared()
	covered := append([]string(nil), entry.nav...)
	for _, chord := range entry.chords {
		key, err := keyInventoryChordKey(chord)
		if err != nil {
			failures = append(failures, id+": "+err.Error())
			continue
		}
		covered = append(covered, key)
		if !keyInventoryIsDeclared(declared, chord, entry.areas) {
			failures = append(failures, fmt.Sprintf("%s: %s is handled there but no action of %s declares it; add it to an action's DefaultKeys or NativeKeys", id, chord, strings.Join(entry.areas, "/")))
		}
	}
	covered = keyInventoryUnion(nil, covered)
	if extra := keyInventoryMinus(keys, covered); len(extra) != 0 {
		failures = append(failures, fmt.Sprintf("%s: the source compares %s, which the audit does not list", id, strings.Join(extra, ",")))
	}
	// Only nav keys can go stale this way: a chord may end in a key the
	// source reaches through a range (VK_0 to VK_9 shows as its two ends).
	if gone := keyInventoryMinus(keyInventoryUnion(nil, entry.nav), keys); len(gone) != 0 {
		failures = append(failures, fmt.Sprintf("%s: the audit lists %s as navigation, which the source no longer compares", id, strings.Join(gone, ",")))
	}
	return failures
}

// keyInventoryDeclared maps area -> lower-cased key spec -> declared, from
// every action's DefaultKeys, NativeKeys and NativeKeyAliases.
func keyInventoryDeclared() map[string]map[string]bool {
	declared := map[string]map[string]bool{}
	for _, a := range action.All() {
		areas := append([]string{a.Area}, a.DefaultAreas...)
		specs := append(append(append([]string(nil), a.DefaultKeys...), a.NativeKeys...), a.NativeKeyAliases...)
		for _, spec := range specs {
			key, _, _ := strings.Cut(spec, ":")
			key = strings.ToLower(strings.TrimSpace(key))
			for _, area := range areas {
				if declared[area] == nil {
					declared[area] = map[string]bool{}
				}
				declared[area][key] = true
			}
		}
	}
	return declared
}

// keyInventoryIsDeclared reports whether the key of chord is declared for one
// of areas or for Common. The condition a declaration names says when the key
// works; the audit only asks that it is declared.
func keyInventoryIsDeclared(declared map[string]map[string]bool, chord string, areas []string) bool {
	key, _, _ := strings.Cut(chord, ":")
	key = strings.ToLower(strings.TrimSpace(key))
	for _, area := range append(append([]string(nil), areas...), "Common") {
		if declared[area][key] {
			return true
		}
	}
	return false
}

// keyInventoryCheckVTUI audits the vtui functions whose keys reach f4. Their
// source is read from the module vtui is built from, so a vtui update that
// adds a shortcut fails here until f4 declares it.
func keyInventoryCheckVTUI(t *testing.T) []string {
	t.Helper()
	if len(keyInventoryVTUIAudit) == 0 {
		return nil
	}
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/unxed/vtui").Output() // #nosec G204 -- fixed arguments.
	if err != nil {
		return []string{fmt.Sprintf("cannot locate the vtui module: %v", err)}
	}
	dir := strings.TrimSpace(string(out))
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return []string{err.Error()}
	}
	discovered := map[string][]string{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return []string{fmt.Sprintf("parse %s: %v", path, err)}
		}
		for id, keys := range keyInventoryOfFile(t, "vtui", file, fset) {
			if _, audited := keyInventoryVTUIAudit[id]; audited {
				discovered[id] = keyInventoryUnion(discovered[id], keys)
			}
		}
	}
	var failures []string
	for id, entry := range keyInventoryVTUIAudit {
		keys, ok := discovered[id]
		if !ok {
			failures = append(failures, id+": not found in vtui any more; the audit is stale")
			continue
		}
		failures = append(failures, keyInventoryCheckEntry(id, keys, entry)...)
	}
	return failures
}

func keyInventoryUnion(a, b []string) []string {
	set := map[string]bool{}
	for _, key := range a {
		set[key] = true
	}
	for _, key := range b {
		set[key] = true
	}
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// keyInventoryMinus returns the keys of a that are not in b.
func keyInventoryMinus(a, b []string) []string {
	set := map[string]bool{}
	for _, key := range b {
		set[key] = true
	}
	var out []string
	for _, key := range a {
		if !set[key] {
			out = append(out, key)
		}
	}
	return out
}

func keyInventoryLoadBaseline(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the repository's own baseline file.
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, keys, ok := strings.Cut(line, " = ")
		if !ok {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		out[id] = strings.Split(keys, ",")
	}
	return out, nil
}

func keyInventoryWriteBaseline(path string, entries map[string][]string) error {
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("# Functions whose keys are not yet declared to the Hotkey Configurator\n")
	b.WriteString("# (cmd/f4/key_inventory_test.go). This list may only shrink.\n")
	for _, id := range ids {
		b.WriteString(id + " = " + strings.Join(entries[id], ",") + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func keyInventoryDump(t *testing.T, path string, discovered map[string][]string) {
	t.Helper()
	ids := make([]string, 0, len(discovered))
	for id := range discovered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id + " = " + strings.Join(discovered[id], ",") + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil { // #nosec G703 -- a developer-chosen dump path.
		t.Fatal(err)
	}
}
