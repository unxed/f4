package panel

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// applyPluginHotkeyChoice is what OK does in the plugin hotkey dialog: text is
// what the one-character field holds. Empty takes the hotkey away: the assigned
// one and every default the plugin brings with it (its declared shortcut, the
// letter its label marks with an ampersand), so that nothing is left, as
// clearing the hot key of a link in the drive menu leaves nothing. A letter or digit becomes the menu hotkey
// of the entry, anything else is refused with ok false so that the dialog stays
// open. changed reports whether the bindings moved.
func applyPluginHotkeyChoice(hm *keymap.HotkeyManager, actionName, label, declaredKey, text string) (changed, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		if area, key := keymap.ConfiguredHotkeyBinding(hm, actionName); key != "" {
			changed = keymap.DeletePluginHotkey(hm, area, key)
		}
		if declaredKey != "" && !PluginDefaultKeyOff(declaredKey) {
			SetPluginDefaultKeyOff(declaredKey, true)
			changed = true
		}
		if pluginLabelHotkey(actionName, label) != 0 {
			SetPluginDefaultKeyOff(pluginLabelHotkeyOffKey(actionName), true)
			changed = true
		}
		return changed, true
	}
	runes := []rune(text)
	if len(runes) != 1 || (!unicode.IsLetter(runes[0]) && !unicode.IsDigit(runes[0])) {
		return false, false
	}
	// Choosing the letter the entry already has is not a change.
	if _, key := keymap.ConfiguredHotkeyBinding(hm, actionName); strings.EqualFold(key, text) {
		return false, true
	}
	return bindPluginMenuHotkey(hm, actionName, runes[0]), true
}

// currentPluginHotkeyText is what the one-character field starts with: the
// assigned letter, else the plugin's own default when it is a single character
// (declared, or marked with an ampersand in the label, as the row "V Visual
// File Renamer" is), else nothing.
func currentPluginHotkeyText(hm *keymap.HotkeyManager, actionName, label, declaredKey string) string {
	if _, key := keymap.ConfiguredHotkeyBinding(hm, actionName); key != "" {
		if r := pluginMenuHotkeyRune(key); r != 0 {
			return string(r)
		}
		return ""
	}
	if declaredKey != "" && !PluginDefaultKeyOff(declaredKey) {
		if r := pluginMenuHotkeyRune(declaredKey); r != 0 {
			return string(r)
		}
	}
	if r := pluginLabelHotkey(actionName, label); r != 0 {
		return string(r)
	}
	return ""
}

// pluginHotkeyEdit is the one-cell field of the hot key: a letter or a digit
// replaces what it holds (never a second character next to it), Delete and
// Backspace empty it, any other printable character is ignored. Whatever is
// typed, one character is all it can hold, as the field of the link editor in
// the drive menu.
type pluginHotkeyEdit struct{ *vtui.Edit }

func (e *pluginHotkeyEdit) ProcessKey(ev *vtinput.InputEvent) bool {
	if ev == nil || !ev.KeyDown {
		return e.Edit.ProcessKey(ev)
	}
	switch ev.VirtualKeyCode {
	case vtinput.VK_DELETE, vtinput.VK_BACK:
		e.SetText("")
		return true
	}
	if r := pluginHotkeyEventRune(ev); r != 0 {
		e.SetText(string(r))
		return true
	}
	if mods := keymap.NormalizeMods(ev.ControlKeyState); unicode.IsPrint(ev.Char) &&
		!mods.Contains(vtinput.LeftCtrlPressed) && !mods.Contains(vtinput.LeftAltPressed) {
		return true // some other printable character: not a hot key, not typed
	}
	return e.Edit.ProcessKey(ev)
}

// Show keeps the caret on the character rather than scrolling it out of the
// one-cell field to make room for the insertion point behind it.
func (e *pluginHotkeyEdit) Show(scr *vtui.ScreenBuf) {
	e.Edit.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_HOME})
	e.Edit.Show(scr)
}

func pluginHotkeyDialogWidth(label string) int {
	cleanLabel, _, _ := vtui.ParseAmpersandString(label)
	maxContent := runewidth.StringWidth(i18n.Msg("Plugins.HotkeyPrompt"))
	for _, width := range []int{
		runewidth.StringWidth(cleanLabel) + 2, // one-cell field and its gap
		runewidth.StringWidth(i18n.Msg("DriveLink.HotkeyHint")),
	} {
		if width > maxContent {
			maxContent = width
		}
	}
	buttonWidth := func(text string) int {
		return runewidth.StringWidth(string(vtui.UIStrings.ButtonBrackets[0]) + " " + text + " " + string(vtui.UIStrings.ButtonBrackets[1]))
	}
	buttons := buttonWidth(i18n.Msg("vtui.Ok")) + 2 + buttonWidth(i18n.Msg("vtui.Cancel"))
	if buttons > maxContent {
		maxContent = buttons
	}
	// Leave one cell between each line and the frame.
	return maxContent + 4
}

// showPluginHotkeyDialog asks for the menu hotkey of a plugin entry in a
// one-character field with OK and Cancel: the letter is shown, edited and
// deleted (empty field) like any other field, as the ticket asked (#918).
// The window is laid out as in the ticket's example: the field stands left
// of the entry's name.
func showPluginHotkeyDialog(hm *keymap.HotkeyManager, actionName, label string, onComplete func()) {
	if hm == nil || vtui.FrameManager == nil {
		return
	}
	cleanLabel, _, _ := vtui.ParseAmpersandString(label)
	declaredKey := declaredHotkeyString(PluginActionDefaultShortcut(actionName))

	// The rows follow the example in the ticket (#918): the prompt, the
	// one-cell field with the name of the entry beside it, the hint, a rule
	// and the buttons as the last row inside the frame, no blank rows.
	width, height := pluginHotkeyDialogWidth(cleanLabel), 7
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Plugins.HotkeyTitle"))
	dlg.ShowClose = false

	edit := &pluginHotkeyEdit{vtui.NewEdit(0, 0, 1, currentPluginHotkeyText(hm, actionName, label, declaredKey))}
	prompt := vtui.NewText(0, 0, i18n.Msg("Plugins.HotkeyPrompt"), vtui.Palette[vtui.ColDialogText])
	title := vtui.NewText(0, 0, cleanLabel, vtui.Palette[vtui.ColDialogText])
	note := vtui.NewText(0, 0, i18n.Msg("DriveLink.HotkeyHint"), vtui.Palette[vtui.ColDialogText])
	sep := vtui.NewSeparator(0, 0, width-4, true, true)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	for _, it := range []vtui.UIElement{prompt, edit, title, note, sep, btnOk, btnCancel} {
		dlg.AddItem(it)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+1, width-4, height-2)
	vbox.Add(prompt, vtui.Margins{}, vtui.AlignLeft)
	row := vtui.NewHBoxLayout(0, 0, width-4, 1)
	row.Add(edit, vtui.Margins{Right: 1}, vtui.AlignLeft)
	row.Add(title, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(row, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(note, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(sep, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	btnCancel.OnClick = func() { dlg.Close() }
	btnOk.OnClick = func() {
		changed, ok := applyPluginHotkeyChoice(hm, actionName, label, declaredKey, edit.GetText())
		if !ok {
			vtui.ShowMessageOn(dlg, i18n.Msg("Plugins.HotkeyTitle"), fmt.Sprint(i18n.Msg("Plugins.HotkeyFieldInvalid")), []string{i18n.Msg("vtui.Ok")})
			return
		}
		dlg.Close()
		if changed && onComplete != nil {
			onComplete()
		}
	}
	dlg.SetFocusedItem(edit)
	vtui.FrameManager.Push(dlg)
}
