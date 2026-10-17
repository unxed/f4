package panel

import (
	"os"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestSubstMenuLabel(t *testing.T) {
	tmp := t.TempDir()
	ctx := &SubstContext{
		Active:            PanelSnapshot{CurDir: "/home/u/work", CurrentFile: "notes.txt"},
		Passive:           PanelSnapshot{CurDir: "/mnt/backup"},
		MarkedListTempDir: tmp,
		AskUser: func(string, string) (string, bool) {
			t.Fatal("a label asked for input")
			return "", false
		},
	}
	for _, tc := range []struct{ label, want string }{
		{`Explorer в папке !\`, "Explorer в папке /home/u/work"},
		{"Copy !.! to !#!/", "Copy notes.txt to /mnt/backup"},
		{"Ask !?Name?default!", "Ask default"},
		{"List !@!", "List !@!"},
		{"Plain label", "Plain label"},
	} {
		if got := SubstMenuLabel(tc.label, ctx); got != tc.want {
			t.Errorf("SubstMenuLabel(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("expanding labels wrote %d file(s)", len(entries))
	}
}

// The menu shows the label expanded for the panel, as Far does, while the
// item keeps the label as written for editing and saving.
func TestUserMenuShowsLabelsExpanded(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	dir := t.TempDir()
	pf := &PanelsFrame{LastW: 120, LastH: 25, Panels: [2]Panel{&FileSystemPanel{Vfs: vfs.NewOSVFS(dir)}, nil}}
	label := `Explorer в папке !\`
	s := &userMenuState{Pf: pf, rootTitle: "Main", rootItems: []UserMenuItem{{HotKey: "E", Label: label, Commands: []string{`Explorer "!\"`}}}}
	s.openCurrent(0)

	frame, ok := vtui.FrameManager.GetTopFrame().(*UserMenuFrame)
	if !ok {
		t.Fatal("the user menu was not opened")
	}
	if text := frame.Items[0].Text; !strings.Contains(text, "Explorer в папке "+dir) || strings.Contains(text, `!\`) {
		t.Fatalf("menu item text %q, want the label with the folder %q", text, dir)
	}
	if got := s.rootItems[0].Label; got != label {
		t.Fatalf("the item's label became %q, want it kept as %q", got, label)
	}
}
