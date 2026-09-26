package app

import (
	"github.com/unxed/f4/internal/panel"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

const testSortGroupsIni = `
[SortGroup_2]
Name = Images
Mask = *.png, *.jpg

[SortGroup_1]
Name = Executables
Group = 0
IncludeAttributes = executable
ExcludeAttributes = directory

[SortGroup_10]
Mask = *.txt
Group = 5
`

func useTestSortGroups(t *testing.T, text string) {
	t.Helper()
	previous := panel.GlobalSortGroups
	panel.GlobalSortGroups = &panel.SortGroupSet{}
	panel.GlobalSortGroups.LoadFromIni(ini.Parse(strings.NewReader(text)))
	t.Cleanup(func() { panel.GlobalSortGroups = previous })
}

func newSortGroupPanel(t *testing.T) *panel.FileSystemPanel {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	fp := panel.NewFileSystemPanel(0, 0, 80, 24, vfs.NewNullVFS(0))
	if fp.CancelLoad != nil {
		fp.CancelLoad()
	}
	return fp
}

func TestParseSortGroupsOrdersSectionsNumericallyAndHonoursGroupKey(t *testing.T) {
	groups := panel.ParseSortGroups(ini.Parse(strings.NewReader(testSortGroupsIni)))
	if len(groups) != 3 {
		t.Fatalf("parsed %d groups, want 3", len(groups))
	}
	// SortGroup_10 must come after SortGroup_2: the suffix is a number, not a
	// string.
	wantNames := []string{"Executables", "Images", "*.txt"}
	for i, want := range wantNames {
		if groups[i].Name != want {
			t.Errorf("group %d name = %q, want %q", i, groups[i].Name, want)
		}
	}
	wantOrders := []int{0, 1, 5}
	for i, want := range wantOrders {
		if groups[i].Order != want {
			t.Errorf("group %d order = %d, want %d", i, groups[i].Order, want)
		}
	}
}

func TestSortGroupSetGroupOfFallsBackToDefaultGroup(t *testing.T) {
	useTestSortGroups(t, testSortGroupsIni)

	cases := []struct {
		item vfs.VFSItem
		want int
	}{
		{vfs.VFSItem{Name: "run.sh", IsExecutable: true}, 0},
		{vfs.VFSItem{Name: "photo.PNG"}, 1},
		{vfs.VFSItem{Name: "notes.txt"}, 5},
		{vfs.VFSItem{Name: "data.bin"}, panel.DefaultSortGroupOrder},
	}
	for _, tc := range cases {
		item := tc.item
		if got := panel.GlobalSortGroups.GroupOf(&item); got != tc.want {
			t.Errorf("GroupOf(%q) = %d, want %d", item.Name, got, tc.want)
		}
	}
}

func TestSortEntriesClustersByGroupBeforeSortMode(t *testing.T) {
	useTestSortGroups(t, testSortGroupsIni)
	fp := newSortGroupPanel(t)

	fp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "readme.md"}},
		{VFSItem: vfs.VFSItem{Name: "b.png"}},
		{VFSItem: vfs.VFSItem{Name: "notes.txt"}},
		{VFSItem: vfs.VFSItem{Name: "sub", IsDir: true}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsExecutable: true}},
		{VFSItem: vfs.VFSItem{Name: "a.png"}},
	}
	fp.SortMode = panel.SortName
	fp.UseSortGroups = true
	fp.SortEntries()

	want := []string{"..", "sub", "run.sh", "a.png", "b.png", "notes.txt", "readme.md"}
	if got := entryNames(fp.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped sort = %v, want %v", got, want)
	}
}

func TestSortEntriesKeepsGroupOrderWhenReversed(t *testing.T) {
	useTestSortGroups(t, testSortGroupsIni)
	fp := newSortGroupPanel(t)

	fp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "a.png"}},
		{VFSItem: vfs.VFSItem{Name: "readme.md"}},
		{VFSItem: vfs.VFSItem{Name: "b.png"}},
		{VFSItem: vfs.VFSItem{Name: "run.sh", IsExecutable: true}},
	}
	fp.SortMode = panel.SortName
	fp.SortReverse = true
	fp.UseSortGroups = true
	fp.SortEntries()

	// Reversing flips the order inside a group; the groups themselves stay
	// where their configuration put them.
	want := []string{"run.sh", "b.png", "a.png", "readme.md"}
	if got := entryNames(fp.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("reversed grouped sort = %v, want %v", got, want)
	}
}

func TestSortEntriesGroupsUnsortedPanelWithoutReordering(t *testing.T) {
	useTestSortGroups(t, testSortGroupsIni)
	fp := newSortGroupPanel(t)

	fp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "z.md"}},
		{VFSItem: vfs.VFSItem{Name: "b.png"}},
		{VFSItem: vfs.VFSItem{Name: "a.md"}},
		{VFSItem: vfs.VFSItem{Name: "a.png"}},
	}
	fp.SortMode = panel.SortUnsorted
	fp.UseSortGroups = true
	fp.SortEntries()

	// Only the clustering moves rows: inside a group the original order stays.
	want := []string{"b.png", "a.png", "z.md", "a.md"}
	if got := entryNames(fp.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped unsorted panel = %v, want %v", got, want)
	}
}

func TestSortEntriesIgnoresGroupsWhenPanelHasThemOff(t *testing.T) {
	useTestSortGroups(t, testSortGroupsIni)
	fp := newSortGroupPanel(t)

	fp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "b.png"}},
		{VFSItem: vfs.VFSItem{Name: "a.md"}},
	}
	fp.SortMode = panel.SortName
	fp.SortEntries()

	want := []string{"a.md", "b.png"}
	if got := entryNames(fp.Entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("ungrouped sort = %v, want %v", got, want)
	}
}

func TestSortEntriesWithoutConfiguredGroupsIsANoOp(t *testing.T) {
	useTestSortGroups(t, "")
	fp := newSortGroupPanel(t)

	fp.Entries = []*panel.FileEntry{
		{VFSItem: vfs.VFSItem{Name: "b.md"}},
		{VFSItem: vfs.VFSItem{Name: "a.md"}},
	}
	fp.SortMode = panel.SortUnsorted
	fp.UseSortGroups = true
	fp.SortEntries()

	if got := entryNames(fp.Entries); !reflect.DeepEqual(got, []string{"b.md", "a.md"}) {
		t.Fatalf("unsorted panel was reordered without configured groups: %v", got)
	}
}

func TestWorkspaceSessionRoundTripsSortGroupFlag(t *testing.T) {
	states := []panel.WorkspaceSessionState{{
		Number: 1, ActivePanel: 0, WidePanel: -1,
		ShowPanels: true, ShowLeft: true, ShowRight: true,
		Left:  panel.PanelSessionState{Path: "/left", ViewMode: int(panel.ViewModeMedium), SortMode: int(panel.SortName), UseSortGroups: true},
		Right: panel.PanelSessionState{Path: "/right", ViewMode: int(panel.ViewModeMedium), SortMode: int(panel.SortName)},
	}}

	var encoded strings.Builder
	panel.WriteWorkspaceSessions(&encoded, states, 0)
	got, _ := panel.LoadWorkspaceSessions(ini.Parse(strings.NewReader(encoded.String())))
	if !reflect.DeepEqual(got, states) {
		t.Fatalf("sort-group flag did not survive the session round trip:\n got: %#v\nwant: %#v", got, states)
	}
}

// TestWorkspaceSessionRoundTripsSortNumericFlag mirrors the sort-group flag
// test above for f4#1471's numeric-sort toggle: it must persist across a
// session save/load the same way every other per-panel sort setting does.
func TestWorkspaceSessionRoundTripsSortNumericFlag(t *testing.T) {
	states := []panel.WorkspaceSessionState{{
		Number: 1, ActivePanel: 0, WidePanel: -1,
		ShowPanels: true, ShowLeft: true, ShowRight: true,
		Left:  panel.PanelSessionState{Path: "/left", ViewMode: int(panel.ViewModeMedium), SortMode: int(panel.SortName), SortNumeric: true},
		Right: panel.PanelSessionState{Path: "/right", ViewMode: int(panel.ViewModeMedium), SortMode: int(panel.SortName)},
	}}

	var encoded strings.Builder
	panel.WriteWorkspaceSessions(&encoded, states, 0)
	got, _ := panel.LoadWorkspaceSessions(ini.Parse(strings.NewReader(encoded.String())))
	if !reflect.DeepEqual(got, states) {
		t.Fatalf("numeric-sort flag did not survive the session round trip:\n got: %#v\nwant: %#v", got, states)
	}
}
