package panel

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// The production Settings hook must not intercept an in-place editing workflow.
func TestContextEditorsRemainLocalWithSettingsAvailable(t *testing.T) {
	old, oldMenu := OpenSettingsAt, OpenUserMenuSettings
	OpenSettingsAt = func(string, string, string, bool) bool { t.Error("context editor opened Settings"); return true }
	OpenUserMenuSettings = func(MenuSettingsSource, *vtui.VMenu, int, bool, bool) bool {
		t.Error("user-menu editor opened Settings")
		return true
	}
	t.Cleanup(func() { OpenSettingsAt, OpenUserMenuSettings = old, oldMenu })
	for _, kind := range []string{"drive-new", "drive-edit", "bookmark", "user-menu", "submenu"} {
		t.Run(kind, func(t *testing.T) {
			t.Cleanup(testutil.SwapFrameManager(t))
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(100, 40)
			vtui.FrameManager.Init(scr)
			switch kind {
			case "drive-new", "drive-edit":
				idx := -1
				if kind == "drive-edit" {
					idx = 0
				}
				pf := &PanelsFrame{}
				pf.openDriveBookmarkEditor(-1, nil, []DriveBookmark{{Name: "Example", Path: "/example", Hotkey: "Q"}}, idx, func() {})
				select {
				case task := <-vtui.FrameManager.TaskChan:
					task()
				case <-time.After(time.Second):
					t.Fatal("drive editor was not scheduled")
				}
				d, ok := vtui.FrameManager.GetTopFrame().(*driveBookmarkEditDialog)
				if !ok {
					t.Fatalf("expected drive link dialog, got %T", vtui.FrameManager.GetTopFrame())
				}
				if idx == 0 && (d.nameEdit.GetText() != "Example" || d.pathEdit.GetText() != "/example" || d.HotkeyEdit.GetText() != "Q") {
					t.Fatal("existing link fields lost")
				}
			case "bookmark":
				d := &BookmarksDialog{Set: BookmarkSet{1: {Path: "/example"}}}
				d.editPath(1)
			default:
				showEditItemDialog(&userMenuState{}, nil, nil, 0, true, kind == "submenu")
			}
			if _, ok := vtui.FrameManager.GetTopFrame().(vtui.Container); !ok {
				t.Fatalf("expected local editor, got %T", vtui.FrameManager.GetTopFrame())
			}
		})
	}
}

func TestDriveLinkHotkeyRemainsVisible(t *testing.T) {
	palette := append([]uint64(nil), vtui.Palette...)
	defer copy(vtui.Palette, palette)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	for _, initial := range []DriveBookmark{{}, {Name: "Link", Path: "/folder", Hotkey: "Ф"}} {
		d := NewDriveBookmarkEditDialog(initial, "/folder", nil)
		for iteration := 0; iteration < 2; iteration++ {
			vtui.Palette[vtui.ColDialogEdit] = vtui.SetRGBBoth(0, testutil.Uint32(0xc0d0e0+iteration), 0x202020)
			vtui.Palette[vtui.ColDialogEditUnchanged] = vtui.Palette[vtui.ColDialogEdit]
			vtui.Palette[vtui.ColDialogBox] = vtui.SetRGBBoth(0, testutil.Uint32(0x8090a0+iteration), 0x303030)
			d.SetFocusedItem(d.HotkeyEdit)
			for _, char := range []rune{'ф', 'a'} {
				d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char})
				for _, focus := range []vtui.UIElement{d.HotkeyEdit, d.nameEdit} {
					d.SetFocusedItem(focus)
					d.Show(scr)
					x, y, x2, _ := d.HotkeyEdit.GetPosition()
					cell := scr.GetCell(x, y)
					if x2 != x || testutil.Rune(cell.Char) != []rune(d.HotkeyEdit.GetText())[0] || cell.Attributes != vtui.Palette[vtui.ColDialogEdit] {
						t.Fatalf("hotkey invisible or wrong palette: %+v", cell)
					}
				}
				d.SetFocusedItem(d.HotkeyEdit)
			}
			d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
			if d.HotkeyEdit.GetText() != "A" {
				t.Fatal("Backspace changed hotkey")
			}
			d.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})
			if d.HotkeyEdit.GetText() != "" {
				t.Fatal("Del did not clear hotkey")
			}
		}
	}
}
