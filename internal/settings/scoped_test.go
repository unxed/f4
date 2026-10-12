package settings

import (
	"fmt"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func driveLinksCollection() f4settings.Collection {
	return f4settings.Collection{ID: "drive-links", Category: "drives", Group: "Drive links",
		Label: f4settings.Text{English: "Drive links"}, NameField: "link.Name", Ordered: true,
		Fields: []f4settings.Field{
			{ID: "link.Name", Label: f4settings.Text{English: "Link name"}, Kind: f4settings.String},
			{ID: "link.Path", Label: f4settings.Text{English: "Link path"}, Kind: f4settings.Path},
		}}
}

func driveLinksCenter(t *testing.T) (*settingsCenter, *f4settings.Draft) {
	t.Helper()
	var records []f4settings.Record
	for i := 0; i < 3; i++ {
		records = append(records, f4settings.Record{ID: fmt.Sprint(i), Values: map[string]string{
			"link.Name": fmt.Sprintf("Link %d", i), "link.Path": fmt.Sprintf("/path/%d", i)}})
	}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{"drive-links": records})
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories,
		Collections: []f4settings.Collection{driveLinksCollection()}}, draft: d}})
	c.SetPosition(0, 0, 129, 34)
	return c, d
}

// F9 in the drive menu opens settings about the drive chooser, not the
// whole application (#1148).
func TestSettingsRestrictToSingleCategory(t *testing.T) {
	c, d := driveLinksCenter(t)
	defer d.Close()
	c.restrictTo("drives")

	if len(c.categories) != 1 || c.categories[0].ID != "drives" {
		t.Fatalf("scope left %d categories: %+v", len(c.categories), c.categories)
	}
	if c.category != "drives" {
		t.Fatalf("scoped window opened on %q", c.category)
	}
	if c.sidebar.ItemCount != 1 {
		t.Fatalf("sidebar still lists %d categories", c.sidebar.ItemCount)
	}
	if title := c.windowTitle(); title != c.categoryLabel("drives") {
		t.Fatalf("scoped window kept the generic title %q", title)
	}
	// A stale deep link must not drag an out-of-scope page back in.
	c.navigate("updates", "", "", false)
	if c.category != "drives" {
		t.Fatalf("navigation escaped the scope to %q", c.category)
	}
}

func TestSettingsRestrictToUnknownCategoryKeepsWindow(t *testing.T) {
	c, d := driveLinksCenter(t)
	defer d.Close()
	before := len(c.categories)
	c.restrictTo("no-such-category")
	if c.scoped || len(c.categories) != before {
		t.Fatalf("unknown scope narrowed the window to %d categories", len(c.categories))
	}
}

// Deleting a record is destructive and asks before it stages the removal.
func TestSettingsCollectionDeleteAsksFirst(t *testing.T) {
	c, d := driveLinksCenter(t)
	defer d.Close()
	c.restrictTo("drives")
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	c.Show(scr)

	var deleteButton *vtui.Button
	for _, row := range c.page.rows {
		if bar, ok := row.control.(*settingsButtonRow); ok && row.control.GetId() == "collection-actions:drive-links" {
			for _, b := range bar.buttons {
				if b.GetCaption() == Phrase("Delete") {
					deleteButton = b
				}
			}
		}
	}
	if deleteButton == nil {
		t.Fatal("no Delete button on the collection")
	}

	deleteButton.OnClick()
	if len(d.Records["drive-links"]) != 3 {
		t.Fatalf("record was removed without confirmation, %d left", len(d.Records["drive-links"]))
	}
	confirm, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("no confirmation dialog on top, got %T", vtui.FrameManager.GetTopFrame())
	}
	confirm.OnResult(1) // Cancel
	vtui.FrameManager.RemoveFrame(confirm)
	if len(d.Records["drive-links"]) != 3 {
		t.Fatalf("cancelling still removed a record, %d left", len(d.Records["drive-links"]))
	}

	deleteButton.OnClick()
	confirm, ok = vtui.FrameManager.GetTopFrame().(*vtui.Window)
	if !ok {
		t.Fatalf("no confirmation dialog on the second attempt, got %T", vtui.FrameManager.GetTopFrame())
	}
	confirm.OnResult(0) // Delete
	vtui.FrameManager.RemoveFrame(confirm)
	if len(d.Records["drive-links"]) != 2 {
		t.Fatalf("confirmed delete left %d records", len(d.Records["drive-links"]))
	}
}

// The Shortcut field used to accept any text; only spellings the drive
// menu can match survive Apply now.
func TestSettingsDriveLinkShortcutValidation(t *testing.T) {
	for _, value := range []string{"", "Q", "q", "F4", "CtrlF5", "NumDel", "Space", "Ю"} {
		if err := settingsValidateShortcut(value); err != nil {
			t.Errorf("shortcut %q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"anything you like", "всё что угодно", "Ctrl", "F4 or so"} {
		if err := settingsValidateShortcut(value); err == nil {
			t.Errorf("shortcut %q accepted", value)
		}
	}
}

// A click on a record selects it for the fields below, and the list has to
// show which record that is: while it has the focus, and while one of those
// fields has it. On the edit-coloured list the cursor used the dialog cursor
// colour, which is the same black on cyan, so it could not be seen (#1148).
func TestSettingsCollectionCursorIsVisible(t *testing.T) {
	c, d := driveLinksCenter(t)
	defer d.Close()
	c.restrictTo("drives")
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	c.Show(scr)
	control := func(id string) vtui.UIElement {
		for _, row := range c.page.rows {
			if row.control != nil && row.control.GetId() == id {
				return row.control
			}
		}
		t.Fatalf("no control %q on the page", id)
		return nil
	}
	table := func() *vtui.Table { return control("collection:drive-links").(*vtui.Table) }
	click := func(x, y int) {
		c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(x), MouseY: testutil.Int16(y), KeyDown: true, ButtonState: vtinput.FromLeft1stButtonPressed})
		c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(x), MouseY: testutil.Int16(y)})
		c.Show(scr)
	}
	x, y, _, _ := table().GetPosition()
	check := func(when string) {
		t.Helper()
		if table().SelectPos != 2 {
			t.Fatalf("%s: the click selected record %d, not 2", when, table().SelectPos)
		}
		selected := scr.GetCell(x+1, y+2).Attributes
		for _, row := range []int{0, 1} {
			if scr.GetCell(x+1, y+row).Attributes == selected {
				t.Fatalf("%s: the selected record is drawn like record %d", when, row)
			}
		}
	}

	click(x+1, y+2)
	if !table().IsFocused() {
		t.Fatal("the click did not focus the record list")
	}
	check("list focused")

	name := control("record-field:drive-links:link.Name")
	nx, ny, _, _ := name.GetPosition()
	click(nx+1, ny)
	if table().IsFocused() {
		t.Fatal("the click on Link name left the focus on the record list")
	}
	check("field focused")
}

// The Settings button of a plugin opens that plugin's own settings: the fields
// of its category whose ids carry its prefix, under its name (f4#918).
func TestSettingsRestrictToOnePluginsFields(t *testing.T) {
	fields := []f4settings.Field{
		{ID: "visren.WordDiv", Category: "operations", Group: "Visual File Renamer", Label: f4settings.Text{English: "Word delimiters"}, Kind: f4settings.String},
		{ID: "copy.Overwrite", Category: "operations", Group: "Copying", Label: f4settings.Text{English: "Overwrite"}, Kind: f4settings.String},
	}
	d := f4settings.NewDraft(map[string]string{"visren.WordDiv": " ", "copy.Overwrite": "ask"}, nil)
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Fields: fields}, draft: d}})
	c.SetPosition(0, 0, 129, 34)
	c.fieldPrefix, c.fieldTitle = "visren.", "Visual File Renamer"
	c.restrictTo("operations")

	if c.category != "operations" || len(c.categories) != 1 {
		t.Fatalf("scope = %q, %d categories", c.category, len(c.categories))
	}
	if title := c.windowTitle(); title != "Visual File Renamer" {
		t.Errorf("window title = %q, want the plugin's name", title)
	}
	var ids []string
	for _, r := range c.page.rows {
		if !r.heading {
			ids = append(ids, r.field.ID)
		}
	}
	if len(ids) != 1 || ids[0] != "visren.WordDiv" {
		t.Errorf("the page lists %v, want only the plugin's own field", ids)
	}
}
