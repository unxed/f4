package settings

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/sdk/f4settings"
)

// ConfigOptionDoc explains one settings.ini key in f4:config (#1177), as
// far2l gives every far:config entry a description and its closest help
// topic. A key that stores one Settings Center option is explained by that
// option, in the interface language, and its help is the topic InstallHelp
// generates for it. Keys Settings Center does not show are explained in
// English by configOptionExtraDocs, as far2l's descriptions are.
func ConfigOptionDoc(section, key string) dialog.ConfigOptionDoc {
	language := config.App.Language
	resolve := func(t f4settings.Text) string { return t.Resolve(language, i18n.Msg) }
	phrase := func(s string) string { return resolve(f4settings.Text{English: s}) }

	name := section + "." + key
	fields := configOptionFields()[name]
	var doc dialog.ConfigOptionDoc
	if len(fields) > 0 {
		doc.HelpTopic = "Setting." + fields[0].ID
	}
	extra, hasExtra := configOptionExtraDocs[name]
	if !hasExtra && section == "Layout" {
		extra, hasExtra = configOptionLayoutExtraDoc, true
	}
	switch {
	case hasExtra && len(fields) > 1:
		lines := []string{phrase(extra)}
		for _, field := range fields {
			label := resolve(field.Label)
			if dot := strings.LastIndex(field.ID, "."); dot >= 0 {
				if n, err := strconv.Atoi(field.ID[dot+1:]); err == nil {
					label = fmt.Sprintf("%d: %s", n, label)
				}
			}
			lines = append(lines, label)
		}
		doc.Description = strings.Join(lines, "\n")
	case hasExtra:
		doc.Description = phrase(extra)
	case len(fields) == 1:
		field := fields[0]
		doc.Label = resolve(field.Label)
		doc.Description = resolve(field.Description)
		if field.Timing != "" {
			doc.Description += "\n" + fmt.Sprintf(phrase("Takes effect: %s"), phrase(field.Timing))
		}
	case len(fields) > 1:
		var labels []string
		for _, field := range fields {
			labels = append(labels, resolve(field.Label))
		}
		doc.Description = strings.Join(labels, "\n")
	}
	return doc
}

// configOptionExtraDocs explains the keys no single Settings Center option
// explains: keys it does not show at all, keys kept for older versions, and
// keys that pack several options into one value, whose options are listed
// after the text. The keys are settings.ini's; configOptionLayoutExtraDoc
// covers the far2l [Layout] keys f4 only keeps.
var configOptionExtraDocs = map[string]string{
	"Interface.FallbackLanguage": "Language of the interface strings the interface language does not translate, tried before English.",
	"Panel.ArchiveEnterExcludeMask": "Files Enter does not open as an archive even when their content is one, such as office documents and e-books, which are ZIP inside. " +
		"A far2l file mask: \"|\" starts the exceptions to it.",
	"Panel.ObserverEnterExcludeMask": "Files Enter does not open as an Observer container (an ISO image, for the one module f4 drives so far) even when their content is one. " +
		"Ctrl+PgDn still opens them. A far2l file mask: \"|\" starts the exceptions to it. Empty by default.",
	"Panel.QueueSmallOpsBypass": "1 lets an operation in the queue on at most 100 files and no folders, of at most 16 MB in all, start while a long one holds the same device, " +
		"when none of its paths is or lies under a path of a running operation. 0 makes it wait its turn.",
	"Panel.CopyThreads": "How many files a copy between two local disks works on at once, 1 to 16 (f4#1894). 1, the default, copies one file at a time, as always. " +
		"The multi-threaded engine is being built in steps: until it is connected, values above 1 change nothing.",
	"Panel.DriveMenuOptions": "The drive menu options as a bit mask, one bit per option of Settings Center, Drive chooser; the final bit hides the Links section so old settings keep showing it:",
	"Panel.ConsoleOverlayUI": "The older form of the console style, read only while ConsoleMode is \"host\": 1 is the host console with f4's overlay, 0 without it. " +
		"ConsoleMode now names the style itself: own, far or mc.",
	"Panel.PluginDefaultHotkeysOff": "Hotkeys a plugin brings with it (Shift+F1 for Add to archive, say) that were removed with F4 and Del in the plugin menu or with Unbind in the Hotkey Configurator, as a semicolon-separated list such as ShiftF1;ShiftF2. " +
		"A key on the list no longer runs the plugin's command; delete it from the list to bring the default back.",
	"Panel.DragOutModifier": "Start dragging files out of a panel into another application only while this key is held: ctrl, alt or shift. " +
		"Empty, the default, starts a drag whenever the left button is dragged from a file, as before.",
	"Panel.DragOutHoldMs": "Without DragOutModifier: how many milliseconds the left button must stay down on a file before moving the mouse starts dragging it out of the panel (the pointer changes at once). " +
		"A quick drag keeps moving the cursor, as before, and still starts a drag once it leaves the panel's rows. 0 starts a drag on the first move; -1 only after leaving the rows. The default is 250.",
	"Panel.DropOnFolder": "Where files dragged in from another application go when they are dropped over a folder and f4 could not highlight that folder while they were dragged " +
		"(macOS and Wayland until gogpu reports a drag in progress, gogpu on Windows, 32-bit Windows, far2l). current, the default: into the folder the panel shows; " +
		"folder: into the folder under the pointer; ask: ask which of the two. Where f4 can follow the drag (Windows, X11), the highlighted folder is where the files go.",
	"Panel.VimHotkeys": "Kept for older f4 versions and shared settings files: 1 while NavigationMode is vim. " +
		"f4 reads it only when NavigationMode is missing.",
	"System.AutoSaveSettings": "The older switch for automatic saving. f4 keeps it on while any of the four automatic saving options is on, " +
		"and a settings file without those options takes it as their value.",
	"System.AnnounceKittyTerm": "Introduce the built-in terminal to the programs in it as kitty (TERM=xterm-kitty), so that picture tools use the kitty graphics protocol. " +
		"Only when the xterm-kitty terminfo description is installed.",
	"System.ANSICodePage": "The codepage f4 takes for \"ANSI\" where the system cannot say, as far2l's ~/.config/far2l/cp. " +
		"0, empty or auto keeps the one the locale suggests.",
	"System.OEMCodePage": "The codepage f4 takes for \"OEM\" where the system cannot say, as far2l's ~/.config/far2l/cp. " +
		"0, empty or auto keeps the one the locale suggests.",
	"Appearance.GuiCols":           "Width of the graphical window in character columns, as last saved. A value below 1 means 100.",
	"Appearance.GuiRows":           "Height of the graphical window in character rows, as last saved. A value below 1 means 30.",
	"Appearance.GuiPosX":           "Horizontal position of the graphical window saved with Shift+F9, restored at start.",
	"Appearance.GuiPosY":           "Vertical position of the graphical window saved with Shift+F9, restored at start.",
	"Appearance.HighlightPriority": "Whose file highlighting rules come first: 0 the rules in highlight.ini, 1 the rules of the colour theme.",
	"Update.LastCheck":             "When the updater last checked, as a Unix timestamp. The updater keeps it.",
	"Update.LastVersion":           "The update key of the installed build, which the updater compares with the newest release. The updater keeps it.",
	"Editor.ExternalEditorCommand": "Kept for older f4 versions: the console editor command, or the older single editor command when there is no console one.",
	"Editor.MemoryMap": "Let the editor map a local file into memory instead of reading it in chunks. " +
		"Off, every file takes the chunked path: the way out on a file system where mapping misbehaves.",
	"History.ShowTimes":           "The timestamp column of three histories, one comma-separated value each: 0 date and time, 1 date, 2 none. Positions:",
	"Images.SlideShowDelay":       "Seconds between pictures in the slide show of the image viewer (Ctrl+S). A value below 1 means 5.",
	"Images.ExternalTimeout":      "Seconds an external converter (magick, convert or ffmpeg) may take to decode a picture. A value below 1 means 20.",
	"Images.DecoderPriority":      "Priorities of the image decoders, overriding the built-in ones: name:number pairs separated by commas, semicolons or vertical bars. Empty keeps the built-in order.",
	"Images.Overlay":              "Show pictures in a window of their own over a terminal that cannot draw them. X11Overlay is the older name of this key.",
	"Images.X11OverlayOffsetX":    "Horizontal correction of the picture window, in pixels, for a terminal that does not report where its character grid starts.",
	"Images.X11OverlayOffsetY":    "Vertical correction of the picture window, in pixels, for a terminal that does not report where its character grid starts.",
	"Video.PauseOnFocusLoss":      "Pause a playing video while the terminal is not on top. Off, it keeps playing.",
	"TTYXi.Keys":                  "Where the terminal has no extended keyboard protocol and the session is a local X one, take the combinations of KeyList from the X server, so that keys a terminal cannot send, such as Ctrl+Enter, still reach f4. Held only while f4's terminal has the focus.",
	"TTYXi.KeyList":               "The combinations Keys takes from the X server, separated by commas. Written only when it differs from the built-in list, so that a profile keeps following that list.",
	"Sync.Asymmetric":             "Synchronize dirs, as last set: make the right folder a mirror of the left one instead of letting the newer file win on either side.",
	"Sync.Subdirs":                "Synchronize dirs, as last set: compare the whole tree instead of the files of the two folders.",
	"Sync.ByContent":              "Synchronize dirs, as last set: read the files whose size and time match, to find those that only look equal.",
	"Sync.IgnoreDate":             "Synchronize dirs, as last set: take name and size as the whole truth, so that files can only be equal or not equal.",
	"Sync.Mask":                   "Synchronize dirs, as last set: the file mask the comparison is limited to, with \"|\" before the exceptions.",
	"Plugins.List":                "The RPC plugins added in Manage Plugins (RPC): their paths, separated by vertical bars.",
	"Layout.WidthDecrement":       "Where the split between the two panels is, moved by Ctrl+Left and Ctrl+Right and reset by Ctrl+Clear. The key far2l's config.ini uses.",
	"Layout.LeftHeightDecrement":  "How much the left panel is shortened to show the terminal under it, moved by Ctrl+Up and Ctrl+Down and reset by Ctrl+Clear. The key far2l's config.ini uses.",
	"Layout.RightHeightDecrement": "How much the right panel is shortened to show the terminal under it, moved by Ctrl+Up and Ctrl+Down and reset by Ctrl+Clear. The key far2l's config.ini uses.",
}

// configOptionLayoutExtraDoc explains a [Layout] key f4 does not use.
const configOptionLayoutExtraDoc = "A far2l layout key f4 does not use itself. It is written back as it was read, so that a settings file shared with far2l keeps it."

// configOptionFields maps each settings.ini key, as "Section.Key", to the
// core Settings Center fields stored in it, in catalog order.
var configOptionFields = sync.OnceValue(func() map[string][]f4settings.Field {
	return deriveConfigOptionFields(config.DefaultConfig(), coreSettingsStaticFields())
})

// deriveConfigOptionFields finds the key each field is stored in by changing
// the field and reading which keys SaveConfig writes differently. There is
// no table of settings.ini keys to consult: f4:config lists what
// SerializeSettingsConfig writes (config.Options), and this reads the same
// output, so a key and its field cannot come apart.
func deriveConfigOptionFields(base config.F4Config, fields []f4settings.Field) map[string][]f4settings.Field {
	before := configOptionValues(base)
	byKey := make(map[string][]f4settings.Field)
	for _, field := range fields {
		seen := make(map[string]bool)
		for _, variant := range configFieldVariants(base, field) {
			for _, option := range config.Options(variant) {
				name := option.Section + "." + option.Key
				old, ok := before[name]
				if !ok || old == option.Value || seen[name] {
					continue
				}
				seen[name] = true
				byKey[name] = append(byKey[name], field)
			}
		}
	}
	return byKey
}

func configOptionValues(cfg config.F4Config) map[string]string {
	values := make(map[string]string)
	for _, option := range config.Options(cfg) {
		values[option.Section+"."+option.Key] = option.Value
	}
	return values
}

// configFieldVariants returns base with the field set to each value that
// differs from its current one: the other boolean, the neighbouring
// integers, every other choice, or a changed and an empty string.
func configFieldVariants(base config.F4Config, field f4settings.Field) []config.F4Config {
	current := coreSettingValue(base, field.ID)
	var values []string
	switch field.Kind {
	case f4settings.Boolean:
		values = []string{strconv.FormatBool(current != "true")}
	case f4settings.Integer:
		if n, err := strconv.Atoi(current); err == nil {
			values = []string{strconv.Itoa(n + 1), strconv.Itoa(n - 1)}
		}
	case f4settings.ChoiceKind:
		for _, choice := range field.Choices {
			if choice.Value != current {
				values = append(values, choice.Value)
			}
		}
	default:
		values = []string{current + "x", ""}
		if config.AnsiColorIndex(field.ID) >= 0 || config.TerminalColorKey(field.ID) {
			// "x" is not a colour and an empty field is the default one.
			values = []string{"#010203"}
		}
	}
	var variants []config.F4Config
	for _, value := range values {
		cfg := base
		if setCoreSetting(&cfg, field.ID, value) == nil {
			variants = append(variants, cfg)
		}
	}
	return variants
}
