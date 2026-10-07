package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/unxed/f4/internal/panel"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// The regions FarColorer reads the cross colors from.
const (
	colorerHorzCrossRegion = "def:HorzCross"
	colorerVertCrossRegion = "def:VertCross"
)

// colorerCrossModeItems lists the "Show cross" choices in the order the
// ColorerCross* constants define, so that the combo box position is the mode.
func colorerCrossModeItems() []string {
	return []string{
		i18n.Msg("ColorerSettings.CrossOff"),
		i18n.Msg("ColorerSettings.CrossVertical"),
		i18n.Msg("ColorerSettings.CrossHorizontal"),
		i18n.Msg("ColorerSettings.CrossBoth"),
		i18n.Msg("ColorerSettings.CrossScheme"),
	}
}

// colorerIsActive reports whether Colorer is the highlighter in charge.
func colorerIsActive() bool {
	return strings.EqualFold(config.App.EditorHighlighter, "Colorer")
}

// crossModeAxes splits a cross mode into its horizontal and vertical parts.
func crossModeAxes(mode int) (horz, vert bool) {
	switch mode {
	case config.ColorerCrossVertical:
		return false, true
	case config.ColorerCrossHorizontal:
		return true, false
	case config.ColorerCrossBoth, config.ColorerCrossScheme:
		// In the scheme mode the editor narrows the axes to the file type's.
		return true, true
	}
	return false, false
}

// colorerCrossAttr resolves one of the cross regions of the active color
// style. The lookup is exact, the way the editor background one is: a cross
// color guessed from an unrelated region would paint a stripe across the whole
// editor, so a style without the region keeps the f4 palette.
func colorerCrossAttr(region string, base uint64) uint64 {
	if !colorerIsActive() {
		return base
	}
	rd := editor.ColorerGetRegionDefine(region)
	if rd == nil {
		return base
	}
	attr := base
	if rd.IsForeSet {
		attr = vtui.SetRGBFore(attr, rd.Fore)
	}
	if rd.IsBackSet {
		attr = vtui.SetRGBBack(attr, rd.Back)
	}
	return attr
}

// EditorCrossAttrs tells the editor which cross lines to draw and in which
// colors. The crosshair checkbox stays the master switch, the mode only picks
// the axes.
func EditorCrossAttrs() (horz, vert bool, horzAttr, vertAttr uint64) {
	if !config.App.EditorCrosshair {
		return false, false, 0, 0
	}
	horz, vert = crossModeAxes(config.App.EditorCrossMode)
	if !horz && !vert {
		return false, false, 0, 0
	}
	base := vtui.Palette[theme.ColEditorCrosshair]
	return horz, vert,
		colorerCrossAttr(colorerHorzCrossRegion, base),
		colorerCrossAttr(colorerVertCrossRegion, base)
}

// colorerCheckMessage turns a check into the text of a message box. An empty
// title means there is nothing to say.
func colorerCheckMessage(check editor.ColorerCheck, allTypes bool) (title, text string, kind vtui.MessageKind) {
	var b strings.Builder
	kind = vtui.MessageWarn
	if check.Err != nil {
		b.WriteString(i18n.Msg("ColorerSettings.CheckFailed"))
		b.WriteString("\n")
		b.WriteString(check.Err.Error())
	}
	if len(check.Reports) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(i18n.Msg("ColorerSettings.CheckReports"))
		for _, report := range check.Reports {
			b.WriteString("\n")
			b.WriteString(report)
		}
	}
	if b.Len() == 0 {
		if !allTypes {
			return "", "", vtui.MessageInfo
		}
		fmt.Fprintf(&b, i18n.Msg("ColorerSettings.CheckPassed"), check.Types)
		kind = vtui.MessageInfo
	}
	return i18n.Msg("ColorerSettings.CheckTitle"), b.String(), kind
}

// runColorerCheck loads a Colorer configuration off the UI thread behind a
// progress dialog, shows what the check found, and hands the result to done on
// the UI thread. A check the user cancelled shows nothing and is not passed on.
func runColorerCheck(pf *panel.PanelsFrame, src editor.ColorerSource, scheme string, allTypes bool, done func(editor.ColorerCheck)) {
	var check editor.ColorerCheck
	pf.RunProgressTask(i18n.Msg("ColorerSettings.CheckTitle"), i18n.Msg("ColorerSettings.Checking"), false,
		func(ctx context.Context, update func(msg string, percent int)) error {
			// DIAG (temporary): which dialog-layout subtest starts a Colorer
			// check, and whether two overlap; read against the === RUN lines.
			println("DIAG runColorerCheck: start")
			check = editor.CheckColorerSource(ctx, src, scheme, allTypes, func(n, total int, label string) {
				update(label, n*100/total)
			})
			println("DIAG runColorerCheck: done")
			return nil
		},
		func(error) {
			if editor.IsColorerCheckCancelled(check) {
				return
			}
			if title, text, kind := colorerCheckMessage(check, allTypes); title != "" {
				vtui.ShowMessageEx(title, text, []string{i18n.Msg("vtui.Ok")}, kind)
			}
			if done != nil {
				done(check)
			}
		})
}

// actionColorerReloadBase is FarColorer's "Reload base" in the editor's
// Colorer menu: the configuration in use is loaded and checked, as the
// settings dialog's Reload does, and, when it loads, Colorer starts afresh in
// the open editors.
func actionColorerReloadBase(pf *panel.PanelsFrame) {
	runColorerCheck(pf, editor.CurrentColorerSource(), config.App.EditorColorerScheme, false, func(check editor.ColorerCheck) {
		if check.Err != nil {
			return
		}
		reloadColorerEditors()
	})
}

// reloadColorerEditors applies a configuration accepted by the settings
// dialog to editors that were already open. Without this, changing the user
// HRC directory and pressing OK only saved the path: existing editors kept
// their sessions and never saw the new schemes until a separate Reload.
func reloadColorerEditors() {
	editor.ResetColorerSessions()
	editor.ResetColorerRegions()
	editor.ReloadColorerEditors()
}

func actionColorerSettings(pf *panel.PanelsFrame) {
	width, height := 74, 21
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("ColorerSettings.Title"))
	dlg.ShowClose = true

	// 1. Initialize Widgets
	chkEnabled := vtui.NewCheckbox(0, 0, i18n.Msg("ColorerSettings.Enabled"), false)
	if colorerIsActive() {
		chkEnabled.State = 1
	}
	chkPairs := vtui.NewCheckbox(0, 0, i18n.Msg("ColorerSettings.Pairs"), false)
	if config.App.EditorColorerPairs {
		chkPairs.State = 1
	}
	chkOldOutline := vtui.NewCheckbox(0, 0, i18n.Msg("ColorerSettings.OldOutline"), false)
	if config.App.EditorColorerOldOutline {
		chkOldOutline.State = 1
	}

	// The catalog carries a machine name and a human description; the machine
	// name is what the config stores, so the two lists are kept in step.
	schemeNames := []string{}
	schemeItems := []string{}
	for _, scheme := range editor.ListColorerSchemes() {
		schemeNames = append(schemeNames, scheme.Name)
		schemeItems = append(schemeItems, editor.ColorerSchemeLabel(scheme))
	}
	if len(schemeItems) == 0 {
		schemeNames = append(schemeNames, "")
		schemeItems = append(schemeItems, "")
	}
	selectedScheme := 0
	for i := 0; i < len(schemeNames); i++ {
		if strings.EqualFold(schemeNames[i], config.App.EditorColorerScheme) {
			selectedScheme = i
			break
		}
	}
	comboScheme := vtui.NewComboBox(0, 0, 44, schemeItems)
	comboScheme.DropdownOnly = true
	comboScheme.Menu.SetSelectPos(selectedScheme)
	comboScheme.Edit.SetText(schemeItems[selectedScheme])
	lblScheme := vtui.NewLabel(0, 0, i18n.Msg("ColorerSettings.Style"), comboScheme)

	crossItems := colorerCrossModeItems()
	crossPos := config.App.EditorCrossMode
	if crossPos < 0 || crossPos >= len(crossItems) {
		crossPos = config.ColorerCrossBoth
	}
	comboCross := vtui.NewComboBox(0, 0, 44, crossItems)
	comboCross.DropdownOnly = true
	comboCross.Menu.SetSelectPos(crossPos)
	comboCross.Edit.SetText(crossItems[crossPos])
	lblCross := vtui.NewLabel(0, 0, i18n.Msg("ColorerSettings.Cross"), comboCross)

	chkSyntax := vtui.NewCheckbox(0, 0, i18n.Msg("ColorerSettings.Syntax"), false)
	if config.App.EditorColorerSyntax {
		chkSyntax.State = 1
	}

	chkBackground := vtui.NewCheckbox(0, 0, i18n.Msg("ColorerSettings.Background"), false)
	if config.App.EditorColorerBackground {
		chkBackground.State = 1
	}

	// The paths: FarColorer's catalog, user file of schemes, user file of
	// color styles and user HRC settings, each label beside its field.
	pathLabels := padLabels(i18n.Msg("ColorerSettings.Catalog"), i18n.Msg("ColorerSettings.UserHrc"), i18n.Msg("ColorerSettings.UserHrd"), i18n.Msg("ColorerSettings.UserHrcSettings"))
	pathField := func(value, label string) (*vtui.Edit, *vtui.Text) {
		edit := vtui.NewEdit(0, 0, width-6, value)
		edit.ClearSelection()
		return edit, vtui.NewLabel(0, 0, label, edit)
	}
	editCatalog, lblCatalog := pathField(config.App.EditorColorerCatalog, pathLabels[0])
	editUserHrc, lblUserHrc := pathField(config.App.EditorColorerUserHrc, pathLabels[1])
	editUserHrd, lblUserHrd := pathField(config.App.EditorColorerUserHrd, pathLabels[2])
	editUserHrcSettings, lblUserHrcSettings := pathField(config.App.EditorColorerHrcSettings, pathLabels[3])
	btnTypeSettings := vtui.NewButton(0, 0, i18n.Msg("ColorerSettings.TypeSettings"))

	btnReload := vtui.NewButton(0, 0, i18n.Msg("ColorerSettings.Reload"))
	btnCheckAll := vtui.NewButton(0, 0, i18n.Msg("ColorerSettings.CheckAll"))
	btnDownload := vtui.NewButton(0, 0, i18n.Msg("ColorerSettings.Download"))
	btnOk := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	btnOk.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	// 2. Add to Dialog in desired focus order
	dlg.AddItem(chkEnabled)
	dlg.AddItem(chkPairs)
	dlg.AddItem(chkOldOutline)
	dlg.AddItem(lblScheme)
	dlg.AddItem(comboScheme)
	dlg.AddItem(lblCross)
	dlg.AddItem(comboCross)
	dlg.AddItem(chkSyntax)
	dlg.AddItem(chkBackground)
	dlg.AddItem(lblCatalog)
	dlg.AddItem(editCatalog)
	dlg.AddItem(lblUserHrc)
	dlg.AddItem(editUserHrc)
	dlg.AddItem(lblUserHrd)
	dlg.AddItem(editUserHrd)
	dlg.AddItem(lblUserHrcSettings)
	dlg.AddItem(editUserHrcSettings)
	dlg.AddItem(btnReload)
	dlg.AddItem(btnCheckAll)
	dlg.AddItem(btnDownload)
	dlg.AddItem(btnTypeSettings)
	dlg.AddItem(btnOk)
	dlg.AddItem(btnCancel)

	// 3. Layout Configuration
	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)

	rowEnabled := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowEnabled.Add(chkEnabled, vtui.Margins{Right: 2}, vtui.AlignLeft)
	rowEnabled.Add(chkPairs, vtui.Margins{Right: 2}, vtui.AlignLeft)
	rowEnabled.Add(chkOldOutline, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowEnabled, vtui.Margins{}, vtui.AlignFill)

	rowScheme := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowScheme.Add(lblScheme, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowScheme.Add(comboScheme, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowScheme, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowCross := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowCross.Add(lblCross, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowCross.Add(comboCross, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowCross, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowChecks := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowChecks.Add(chkSyntax, vtui.Margins{Right: 2}, vtui.AlignLeft)
	rowChecks.Add(chkBackground, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)

	for i, path := range []struct {
		lbl  *vtui.Text
		edit *vtui.Edit
	}{{lblCatalog, editCatalog}, {lblUserHrc, editUserHrc}, {lblUserHrd, editUserHrd}, {lblUserHrcSettings, editUserHrcSettings}} {
		r := vtui.NewHBoxLayout(0, 0, width-4, 1)
		r.Add(path.lbl, vtui.Margins{Right: 1}, vtui.AlignLeft)
		r.Add(path.edit, vtui.Margins{}, vtui.AlignFill)
		top := 0
		if i == 0 {
			top = 1
		}
		vbox.Add(r, vtui.Margins{Top: top}, vtui.AlignFill)
	}

	rowTools := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTools.HorizontalAlign = vtui.AlignCenter
	rowTools.Spacing = 2
	rowTools.Add(btnReload, vtui.Margins{}, vtui.AlignTop)
	rowTools.Add(btnCheckAll, vtui.Margins{}, vtui.AlignTop)
	rowTools.Add(btnDownload, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowTools, vtui.Margins{Top: 1}, vtui.AlignFill)
	rowTypes := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTypes.HorizontalAlign = vtui.AlignCenter
	rowTypes.Add(btnTypeSettings, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowTypes, vtui.Margins{}, vtui.AlignFill)

	rowButtons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowButtons.HorizontalAlign = vtui.AlignCenter
	rowButtons.Spacing = 2
	rowButtons.Add(btnOk, vtui.Margins{}, vtui.AlignTop)
	rowButtons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowButtons, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Apply()

	// 4. Logic
	apply := func() {
		if chkEnabled.State == 1 {
			config.App.EditorHighlighter = "Colorer"
		} else if colorerIsActive() {
			config.App.EditorHighlighter = "Chroma"
		}
		config.App.EditorColorerScheme = ""
		if pos := comboScheme.Menu.SelectPos; pos > 0 && pos < len(schemeNames) {
			config.App.EditorColorerScheme = schemeNames[pos]
		}
		config.App.EditorCrossMode = comboCross.Menu.SelectPos
		config.App.EditorColorerSyntax = chkSyntax.State == 1
		config.App.EditorColorerPairs = chkPairs.State == 1
		config.App.EditorColorerOldOutline = chkOldOutline.State == 1
		config.App.EditorColorerBackground = chkBackground.State == 1
		config.App.EditorColorerCatalog = strings.TrimSpace(editCatalog.GetText())
		config.App.EditorColorerUserHrc = strings.TrimSpace(editUserHrc.GetText())
		config.App.EditorColorerUserHrd = strings.TrimSpace(editUserHrd.GetText())
		config.App.EditorColorerHrcSettings = strings.TrimSpace(editUserHrcSettings.GetText())
		// The catalog may now point somewhere else, so the styles are dropped
		// instead of being kept under the same name.
		editor.ResetColorerScheme()
		editor.SetColorerScheme(config.App.EditorColorerScheme)
		config.SaveConfig()
	}

	// What the dialog would apply: the configuration a check has to load.
	pending := func() (editor.ColorerSource, string) {
		src := editor.ColorerSource{
			ConfigsDir:      strings.TrimSpace(editCatalog.GetText()),
			UserHRC:         strings.TrimSpace(editUserHrc.GetText()),
			UserHRD:         strings.TrimSpace(editUserHrd.GetText()),
			UserHRCSettings: strings.TrimSpace(editUserHrcSettings.GetText()),
		}
		if src.ConfigsDir == "" {
			src.ConfigsDir = editor.DefaultColorerConfigsDir()
		}
		scheme := ""
		if pos := comboScheme.Menu.SelectPos; pos > 0 && pos < len(schemeNames) {
			scheme = schemeNames[pos]
		}
		return src, scheme
	}

	btnCancel.OnClick = func() { dlg.Close() }

	// FarColorer's "HRC settings": parameters of each file type, from the
	// configuration the dialog would apply.
	btnTypeSettings.OnClick = func() {
		src, _ := pending()
		actionColorerTypeSettings(src)
	}

	// As FarColorer's OK does: a changed configuration is loaded first, and one
	// Colorer cannot load keeps the dialog open. What Colorer merely reports is
	// shown and does not stop the change.
	btnOk.OnClick = func() {
		src, scheme := pending()
		changed := src != editor.CurrentColorerSource() || !strings.EqualFold(scheme, config.App.EditorColorerScheme) || !colorerIsActive()
		if chkEnabled.State != 1 || !changed {
			apply()
			dlg.Close()
			return
		}
		runColorerCheck(pf, src, scheme, false, func(check editor.ColorerCheck) {
			if check.Err != nil {
				return
			}
			apply()
			reloadColorerEditors()
			dlg.Close()
		})
	}

	btnReload.OnClick = func() {
		src, scheme := pending()
		runColorerCheck(pf, src, scheme, false, func(check editor.ColorerCheck) {
			if check.Err != nil {
				return
			}
			apply()
			reloadColorerEditors()
		})
	}

	// FarColorer's "Reload all": every file type's scheme is loaded, so a
	// scheme that breaks only when its type is used is found now. It applies
	// nothing.
	btnCheckAll.OnClick = func() {
		src, scheme := pending()
		runColorerCheck(pf, src, scheme, true, nil)
	}

	btnDownload.OnClick = func() {
		apply()
		dlg.Close()
		editor.DownloadColorerSchemas(pf, func(success bool) {
			if !success {
				return
			}
			editor.ResetColorerSessions()
			editor.ResetColorerRegions()
			editor.ResetColorerScheme()
			editor.SetColorerScheme(config.App.EditorColorerScheme)
			editor.ReloadColorerEditors()
		})
	}

	vtui.FrameManager.Push(dlg)
}
