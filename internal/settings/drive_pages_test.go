package settings

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
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
	// f4#1148): Physical Disks, the first tool by default, then Other panel and
	// Temporary panel, Windows Registry among them only where there is one.
	if len(labels) == 0 || !strings.HasPrefix(labels[0], "Physical Disks") {
		t.Fatalf("tool check boxes = %v, want Physical Disks first", labels)
	}
	labels = labels[1:]
	want := "[Other panel Temporary panel A Alpha tool Beta tool]"
	if len(labels) == 5 {
		want = "[Other panel Temporary panel Windows Registry A Alpha tool Beta tool]"
	}
	if fmt.Sprint(labels) != want {
		t.Errorf("tool check boxes = %v, want %s", labels, want)
	}
}

// f4#1148 (mitek1977-coder): Drive options names its check boxes in words
// (12.10 list, point 5), leaves out the tool options, which the Tools page
// already covers (Show Bookmarks is on the Bookmarks page), says nothing about when an
// option takes effect, and does not list the tools again.
func TestDriveOptionsPageNamesAndNoToolsList(t *testing.T) {
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

	var names []string
	for _, row := range c.page.rows {
		if row.control != nil && row.control.GetId() == "collection:drive-tools" {
			t.Error("the tools are listed again on the Drive options page")
		}
		if row.field.Timing != "" && strings.HasPrefix(row.field.ID, "DriveMenuOptions.") {
			t.Errorf("%s still says it takes effect %q", row.field.ID, row.field.Timing)
		}
		if b, ok := row.control.(*settingsCheckbox); ok && strings.HasPrefix(row.field.ID, "DriveMenuOptions.") {
			names = append(names, strings.TrimSpace(b.lines[0]))
		}
	}
	want := "[Show disk type Show disk label Use shell name Show file system type Show size Show size float " +
		"Show network path Show removable drive parameters Show CD drive parameters Show remote drive parameters " +
		"Detect virtual drives]"
	if fmt.Sprint(names) != want {
		t.Errorf("check boxes = %v\nwant %s", names, want)
	}
}

// f4#1148 (mitek1977-coder): "Show size float" sits four columns in under
// "Show size" and is grayed out while "Show size" is off.
func TestDriveOptionsShowSizeFloatDependsOnShowSize(t *testing.T) {
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

	var size, float *settingsRow
	for _, row := range c.page.rows {
		switch row.field.ID {
		case driveMenuOptionFieldID("Drive.ShowSize"):
			size = row
		case driveMenuOptionFieldID("Drive.ShowSizeFloat"):
			float = row
		}
	}
	if size == nil || float == nil {
		t.Fatal("Show size or Show size float is not on the page")
	}
	if float.controlX != 4 || size.controlX != 0 {
		t.Errorf("indents: Show size %d, Show size float %d; want 0 and 4", size.controlX, float.controlX)
	}
	box := size.control.(*settingsCheckbox)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 45)
	set := func(on bool) {
		if (box.State == 1) != on {
			box.Toggle()
		}
		c.Show(scr)
	}
	set(false)
	if !float.control.IsDisabled() {
		t.Error("Show size float is available with Show size off")
	}
	set(true)
	if float.control.IsDisabled() {
		t.Error("Show size float is grayed out with Show size on")
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

// f4#1148 (mitek1977-coder): F9 in the drive menu opens the window on the
// page it was closed on last time, not always on Drive options.
func TestDriveChooserWindowReopensOnTheLastPage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := lastDriveChooserPage
	t.Cleanup(func() { lastDriveChooserPage = old })
	lastDriveChooserPage = ""

	open := func() *settingsCenter {
		t.Helper()
		if !OpenCategoryOnly("drives") {
			t.Fatal("the window did not open")
		}
		c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
		if !ok {
			t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
		}
		return c
	}
	c := open()
	if c.category != driveOptionsPage {
		t.Fatalf("first open: page = %q, want %q", c.category, driveOptionsPage)
	}
	c.selectCategory(driveBookmarksPage)
	c.OnResult(-1)
	vtui.FrameManager.Pop()

	c = open()
	defer vtui.FrameManager.Pop()
	if c.category != driveBookmarksPage {
		t.Fatalf("reopened: page = %q, want %q", c.category, driveBookmarksPage)
	}
	if lastSettingsCategory == driveBookmarksPage {
		t.Errorf("the main Settings window would open on %q too", driveBookmarksPage)
	}
}

// f4#1148 (mitek1977-coder, 12.10 list, second point 5): on the Tools page
// Up and Down stand one blank line under the tools, and one more blank line
// down is "Hot key:" with a one-character field one column after it. The
// field shows the selected tool's hot key; Apply binds what it holds, as F4
// on the tool in the drive menu does.
func TestDriveToolsPageHotKeyField(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := sysinfo.DriveRegistrySnapshot()
	t.Cleanup(func() { sysinfo.SetDrives(old) })
	sysinfo.SetDrives([]sysinfo.DriveEntry{{Name: "Alpha tool"}, {Name: "Beta tool"}})
	oldHotkeys := keymap.GlobalHotkeysMgr
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = oldHotkeys })
	alpha, beta := panel.DriveToolActionName("Alpha tool"), panel.DriveToolActionName("Beta tool")
	hm := &keymap.HotkeyManager{Bindings: map[string]map[string]string{"Shell": {"A": beta}}}
	keymap.GlobalHotkeysMgr = hm

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

	var lastTool, bar, hotkeyRow *settingsRow
	boxes := map[string]*settingsCheckbox{}
	for _, row := range c.page.rows {
		switch control := row.control.(type) {
		case *settingsCheckbox:
			if control.boxOnly {
				lastTool = row
				boxes[row.field.Label.English] = control
			}
		case *settingsButtonRow:
			bar = row
		case *panel.HotkeyEdit:
			hotkeyRow = row
		}
	}
	if lastTool == nil || bar == nil || hotkeyRow == nil {
		t.Fatalf("tools %v, Up/Down %v, Hot key %v: want all three on the page", lastTool != nil, bar != nil, hotkeyRow != nil)
	}
	if bar.y != lastTool.y+lastTool.height+1 {
		t.Errorf("Up/Down at line %d, last tool at %d: want one blank line between", bar.y, lastTool.y)
	}
	if hotkeyRow.y != bar.y+bar.height+1 {
		t.Errorf("Hot key: at line %d, Up/Down at %d: want one blank line between", hotkeyRow.y, bar.y)
	}
	field := hotkeyRow.control.(*panel.HotkeyEdit)
	if want := c.page.X1 + 2 + vtui.StringWidth("Hot key:") + 1; field.X1 != want || field.X2 != want {
		t.Errorf("hot key field at columns %d..%d, want one cell at %d", field.X1, field.X2, want)
	}
	if field.Y1 != c.page.Y1+hotkeyRow.y-c.page.scroll {
		t.Errorf("hot key field on line %d, want the line of its label", field.Y1)
	}

	key := func(e vtui.UIElement, vk uint16, ch rune) {
		e.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, Char: ch})
	}
	boxes["Beta tool"].SetFocus(true)
	if got := field.GetText(); got != "A" {
		t.Errorf("Beta tool selected: field = %q, want A", got)
	}
	boxes["Alpha tool"].SetFocus(true)
	if got := field.GetText(); got != "" {
		t.Errorf("Alpha tool selected: field = %q, want empty", got)
	}
	key(field, 'B', 'b')
	boxes["Beta tool"].SetFocus(true)
	key(field, vtinput.VK_DELETE, 0)
	if area, k := keymap.ConfiguredHotkeyBinding(hm, alpha); k != "" {
		t.Fatalf("before Apply Alpha tool already has %s:%s", area, k)
	}

	c.commit(false)
	if _, k := keymap.ConfiguredHotkeyBinding(hm, alpha); k != "B" {
		t.Errorf("after Apply Alpha tool's hot key = %q, want B", k)
	}
	if _, k := keymap.ConfiguredHotkeyBinding(hm, beta); k != "" {
		t.Errorf("after Apply Beta tool's hot key = %q, want none", k)
	}
}

// f4#1148 (mitek1977-coder, 12.10 list, point 6): the Links page edits a
// link's hot key as the drive menu's link dialog does, in a one-character
// field after "Hot key:" on the same line, with no chord field and no Capture.
func TestDriveLinksPageHotKeyField(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := panel.DriveBookmarksFilePath()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
	if err := panel.SaveDriveBookmarks(path, []panel.DriveBookmark{{Name: "Home", Path: "/home", Hotkey: "H"}}); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 99, 30)
	c.selectCategory(driveLinksPage)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 31)
	c.Show(scr)

	var field *panel.HotkeyEdit
	var row *settingsRow
	for _, r := range c.page.rows {
		if _, chord := r.control.(*settingsChord); chord {
			t.Errorf("the Links page still has a chord field with Capture (%s)", r.field.ID)
		}
		if e, ok := r.control.(*panel.HotkeyEdit); ok && r.field.ID == "link.Hotkey" {
			field, row = e, r
		}
	}
	if field == nil {
		t.Fatal("no one-character hot key field for the link")
	}
	if got := row.field.Label.Resolve("English", func(string) string { return "" }); got != "Hot key:" {
		t.Errorf("label = %q, want Hot key:", got)
	}
	if want := c.page.X1 + 2 + vtui.StringWidth("Hot key:") + 1; field.X1 != want || field.X2 != want {
		t.Errorf("field at columns %d..%d, want one cell at %d", field.X1, field.X2, want)
	}
	if field.Y1 != c.page.Y1+row.y-c.page.scroll {
		t.Errorf("field on line %d, want the line of its label", field.Y1)
	}
	if got := field.GetText(); got != "H" {
		t.Errorf("field = %q, want H", got)
	}
	field.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: 'Q', Char: 'q'})

	c.commit(false)
	// The core settings commit in the background.
	deadline := time.After(10 * time.Second)
	for c.running != nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("Apply did not finish; status is %q", c.status)
		}
	}
	links, err := panel.LoadDriveBookmarks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || !strings.EqualFold(links[0].Hotkey, "Q") {
		t.Errorf("links after Apply = %+v, want Home with hot key Q", links)
	}
}

// f4#2171: the Links page takes a link's global hot key in a field the key is
// pressed in, on the line of its label, with no Capture button; Apply writes
// it to the links file.
func TestDriveLinksPageGlobalHotkeyField(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := panel.DriveBookmarksFilePath()
	before, readErr := os.ReadFile(path)
	t.Cleanup(func() {
		if readErr != nil {
			_ = os.Remove(path)
		} else {
			_ = os.WriteFile(path, before, 0o600) // #nosec G703 -- the test's own profile file, put back.
		}
	})
	if err := panel.SaveDriveBookmarks(path, []panel.DriveBookmark{{Name: "Home", Path: "/home", Hotkey: "H"}}); err != nil {
		t.Fatal(err)
	}
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	if !OpenCategoryOnly("drives") {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 99, 30)
	c.selectCategory(driveLinksPage)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(100, 31)
	c.Show(scr)

	var field *panel.GlobalHotkeyEdit
	var row *settingsRow
	for _, r := range c.page.rows {
		if e, ok := r.control.(*panel.GlobalHotkeyEdit); ok && r.field.ID == "link.GlobalHotkey" {
			field, row = e, r
		}
	}
	if field == nil {
		t.Fatal("no global hot key field for the link")
	}
	label := row.field.Label.Resolve("English", func(string) string { return "" })
	if label != "Global hot key:" {
		t.Errorf("label = %q, want Global hot key:", label)
	}
	if want := c.page.X1 + 2 + vtui.StringWidth(label) + 1; field.X1 != want {
		t.Errorf("field starts at column %d, want %d, one blank after its label", field.X1, want)
	}
	if field.Y1 != c.page.Y1+row.y-c.page.scroll {
		t.Errorf("field on line %d, want the line of its label", field.Y1)
	}
	field.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F12,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.LeftAltPressed | vtinput.ShiftPressed})
	if got := field.GetText(); got != "Ctrl+Alt+Shift+F12" {
		t.Errorf("field shows %q, want Ctrl+Alt+Shift+F12", got)
	}

	c.commit(false)
	deadline := time.After(10 * time.Second)
	for c.running != nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("Apply did not finish; status is %q", c.status)
		}
	}
	links, err := panel.LoadDriveBookmarks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].GlobalHotkey != "CtrlAltShiftF12" {
		t.Errorf("links after Apply = %+v, want Home with the global hot key CtrlAltShiftF12; status %q", links, c.status)
	}
}

// f4#2171: in the main Settings window a link's global hot key was a text
// field with Capture. Ctrl+N typed into it opened a workspace, and Capture
// took Ctrl+N without a word; only Apply said the key was taken. Now it is the
// field the key is pressed in, as on the drive menu's Links page: the key
// router hands it every key, a taken key is refused the moment it is pressed,
// a free one goes in, and the key the link has already is not asked about.
func TestSettingsDriveLinkGlobalHotkeyRefusesATakenKeyAtOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// The window lists the Colorer schemes in the background, writing into
	// HOME; that must be over before the test's HOME is removed.
	t.Cleanup(WaitSchemeRefreshes)
	t.Cleanup(action.Snapshot())
	action.RegisterAction(action.Action{Name: "Test.Fork", Area: "Common", Label: "Fork", NativeKeys: []string{"CtrlN"}})
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = &keymap.HotkeyManager{Defaults: map[string]map[string]string{}, Bindings: map[string]map[string]string{}}
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previousHotkeys })
	path := panel.DriveBookmarksFilePath()
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := panel.SaveDriveBookmarks(path, []panel.DriveBookmark{{Name: "Logs", Path: "/logs", GlobalHotkey: "CtrlAltY"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(testutil.SwapFrameManager(t))
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(120, 40)
	vtui.FrameManager.Init(scr)
	if !OpenAt("drives", "drive-links", "", false) {
		t.Fatal("the window did not open")
	}
	c, ok := vtui.FrameManager.GetTopFrame().(*settingsCenter)
	if !ok {
		t.Fatalf("top frame is %T", vtui.FrameManager.GetTopFrame())
	}
	defer vtui.FrameManager.Pop()
	c.SetPosition(0, 0, 119, 39)
	c.Show(scr)

	var field *panel.GlobalHotkeyEdit
	for _, r := range c.page.rows {
		if e, ok := r.control.(*panel.GlobalHotkeyEdit); ok && r.field.ID == "link.GlobalHotkey" {
			field = e
		}
	}
	if field == nil {
		t.Fatal("the link's global hot key is not the field a key is pressed in")
	}
	c.SetFocusedItem(c.page)
	c.page.SetFocusedItem(field)
	if !keymap.FocusCapturesKeys(c) {
		t.Error("the key router would not hand the focused field its keys")
	}

	key := func(vk uint16, mods vtinput.ControlKeyState) *vtinput.InputEvent {
		return &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk, ControlKeyState: mods}
	}
	ctrlAlt := vtinput.ControlKeyState(vtinput.LeftCtrlPressed | vtinput.LeftAltPressed)
	field.ProcessKey(key('N', vtinput.LeftCtrlPressed))
	if top := vtui.FrameManager.GetTopFrame(); top == c {
		t.Error("Ctrl+N, the built-in key of Fork, was not refused")
	} else {
		vtui.FrameManager.Pop()
	}
	if got := field.GetText(); got != "Ctrl+Alt+Y" {
		t.Errorf("after a refused key the field shows %q, want the link's Ctrl+Alt+Y", got)
	}
	field.ProcessKey(key('U', ctrlAlt))
	if top := vtui.FrameManager.GetTopFrame(); top != c || field.GetText() != "Ctrl+Alt+U" {
		t.Errorf("a free key: top %T, field %q", top, field.GetText())
	}
	field.ProcessKey(key('Y', ctrlAlt))
	if top := vtui.FrameManager.GetTopFrame(); top != c || field.GetText() != "Ctrl+Alt+Y" {
		t.Errorf("the link's own key: top %T, field %q", top, field.GetText())
	}
}

// f4#1148 (mitek1977-coder, 15.10 list, point 2): the Bookmarks page of the
// F9 window has "Show Bookmarks" over the list of the folder bookmark slots,
// one blank line between them, and the box clears the drive menu's option;
// the Drive options page does not repeat it.
func TestDriveBookmarksPageShowBookmarksSwitch(t *testing.T) {
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
	id := driveMenuOptionFieldID("Drive.ShowBookmarks")

	c.selectCategory(driveOptionsPage)
	for _, row := range c.page.rows {
		if row.field.ID == id {
			t.Fatal("Show Bookmarks is still on the Drive options page")
		}
	}

	c.selectCategory(driveBookmarksPage)
	var box, list *settingsRow
	for _, row := range c.page.rows {
		switch {
		case row.field.ID == id:
			box = row
		case row.control != nil && row.control.GetId() == "collection:bookmarks":
			list = row
		}
	}
	if box == nil || list == nil {
		t.Fatalf("Bookmarks page: Show Bookmarks %v, bookmark list %v", box != nil, list != nil)
	}
	check, ok := box.control.(*settingsCheckbox)
	if !ok {
		t.Fatalf("Show Bookmarks is a %T, want a check box", box.control)
	}
	if got := strings.TrimSpace(check.lines[0]); got != "Show bookmarks" {
		t.Errorf("check box label = %q, want %q", got, "Show bookmarks")
	}
	if box.y >= list.y {
		t.Fatalf("Show Bookmarks (line %d) is not above the list (line %d)", box.y, list.y)
	}
	if gap := list.y - (box.y + box.height); gap != 1 {
		t.Errorf("%d blank lines between Show Bookmarks and the list, want 1", gap)
	}

	// Apply writes the switch into the drive menu's options.
	check.OnChange(0)
	if got := box.session.draft.Values[id]; got != "false" {
		t.Errorf("draft value after unchecking = %q, want false", got)
	}
}

// f4#1148: the Links page has its own visibility switch immediately above the
// named-links box; this fails when the switch is missing from the catalog.
func TestDriveLinksPageShowLinksSwitch(t *testing.T) {
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
	id := driveMenuOptionFieldID("Drive.ShowLinks")

	c.selectCategory(driveOptionsPage)
	for _, row := range c.page.rows {
		if row.field.ID == id {
			t.Fatal("Show links is still on the Drive options page")
		}
	}

	c.selectCategory(driveLinksPage)
	var box, list *settingsRow
	for _, row := range c.page.rows {
		switch {
		case row.field.ID == id:
			box = row
		case row.control != nil && row.control.GetId() == "collection:drive-links":
			list = row
		}
	}
	if box == nil || list == nil {
		t.Fatalf("Links page: Show links %v, links list %v", box != nil, list != nil)
	}
	check, ok := box.control.(*settingsCheckbox)
	if !ok {
		t.Fatalf("Show links is a %T, want a check box", box.control)
	}
	if got := strings.TrimSpace(check.lines[0]); got != "Show links" {
		t.Errorf("check box label = %q, want %q", got, "Show links")
	}
	if box.y >= list.y {
		t.Fatalf("Show links (line %d) is not above the list (line %d)", box.y, list.y)
	}
	if gap := list.y - (box.y + box.height); gap != 1 {
		t.Errorf("%d blank lines between Show links and the list, want 1", gap)
	}

	// An old options mask has no hide bit and therefore still shows links.
	cfg := config.F4Config{}
	if got := coreSettingValue(cfg, id); got != "true" {
		t.Errorf("Show links for an old options mask = %q, want true", got)
	}
	if err := setCoreSetting(&cfg, id, "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.DriveMenuOptions&config.DriveMenuHideLinks == 0 || coreSettingValue(cfg, id) != "false" {
		t.Errorf("unchecking Show links did not persist: options=%b, value=%q", cfg.DriveMenuOptions, coreSettingValue(cfg, id))
	}
	if err := setCoreSetting(&cfg, id, "true"); err != nil {
		t.Fatal(err)
	}
	if cfg.DriveMenuOptions&config.DriveMenuHideLinks != 0 || coreSettingValue(cfg, id) != "true" {
		t.Errorf("checking Show links did not persist: options=%b, value=%q", cfg.DriveMenuOptions, coreSettingValue(cfg, id))
	}
}
