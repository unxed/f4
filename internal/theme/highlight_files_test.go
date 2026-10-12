package theme

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestHighlightRule_Match(t *testing.T) {
	rule := HighlightRule{
		Masks:      []string{"*.go", "*.sh"},
		IgnoreCase: true,
	}

	tests := []struct {
		item vfs.VFSItem
		want bool
	}{
		{vfs.VFSItem{Name: "main.go"}, true},
		{vfs.VFSItem{Name: "SCRIPT.SH"}, true},
		{vfs.VFSItem{Name: "readme.txt"}, false},
	}

	for _, tt := range tests {
		if got := rule.Match(&tt.item); got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestHighlightRule_CursorColorAliases(t *testing.T) {
	ini := ini.Parse(strings.NewReader(`[Highlight_0]
NormalColorUnderCursor = foreground:#112233
SelectedColorUnderCursor = background:#445566
`))
	rules := ParseHighlightRules(ini)
	if len(rules) != 1 {
		t.Fatalf("ParseHighlightRules returned %d rules, want 1", len(rules))
	}
	if rules[0].CursorStr != "foreground:#112233" {
		t.Errorf("CursorStr = %q, want alias value", rules[0].CursorStr)
	}
	if rules[0].SelectedCursorStr != "background:#445566" {
		t.Errorf("SelectedCursorStr = %q, want alias value", rules[0].SelectedCursorStr)
	}
}

func TestHighlightRule_MatchAttributes(t *testing.T) {
	ruleDir := HighlightRule{
		AttrSet: AttrDirectory,
	}
	ruleExec := HighlightRule{
		AttrSet: AttrExecutable,
	}
	ruleNotExec := HighlightRule{
		AttrClear: AttrExecutable,
	}

	tests := []struct {
		item vfs.VFSItem
		rule HighlightRule
		want bool
	}{
		{vfs.VFSItem{Name: "dir", IsDir: true}, ruleDir, true},
		{vfs.VFSItem{Name: "file", IsDir: false}, ruleDir, false},
		{vfs.VFSItem{Name: "run.sh", IsExecutable: true}, ruleExec, true},
		// On Unix directories carry the x bit, but the Executable rule
		// attribute means "executable program" and must skip them (#419).
		{vfs.VFSItem{Name: "dir", IsDir: true, IsExecutable: true}, ruleExec, false},
		{vfs.VFSItem{Name: "dir", IsDir: true, IsExecutable: true}, ruleNotExec, true},
		{vfs.VFSItem{Name: "run.sh", IsExecutable: true}, ruleNotExec, false},
	}

	for _, tt := range tests {
		if got := tt.rule.Match(&tt.item); got != tt.want {
			t.Errorf("Rule Match on attributes failed for %q: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestHighlightRule_MatchSymlinkAttribute(t *testing.T) {
	ruleSym := HighlightRule{
		AttrSet: AttrSymlink,
	}

	tests := []struct {
		item vfs.VFSItem
		want bool
	}{
		{vfs.VFSItem{Name: "link_file", IsSymlink: true}, true},
		{vfs.VFSItem{Name: "link_dir", IsDir: true, IsSymlink: true}, true},
		{vfs.VFSItem{Name: "normal_file", IsSymlink: false}, false},
	}

	for _, tt := range tests {
		if got := ruleSym.Match(&tt.item); got != tt.want {
			t.Errorf("Rule Match on symlink attribute failed for %q: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}

	parsed := parseAttrFlags("symlink,link,sym,l")
	if parsed&AttrSymlink == 0 {
		t.Error("parseAttrFlags failed to parse symlink flag")
	}
}

func TestHighlightRule_MatchJunctionAttribute(t *testing.T) {
	junction := vfs.VFSItem{Name: "junc", IsDir: true, IsSymlink: true, ReparseTag: vfs.ReparseTagMountPoint}
	symlink := vfs.VFSItem{Name: "sym", IsDir: true, IsSymlink: true}
	plain := vfs.VFSItem{Name: "dir", IsDir: true}

	only := HighlightRule{AttrSet: AttrJunction}
	if !only.Match(&junction) || only.Match(&symlink) || only.Match(&plain) {
		t.Error("IncludeAttributes = Junction must match junctions and nothing else")
	}
	without := HighlightRule{AttrClear: AttrJunction}
	if without.Match(&junction) || !without.Match(&symlink) || !without.Match(&plain) {
		t.Error("ExcludeAttributes = Junction must reject junctions only")
	}
	// Symlink keeps meaning every link, so rules written before Junction
	// existed colour a junction exactly as they did.
	anyLink := HighlightRule{AttrSet: AttrSymlink}
	if !anyLink.Match(&junction) || !anyLink.Match(&symlink) {
		t.Error("Symlink must still match a junction")
	}

	for _, spelling := range []string{"junction", "junc", "j", "Directory, Junction"} {
		if parseAttrFlags(strings.ToLower(spelling))&AttrJunction == 0 {
			t.Errorf("parseAttrFlags(%q) lost the junction flag", spelling)
		}
	}
}

func TestBuiltInStylesHighlightSymlinks(t *testing.T) {
	styles := loadStylesFromFS(builtInStyles, "styles/*.ini")
	if len(styles) == 0 {
		t.Fatal("no built-in styles loaded")
	}

	linkDir := vfs.VFSItem{Name: "link-dir", IsDir: true, IsSymlink: true}
	plainDir := vfs.VFSItem{Name: "plain-dir", IsDir: true}
	for _, style := range styles {
		found := false
		for _, rule := range ParseHighlightRules(style.ini) {
			if !rule.Match(&linkDir) {
				continue
			}
			if rule.AttrSet&AttrSymlink != 0 && !rule.Match(&plainDir) && rule.NormalStr != "" {
				found = true
				if rule.Mark != "" {
					t.Errorf("built-in style %q symlink rule marker = %q, want empty so the panel arrow fallback stays authoritative", style.Name, rule.Mark)
				}
			}
			break
		}
		if !found {
			t.Errorf("built-in style %q has no visible symlink highlight rule", style.Name)
		}
	}
}

func TestFileHighlighter_GetColor(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	iniData := `[Highlight_0]
Name = Executables
Group = 0
Mask = *.exe, *.sh
NormalColor = foreground:#00FF00
SelectedColor = foreground:#00FF00 | background:#0000FF

[Highlight_1]
Name = Archives
Mask = *.zip
NormalColor = foreground:#FF00FF
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	tests := []struct {
		item       vfs.VFSItem
		defaultVal uint64
		selected   bool
		cursor     bool
		wantRGBFg  uint32
		wantRGBBg  uint32
	}{
		{
			item:       vfs.VFSItem{Name: "app.exe"},
			defaultVal: 0,
			selected:   false,
			cursor:     false,
			wantRGBFg:  0x00FF00,
			wantRGBBg:  0x000000,
		},
		{
			item:       vfs.VFSItem{Name: "app.exe"},
			defaultVal: 0,
			selected:   true,
			cursor:     false,
			wantRGBFg:  0x00FF00,
			wantRGBBg:  0x0000FF,
		},
		{
			item:       vfs.VFSItem{Name: "archive.zip"},
			defaultVal: 0,
			selected:   false,
			cursor:     false,
			wantRGBFg:  0xFF00FF,
			wantRGBBg:  0x000000,
		},
	}

	for _, tt := range tests {
		got := highlighter.GetColor(&tt.item, tt.defaultVal, tt.selected, tt.cursor)
		fg := vtui.GetRGBFore(got)
		bg := vtui.GetRGBBack(got)

		if fg != tt.wantRGBFg {
			t.Errorf("[%s] Fg mismatch: got %06X, want %06X", tt.item.Name, fg, tt.wantRGBFg)
		}
		if bg != tt.wantRGBBg {
			t.Errorf("[%s] Bg mismatch: got %06X, want %06X", tt.item.Name, bg, tt.wantRGBBg)
		}
	}
}

func TestFileHighlighter_RulesSortingAndPrecedence(t *testing.T) {
	// Проверяем, что правила сортируются по индексу независимо от порядка их объявления в INI.
	// Highlight_1 совпадает со всеми файлами, но Highlight_0 имеет приоритет выше.
	iniData := `[Highlight_1]
Name = Low Priority
Mask = *
NormalColor = foreground:#FF0000

[Highlight_0]
Name = High Priority
Mask = *.go
NormalColor = foreground:#00FF00
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	if len(highlighter.Rules) != 2 {
		t.Fatalf("Expected 2 rules loaded, got %d", len(highlighter.Rules))
	}

	item := vfs.VFSItem{Name: "main.go"}
	color := highlighter.GetColor(&item, 0, false, false)
	fg := vtui.GetRGBFore(color)

	if fg != 0x00FF00 {
		t.Errorf("Precedence error: expected higher priority Highlight_0 (0x00FF00), got %06X", fg)
	}
}

func TestFileHighlighter_AttributeExclusion(t *testing.T) {
	rule := HighlightRule{
		AttrSet:   AttrExecutable,
		AttrClear: AttrDirectory,
		Masks:     []string{"*"},
	}

	tests := []struct {
		item vfs.VFSItem
		want bool
	}{
		{vfs.VFSItem{Name: "run.sh", IsExecutable: true, IsDir: false}, true},
		{vfs.VFSItem{Name: "run.sh", IsExecutable: true, IsDir: true}, false}, // Исключено, так как это папка
	}

	for _, tt := range tests {
		if got := rule.Match(&tt.item); got != tt.want {
			t.Errorf("Match(%q) with exclusion failed: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestFileHighlighter_CascadingColors(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	// Настраиваем правило, которое переопределяет только цвет текста (Foreground)
	iniData := `[Highlight_0]
Name = GreenText
Mask = *.txt
NormalColor = foreground:#00FF00
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	// В качестве базового цвета передаем синий фон (0x0000FF)
	defaultAttr := vtui.SetRGBBack(0, 0x0000FF)
	item := vfs.VFSItem{Name: "readme.txt"}

	got := highlighter.GetColor(&item, defaultAttr, false, false)
	fg := vtui.GetRGBFore(got)
	bg := vtui.GetRGBBack(got)

	if fg != 0x00FF00 {
		t.Errorf("Expected foreground to be updated to 0x00FF00, got %06X", fg)
	}
	if bg != 0x0000FF {
		t.Errorf("Expected background to preserve original color 0x0000FF, got %06X", bg)
	}
}

func TestFileHighlighter_SizeFiltering(t *testing.T) {
	ruleBig := HighlightRule{
		SizeAbove: 10 * 1024 * 1024, // > 10MB
	}
	ruleSmall := HighlightRule{
		SizeBelow: 1024, // < 1KB
	}

	tests := []struct {
		item vfs.VFSItem
		rule HighlightRule
		want bool
	}{
		{vfs.VFSItem{Name: "big.zip", Size: 20 * 1024 * 1024}, ruleBig, true},
		{vfs.VFSItem{Name: "mid.zip", Size: 5 * 1024 * 1024}, ruleBig, false},
		{vfs.VFSItem{Name: "tiny.txt", Size: 512}, ruleSmall, true},
		{vfs.VFSItem{Name: "normal.txt", Size: 2048}, ruleSmall, false},
	}

	for _, tt := range tests {
		if got := tt.rule.Match(&tt.item); got != tt.want {
			t.Errorf("Size filter failed for %s: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestFileHighlighter_DateFiltering(t *testing.T) {
	now := time.Now()
	ruleNew := HighlightRule{
		DateAfter: now.Add(-24 * time.Hour), // Изменен в последние 24 часа
	}

	tests := []struct {
		item vfs.VFSItem
		want bool
	}{
		{vfs.VFSItem{Name: "new.txt", MTime: now.Add(-1 * time.Hour)}, true},
		{vfs.VFSItem{Name: "old.txt", MTime: now.Add(-48 * time.Hour)}, false},
	}

	for _, tt := range tests {
		if got := ruleNew.Match(&tt.item); got != tt.want {
			t.Errorf("Date filter failed for %s: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestFileHighlighter_RelativeDateFiltering(t *testing.T) {
	// Имитируем загрузку относительной даты с поддержкой дней (например, "2d" назад)
	iniData := `[Highlight_0]
Name = RelativeNew
DateRelative = 1
DateAfter = 2d
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	now := time.Now()
	tests := []struct {
		item vfs.VFSItem
		want bool
	}{
		{vfs.VFSItem{Name: "fresh.txt", MTime: now.Add(-12 * time.Hour)}, true},
		{vfs.VFSItem{Name: "stale.txt", MTime: now.Add(-72 * time.Hour)}, false},
	}

	for _, tt := range tests {
		if got := highlighter.Rules[0].Match(&tt.item); got != tt.want {
			t.Errorf("Relative Date check failed for %s: got %v, want %v", tt.item.Name, got, tt.want)
		}
	}
}

func TestFileHighlighter_GetMarker(t *testing.T) {
	iniData := `[Highlight_0]
Name = Executables
Mask = *.sh
Mark = *

[Highlight_1]
Name = Dirs
IncludeAttributes = Directory
Mark = /
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	tests := []struct {
		item vfs.VFSItem
		want string
	}{
		{vfs.VFSItem{Name: "run.sh"}, "*"},
		{vfs.VFSItem{Name: "folder", IsDir: true}, "/"},
		{vfs.VFSItem{Name: "readme.txt"}, ""},
	}

	for _, tt := range tests {
		if got := highlighter.GetMarker(&tt.item); got != tt.want {
			t.Errorf("[%s] Expected marker %q, got %q", tt.item.Name, tt.want, got)
		}
	}
}

func TestFileHighlighter_ContinueProcessing(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	// Настройка двух правил:
	// 1. Изменяет цвет текста (foreground) в зеленый и разрешает каскад
	// 2. Изменяет цвет фона (background) в синий
	iniData := `[Highlight_0]
Name = GreenText
Mask = *.txt
NormalColor = foreground:#00FF00
ContinueProcessing = 1

[Highlight_1]
Name = BlueBackground
Mask = *
NormalColor = background:#0000FF
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	item := vfs.VFSItem{Name: "readme.txt"}
	got := highlighter.GetColor(&item, 0, false, false)

	fg := vtui.GetRGBFore(got)
	bg := vtui.GetRGBBack(got)

	if fg != 0x00FF00 {
		t.Errorf("Expected foreground blended from Highlight_0: %06X", fg)
	}
	if bg != 0x0000FF {
		t.Errorf("Expected background blended from Highlight_1: %06X", bg)
	}
}

func TestHighlightRule_PlatformAttributes(t *testing.T) {
	rule := HighlightRule{
		AttrSet: AttrReadOnly,
	}

	var item vfs.VFSItem
	if runtime.GOOS == "windows" {
		item = vfs.VFSItem{Name: "readonly.txt", WinAttrs: 1} // FILE_ATTRIBUTE_READONLY
	} else {
		item = vfs.VFSItem{Name: "readonly.txt", UnixMode: 0444} // Нет прав на запись (mask 0222)
	}

	if !rule.Match(&item) {
		t.Errorf("Platform-specific ReadOnly attribute matching failed on %s", runtime.GOOS)
	}
}

func TestFileHighlighter_ParentDirSkipped(t *testing.T) {
	iniData := `[Highlight_0]
Mask = *
Mark = *
NormalColor = foreground:#00FF00
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	item := vfs.VFSItem{Name: "..", IsDir: true}
	color := highlighter.GetColor(&item, 0, false, false)
	marker := highlighter.GetMarker(&item)

	if color != 0 {
		t.Error("Parent directory '..' should never be colored by highlighter")
	}
	if marker != "" {
		t.Error("Parent directory '..' should never receive a marker")
	}
}

func TestHighlightRule_DateTypes(t *testing.T) {
	now := time.Now()
	mtime := now.Add(-1 * time.Hour)
	ctime := now.Add(-5 * time.Hour)
	atime := now.Add(-10 * time.Hour)

	item := vfs.VFSItem{
		Name:  "dates.txt",
		MTime: mtime,
		CTime: ctime,
		ATime: atime,
	}

	// Должно отвалиться, т.к. доступ был 10 часов назад (лимит - 6 часов)
	ruleAccess := HighlightRule{
		DateType:  DateAccessed,
		DateAfter: now.Add(-6 * time.Hour),
	}

	// Должно пройти, т.к. создание было 5 часов назад (лимит - 6 часов)
	ruleCreate := HighlightRule{
		DateType:  DateCreated,
		DateAfter: now.Add(-6 * time.Hour),
	}

	if ruleAccess.Match(&item) {
		t.Error("DateAccessed matching failed (should have failed)")
	}
	if !ruleCreate.Match(&item) {
		t.Error("DateCreated matching failed (should have passed)")
	}
}

func TestFileHighlighter_GetColor_ContrastCorrection(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := config.App
	config.App.EnforceColorCorrection = true
	defer func() { config.App = oldCfg }()

	// Low contrast rule: Dark Gray on Black background
	iniData := `[Highlight_0]
Name = DarkOnBlack
Mask = *.txt
NormalColor = foreground:#111111 | background:#000000
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	item := vfs.VFSItem{Name: "doc.txt"}
	attr := highlighter.GetColor(&item, 0, false, false)

	fg := vtui.GetRGBFore(attr)
	bg := vtui.GetRGBBack(attr)

	dE := deltaE2000(rgbToLAB(toRGBF(fg)), rgbToLAB(toRGBF(bg)))
	if dE < 29.0 {
		t.Errorf("highlighter left deltaE2000 at %.2f, want the ~30 far2l aims for", dE)
	}
}

func TestFileHighlighter_CursorSemantics(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	iniData := `[Highlight_0]
Name = Executables
Mask = *.exe
NormalColor = foreground:#00FF00
`
	ini := ini.Parse(strings.NewReader(iniData))
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini)

	// Item under cursor (default cursor attribute is ColPanelCursor)
	cursorAttr := vtui.Palette[ColPanelCursor]
	item := vfs.VFSItem{Name: "app.exe"}

	got := highlighter.GetColor(&item, cursorAttr, false, true)

	// Since the rule has no CursorColor defined, it must remain the default cursorAttr!
	if got != cursorAttr {
		t.Errorf("Expected cursor color to be untouched, got %X, want %X", got, cursorAttr)
	}
}

func TestFileHighlighter_SelectedCursorDoesNotFallBackToCursorColor(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	iniData := `[Highlight_0]
Name = Pictures
Mask = *.jpg
NormalColor = foreground:#FF9238
CursorColor = foreground:#FF9238
`
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini.Parse(strings.NewReader(iniData)))

	selectedCursorAttr := vtui.Palette[ColPanelSelectedCursor]
	item := vfs.VFSItem{Name: "photo.jpg"}
	if got := highlighter.GetColor(&item, selectedCursorAttr, true, true); got != selectedCursorAttr {
		t.Fatalf("selected file under cursor used ordinary CursorColor: got %#x, want %#x", got, selectedCursorAttr)
	}
}

// SelectedColor paints a selected file that is not under the cursor and
// nothing else: a selected file under the cursor keeps Panel.Cursor.Selected
// unless the group sets SelectedCursorColor, as in far2l. Falling back to
// SelectedColor made the cursor vanish on selected files whenever that colour
// carried a background (#1150).
func TestFileHighlighter_SelectedCursorDoesNotFallBackToSelectedColor(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := config.App
	defer func() { config.App = oldCfg }()
	config.App.EnforceColorCorrection = false

	base := vtui.Palette[ColPanelSelectedCursor]
	item := vfs.VFSItem{Name: "photo.jpg"}

	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Pictures
Mask = *.jpg
SelectedColor = foreground:#FFFFFF | background:#0000A0
CursorColor = foreground:#FF9238
`)))
	if got := highlighter.GetColor(&item, base, true, true); got != base {
		t.Fatalf("selected file under cursor = %#x, want Panel.Cursor.Selected %#x untouched", got, base)
	}
	selected := highlighter.GetColor(&item, vtui.Palette[ColPanelSelectedText], true, false)
	if fg, bg := vtui.GetRGBFore(selected), vtui.GetRGBBack(selected); fg != 0xFFFFFF || bg != 0x0000A0 {
		t.Fatalf("selected file off the cursor = #%06x on #%06x, want SelectedColor #FFFFFF on #0000A0", fg, bg)
	}

	// An explicit SelectedCursorColor still applies to that state.
	highlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Pictures
Mask = *.jpg
SelectedColor = foreground:#FFFFFF | background:#0000A0
SelectedCursorColor = foreground:#123456
`)))
	got := highlighter.GetColor(&item, base, true, true)
	if fg := vtui.GetRGBFore(got); fg != 0x123456 {
		t.Fatalf("selected file under cursor foreground = #%06x, want SelectedCursorColor #123456", fg)
	}
	if bg, want := vtui.GetRGBBack(got), vtui.GetRGBBack(base); bg != want {
		t.Fatalf("selected file under cursor background = #%06x, want cursor background #%06x", bg, want)
	}
}

func TestHighlightRule_FarColorKeyNames(t *testing.T) {
	ini := ini.Parse(strings.NewReader(`[Highlight_0]
NormalFileName = foreground:#111111
SelectedFileName = foreground:#222222
FileNameUnderCursor = foreground:#333333
FileNameSelectedUnderCursor = foreground:#444444
`))
	rules := ParseHighlightRules(ini)
	if len(rules) != 1 {
		t.Fatalf("ParseHighlightRules returned %d rules, want 1", len(rules))
	}
	rule := rules[0]
	for _, tt := range []struct {
		key  string
		got  string
		want string
	}{
		{"NormalFileName", rule.NormalStr, "foreground:#111111"},
		{"SelectedFileName", rule.SelectedStr, "foreground:#222222"},
		{"FileNameUnderCursor", rule.CursorStr, "foreground:#333333"},
		{"FileNameSelectedUnderCursor", rule.SelectedCursorStr, "foreground:#444444"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s parsed as %q, want %q", tt.key, tt.got, tt.want)
		}
	}
}

func TestHighlightRule_NativeColorKeysWinOverFarNames(t *testing.T) {
	ini := ini.Parse(strings.NewReader(`[Highlight_0]
NormalColor = foreground:#101010
NormalFileName = foreground:#202020
CursorColor = foreground:#303030
FileNameUnderCursor = foreground:#404040
`))
	rules := ParseHighlightRules(ini)
	if len(rules) != 1 {
		t.Fatalf("ParseHighlightRules returned %d rules, want 1", len(rules))
	}
	if rules[0].NormalStr != "foreground:#101010" {
		t.Errorf("NormalStr = %q, want the NormalColor value", rules[0].NormalStr)
	}
	if rules[0].CursorStr != "foreground:#303030" {
		t.Errorf("CursorStr = %q, want the CursorColor value", rules[0].CursorStr)
	}
}

func TestFileHighlighter_NormalColorKeepsItsBackground(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Archives
Mask = *.zip
NormalColor = foreground:#FF00FF | background:#008080
`)))

	item := vfs.VFSItem{Name: "Autoruns.zip"}
	got := highlighter.GetColor(&item, vtui.Palette[ColPanelText], false, false)
	if fg := vtui.GetRGBFore(got); fg != 0xFF00FF {
		t.Errorf("foreground = #%06x, want #FF00FF", fg)
	}
	if bg := vtui.GetRGBBack(got); bg != 0x008080 {
		t.Errorf("background = #%06x, want #008080", bg)
	}
}

func TestFileHighlighter_DirectoryRuleLeavesFilesAlone(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	oldCfg := config.App
	config.App.EnforceColorCorrection = false
	defer func() { config.App = oldCfg }()

	// A rule for folders carries no mask, so only IncludeAttributes keeps it
	// off the files; Name is a caption and matches nothing by itself (#912).
	highlighter := &FileHighlighter{}
	highlighter.LoadFromIni(ini.Parse(strings.NewReader(`[Highlight_0]
Name = Directories
IncludeAttributes = Directory
NormalFileName = foreground:#FFFFFF
`)))

	base := vtui.Palette[ColPanelText]
	dir := vfs.VFSItem{Name: "src", IsDir: true}
	if fg := vtui.GetRGBFore(highlighter.GetColor(&dir, base, false, false)); fg != 0xFFFFFF {
		t.Errorf("directory foreground = #%06x, want #FFFFFF", fg)
	}
	File := vfs.VFSItem{Name: "readme.txt"}
	if got := highlighter.GetColor(&File, base, false, false); got != base {
		t.Errorf("file color = %#x, want the untouched panel color %#x", got, base)
	}
}
