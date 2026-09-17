package panel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestUserMenuFrameAndStateRenderingContracts(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	pf := &PanelsFrame{LastW: 80, LastH: 25}
	state := &userMenuState{
		Pf:        pf,
		rootTitle: "Root",
		rootItems: []UserMenuItem{{Label: "&Tools", Submenu: []UserMenuItem{{Label: "&Build"}}}},
		path:      []int{0},
	}
	if got := state.currentTitle(); got != "Root -> Tools" {
		t.Fatalf("currentTitle() = %q", got)
	}
	if got := state.currentItems(); len(got) != 1 || got[0].Label != "&Build" {
		t.Fatalf("currentItems() = %#v", got)
	}
	state.path = []int{9}
	if got := state.currentItems(); got != nil {
		t.Fatalf("invalid currentItems() = %#v, want nil", got)
	}
	if got := state.currentTitle(); got != "Root" {
		t.Fatalf("invalid currentTitle() = %q", got)
	}

	state.path = nil
	state.replaceCurrentItems([]UserMenuItem{{Label: "new root"}})
	if got := state.rootItems[0].Label; got != "new root" {
		t.Fatalf("replaceCurrentItems() = %q", got)
	}

	scr := vtui.NewSilentScreenBuf()
	frame := &UserMenuFrame{VMenu: vtui.NewVMenu("title"), bottomHint: " hint "}
	frame.SetPosition(2, 2, 30, 8)
	frame.Show(scr)
	if frame.bottomHint == "" {
		t.Fatal("bottom hint was lost")
	}
}

func TestUserMenuModesAndPersistence(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	dir := t.TempDir()
	fsp := &FileSystemPanel{Vfs: vfs.NewOSVFS(dir)}
	pf := &PanelsFrame{ActiveIdx: 0, Panels: [2]Panel{fsp, nil}, LastW: 80, LastH: 25}

	if got := defaultSavePath(pf, MenuModeLocal); got != filepath.Join(dir, FarMenuFileName) {
		t.Fatalf("local defaultSavePath() = %q", got)
	}
	if got := defaultSavePath(pf, MenuModeMain); !strings.HasSuffix(got, filepath.Join("f4", "settings", "user_menu.ini")) {
		t.Fatalf("main defaultSavePath() = %q", got)
	}
	if got := defaultSavePath(pf, MenuMode(99)); got != "" {
		t.Fatalf("unknown defaultSavePath() = %q", got)
	}

	items := []UserMenuItem{{HotKey: "x", Label: "Run", Commands: []string{"echo x"}}}
	localPath := filepath.Join(dir, FarMenuFileName)
	if err := SaveRootForMode(MenuModeLocal, localPath, items); err != nil {
		t.Fatal(err)
	}
	loaded := loadRootForMode(MenuModeLocal, localPath)
	if len(loaded) != 1 || loaded[0].Label != "Run" {
		t.Fatalf("loadRootForMode(local) = %#v", loaded)
	}
	if got := loadRootForMode(MenuModeLocal, filepath.Join(dir, "missing")); got != nil {
		t.Fatalf("missing loadRootForMode() = %#v", got)
	}

	got, title, source, ok := LoadMenuForMode(pf, MenuModeLocal)
	if !ok || len(got) != 1 || title == "" || source != localPath {
		t.Fatalf("LoadMenuForMode(local) = %#v, %q, %q, %v", got, title, source, ok)
	}
	missingPF := &PanelsFrame{ActiveIdx: 0}
	if _, _, _, ok := LoadMenuForMode(missingPF, MenuModeLocal); ok {
		t.Fatal("LoadMenuForMode(nil panel) unexpectedly succeeded")
	}

	state := &userMenuState{mode: MenuModeLocal, SourcePath: localPath, rootItems: items}
	if !state.saveRoot() {
		t.Fatal("saveRoot() failed")
	}
	if (&userMenuState{}).saveRoot() {
		t.Fatal("empty saveRoot() succeeded")
	}

	resolved, _, mode, resolvedPath := resolveMenuStart(pf, MenuModeLocal)
	if mode != MenuModeLocal || resolvedPath != localPath || len(resolved) != 1 {
		t.Fatalf("resolveMenuStart() = %#v, mode %d, path %q", resolved, mode, resolvedPath)
	}
}

func TestUserMenuPushLevelAndKeyboardNavigation(t *testing.T) {
	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	state := &userMenuState{
		Pf:        &PanelsFrame{LastW: 80, LastH: 25},
		rootTitle: "Root",
		rootItems: []UserMenuItem{
			{HotKey: "F3", Label: "Build", Commands: []string{"echo build"}},
			{HotKey: "s", Label: "&Sub", Submenu: []UserMenuItem{{HotKey: "F4", Label: "Child"}}},
			{HotKey: "--"},
		},
	}
	state.openCurrent(0)
	top, ok := vtui.FrameManager.GetTopFrame().(*UserMenuFrame)
	if !ok || len(top.Items) != 3 {
		t.Fatalf("top menu = %T, items=%d", vtui.FrameManager.GetTopFrame(), func() int {
			if top == nil {
				return 0
			}
			return len(top.Items)
		}())
	}
	if top.Items[0].Text != "F3    Build" || top.Items[1].Shortcut != submenuMarker || !top.Items[2].Separator {
		t.Fatalf("menu items = %#v", top.Items)
	}
	if itemIndexAtUI(top.VMenu, -1) != -1 || itemIndexAtUI(top.VMenu, 99) != -1 {
		t.Fatal("itemIndexAtUI accepted an invalid position")
	}

	top.SetSelectPos(1)
	// UserMenuFrame embeds VMenu; invoke the wrapper's handler directly so
	// this assertion exercises the user-menu contract rather than vtui's
	// frame-identity routing for embedded menus.
	if !top.OnKeyDown(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT}) {
		t.Fatal("Right did not enter submenu")
	}
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			goto drained
		}
	}
drained:
	child, ok := vtui.FrameManager.GetTopFrame().(*UserMenuFrame)
	if !ok || child.GetTitle() != " Root -> Sub " {
		t.Fatalf("child menu = %T title %q", vtui.FrameManager.GetTopFrame(), func() string {
			if child == nil {
				return ""
			}
			return child.GetTitle()
		}())
	}
	if !child.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE}) {
		t.Fatal("Escape did not return from submenu")
	}
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			return
		}
	}
}
