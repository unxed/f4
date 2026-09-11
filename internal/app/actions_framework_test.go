package app

import (
	"bytes"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

type frameworkActionTestFrame struct {
	vtui.BaseFrame
	title      string
	help       string
	menu       *vtui.MenuBar
	menuInfo   vtui.WorkspaceMenuInfo
	focused    vtui.UIElement
	handleF1   bool
	f1Calls    int
	forkCalled bool
}

func (*frameworkActionTestFrame) GetType() vtui.FrameType { return vtui.TypeUser + 91 }
func (frame *frameworkActionTestFrame) GetTitle() string  { return frame.title }
func (frame *frameworkActionTestFrame) GetHelp() string   { return frame.help }
func (frame *frameworkActionTestFrame) GetMenuBar() *vtui.MenuBar {
	return frame.menu
}
func (frame *frameworkActionTestFrame) GetWorkspaceTabTitle() string {
	return frame.title
}
func (frame *frameworkActionTestFrame) GetWorkspaceMenuInfo() vtui.WorkspaceMenuInfo {
	return frame.menuInfo
}
func (frame *frameworkActionTestFrame) GetFocusedItem() vtui.UIElement { return frame.focused }
func (frame *frameworkActionTestFrame) ProcessKey(event *vtinput.InputEvent) bool {
	if event != nil && event.Type == vtinput.KeyEventType && event.KeyDown && event.VirtualKeyCode == vtinput.VK_F1 {
		frame.f1Calls++
		return frame.handleF1
	}
	return false
}
func (frame *frameworkActionTestFrame) HandleCommand(command int, args any) bool {
	if command == vtui.CmResize && args == "fork" {
		frame.forkCalled = true
		return true
	}
	return false
}

func initFrameworkActionTestScreen(t *testing.T) *vtui.ScreenBuf {
	t.Helper()
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(100, 30)
	vtui.FrameManager.Init(screen)
	return screen
}

func TestFrameworkActionsKeepNativeShortcutsOutOfHotkeyDefaults(t *testing.T) {
	want := map[string]string{
		"App.Help":           "F1",
		"App.MainMenu":       "F9",
		"Workspace.New":      "CtrlN",
		"Workspace.Close":    "CtrlW",
		"Workspace.Next":     "CtrlTab",
		"Workspace.Previous": "CtrlShiftTab",
		"Workspace.List":     "F12",
	}
	manager := keymap.NewHotkeyManager(filepath.Join(t.TempDir(), "hotkeys.ini"))
	for name, nativeKey := range want {
		action, ok := GetAction(name)
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		if len(action.DefaultKeys) != 0 {
			t.Errorf("%s DefaultKeys = %v; framework fallback must keep physical-key priority", name, action.DefaultKeys)
		}
		if len(action.NativeKeys) != 1 {
			t.Errorf("%s NativeKeys = %v, want one %s shortcut", name, action.NativeKeys, nativeKey)
		} else if key, _, _ := strings.Cut(action.NativeKeys[0], ":"); key != nativeKey {
			t.Errorf("%s NativeKeys = %v, want %s", name, action.NativeKeys, nativeKey)
		}
		if got := manager.GetAction("Shell", nativeKey); got != "" {
			t.Errorf("%s was installed into HotkeyManager as %q", nativeKey, got)
		}
	}

	// No hotkey at all: the dump is a palette command. CtrlAltP was tried
	// here for Wine's tty and did not work there either (WINE.md §15.2),
	// while holding it kept App.CommandPalette's own legacy fallback on the
	// same chord from ever firing (issue #980).
	dump, ok := GetAction("Debug.ScreenDump")
	if !ok || len(dump.DefaultKeys) != 0 || len(dump.NativeKeys) != 0 {
		t.Fatalf("screen dump registration = %+v; want no hotkey of its own", dump)
	}
}

func TestNativeFrameworkShortcutMetadataHonorsExplicitOverrides(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	previous := keymap.GlobalHotkeysMgr
	manager := keymap.NewHotkeyManager(filepath.Join(t.TempDir(), "hotkeys.ini"))
	keymap.GlobalHotkeysMgr = manager
	t.Cleanup(func() { keymap.GlobalHotkeysMgr = previous })

	help, ok := GetAction("App.Help")
	if !ok {
		t.Fatal("App.Help is not registered")
	}
	if got := keymap.NativeShortcutsForAction("Shell", help); !reflect.DeepEqual(got, []string{"F1"}) {
		t.Fatalf("native help shortcuts = %v, want [F1]", got)
	}

	manager.Bind("Shell", "F1", "None")
	if got := keymap.NativeShortcutsForAction("Shell", help); len(got) != 0 {
		t.Fatalf("explicit F1=None still advertised native help shortcut: %v", got)
	}

	manager.Bind("Shell", "F1", "Editor.Save")
	if got := keymap.NativeShortcutsForAction("Shell", help); len(got) != 0 {
		t.Fatalf("explicit F1 override still advertised native help shortcut: %v", got)
	}

	manager.Unbind("Shell", "F1")
	topic := generateKeysHelpTopic("FrameworkKeys", "Framework keys", []string{"Common"}, "")
	if !strings.Contains(strings.Join(topic.Lines, "\n"), "F1") {
		t.Fatal("generated help omitted the framework-owned F1 shortcut")
	}
}

func TestNativeFrameworkShortcutMetadataRespectsTerminalOwnership(t *testing.T) {
	initFrameworkActionTestScreen(t)
	previousHotkeys := keymap.GlobalHotkeysMgr
	keymap.GlobalHotkeysMgr = nil
	previousCtrlN := config.App.TerminalCtrlNWorkspace
	config.App.TerminalCtrlNWorkspace = true
	t.Cleanup(func() {
		keymap.GlobalHotkeysMgr = previousHotkeys
		config.App.TerminalCtrlNWorkspace = previousCtrlN
	})

	// A hidden AltScreen terminal consumes ordinary framework fallbacks before
	// FrameManager sees them, but explicitly releases Ctrl+Tab and preferred
	// Ctrl+N workspace handling.
	panels := &panel.PanelsFrame{
		ShowPanels: false,
		TermView:   &terminal.TerminalView{UseAltScreen: true},
	}
	vtui.FrameManager.Push(panels)

	closeAction, _ := GetAction("Workspace.Close")
	if got := keymap.NativeShortcutsForAction("Shell", closeAction); len(got) != 0 {
		t.Fatalf("busy terminal advertised Ctrl+W workspace close: %v", got)
	}
	helpAction, _ := GetAction("App.Help")
	if got := keymap.NativeShortcutsForAction("Shell", helpAction); len(got) != 0 {
		t.Fatalf("busy terminal advertised consumed F1 help: %v", got)
	}
	nextAction, _ := GetAction("Workspace.Next")
	if got := keymap.NativeShortcutsForAction("Shell", nextAction); !reflect.DeepEqual(got, []string{"Ctrl+Tab"}) {
		t.Fatalf("busy terminal next-workspace shortcut = %v, want [Ctrl+Tab]", got)
	}
	newAction, _ := GetAction("Workspace.New")
	if got := keymap.NativeShortcutsForAction("Shell", newAction); !reflect.DeepEqual(got, []string{"Ctrl+N"}) {
		t.Fatalf("preferred terminal new-workspace shortcut = %v, want [Ctrl+N]", got)
	}
	config.App.TerminalCtrlNWorkspace = false
	if got := keymap.NativeShortcutsForAction("Shell", newAction); len(got) != 0 {
		t.Fatalf("terminal-owned Ctrl+N was advertised with preference disabled: %v", got)
	}
}

func TestFrameworkHelpAndMainMenuActionsPreserveFrameBehavior(t *testing.T) {
	initFrameworkActionTestScreen(t)
	previousHelpEngine := vtui.GlobalHelpEngine
	helpEngine := vtui.NewHelpEngine(nil)
	helpEngine.AddTopic(&vtui.HelpTopic{Name: "WorkspaceHelp", Lines: []string{"workspace help"}})
	vtui.GlobalHelpEngine = helpEngine
	t.Cleanup(func() { vtui.GlobalHelpEngine = previousHelpEngine })

	menu := vtui.NewMenuBar([]string{"&File"})
	menu.Items[0].SubItems = []vtui.MenuItem{{Text: "&Open"}}
	frame := &frameworkActionTestFrame{
		title:    "Workspace",
		help:     "WorkspaceHelp",
		menu:     menu,
		handleF1: true,
	}
	vtui.FrameManager.Push(frame)

	if !actionContextHelp() {
		t.Fatal("context help did not open the resolved frame topic")
	}
	if frame.f1Calls != 0 {
		t.Fatalf("context help redispatched F1 to the frame %d time(s)", frame.f1Calls)
	}
	if _, ok := vtui.FrameManager.GetTopFrame().(*vtui.HelpView); !ok {
		t.Fatalf("top frame = %T, want contextual help", vtui.FrameManager.GetTopFrame())
	}
	vtui.FrameManager.Pop()
	vtui.FrameManager.SyncCurrentScreen()

	if !actionActivateMainMenu() || !menu.Active {
		t.Fatal("main menu action did not activate the frame menu")
	}
	if top := vtui.FrameManager.GetTopFrame(); top == frame || top.GetType() != vtui.TypeMenu {
		t.Fatalf("top frame = %T, want the activated submenu", top)
	}
}

func TestPaletteMainMenuMatchesPanelsF9ActiveSide(t *testing.T) {
	initFrameworkActionTestScreen(t)
	panels := &panel.PanelsFrame{
		ActiveIdx:  1,
		ShowPanels: true,
		MenuBar:    vtui.NewMenuBar(nil),
		CmdLine:    cmdline.NewCommandLine(">"),
		TermView:   terminal.NewTerminalView(100, 30),
	}
	panels.MenuBar.SetOwner(panels)
	vtui.FrameManager.Push(panels)

	panels.MenuBar.SelectPos = 2
	if !actionActivateMainMenu() {
		t.Fatal("palette main-menu action failed for PanelsFrame")
	}
	if got := panels.MenuBar.SelectPos; got != 4 {
		t.Fatalf("right-panel menu position = %d, want physical F9 position 4", got)
	}
	if top := vtui.FrameManager.GetTopFrame(); top == panels || top.GetType() != vtui.TypeMenu {
		t.Fatalf("right-panel F9 top frame = %T, want menu", top)
	}
	vtui.FrameManager.Pop()
	vtui.FrameManager.SyncCurrentScreen()
	panels.MenuBar.Active = false

	panels.ActiveIdx = 0
	panels.MenuBar.SelectPos = 3
	if !actionActivateMainMenu() {
		t.Fatal("palette main-menu action failed for left panel")
	}
	if got := panels.MenuBar.SelectPos; got != 0 {
		t.Fatalf("left-panel menu position = %d, want physical F9 position 0", got)
	}
}

func TestFrameworkContextHelpPrefersFocusedTopicAndFallsBackToContents(t *testing.T) {
	screen := initFrameworkActionTestScreen(t)
	previousHelpEngine := vtui.GlobalHelpEngine
	helpEngine := vtui.NewHelpEngine(nil)
	helpEngine.AddTopic(&vtui.HelpTopic{Name: "FrameHelp", Lines: []string{"frame-topic-marker"}})
	helpEngine.AddTopic(&vtui.HelpTopic{Name: "FocusedHelp", Lines: []string{"focused-topic-marker"}})
	helpEngine.AddTopic(&vtui.HelpTopic{Name: "Contents", Lines: []string{"contents-topic-marker"}})
	vtui.GlobalHelpEngine = helpEngine
	t.Cleanup(func() { vtui.GlobalHelpEngine = previousHelpEngine })

	focused := vtui.NewText(0, 0, "focused", 0)
	focused.SetHelp("FocusedHelp")
	frame := &frameworkActionTestFrame{help: "FrameHelp", focused: focused, handleF1: true}
	vtui.FrameManager.Push(frame)

	if !actionContextHelp() {
		t.Fatal("focused context help was not opened")
	}
	helpView, ok := vtui.FrameManager.GetTopFrame().(*vtui.HelpView)
	if !ok {
		t.Fatalf("top frame = %T, want focused help", vtui.FrameManager.GetTopFrame())
	}
	helpView.Show(screen)
	var dump bytes.Buffer
	screen.Dump(&dump)
	if !strings.Contains(dump.String(), "focused-topic-marker") || strings.Contains(dump.String(), "frame-topic-marker") {
		t.Fatalf("focused help rendered unexpected topic:\n%s", dump.String())
	}
	if frame.f1Calls != 0 {
		t.Fatalf("focused help redispatched F1 %d time(s)", frame.f1Calls)
	}

	vtui.FrameManager.Pop()
	vtui.FrameManager.SyncCurrentScreen()
	frame.help = ""
	frame.focused = nil
	screen.AllocBuf(100, 30)
	if !actionContextHelp() {
		t.Fatal("Contents fallback help was not opened")
	}
	helpView, ok = vtui.FrameManager.GetTopFrame().(*vtui.HelpView)
	if !ok {
		t.Fatalf("top frame = %T, want Contents help", vtui.FrameManager.GetTopFrame())
	}
	helpView.Show(screen)
	dump.Reset()
	screen.Dump(&dump)
	if !strings.Contains(dump.String(), "contents-topic-marker") {
		t.Fatalf("Contents fallback rendered unexpected topic:\n%s", dump.String())
	}
}

func TestWorkspacePaletteEntriesResolveStableNumbersAtExecution(t *testing.T) {
	initFrameworkActionTestScreen(t)
	first := &frameworkActionTestFrame{
		title:    "Left",
		menuInfo: vtui.WorkspaceMenuInfo{Icon: "L", Primary: "Left files", Secondary: `C:\left`},
	}
	second := &frameworkActionTestFrame{
		title:    "Right",
		menuInfo: vtui.WorkspaceMenuInfo{Icon: "R", Primary: "Right files", Secondary: `D:\right`},
	}
	vtui.FrameManager.Push(first)
	vtui.FrameManager.AddScreen(second)
	vtui.FrameManager.RestoreScreenNumbers([]int{7, 3})
	vtui.FrameManager.ConfigureWorkspaceTabs(vtui.WorkspaceTabsOnCtrl, vtui.WorkspaceCtrlTabDirect)
	vtui.FrameManager.ConfigureWorkspaceAltNumberSwitch(true)

	entries := commandPaletteWorkspaceEntries()
	if len(entries) != 4 {
		t.Fatalf("workspace entries = %d, want activate and close for both workspaces", len(entries))
	}
	var firstEntry, secondEntry, firstClose, secondClose commandPaletteEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.ID, "Workspace.Move.") || strings.HasPrefix(entry.ID, "Workspace.Reorder.") {
			t.Fatalf("gesture-only workspace reorder leaked into the palette: %s", entry.ID)
		}
		switch entry.ID {
		case "Workspace.Activate.7":
			firstEntry = entry
		case "Workspace.Activate.3":
			secondEntry = entry
		case "Workspace.Close.7":
			firstClose = entry
		case "Workspace.Close.3":
			secondClose = entry
		}
	}
	if firstEntry.ID == "" || secondEntry.ID == "" || firstClose.ID == "" || secondClose.ID == "" {
		t.Fatalf("stable workspace IDs missing: %#v", entries)
	}
	if !strings.Contains(commandPaletteWorkspaceReorderExclusion, "no stable workspace reorder semantic action") {
		t.Fatalf("workspace gesture exclusion is not explicit: %q", commandPaletteWorkspaceReorderExclusion)
	}
	if secondEntry.Checked != true || firstEntry.Checked {
		t.Fatalf("checked states first=%v second=%v, active workspace is 3", firstEntry.Checked, secondEntry.Checked)
	}
	if firstEntry.Shortcut != "Alt+7, Ctrl+Alt+7" {
		t.Fatalf("workspace 7 shortcut = %q", firstEntry.Shortcut)
	}
	if !strings.Contains(firstEntry.Label, "7") || !strings.Contains(firstEntry.Label, "Left files") {
		t.Fatalf("workspace label = %q, want stable number and title", firstEntry.Label)
	}
	if firstClose.Shortcut != "" || !strings.Contains(firstClose.Label, "7") || !strings.Contains(firstClose.Label, "Left files") {
		t.Fatalf("workspace close metadata = %#v", firstClose)
	}
	if results := rankCommandPaletteEntries([]commandPaletteEntry{firstEntry}, "рабочее пространство", nil); len(results) != 1 {
		t.Fatal("Russian workspace translation did not find the current-language entry")
	}
	if results := rankCommandPaletteEntries([]commandPaletteEntry{firstClose}, "закрыть рабочее пространство", nil); len(results) != 1 {
		t.Fatal("Russian workspace close translation did not find the current-language entry")
	}

	// Reorder the tabs after taking the palette snapshot while preserving the
	// active screen. The captured command must still locate workspace 7.
	activeScreen := vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx]
	vtui.FrameManager.Screens[0], vtui.FrameManager.Screens[1] = vtui.FrameManager.Screens[1], vtui.FrameManager.Screens[0]
	for index, screen := range vtui.FrameManager.Screens {
		if screen == activeScreen {
			vtui.FrameManager.ActiveIdx = index
			break
		}
	}
	if !executeCommandPaletteEntry(firstEntry) {
		t.Fatal("workspace activation entry failed after tab reorder")
	}
	if got := vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Number; got != 7 {
		t.Fatalf("activated workspace %d, want stable number 7", got)
	}

	if !actionWorkspaceNext() || vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Number != 3 {
		t.Fatal("next workspace did not wrap to workspace 3")
	}
	if !actionWorkspacePrevious() || vtui.FrameManager.Screens[vtui.FrameManager.ActiveIdx].Number != 7 {
		t.Fatal("previous workspace did not wrap back to workspace 7")
	}
	if !actionWorkspaceNew() || !first.forkCalled {
		t.Fatal("new workspace did not emit the native fork command to the active frame")
	}
	if !actionWorkspaceList() || vtui.FrameManager.GetTopFrame().GetType() != vtui.TypeMenu {
		t.Fatal("workspace list did not open the native Screens menu")
	}
	vtui.FrameManager.Pop()
	vtui.FrameManager.SyncCurrentScreen()
	if !executeCommandPaletteEntry(firstClose) {
		t.Fatal("stable workspace close entry failed after tab reorder")
	}
	if len(vtui.FrameManager.Screens) != 1 || vtui.FrameManager.Screens[0].Number != 3 || !first.IsDone() {
		t.Fatalf("close left screens=%d number=%d firstDone=%v", len(vtui.FrameManager.Screens), vtui.FrameManager.Screens[0].Number, first.IsDone())
	}
	if executeCommandPaletteEntry(firstClose) {
		t.Fatal("stale workspace close entry succeeded after its stable target disappeared")
	}
}

func TestWorkspaceClosePreservesQueueVetoBelowHelpAndForBackgroundTarget(t *testing.T) {
	initFrameworkActionTestScreen(t)
	previousQueue := fileops.GlobalQueueManager
	previousHelp := vtui.GlobalHelpEngine
	t.Cleanup(func() {
		fileops.GlobalQueueManager = previousQueue
		vtui.GlobalHelpEngine = previousHelp
	})

	vtui.FrameManager.Push(&frameworkActionTestFrame{title: "Files"})
	task := &fileops.QueueTask{ID: 41, State: "Running"}
	fileops.GlobalQueueManager = fileops.NewQueueManagerWithTasks(task)
	queue := fileops.NewQueueFrame()
	queue.UpdateTasks([]*fileops.QueueTask{task})
	vtui.FrameManager.AddScreen(queue)
	vtui.FrameManager.RestoreScreenNumbers([]int{11, 29})

	helpEngine := vtui.NewHelpEngine(nil)
	helpEngine.AddTopic(&vtui.HelpTopic{Name: "Contents", Lines: []string{"Queue help"}})
	vtui.GlobalHelpEngine = helpEngine
	help := vtui.NewHelpView(helpEngine, "Contents")
	vtui.FrameManager.Push(help)

	var closeQueue commandPaletteEntry
	for _, entry := range commandPaletteWorkspaceEntries() {
		if entry.ID == "Workspace.Close.29" {
			closeQueue = entry
			break
		}
	}
	if closeQueue.ID == "" {
		t.Fatal("dynamic close for queue workspace 29 is missing")
	}
	if !actionWorkspaceClose() || len(vtui.FrameManager.Screens) != 2 {
		t.Fatal("active Workspace.Close bypassed Queue veto below Help")
	}
	if help.IsDone() || queue.IsDone() {
		t.Fatal("Queue veto closed frames beneath Help")
	}

	vtui.FrameManager.SwitchScreen(0)
	if !executeCommandPaletteEntry(closeQueue) || len(vtui.FrameManager.Screens) != 2 {
		t.Fatal("stable background Workspace.Close bypassed Queue veto")
	}

	task.SetState("Done")
	if !executeCommandPaletteEntry(closeQueue) {
		t.Fatal("stable background Workspace.Close failed after Queue became idle")
	}
	if len(vtui.FrameManager.Screens) != 1 || vtui.FrameManager.Screens[0].Number != 11 {
		t.Fatalf("idle queue close left workspaces %#v", vtui.FrameManager.Screens)
	}
	if !help.IsDone() || !queue.IsDone() {
		t.Fatal("idle queue workspace did not close its complete frame stack")
	}
}

func TestWorkspaceCloseKeepsTheOnlyPanelsWorkspace(t *testing.T) {
	initFrameworkActionTestScreen(t)
	panels := paneltest.SetupMockPanelsFrame(t)
	vtui.FrameManager.Push(panels)
	viewer := &frameworkActionTestFrame{title: "Viewer"}
	vtui.FrameManager.AddScreen(viewer)

	panelsScreen := vtui.FrameManager.Screens[0]
	if !actionWorkspaceCloseNumber(panelsScreen.Number) {
		t.Fatal("closing the only panels workspace was not handled")
	}
	if len(vtui.FrameManager.Screens) != 2 || panels.IsDone() {
		t.Fatalf("only panels workspace was closed: screens=%d panelsDone=%v", len(vtui.FrameManager.Screens), panels.IsDone())
	}

	viewerScreen := vtui.FrameManager.Screens[1]
	if !actionWorkspaceCloseNumber(viewerScreen.Number) {
		t.Fatal("viewer workspace did not close while panels workspace remained")
	}
	if len(vtui.FrameManager.Screens) != 1 || vtui.FrameManager.Screens[0] != panelsScreen || panels.IsDone() {
		t.Fatalf("closing viewer changed the wrong workspace: screens=%d panelsDone=%v", len(vtui.FrameManager.Screens), panels.IsDone())
	}
}

func TestPanelsFrameCloseVetoProtectsTheOnlyPanelsWorkspace(t *testing.T) {
	initFrameworkActionTestScreen(t)
	panels := paneltest.SetupMockPanelsFrame(t)
	vtui.FrameManager.Push(panels)
	vtui.FrameManager.AddScreen(&frameworkActionTestFrame{title: "Editor"})
	vtui.FrameManager.SwitchScreen(0)

	vtui.FrameManager.CloseActiveScreen()
	if len(vtui.FrameManager.Screens) != 2 || panels.IsDone() {
		t.Fatalf("native close path removed the only panels workspace: screens=%d panelsDone=%v", len(vtui.FrameManager.Screens), panels.IsDone())
	}
}

func TestWorkspaceNewFindsPanelsBehindFullScreenWorkspace(t *testing.T) {
	initFrameworkActionTestScreen(t)
	source := paneltest.SetupMockPanelsFrame(t)
	defer source.Close()
	vtui.FrameManager.Push(source)

	// Image, Editor, Viewer and Queue screens are all separate workspaces with
	// no panel.PanelsFrame in their active stack. Image is a lightweight real frame
	// that exercises that shared full-screen layout without test doubles.
	image := &media.ImageView{}
	vtui.FrameManager.AddScreen(image)
	if got := len(vtui.FrameManager.Screens); got != 2 {
		t.Fatalf("screens before fork = %d, want 2", got)
	}
	if !actionWorkspaceNew() {
		t.Fatal("Workspace.New could not resolve panels behind an image workspace")
	}
	if got := len(vtui.FrameManager.Screens); got != 3 {
		t.Fatalf("screens after fork = %d, want 3", got)
	}
	clone, ok := vtui.FrameManager.GetTopFrame().(*panel.PanelsFrame)
	if !ok || clone == source {
		t.Fatalf("new workspace top = %T, want a cloned PanelsFrame", vtui.FrameManager.GetTopFrame())
	}
	defer clone.Close()
	if !clone.ShowPanels {
		t.Fatal("new workspace did not expose the cloned panels")
	}
}

func TestDumpScreenToWritesCurrentBuffer(t *testing.T) {
	screen := initFrameworkActionTestScreen(t)
	vtui.NewPainter(screen).DrawString(2, 1, "palette-screen-dump", 0)
	path := filepath.Join(t.TempDir(), "screen.log")
	if err := dumpScreenTo(path); err != nil {
		t.Fatalf("dumpScreenTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read screen dump: %v", err)
	}
	if !strings.Contains(string(data), "palette-screen-dump") {
		t.Fatalf("screen dump does not contain rendered text: %q", data)
	}
}
