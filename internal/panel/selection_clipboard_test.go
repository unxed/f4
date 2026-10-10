package panel

import (
	"reflect"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestSelectFromClipboard(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"lines", "one.txt\r\ntwo.txt", []string{"one.txt", "two.txt"}},
		{"spaces and tabs", "one.txt  two.txt\tfolder", []string{"one.txt", "two.txt", "folder"}},
		{"quotes", `"one.txt" 'two.txt' "long file.txt"`, []string{"one.txt", "two.txt", "long file.txt"}},
		{"unquoted spaces", "long file.txt two.txt", []string{"two.txt", "long file.txt"}},
		{"longest name", "long file.txt", []string{"long file.txt"}},
		{"quoted unknown", `"missing one.txt"`, nil},
		{"unfinished quote", `"one.txt`, nil},
		{"exact boundaries", "notone.txt one.txt.bak", nil},
		{"literal masks", "*.txt", nil},
		{"unicode and case", "ONE.TXT 'файл Я.txt'", []string{"one.txt", "файл Я.txt"}},
		{"paths", "C:\\source\\one.txt\n'/source/long file.txt'", []string{"one.txt", "long file.txt"}},
		{"duplicates and parent", ".. one.txt one.txt", []string{"one.txt"}},
		{"apostrophe", "it's.txt", []string{"it's.txt"}},
		{"multiple spaces in name", "'two  spaces.txt'", []string{"two  spaces.txt"}},
		{"unquoted multiple spaces in name", "two  spaces.txt one.txt", []string{"one.txt", "two  spaces.txt"}},
		{"empty keeps selection", " \t\r\n", []string{"old.log"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := &FileSystemPanel{Vfs: vfs.NewOSVFS(t.TempDir()), CursorIdx: 2}
			for _, name := range []string{"..", "one.txt", "two.txt", "long", "long file.txt", "folder", "файл Я.txt", "it's.txt", "two  spaces.txt", "old.log"} {
				fp.Entries = append(fp.Entries, &FileEntry{VFSItem: vfs.VFSItem{Name: name, IsDir: name == ".." || name == "folder"}})
			}
			fp.SetItemSelected(len(fp.Entries)-1, true)
			fp.SelectFromClipboard(tc.text)
			var got []string
			for _, entry := range fp.Entries {
				if entry.Selected {
					got = append(got, entry.Name)
				}
				if entry.Selected != fp.SelectedItems[entry.Name] {
					t.Fatalf("selection map disagrees for %q", entry.Name)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected=%v, want %v", got, tc.want)
			}
			if fp.CursorIdx != 2 {
				t.Fatal("selection moved the cursor")
			}
			if tc.name != "empty keeps selection" {
				fp.RestoreSelection()
				if !fp.Entries[len(fp.Entries)-1].Selected || len(fp.SelectedItems) != 1 {
					t.Fatal("Ctrl+M did not restore the previous marks")
				}
			}
		})
	}
}
