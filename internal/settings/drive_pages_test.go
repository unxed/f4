package settings

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// f4#1148: F9 in the drive menu shows Drive options, Bookmarks and Links as
// pages of their own, and each record list in full.
func TestDriveChooserPagesSplitTheCategory(t *testing.T) {
	var records []f4settings.Record
	for i := 0; i < 9; i++ {
		records = append(records, f4settings.Record{ID: fmt.Sprint(i), Values: map[string]string{
			"link.Name": fmt.Sprintf("Link %d", i), "link.Path": fmt.Sprintf("/path/%d", i)}})
	}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{"drive-links": records})
	defer d.Close()
	session := &settingsSession{catalog: f4settings.Catalog{ID: "test", Categories: Categories,
		Collections: []f4settings.Collection{driveLinksCollection()}}, draft: d}
	original := session.catalog

	splitDriveChooser([]*settingsSession{session})
	if original.Collections[0].Category != "drives" {
		t.Fatalf("the shared catalog was edited in place: %q", original.Collections[0].Category)
	}
	c := newSettingsCenter([]*settingsSession{session})
	c.fullLists = true
	c.restrictTo(driveChooserPageIDs()...)
	c.SetPosition(0, 0, 129, 44)

	var ids []string
	for _, cat := range c.categories {
		ids = append(ids, cat.ID)
	}
	if fmt.Sprint(ids) != fmt.Sprint(driveChooserPageIDs()) {
		t.Fatalf("pages = %v, want %v", ids, driveChooserPageIDs())
	}
	if c.category != driveOptionsPage {
		t.Fatalf("opened on %q", c.category)
	}
	for _, row := range c.page.rows {
		if row.control != nil && row.control.GetId() == "collection:drive-links" {
			t.Fatal("the Links list is on the Drive options page")
		}
	}

	c.selectCategory(driveLinksPage)
	found := false
	for _, row := range c.page.rows {
		if row.control != nil && row.control.GetId() == "collection:drive-links" {
			found = true
			if row.controlHeight != len(records) {
				t.Fatalf("list height %d, want all %d rows", row.controlHeight, len(records))
			}
		}
	}
	if !found {
		t.Fatal("the Links page has no list")
	}
	// Choosing a low row, as a click does, must not scroll the list inside
	// its own window (f4#1148).
	c.offsets["record:drive-links"] = len(records) - 1
	c.rebuildCategory()
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 45)
	c.Show(scr)
	for _, row := range c.page.rows {
		if table, ok := row.control.(*vtui.Table); ok && table.GetId() == "collection:drive-links" {
			if table.TopPos != 0 || table.SelectPos != len(records)-1 {
				t.Fatalf("list scrolled to %d with row %d chosen", table.TopPos, table.SelectPos)
			}
		}
	}
	if got := c.categoryLabel(driveLinksPage); got != "Links" {
		t.Fatalf("page label %q", got)
	}
}

// The real catalogs put bookmarks and drive links on their own pages and keep
// the drive menu's options on the first one.
func TestDriveChooserPagesOnRealCatalogs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sessions, err := beginSettingsSessions(context.Background())
	if err != nil {
		t.Skipf("settings sessions are not available here: %v", err)
	}
	defer func() {
		for _, s := range sessions {
			s.draft.Close()
		}
	}()
	splitDriveChooser(sessions)
	pages := map[string]int{}
	for _, s := range sessions {
		for _, f := range s.catalog.Fields {
			if f.Category == "drives" {
				t.Errorf("field %s is still in the drives category", f.ID)
			}
			pages[f.Category]++
		}
		for _, col := range s.catalog.Collections {
			if col.Category == "drives" {
				t.Errorf("collection %s is still in the drives category", col.ID)
			}
			pages[col.Category]++
		}
	}
	for _, page := range driveChooserPageIDs() {
		// The Tools page comes from its own provider, which only this window adds.
		if page == driveToolsPage {
			continue
		}
		if pages[page] == 0 {
			t.Errorf("page %s has nothing on it", page)
		}
	}
}

// f4#1148: the Tools page has a check box per tool of the drive menu; Apply
// stores the unchecked ones in the visibility file, keeps what was in it, and
// puts the page right after Drive options.
func TestDriveToolsPageHidesAndShowsTools(t *testing.T) {
	path := panel.DriveToolsVisibilityFilePath()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
	if err := panel.SaveDisabledDriveTools(path, []string{"Gone plugin", "Beta tool"}); err != nil {
		t.Fatal(err)
	}
	p := driveToolsProvider{tools: []panel.DriveToolChoice{{Key: "&A Alpha tool", Label: "A Alpha tool"}, {Key: "Beta tool", Label: "Beta tool"}, {Key: "Gamma tool", Label: "Gamma tool"}}}
	cat := p.Catalog()
	if len(cat.Fields) != 3 || cat.Fields[0].Label.English != "A Alpha tool" || cat.Fields[0].Category != driveToolsPage {
		t.Fatalf("catalog fields = %+v", cat.Fields)
	}
	d, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Values["drivetool.0"] != "true" || d.Values["drivetool.1"] != "false" || d.Values["drivetool.2"] != "true" {
		t.Fatalf("check boxes = %v, want Alpha on, Beta off (hidden in the file), Gamma on", d.Values)
	}
	// Show Beta again, hide Gamma.
	d.Values["drivetool.1"], d.Values["drivetool.2"] = "true", "false"
	result := d.CommitFunc(context.Background(), d)
	if len(result.Errors) != 0 {
		t.Fatalf("commit errors: %v", result.Errors)
	}
	got, err := panel.LoadDisabledDriveTools(path)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[Gone plugin Gamma tool]" {
		t.Errorf("hidden tools after Apply = %v, want [Gone plugin Gamma tool] (the unregistered one is kept)", got)
	}

	// The page stands right after Drive options.
	ids := driveChooserPageIDs()
	if ids[0] != driveOptionsPage || ids[1] != driveToolsPage {
		t.Errorf("pages = %v, want Tools right after Drive options", ids)
	}
}

// The window F9 opens in the drive menu has the Tools page second, with a
// check box for each tool registered now.
func TestDriveChooserWindowListsTheToolsOnItsToolsPage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(old) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "&A Alpha tool"}, {Name: "Beta tool"}})

	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	var pages []string
	for _, cat := range c.categories {
		pages = append(pages, cat.ID)
	}
	if fmt.Sprint(pages) != fmt.Sprint(driveChooserPageIDs()) {
		t.Fatalf("pages = %v, want %v", pages, driveChooserPageIDs())
	}
	c.SetPosition(0, 0, 129, 34)
	c.selectCategory(driveToolsPage)
	var labels []string
	for _, row := range c.page.rows {
		if row.control != nil && strings.HasPrefix(row.control.GetId(), "setting:"+driveToolFieldPrefix) {
			labels = append(labels, row.field.Label.English)
		}
	}
	// The built-in tools come first, as in the drive menu (mitek1977-coder,
	// f4#1148); Windows Registry among them only where there is one.
	want := "[Other panel Temporary panel A Alpha tool Beta tool]"
	if len(labels) == 5 {
		want = "[Other panel Temporary panel Windows Registry A Alpha tool Beta tool]"
	}
	if fmt.Sprint(labels) != want {
		t.Errorf("tool check boxes = %v, want %s", labels, want)
	}
}

// f4#1148 (mitek1977-coder): on Drive options the two tool options are named
// ShowTools and SortToolsByHotkey, the second set in four columns under the
// first, and the tools are not listed again under them: they have a page of
// their own in this window.
func TestDriveOptionsPageNamesIndentAndNoToolsList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 129, 44)
	c.selectCategory(driveOptionsPage)

	x := map[string]int{}
	for _, row := range c.page.rows {
		if row.control != nil && row.control.GetId() == "collection:drive-tools" {
			t.Error("the tools are listed again on the Drive options page")
		}
		if b, ok := row.control.(*settingsCheckbox); ok {
			x[strings.TrimSpace(b.lines[0])] = row.controlX
		}
	}
	show, okShow := x["ShowTools"]
	sort, okSort := x["SortToolsByHotkey"]
	if !okShow || !okSort {
		t.Fatalf("no ShowTools / SortToolsByHotkey check boxes among %v", x)
	}
	if sort != show+4 {
		t.Errorf("SortToolsByHotkey at column %d, ShowTools at %d: want four columns in", sort, show)
	}
}

// f4#1148 (mitek1977-coder): on the Tools page a click on a tool's name
// selects its row and leaves it shown or hidden; only a click on "[x]"
// toggles it.
func TestDriveToolsPageClickOnNameSelectsWithoutToggling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(old) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Alpha tool"}, {Name: "Beta tool"}})
	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 129, 34)
	c.selectCategory(driveToolsPage)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	c.Show(scr)

	var boxes []*settingsCheckbox
	for _, row := range c.page.rows {
		if b, ok := row.control.(*settingsCheckbox); ok && strings.HasPrefix(b.GetId(), "setting:"+driveToolFieldPrefix) {
			boxes = append(boxes, b)
		}
	}
	if len(boxes) < 2 {
		t.Fatalf("%d tool check boxes on the Tools page", len(boxes))
	}
	click := func(x, y int) {
		for _, down := range []bool{true, false} {
			buttons := uint32(0)
			if down {
				buttons = vtinput.FromLeft1stButtonPressed
			}
			c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(x), MouseY: testutil.Int16(y), KeyDown: down, ButtonState: buttons})
		}
	}
	b := boxes[1]
	click(b.X1+6, b.Y1)
	if b.State != 1 {
		t.Errorf("a click on the name toggled the tool: state %d", b.State)
	}
	if !b.IsFocused() {
		t.Error("a click on the name did not select the tool's row")
	}
	click(b.X1+1, b.Y1)
	if b.State != 0 {
		t.Errorf("a click on [x] did not toggle the tool: state %d", b.State)
	}

	// Elsewhere in Settings a click on the label still toggles.
	if settingsCheckboxBoxOnly(driveMenuOptionFieldID("Drive.ShowTools")) {
		t.Error("the Drive options check boxes became box-only too")
	}
}

// f4#1148 (mitek1977-coder): Up and Down under the tools move the selected
// tool, which stays selected, and Apply writes the order the drive menu
// follows.
func TestDriveToolsPageUpDownMovesTheSelectedTool(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Apply writes the order file of the shared test profile: put it back,
	// or the other tests read this test's order.
	path := panel.DriveToolsOrderFilePath()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(old) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Alpha tool"}, {Name: "Beta tool"}})
	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 129, 34)
	c.selectCategory(driveToolsPage)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	c.Show(scr)

	labels := func() (names []string, focused string) {
		for _, row := range c.page.rows {
			if b, ok := row.control.(*settingsCheckbox); ok && b.boxOnly {
				names = append(names, row.field.Label.English)
				if b.IsFocused() {
					focused = row.field.Label.English
				}
			}
		}
		return names, focused
	}
	click := func(x, y int) {
		for _, down := range []bool{true, false} {
			buttons := uint32(0)
			if down {
				buttons = vtinput.FromLeft1stButtonPressed
			}
			c.ProcessMouse(&vtinput.InputEvent{Type: vtinput.MouseEventType, MouseX: testutil.Int16(x), MouseY: testutil.Int16(y), KeyDown: down, ButtonState: buttons})
		}
	}
	element := func(id string) vtui.UIElement {
		for _, row := range c.page.rows {
			if row.control != nil && row.control.GetId() == id {
				return row.control
			}
			if bar, ok := row.control.(*settingsButtonRow); ok {
				for _, b := range bar.buttons {
					if b.GetId() == id {
						return b
					}
				}
			}
		}
		t.Fatalf("no %s on the Tools page", id)
		return nil
	}
	names, _ := labels()
	beta := -1
	for i, name := range names {
		if name == "Beta tool" {
			beta = i
		}
	}
	if beta < 1 || names[beta-1] != "Alpha tool" {
		t.Fatalf("tools = %v, want Alpha tool just before Beta tool", names)
	}
	b := element("setting:" + driveToolField(beta)).(*settingsCheckbox)
	c.Show(scr)
	click(b.X1+6, b.Y1)
	up := element("drive-tools-move:Up").(*vtui.Button)
	c.Show(scr)
	click(up.X1+1, up.Y1)

	names, focused := labels()
	if names[beta-1] != "Beta tool" || names[beta] != "Alpha tool" {
		t.Fatalf("after Up tools = %v, want Beta tool before Alpha tool", names)
	}
	if focused != "Beta tool" {
		t.Errorf("after Up the selected tool is %q, want Beta tool", focused)
	}

	c.commit(false)
	order, err := panel.LoadDriveToolsOrder(panel.DriveToolsOrderFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if i := strings.Index(strings.Join(order, "|"), "Beta tool|Alpha tool"); i < 0 {
		t.Errorf("order file = %v, want Beta tool before Alpha tool", order)
	}
	var menu []string
	for _, choice := range panel.DriveMenuToolChoices() {
		menu = append(menu, choice.Label)
	}
	if !strings.Contains(strings.Join(menu, "|"), "Beta tool|Alpha tool") {
		t.Errorf("drive menu tools = %v, want Beta tool before Alpha tool", menu)
	}
}
