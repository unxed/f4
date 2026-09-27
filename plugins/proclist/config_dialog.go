//go:build linux || windows || darwin

package proclist

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// configDialogWidth is clamped to the screen the same way
// plugins/mediainfo/config_dialog.go clamps its own settings dialog. Height
// depends on how many columns allColumnSpecs (panel.go) lists, so it is
// computed in configure itself.
const configDialogWidth = 50

// configure is ProcList.Config's F9 "Plugin configuration" entry
// (plugin.go's own RegisterPluginCommand call): a checkbox per
// allColumnSpecs entry for Settings.VisibleColumns, and an edit field for
// Settings.RefreshIntervalMS. It otherwise follows
// plugins/mediainfo/config_dialog.go's dialog/validate/save shape.
func (p *Plugin) configure(vfs.App) {
	store := p.settingsStore()
	settings := store.snapshot()

	height := 8 + len(allColumnSpecs)
	width := configDialogWidth
	if vtui.FrameManager != nil {
		if maximum := vtui.FrameManager.GetScreenSize() - 2; maximum > 20 && width > maximum {
			width = maximum
		}
		if maximum := vtui.FrameManager.GetScreenHeight() - 2; maximum > 10 && height > maximum {
			height = maximum
		}
	}

	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("ProcList.Config.Title"))
	dlg.ShowClose = true

	x := dlg.X1 + 2
	y := dlg.Y1 + 2
	dlg.AddItem(vtui.NewText(x, y, i18n.Msg("ProcList.Config.ColumnsLabel"), 0))
	y++

	visible := make(map[string]bool, len(settings.VisibleColumns))
	for _, k := range settings.VisibleColumns {
		visible[k] = true
	}

	checkboxes := make([]*vtui.Checkbox, len(allColumnSpecs))
	for i, c := range allColumnSpecs {
		cb := vtui.NewCheckbox(x, y, i18n.Msg(c.titleKey), false)
		cb.State = boolInt(visible[c.key])
		dlg.AddItem(cb)
		checkboxes[i] = cb
		y++
	}
	y++

	refreshLabel := i18n.Msg("ProcList.Config.RefreshLabel")
	dlg.AddItem(vtui.NewText(x, y, refreshLabel, 0))
	labelWidth := vtui.StringWidth(refreshLabel) + 1
	intervalEdit := vtui.NewEdit(x+labelWidth, y, width-4-labelWidth, strconv.Itoa(settings.RefreshIntervalMS))
	dlg.AddItem(intervalEdit)

	saveButton := vtui.NewButton(0, 0, i18n.Msg("ProcList.Config.SaveBtn"))
	cancelButton := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	saveButton.IsDefault = true
	dlg.AddItem(saveButton)
	dlg.AddItem(cancelButton)

	hbox := vtui.NewHBoxLayout(dlg.X1+2, dlg.Y2-2, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(saveButton, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(cancelButton, vtui.Margins{}, vtui.AlignTop)
	hbox.Apply()

	showSaveError := func(err error) {
		vtui.ShowMessageOn(dlg, i18n.Msg("ProcList.Config.Error"), err.Error(), []string{i18n.Msg("vtui.Ok")})
	}

	saveButton.OnClick = func() {
		var keys []string
		for i, c := range allColumnSpecs {
			if checkboxes[i].State == 1 {
				keys = append(keys, c.key)
			}
		}
		ms, err := strconv.Atoi(strings.TrimSpace(intervalEdit.GetText()))
		if err != nil {
			showSaveError(fmt.Errorf("%s", i18n.Msg("ProcList.Config.ErrInterval")))
			return
		}
		next := normalizeSettings(Settings{VisibleColumns: keys, RefreshIntervalMS: ms})
		if err := next.validate(); err != nil {
			showSaveError(err)
			return
		}
		if err := store.save(next); err != nil {
			showSaveError(err)
			return
		}
		dlg.Close()
	}
	cancelButton.OnClick = dlg.Close
	dlg.SetFocusedItem(saveButton)
	vtui.FrameManager.Push(dlg)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
