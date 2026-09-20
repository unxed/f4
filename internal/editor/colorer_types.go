package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The file type parameters FarColorer's list of types keeps per user.
const (
	colorerParamFavorite = "favorite"
	colorerParamHotkey   = "hotkey"
)

// colorerHRCSettingsPath is far2l's plug/hrcsettings.xml in the configuration
// directory: the defaults of the file type parameters. An installation
// without one has no parameters to set.
func colorerHRCSettingsPath(configsDir string) string {
	return filepath.Join(configsDir, "plug", "hrcsettings.xml")
}

// ColorerProfilePath is FarColorer's HrcSettings.ini: the user's values of
// file type parameters, one section per type.
func ColorerProfilePath() string {
	return filepath.Join(config.GetF4ConfigDir(), "colorer", "HrcSettings.ini")
}

// loadColorerProfile reads the user's parameter values: type -> param -> value.
func loadColorerProfile() map[string]map[string]string {
	profile := map[string]map[string]string{}
	if _, err := os.Stat(ColorerProfilePath()); err != nil {
		return profile
	}
	for section, values := range ini.Load(ColorerProfilePath()).Sections() {
		if section == "" || len(values) == 0 {
			continue
		}
		profile[section] = map[string]string{}
		for key, value := range values {
			profile[section][key] = value
		}
	}
	return profile
}

// saveColorerProfile writes the user's parameter values, as
// FarHrcSettings::writeUserProfile does, sections and keys sorted.
func saveColorerProfile(profile map[string]map[string]string) error {
	var b strings.Builder
	types := make([]string, 0, len(profile))
	for name := range profile {
		types = append(types, name)
	}
	sort.Strings(types)
	for _, name := range types {
		params := make([]string, 0, len(profile[name]))
		for param := range profile[name] {
			params = append(params, param)
		}
		sort.Strings(params)
		if len(params) == 0 {
			continue
		}
		fmt.Fprintf(&b, "[%s]\n", name)
		for _, param := range params {
			fmt.Fprintf(&b, "%s=%s\n", param, profile[name][param])
		}
		b.WriteString("\n")
	}
	path := ColorerProfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// applyColorerProfile gives a session the user's parameter values, as
// FarHrcSettings::readUserProfile does after loading the configuration. A
// value for a type or parameter the configuration no longer has is skipped.
func applyColorerProfile(session *colorer.Session) {
	for typeName, params := range loadColorerProfile() {
		for param, value := range params {
			if err := session.SetFileTypeParam(typeName, param, value); err != nil {
				vtui.DebugLog("COLORER: profile value %s.%s skipped: %v", typeName, param, err)
				if session.Err() != nil {
					return
				}
			}
		}
	}
}

// colorerTypeEntry is one file type in the list of types.
type colorerTypeEntry struct {
	name, group, description string
	favorite                 bool
	hotkey                   string
}

// colorerTypeRow is ChooseTypeMenu::GenerateName: the hotkey as the menu's
// hotkey, or a space, then the description.
func colorerTypeRow(t colorerTypeEntry) string {
	description := strings.ReplaceAll(t.description, "&", "&&")
	if t.hotkey != "" {
		return "&" + t.hotkey + " " + description
	}
	return "  " + description
}

// colorerTypeFrame is FarEditorSet::chooseType: auto detection, the
// favourites, then every type under its group. Enter picks a type for the
// editor; Ins adds the type to the favourites and Del takes it out; F4 assigns
// the type a hotkey.
type colorerTypeFrame struct {
	*vtui.VMenu
	ev      *EditorView
	types   []colorerTypeEntry
	current string // the editor's type, "" when detection chose it
	rowType []int  // the type each row shows, -1 for auto detection and groups
	profile map[string]map[string]string
}

func (f *colorerTypeFrame) rebuild(selectType int) {
	f.Items = f.Items[:0]
	f.rowType = f.rowType[:0]
	add := func(item vtui.MenuItem, typeIdx int) {
		f.AddItem(item)
		f.rowType = append(f.rowType, typeIdx)
	}
	add(vtui.MenuItem{Text: i18n.Msg("Colorer.AutoDetect")}, -1)
	selected := 0
	pick := func(typeIdx int) {
		if typeIdx == selectType || (selectType < 0 && f.current != "" && f.types[typeIdx].name == f.current) {
			selected = len(f.rowType) - 1
		}
	}
	// ChooseTypeMenu::HideEmptyGroup: a group without items has no header.
	var favorites []int
	for i, t := range f.types {
		if t.favorite {
			favorites = append(favorites, i)
		}
	}
	if len(favorites) > 0 {
		add(vtui.MenuItem{Separator: true, Text: i18n.Msg("Colorer.Favorites")}, -1)
		for _, i := range favorites {
			add(vtui.MenuItem{Text: colorerTypeRow(f.types[i])}, i)
			pick(i)
		}
	}
	group := ""
	for i, t := range f.types {
		if t.favorite {
			continue
		}
		if i == 0 || t.group != group {
			group = t.group
			add(vtui.MenuItem{Separator: true, Text: group}, -1)
		}
		add(vtui.MenuItem{Text: colorerTypeRow(t)}, i)
		pick(i)
	}
	f.SetSelectPos(selected)
}

// Show paints the total of types under the list, which VMenu does not. The
// group names are the text of the separators and VMenu draws them itself: they
// used to be painted here by row number, which left them on rows that held other
// items once the list was filtered (#263).
func (f *colorerTypeFrame) Show(scr *vtui.ScreenBuf) {
	f.VMenu.Show(scr)
	p := vtui.NewPainter(scr)
	p.DrawTitle(f.X1, f.Y2, f.X2, " "+fmt.Sprintf(i18n.Msg("Colorer.TotalTypes"), len(f.types))+" ", vtui.Palette[f.ColorTitleIdx])
}

func (f *colorerTypeFrame) selectedType() int {
	if f.SelectPos < 0 || f.SelectPos >= len(f.rowType) {
		return -1
	}
	return f.rowType[f.SelectPos]
}

// setParam records a user value for a type in the profile and saves it.
func (f *colorerTypeFrame) setParam(typeIdx int, param, value string) {
	name := f.types[typeIdx].name
	if f.profile[name] == nil {
		f.profile[name] = map[string]string{}
	}
	f.profile[name][param] = value
	if err := saveColorerProfile(f.profile); err != nil {
		vtui.DebugLog("COLORER: cannot save %s: %v", ColorerProfilePath(), err)
	}
}

func (f *colorerTypeFrame) key(e *vtinput.InputEvent) bool {
	if !e.KeyDown || e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed|vtinput.LeftAltPressed|vtinput.RightAltPressed|vtinput.ShiftPressed) != 0 {
		return false
	}
	typeIdx := f.selectedType()
	switch e.VirtualKeyCode {
	case vtinput.VK_INSERT, vtinput.VK_DELETE:
		if typeIdx >= 0 {
			favorite := e.VirtualKeyCode == vtinput.VK_INSERT
			if f.types[typeIdx].favorite != favorite {
				f.types[typeIdx].favorite = favorite
				f.setParam(typeIdx, colorerParamFavorite, map[bool]string{true: "true", false: "false"}[favorite])
				f.rebuild(typeIdx)
			}
		}
		return true
	case vtinput.VK_F4:
		if typeIdx >= 0 {
			vtui.InputBox(i18n.Msg("Colorer.HotkeyTitle"), i18n.Msg("Colorer.HotkeyPrompt"), f.types[typeIdx].hotkey, func(text string) {
				f.types[typeIdx].hotkey = colorerHotkey(text)
				f.setParam(typeIdx, colorerParamHotkey, f.types[typeIdx].hotkey)
				f.rebuild(typeIdx)
			})
		}
		return true
	}
	return false
}

// colorerHotkey is the key FarColorer's hotkey dialog accepts: one letter or
// digit, upper case; anything else clears the hotkey.
func colorerHotkey(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	r := runes[0]
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return string(unicode.ToUpper(r))
	}
	return ""
}

// ColorerChooseType opens FarColorer's list of types. Picking one highlights
// the editor as that type; picking auto detection goes back to choosing by
// file name.
func (ev *EditorView) ColorerChooseType() {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok || vtui.FrameManager == nil {
		return
	}
	src := ch.colorerSrc
	session, err := acquireColorerSession(src)
	if err != nil {
		vtui.DebugLog("COLORER: cannot list file types: %v", err)
		return
	}
	names, err := session.FileTypes()
	var types []colorerTypeEntry
	for _, t := range names {
		if err != nil {
			break
		}
		entry := colorerTypeEntry{name: t.Name, group: t.Group, description: t.Description}
		if v, ok, _ := session.FileTypeParam(t.Name, colorerParamFavorite); ok {
			entry.favorite = v == "true"
		}
		if v, ok, _ := session.FileTypeParam(t.Name, colorerParamHotkey); ok {
			entry.hotkey = v
		}
		types = append(types, entry)
	}
	releaseColorerSession(session, src)
	if err != nil || len(types) == 0 {
		vtui.DebugLog("COLORER: cannot list file types: %v", err)
		return
	}

	current := ch.fileTypeOverride
	if current == "" {
		current = ch.detectedType
	}
	f := &colorerTypeFrame{VMenu: vtui.NewVMenu(i18n.Msg("Colorer.SelectSyntax")), ev: ev, types: types, current: current, profile: loadColorerProfile()}
	f.rebuild(-1)
	f.OnKeyDown = f.key
	f.OnAction = func(int) {
		typeIdx := f.selectedType()
		f.Close()
		if typeIdx >= 0 {
			ch.setFileType(f.types[typeIdx].name)
		} else {
			ch.setFileType("")
		}
	}
	width := vtui.StringWidth(i18n.Msg("Colorer.SelectSyntax")) + 8
	for _, item := range f.Items {
		width = max(width, vtui.StringWidth(item.Text)+6)
	}
	screenW, screenH := vtui.FrameManager.GetScreenSize(), vtui.FrameManager.GetScreenHeight()
	width = min(width, screenW-4)
	height := min(len(f.Items)+2, screenH-4)
	x, y := max((screenW-width)/2, 0), max((screenH-height)/2, 0)
	f.SetPosition(x, y, x+width-1, y+height-1)
	vtui.FrameManager.Push(f)
}

// setFileType highlights the file as the named type from now on, or as the
// type its name selects when name is empty.
func (ch *ColorerHighlighter) setFileType(name string) {
	if ch.fileTypeOverride == name {
		return
	}
	ch.fileTypeOverride = name
	ch.DropFrom(0)
	if ch.redraw != nil {
		ch.redraw()
	}
}

// colorerRegionSpan is one region of a line, as offsets.
type colorerRegionSpan struct {
	start, end int
}

// colorerRegionSpans keeps the non-empty regions of a parsed line.
func colorerRegionSpans(regions []colorer.Region) []colorerRegionSpan {
	var spans []colorerRegionSpan
	for _, r := range regions {
		if r.Start != r.End {
			spans = append(spans, colorerRegionSpan{r.Start, r.End})
		}
	}
	return spans
}

// colorerRegionAt is FarEditor's cursorRegion: the last region of the line
// that holds the cursor, its end included; a region running to the end of the
// line ends at lineRunes.
func colorerRegionAt(spans []colorerRegionSpan, pos, lineRunes int) (colorerRegionSpan, bool) {
	var found colorerRegionSpan
	ok := false
	for _, s := range spans {
		end := s.end
		if end < 0 {
			end = lineRunes
		}
		if s.start <= pos && pos <= end {
			found, ok = colorerRegionSpan{s.start, end}, true
		}
	}
	return found, ok
}

// ColorerSelectRegion selects the syntax region under the cursor, as
// FarColorer's "Select region" does.
func (ev *EditorView) ColorerSelectRegion() {
	ch, ok := ev.Highlighter.(*ColorerHighlighter)
	if !ok {
		return
	}
	if _, parsed := ch.attrCache[ev.CursorLine]; !parsed {
		return
	}
	text, ok := ev.lineTextForHighlight(ev.CursorLine)
	if !ok {
		return
	}
	line := strings.TrimRight(text, "\r\n")
	region, ok := colorerRegionAt(ch.regionCache[ev.CursorLine], runeIndexAtByte(text, ev.CursorPos), len([]rune(line)))
	if !ok || region.end-region.start <= 0 {
		return
	}
	lineStart := ev.Li.GetLineOffset(ev.CursorLine)
	ev.RectSelActive = false
	ev.SelAnchorOffset = lineStart + byteIndexAtRune(line, region.start)
	ev.CursorPos = byteIndexAtRune(line, region.end)
	ev.SelActive = true
	ev.EnsureCursorVisible()
	if vtui.FrameManager != nil {
		vtui.FrameManager.Redraw()
	}
}
