package settings

import (
	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/editor"
	"github.com/unxed/f4/internal/gui"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"

	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type coreSettingsProvider struct {
	// Shared by the draft and renderer for one opening; refreshed next time.
	catalog *f4settings.Catalog
}

func settingsConfigField(v reflect.Value, path string) reflect.Value {
	for _, part := range strings.Split(path, ".") {
		if v.Kind() == reflect.Array {
			i, _ := strconv.Atoi(part)
			v = v.Index(i)
		} else {
			v = v.FieldByName(part)
		}
	}
	return v
}
func coreSettingValue(cfg config.F4Config, id string) string {
	if id == "StartupMode" {
		return cfg.StartupMode.String()
	}
	if strings.HasPrefix(id, "DriveMenuOptions.") {
		bit, _ := strconv.Atoi(strings.TrimPrefix(id, "DriveMenuOptions."))
		return strconv.FormatBool(cfg.DriveMenuOptions&(1<<bit) != 0)
	}
	if id == "ConsoleMode" {
		return terminal.ConsoleViewStyleOf(terminal.ShellModeConfig{ConsoleMode: cfg.ConsoleMode, ConsoleOverlayUI: cfg.ConsoleOverlayUI})
	}
	v := settingsConfigField(reflect.ValueOf(cfg), id)
	switch v.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int:
		return strconv.FormatInt(v.Int(), 10)
	default:
		return v.String()
	}
}
func setCoreSetting(cfg *config.F4Config, id, value string) error {
	if id == "StartupMode" {
		cfg.StartupMode = config.ParseStartupMode(value)
		return nil
	}
	if strings.HasPrefix(id, "DriveMenuOptions.") {
		bit, _ := strconv.Atoi(strings.TrimPrefix(id, "DriveMenuOptions."))
		if value == "true" {
			cfg.DriveMenuOptions |= 1 << bit
		} else {
			cfg.DriveMenuOptions &^= 1 << bit
		}
		return nil
	}
	v := settingsConfigField(reflect.ValueOf(cfg).Elem(), id)
	switch v.Kind() {
	case reflect.Bool:
		n, err := strconv.ParseBool(value)
		if err != nil {
			return err
		}
		v.SetBool(n)
	case reflect.Int:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return err
		}
		v.SetInt(n)
	default:
		v.SetString(value)
	}
	return nil
}

// coreSettingsStaticFields is every core field with its label and
// description, before Catalog adds what it discovers at run time: installed
// fonts, colour styles, languages and renderers. f4:config reads the texts
// from here, so opening it scans nothing.
func coreSettingsStaticFields() []f4settings.Field {
	fields := coreSettingsFields()
	for _, area := range []string{"Panel", "Editor", "Viewer", "Menu", "Table"} {
		for _, direction := range []string{"Up", "Down"} {
			id := "Wheel" + area + direction
			fields = append(fields, f4settings.Field{ID: id, Category: "keyboard", Group: "Mouse wheel", Label: f4settings.Text{English: area + " wheel " + strings.ToLower(direction)}, Description: f4settings.Text{English: "Number of " + strings.ToLower(area) + " rows per " + strings.ToLower(direction) + "ward wheel notch. Zero follows the system setting."}, Kind: f4settings.Integer, Timing: "live"})
		}
	}
	// The fast-spin ramp behind Mouse wheel: a spin queues lines the view
	// then scrolls on its own, faster than the wheel itself reports them.
	// The ramp shape itself is tuned in code (see internal/wheel), so this
	// is the only knob it has.
	fields = append(fields, f4settings.Field{
		ID: "WheelAcceleration", Category: "keyboard", Group: "Mouse wheel",
		Label:       f4settings.Text{English: "Wheel acceleration"},
		Description: f4settings.Text{English: "How many lines the very fastest wheel notch queues on top of the rows it scrolls at once, from 1 (the ramp never queues anything) to 10."},
		Kind:        f4settings.Integer, Timing: "live",
	})
	for i, label := range []string{"Command history timestamps", "Folder history timestamps", "Viewer/editor history timestamps"} {
		fields = append(fields, f4settings.Field{ID: fmt.Sprintf("HistoryShowTimes.%d", i), Category: "history", Group: "Presentation", Label: f4settings.Text{English: label}, Description: f4settings.Text{English: "Choose the timestamp presentation independently for this history."}, Kind: f4settings.ChoiceKind, Choices: settingsChoices("0:Date and time;1:Date;2:None"), Timing: "new history dialogs"})
	}
	driveDescriptions := []string{"Add the drive-kind column.", "Display the filesystem volume label.", "This stored compatibility flag currently has no consumer.", "Display filesystem type.", "Display total and free capacity columns.", "Use fractional human-readable capacity instead of integer binary units.", "Display the mount target when different from the drive path.", "Include registered plugin drives and providers.", "Order plugin drive rows by their assigned shortcut.", "Include removable drives.", "Include optical drives.", "Include mapped and network drives.", "This stored compatibility flag has no consumer. SUBST detection runs independently.", "Include saved bookmarks and links."}
	for i, spec := range panel.DriveMenuOptionSpecs {
		f := f4settings.Field{ID: driveMenuOptionFieldID(spec.Label), Category: "drives", Group: "Drive entries", Label: f4settings.Text{English: strings.TrimPrefix(spec.Label, "Drive."), Key: spec.Label}, Description: f4settings.Text{English: driveDescriptions[i]}, Kind: f4settings.Boolean, Timing: "next drive menu"}
		if i == 2 || i == 12 {
			f.Unavailable = driveDescriptions[i]
		}
		fields = append(fields, f)
	}
	for i := range fields {
		f := &fields[i]
		if f.Label.Key == "" {
			f.Label.Key = "SettingsCenter." + f.ID + ".Label"
		}
		if f.Description.Key == "" {
			f.Description.Key = "SettingsCenter." + f.ID + ".Description"
		}
	}
	return fields
}

func (p coreSettingsProvider) Catalog() f4settings.Catalog {
	if p.catalog != nil {
		return *p.catalog
	}
	fields := coreSettingsStaticFields()
	for i := range fields {
		f := &fields[i]
		switch f.ID {
		case "Language", "HelpLanguage", "GuiFont", "ColorStyle", "GuiBackend", "EditorDefaultCodePage", "ViewerDefaultCodePage", "EditorColorerScheme":
			f.ChoicePresentation = "dropdown"
		}
		switch f.ID {
		case "GuiFont":
			f.Kind = f4settings.ChoiceKind
			f.AllowCustom = true
			installed := gui.DiscoverInstalledGuiFonts(config.App.Language)
			values := gui.GuiFontChoicesFromInstalled(config.App.GuiFont, installed)
			labels := gui.GuiFontDisplayValuesFromInstalled(values, installed)
			for i, value := range values {
				f.Choices = append(f.Choices, f4settings.Choice{Value: value, Label: f4settings.Text{English: labels[i], Literal: true}})
			}
		case "ColorStyle":
			f.Kind = f4settings.ChoiceKind
			for _, style := range theme.AvailableColorStyles() {
				f.Choices = append(f.Choices, f4settings.Choice{Value: style.Name, Label: f4settings.Text{English: style.Name, Literal: true}})
			}
		case "Language":
			f.Kind = f4settings.ChoiceKind
			for _, l := range i18n.ListAvailable(filepath.Join(config.GetF4ConfigDir(), "lang")) {
				f.Choices = append(f.Choices, f4settings.Choice{Value: l.Code, Label: f4settings.Text{English: l.Name, Literal: true}})
			}
		case "HelpLanguage":
			f.Kind = f4settings.ChoiceKind
			for _, l := range dialog.ListAvailableHelpLanguages() {
				f.Choices = append(f.Choices, f4settings.Choice{Value: l.Code, Label: f4settings.Text{English: l.Name, Literal: true}})
			}
		case "GuiBackend":
			f.Kind = f4settings.ChoiceKind
			f.Choices = settingsChoices(":Automatic")
			for _, b := range host.GuiBackends() {
				f.Choices = append(f.Choices, f4settings.Choice{Value: b, Label: f4settings.Text{English: b, Literal: true}})
			}
		case "EditorDefaultCodePage", "ViewerDefaultCodePage":
			f.Kind = f4settings.ChoiceKind
			for _, cp := range vfs.AvailableCodepages {
				f.Choices = append(f.Choices, f4settings.Choice{Value: strconv.Itoa(cp.ID), Label: f4settings.Text{English: vfs.CodepageMenuLabel(cp), Literal: true}})
			}
		case "EditorColorerScheme":
			f.Kind = f4settings.ChoiceKind
			f.Choices = settingsChoices(":Built-in default")
		}
		f.Aliases = append(f.Aliases, "settings", f.ID)
		if f.Group == "Typing and focus" || f.Group == "Path suggestions" {
			f.Aliases = append(f.Aliases, "Navigation & suggestions", "Panel settings Additional settings Path hints")
		}

		// Preserve discoverability for users who remember the former dialog.
		f.Aliases = append(f.Aliases, map[string]string{
			"appearance": "Appearance settings Language Help language",
			"workspaces": "Appearance settings Panel settings Auto save Details",
			"panels":     "Panel settings Additional settings Details",
			"drives":     "Drive menu options F9",
			"operations": "Confirmations Compare folders Panel settings",
			"editor":     "Editor settings Viewer settings Colorer settings",
			"syntax":     "Colorer settings Editor settings",
			"keyboard":   "Mouse wheel Hotkey configuration Exact hit",
			"terminal":   "Panel settings Additional settings",
			"updates":    "Auto update settings",
		}[f.Category])
	}
	return settingsCatalogChoiceHelp(f4settings.Catalog{ID: "core", Categories: Categories, Fields: fields, Background: true})
}
func settingsChoices(s string) []f4settings.Choice {
	var result []f4settings.Choice
	for _, part := range strings.Split(s, ";") {
		p := strings.SplitN(part, ":", 2)
		result = append(result, f4settings.Choice{Value: p[0], Label: f4settings.Text{English: p[1]}})
	}
	return result
}

var writeSettingsCandidate = config.WriteSettingsCandidate

func (p coreSettingsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	catalog := p.Catalog()
	values := map[string]string{}
	for _, f := range catalog.Fields {
		values[f.ID] = coreSettingValue(config.App, f.ID)
	}
	d := f4settings.NewDraft(values, nil)
	palette := append([]uint64(nil), vtui.Palette...)
	previewed := false
	d.PreviewFunc = func(d *f4settings.Draft) error {
		if d.Values["ColorStyle"] == d.Baseline["ColorStyle"] && !previewed {
			return nil
		}
		if err := theme.ApplyColorStyle(d.Values["ColorStyle"]); err != nil {
			return err
		}
		previewed = true
		return nil
	}
	d.CloseFunc = func() {
		if previewed {
			_ = theme.ApplyColorStyle(config.App.ColorStyle)
			copy(vtui.Palette, palette)
		}
	}
	d.ValidateFunc = func(d *f4settings.Draft) map[string]error {
		errors := map[string]error{}
		for _, f := range catalog.Fields {
			if !d.Dirty(f.ID) {
				continue
			}
			value := d.Values[f.ID]
			if f.Unavailable != "" {
				errors[f.ID] = settingsError("%s", f.Unavailable)
				continue
			}
			if strings.ContainsAny(value, "\r\n") {
				errors[f.ID] = settingsError("value must be on one line")
			}
			if f.Kind == f4settings.Boolean {
				if _, err := strconv.ParseBool(value); err != nil {
					errors[f.ID] = settingsError("choose enabled or disabled")
				}
			}
			if f.Kind == f4settings.ChoiceKind && !f.AllowCustom && f.ID != "EditorColorerScheme" {
				known := false
				for _, choice := range f.Choices {
					if choice.Value == value {
						known = true
						break
					}
				}
				if !known && (f.ID != "GuiBackend" || (value != "qt" && !strings.HasPrefix(value, "ext:"))) {
					errors[f.ID] = settingsError("choose an available value")
				}
			}
			if f.Kind == f4settings.Integer {
				n, err := strconv.Atoi(value)
				min := 0
				if f.ID == "EditorTabSize" || f.ID == "PathHintTimeout" || f.ID == "PathHintMaxVisible" || f.ID == "Compare.MaxDepth" || f.ID == "GuiFontSize" {
					min = 1
				}
				if f.ID == "HistoryDirsPrefixLen" {
					min = 4
				}
				if err != nil || n < min {
					errors[f.ID] = settingsError("enter an integer of at least %d", min)
				}
				if f.ID == "Compare.MaxDepth" && n > 99 {
					errors[f.ID] = settingsError("maximum depth is 99")
				}
			}

			switch f.ID {
			case "ClipboardImageJPEGQuality":
				n, err := strconv.Atoi(value)
				if err != nil || n < 1 || n > 100 {
					errors[f.ID] = settingsError("JPEG quality must be between 1 and 100")
				}
			case "ClipboardImageTemplate":
				if _, err := cmdline.CompileFilenameTemplate(value); err != nil {
					errors[f.ID] = err
				}
			case "ClipboardImageDigitFormat":
				if len(value) < 1 || len(value) > 20 || strings.Trim(value, "0") != "" {
					errors[f.ID] = settingsError("digit format must contain between 1 and 20 zeros")
				}
			case "ClipboardImagePrefix":
				if err := panel.ValidateClipboardImageName(value + "1.png"); err != nil {
					errors[f.ID] = err
				}
			}
			if current := coreSettingValue(config.App, f.ID); current != d.Baseline[f.ID] && current != value {
				errors[f.ID] = settingsError("this setting changed outside Settings Center; reopen to load its current value")
			}
		}
		if d.Dirty("PanelGroupSmallMiB") || d.Dirty("PanelGroupMediumMiB") || d.Dirty("PanelGroupLargeMiB") {
			small, e1 := strconv.Atoi(d.Values["PanelGroupSmallMiB"])
			medium, e2 := strconv.Atoi(d.Values["PanelGroupMediumMiB"])
			large, e3 := strconv.Atoi(d.Values["PanelGroupLargeMiB"])
			if e1 != nil || e2 != nil || e3 != nil || !config.ValidPanelGroupLimits(small, medium, large) {
				for _, id := range []string{"PanelGroupSmallMiB", "PanelGroupMediumMiB", "PanelGroupLargeMiB"} {
					errors[id] = fmt.Errorf("%s", i18n.Msg("Group.InvalidLimits"))
				}
			}
		}
		return errors
	}
	d.CommitFunc = func(ctx context.Context, d *f4settings.Draft) f4settings.Result {
		if err := ctx.Err(); err != nil {
			return f4settings.Result{Errors: map[string]error{"core": err}}
		}
		onUI := func(run func()) {
			if task, ok := ctx.(*vtui.TaskContext); ok {
				done := make(chan struct{})
				task.RunOnUI(func() { defer close(done); run() })
				<-done
			} else {
				run()
			}
		}
		var before, candidate config.F4Config
		var failures map[string]error
		changed := d.Changed()
		onUI(func() {
			failures = d.Validate()
			before = config.App
			candidate = before
			for _, id := range changed {
				if err := setCoreSetting(&candidate, id, d.Values[id]); err != nil {
					failures[id] = err
				}
			}
			candidate.AutoSaveSettings = candidate.AutoSaveDialogSettings || candidate.AutoSavePanelSettings || candidate.AutoSaveCurrentPanel || candidate.AutoSaveGUIWindow
			if d.Dirty("ExternalEditorConsole") {
				candidate.ExternalEditorCommand = candidate.ExternalEditorConsole
			}
		})
		if len(failures) > 0 {
			return f4settings.Result{Errors: failures}
		}
		if err := ctx.Err(); err != nil {
			return f4settings.Result{Errors: map[string]error{"core": err}}
		}
		if err := writeSettingsCandidate(before, candidate); err != nil {
			return f4settings.Result{Errors: map[string]error{"core": err}}
		}
		onUI(func() {
			previous := config.App
			for _, id := range changed {
				_ = setCoreSetting(&config.App, id, d.Values[id])
			}
			config.SyncAutoSaveMaster()
			if d.Dirty("ExternalEditorConsole") {
				config.App.ExternalEditorCommand = config.App.ExternalEditorConsole
			}
			host.ApplyRuntime(previous, changed)
			palette = append(palette[:0], vtui.Palette...)
			previewed = false
		})
		return f4settings.Result{Applied: changed}
	}

	return d, nil
}

// settingsColorerSchemes lists the colour styles of the applied Colorer
// configuration, the user's own included. Colorer reads the catalog itself:
// catalog.xml pulls its style lists in through external XML entities, which
// encoding/xml does not follow, so reading it here listed nothing on a real
// installation. It starts Colorer on a cache miss; call it off the UI thread.
func settingsColorerSchemes() []editor.ColorerScheme {
	return editor.ListColorerSchemesFor(editor.CurrentColorerSource())
}

// driveMenuOptionFieldID is the settings field of the drive menu option whose
// label key is label: DriveMenuOptions.N, N being its place in
// panel.DriveMenuOptionSpecs, as it is stored.
func driveMenuOptionFieldID(label string) string {
	for i, spec := range panel.DriveMenuOptionSpecs {
		if spec.Label == label {
			return fmt.Sprintf("DriveMenuOptions.%d", i)
		}
	}
	return ""
}
