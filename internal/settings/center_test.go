package settings

import (
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/testutil"

	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestSettingsDisabledInputUsesDimmedNormalPalette(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	field := f4settings.Scalar("location", "startup", "Locations", "Configuration location", "Current configuration directory.", f4settings.String)
	field.Unavailable = "Informational location"
	d := f4settings.NewDraft(map[string]string{"location": `C:\Users\profile`}, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: []f4settings.Field{field}}, draft: d}})
	c.selectCategory("startup")
	c.SetPosition(0, 0, 129, 34)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	for iteration := 0; iteration < 2; iteration++ {
		vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, testutil.Uint32(0xd0c0b0+iteration*0x101010), testutil.Uint32(0x181820+iteration*0x080808))
		vtui.Palette[vtui.ColDialogEditUnchanged] = vtui.SetRGBBoth(0, 0, 0x303030)
		vtui.Palette[vtui.ColDialogEditSelected] = vtui.SetRGBBoth(0, 0x000001, 0x0000ff)
		vtui.Palette[vtui.ColDialogBox] = vtui.SetRGBBoth(0, testutil.Uint32(0x807060+iteration*0x101010), 0x303030)
		for _, focused := range []vtui.UIElement{c.sidebar, c.page} {
			c.SetFocusedItem(focused)
			for _, row := range c.page.rows {
				if edit, ok := row.control.(*settingsEdit); ok {
					edit.ClearSelection()
				}
			}
			for _, query := range []string{"", "does-not-match"} {
				c.query = query
				c.updateMatches()
				c.Show(scr)
				for _, row := range c.page.rows {
					if row.control == nil {
						continue
					}
					x, y, _, _ := row.control.GetPosition()
					want := vtui.DimColor(vtui.Palette[vtui.ColDialogEdit])
					border := vtui.Palette[vtui.ColDialogBox]
					if query != "" {
						want = vtui.DimColor(want)
						border = vtui.DimColor(border)
					}
					cell := scr.GetCell(x, y)
					if cell.Char != 'C' || cell.Attributes != want {
						t.Fatalf("disabled input cell=%+v, want C with %x", cell, want)
					}
					if !row.control.(*settingsEdit).readOnly || !row.control.CanFocus() {
						t.Fatal("informational input must be focusable and read-only")
					}
					if scr.GetCell(c.page.X1, y).Attributes != border {
						t.Fatal("group border did not follow the active palette")
					}
				}
			}
		}
	}
}

func TestSettingsCatalogComplete(t *testing.T) {
	c := (coreSettingsProvider{}).Catalog()
	if err := f4settings.ValidateCatalog(c); err != nil {
		t.Fatal(err)
	}
	if len(c.Fields) < 120 {
		t.Fatalf("unexpectedly small catalog: %d", len(c.Fields))
	}
	for _, f := range c.Fields {
		if !strings.HasPrefix(f.ID, "DriveMenuOptions.") && !settingsConfigField(reflect.ValueOf(config.App), f.ID).IsValid() {
			t.Fatalf("unknown config field %s", f.ID)
		}
		_ = coreSettingValue(config.App, f.ID)
	}
}

func TestSettingsApplyPreservesUnknownKeysAndRuntimeChanges(t *testing.T) {
	old := config.App
	pathFunc := config.GetUserConfigIniPath
	defer func() { config.App = old; config.GetUserConfigIniPath = pathFunc }()
	path := filepath.Join(t.TempDir(), "settings.ini")
	config.GetUserConfigIniPath = func() string { return path }
	if err := os.WriteFile(path, []byte("[FutureFrontend]\nUnknown = preserve\n[Interface]\nGuiBackend = qt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config.App.GuiBackend = "ext:qt"
	d, err := (coreSettingsProvider{}).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Values["ShowHiddenFiles"] = map[bool]string{true: "false", false: "true"}[config.App.ShowHiddenFiles]
	config.App.GuiCols = 143
	if r := d.Commit(context.Background()); len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	if config.App.GuiCols != 143 || config.App.GuiBackend != "ext:qt" {
		t.Fatal("unrelated configuration overwritten")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Unknown = preserve") {
		t.Fatal("unknown setting removed")
	}
}

func TestSettingsFailedSaveRetainsDraft(t *testing.T) {
	old := config.App
	writer := writeSettingsCandidate
	defer func() { config.App = old; writeSettingsCandidate = writer }()
	writeSettingsCandidate = func(config.F4Config, config.F4Config) error { return errors.New("read-only disk") }
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	d.Values["ShowHiddenFiles"] = "true"
	config.App.ShowHiddenFiles = false
	d.Baseline["ShowHiddenFiles"] = "false"
	r := d.Commit(context.Background())
	if len(r.Errors) == 0 || !d.Dirty("ShowHiddenFiles") || config.App.ShowHiddenFiles {
		t.Fatal("failed save published changes")
	}
}

func TestSettingsCenterRenderThemeAndSearch(t *testing.T) {
	oldPalette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, oldPalette)
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	defer d.Close()
	c.selectCategory("panels")
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	for iteration := 0; iteration < 2; iteration++ {
		for j, id := range []int{vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton, vtui.ColDialogBox} {
			vtui.Palette[id] = uint64(0x10 + j + iteration*16)
		}
		c.query = "hidden"
		c.updateMatches()
		c.Show(scr)
		var match, other *settingsRow
		for _, r := range c.page.rows {
			if r.field.ID == "ShowHiddenFiles" {
				match = r
			}
			if r.field.ID == "ShowDirPrefix" {
				other = r
			}
		}
		if match == nil || other == nil || !match.match || other.match || other.control.IsDisabled() {
			t.Fatal("search changed availability or mismatched rows")
		}
		x, y, _, _ := other.control.GetPosition()
		if y <= c.page.Y2 {
			want := vtui.DimColor(vtui.Palette[vtui.ColDialogText])
			if got := scr.GetCell(x, y).Attributes; got != want {
				t.Fatalf("theme %d dim attr %x, want %x", iteration, got, want)
			}
		}
		c.query = ""
		c.updateMatches()
		c.page.SetFocusedItem(match.control)
		c.SetFocusedItem(c.page)
		c.Show(scr)
		x, y, _, _ = match.control.GetPosition()
		if got := scr.GetCell(x, y).Attributes; got != vtui.Palette[vtui.ColDialogSelectedButton] {
			t.Fatalf("focused theme %d got %x", iteration, got)
		}
		c.ResizeConsole(130, 35)
		c.SetPosition(0, 0, 129, 34)
		scr.AllocBuf(130, 35)
		c.Show(scr)
		if c.help.X1 <= c.page.X2 {
			t.Fatal("wide explanation pane overlaps settings")
		}
		c.ResizeConsole(80, 25)
		scr.AllocBuf(80, 25)
	}
}

func TestSettingsCenterCompactCheckboxesAndResizableLayout(t *testing.T) {
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	c.ResizeConsole(180, 60)
	if c.X1 != 45 || c.X2 != 134 || c.Y2-c.Y1+1 != 45 || !c.ShowZoom {
		t.Fatalf("unexpected initial bounds: %d,%d–%d,%d", c.X1, c.Y1, c.X2, c.Y2)
	}
	c.selectCategory("panels")
	for _, row := range c.page.rows {
		if row.field.Kind != f4settings.Boolean {
			continue
		}
		b, ok := row.control.(*settingsCheckbox)
		if !ok || len(row.label) != 0 || row.height != len(b.lines) || strings.Join(b.lines, " ") != row.field.Label.Resolve(config.App.Language, i18n.Msg) {
			t.Fatalf("checkbox %s has a redundant label or spacing", row.field.ID)
		}
		before := d.Values[row.field.ID]
		b.Toggle()
		if d.Values[row.field.ID] == before {
			t.Fatal("checkbox did not edit draft")
		}
		break
	}
	c.ChangeSize(130, 45)
	c.syncWindowBounds()
	if c.help.X1 <= c.page.X2 || c.page.X1 <= c.X1 {
		t.Fatal("resize did not lay out columns")
	}
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(180, 60)
	for iteration := 0; iteration < 2; iteration++ {
		for j, id := range []int{vtui.ColDialogBox, vtui.ColDialogBoxTitle, vtui.ColDialogEditSelected, vtui.ColDialogSelectedButton} {
			vtui.Palette[id] = uint64(0x31 + j + iteration*16)
		}
		c.SetFocusedItem(c.page)
		c.query = ""
		c.updateMatches()
		c.Show(scr)
		for _, x := range []int{c.sidebar.X2 + 1, c.help.X1 - 1, c.page.X1} {
			if scr.GetCell(x, c.page.Y1).Attributes != vtui.Palette[vtui.ColDialogBox] {
				t.Fatal("separator/group border did not follow palette")
			}
		}
		row := settingsCategoryRow{center: c, category: f4settings.Category{ID: "panels"}}
		if row.GetCellAttr(0, 0) != settingsInactiveCategoryAttr(vtui.Palette[vtui.ColDialogText]) {
			t.Fatal("inactive category selection missing")
		}
		c.SetFocusedItem(c.sidebar)
		if row.GetCellAttr(0, vtui.Palette[vtui.ColDialogSelectedButton]) != vtui.Palette[vtui.ColDialogSelectedButton] {
			t.Fatal("active category selection lost")
		}
		c.query = "no-such-setting-xyz"
		c.updateMatches()
		c.Show(scr)
		if scr.GetCell(c.page.X1, c.page.Y1).Attributes != vtui.DimColor(vtui.Palette[vtui.ColDialogBox]) {
			t.Fatal("unmatched group border not dimmed")
		}
	}
	c.ResizeConsole(80, 25)
	if c.X1 < 0 || c.Y1 < 0 || c.X2 >= 80 || c.Y2 >= 25 {
		t.Fatal("resize left window outside screen")
	}
	c.ProcessMouse(&vtinput.InputEvent{MouseX: testutil.Int16(c.X2), MouseY: testutil.Int16(c.Y2), KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed})
	c.ProcessMouse(&vtinput.InputEvent{MouseX: testutil.Int16(c.X1 + 71), MouseY: testutil.Int16(c.Y1 + 21), ButtonState: vtinput.FromLeft1stButtonPressed, MouseEventFlags: vtinput.MouseMoved})
	c.ProcessMouse(&vtinput.InputEvent{})
	if c.resizing || c.X2-c.X1+1 != 72 || c.Y2-c.Y1+1 != 22 {
		t.Fatal("mouse resizing into content was intercepted")
	}
}

func TestSettingsCenterExclusivePaneFocus(t *testing.T) {
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	c.ResizeConsole(130, 35)
	c.SetPosition(0, 0, 129, 34)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	for iteration := 0; iteration < 2; iteration++ {
		vtui.Palette[vtui.ColDialogText] = vtui.SetRGBBoth(0, 0xeeeeee, testutil.Uint32(0x201040+iteration*0x102030))
		vtui.Palette[vtui.ColDialogSelectedButton] = vtui.SetRGBBoth(0, 0xffffff, 0x0000ff)
		c.SetFocusedItem(c.sidebar)
		c.category = ""
		c.selectCategory("panels")
		for i, cat := range c.categories {
			if cat.ID == "panels" {
				c.sidebar.SetSelectPos(i)
			}
		}
		c.Show(scr)
		control := c.page.GetFocusedItem()
		x, y, _, _ := control.GetPosition()
		if control.IsFocused() || scr.GetCell(x, y).Attributes != vtui.Palette[vtui.ColDialogText] {
			t.Fatal("inactive content displays keyboard focus")
		}
		c.SetFocusedItem(c.page)
		c.Show(scr)
		if !control.IsFocused() || scr.GetCell(x, y).Attributes != vtui.Palette[vtui.ColDialogSelectedButton] {
			t.Fatal("active content lost keyboard focus")
		}
		categoryY := c.sidebar.Y1 + c.sidebar.SelectPos - c.sidebar.TopPos
		attr := scr.GetCell(c.sidebar.X1, categoryY).Attributes
		bg := vtui.GetRGBBack(attr)
		if attr == vtui.Palette[vtui.ColDialogSelectedButton] || (bg>>16)&255 != (bg>>8)&255 || (bg>>8)&255 != bg&255 {
			t.Fatalf("inactive category isn't neutral gray: %x", attr)
		}
		c.rebuildCategory()
		c.Show(scr)
		if !c.page.GetFocusedItem().IsFocused() {
			t.Fatal("rebuilding active content lost focus")
		}
		c.SetFocusedItem(c.sidebar)
		c.Show(scr)
		if c.page.GetFocusedItem().IsFocused() {
			t.Fatal("returning to sidebar left content focused")
		}
	}
}

func TestSettingsActionCaptionsAppearOnce(t *testing.T) {
	d := f4settings.NewDraft(nil, nil)
	defer d.Close()
	label := "Save applied preferences"
	cat := f4settings.Catalog{ID: "test", Categories: Categories, Commands: []f4settings.Command{{ID: "test.save", Category: "workspaces", Group: "Manual saving", Label: f4settings.Text{English: label}, Description: f4settings.Text{English: "Save the applied configuration."}}}}
	c := newSettingsCenter([]*settingsSession{{catalog: cat, draft: d}})
	c.selectCategory("workspaces")
	c.ResizeConsole(130, 35)
	c.SetPosition(0, 0, 129, 34)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	old := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, old)
	row := c.page.rows[0]
	if len(row.label) != 0 || row.height != 1 {
		t.Fatal("action has duplicate label or redundant label space")
	}
	for iteration := 0; iteration < 2; iteration++ {
		vtui.Palette[vtui.ColDialogBoxTitle] = uint64(0x15 + iteration*16)
		vtui.Palette[vtui.ColDialogButton] = uint64(0x12 + iteration*16)
		vtui.Palette[vtui.ColDialogSelectedButton] = uint64(0x13 + iteration*16)
		c.SetFocusedItem(c.sidebar)
		c.Show(scr)
		title := c.categoryLabel(c.category)
		titleX := c.page.X1 + (c.page.X2-c.page.X1+1-vtui.StringWidth(title))/2
		for i, ch := range title {
			cell := scr.GetCell(titleX+i, c.Y1+1)
			if testutil.Rune(cell.Char) != ch || cell.Attributes != vtui.Palette[vtui.ColDialogBoxTitle] {
				t.Fatal("category title is not centered or does not follow the palette")
			}
		}
		x, y, _, _ := row.control.GetPosition()
		if scr.GetCell(x, y).Attributes != vtui.Palette[vtui.ColDialogButton] {
			t.Fatal("normal action palette mismatch")
		}
		c.SetFocusedItem(c.page)
		c.Show(scr)
		if scr.GetCell(x, y).Attributes != vtui.Palette[vtui.ColDialogSelectedButton] {
			t.Fatal("focused action palette mismatch")
		}
		var rendered strings.Builder
		for line := c.page.Y1; line <= c.page.Y2; line++ {
			for col := c.page.X1; col <= c.page.X2; col++ {
				rendered.WriteRune(testutil.Rune(scr.GetCell(col, line).Char))
			}
			rendered.WriteByte('\n')
		}
		if strings.Count(rendered.String(), label) != 1 {
			t.Fatalf("action caption must render once: %q", rendered.String())
		}
		c.describe(row)
		if !strings.Contains(c.help.text, "Save the applied configuration.") {
			t.Fatal("action explanation lost")
		}
		c.query = label
		c.updateMatches()
		if !row.match {
			t.Fatal("action label no longer searchable")
		}
	}
}

func TestSettingsCategoryHeadingNotRepeatedInHelp(t *testing.T) {
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	scr := vtui.NewSilentScreenBuf()
	for _, width := range []int{80, 130} {
		scr.AllocBuf(width, 35)
		c.ResizeConsole(width, 35)
		c.SetPosition(0, 0, width-1, 34)
		for _, cat := range c.categories {
			c.category = ""
			c.selectCategory(cat.ID)
			for _, box := range c.page.boxes {
				last := box.rows[len(box.rows)-1]
				if last.control != nil && box.bottom != last.y+max(len(last.label), last.controlY+max(1, last.controlHeight)) {
					t.Fatalf("%s / %s has blank space before its bottom border", cat.ID, box.title)
				}
			}
			if c.help.text != settingsText("Select", "Select a setting to read what it does.") {
				t.Fatalf("%s has a redundant help heading", cat.ID)
			}
		}
		c.selectCategory("operations")
		c.Show(scr)
		var top strings.Builder
		for x := c.page.X1; x <= c.help.X2; x++ {
			top.WriteRune(testutil.Rune(scr.GetCell(x, c.Y1+1).Char))
		}
		if strings.Count(top.String(), c.categoryLabel("operations")) != 1 {
			t.Fatalf("category heading must appear once: %q", top.String())
		}
	}
}

func TestSettingsPaneKeyboardPolicy(t *testing.T) {
	fields := []f4settings.Field{
		f4settings.Scalar("one", "panels", "Group", "One", "First setting.", f4settings.Boolean),
		f4settings.Scalar("two", "panels", "Group", "Two", "Second setting.", f4settings.Boolean),
		f4settings.Scalar("three", "panels", "Group", "Three", "Third setting.", f4settings.Boolean),
	}
	fields[1].Unavailable = "Unavailable"
	d := f4settings.NewDraft(map[string]string{"one": "false", "two": "false", "three": "false"}, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: fields}, draft: d}})
	c.ResizeConsole(130, 35)
	c.SetPosition(0, 0, 129, 34)
	key := func(k uint16, shift bool) {
		event := &vtinput.InputEvent{KeyDown: true, VirtualKeyCode: k}
		if shift {
			event.ControlKeyState = vtinput.ShiftPressed
		}
		c.ProcessKey(event)
	}
	c.SetFocusedItem(c.sidebar)
	c.sidebar.SetSelectPos(len(c.categories) - 1)
	key(vtinput.VK_DOWN, false)
	if c.GetFocusedItem() != c.sidebar {
		t.Fatal("Down escaped categories")
	}
	c.sidebar.SetSelectPos(0)
	key(vtinput.VK_UP, false)
	if c.GetFocusedItem() != c.search {
		t.Fatal("Up from first category must reach search")
	}
	key(vtinput.VK_DOWN, false)
	if c.GetFocusedItem() != c.sidebar {
		t.Fatal("Down from search must reach categories")
	}
	c.selectCategory("panels")
	c.SetFocusedItem(c.page)
	first, last := c.page.rows[1].control, c.page.rows[3].control
	c.page.SetFocusedItem(first)
	key(vtinput.VK_UP, false)
	if c.GetFocusedItem() != c.page || c.page.GetFocusedItem() != last {
		t.Fatal("Up must wrap inside content")
	}
	key(vtinput.VK_DOWN, false)
	if c.GetFocusedItem() != c.page || c.page.GetFocusedItem() != first {
		t.Fatal("Down must wrap inside content")
	}
	key(vtinput.VK_DOWN, false)
	if c.page.GetFocusedItem() != last {
		t.Fatal("navigation must skip unavailable setting")
	}
	key(vtinput.VK_LEFT, false)
	if c.GetFocusedItem() != c.sidebar {
		t.Fatal("Left from content must reach categories")
	}
	key(vtinput.VK_RIGHT, false)
	if c.GetFocusedItem() != c.page || c.page.GetFocusedItem() != last {
		t.Fatal("Right must return to the remembered content control")
	}
	c.SetFocusedItem(c.search)
	for _, want := range []vtui.UIElement{c.sidebar, c.page, c.apply, c.ok, c.cancel, c.search} {
		key(vtinput.VK_TAB, false)
		if c.GetFocusedItem() != want {
			t.Fatalf("Tab did not follow pane/button order: got %T %s, want %T %s (category %s)", c.GetFocusedItem(), c.GetFocusedItem().GetId(), want, want.GetId(), c.category)
		}
	}
	key(vtinput.VK_TAB, true)
	if c.GetFocusedItem() != c.cancel {
		t.Fatal("Shift+Tab did not reverse focus order")
	}
	key(vtinput.VK_DOWN, false)
	if c.GetFocusedItem() != c.apply {
		t.Fatal("arrows escaped bottom buttons")
	}
	for _, cat := range c.categories {
		if got := c.categoryLabel(cat.ID); got != cat.Label.Resolve(config.App.Language, i18n.Msg) {
			t.Fatalf("duplicated display title: %q", got)
		}
	}
}

func TestSettingsComboInlineLayout(t *testing.T) {
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	c.selectCategory("appearance")
	scr := vtui.NewSilentScreenBuf()
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	for iteration, width := range []int{130, 80, 130} {
		scr.AllocBuf(width, 35)
		c.ResizeConsole(width, 35)
		c.SetPosition(0, 0, width-1, 34)
		vtui.Palette[vtui.ColDialogText] = uint64(0x21 + iteration*16)
		vtui.Palette[vtui.ColDialogEditUnchanged] = uint64(0x22 + iteration*16)
		vtui.Palette[vtui.ColDialogBox] = uint64(0x23 + iteration*16)
		var previous *settingsRow
		for _, row := range c.page.rows {
			if _, ok := row.control.(*vtui.ComboBox); !ok {
				continue
			}
			x, y, x2, _ := row.control.GetPosition()
			if y != c.page.Y1+row.y-c.page.scroll || x <= c.page.X1+2 || x > x2 || row.gap != 0 {
				t.Fatal("combo is not inline and compact")
			}
			if previous != nil && previous.field.Group == row.field.Group && row.y != previous.y+previous.height {
				t.Fatal("blank line between combo rows")
			}
			previous = row
		}
		if previous == nil {
			t.Fatal("missing combos")
		}
		c.query = ""
		c.updateMatches()
		c.Show(scr)
		first := previous
		x, y, _, _ := first.control.GetPosition()
		controlAttr := scr.GetCell(x, y).Attributes
		if scr.GetCell(c.page.X1+2, y).Attributes != vtui.Palette[vtui.ColDialogText] {
			t.Fatal("inline label palette mismatch")
		}
		c.query = "no-such-setting-xyz"
		c.updateMatches()
		c.Show(scr)
		x, y, _, _ = first.control.GetPosition()
		if scr.GetCell(c.page.X1+2, y).Attributes != vtui.DimColor(vtui.Palette[vtui.ColDialogText]) || scr.GetCell(x, y).Attributes != vtui.DimColor(controlAttr) {
			t.Fatal("inline label/control did not dim with live palette")
		}
	}
}

func TestSettingsShortInputsInlineLongInputsFullWidth(t *testing.T) {
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: (coreSettingsProvider{}).Catalog(), draft: d}})
	for _, width := range []int{80, 130} {
		c.ResizeConsole(width, 35)
		c.SetPosition(0, 0, width-1, 34)
		for _, item := range []struct {
			category, id string
			compact      bool
		}{{"appearance", "GuiFontSize", true}, {"network", "ProxyPort", true}, {"editor", "EditorAutoCompleteMask", false}} {
			c.selectCategory(item.category)
			var found bool
			for _, row := range c.page.rows {
				if row.field.ID != item.id {
					continue
				}
				found = true
				x, y, x2, _ := row.control.GetPosition()
				if item.compact {
					if x <= c.page.X1+2 || y != c.page.Y1+row.y-c.page.scroll || row.gap != 0 || x2-x+1 > 10 || x2 < x {
						t.Fatalf("%s isn't a compact inline input", item.id)
					}
				} else if x != c.page.X1+2 || x2 != c.page.X2-4 || y != c.page.Y1+row.y+len(row.label)-c.page.scroll {
					t.Fatalf("%s lost full-width editor", item.id)
				}
				edit := row.control.(*settingsEdit)
				value := "12345678901234567890"
				edit.SetText(value)
				if edit.GetText() != value {
					t.Fatal("compact width truncated the value")
				}
			}
			if !found {
				t.Fatalf("missing %s", item.id)
			}
		}
	}
}

func TestSettingsInactiveZeroMatchCategoryText(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer func() { vtui.Palette = palette }()
	d := f4settings.NewDraft(map[string]string{"value": "true"}, nil)
	defer d.Close()
	field := f4settings.Scalar("value", "startup", "Defaults", "Example", "Example setting.", f4settings.Boolean)
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: []f4settings.Field{field}}, draft: d}})
	c.selectCategory("startup")
	for i, category := range c.categories {
		if category.ID == "startup" {
			c.sidebar.SetSelectPos(i)
		}
	}
	c.SetPosition(0, 0, 149, 39)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(150, 40)
	for _, pair := range [][2]uint32{{0xd0d0d0, 0x434343}, {0xe0e0e0, 0x666666}} {
		vtui.Palette[vtui.ColDialogText] = vtui.SetRGBBoth(0, pair[0], pair[1])
		vtui.Palette[vtui.ColDialogSelectedButton] = vtui.SetRGBBoth(0, 0xfefefe, 0x345678)
		c.search.OnTextChange("no-matching-settings")
		c.SetFocusedItem(c.page)
		c.Show(scr)
		y := c.sidebar.Y1 + c.sidebar.SelectPos
		attr := scr.GetCell(c.sidebar.X1, y).Attributes
		fg, bg := theme.GetColorRGBBoth(attr)
		if fg >= bg || bg-fg < 0x303030 {
			t.Fatalf("inactive zero-match text not sufficiently darker: %06x on %06x", fg, bg)
		}
		_, wantBG := theme.GetColorRGBBoth(settingsInactiveCategoryAttr(vtui.Palette[vtui.ColDialogText]))
		if bg != wantBG {
			t.Fatal("inactive selection background changed")
		}
		c.SetFocusedItem(c.sidebar)
		c.Show(scr)
		if scr.GetCell(c.sidebar.X1, y).Attributes != vtui.SetRGBFore(vtui.Palette[vtui.ColDialogSelectedButton], fg) {
			t.Fatal("active cursor must retain its blue background and use the same dark text")
		}
		c.search.OnTextChange("")
		c.SetFocusedItem(c.page)
		c.Show(scr)
		if scr.GetCell(c.sidebar.X1, y).Attributes != settingsInactiveCategoryAttr(vtui.Palette[vtui.ColDialogText]) {
			t.Fatal("matching category styling changed")
		}
	}
}

func TestHotkeyCategoryHidesDescriptionRendering(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	d := f4settings.NewDraft(nil, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories}, draft: d}})
	for _, width := range []int{80, 140} {
		scr := vtui.NewSilentScreenBuf()
		scr.AllocBuf(width, 30)
		c.SetPosition(0, 0, width-1, 29)
		for iteration := 0; iteration < 2; iteration++ {
			vtui.Palette[vtui.ColDialogText] = vtui.SetRGBBoth(0, testutil.Uint32(0xc0d0e0+iteration*0x010101), 0x202020)
			vtui.Palette[vtui.ColDialogBox] = vtui.SetRGBBoth(0, testutil.Uint32(0x8090a0+iteration*0x010101), 0x303030)
			c.selectCategory("appearance")
			c.help.text = "DESCRIPTION_SENTINEL"
			c.Show(scr)
			if scr.GetCell(c.help.X1, c.help.Y1).Char != 'D' {
				t.Fatal("ordinary category lost description")
			}
			c.selectCategory("hotkeys")
			for _, focus := range []vtui.UIElement{c.sidebar, c.page} {
				c.SetFocusedItem(focus)
				c.Show(scr)
				if c.help.IsVisible() || c.help.CanFocus() {
					t.Fatal("hotkey description pane remains visible/focusable")
				}
				if c.page.X2 != c.X2-2 || c.page.Y2 != c.contentBottom() {
					t.Fatal("hotkey page does not occupy the available area")
				}
				for y := 0; y < 30; y++ {
					var line strings.Builder
					for x := 0; x < width; x++ {
						line.WriteRune(testutil.Rune(scr.GetCell(x, y).Char))
					}
					if strings.Contains(line.String(), "DESCRIPTION_SENTINEL") {
						t.Fatal("hidden description overpainted hotkey page")
					}
				}
			}
			c.selectCategory("appearance")
			c.help.text = "DESCRIPTION_SENTINEL"
			c.Show(scr)
			cell := scr.GetCell(c.help.X1, c.help.Y1)
			if cell.Char != 'D' || cell.Attributes != vtui.Palette[vtui.ColDialogText] {
				t.Fatal("description did not return with current palette")
			}
		}
	}
}
