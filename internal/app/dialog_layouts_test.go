package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fusefs"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/macro"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestAllDialogs_LayoutValidation(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	testutil.SkipIfNoRelevantChanges(t, "layouts",
		"lang/*.lng",
		"lang/*.txt",
		"file_ops.go",
		"dialog_button_layout.go",
		"*_dialog*.go",
		"*_ui*.go",
		"*_settings*.go",
		"actions*.go",
		"dialog_layouts_test.go",
		"go.mod",
	)
	// vtui.SetDefaultPalette sizes the palette to vtui's own last index; f4
	// extends it, and a dialog that reads one of the extra colours indexes past
	// the end. Grow it back the way the application does.
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()

	// 1. Temporary redirect of the config paths to prevent writing/reading from the user's home directory.
	tmpDir := t.TempDir()

	oldGetConfig := config.GetUserConfigIniPath
	oldConfig := config.App
	defer func() {
		config.GetUserConfigIniPath = oldGetConfig
		config.App = oldConfig
	}()
	config.GetUserConfigIniPath = func() string {
		return filepath.Join(tmpDir, "settings.ini")
	}

	// Actions that are destructive, async, or mutate global state without a dialog.
	skipActions := map[string]bool{
		"app.quit":                         true,
		"panel.systemexplorer":             true,
		"app.togglewindowsize":             true,
		"settings.checkupdates":            true, // network check, no dialog of its own
		"panel.rescan":                     true, // no dialog
		"panel.swap":                       true, // no dialog
		"panel.toggle":                     true, // no dialog
		"panel.toggleleftpanel":            true, // no dialog
		"panel.togglerightpanel":           true, // no dialog
		"panel.togglepassivepanel":         true, // no dialog
		"panel.splitleft":                  true, // no dialog
		"panel.splitright":                 true, // no dialog
		"panel.splitup":                    true, // no dialog
		"panel.splitdown":                  true, // no dialog
		"panel.splitactiveup":              true, // no dialog
		"panel.splitactivedown":            true, // no dialog
		"panel.splitreset":                 true, // no dialog
		"panel.viewbrief":                  true, // no dialog
		"panel.viewmedium":                 true, // no dialog
		"panel.viewdetailed":               true, // no dialog
		"panel.viewwide":                   true, // no dialog
		"panel.sortbyname":                 true, // no dialog
		"panel.sortbyext":                  true, // no dialog
		"panel.sortbytime":                 true,
		"panel.sortbysize":                 true,
		"panel.sortunsorted":               true,
		"panel.togglekeybar":               true,
		"panel.toggleinfobytes":            true,
		"panel.togglehidden":               true,
		"panel.historyback":                true,
		"panel.historyforward":             true,
		"file.view":                        true, // async launch
		"file.edit":                        true, // async launch
		"file.new":                         true, // async launch
		"file.attributes":                  true, // async launch
		"file.findduplicates":              true, // async launch
		"term.viewlog":                     true, // async launch
		"term.editlog":                     true, // async launch
		"editor.switchtoviewer":            true, // async launch
		"viewer.switchtoeditor":            true, // async launch
		"editor.codepagenext":              true, // modifies config
		"viewer.codepagenext":              true, // modifies config
		"editor.save":                      true, // no dialog
		"editor.undo":                      true, // no dialog
		"editor.redo":                      true, // no dialog
		"editor.copy":                      true, // no dialog
		"editor.cut":                       true, // no dialog
		"editor.paste":                     true, // no dialog
		"editor.selectall":                 true, // no dialog
		"editor.deleteline":                true, // no dialog
		"editor.toggleovertype":            true, // no dialog
		"editor.searchnext":                true, // no dialog
		"editor.wordwrap":                  true, // no dialog
		"editor.showwhitespaces":           true, // no dialog
		"editor.insertleftpanelpath":       true, // no dialog
		"editor.insertrightpanelpath":      true, // no dialog
		"editor.insertactivepanelfilename": true, // no dialog
		"editor.deletespacersforward":      true, // no dialog
		"viewer.wrapmode":                  true, // no dialog
		"viewer.hexmode":                   true, // no dialog
		"panel.copypath":                   true, // no dialog
		"panel.copyname":                   true, // no dialog
		"panel.copyselectednames":          true, // no dialog
		"panel.copyselectedpaths":          true, // no dialog
		"panel.copyselectedrealpaths":      true, // no dialog
		"panel.invertselection":            true, // no dialog
		"panel.restoreselection":           true, // no dialog
		"app.screengrab":                   true, // full screen raw frame
		"app.plugring":                     true, // async fetch
		"panel.leftdrivemenu":              true, // relies on active pty/panels
		"panel.rightdrivemenu":             true, // relies on active pty/panels
		"panel.enterdirectory":             true, // no dialog
		"panel.insertfilename":             true, // no dialog
		"panel.insertleftpath":             true, // no dialog
		"panel.insertrightpath":            true, // no dialog
		"debug.dummyoperation":             true, // async queue
		"macro.Reload":                     true, // no dialog; owns an asynchronous toast
		"panel.infopanel":                  true, // no dialog
		"panel.quickview":                  true, // no dialog
	}

	// 3. Create a dummy file in the temp directory so file operations (Copy, Edit, etc.)
	// have a valid target and will naturally display their progress/confirmation dialogs.
	srcFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(srcFile, []byte("dummy content"), 0600); err != nil {
		t.Fatal(err)
	}

	oldHotkeys := keymap.GlobalHotkeysMgr
	oldMacro := macro.MacroMgr
	defer func() {
		keymap.GlobalHotkeysMgr = oldHotkeys
		macro.MacroMgr = oldMacro
	}()
	keymap.GlobalHotkeysMgr = keymap.NewHotkeyManager(filepath.Join(tmpDir, "hotkeys.ini"))
	macro.MacroMgr = macro.NewMacroManager(filepath.Join(tmpDir, "key_macros.ini"))

	// Load all language packs so the validator can assert layout against all translations dynamically
	packs := i18n.LoadAllLanguagePacks()
	if len(packs) == 0 {
		packs = []vtui.LanguagePack{{Name: "current"}}
	}

	oldMountTaskRunner := panel.RunPanelMountTask
	panel.RunPanelMountTask = func(pf *panel.PanelsFrame, label string, readOnly bool, _ func(context.Context) (*fusefs.Mount, error)) {
		panel.ReportMount(pf, label, &fusefs.Mount{
			MountPoint: filepath.Join(tmpDir, "layout-validation-mount"),
			ReadOnly:   readOnly,
		}, nil, readOnly)
	}
	t.Cleanup(func() { panel.RunPanelMountTask = oldMountTaskRunner })

	rig := newDialogLayoutRig(t, tmpDir)
	defer rig.close(t)

	// Complex-script widths are handled by vtui's grapheme-cell shaping.
	for _, act := range action.AllSorted() {
		name := act.Name
		// Settings uses an intentionally clipped scrolling viewport. Its layout
		// and all category deep links are covered by the dedicated Center tests.
		if _, linked := settingsDeepLinks[strings.ToLower(name)]; linked || name == "Settings.Open" || strings.HasPrefix(name, "Settings.Category.") {
			continue
		}
		if skipActions[strings.ToLower(name)] {
			continue
		}

		t.Run(name, func(t *testing.T) {
			rules := vtui.DefaultLayoutRules
			rules.MaxWidth = 120 // Allow configurator/large dialogs to exceed default 78 columns
			baseStrings := vtui.SnapshotStrings()
			defer vtui.ReplaceStrings(baseStrings)

			var msgs []string
			for _, pack := range packs {
				vtui.ReplaceStrings(baseStrings)
				if len(pack.Strings) > 0 {
					vtui.AddStrings(pack.Strings)
				}

				var errs []error
				if dialogLayoutActionNeedsFreshRig(name) {
					errs = func() []error {
						rig.detach(t)
						defer rig.attach()

						fresh := newDialogLayoutRig(t, tmpDir)
						defer fresh.close(t)
						return fresh.validateAction(t, act, name, srcFile, rules)
					}()
				} else {
					errs = func() []error {
						defer rig.reset(t)
						return rig.validateAction(t, act, name, srcFile, rules)
					}()
				}

				packName := pack.Name
				if packName == "" {
					packName = "?"
				}
				for _, err := range errs {
					msgs = append(msgs, fmt.Sprintf("[lang:%s] %s", packName, err))
				}
			}

			if len(msgs) > 0 {
				t.Errorf("Layout validation failed for Action %s:\n%s", name, strings.Join(msgs, "\n"))
			}
		})
	}
}

type dialogLayoutRig struct {
	manager    *vtui.FrameManagerType
	screen     *vtui.ScreenBuf
	baseScreen *vtui.AppScreen
	panels     *panel.PanelsFrame
	localVFS   vfs.VFS
}

func newDialogLayoutRig(t *testing.T, dir string) *dialogLayoutRig {
	t.Helper()
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(120, 60)
	manager := vtui.FrameManager
	manager.Init(screen)

	localVFS := vfs.NewOSVFS(dir)
	if err := localVFS.SetPath(dir); err != nil {
		t.Fatal(err)
	}
	panels := panel.NewPanelsFrame()
	left := panel.NewFileSystemPanel(0, 0, 40, 20, localVFS)
	right := panel.NewFileSystemPanel(40, 0, 40, 20, localVFS.Clone())
	paneltest.WaitForLoad(t, left)
	paneltest.WaitForLoad(t, right)
	panels.Panels[0] = left
	panels.Panels[1] = right
	panels.ResizeConsole(120, 60)
	paneltest.WaitForLoad(t, panels.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, panels.Panels[1].(*panel.FileSystemPanel))
	manager.Push(panels)

	return &dialogLayoutRig{
		manager:    manager,
		screen:     screen,
		baseScreen: manager.Screens[manager.ActiveIdx],
		panels:     panels,
		localVFS:   localVFS,
	}
}

func (rig *dialogLayoutRig) validateAction(t *testing.T, act action.Action, name, srcFile string, rules vtui.LayoutRules) []error {
	t.Helper()
	if strings.HasPrefix(name, "Editor.") {
		showEditor(rig.panels, rig.localVFS, srcFile, &vfs.MemoryReadAtCloser{Data: []byte("dummy")})
	} else if strings.HasPrefix(name, "Viewer.") {
		vv, err := viewer.NewViewerView(context.Background(), rig.localVFS, srcFile)
		if err == nil {
			showViewer(rig.panels, vv, srcFile)
		}
	}

	initialCount := len(rig.manager.Screens[rig.manager.ActiveIdx].Frames)
	act.Handler()
	paneltest.WaitForLoad(t, rig.panels.Panels[0].(*panel.FileSystemPanel))
	paneltest.WaitForLoad(t, rig.panels.Panels[1].(*panel.FileSystemPanel))
	if rig.manager.GetActiveToast() != "" {
		testutil.WaitForToastExpiry(t, 6*time.Second)
	}

	frames := rig.manager.Screens[rig.manager.ActiveIdx].Frames
	if len(frames) <= initialCount {
		// Many actions are silent and do not open a dialog (e.g. Editor.Save).
		// This is completely expected, so skip validation for this pass.
		return nil
	}

	// Check if the top-most frame is a container. If not (e.g. raw drawing view), skip it safely.
	topFrame := frames[len(frames)-1]
	container, ok := topFrame.(vtui.Container)
	if !ok {
		return nil
	}
	return vtui.ValidateLayoutWithRules(container, rules)
}

func (rig *dialogLayoutRig) reset(t *testing.T) {
	t.Helper()
	paneltest.WaitForDirectoryLoads(t)

	baseIdx := -1
	for i, screen := range rig.manager.Screens {
		if screen == rig.baseScreen {
			baseIdx = i
		}
	}
	if baseIdx < 0 {
		t.Fatal("layout validation action removed the shared panels workspace")
	}

	// Close non-base workspaces directly. CloseScreen intentionally asks for
	// confirmation when an editor has unsaved changes, but these fixtures are
	// discarded between checks and must not open a confirmation dialog while
	// the test is trying to restore its panels workspace.
	screens := append([]*vtui.AppScreen(nil), rig.manager.Screens...)
	for _, screen := range screens {
		if screen == rig.baseScreen {
			continue
		}
		for i, candidate := range rig.manager.Screens {
			if candidate != screen {
				continue
			}
			rig.manager.SwitchScreen(i)
			testutil.CloseFrameManagerScreens([]*vtui.AppScreen{screen})
			break
		}
		for i, candidate := range rig.manager.Screens {
			if candidate == rig.baseScreen {
				rig.manager.SwitchScreen(i)
				break
			}
		}
	}
	for i, screen := range rig.manager.Screens {
		if screen == rig.baseScreen {
			baseIdx = i
			break
		}
	}
	rig.manager.Screens = []*vtui.AppScreen{rig.baseScreen}
	rig.manager.ActiveIdx = 0
	rig.manager.Screens[0].Frames = rig.manager.GetActiveFrames(0)

	if baseIdx < 0 {
		t.Fatal("layout validation action removed the shared panels workspace")
	}
	frames := append([]vtui.Frame(nil), rig.baseScreen.Frames...)
	foundPanels := false
	for i := len(frames) - 1; i >= 0; i-- {
		frame := frames[i]
		if frame == rig.panels {
			foundPanels = true
			continue
		}
		frame.Close()
		rig.manager.RemoveFrame(frame)
	}
	if !foundPanels || rig.panels.IsDone() {
		t.Fatal("layout validation action mutated the shared panels frame")
	}
}

func (rig *dialogLayoutRig) detach(t *testing.T) {
	t.Helper()
	// The next rig reinitializes the global manager. Clipboard workers read
	// that global asynchronously, so join them at this lifecycle boundary.
	terminal.WaitForAsyncClipboard()
	rig.reset(t)
}

func (rig *dialogLayoutRig) attach() {
	rig.manager.Init(rig.screen)
	rig.manager.Push(rig.panels)
	rig.baseScreen = rig.manager.Screens[rig.manager.ActiveIdx]
}

func (rig *dialogLayoutRig) close(t *testing.T) {
	t.Helper()
	terminal.WaitForAsyncClipboard()
	paneltest.WaitForDirectoryLoads(t)
	testutil.CloseFrameManagerFrames(rig.manager)
}

// These handlers change the reusable panel.PanelsFrame itself (or stop/close its
// manager). Keeping their old per-combination freshness avoids making a later
// action or translation depend on that mutation; ordinary actions share the
// expensive VFS and panels fixture above.
func dialogLayoutActionNeedsFreshRig(name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "panel.left.") || strings.HasPrefix(name, "panel.right.") {
		return true
	}
	switch name {
	case "ai.togglepanel",
		"app.background",
		"panel.goparent",
		"panel.goroot",
		"panel.insertpath",
		"panel.selectnavigation",
		"panel.togglecommandlinefocus",
		"workspace.close":
		return true
	default:
		return false
	}
}
