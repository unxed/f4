package cmdline

import (
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"testing"
)

func TestCommandLine_Input(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 10, 0)

	// Simulate typing 'f'
	cl.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'f',
	})

	if cl.Edit.GetText() != "f" {
		t.Errorf("Expected cmdline text 'f', got '%s'", cl.Edit.GetText())
	}

	// Simulate Backspace
	cl.ProcessKey(&vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_BACK,
	})

	if len(cl.Edit.GetText()) != 0 {
		t.Error("CommandLine should be empty after backspace")
	}
}
func TestCommandLine_InitialFocus(t *testing.T) {
	cl := NewCommandLine("> ")

	if !cl.Edit.IsFocused() {
		t.Error("CommandLine's underlying Edit should be focused upon creation to ensure cursor visibility")
	}
	if !cl.IsFocused() {
		t.Error("CommandLine should be focused upon creation")
	}
}

func TestCommandLine_History(t *testing.T) {
	vtui.SetDefaultPalette()
	theme.SetDefaultF4Palette()
	cl := NewCommandLine("> ")

	// 1. Test adding history
	cl.Edit.AddHistory("ls -la")
	cl.Edit.AddHistory("cd /tmp")
	cl.Edit.AddHistory("ls -la") // Duplicate of a previous one, but not the latest

	// With DeduplicateHistory = true, the duplicate "ls -la" is moved to top, removing the older one.
	if len(cl.Edit.History) != 2 {
		t.Errorf("Expected history length 2, got %d", len(cl.Edit.History))
	}

	// 2. Test navigation
	cl.Edit.HistoryUp() // Should be "ls -la" (index 0)
	if cl.Edit.GetText() != "ls -la" {
		t.Errorf("HistoryUp(1) failed: expected 'ls -la', got '%s'", cl.Edit.GetText())
	}

	cl.Edit.HistoryUp() // Should be "cd /tmp" (index 1)
	if cl.Edit.GetText() != "cd /tmp" {
		t.Errorf("HistoryUp(2) failed: expected 'cd /tmp', got '%s'", cl.Edit.GetText())
	}

	cl.Edit.HistoryDown() // Back to "ls -la" (index 0)
	if cl.Edit.GetText() != "ls -la" {
		t.Errorf("HistoryDown(1) failed: expected 'ls -la', got '%s'", cl.Edit.GetText())
	}

	cl.Edit.HistoryDown() // Should clear the line
	if cl.Edit.GetText() != "" {
		t.Errorf("HistoryDown(2) failed: expected empty string, got '%s'", cl.Edit.GetText())
	}

	// 3. Test duplicate prevention (consecutive)
	cl.Edit.AddHistory("pwd")
	cl.Edit.AddHistory("pwd")
	if len(cl.Edit.History) != 3 { // Only one "pwd" should be added, total items: ["pwd", "ls -la", "cd /tmp"]
		t.Errorf("Duplicate history prevention failed, length: %d", len(cl.Edit.History))
	}

	// 4. Test reset on typing
	cl.Edit.HistoryUp() // "pwd"
	cl.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    ' ',
	})
	if cl.Edit.HistoryPos != -1 {
		t.Error("History browsing state should reset after typing")
	}
}
func TestCommandLine_HistoryBoundaries(t *testing.T) {
	cl := NewCommandLine("> ")
	cl.Edit.AddHistory("cmd1")

	// Go up once
	cl.Edit.HistoryUp()
	if cl.Edit.GetText() != "cmd1" {
		t.Fatal("Setup failed")
	}

	// Go up again - should stay at cmd1
	cl.Edit.HistoryUp()
	if cl.Edit.GetText() != "cmd1" {
		t.Error("HistoryUp should cap at the end of the list")
	}

	// Go down to clear
	cl.Edit.HistoryDown()
	if cl.Edit.GetText() != "" {
		t.Error("HistoryDown should clear the line when at the start of history")
	}

	// Go down again - should stay empty and not crash
	cl.Edit.HistoryDown()
	if cl.Edit.HistoryPos != -1 || cl.Edit.GetText() != "" {
		t.Error("HistoryDown should stay at -1 when already empty")
	}
}

func TestCommandLine_AutoCompleteDisabled(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 10, 0)
	cl.Edit.History = []string{"ls", "long-command"}

	// 1. Выключаем глобальную настройку
	oldCfg := config.App
	config.App.CommandLineAutoComplete = false
	defer func() { config.App = oldCfg }()

	// 2. Симулируем ввод буквы 'l'
	cl.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'l',
	})

	// 3. Проверяем, что меню автодополнения НЕ появилось в FrameManager
	top := vtui.FrameManager.GetTopFrame()
	if _, isAc := AsCompletionMenu(top); isAc {
		t.Error("AutoCompleteMenu was shown even though CommandLineAutoComplete is false")
	}
}

func TestCommandLine_NoAutoCompleteMenuWhenDisabled(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	config.App.CommandLineAutoComplete = false
	defer func() { config.App = oldCfg }()

	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 30, 0)
	// PathHintsEnabled follows the option (synced like the settings dialog does).
	cl.Edit.PathHintsEnabled = config.App.CommandLineAutoComplete
	cl.Edit.History = []string{"dir /s", "dir /s d", "dir /s"}

	// Type "dir /s": the separator and the trailing character must not open
	// the autocomplete menu (neither path hints nor legacy history).
	for _, r := range "dir /s" {
		cl.ProcessKey(&vtinput.InputEvent{
			Type:    vtinput.KeyEventType,
			KeyDown: true,
			Char:    r,
		})
	}

	top := vtui.FrameManager.GetTopFrame()
	if _, isAc := AsCompletionMenu(top); isAc {
		t.Error("AutoCompleteMenu was shown even though CommandLineAutoComplete is false")
	}
}

func TestCommandLine_AutoCompleteSuppressed(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 10, 0)
	cl.Edit.History = []string{"ls", "long-command"}
	cl.AutoCompleteSuppressed = true

	oldCfg := config.App
	config.App.CommandLineAutoComplete = true
	defer func() { config.App = oldCfg }()

	cl.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'l',
	})

	top := vtui.FrameManager.GetTopFrame()
	if _, isAc := AsCompletionMenu(top); isAc {
		t.Error("AutoCompleteMenu was shown even though AutoCompleteSuppressed is true")
	}
}

// The command line opts out of the generic vtui trigger because it drives the
// completion menu itself, in ProcessKey, under gating vtui cannot see. If the
// opt-out is ever dropped, Edit.ProcessKey opens the menu one call earlier and
// CommandLineAutoComplete, AutoCompleteSuppressed and history browsing stop
// having any effect -- which is how the three tests above started failing once
// vtui learned to complete history fields on its own.
func TestCommandLine_OptsOutOfWidgetAutoComplete(t *testing.T) {
	cl := NewCommandLine("> ")
	if !cl.Edit.NoAutoComplete {
		t.Error("command line edit must opt out of vtui's own autocomplete trigger")
	}
}

func TestCommandLine_AutoCompleteStillOpensWhenAllowed(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()

	oldCfg := config.App
	config.App.CommandLineAutoComplete = true
	defer func() { config.App = oldCfg }()

	cl := NewCommandLine("> ")
	cl.SetPosition(0, 0, 10, 0)
	cl.Edit.History = []string{"ls", "long-command"}

	cl.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'l'})

	top := vtui.FrameManager.GetTopFrame()
	if _, isAc := AsCompletionMenu(top); !isAc {
		t.Fatal("opting out of the widget trigger also killed the command line's own menu")
	}
	vtui.FrameManager.Pop()
}
