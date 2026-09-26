package theme

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

//go:embed styles/*.ini
var builtInStyles embed.FS

type ColorStyle struct {
	Name     string
	ini      *ini.File
	custom   bool
	baseName string
}

const CustomColorStyleName = "Custom"

var getUserStylesDir = func() string {
	return filepath.Join(config.GetF4ConfigDir(), "styles")
}

func styleFromIni(fallbackName string, ini *ini.File) ColorStyle {
	name := strings.TrimSpace(ini.GetString("style", "Name", fallbackName))
	if name == "" {
		name = fallbackName
	}
	return ColorStyle{Name: name, ini: ini}
}

func customStyleFromIni(ini *ini.File) ColorStyle {
	baseName := strings.TrimSpace(ini.GetString("style", "Base", ""))
	if baseName == "" || strings.EqualFold(baseName, CustomColorStyleName) {
		baseName = strings.TrimSpace(config.App.ColorStyle)
	}
	if baseName == "" || strings.EqualFold(baseName, CustomColorStyleName) {
		baseName = "Modern"
	}
	return ColorStyle{
		Name:     CustomColorStyleName,
		ini:      ini,
		custom:   true,
		baseName: baseName,
	}
}

func loadStylesFromFS(source fs.FS, pattern string) []ColorStyle {
	paths, _ := fs.Glob(source, pattern)
	styles := make([]ColorStyle, 0, len(paths))
	for _, path := range paths {
		f, err := source.Open(path)
		if err != nil {
			continue
		}
		ini := ini.Parse(f)
		f.Close()
		fallback := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		styles = append(styles, styleFromIni(fallback, ini))
	}
	return styles
}

func AvailableColorStyles() []ColorStyle {
	byName := make(map[string]ColorStyle)
	for _, style := range loadStylesFromFS(builtInStyles, "styles/*.ini") {
		byName[strings.ToLower(style.Name)] = style
	}

	userDir := getUserStylesDir()
	if entries, err := os.ReadDir(userDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".ini") {
				continue
			}
			ini := ini.Load(filepath.Join(userDir, entry.Name()))
			fallback := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			style := styleFromIni(fallback, ini)
			byName[strings.ToLower(style.Name)] = style
		}
	}

	// An exported farcolors.ini is a complete user scheme. Keep it in the
	// selector as Custom instead of applying it to every named style: a full
	// file would otherwise overwrite every colour as soon as a built-in style
	// is selected, making the selector appear broken. Partial files retain the
	// historical overlay behavior below in ApplyColorStyle.
	if path := UserColorOverridesPath(); fileExists(path) {
		byName[strings.ToLower(CustomColorStyleName)] = customStyleFromIni(ini.Load(path))
	}

	styles := make([]ColorStyle, 0, len(byName))
	for _, style := range byName {
		styles = append(styles, style)
	}
	sort.Slice(styles, func(i, j int) bool {
		order := func(name string) int {
			switch strings.ToLower(name) {
			case "modern":
				return 0
			case "classic":
				return 1
			default:
				return 2
			}
		}
		io, jo := order(styles[i].Name), order(styles[j].Name)
		if io != jo {
			return io < jo
		}
		return strings.ToLower(styles[i].Name) < strings.ToLower(styles[j].Name)
	})
	return styles
}

func findColorStyle(styles []ColorStyle, name string) (ColorStyle, bool) {
	for _, style := range styles {
		if strings.EqualFold(style.Name, name) {
			return style, true
		}
	}
	return ColorStyle{}, false
}

func colorIniDefinesSlot(ini *ini.File, slot ColorSlot) bool {
	if ini == nil {
		return false
	}
	section, ok := ini.Sections()["farcolors"]
	if !ok {
		return false
	}
	if _, ok := section[slot.Canonical]; ok {
		return true
	}
	for _, alias := range slot.Aliases {
		if _, ok := section[alias]; ok {
			return true
		}
	}
	return false
}

// isCompleteColorIni reports whether ini defines (almost) every color slot,
// i.e. it looks like a full replacement scheme rather than a handful of
// tweaks meant to sit on top of whichever style is active.
//
// This does not require literal 100% coverage. ColorSlots keeps growing as
// f4 gains new themeable elements, so a farcolors.ini that was a complete
// export at the time it was written will, release after release, define a
// shrinking fraction of the *current* slot list even though nothing about
// the file itself changed. Demanding exact coverage made such a file look
// "partial" and forced nearly all of its colors onto every built-in style,
// silently ignoring whatever theme was actually selected in Settings
// (f4#107). A generous coverage threshold tolerates that drift while still
// rejecting a file that only overrides a couple of colors.
func isCompleteColorIni(ini *ini.File) bool {
	const minCoverage = 0.9 // tolerate slots added to f4 after the file was written

	total := 0
	defined := 0
	for _, slot := range ColorSlots {
		// Older complete exports predate this optional, inherited background.
		if slot.Index == vtui.ColDialogIndicatorBackground || slot.Index == ColDialogSettingsBackground {
			continue
		}
		total++
		if colorIniDefinesSlot(ini, slot) {
			defined++
		}
	}
	if total == 0 {
		return true
	}
	return float64(defined)/float64(total) >= minCoverage
}

func isStandaloneCustomColorIni(ini *ini.File) bool {
	if ini != nil {
		if section, ok := ini.Sections()["style"]; ok && strings.EqualFold(strings.TrimSpace(section["Name"]), CustomColorStyleName) {
			return true
		}
	}
	// Recognize files exported by older f4 versions, before the explicit
	// [style] marker was added.
	return isCompleteColorIni(ini)
}

// UserColorOverridesPath points at the personal farcolors.ini. A partial file
// sits on top of whichever style is active; a complete exported file is also
// available as the standalone Custom style. It is a variable for the same
// reason getUserStylesDir is: tests need to point it somewhere harmless.
var UserColorOverridesPath = func() string {
	return filepath.Join(config.GetF4ConfigDir(), "farcolors.ini")
}

// ApplyColorStyle rebuilds the palette from scratch: built-in defaults, then
// the named style, and finally any partial farcolors.ini overrides. A complete
// farcolors.ini is applied only when Custom is selected, so a saved scheme
// cannot mask every built-in style in the selector.
func ApplyColorStyle(name string) error {
	styles := AvailableColorStyles()
	style, ok := findColorStyle(styles, name)
	if !ok {
		return fmt.Errorf("color style %q not found", name)
	}

	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	themeStyle := style
	if style.custom {
		// Custom files normally contain every exported slot. When a user edits
		// or creates a partial file, use the recorded base theme (or the
		// currently configured theme) for newly-added slots.
		base, found := findColorStyle(styles, style.baseName)
		if !found || base.custom {
			base, found = findColorStyle(styles, "Modern")
		}
		if found {
			ApplyColorIni(base.ini)
			themeStyle = base
		}
		ApplyColorIni(style.ini)
		// A pre-existing custom theme did not opt into indicator surfaces,
		// even if its fallback base now defines the newly introduced slot.
		for _, optional := range []ColorSlot{
			{Canonical: "Dialog.Indicator.Background", Index: vtui.ColDialogIndicatorBackground},
			{Canonical: "Dialog.Settings.Background", Index: ColDialogSettingsBackground},
		} {
			if !colorIniDefinesSlot(style.ini, optional) {
				vtui.Palette[optional.Index] = 0
				delete(colorSourceExpressions, optional.Canonical)
			}
		}
	} else {
		ApplyColorIni(style.ini)
		if path := UserColorOverridesPath(); fileExists(path) {
			userIni := ini.Load(path)
			if !isStandaloneCustomColorIni(userIni) {
				ApplyColorIni(userIni)
			}
		}
	}
	FinishColors()
	GlobalFileHighlighter.LoadThemeRules(themeStyle.ini)
	configureWorkspaceTabColors()
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
