package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"time"
)

// These two press keys through the application's routing — the hotkey manager
// and the macro filter — which is why they did not follow the viewer.

func TestViewerView_HexModeToggle(t *testing.T) {
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	tmp := filepath.Join(tmpDir, "hex.txt")
	// 32 bytes of data
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	vv, err := viewer.NewViewerView(context.Background(), v, tmp)
	if err != nil {
		t.Fatalf("Failed to create viewer.ViewerView: %v", err)
	}
	defer vv.Close()
	vtui.FrameManager.Push(vv)

	// Binary detection may open this fixture in hex mode already. Exercise the
	// toggle itself from a deterministic text-mode starting state.
	vv.HexMode = false
	// Set an offset that is NOT aligned to 16
	vv.TopOffset = 10

	// Toggle Hex Mode
	pressKey(vv, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F4})

	if !vv.HexMode {
		t.Error("F4 failed to toggle HexMode")
	}

	// Hex mode MUST align TopOffset to 16-byte boundary
	if vv.TopOffset != 0 {
		t.Errorf("Hex mode failed to align offset: expected 0, got %d", vv.TopOffset)
	}

	// Toggle back to Text
	pressKey(vv, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F4})
	if vv.HexMode {
		t.Error("F4 failed to toggle back to TextMode")
	}

}
func TestViewerView_ScrollbarEOFAlignment(t *testing.T) {
	vtui.SetDefaultPalette()
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	tmp := filepath.Join(tmpDir, "scroll_test.txt")
	// Создаем файл из 50 строк
	content := ""
	for i := 0; i < 50; i++ {
		content += "this is a test line for scrollbar alignment\n"
	}
	if err := os.WriteFile(tmp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(tmpDir)
	vv, err := viewer.NewViewerView(context.Background(), v, tmp)
	if err != nil {
		t.Fatalf("Failed to create viewer.ViewerView: %v", err)
	}
	defer vv.Close()

	// Viewport: 1 строка статус, 10 строк контент.
	vv.SetPosition(0, 0, 40, 10)

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(41, 11)

	// --- 1. Проверка в текстовом режиме ---
	// Прыгаем в конец
	vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})

	// Ждем завершения асинхронного расчета jumpToEnd.
	// jumpToEnd устанавливает TopOffset через ctx.RunOnUI, которая ставит задачу
	// в FrameManager.TaskChan. Задача "Busy = false" (defer) и задача установки
	// TopOffset могут быть в канале в любом порядке. Поэтому не выходим из цикла,
	// пока vv.Busy не станет false И TopOffset не изменится с начального 0.
	timeout := time.After(2 * time.Second)
	for vv.Busy || vv.TopOffset == 0 {
		select {
		case task := <-fm.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for Text jumpToEnd")
		}
	}

	// Вызываем Show, чтобы сработала логика SetParams внутри DisplayObject
	vv.Show(scr)

	if vv.ScrollBar.Max != int(vv.Backend.Size()) {
		t.Errorf("Text Mode: ScrollBar.Max (%d) != Size (%d) at EOF", vv.ScrollBar.Max, vv.Backend.Size())
	}

	// --- 2. Проверка в Hex режиме ---
	pressKey(vv, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F4})
	// В Hex режиме jumpToEnd отрабатывает мгновенно, если данные в кэше
	vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	vv.Show(scr)

	if int(vv.TopOffset) != vv.ScrollBar.Max {
		t.Errorf("Hex Mode: TopOffset (%d) != ScrollBar.Max (%d) at EOF", vv.TopOffset, vv.ScrollBar.Max)
	}

	// Дополнительно: проверяем, что TopOffset в Hex выровнен по 16 байт
	if vv.TopOffset%16 != 0 {
		t.Errorf("Hex Mode: TopOffset (%d) is not aligned to 16 bytes", vv.TopOffset)
	}
}
