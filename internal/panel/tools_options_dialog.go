package panel

import (
	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// toolsOptionsList is the list of the tools window: a row per tool with its
// check box. A click on the box or Space turns the tool on or off, a click on
// the name only selects it (f4#918).
type toolsOptionsList struct {
	*vtui.ListBox
	onToggle   func(idx int)
	onSelect   func(idx int)
	onActivate func(idx int) // a double click on a name
}

func (l *toolsOptionsList) ProcessMouse(e *vtinput.InputEvent) bool {
	handled := l.ListBox.ProcessMouse(e)
	if handled && e.Type == vtinput.MouseEventType && e.ButtonState == vtinput.FromLeft1stButtonPressed &&
		vtui.IsMousePress(e) && int(e.MouseX) <= l.X1+4 && l.hasRowAt(l.SelectPos) {
		l.onToggle(l.SelectPos)
	}
	if l.onSelect != nil {
		l.onSelect(l.SelectPos)
	}
	// A double click on a name is the Settings button (f4#918).
	if handled && l.onActivate != nil && e.Type == vtinput.MouseEventType && e.KeyDown &&
		e.ButtonState&vtinput.FromLeft1stButtonPressed != 0 && e.MouseEventFlags&vtinput.DoubleClick != 0 &&
		int(e.MouseX) > l.X1+4 && l.hasRowAt(l.SelectPos) {
		l.onActivate(l.SelectPos)
	}
	return handled
}

func (l *toolsOptionsList) ProcessKey(e *vtinput.InputEvent) bool {
	handled := l.ListBox.ProcessKey(e)
	if l.onSelect != nil {
		l.onSelect(l.SelectPos)
	}
	return handled
}

func (l *toolsOptionsList) hasRowAt(idx int) bool { return idx >= 0 && idx < len(l.Items) }

// toolsOptionsRowText is a row as the window shows it.
func toolsOptionsRowText(shown bool, name string) string {
	if shown {
		return "[x] " + name
	}
	return "[ ] " + name
}

// toolsWindow describes one tools window: the F11 menu's and the drive
// menu's differ only in where the rows come from and what the buttons do.
type toolsWindow struct {
	title, empty string
	names        []string
	shown        []bool
	// apply stores the final choice of every row; it runs on Ok only, so Cancel
	// and Esc leave everything as it was. An error keeps the window open.
	apply func(shown []bool) error
	// configure returns what the Settings button runs for row idx; nil, or a
	// false result, dims the button. A nil configure leaves the button out.
	configure func(idx int) (func(), bool)
	// extraLabel and extra add a button of the window's own, always live.
	extraLabel string
	extra      func()
	// applied runs after Ok has stored the choice and the window is closed.
	applied func()
}

// show opens the window: every row with a check box that works at once.
func (tw *toolsWindow) show() {
	if len(tw.names) == 0 {
		vtui.ShowMessage(tw.title, tw.empty, []string{i18n.Msg("vtui.Ok")})
		return
	}
	settingsLabel := i18n.Msg("Plugins.ToolsOptionsSettings")
	buttonsWidth := 0
	if tw.configure != nil {
		buttonsWidth += runewidth.StringWidth(settingsLabel) + 8
	}
	if tw.extra != nil {
		buttonsWidth += runewidth.StringWidth(tw.extraLabel) + 8
	}
	width := runewidth.StringWidth(tw.title) + 6
	for _, name := range tw.names {
		if w := runewidth.StringWidth(name) + 4 + 4; w > width {
			width = w
		}
	}
	width = max(width, buttonsWidth+4, 30)
	screenW, screenH := vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight()
	if screenW > 0 && width > screenW-2 {
		width = screenW - 2
	}
	listH := len(tw.names)
	if screenH > 0 && listH > screenH-6 {
		listH = max(3, screenH-6)
	}
	// Inside the two border rows: the list, a blank row, the window's buttons,
	// a rule, and Ok / Cancel on the last row, with nothing under them (f4#918).
	height := listH + 6

	dlg := vtui.NewCenteredDialog(width, height, tw.title)
	dlg.ShowClose = false

	items := make([]string, len(tw.names))
	for i := range tw.names {
		items[i] = toolsOptionsRowText(tw.shown[i], tw.names[i])
	}
	list := &toolsOptionsList{ListBox: vtui.NewListBox(0, 0, width-4, listH, items)}
	// The rows sit on the dialog background in the dialog text colour, as the
	// lists of the Settings dialog do (f4#918).
	list.ColorTextIdx = vtui.ColDialogText
	list.ColorItemSelectTextIdx = vtui.ColDialogText

	var settings *vtui.Button
	configFor := func(idx int) (func(), bool) {
		if tw.configure == nil || idx < 0 || idx >= len(tw.names) {
			return nil, false
		}
		return tw.configure(idx)
	}
	refreshButton := func(idx int) {
		if settings != nil {
			_, ok := configFor(idx)
			settings.SetDisabled(!ok)
		}
	}
	list.onSelect = refreshButton
	list.onActivate = func(idx int) {
		if run, ok := configFor(idx); ok {
			run()
		}
	}
	list.onToggle = func(idx int) {
		if idx < 0 || idx >= len(tw.names) {
			return
		}
		tw.shown[idx] = !tw.shown[idx]
		list.Items[idx] = toolsOptionsRowText(tw.shown[idx], tw.names[idx])
		list.UpdateRows()
	}
	list.OnKeyDown = func(e *vtinput.InputEvent) bool {
		switch {
		case e.VirtualKeyCode == vtinput.VK_SPACE || e.Char == ' ':
			list.onToggle(list.SelectPos)
			return true
		case e.VirtualKeyCode == vtinput.VK_RETURN:
			if run, ok := configFor(list.SelectPos); ok {
				run()
			} else {
				list.onToggle(list.SelectPos)
			}
			return true
		}
		return false
	}
	dlg.AddItem(list)

	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	if tw.configure != nil {
		settings = vtui.NewButton(0, 0, settingsLabel)
		settings.IsDefault = false
		settings.OnClick = func() {
			if run, ok := configFor(list.SelectPos); ok {
				run()
			}
		}
		dlg.AddItem(settings)
		buttons.Add(settings, vtui.Margins{Right: 1}, vtui.AlignTop)
		refreshButton(0)
	}
	if tw.extra != nil {
		extra := vtui.NewButton(0, 0, tw.extraLabel)
		extra.IsDefault = false
		extra.OnClick = tw.extra
		dlg.AddItem(extra)
		buttons.Add(extra, vtui.Margins{}, vtui.AlignTop)
	}

	sep := vtui.NewSeparator(0, 0, width, true, true)
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	btnOk.OnClick = func() {
		if err := tw.apply(tw.shown); err != nil {
			vtui.ShowMessage(tw.title, err.Error(), []string{i18n.Msg("vtui.Ok")})
			return
		}
		dlg.SetExitCode(1)
		if tw.applied != nil {
			tw.applied()
		}
	}
	btnCancel.OnClick = func() { dlg.SetExitCode(-1) }
	dlg.AddItem(sep)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)
	okRow := vtui.NewHBoxLayout(0, 0, width-4, 1)
	okRow.HorizontalAlign = vtui.AlignCenter
	okRow.Spacing = 2
	okRow.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	okRow.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+1, width-4, height-2)
	vbox.Add(list, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(sep, vtui.Margins{Left: -2, Right: -2}, vtui.AlignFill)
	vbox.Add(okRow, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()
	dlg.SetFocusedItem(list)

	vtui.FrameManager.Push(dlg)
}

// ShowToolsOptions opens the window of the F11 menu's plugins: every entry with a
// check box, Ok to store the choice (applied then runs), and a Settings button
// that is live when the selected plugin has settings of its own (f4#918).
func (pf *PanelsFrame) ShowToolsOptions(applied func()) {
	entries := PluginMenuEntriesSnapshot()
	hidden := hiddenPluginMenuEntries()
	tw := &toolsWindow{
		title:   i18n.Msg("Plugins.ToolsOptionsTitle"),
		empty:   i18n.Msg("Plugins.ToolsOptionsEmpty"),
		applied: applied,
		apply: func(shown []bool) error {
			for i, entry := range entries {
				name := entry.ActionName
				if hidden[name] == !shown[i] {
					continue
				}
				if err := SetPluginMenuEntryHidden(name, !shown[i]); err != nil {
					return err
				}
				hidden[name] = !shown[i]
			}
			return nil
		},
		configure: func(idx int) (func(), bool) {
			if entries[idx].CommandID == "" {
				return nil, false
			}
			command, ok := pluginConfigCommandFor(entries[idx].CommandID, pf)
			if !ok {
				return nil, false
			}
			return func() { plughost.ExecutePluginCommand(vfs.PluginCommandConfig, command.ID, pf) }, true
		},
	}
	for _, entry := range entries {
		tw.names = append(tw.names, action.PlainLabel(entry.Label))
		tw.shown = append(tw.shown, !hidden[entry.ActionName])
	}
	tw.show()
}
