package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/history"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// Typing "./" in the command line offered every command that contained "./"
// anywhere. Like far2l, it now offers the commands that start with it, the
// ones run in this directory first, and the files after them.
func TestCommandLineCompletionMatchesHistoryByPrefix(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	oldConfig := config.App
	t.Cleanup(func() { config.App = oldConfig })
	oldPathHints := vtui.PathHintProvider
	t.Cleanup(func() { vtui.PathHintProvider = oldPathHints })
	oldHistory := vtui.GlobalHistoryProvider
	t.Cleanup(func() { vtui.GlobalHistoryProvider = oldHistory })

	config.App.CommandLineAutoComplete = true
	config.App.PathHintSource = panel.PathHintSourceActive
	vtui.PathHintProvider = panel.PathHintProvider

	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "build.sh"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	pf := paneltest.SetupMockPanelsFrame(t)
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	pf.GetActivePanel().Vfs = vfs.NewOSVFS(root)
	cwd := pf.GetActivePanel().Vfs.GetPath()

	hp := history.NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	vtui.GlobalHistoryProvider = hp
	hp.SaveRichHistory("cmdline", []history.HistoryRecord{
		{Name: "cd ../x", Dir: cwd},
		{Name: "./deploy.sh", Dir: "/elsewhere"},
		{Name: "git add ./y", Dir: cwd},
		{Name: "./build.sh --fast", Dir: cwd},
	})
	pf.AttachCommandHistory()
	pf.CmdLine.Edit.SetText("")
	vtui.FrameManager.Push(pf)

	// Each key goes where the event loop would send it: to the menu once
	// "." has opened it, which hands the key on to the line and re-filters.
	for _, r := range "./" {
		key := &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r}
		if menu, ok := vtui.FrameManager.GetTopFrame().(*cmdline.CompletionMenu); ok {
			menu.ProcessKey(key)
		} else {
			pf.CmdLine.ProcessKey(key)
		}
	}
	menu, ok := cmdline.AsCompletionMenu(vtui.FrameManager.GetTopFrame())
	if !ok {
		t.Fatal("typing ./ should open the completion menu")
	}
	defer menu.Close()

	var rows []string
	for _, m := range menu.Matches {
		if m == "" {
			m = "---"
		}
		rows = append(rows, m)
	}
	got := strings.Join(rows, " | ")
	for _, unwanted := range []string{"cd ../x", "git add ./y"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("menu offers %q, which only contains ./: %s", unwanted, got)
		}
	}
	// History first (this directory's command, then the other one), files last.
	if len(rows) < 5 || rows[0] != "./build.sh --fast" || rows[1] != "---" ||
		rows[2] != "./deploy.sh" || rows[3] != "---" || !strings.Contains(rows[4], "build.sh") {
		t.Fatalf("menu rows = %s", got)
	}
}
