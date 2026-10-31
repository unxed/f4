package settings

import (
	"github.com/unxed/f4/internal/config"

	"fmt"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestSettingsSidebarSearchAndContentSurface(t *testing.T) {
	oldConfig := config.App
	defer func() { config.App = oldConfig; InitLang() }()
	palette := append([]uint64(nil), vtui.Palette...)
	defer func() { vtui.Palette = palette }()
	for _, language := range []string{"en", "ru"} {
		config.App.Language = language
		config.App.UseLocalLanguageFiles = false
		InitLang()
		fields := []f4settings.Field{
			f4settings.Scalar("enabled", "startup", "Defaults", "Enabled", "Enable this behavior.", f4settings.Boolean),
			f4settings.Scalar("path", "startup", "Defaults", "Path", "Set path.", f4settings.String),
		}
		d := f4settings.NewDraft(map[string]string{"enabled": "true", "path": "example"}, nil)
		defer d.Close()
		c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: fields}, draft: d}})
		c.selectCategory("startup")
		scr := vtui.NewSilentScreenBuf()
		for iteration, size := range [][2]int{{80, 25}, {150, 40}, {80, 25}} {
			scr.AllocBuf(size[0], size[1])
			c.SetPosition(0, 0, size[0]-1, size[1]-1)
			for _, slot := range []int{vtui.ColDialogText, vtui.ColDialogBox, vtui.ColDialogBoxTitle} {
				vtui.Palette[slot] = vtui.SetRGBBoth(0, testutil.Uint32(0xa0b0c0+slot), 0x505050)
			}
			vtui.Palette[theme.ColDialogSettingsBackground] = vtui.SetRGBBack(0, testutil.Uint32(0x303030+iteration))
			vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, 0xffffff, 0x101010)
			vtui.Palette[vtui.ColDialogEditUnchanged] = vtui.Palette[vtui.ColDialogEdit]
			vtui.Palette[vtui.ColDialogSelectedButton] = vtui.SetRGBBoth(0, 0xffffff, testutil.Uint32(0x123456+iteration))
			vtui.Palette[vtui.ColDialogIndicatorBackground] = 0
			sideWidth := c.sidebar.X2 - c.sidebar.X1 + 1
			for _, query := range []string{"", "Enabled", "no-match-xyz", " "} {
				c.search.SetText(query)
				c.search.OnTextChange(query)
				c.SetFocusedItem(c.sidebar)
				c.Show(scr)
				searching := strings.TrimSpace(query) != ""
				if c.sidebar.X2-c.sidebar.X1+1 != sideWidth {
					t.Fatal("search resized sidebar")
				}
				if c.sidebar.Y1 != c.Y1+4 {
					t.Fatal("blank row after search separator")
				}
				if c.sidebar.Y2 != c.apply.Y1-1 || c.help.Y2 != c.apply.Y1-1 {
					t.Fatal("unused space above buttons")
				}
				if c.help.X1 > c.page.X2 && c.help.Y1 != c.Y1+1 {
					t.Fatal("help starts below search label")
				}
				if scr.GetCell(c.sidebar.X2+1, c.Y1+1).Char != '│' || scr.GetCell(c.sidebar.X2+1, c.apply.Y1-1).Char != '│' {
					t.Fatal("column separator does not span the pane")
				}

				if c.previous.IsVisible() != searching || c.next.IsVisible() != searching {
					t.Fatal("search arrows visibility")
				}
				if c.search.X1 != c.sidebar.X1 || c.search.X2 > c.sidebar.X2 || c.search.Y1 >= c.sidebar.Y1 {
					t.Fatal("search not inside sidebar")
				}
				if searching && (c.search.X2 >= c.previous.X1 || c.next.X2 != c.sidebar.X2 || c.next.Y1 != c.search.Y1 || c.next.X2-c.next.X1+1 != 3) {
					t.Fatal("compact search arrows placement")
				}
				var separator strings.Builder
				for x := c.sidebar.X1; x <= c.sidebar.X2; x++ {
					separator.WriteRune(testutil.Rune(scr.GetCell(x, c.Y1+3).Char))
				}
				if searching {
					count := 0
					if query == "Enabled" {
						count = 1
					}
					if !strings.Contains(separator.String(), fmt.Sprintf(settingsText("Matches", "Matches: %d"), count)) {
						t.Fatalf("missing match count: %s", separator.String())
					}
				} else if strings.ContainsAny(separator.String(), "0123456789") {
					t.Fatal("idle separator contains match count")
				}
				if c.cancel.X2 != c.X2-2 || c.apply.X1 >= c.ok.X1 || c.ok.X1 >= c.cancel.X1 {
					t.Fatal("action alignment/order")
				}
			}
			c.search.SetText("")
			c.search.OnTextChange("")
			checkbox := c.page.rows[1].control
			for _, active := range []bool{false, true} {
				if active {
					c.SetFocusedItem(c.page)
					c.page.SetFocusedItem(checkbox)
				} else {
					c.SetFocusedItem(c.sidebar)
				}
				c.Show(scr)
				x, y, _, _ := checkbox.GetPosition()
				got := scr.GetCell(x+4, y).Attributes
				want := vtui.SetRGBBack(vtui.Palette[vtui.ColDialogText], testutil.Uint32(0x303030+iteration))
				if active {
					want = vtui.Palette[vtui.ColDialogSelectedButton]
				}
				if got != want {
					t.Fatalf("content/focus palette %x want %x", got, want)
				}
				// The last cell of the field lies past its text, so the initial
				// selection of the text (drawn in the selection colour since
				// vtui v0.1.405) does not hide whether the surface was overwritten.
				_, y, x2, _ := c.page.rows[2].control.GetPosition()
				if scr.GetCell(x2, y).Attributes != vtui.Palette[vtui.ColDialogEdit] {
					t.Fatal("input surface overwritten")
				}
				border := scr.GetCell(c.page.X1, c.Y1+3).Attributes
				if border != vtui.SetRGBBack(vtui.Palette[vtui.ColDialogBox], testutil.Uint32(0x303030+iteration)) {
					t.Fatal("content border background")
				}
			}
			c.search.OnTextChange("Enabled")
			c.SetFocusedItem(c.search)
			for _, want := range []vtui.UIElement{c.clearSearch, c.previous, c.next, c.sidebar, c.page, c.apply, c.ok, c.cancel, c.search} {
				c.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})
				if c.GetFocusedItem() != want {
					t.Fatalf("search tab order got %T want %T", c.GetFocusedItem(), want)
				}
			}
			c.SetFocusedItem(c.next)
			c.search.OnTextChange("")
			if c.GetFocusedItem() != c.search {
				t.Fatal("focus remained on hidden arrow")
			}
		}
	}
}

func TestSettingsSearchClearButton(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer func() { vtui.Palette = palette }()
	d := f4settings.NewDraft(nil, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories}, draft: d}})
	scr := vtui.NewSilentScreenBuf()
	for iteration, width := range []int{80, 150} {
		scr.AllocBuf(width, 25)
		c.SetPosition(0, 0, width-1, 24)
		vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, testutil.Uint32(0xeeeeee+iteration), testutil.Uint32(0x123456+iteration))
		vtui.Palette[vtui.ColDialogSelectedButton] = vtui.SetRGBBoth(0, 0xffffff, testutil.Uint32(0x56789a+iteration))
		sidebarWidth := c.sidebar.X2 - c.sidebar.X1 + 1
		for _, query := range []string{"example", " "} {
			c.search.SetText(query)
			c.search.OnTextChange(query)
			for _, focused := range []vtui.UIElement{c.search, c.clearSearch} {
				c.SetFocusedItem(focused)
				c.Show(scr)
				b := c.clearSearch
				want := vtui.Palette[vtui.ColDialogEdit]
				if focused == b {
					want = vtui.Palette[vtui.ColDialogSelectedButton]
				}
				if !b.IsVisible() || b.X1 != c.search.X2+1 || b.Y1 != c.search.Y1 {
					t.Fatal("clear button not inside search surface")
				}
				if cell := scr.GetCell(b.X1+1, b.Y1); cell.Char != '×' || cell.Attributes != want {
					t.Fatal("clear button theme/focus rendering")
				}
			}
			b := c.clearSearch
			for _, press := range []bool{true, false} {
				buttons := uint32(0)
				if press {
					buttons = vtinput.FromLeft1stButtonPressed
				}
				c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, KeyDown: press, ButtonState: buttons, MouseX: testutil.Int16(b.X1 + 1), MouseY: testutil.Int16(b.Y1)})
			}
			c.Show(scr)
			if c.query != "" || c.search.GetText() != "" || c.GetFocusedItem() != c.search || c.clearSearch.IsVisible() || c.previous.IsVisible() || c.next.IsVisible() {
				t.Fatal("clear did not reset search and restore focus")
			}
			if c.sidebar.X2-c.sidebar.X1+1 != sidebarWidth || c.search.X2 != c.sidebar.X2 || len(d.Changed()) != 0 {
				t.Fatal("clearing changed layout or preferences")
			}
		}
		c.search.SetText("keyboard")
		c.search.OnTextChange("keyboard")
		c.SetFocusedItem(c.clearSearch)
		c.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
		if c.query != "" || c.GetFocusedItem() != c.search {
			t.Fatal("keyboard clear failed")
		}
	}
}

func TestSettingsStatusSharesActionRow(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer func() { vtui.Palette = palette }()
	d := f4settings.NewDraft(nil, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories}, draft: d}})
	scr := vtui.NewSilentScreenBuf()
	for iteration, width := range []int{80, 150} {
		scr.AllocBuf(width, 25)
		c.SetPosition(0, 0, width-1, 24)
		c.status = ""
		c.Show(scr)
		bottom := c.page.Y2
		vtui.Palette[vtui.ColDialogHighlightText] = vtui.SetRGBBoth(0, testutil.Uint32(0xabcdef+iteration), 0x123456)
		for _, status := range []string{"Settings applied.", strings.Repeat("Long status ", 30), ""} {
			c.status = status
			c.Show(scr)
			if c.page.Y2 != bottom || c.sidebar.Y2 != c.apply.Y1-1 {
				t.Fatal("status changed content height")
			}
			if status != "" {
				cell := scr.GetCell(c.X1+2, c.apply.Y1)
				if cell.Char != uint64(status[0]) || cell.Attributes != vtui.Palette[vtui.ColDialogHighlightText] {
					t.Fatal("status not rendered on action row")
				}
			}
			if vtui.CellBaseRune(scr.GetCell(c.apply.X1, c.apply.Y1).Char) != '[' || vtui.CellBaseRune(scr.GetCell(c.cancel.X1, c.cancel.Y1).Char) != '[' {
				t.Fatal("status overlaps buttons")
			}
		}
	}
}

func TestSettingsNavigationRegrouping(t *testing.T) {
	catalog := (coreSettingsProvider{}).Catalog()
	for _, category := range catalog.Categories {
		if category.ID == "navigation" {
			t.Fatal("removed category still present")
		}
	}
	counts := map[string]int{}
	for _, f := range catalog.Fields {
		if f.Group == "Typing and focus" {
			if f.Category != "panels" {
				t.Fatal("panel navigation in wrong category")
			}
			counts[f.Group]++
		}
		if f.Group == "Path suggestions" {
			if f.Category != "terminal" {
				t.Fatal("completion in wrong category")
			}
			counts[f.Group]++
		}
	}
	if counts["Typing and focus"] != 5 || counts["Path suggestions"] != 7 {
		t.Fatal("settings lost while regrouping")
	}

}

func TestSettingsSearchCacheInvalidation(t *testing.T) {
	old := config.App
	defer func() { config.App = old; InitLang() }()
	config.App.Language = "en"
	col := f4settings.Collection{ID: "records", Category: "keyboard", Group: "Records", Label: f4settings.Text{English: "Saved records", Translations: map[string]string{"ru": "Русский список"}}, NameField: "name"}
	record := f4settings.Record{ID: "one", Values: map[string]string{"name": "original", "secret": "unsearchable-secret"}}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{"records": {record}})
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Collections: []f4settings.Collection{col}}, draft: d}})
	c.selectCategory("keyboard")
	attr := vtui.SetRGBBoth(0, 0xf0e0d0, 0x123456)
	row := settingsRecordRow{c, col, d.Records["records"][0]}
	c.query = "target"
	c.updateMatches()
	for i := 0; i < 3; i++ {
		if c.categoryMatches("keyboard") != 0 || row.GetCellAttr(0, attr) != vtui.DimColor(attr) {
			t.Fatal("initial no-match result")
		}
	}
	// The same path used after an inline record edit invalidates both counts and row colors.
	d.Records["records"][0].Values["name"] = "target"
	c.updateMatches()
	if c.categoryMatches("keyboard") != 1 || row.GetCellAttr(0, attr) != attr {
		t.Fatal("renamed record kept stale search result")
	}
	c.query = "Русский" // Direct query/language changes are also detected defensively.
	if c.categoryMatches("keyboard") != 0 {
		t.Fatal("query change reused old results")
	}
	config.App.Language = "ru"
	if c.categoryMatches("keyboard") != 1 || row.GetCellAttr(0, attr) != attr {
		t.Fatal("language change reused old results")
	}
	c.query = "unsearchable-secret"
	if c.categoryMatches("keyboard") != 0 || row.GetCellAttr(0, attr) != vtui.DimColor(attr) {
		t.Fatal("record contents leaked into search")
	}
	c.query = "target"
	c.updateMatches()
	if c.categoryMatches("keyboard") != 1 {
		t.Fatal("missing record")
	}
	d.Records["records"] = nil
	c.rebuildCategory()
	if c.categoryMatches("keyboard") != 0 {
		t.Fatal("deleted record retained in counts")
	}
	c.query = ""
	c.updateMatches()
	if row.GetCellAttr(0, attr) != attr {
		t.Fatal("clearing query left row dimmed")
	}
}
