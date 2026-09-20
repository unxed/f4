package settings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/plughost"
	"github.com/unxed/f4/internal/tarindexcache"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type settingsOperationsProvider struct{}

func (settingsOperationsProvider) Catalog() f4settings.Catalog {
	cat := f4settings.Catalog{ID: "operations", Categories: Categories}
	add := func(id, category, group, label, desc string, background bool, run func(context.Context) error) {
		cat.Commands = append(cat.Commands, f4settings.Command{ID: id, Category: category, Group: group, Label: f4settings.Text{English: label}, Description: f4settings.Text{English: desc}, Background: background, Run: run})
	}
	add("save.preferences", "workspaces", "Manual saving", "Save applied preferences", "Write the applied configuration even when automatic saving is disabled.", false, func(context.Context) error { return config.SaveAppliedConfiguration() })
	add("save.session", "workspaces", "Manual saving", "Save session", "Save workspaces, panel state and remembered operation inputs using the applied path-restoration policy.", false, func(context.Context) error { return host.SaveSession() })
	add("save.geometry", "workspaces", "Manual saving", "Save window geometry", "Capture and save the current graphical window dimensions and position independently of other settings.", false, func(context.Context) error {
		return host.SaveGeometry()
	})
	add("colors.export", "appearance", "Theme", "Export applied colors", "Write the complete applied palette to farcolors.ini in the current configuration directory.", false, func(context.Context) error { return theme.ExportColors(theme.UserColorOverridesPath()) })
	add("syntax.reload", "syntax", "Colorer", "Reload schemas", "Load the applied Colorer configuration, report what Colorer finds wrong with it, then drop cached sessions, regions and scheme so subsequent highlighting uses it.", true, func(ctx context.Context) error {
		check := editor.CheckColorerSource(ctx, editor.CurrentColorerSource(), config.App.EditorColorerScheme, false, nil)
		if check.Err == nil {
			editor.ResetColorerSessions()
			editor.ResetColorerRegions()
			editor.ResetColorerScheme()
		}
		return colorerCheckError(check)
	})
	add("archives.clearTarIndexes", "panels", "Directory loading", "Rebuild all tar indexes", "Delete every cached tar archive index. Each is built again the next time its archive is opened.", false, func(context.Context) error {
		tarindexcache.Clear()
		return nil
	})
	add("syntax.check", "syntax", "Colorer", "Check all schemes", "Load the scheme of every Colorer file type in the applied configuration and report the first one Colorer cannot load, and what it reports on the way. Applies nothing.", true, func(ctx context.Context) error {
		// Every file type is loaded in turn and that takes a while; the status
		// line says which one it is at, or the window looks hung (#277).
		return colorerCheckError(editor.CheckColorerSource(ctx, editor.CurrentColorerSource(), config.App.EditorColorerScheme, true, func(done, total int, label string) {
			reportSettingsProgress(ctx, fmt.Sprintf("%d/%d  %s", done+1, total, label))
		}))
	})
	add("syntax.download", "syntax", "Colorer", "Download schemas", "Download and validate the Colorer schema archive, then install it at the applied configuration directory.", true, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", editor.ColorerDownloadURL, nil)
		if err != nil {
			return err
		}
		resp, err := netproxy.HTTPClient(0).Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }() // The completed read is validated below.
		if resp.StatusCode != 200 {
			return settingsError("schema download: HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, editor.MaxColorerDownload+1))
		if err != nil {
			return err
		}
		if len(data) > editor.MaxColorerDownload {
			return settingsError("schema archive is too large")
		}
		return editor.InstallColorerSchemas(data, editor.ColorerConfigsDir(), ctx)
	})
	add("updates.check", "updates", "Updates", "Check now", "Check the applied release channel now. An available update is offered as a separate installation operation.", true, host.CheckUpdates)
	for _, enable := range []bool{true, false} {
		for _, move := range []bool{false, true} {
			target := dialog.SystemProfileDir()
			name := "user profile"
			if enable {
				target = dialog.PortableProfileDir()
				name = "portable profile"
			}
			verb := "Copy"
			if move {
				verb = "Move"
			}
			id := fmt.Sprintf("profile.%t.%t", enable, move)
			add(id, "startup", "Profile transfer", verb+" to "+name, verb+" the applied profile from %s to %s and select it for the next launch. Restart is required. This operation is not undone by Cancel.", false, func(context.Context) error {
				dlg := vtui.ShowMessage(Phrase("Profile transfer"), fmt.Sprintf(Phrase(verb+" profile to:\n%s?\nRestart is required after completion."), target), []string{Phrase(verb), i18n.Msg("vtui.Cancel")})
				dlg.OnResult = func(code int) {
					if code != 0 {
						return
					}
					if err := config.SaveAppliedConfiguration(); err != nil {
						vtui.ShowMessage(Phrase("Profile transfer"), err.Error(), []string{i18n.Msg("vtui.Ok")})
						return
					}
					src, iniPath := config.GetF4ConfigDir(), dialog.CurrentPortableIniPath()
					vtui.RunAsync(func(task *vtui.TaskContext) {
						err := dialog.TransferProfile(iniPath, src, target, enable, move)
						task.RunOnUI(func() {
							message := Phrase("Profile transferred. Restart f4 to use it.")
							if err != nil {
								message = err.Error()
							}
							vtui.ShowMessage(Phrase("Profile transfer"), message, []string{i18n.Msg("vtui.Ok")})
						})
					})
				}
				return nil
			})
			cat.Commands[len(cat.Commands)-1].Description.Args = []any{config.GetF4ConfigDir(), target}
		}
	}
	// Legacy external plugins keep explicit launchers, resolved against the live registry.
	for _, cmd := range plughost.PluginCommandsSnapshot(vfs.PluginCommandConfig, panel.FindPanelsFrameAnyScreen()) {
		if _, bundled := bundledSettingsCommands[strings.ToLower(cmd.ID)]; bundled {
			continue
		}
		id := cmd.ID
		add("legacy."+id, "plugins", "Legacy configuration", "Legacy configuration: "+action.PlainLabel(plughost.PluginCommandDisplayLabel(cmd)), "This external plugin has not contributed settings metadata. Opens its own configuration interface using applied preferences.", false, func(context.Context) error {
			pf := panel.FindPanelsFrameAnyScreen()
			if pf == nil {
				return settingsError("this legacy plugin requires an open panels workspace")
			}
			if !plughost.ExecutePluginCommand(vfs.PluginCommandConfig, id, pf) {
				return settingsError("plugin is no longer available")
			}
			return nil
		})
		cat.Commands[len(cat.Commands)-1].Label = f4settings.Text{English: "Legacy configuration: %s", Args: []any{action.PlainLabel(plughost.PluginCommandDisplayLabel(cmd))}}
	}
	for _, item := range []struct{ id, label, value string }{{"profile.path", "Current configuration directory", config.GetF4ConfigDir()}, {"profile.ini", "Main settings file", config.GetUserConfigIniPath()}, {"profile.session", "Session file", host.SessionPath()}, {"profile.portable", "Portable profile directory", dialog.PortableProfileDir()}, {"profile.system", "User profile directory", dialog.SystemProfileDir()}} {
		f := f4settings.Scalar(item.id, "startup", "Configuration locations", item.label, "Resolved configuration location for the running process. Profile transfers take effect on restart.", f4settings.Path)
		f.Unavailable = "Informational location"
		f.Default = item.value
		cat.Fields = append(cat.Fields, f)
	}
	status := f4settings.Scalar("updates.status", "updates", "Updates", "Last startup/manual check", "Timestamp of the latest attempted update check. Frequency is evaluated on application startup, not by a continuously running timer.", f4settings.String)
	status.Default = "Never"
	if config.App.LastUpdateCheck != 0 {
		status.Default = time.Unix(config.App.LastUpdateCheck, 0).Format(time.RFC1123)
	}
	status.Unavailable = "Status"
	cat.Fields = append(cat.Fields, status)
	for i := range cat.Commands {
		cmd := &cat.Commands[i]
		switch {
		case strings.HasPrefix(cmd.ID, "profile.") || strings.HasPrefix(cmd.ID, "legacy."):
			cmd.Requires = []string{"*"}
		case cmd.ID == "colors.export":
			cmd.Requires = []string{"ColorStyle", "EnforceColorCorrection"}
		case strings.HasPrefix(cmd.ID, "syntax."):
			cmd.Requires = []string{"EditorColorerCatalog", "EditorColorerUserHrc", "EditorColorerUserHrd", "EditorColorerScheme", "ProxyMode", "ProxyHost", "ProxyPort", "ProxyUser", "ProxyPass"}
		case cmd.ID == "updates.check":
			cmd.Requires = []string{"UpdateChannel", "ProxyMode", "ProxyHost", "ProxyPort", "ProxyUser", "ProxyPass"}
		}
	}
	return cat
}
func (p settingsOperationsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	values := map[string]string{}
	for _, f := range p.Catalog().Fields {
		values[f.ID] = f.Default
	}
	return f4settings.NewDraft(values, nil), nil
}

var bundledSettingsCommands = map[string]string{"visren.configure": "operations", "f4.envman.configure": "terminal", "f4.mediainfo.configure": "metadata"}

func RunOnUI(ctx context.Context, run func()) {
	if task, ok := ctx.(*vtui.TaskContext); ok {
		done := make(chan struct{})
		task.RunOnUI(func() { defer close(done); run() })
		<-done
	} else {
		run()
	}
}

// colorerCheckError reports a Colorer check as a command's result: nil when it
// is clean, otherwise why it failed followed by what Colorer reported.
// settingsProgressKey carries, in the context of an operation started from the
// Settings Center, the function that puts a line of progress in its status row.
type settingsProgressKey struct{}

// reportSettingsProgress shows text in the status row while the operation runs.
// It is safe to call from the operation's goroutine, and does nothing when the
// operation was not started by the Settings Center.
func reportSettingsProgress(ctx context.Context, text string) {
	if report, ok := ctx.Value(settingsProgressKey{}).(func(string)); ok {
		report(text)
	}
}

func colorerCheckError(check editor.ColorerCheck) error {
	if check.Clean() {
		return nil
	}
	var parts []string
	if check.Err != nil {
		parts = append(parts, check.Err.Error())
	}
	parts = append(parts, check.Reports...)
	return errors.New(strings.Join(parts, "\n"))
}
