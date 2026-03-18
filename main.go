package main

import (
	"fmt"
	"os"

	"github.com/unxed/vtinput"
	"path/filepath"

	"github.com/unxed/vtui"
	"golang.org/x/term"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-test-plugins" {
		vtui.DebugLog("--- TEST MODE ---")
		pm := NewPluginManager()
		pm.LoadAll()
		pm.CloseAll()
		return
	}

	// 1. Enter Raw Mode
	restore, err := vtinput.Enable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	defer restore()

	// 2. Initialize ScreenBuf
	width, height, _ := term.GetSize(int(os.Stdin.Fd()))
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(width, height)

	// 3. Configure FrameManager
	vtui.FrameManager.Init(scr)
	// Sync vtui strings with our localization
	vtui.UIStrings.DesktopWelcome = Msg("Desktop.Welcome")
	// Setup f4 specific palette extensions
	SetDefaultF4Palette()

	// Apply custom palette from system config directory
	configDir, err := os.UserConfigDir()
	if err == nil {
		configPath := filepath.Join(configDir, "f4", "farcolors.ini")
		ini := LoadIni(configPath)
		InitColors(ini)
	}

	// Initialize MacroManager
	os.MkdirAll(filepath.Join(configDir, "f4"), 0755)
	MacroMgr = NewMacroManager(filepath.Join(configDir, "f4", "key_macros.ini"))
	vtui.FrameManager.EventFilter = MacroMgr.Filter
	// Layer 0: Desktop (background)
	vtui.FrameManager.Push(vtui.NewDesktop())

	// Layer 1: Panels (f4 core)
	panels := NewPanelsFrame()
	panels.ResizeConsole(width, height) // Initialize panel sizes before pushing
	vtui.FrameManager.Push(panels)

	// Create test panel with many files for scrollbar
	if fsp, ok := panels.left.(*FileSystemPanel); ok {
		for i := 0; i < 50; i++ {
			fsp.entries = append(fsp.entries, &fileEntry{VFSItem: VFSItem{Name: fmt.Sprintf("test_file_%d.txt", i), Size: 1024}})
		}
		rows := make([]vtui.TableRow, len(fsp.entries))
		for i, e := range fsp.entries { rows[i] = e }
		fsp.table.SetRows(rows)
	}

	// --- Initialize Plugins ---
	pluginManager := NewPluginManager()
	pluginManager.LoadAll()
	defer pluginManager.CloseAll()

	// 4. Run!
	vtui.FrameManager.Run()
}