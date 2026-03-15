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

	// Layer 0: Desktop (background)
	vtui.FrameManager.Push(vtui.NewDesktop())

	// Layer 1: Panels (f4 core)
	panels := NewPanelsFrame()
	panels.ResizeConsole(width, height) // Initialize panel sizes before pushing
	vtui.FrameManager.Push(panels)

	// 4. Run!
	vtui.FrameManager.Run()
}