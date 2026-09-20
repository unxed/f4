package app

import "github.com/unxed/f4/internal/action"

import "testing"

// TestMenuHonoursVisible checks the three things the hook has to get right:
// a hidden action leaves no item, a visible one still appears, and a menu
// whose every action is hidden does not show up as an empty group.
func TestMenuHonoursVisible(t *testing.T) {
	preserveActionRegistry(t)
	show := true
	action.RegisterAction(action.Action{
		Name:     "Test.Visibility.Shown",
		Area:     "Shell",
		Label:    "Sometimes Here",
		MenuPath: "Commands",
		Visible:  func() bool { return show },
		Handler:  func() bool { return true },
	})
	action.RegisterAction(action.Action{
		Name:     "Test.Visibility.Group",
		Area:     "Shell",
		Label:    "Only Member",
		MenuPath: "TestOnlyGroup",
		Visible:  func() bool { return show },
		Handler:  func() bool { return true },
	})

	// The Commands menu carries a localized label, so the items are counted
	// across every menu instead; the test group has no translation and keeps
	// its own name.
	count := func(item string) (groups, items int) {
		for _, m := range BuildMenuBarItems("Shell") {
			if m.Label == "TestOnlyGroup" {
				groups++
			}
			for _, it := range m.SubItems {
				if plainMenuText(it.Text) == item {
					items++
				}
			}
		}
		return
	}

	if _, items := count("Sometimes Here"); items != 1 {
		t.Errorf("a visible action produced %d items, want 1", items)
	}
	if groups, _ := count("Only Member"); groups == 0 {
		t.Error("the group of a visible action is missing")
	}

	show = false
	if _, items := count("Sometimes Here"); items != 0 {
		t.Errorf("a hidden action still produced %d items", items)
	}
	if groups, _ := count("Only Member"); groups != 0 {
		t.Errorf("a group whose only action is hidden appeared %d times", groups)
	}

	// An action without the hook is unaffected, which is every other one.
	found := false
	for _, m := range BuildMenuBarItems("Shell") {
		if len(m.SubItems) > 0 {
			found = true
		}
	}
	if !found {
		t.Error("the menu came back empty")
	}
}
