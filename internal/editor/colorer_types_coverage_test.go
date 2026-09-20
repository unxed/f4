package editor

import (
	"strings"
	"testing"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestColorerRegionSpansDropsEmptyRegions(t *testing.T) {
	got := colorerRegionSpans([]colorer.Region{
		{Start: 0, End: 0},
		{Start: 1, End: 4},
		{Start: 4, End: 4},
		{Start: 5, End: -1},
	})
	want := []colorerRegionSpan{{1, 4}, {5, -1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("colorerRegionSpans=%v, want %v", got, want)
	}
}

func TestColorerTypeFrameRebuildAndProfileParam(t *testing.T) {
	_ = config.GetF4ConfigDir()
	oldConfigDir := config.CachedF4ConfigDir
	config.CachedF4ConfigDir = t.TempDir()
	t.Cleanup(func() { config.CachedF4ConfigDir = oldConfigDir })

	f := &colorerTypeFrame{
		VMenu: vtui.NewVMenu("types"),
		types: []colorerTypeEntry{
			{name: "go", group: "Languages", description: "Go"},
			{name: "json", group: "Data", description: "JSON", favorite: true},
			{name: "yaml", group: "Data", description: "YAML"},
		},
		current: "go",
		profile: map[string]map[string]string{},
	}
	f.rebuild(-1)
	if len(f.Items) != 7 || len(f.rowType) != 7 {
		t.Fatalf("rebuilt rows=%d types=%v", len(f.Items), f.rowType)
	}
	if got := f.selectedType(); got != 0 {
		t.Fatalf("current type selected=%d, want go index 0", got)
	}
	f.rebuild(2)
	if got := f.selectedType(); got != 2 {
		t.Fatalf("explicit type selected=%d, want yaml index 2", got)
	}
	f.setParam(2, colorerParamHotkey, "Y")
	got := loadColorerProfile()
	if got["yaml"][colorerParamHotkey] != "Y" {
		t.Fatalf("saved profile=%v", got)
	}
}

func TestColorerTypeFrameShowPaintsSeparatorsAndTotal(t *testing.T) {
	vtui.SetDefaultPalette()
	f := &colorerTypeFrame{
		VMenu: vtui.NewVMenu("types"),
		types: []colorerTypeEntry{{name: "go", group: "Languages", description: "Go"}},
	}
	f.rebuild(-1)
	f.SetPosition(1, 1, 30, 10)
	f.Show(vtui.NewSilentScreenBuf())
}

// Typing into the list of types hides the ones that do not match. The group
// names went by row number and were left on rows that held other items, and
// stayed on an empty list (#263).
func TestColorerTypeFrameGroupNamesFollowTheFilter(t *testing.T) {
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	f := &colorerTypeFrame{
		VMenu: vtui.NewVMenu("types"),
		types: []colorerTypeEntry{
			{name: "go", group: "Languages", description: "Go"},
			{name: "rust", group: "Languages", description: "Rust"},
			{name: "json", group: "Data", description: "JSON"},
		},
	}
	f.rebuild(-1)
	f.SetPosition(0, 0, 40, 12)
	f.ClearDone()

	rows := func() []string {
		scr := vtui.NewSilentScreenBuf()
		scr.AllocBuf(50, 15)
		f.Show(scr)
		var out []string
		for y := f.Y1 + 1; y < f.Y2; y++ {
			var b strings.Builder
			for x := f.X1 + 1; x < f.X2; x++ {
				b.WriteRune(rune(scr.GetCell(x, y).Char))
			}
			out = append(out, b.String())
		}
		return out
	}
	has := func(rows []string, s string) bool {
		for _, r := range rows {
			if strings.Contains(r, s) {
				return true
			}
		}
		return false
	}

	all := rows()
	if !has(all, "Languages") || !has(all, "Data") {
		t.Fatalf("the group names are missing from the full list: %q", all)
	}

	// Only JSON matches "js": no other group name is left on screen.
	f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed})
	for _, r := range "js" {
		f.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	filtered := rows()
	if !has(filtered, "JSON") || has(filtered, "Languages") || has(filtered, "Go") {
		t.Fatalf("filtered list %q shows what the filter hides", filtered)
	}
}
