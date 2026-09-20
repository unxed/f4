package menuhotkeys

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

func texts(items []vtui.MenuItem) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Text
	}
	return out
}

func menu(texts ...string) []vtui.MenuItem {
	items := make([]vtui.MenuItem, len(texts))
	for i, text := range texts {
		items[i] = vtui.MenuItem{Text: text}
	}
	return items
}

func TestUniqueMovesTheRepeatedHotkey(t *testing.T) {
	// The Left menu of #1258.
	items := menu("&Brief", "&Background", "&Name", "&New workspace", "&Unsorted", "&Use sort groups", "&Size")
	Unique(items)
	// "Use sort groups" cannot have U or S (Size holds it), so it takes the G
	// of its last word.
	want := []string{"&Brief", "Ba&ckground", "&Name", "New &workspace", "&Unsorted", "Use sort &groups", "&Size"}
	if got := texts(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUniqueLeavesADistinctMenuAlone(t *testing.T) {
	items := menu("&Files", "F&ind", "Sa&ve", "Plain", "E&xit")
	before := texts(items)
	Unique(items)
	if got := texts(items); !reflect.DeepEqual(got, before) {
		t.Fatalf("a menu with distinct hotkeys changed: %q -> %q", before, got)
	}
}

func TestUniqueIsCaseInsensitiveAndKeepsPrefixes(t *testing.T) {
	items := menu("√&Copy", " &copy again", "√ &Delete", "  &delete all")
	Unique(items)
	if got := vtui.ExtractHotkey(items[1].Text); got == 'c' || got == 0 {
		t.Fatalf("%q still repeats or lost its hotkey", items[1].Text)
	}
	if got := vtui.ExtractHotkey(items[3].Text); got == 'd' || got == 0 {
		t.Fatalf("%q still repeats or lost its hotkey", items[3].Text)
	}
	if items[0].Text != "√&Copy" || items[2].Text != "√ &Delete" {
		t.Fatalf("the first claimants changed: %q", texts(items))
	}
}

func TestUniqueDoesNotGiveHotkeysToItemsWithoutOne(t *testing.T) {
	items := menu("&Drive", "Drives", "Background")
	Unique(items)
	if items[1].Text != "Drives" || items[2].Text != "Background" {
		t.Fatalf("items built without a hotkey got one: %q", texts(items))
	}
}

func TestUniqueDropsTheHotkeyWhenNoLetterIsFree(t *testing.T) {
	items := menu("&A", "&a", "&A ")
	Unique(items)
	if items[0].Text != "&A" || items[1].Text != "a" || items[2].Text != "A " {
		t.Fatalf("got %q", texts(items))
	}
}

func TestUniqueKeepsLiteralAmpersands(t *testing.T) {
	items := menu("&Save", "&Save && exit", "Q&&A")
	Unique(items)
	if got := vtui.ExtractHotkey(items[1].Text); got == 's' || got == 0 {
		t.Fatalf("%q has hotkey %q", items[1].Text, got)
	}
	clean, _, _ := vtui.ParseAmpersandString(items[1].Text)
	if clean != "Save & exit" {
		t.Fatalf("%q reads %q, want \"Save & exit\"", items[1].Text, clean)
	}
	if items[2].Text != "Q&&A" {
		t.Fatalf("an item without a hotkey changed: %q", items[2].Text)
	}
}

func TestUniqueMovesToAnyScript(t *testing.T) {
	items := menu("&Имя", "&Изменить", "&الاسم", "&الاستبدال")
	Unique(items)
	seen := map[rune]bool{}
	for _, item := range items {
		hk := vtui.ExtractHotkey(item.Text)
		if hk == 0 || seen[hk] {
			t.Fatalf("hotkeys are not distinct: %q", texts(items))
		}
		seen[hk] = true
	}
}

func TestUniqueIsIdempotent(t *testing.T) {
	items := menu("&Brief", "&Background", "&Name", "&New workspace", "&Use sort groups", "&Unsorted")
	Unique(items)
	once := texts(items)
	Unique(items)
	if got := texts(items); !reflect.DeepEqual(got, once) {
		t.Fatalf("a second pass changed %q into %q", once, got)
	}
}

func TestUniqueTreatsEachSubmenuOnItsOwn(t *testing.T) {
	items := []vtui.MenuItem{
		{Text: "&Sort", SubItems: menu("&Name", "&Name again")},
		{Text: "&Show", SubItems: menu("&Name")},
	}
	Unique(items)
	if items[1].SubItems[0].Text != "&Name" {
		t.Fatalf("another menu's hotkey took this one: %q", items[1].SubItems[0].Text)
	}
	if got := vtui.ExtractHotkey(items[0].SubItems[1].Text); got == 'n' || got == 0 {
		t.Fatalf("%q repeats a hotkey of its own menu", items[0].SubItems[1].Text)
	}
	if items[1].Text == items[0].Text || vtui.ExtractHotkey(items[1].Text) == 's' {
		t.Fatalf("the two headings share a hotkey: %q, %q", items[0].Text, items[1].Text)
	}
}

func TestUniqueBarCoversTheMenuNamesToo(t *testing.T) {
	bar := []vtui.MenuBarItem{
		{Label: "&Left", SubItems: menu("&Brief", "&Background")},
		{Label: "&Files"},
		{Label: "&Options", SubItems: menu("&Layout")},
		{Label: "&Commands"},
		{Label: "&Right"},
	}
	UniqueBar(bar)
	seen := map[rune]bool{}
	for _, item := range bar {
		hk := vtui.ExtractHotkey(item.Label)
		if hk == 0 || seen[hk] {
			t.Fatalf("the menu names do not have distinct hotkeys: %+v", bar)
		}
		seen[hk] = true
	}
	if vtui.ExtractHotkey(bar[0].SubItems[1].Text) == 'b' {
		t.Fatalf("the Left menu still repeats B: %q", texts(bar[0].SubItems))
	}
}

func TestAutoHotkeyGivesWayToAMarkedLabel(t *testing.T) {
	// The first letter of "Link" is only a default; "Edit symbolic &link" was
	// marked by somebody, and it is below the item that wants the same L.
	items := menu(Auto("Link"), "Edit symbolic &link", Auto("Edit"), Auto("Copy"))
	Unique(items)
	want := []string{"Li&nk", "Edit symbolic &link", "&Edit", "&Copy"}
	if got := texts(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAutoMarkerNeverSurvives(t *testing.T) {
	items := menu("√ "+Auto("Copy"), Auto("Cut"), Auto("Cat"))
	Unique(items)
	for _, item := range items {
		if strings.Contains(item.Text, autoMarker) {
			t.Fatalf("the marker was left in %q", item.Text)
		}
	}
	if items[0].Text != "√ &Copy" {
		t.Fatalf("got %q", items[0].Text)
	}
}

// A menu of short words runs out of letters for the last item if every repeat
// just takes the best free one. Here the third item can only have A or B, and
// the second has taken B: it must be moved on to C, and the first keeps its A.
func TestUniqueMovesOthersToGiveEveryItemALetter(t *testing.T) {
	items := menu(Auto("Abc"), Auto("Abc"), Auto("Ab"))
	Unique(items)
	want := []string{"&Abc", "Ab&c", "A&b"}
	if got := texts(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A letter a translator chose is never moved to make room.
func TestUniqueNeverMovesAChosenLetter(t *testing.T) {
	items := menu("E&xit", Auto("Exp"), Auto("Xe"))
	Unique(items)
	if items[0].Text != "E&xit" {
		t.Fatalf("the chosen letter moved: %q", texts(items))
	}
	seen := map[rune]bool{}
	for _, item := range items {
		hk := vtui.ExtractHotkey(item.Text)
		if hk == 0 || seen[hk] {
			t.Fatalf("hotkeys are not distinct or one is missing: %q", texts(items))
		}
		seen[hk] = true
	}
}

// A letter counts as the best thing it is anywhere in the text: the s of "Use
// sort" is the first letter of a word, and is taken before the consonants.
func TestUniqueRanksALetterByItsBestRole(t *testing.T) {
	items := menu("&Use", Auto("Use sort"))
	Unique(items)
	if items[1].Text != "Use &sort" {
		t.Fatalf("got %q", texts(items))
	}
}

// When both letters an item can have were chosen by translators, the item would
// have no hotkey at all; one of those is then moved to a third letter instead.
func TestUniqueMovesAChosenLetterRatherThanLeaveAnItemWithNone(t *testing.T) {
	items := menu("&Abc", "A&bc", Auto("Ab"))
	Unique(items)
	seen := map[rune]bool{}
	for _, item := range items {
		hk := vtui.ExtractHotkey(item.Text)
		if hk == 0 || seen[hk] {
			t.Fatalf("hotkeys are not distinct or one is missing: %q", texts(items))
		}
		seen[hk] = true
	}
}

func TestAutoLeavesAMarkedLabelAlone(t *testing.T) {
	if got := Auto("E&xit"); got != "E&xit" {
		t.Fatalf("Auto(%q) = %q", "E&xit", got)
	}
	if got := Auto("Q&&A"); got != autoMarker+"&Q&&A" {
		t.Fatalf("a literal ampersand is not a marker: %q", got)
	}
}
