package theme

import (
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/vfs"
)

const useDefaultsTheme = `
[Highlight_0]
Name = Symbolic Links
IncludeAttributes = Symlink
NormalColor = foreground:#34E2E2

[Highlight_1]
Name = Hidden Directories
IncludeAttributes = Hidden, Directory
NormalColor = foreground:#7AC3C3

[Highlight_2]
Name = Directories
IncludeAttributes = Directory
NormalColor = foreground:#EEEEEC
`

func useDefaultsHighlighter(t *testing.T, user string) *FileHighlighter {
	t.Helper()
	oldPriority, oldCorrection := config.App.HighlightPriority, config.App.EnforceColorCorrection
	config.App.HighlightPriority, config.App.EnforceColorCorrection = 0, false
	t.Cleanup(func() { config.App.HighlightPriority, config.App.EnforceColorCorrection = oldPriority, oldCorrection })
	fh := &FileHighlighter{}
	fh.LoadThemeRules(ini.Parse(strings.NewReader(useDefaultsTheme)))
	fh.LoadUserRules(ini.Parse(strings.NewReader(user)))
	return fh
}

func wantColor(t *testing.T, fh *FileHighlighter, what string, item vfs.VFSItem, selected, cursor bool, want string) {
	t.Helper()
	const base = uint64(0x07)
	got := fh.GetColor(&item, base, selected, cursor)
	if exp := ParseFarColor(want, base); want != "" && got != exp {
		t.Errorf("%s: colour %#x, want %q (%#x)", what, got, want, exp)
	}
	if want == "" && got != base {
		t.Errorf("%s: colour %#x, want the panel's own %#x", what, got, base)
	}
}

var (
	udDir       = vfs.VFSItem{Name: "dir", IsDir: true}
	udHiddenDir = vfs.VFSItem{Name: ".dir", IsDir: true, IsHidden: true}
	udLinkDir   = vfs.VFSItem{Name: "link", IsDir: true, IsSymlink: true}
	udJunction  = vfs.VFSItem{Name: "junc", IsDir: true, IsSymlink: true, ReparseTag: vfs.ReparseTagMountPoint}
)

// f4#912: a UseDefaults section recolours its own attribute only, wherever it
// stands in the file, and the other attributes keep the style's colours.
func TestUseDefaultsRecoloursOnlyItsOwnAttribute(t *testing.T) {
	fh := useDefaultsHighlighter(t, `
[Highlight_101]
UseDefaults = 1
Name = Directories
IncludeAttributes = Directory
NormalFileName = foreground:#FF00FF
`)
	wantColor(t, fh, "plain directory", udDir, false, false, "foreground:#FF00FF")
	wantColor(t, fh, "symlink to a directory", udLinkDir, false, false, "foreground:#34E2E2")
	wantColor(t, fh, "junction", udJunction, false, false, "foreground:#34E2E2")
	wantColor(t, fh, "hidden directory", udHiddenDir, false, false, "foreground:#7AC3C3")
	// The section names no cursor colour: the state stays as the style has it.
	wantColor(t, fh, "directory under the cursor", udDir, false, true, "")
}

func TestUseDefaultsIgnoresTheOrderOfSections(t *testing.T) {
	sections := map[string]string{
		"Directory": "IncludeAttributes = Directory\nNormalColor = foreground:#111111",
		"Symlink":   "IncludeAttributes = Symlink\nNormalColor = foreground:#222222",
		"Junction":  "IncludeAttributes = Junction\nNormalColor = foreground:#333333",
	}
	build := func(order ...string) string {
		var b strings.Builder
		for i, name := range order {
			b.WriteString("[Highlight_" + string(rune('1'+i)) + "]\nUseDefaults = 1\n" + sections[name] + "\n\n")
		}
		return b.String()
	}
	for _, order := range [][]string{{"Directory", "Symlink", "Junction"}, {"Junction", "Symlink", "Directory"}, {"Symlink", "Directory", "Junction"}} {
		fh := useDefaultsHighlighter(t, build(order...))
		name := strings.Join(order, ",")
		wantColor(t, fh, name+": directory", udDir, false, false, "foreground:#111111")
		wantColor(t, fh, name+": symlink", udLinkDir, false, false, "foreground:#222222")
		wantColor(t, fh, name+": junction", udJunction, false, false, "foreground:#333333")
		wantColor(t, fh, name+": hidden directory", udHiddenDir, false, false, "foreground:#7AC3C3")
	}
}

// A Symlink override does not repaint a junction: the junction has its own tier.
func TestUseDefaultsSymlinkDoesNotTakeJunctions(t *testing.T) {
	fh := useDefaultsHighlighter(t, "[Highlight_1]\nUseDefaults = 1\nIncludeAttributes = Symlink\nNormalColor = foreground:#222222\n")
	wantColor(t, fh, "symlink", udLinkDir, false, false, "foreground:#222222")
	wantColor(t, fh, "junction", udJunction, false, false, "foreground:#34E2E2")
}

func TestUseDefaultsMarkAndOrdinaryRulesStillWork(t *testing.T) {
	fh := useDefaultsHighlighter(t, `
[Highlight_1]
Mask = *.zip
ExcludeAttributes = Directory
NormalColor = foreground:#AAAAAA

[Highlight_2]
UseDefaults = 1
IncludeAttributes = Directory
Mark = >
`)
	wantColor(t, fh, "ordinary rule", vfs.VFSItem{Name: "a.zip"}, false, false, "foreground:#AAAAAA")
	if got := fh.GetMarker(&udDir); got != ">" {
		t.Errorf("marker = %q, want >", got)
	}
	if got := fh.GetMarker(&udLinkDir); got != "" {
		t.Errorf("a symlink took the directory marker: %q", got)
	}
	wantColor(t, fh, "directory keeps the style's colour", udDir, false, false, "foreground:#EEEEEC")
}
