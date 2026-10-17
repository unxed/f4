package panel

import (
	"reflect"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

// saveEditedMenuItem opens the item editor on items[idx] of the level s is
// on, gives it a new label and presses Save.
func saveEditedMenuItem(t *testing.T, s *userMenuState, idx int, submenu bool, label string) {
	t.Helper()
	showEditItemDialog(s, nil, s.currentItems(), idx, false, submenu)
	dlg, ok := vtui.FrameManager.GetTopFrame().(*userMenuItemDialog)
	if !ok {
		t.Fatal("menu-item editor was not opened")
	}
	dlg.labelEdit.SetText(label)
	for _, child := range dlg.GetChildren() {
		if b, ok := child.(*vtui.Button); ok && b.IsDefault {
			b.OnClick()
			return
		}
	}
	t.Fatal("the editor has no Save button")
}

func TestUserMenuEditingAGroupKeepsItsCommands(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	inner := []UserMenuItem{
		{HotKey: "1", Label: "First", Commands: []string{"echo 1"}},
		{HotKey: "2", Label: "Nested", Submenu: []UserMenuItem{{HotKey: "a", Label: "Deep", Commands: []string{"echo deep"}}}},
	}
	s := &userMenuState{rootItems: []UserMenuItem{
		{HotKey: "g", Label: "Group", Submenu: append([]UserMenuItem(nil), inner...)},
		{HotKey: "c", Label: "Command", Commands: []string{"ls"}},
	}}

	saveEditedMenuItem(t, s, 0, true, "Renamed group")
	if got := s.rootItems[0]; got.Label != "Renamed group" || !reflect.DeepEqual(got.Submenu, inner) {
		t.Fatalf("edited group = %+v, want the new label over the same commands %+v", got, inner)
	}

	s.path = []int{0}
	saveEditedMenuItem(t, s, 1, true, "Renamed nested")
	if got := s.rootItems[0].Submenu[1]; got.Label != "Renamed nested" || !reflect.DeepEqual(got.Submenu, inner[1].Submenu) {
		t.Fatalf("edited nested group = %+v, want its command kept", got)
	}
}
