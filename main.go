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
	// 1. Входим в Raw Mode
	restore, err := vtinput.Enable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return
	}
	defer restore()

	fmt.Print("\x1b[?25l") // Скрыть курсор
	defer fmt.Print("\x1b[?25h")

	// 2. Инициализируем ScreenBuf
	width, height, _ := term.GetSize(int(os.Stdin.Fd()))
	scr := vtui.NewScreenBuf()
	scr.AllocBuf(width, height)

	// 3. Настраиваем FrameManager
	vtui.FrameManager.Init(scr)

	// Применяем пользовательскую палитру из системной папки конфигов
	configDir, err := os.UserConfigDir()
	if err == nil {
		configPath := filepath.Join(configDir, "f4", "farcolors.ini")
		ini := LoadIni(configPath)
		InitColors(ini)
	}

	// Слой 0: Рабочий стол (фон)
	vtui.FrameManager.Push(vtui.NewDesktop())

	// Слой 1: Панели (ядро f4)
	panels := NewPanelsFrame()
	panels.ResizeConsole(width, height) // Инициализируем размеры панелей перед пушем
	vtui.FrameManager.Push(panels)

	// 4. Запуск!
	vtui.FrameManager.Run()
}