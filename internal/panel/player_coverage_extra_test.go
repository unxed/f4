package panel

import (
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestPlayerPlaylistKeysAndBounds(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pp := newTestPlayerPanel()
	t.Cleanup(pp.Close)
	folder := &playlistItem{Name: "Folder", Folder: true, Expanded: true}
	first := &playlistItem{Name: "first", Path: "/first.mp3"}
	second := &playlistItem{Name: "second", Path: "/second.mp3"}
	folder.insertChild(-1, first)
	pp.root.insertChild(-1, folder)
	pp.root.insertChild(-1, second)
	pp.rebuildRows()
	if len(pp.rows) != 3 || pp.GetSelectedName() != "" {
		t.Fatalf("initial rows = %#v, selected=%q", pp.rows, pp.GetSelectedName())
	}

	pp.cursor = 1
	if pp.GetSelectedName() != "first" {
		t.Fatalf("GetSelectedName() = %q", pp.GetSelectedName())
	}
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_UP}, false)
	if pp.cursor != 0 {
		t.Fatalf("Up cursor = %d, want 0", pp.cursor)
	}
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_LEFT}, false)
	if folder.Expanded {
		t.Fatal("Left did not collapse the folder")
	}
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_RIGHT}, false)
	if !folder.Expanded {
		t.Fatal("Right did not expand the folder")
	}
	pp.rebuildRows()
	pp.cursor = 1
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_DELETE}, false)
	if len(folder.Children) != 0 || len(pp.root.Children) != 2 {
		t.Fatalf("Delete changed wrong playlist level: folder=%d root=%d", len(folder.Children), len(pp.root.Children))
	}

	// Moving beyond either sibling edge is a no-op, while moving into and out
	// of a folder preserves the selected item.
	pp.rebuildRows()
	pp.cursor = 0
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_UP, ControlKeyState: vtinput.LeftCtrlPressed}, true)
	if pp.root.Children[0] != folder {
		t.Fatal("Ctrl+Up moved the first item past the edge")
	}
	pp.playlistKey(&vtinput.InputEvent{VirtualKeyCode: vtinput.VK_DOWN, ControlKeyState: vtinput.LeftCtrlPressed}, true)
	if pp.root.Children[0] != second || second.parent != pp.root {
		t.Fatalf("Ctrl+Down order = %#v", pp.root.Children)
	}
}

func TestPlayerProcessKeyAndNavigationContracts(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pp := newTestPlayerPanel()
	t.Cleanup(pp.Close)

	if pp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN}) {
		t.Fatal("unfocused player handled a key")
	}
	pp.focused = true
	if pp.ProcessKey(&vtinput.InputEvent{KeyDown: true, ControlKeyState: vtinput.LeftAltPressed, Char: 'x'}) {
		t.Fatal("Alt key leaked into player controls")
	}
	if !pp.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F3}) {
		t.Fatal("file-panel-only key was not swallowed")
	}
	if pp.ProcessKey(&vtinput.InputEvent{KeyDown: false, VirtualKeyCode: vtinput.VK_F3}) {
		t.Fatal("key-up event was handled")
	}
	if !pp.ProcessKey(&vtinput.InputEvent{KeyDown: true, Char: '+'}) {
		t.Fatal("volume character was not handled")
	}

	pp.root.insertChild(-1, &playlistItem{Name: "track", Path: "/does-not-exist.mp3"})
	pp.rebuildRows()
	pp.cursor = 0
	for _, event := range []*vtinput.InputEvent{
		{KeyDown: true, VirtualKeyCode: vtinput.VK_HOME},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_END},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR},
		{KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT},
	} {
		if !pp.ProcessKey(event) {
			t.Fatalf("navigation event %#v was not handled", event)
		}
	}
	if pp.playItem(nil) || pp.playItem(&playlistItem{Folder: true}) {
		t.Fatal("invalid playlist item started playback")
	}
	if pp.PlayFile(nil, 0) || pp.PlayFile([]string{"/missing"}, -1) {
		t.Fatal("invalid file queue position started playback")
	}
}

func TestPlayerFormattingAndSelectionHelpers(t *testing.T) {
	for _, tc := range []struct {
		text   string
		width  int
		offset int
		want   string
	}{
		{text: "abc", width: 2, offset: 0, want: "ab"},
		{text: "abc", width: 2, offset: 2, want: "ca"},
		{text: "你好", width: 3, offset: 0, want: "你好"},
		{text: "", width: 3, offset: 0, want: ""},
	} {
		if got := marqueeSlice(tc.text, tc.width, tc.offset); got != tc.want {
			t.Errorf("marqueeSlice(%q, %d, %d) = %q, want %q", tc.text, tc.width, tc.offset, got, tc.want)
		}
	}
	pp := newTestPlayerPanel()
	t.Cleanup(pp.Close)
	pp.rebuildRows()
	if pp.cursor != -1 || pp.GetSelectedName() != "" {
		t.Fatalf("empty selection = cursor %d name %q", pp.cursor, pp.GetSelectedName())
	}
	pp.root.insertChild(-1, &playlistItem{Name: "one"})
	pp.root.insertChild(-1, &playlistItem{Name: "two"})
	pp.cursor = 99
	pp.rebuildRows()
	if pp.cursor != 1 || pp.GetSelectedName() != "two" {
		t.Fatalf("cursor clamp = %d name %q", pp.cursor, pp.GetSelectedName())
	}
}
