package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The palette lists every command but gave no way to put a key on one of
// them (#1836): the key had to be found again in the hotkey list of the
// settings. Ctrl+K on a result now opens the same area and key dialogs that
// list uses, for the command under the cursor.

// commandPaletteAssignKey reports whether event is the chord that assigns a
// key to the selected command.
func commandPaletteAssignKey(event *vtinput.InputEvent) bool {
	const ctrl = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed
	const other = vtinput.LeftAltPressed | vtinput.RightAltPressed | vtinput.ShiftPressed
	return event.KeyDown && event.VirtualKeyCode == vtinput.VK_K &&
		event.ControlKeyState&ctrl != 0 && event.ControlKeyState&other == 0
}

// commandPaletteUnassignKey reports whether event is Ctrl+Shift+K, which takes
// a key off the selected command (#1836, f4#1842: a default key the user
// needs for something else, like the right Ctrl+A of the AI panel).
func commandPaletteUnassignKey(event *vtinput.InputEvent) bool {
	const ctrl = vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed
	const alt = vtinput.LeftAltPressed | vtinput.RightAltPressed
	return event.KeyDown && event.VirtualKeyCode == vtinput.VK_K &&
		event.ControlKeyState&ctrl != 0 && event.ControlKeyState&vtinput.ShiftPressed != 0 &&
		event.ControlKeyState&alt == 0
}

// commandPaletteBinding is one key bound to a command, in one area.
type commandPaletteBinding struct{ area, key string }

// maxUnassignChoices bounds the keys offered at once; a command rarely has
// more than two.
const maxUnassignChoices = 6

// commandPaletteBindings lists the keys bound to actionName in every area,
// in a stable order.
func commandPaletteBindings(hm *keymap.HotkeyManager, actionName string) []commandPaletteBinding {
	var out []commandPaletteBinding
	for area, binds := range hm.GetActiveBindings() {
		for key, binding := range binds {
			if name, _, _ := strings.Cut(binding, ":"); strings.EqualFold(name, actionName) {
				out = append(out, commandPaletteBinding{area, key})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].area != out[j].area {
			return out[i].area < out[j].area
		}
		return out[i].key < out[j].key
	})
	return out
}

func (d *commandPaletteDialog) unassignKeyFromSelected() {
	if d == nil || d.table == nil {
		return
	}
	index := d.table.SelectPos
	if index < 0 || index >= len(d.filtered) {
		return
	}
	entry := d.filtered[index]
	act, ok := commandPaletteAssignTarget(entry)
	hm := keymap.GlobalHotkeysMgr
	if !ok || hm == nil {
		vtui.ShowMessageOn(d.Window, i18n.Msg("CommandPalette.Title"),
			i18n.Msg("CommandPalette.AssignUnavailable"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	bindings := commandPaletteBindings(hm, act.Name)
	if len(bindings) == 0 {
		vtui.ShowMessageOn(d.Window, i18n.Msg("CommandPalette.Title"),
			i18n.Msg("CommandPalette.NoKeyToRemove"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	if len(bindings) > maxUnassignChoices {
		bindings = bindings[:maxUnassignChoices]
	}
	buttons := make([]string, 0, len(bindings)+1)
	for _, b := range bindings {
		buttons = append(buttons, keymap.FormatKeyForUI(b.key))
	}
	buttons = append(buttons, i18n.Msg("vtui.Cancel"))
	question := fmt.Sprintf(i18n.Msg("CommandPalette.RemoveKeyQuestion"), entry.Label)
	vtui.ShowMessageOn(d.Window, i18n.Msg("CommandPalette.Title"), question, buttons).OnResult = func(choice int) {
		if choice < 0 || choice >= len(bindings) {
			return
		}
		// "None" overrides a default key as well as a key of the user's.
		hm.Bind(bindings[choice].area, bindings[choice].key, "None")
		hm.Save()
		d.reloadAfterAssign(entry.Key)
	}
}

// commandPaletteAssignTarget is the action a key can be bound to for entry,
// and the area the key dialog offers first. Entries that run a stored
// function rather than an action (workspaces, drives, macros) have none.
func commandPaletteAssignTarget(entry commandPaletteEntry) (act action.Action, ok bool) {
	switch entry.source {
	case commandPaletteSourceAction:
		return GetAction(entry.ID)
	case commandPaletteSourcePlugin:
		if entry.ID == "" {
			return action.Action{}, false
		}
		return GetAction(keymap.PluginCommandActionName(entry.ID))
	}
	return action.Action{}, false
}

func (d *commandPaletteDialog) assignKeyToSelected() {
	if d == nil || d.table == nil {
		return
	}
	index := d.table.SelectPos
	if index < 0 || index >= len(d.filtered) {
		return
	}
	entry := d.filtered[index]
	act, ok := commandPaletteAssignTarget(entry)
	hm := keymap.GlobalHotkeysMgr
	if !ok || hm == nil {
		vtui.ShowMessageOn(d.Window, i18n.Msg("CommandPalette.Title"),
			i18n.Msg("CommandPalette.AssignUnavailable"), []string{i18n.Msg("vtui.Ok")})
		return
	}
	showAreaSelectDialog(hm, act.Name, act.Area, "", func() {
		hm.Save()
		d.reloadAfterAssign(entry.Key)
	})
}

// reloadAfterAssign rebuilds the rows, so the new key shows in the Shortcut
// column, and puts the cursor back on the command it was on.
func (d *commandPaletteDialog) reloadAfterAssign(key string) {
	if d.rebuild != nil {
		d.entries = d.rebuild()
	}
	d.refilter(d.query.GetText())
	for i, entry := range d.filtered {
		if entry.Key == key {
			d.table.SetSelectPos(i)
			d.refreshDescription()
			break
		}
	}
}
