package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/sdk/f4settings"
	"github.com/unxed/vtui"
)

type settingsTestProvider struct {
	catalog f4settings.Catalog
	calls   int
}

func TestSettingsCollectionFocusAndSemanticRendering(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	col := recordCollection("test-records", "history", "Records", "Ordered records.", "name", []f4settings.Field{recordField("name", "Name", "Record display name.", f4settings.String)})
	var records []f4settings.Record
	for i := 0; i < 12; i++ {
		records = append(records, f4settings.Record{ID: fmt.Sprint(i), Values: map[string]string{"name": fmt.Sprintf("Record %d", i)}})
	}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{col.ID: records})
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: f4settings.Catalog{ID: "test", Categories: Categories, Collections: []f4settings.Collection{col}}, draft: d}})
	c.selectCategory("history")
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(130, 35)
	c.ResizeConsole(80, 25)
	table := c.page.rows[0].control.(*vtui.Table)
	c.SetFocusedItem(c.page)
	c.page.SetFocusedItem(table)
	c.page.scroll = 1
	table.OnSelect(1)
	if c.page.scroll != min(1, c.page.bar.Max) || c.page.GetFocusedItem().GetId() != "collection:"+col.ID {
		t.Fatal("selecting a record lost page position or table focus")
	}
	c.page.scroll = 0
	c.ResizeConsole(130, 35)
	c.SetPosition(0, 0, 129, 34)
	c.page.positionRows()
	fieldRow := c.page.rows[len(c.page.rows)-1]
	fieldRow.field.Unavailable = "Retained compatibility setting"
	for iteration := 0; iteration < 2; iteration++ {
		for j, id := range []int{vtui.ColDialogText, vtui.ColDialogSelectedButton, vtui.ColDialogHighlightText, vtui.ColDialogHighlightSelectedButton, vtui.ColDialogBox, vtui.ColDialogEdit, vtui.ColDialogEditSelected, vtui.ColDialogEditUnchanged, vtui.ColDialogComboSelectedText} {
			vtui.Palette[id] = uint64(0x21 + j + iteration*16)
		}
		c.query = "Record 1"
		c.updateMatches()
		c.Show(scr)
		fx, fy, _, _ := fieldRow.control.GetPosition()
		if !fieldRow.control.(*settingsEdit).readOnly || scr.GetCell(fx, fy).Attributes != vtui.DimColor(vtui.Palette[vtui.ColDialogEdit]) {
			t.Fatalf("theme %d unavailable editor did not follow dialog palette", iteration)
		}
		table = c.page.rows[0].control.(*vtui.Table)
		for _, point := range [][2]int{{0, 0}, {table.ScrollBar.X1, table.ScrollBar.Y1}} {
			if attr := scr.GetCell(point[0], point[1]).Attributes; attr != vtui.Palette[vtui.ColDialogBox] {
				t.Fatalf("theme %d border/scrollbar %v: %x", iteration, point, attr)
			}
		}
		if attr := scr.GetCell(table.X1, table.Y1).Attributes; attr != vtui.DimColor(vtui.Palette[vtui.ColDialogEdit]) {
			t.Fatalf("theme %d nonmatching record: %x", iteration, attr)
		}
		// Record lists mark their cursor in the combo cursor colour (#1148).
		if attr := scr.GetCell(table.X1, table.Y1+1).Attributes; attr != vtui.Palette[vtui.ColDialogComboSelectedText] {
			t.Fatalf("theme %d selected matching record: %x", iteration, attr)
		}
		c.help.text = strings.Repeat("Long explanation with enough words to require independent scrolling. ", 50)
		c.Show(scr)
		if attr := scr.GetCell(c.help.X2, c.help.Y1).Attributes; attr != vtui.Palette[vtui.ColDialogBox] {
			t.Fatalf("theme %d explanation scrollbar: %x", iteration, attr)
		}
	}
}

func (p *settingsTestProvider) Catalog() f4settings.Catalog { return p.catalog }
func (p *settingsTestProvider) Begin(context.Context) (*f4settings.Draft, error) {
	d := f4settings.NewDraft(map[string]string{"qt.scale": "1", "qt.font": "mono"}, nil)
	d.ValidateFunc = func(*f4settings.Draft) map[string]error { p.calls++; return nil }
	d.CommitFunc = func(context.Context, *f4settings.Draft) f4settings.Result {
		p.calls++
		return f4settings.Result{Applied: []string{"qt.scale", "qt.font"}}
	}
	d.CloseFunc = func() { p.calls++ }
	return d, nil
}
func TestSettingsFrontendRegistrationAndRemoval(t *testing.T) {
	p := &settingsTestProvider{catalog: f4settings.Catalog{ID: "test-qt", Categories: []f4settings.Category{{ID: "test-frontend", Label: f4settings.Text{English: "Qt frontend"}}}, Groups: []f4settings.Group{{ID: "qt.render", Category: "test-frontend", Label: f4settings.Text{English: "Rendering"}}, {ID: "qt.text", Category: "test-frontend", Label: f4settings.Text{English: "Fonts"}}}, Fields: []f4settings.Field{f4settings.Scalar("qt.scale", "test-frontend", "qt.render", "Scale", "Qt drawing scale.", f4settings.Integer), f4settings.Scalar("qt.font", "test-frontend", "qt.text", "Font", "Qt font selection.", f4settings.String)}}}
	reg, err := RegisterProvider(p)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Unregister()
	settingsProviders.RLock()
	wrapped := settingsProviders.providers[len(settingsProviders.providers)-1]
	settingsProviders.RUnlock()
	d, _ := wrapped.Begin(context.Background())
	c := newSettingsCenter([]*settingsSession{{provider: wrapped, catalog: wrapped.Catalog(), draft: d, contributed: true}})
	if len(c.page.rows) != 4 || c.groupLabel("qt.text") != "Fonts" {
		t.Fatal("provider groups require renderer changes")
	}
	d.Values["qt.scale"] = "2"
	reg.Unregister()
	c.commit(false)
	if p.calls != 0 || !d.Dirty("qt.scale") {
		t.Fatal("unloaded provider was called or edits lost")
	}
	c.OnResult(0)
	if p.calls != 0 {
		t.Fatal("stale close callback invoked")
	}
}
func TestSettingsApplyThenEditCancelKeepsAppliedBaseline(t *testing.T) {
	before := config.App
	writer := writeSettingsCandidate
	defer func() { config.App = before; writeSettingsCandidate = writer }()
	writes := 0
	writeSettingsCandidate = func(config.F4Config, config.F4Config) error { writes++; return nil }
	config.App.ShowHiddenFiles = false
	config.App.AutoSaveDialogSettings = false
	d, _ := (coreSettingsProvider{}).Begin(context.Background())
	d.Values["ShowHiddenFiles"] = "true"
	r := d.Commit(context.Background())
	if len(r.Errors) > 0 {
		t.Fatal(r.Errors)
	}
	d.Values["ShowHiddenFiles"] = "false"
	d.Close()
	if !config.App.ShowHiddenFiles || writes != 1 {
		t.Fatal("Cancel reverted Apply or wrote the later draft")
	}
}

func TestSettingsPreviewCancelRestoresCompletePalette(t *testing.T) {
	original := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, original)
	for i := range vtui.Palette {
		vtui.Palette[i] = uint64(i + 123)
	}
	want := append([]uint64(nil), vtui.Palette...)
	d, err := (coreSettingsProvider{}).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, style := range theme.AvailableColorStyles() {
		if style.Name != d.Baseline["ColorStyle"] {
			d.Values["ColorStyle"] = style.Name
			break
		}
	}
	if err := d.PreviewFunc(d); err != nil {
		d.Close()
		t.Fatal(err)
	}
	d.Close()
	if !reflect.DeepEqual(vtui.Palette, want) {
		t.Fatal("Cancel lost custom palette entries")
	}
}

func TestSettingsValidatesAllProvidersBeforeSaving(t *testing.T) {
	writes := 0
	makeSession := func(id string, fail bool) *settingsSession {
		d := f4settings.NewDraft(map[string]string{id: "old"}, nil)
		d.Values[id] = "new"
		d.ValidateFunc = func(*f4settings.Draft) map[string]error {
			if fail {
				return map[string]error{id: errors.New("invalid record")}
			}
			return nil
		}
		d.CommitFunc = func(context.Context, *f4settings.Draft) f4settings.Result {
			writes++
			return f4settings.Result{Applied: []string{id}}
		}
		return &settingsSession{catalog: f4settings.Catalog{ID: id}, draft: d}
	}
	a, b := makeSession("first", false), makeSession("second", true)
	defer a.draft.Close()
	defer b.draft.Close()
	c := newSettingsCenter([]*settingsSession{a, b})
	c.commit(true)
	if writes != 0 || c.IsDone() || !a.draft.Dirty("first") || !b.draft.Dirty("second") {
		t.Fatal("validation failure saved drafts or closed the dialog")
	}
}
func TestSettingsSearchCollectionsAndCrossCategoryNavigation(t *testing.T) {
	cols := []f4settings.Collection{{ID: "sites", Category: "network", Group: "Connections", Label: f4settings.Text{English: "Sites"}, Description: f4settings.Text{English: "Saved connections"}, NameField: "name", Fields: []f4settings.Field{recordField("name", "Display name", "Name of this connection", f4settings.String), recordField("password", "Password", "Authentication secret", f4settings.Secret)}}}
	d := f4settings.NewDraft(map[string]string{"theme": "dark"}, map[string][]f4settings.Record{"sites": {{ID: "one", Values: map[string]string{"name": "Office", "password": "secretneedle"}}, {ID: "two", Values: map[string]string{"name": "Home", "password": "other"}}}})
	catalog := f4settings.Catalog{ID: "test", Categories: Categories, Fields: []f4settings.Field{f4settings.Scalar("theme", "appearance", "Colors", "Theme", "Color palette", f4settings.String)}, Collections: cols}
	c := newSettingsCenter([]*settingsSession{{catalog: catalog, draft: d}})
	defer d.Close()
	c.query = "office"
	c.updateMatches()
	if c.categoryMatches("network") != 1 {
		t.Fatal("record names are not indexed")
	}
	c.nextMatch(1)
	if c.category != "network" {
		t.Fatal("match navigation did not cross categories")
	}
	before := append([]f4settings.Record(nil), d.Records["sites"]...)
	c.query = "secretneedle"
	c.updateMatches()
	if c.categoryMatches("network") != 0 {
		t.Fatal("secret entered search index")
	}
	if !reflect.DeepEqual(before, d.Records["sites"]) {
		t.Fatal("search reordered records")
	}
	for _, r := range c.page.rows {
		if r.control != nil && r.control.IsDisabled() {
			t.Fatal("no-match search disabled editing")
		}
	}
	c.query = "network"
	c.updateMatches()
	if c.categoryMatches("network") == 0 {
		t.Fatal("category name did not match descendants")
	}
	c.query = ""
	c.updateMatches()
	for _, r := range c.page.rows {
		if !r.match {
			t.Fatal("clear search did not restore row")
		}
	}
}
func TestSettingsRecordStorePartialFailureAndRevision(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	_ = os.WriteFile(a, []byte("old"), 0600)
	_ = os.WriteFile(b, []byte("old"), 0600)
	store := func(id, path string, fail bool) settingsRecordStore {
		return settingsRecordStore{collection: recordCollection(id, "history", id, "Store description", "name", []f4settings.Field{recordField("name", "Name", "Record name", f4settings.String)}), path: path, load: func() ([]f4settings.Record, error) {
			return []f4settings.Record{{ID: id + ".one", Values: map[string]string{"name": "old"}}}, nil
		}, save: func(rows []f4settings.Record) error {
			if fail {
				return errors.New("disk full")
			}
			return os.WriteFile(path, []byte(rows[0].Values["name"]), 0600)
		}}
	}
	p := coreRecordSettingsProvider{stores: []settingsRecordStore{store("a", a, false), store("b", b, true)}}
	d, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Records["a"][0].Values["name"] = "new"
	d.Records["b"][0].Values["name"] = "new"
	result := d.Commit(context.Background())
	if d.Dirty("a") || !d.Dirty("b") || result.Errors["b"] == nil {
		t.Fatal("partial save was not rebased correctly")
	}
	_ = os.WriteFile(b, []byte("concurrent"), 0600)
	if len(d.Validate()) == 0 {
		t.Fatal("concurrent source revision ignored")
	}
}

// The installed catalog (far2l's colorer/configs) lists its colour styles
// through external XML entities such as &catalog-rgb;. The encoding/xml reader
// this used to be stopped at the first one, and the Settings Center offered no
// styles at all. Colorer reads the catalog now, and brings the user's own
// colour styles along (issue #277).
func TestSettingsSchemeEnumerationAndWorktreeIdentity(t *testing.T) {
	oldCatalog, oldUserHrd := config.App.EditorColorerCatalog, config.App.EditorColorerUserHrd
	defer func() { config.App.EditorColorerCatalog, config.App.EditorColorerUserHrd = oldCatalog, oldUserHrd }()
	dir := t.TempDir()
	config.App.EditorColorerCatalog = dir
	base := filepath.Join(dir, "base")
	_ = os.MkdirAll(filepath.Join(base, "hrd"), 0700)
	_ = os.WriteFile(filepath.Join(base, "catalog.xml"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE catalog [
    <!ENTITY hrd "hrd">
    <!ENTITY catalog-rgb SYSTEM "hrd/catalog-rgb.xml">
]>
<catalog xmlns="http://colorer.github.io/schema/v1/catalog">
    <hrc-sets/>
    <hrd-sets>
        &catalog-rgb;
    </hrd-sets>
</catalog>
`), 0600)
	_ = os.WriteFile(filepath.Join(base, "hrd", "catalog-rgb.xml"), []byte(`
        <hrd class="rgb" name="first" description="First scheme">
            <location link="&hrd;/first.hrd"/>
        </hrd>
        <hrd class="text" name="other" description="Other">
            <location link="&hrd;/first.hrd"/>
        </hrd>
`), 0600)
	_ = os.WriteFile(filepath.Join(base, "hrd", "first.hrd"), []byte(`<hrd xmlns="http://colorer.sf.net/2003/hrd"/>`), 0600)
	user := t.TempDir()
	_ = os.WriteFile(filepath.Join(user, "mine.hrd"), []byte(`<hrd xmlns="http://colorer.sf.net/2003/hrd" class="rgb" name="mine" description="My style"/>`), 0600)
	config.App.EditorColorerUserHrd = user

	names := map[string]bool{}
	for _, scheme := range settingsColorerSchemes() {
		names[scheme.Name] = true
	}
	if !names["first"] || !names["mine"] || names["other"] {
		t.Fatalf("styles listed: %v; want first and mine, and no text-class style", names)
	}
}

// A user's hrd-sets file can live anywhere on disk, and a <location link> in
// it names a sibling .hrd file relative to that file, not to catalog.xml
// (f4#277, reported by montoner0). Unlike the folder-of-.hrd-files case
// above, an hrd-sets file's <location link> is real indirection Colorer
// itself resolves, so this exercises the fix through an actual session
// instead of only the rewrite helper (colorer_userhrd_location_test.go).
func TestSettingsSchemeEnumerationUserHrdLocationLinkElsewhere(t *testing.T) {
	oldCatalog, oldUserHrd := config.App.EditorColorerCatalog, config.App.EditorColorerUserHrd
	defer func() { config.App.EditorColorerCatalog, config.App.EditorColorerUserHrd = oldCatalog, oldUserHrd }()
	dir := t.TempDir()
	config.App.EditorColorerCatalog = dir
	base := filepath.Join(dir, "base")
	_ = os.MkdirAll(filepath.Join(base, "hrd"), 0700)
	_ = os.WriteFile(filepath.Join(base, "catalog.xml"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<catalog xmlns="http://colorer.github.io/schema/v1/catalog">
    <hrc-sets/>
    <hrd-sets/>
</catalog>
`), 0600)

	// The user's own files live in a directory with no relation to dir/base,
	// exactly the montoner0 case: "пользовательские файлы могут лежать где
	// угодно" ("user files can live anywhere").
	user := t.TempDir()
	_ = os.WriteFile(filepath.Join(user, "elsewhere.hrd"), []byte(`<hrd xmlns="http://colorer.sf.net/2003/hrd" class="rgb" name="elsewhere" description="Elsewhere"/>`), 0600)
	_ = os.WriteFile(filepath.Join(user, "styles.xml"), []byte(`<?xml version="1.0" encoding="UTF-8"?>
<hrd-sets>
    <hrd class="rgb" name="mine" description="My style">
        <location link="elsewhere.hrd"/>
    </hrd>
</hrd-sets>
`), 0600)
	config.App.EditorColorerUserHrd = filepath.Join(user, "styles.xml")

	names := map[string]bool{}
	for _, scheme := range settingsColorerSchemes() {
		names[scheme.Name] = true
	}
	if !names["mine"] {
		t.Fatalf("styles listed: %v; want mine, whose hrd-sets file links to elsewhere.hrd relative to itself", names)
	}
}

func TestSettingsAllCategoryLayouts(t *testing.T) {
	p := coreSettingsProvider{}
	d, _ := p.Begin(context.Background())
	defer d.Close()
	c := newSettingsCenter([]*settingsSession{{catalog: p.Catalog(), draft: d}})
	scr := vtui.NewSilentScreenBuf()
	for _, size := range [][2]int{{80, 25}, {110, 25}, {160, 50}} {
		c.ResizeConsole(size[0], size[1])
		scr.AllocBuf(size[0], size[1])
		for _, cat := range c.categories {
			c.selectCategory(cat.ID)
			c.Show(scr)
			if c.page.X2 >= size[0] || c.page.Y2 >= c.apply.Y1 || c.help.Y2 >= c.apply.Y1 {
				t.Fatalf("%s clipped into fixed controls", cat.ID)
			}
			if c.page.total > c.page.Y2-c.page.Y1+1 {
				c.page.scroll = c.page.bar.Max
				c.page.positionRows()
				c.Show(scr)
			}
			for _, b := range []*vtui.Button{c.previous.Button, c.next.Button, c.apply, c.ok, c.cancel} {
				if b.X2 >= size[0] {
					t.Fatalf("button outside %v", size)
				}
			}
		}
	}
}
