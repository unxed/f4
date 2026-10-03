package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/settings"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/update"
	"github.com/unxed/vtui"
)

type settingsHost struct{}

func (settingsHost) ApplyRuntime(before config.F4Config, changed []string) {
	config.ApplyProxySettings()
	config.ApplyWheelSettings()
	config.ApplyMenuSettings()
	panel.ApplyPathHintSettings()
	config.ApplyCursorSettings()
	ApplyGlyphStyle()
	for _, id := range changed {
		if id == "ColorStyle" || id == "EnforceColorCorrection" {
			_ = theme.ApplyColorStyle(config.App.ColorStyle)
		}
		if strings.HasPrefix(id, "EditorColorer") {
			editor.ResetColorerSessions()
			editor.ResetColorerRegions()
			editor.ResetColorerScheme()
		}
	}
	if before.Language != config.App.Language || before.HelpLanguage != config.App.HelpLanguage || before.UseLocalLanguageFiles != config.App.UseLocalLanguageFiles {
		initLang()
		InitHelpSystem()
	}
	if fm := vtui.FrameManager; fm != nil {
		mode := vtui.WorkspaceCtrlTabDirect
		if config.App.CtrlTabShowsMenu {
			mode = vtui.WorkspaceCtrlTabMenu
		}
		fm.ConfigureWorkspaceTabs(vtui.WorkspaceTabMode(config.App.WorkspaceTabMode), mode)
		fm.ConfigureWorkspaceTabOverlay(config.App.WorkspaceTabsOverlay)
		fm.ConfigureWorkspaceAltNumberSwitch(config.App.AltNumberSwitchesTabs)
		if before.WorkspaceTabNumbering != config.App.WorkspaceTabNumbering && config.App.WorkspaceTabNumbering == config.WorkspaceTabNumbersOrder {
			panel.RenumberWorkspaceScreens()
		}
		if before.AlwaysShowMenuBar != config.App.AlwaysShowMenuBar {
			// Editors and viewers place a pinned menu bar in ResizeConsole,
			// like the panels frames resized below (issue #1153).
			fm.ResizeAllScreens()
		}
		for _, screen := range fm.Screens {
			for _, frame := range screen.Frames {
				if pf, ok := frame.(*panel.PanelsFrame); ok && !pf.Closed {
					pf.CmdLine.Edit.PathHintsEnabled = config.App.CommandLineAutoComplete
					if before.NavigationMode != config.App.NavigationMode {
						pf.ApplyNavigationMode()
					}
					pf.ResizeConsole(pf.LastW, pf.LastH)
					pf.RefreshAll()
					for _, p := range pf.Panels {
						if fp, ok := p.(*panel.FileSystemPanel); ok {
							fp.SetGrouping(fp.GroupBy, fp.GroupReverse, fp.GroupFoldersSeparately)
						}
					}
				}
			}
		}
		fm.HardRefresh()
	}
}

func (settingsHost) CheckUpdates(ctx context.Context) error {
	var before, after config.F4Config
	settings.RunOnUI(ctx, func() { before = config.App; after = before; after.LastUpdateCheck = time.Now().Unix() })
	if err := config.WriteSettingsCandidate(before, after); err != nil {
		return err
	}
	settings.RunOnUI(ctx, func() { config.App.LastUpdateCheck = after.LastUpdateCheck })
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	candidate, err := update.Check(bounded, update.Settings{Channel: after.UpdateChannel, Interval: after.UpdateInterval, LastCheck: after.LastUpdateCheck, LastVersion: after.LastUpdateVersion}, currentBuild())
	if err != nil {
		return err
	}
	settings.RunOnUI(ctx, func() {
		if !candidate.NeedsUpdate {
			vtui.ShowMessage(settings.Phrase("Updates"), settings.Phrase("You are using the latest version."), []string{i18n.Msg("vtui.Ok")})
			return
		}
		pf := panel.FindPanelsFrameAnyScreen()
		if pf == nil {
			vtui.ShowMessage(settings.Phrase("Updates"), fmt.Sprintf(settings.Phrase("Update available: %s. Open a panels workspace to install it."), candidate.DisplayVersion), []string{i18n.Msg("vtui.Ok")})
			return
		}
		dlg := vtui.ShowMessage(settings.Phrase("Updates"), fmt.Sprintf(settings.Phrase("Download and install %s?"), candidate.DisplayVersion), []string{settings.Phrase("Install"), i18n.Msg("vtui.Cancel")})
		dlg.OnResult = func(code int) {
			if code == 0 {
				performUpdate(pf, candidate)
			}
		}
	})
	return nil
}

func (settingsHost) SaveGeometry() error {

	captureCurrentWindowSize()
	captureCurrentWindowPosition()
	path := config.GetUserConfigIniPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return config.WriteUserFileAtomically(path, config.UpdateIniValues(data, "Appearance", config.GuiWindowValues()), 0600)
}
func (settingsHost) SaveSession() error  { return saveSessionFileError(getSessionIniPath(), true, true) }
func (settingsHost) SessionPath() string { return getSessionIniPath() }

func (settingsHost) GuiBackends() []string { return startupGuiBackends }
func (settingsHost) PluginPackage(install bool, pf *panel.PanelsFrame, item plughost.PlugRingItem, refresh func()) {
	if install {
		// actionInstallPlugRingItem may wait synchronously for a confirmation
		// dialog through PanelsFrame.Message. This method is called from a UI
		// task by the Settings catalog, so the wait must not occupy that task
		// (f4#1710).
		go actionInstallPlugRingItem(pf, nil, item, refresh)
	} else {
		actionRemovePlugRingItem(pf, nil, item, refresh)
	}
}

// ApplyGlyphStyle gives the graphical backends the frame and control glyph set
// the GlyphStyle setting names (f4#285); text terminals ignore it.
func ApplyGlyphStyle() {
	style := vtui.GlyphStyleClassic
	if config.NormalizeGlyphStyle(config.App.GlyphStyle) == config.GlyphStyleRounded {
		style = vtui.GlyphStyleRounded
	}
	vtui.SetGlyphStyle(style)
}
