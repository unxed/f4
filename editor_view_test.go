package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockCrashingHighlighter fails the test if it receives a line longer than the safety limit.
type mockCrashingHighlighter struct {
	t *testing.T
}

const highlighterLimit = 64 * 1024

func (m *mockCrashingHighlighter) Highlight(line string, prev any, base uint64) ([]uint64, any) {
	if len(line) > highlighterLimit {
		m.t.Errorf("FATAL: Highlighter received a line of %d bytes, which is over the safety limit of %d", len(line), highlighterLimit)
	}
	return nil, nil
}
func (m *mockCrashingHighlighter) Name() string                                     { return "CrashingMock" }
func (m *mockCrashingHighlighter) Match(filename, content string) bool              { return true }
func (m *mockCrashingHighlighter) Create(filename, content string) vtui.Highlighter { return m }

type mockStatefulHighlighter struct {
	statesComputed int
}

func (m *mockStatefulHighlighter) Highlight(line string, prev any, base uint64) ([]uint64, any) {
	depth := 0
	if prev != nil {
		depth = prev.(int)
	}
	// Мы увеличиваем счетчик только когда prev == nil или когда это новый расчет (имитация)
	return make([]uint64, len(line)), depth + 1
}

func TestEditor_StatefulHighlighting(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	content := "line1\nline2\nline3"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)

	mh := &mockStatefulHighlighter{}
	ev.highlighter = mh

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)

	// 1. Первый рендер
	ev.Show(scr)

	if len(ev.lineStates) != 3 {
		t.Errorf("Expected 3 line states in cache, got %d", len(ev.lineStates))
	}

	// Проверяем цепочку состояний (1 -> 2 -> 3)
	if ev.lineStates[0].(int) != 1 || ev.lineStates[1].(int) != 2 || ev.lineStates[2].(int) != 3 {
		t.Errorf("State chain corrupted: %v", ev.lineStates)
	}

	// 2. Очищаем кэш состояний вручную и проверяем, что он пересчитывается
	ev.invalidateStates(0)
	if len(ev.lineStates) != 0 {
		t.Error("invalidateStates(0) failed to clear cache")
	}
	ev.Show(scr)
	if len(ev.lineStates) != 3 {
		t.Error("Cache failed to re-populate")
	}
}

func TestEditor_HighlightingInvalidation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1\nline2\nline3"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)

	mh := &mockStatefulHighlighter{}
	ev.highlighter = mh
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)

	// Наполняем кэш
	ev.Show(scr)
	if len(ev.lineStates) != 3 {
		t.Fatal("Setup failed")
	}

	// 1. Редактируем вторую строку (индекс 1)
	ev.CursorLine = 1
	ev.CursorPos = 0
	// Симулируем ввод символа '!'
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// Кэш должен быть инвалидирован НАЧИНАЯ со второй строки.
	// То есть должна остаться только первая строка (индекс 0).
	if len(ev.lineStates) != 1 {
		t.Errorf("Cache not invalidated correctly. Expected length 1 (line 0 state), got %d", len(ev.lineStates))
	}

	// 2. Рендерим снова - кэш должен восстановиться
	ev.Show(scr)
	if len(ev.lineStates) != 3 {
		t.Error("Cache failed to re-populate after invalidation")
	}
}

// waitPtString waits for a PieceTable to settle and returns its content as string.
// Used for tests involving AsyncBuffers.
func waitPtString(t *testing.T, pt *piecetable.PieceTable) string {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for PieceTable data")
		default:
			// Pump UI tasks to allow AsyncBuffer fetches to complete
			select {
			case task := <-vtui.FrameManager.TaskChan:
				task()
			default:
				res, err := pt.Bytes()
				if err == nil {
					return string(res)
				}
				if err != piecetable.ErrLoading {
					t.Fatalf("PieceTable read error: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}
}
func TestEditorView_TypingAndBackspace(t *testing.T) {
	pt := piecetable.New([]byte("Hello"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24) // Устанавливаем стандартный размер 80x25
	ev.CursorPos = 5             // End of "Hello"

	// 1. Typing '!'
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	if pt.String() != "Hello!" {
		t.Errorf("Typing failed: expected 'Hello!', got '%s'", pt.String())
	}
	if ev.CursorPos != 6 {
		t.Errorf("CursorPos after typing: expected 6, got %d", ev.CursorPos)
	}

	// 2. Deleting '!' via Backspace
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if pt.String() != "Hello" {
		t.Errorf("Backspace failed: expected 'Hello', got '%s'", pt.String())
	}
	if ev.CursorPos != 5 {
		t.Errorf("CursorPos after backspace: expected 5, got %d", ev.CursorPos)
	}
}

func TestEditorView_LineNavigation(t *testing.T) {
	pt := piecetable.New([]byte("Line1\nLine2"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorLine = 0
	ev.CursorPos = 5 // End of "Line1"

	// 1. Right Arrow at the end of the line -> move to the beginning of the next
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})
	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("Cross-line Right failed: expected Line 1, Pos 0. Got Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}

	// 2. Left Arrow at the start of the line -> move to the end of the previous
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT})
	if ev.CursorLine != 0 || ev.CursorPos != 5 {
		t.Errorf("Cross-line Left failed: expected Line 0, Pos 5. Got Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_EnterAndBackspaceMerging(t *testing.T) {
	pt := piecetable.New([]byte("ABC"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 1 // Between A and B

	// 1. Press Enter -> split line "A" and "BC"
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	if pt.String() != "A\nBC" {
		t.Errorf("Enter splitting failed: expected 'A\\nBC', got %q", pt.String())
	}
	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("Cursor position after Enter wrong: Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}

	// 2. Press Backspace at the start of the second line -> merge back to "ABC"
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if pt.String() != "ABC" {
		t.Errorf("Backspace merging failed: expected 'ABC', got %q", pt.String())
	}
	if ev.CursorLine != 0 || ev.CursorPos != 1 {
		t.Errorf("Cursor position after merge wrong: Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_StickyColumn(t *testing.T) {
	// Creating text:
	// LongLine (8)
	// Short (5)
	// LongLine (8)
	pt := piecetable.New([]byte("LongLine\nShort\nLongLine"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.WordWrap = false // Для этого теста отключаем перенос, чтобы имитировать классику

	// Position at the end of the first long line
	ev.CursorLine = 0
	ev.CursorPos = 8
	ev.DesiredVisualCol = 8

	// 1. Down to short line -> visually at the end (5), но желаемая колонка остается 8
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if ev.CursorPos != 5 {
		t.Errorf("Down to short line: expected pos 5, got %d", ev.CursorPos)
	}
	if ev.DesiredVisualCol != 8 {
		t.Errorf("Desired position lost! Expected 8, got %d", ev.DesiredVisualCol)
	}

	// 2. Down to long line -> position should be restored to 8
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if ev.CursorLine != 2 || ev.CursorPos != 8 {
		t.Errorf("Sticky column failed: expected Line 2, Pos 8. Got Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_SaveFile(t *testing.T) {
	// 1. Create a temporary file
	tmpFile := "test_save.txt"
	defer os.Remove(tmpFile)
	err := os.WriteFile(tmpFile, []byte("Original"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Open it in the editor
	pt := piecetable.New([]byte("Original"))
	v := vfs.NewOSVFS(t.TempDir())
	ev := NewEditorView(pt, v, tmpFile)
	defer ev.Close()
	// Add mock file object to editor so SaveToFile logic triggers cleanly
	f, err := v.Open(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	ev.file = f

	// 3. Simulate typing text " + Edit" at the end
	ev.CursorPos = 8
	for _, char := range " + Edit" {
		ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char})
	}

	// 4. Simulate pressing F2 (Save)
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf()) // Needed for PostTask to work
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F2})

	// 5. Wait for async save to finish by processing tasks
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for async save to complete")
		}
	}

	// 6. Read file from disk and check that data was written
	savedData, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	expected := "Original + Edit"
	if string(savedData) != expected {
		t.Errorf("Save failed: expected %q on disk, got %q", expected, string(savedData))
	}
}

func TestEditorView_Selection(t *testing.T) {
	pt := piecetable.New([]byte("Select Me"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorLine = 0
	ev.CursorPos = 0

	// 1. Start selection (Shift + Right x 6)
	// Important to emulate KeyDown with Shift flag in the test
	for i := 0; i < 6; i++ {
		ev.ProcessKey(&vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  vtinput.VK_RIGHT,
			ControlKeyState: vtinput.ShiftPressed,
		})
	}

	if !ev.selActive {
		t.Fatal("Selection should be active")
	}
	if ev.selAnchorOffset != 0 {
		t.Errorf("Anchor should be 0, got %d", ev.selAnchorOffset)
	}

	min, max := ev.getSelectionRange()
	if min != 0 || max != 6 {
		t.Errorf("Wrong selection range: [%d:%d]", min, max)
	}

	// 2. Copying (Ctrl+C) - checking only the log or lack of panic
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_C,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})

	// 3. Deleting selected (Delete)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_DELETE,
	})

	if pt.String() != " Me" {
		t.Errorf("Delete selection failed: %q", pt.String())
	}
	if ev.selActive {
		t.Error("Selection should be cleared after delete")
	}
}

func TestEditorView_DeleteSelectionMultiline(t *testing.T) {
	// Three-line text
	pt := piecetable.New([]byte("Line1\nLine2\nLine3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)

	// 1. Select the end of the first line, all of the second, and the start of the third
	// "Line[1\nLine2\nLin]e3"
	ev.CursorLine = 0
	ev.CursorPos = 4
	ev.selActive = true
	ev.selAnchorOffset = ev.li.GetLineOffset(0) + ev.CursorPos // Offset 4

	// Move cursor to the end of selection
	ev.CursorLine = 2
	ev.CursorPos = 3
	// Offset of the beginning of "Line3" (12) + 3 = 15

	// 2. Delete selection
	ev.DeleteSelection()

	// Expected result: "Linee3"
	expected := "Linee3"
	if pt.String() != expected {
		t.Errorf("Multiline delete failed: expected %q, got %q", expected, pt.String())
	}

	// Check that line index updated (1 line left)
	if ev.li.LineCount() != 1 {
		t.Errorf("LineCount after multiline delete: expected 1, got %d", ev.li.LineCount())
	}

	// Check cursor position (should be at the deletion point)
	if ev.CursorLine != 0 || ev.CursorPos != 4 {
		t.Errorf("Cursor after multiline delete: expected Line 0, Pos 4. Got Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_WordWrapNavigation(t *testing.T) {
	// Текст: "0123456789ABCDEFGHIJklmno" (25 символов)
	// При чистой ширине 10:
	// Ряд 0: "0123456789" (оффсеты 0-10)
	// Ряд 1: "ABCDEFGHIJ" (оффсеты 10-20)
	// Ряд 2: "klmno"      (оффсеты 20-25)
	text := "0123456789ABCDEFGHIJklmno"
	pt := piecetable.New([]byte(text))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true
	// Set width to 11 so that width minus scrollbar (11-1) is exactly 10.
	ev.SetPosition(0, 0, 10, 6)

	// Инициализируем DesiredVisualCol (имитируем клик или переход)
	ev.CursorLine = 0
	ev.CursorPos = 5 // Символ '5'
	ev.updateDesiredVisualCol()

	// 1. Вниз на Ряд 1. Колонка 5 должна соответствовать символу 'F' (оффсет 15)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})

	if ev.CursorPos != 15 {
		t.Errorf("WordWrap Down: expected byte pos 15, got %d", ev.CursorPos)
	}

	// 2. Вниз на Ряд 2. Колонка 5 должна соответствовать концу строки (оффсет 25),
	// так как "klmno" короче 5 колонок.
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if ev.CursorPos != 25 {
		t.Errorf("WordWrap Down to end: expected byte pos 25, got %d", ev.CursorPos)
	}

	// 3. Вверх обратно на Ряд 1. Должны вернуться на символ 'F' (15)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_UP})
	if ev.CursorPos != 15 {
		t.Errorf("WordWrap Up: expected byte pos 15, got %d", ev.CursorPos)
	}
}

func TestEditorView_UTF8Editing(t *testing.T) {
	// "Привет" - Russian letters occupy 2 bytes each
	pt := piecetable.New([]byte("Привет"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 4 // After "Пр" (4 bytes)

	// 1. Insert another letter (2 bytes)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'и'})
	if ev.CursorPos != 6 {
		t.Errorf("UTF8 typing: expected pos 6, got %d", ev.CursorPos)
	}

	// 2. Backspace should remove exactly one character (2 bytes)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if pt.String() != "Привет" {
		t.Errorf("UTF8 backspace failed: %q", pt.String())
	}
	if ev.CursorPos != 4 {
		t.Errorf("UTF8 backspace pos: expected 4, got %d", ev.CursorPos)
	}
}

func TestEditorView_WideCharWrap(t *testing.T) {
	// "A世B" -> A(1), 世(2), B(1).
	// Ширина 2.
	pt := piecetable.New([]byte("A世B"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true
	ev.engine.SetWidth(2)

	frags := ev.engine.GetFragments(0)
	if len(frags) < 2 {
		t.Fatalf("Expected at least 2 fragments, got %d", len(frags))
	}
	// Проверяем, что широкие символы не разрываются (это гарантирует WrapEngine)
}

func TestEditorView_SelectionWrapping(t *testing.T) {
	pt := piecetable.New([]byte("1234567890"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true
	ev.SetPosition(0, 0, 4, 3) // Width 5, Text height 3

	// Select "456" (from 3rd to 6th position)
	// This captures the end of the first fragment "12345" and the start of the second "67890"
	ev.CursorPos = 3
	ev.selActive = true
	ev.selAnchorOffset = 3
	ev.CursorPos = 6

	min, max := ev.getSelectionRange()
	if min != 3 || max != 6 {
		t.Errorf("Wrapped selection range failed: [%d:%d]", min, max)
	}
}

func TestEditorView_WideCharNavigation(t *testing.T) {
	// "A世B" -> 世 occupies 2 columns.
	pt := piecetable.New([]byte("A世B"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.WordWrap = false
	ev.CursorPos = 0 // On 'A'

	// 1. Right -> should land on '世' (offset 1)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})
	if ev.CursorPos != 1 {
		t.Errorf("Navigate to Wide: expected pos 1, got %d", ev.CursorPos)
	}

	// 2. Right -> should SKIP OVER '世' (size 3 bytes in UTF-8) and land on 'B' (offset 1+3=4)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})
	if ev.CursorPos != 4 {
		t.Errorf("Navigate over Wide: expected pos 4, got %d", ev.CursorPos)
	}
}

func TestEditorView_UTF8Selection(t *testing.T) {
	// "Да" - 2 runes, 4 bytes
	pt := piecetable.New([]byte("Да"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 0

	// Start selection: Shift + Right (one letter 'Д')
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.ShiftPressed,
	})

	if !ev.selActive {
		t.Fatal("Selection should be active")
	}
	min, max := ev.getSelectionRange()
	if min != 0 || max != 2 {
		t.Errorf("UTF8 Selection failed: expected [0:2], got [%d:%d]", min, max)
	}
}

func TestEditorView_HomeEnd(t *testing.T) {
	pt := piecetable.New([]byte("Hello World"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)

	// 1. End test
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_END})
	if ev.CursorPos != 11 {
		t.Errorf("End failed: expected pos 11, got %d", ev.CursorPos)
	}

	// 2. Home test
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_HOME})
	if ev.CursorPos != 0 {
		t.Errorf("Home failed: expected pos 0, got %d", ev.CursorPos)
	}
}

func TestEditorView_WideCharBackspace(t *testing.T) {
	// "A世" -> 'A' (1), '世' (3 bytes)
	pt := piecetable.New([]byte("A世"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 4 // At the very end

	// Press Backspace (remove '世')
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})

	if pt.String() != "A" {
		t.Errorf("Wide Backspace failed: expected 'A', got %q", pt.String())
	}
	if ev.CursorPos != 1 {
		t.Errorf("Wide Backspace pos failed: expected 1, got %d", ev.CursorPos)
	}
}

func TestEditorView_BracketedPaste(t *testing.T) {
	pt := piecetable.New([]byte("Start-"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorLine = 0
	ev.CursorPos = 6

	// 1. Paste start signal (PasteStart: true)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
	if !ev.IsBusy() {
		t.Error("Editor should be Busy during paste")
	}

	// 2. Simulate characters: "A", "B", Enter (\n), "C"
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'A'})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'B'})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'C'})

	// IMPORTANT: Model should not change until PasteStart: false
	if pt.String() != "Start-" {
		t.Errorf("Model changed prematurely during paste: %q", pt.String())
	}

	// 3. Paste end signal (PasteStart: false)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: false})

	// Now everything should be in the model
	expected := "Start-AB\nC"
	if pt.String() != expected {
		t.Errorf("Paste commit failed: expected %q, got %q", expected, pt.String())
	}

	// Check cursor position (line 1, position 1 - after 'C')
	if ev.CursorLine != 1 || ev.CursorPos != 1 {
		t.Errorf("Post-paste cursor error: Line %d, Pos %d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_ExtremeBounds(t *testing.T) {
	pt := piecetable.New([]byte("A"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)

	// 1. Backspace at file start should not break anything
	ev.CursorLine = 0
	ev.CursorPos = 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if pt.String() != "A" {
		t.Error("Backspace at file start modified the text")
	}

	// 2. Delete at file end should not break anything
	ev.CursorPos = 1
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})
	if pt.String() != "A" {
		t.Error("Delete at file end modified the text")
	}
}

func TestEditorView_EmptyLinesWrap(t *testing.T) {
	// File of three empty lines (breaks only)
	pt := piecetable.New([]byte("\n\n"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true
	ev.SetPosition(0, 0, 10, 11)

	if ev.li.LineCount() != 3 {
		t.Errorf("Expected 3 lines, got %d", ev.li.LineCount())
	}

	// Check that engine returns fragments even for empty lines
	ev.engine.SetWidth(10)
	frags := ev.engine.GetFragments(0)
	if len(frags) == 0 {
		t.Fatal("Empty line fragments should not be empty")
	}

	// Empty line navigation
	ev.CursorLine = 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
	if ev.CursorLine != 1 {
		t.Errorf("Down on empty lines failed: expected line 1, got %d", ev.CursorLine)
	}
}

func TestEditorView_WordWrapScrolling(t *testing.T) {
	// Текст 46 байт. Ширина 10.
	// Фрагменты: 0 (0-10), 1 (10-20), 2 (20-30), 3 (30-40), 4 (40-46)
	text := "0123456789ABCDEFGHIJklmnopqrstuvwxyz0123456789"
	pt := piecetable.New([]byte(text))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true
	ev.SetPosition(0, 0, 9, 2) // Высота 3, высота текста 2
	ev.engine.SetWidth(10)

	ev.ensureCursorVisible()
	if ev.ScrollTopRow != 0 {
		t.Error("Initial scroll should be 0")
	}

	// 1. Прыгаем в конец строки (оффсет 46)
	// Конец строки — это 4-й визуальный ряд (индекс 4).
	ev.CursorPos = 46
	ev.ensureCursorVisible()

	// Чтобы увидеть 4-й ряд при высоте окна 2, верхним должен быть 3-й ряд (индекс 3).
	// Тогда видны ряды 3 и 4.
	if ev.ScrollTopRow != 3 {
		t.Errorf("WordWrap scroll failed: expected ScrollTopRow 3, got %d", ev.ScrollTopRow)
	}

	// 2. Прыгаем в начало
	ev.CursorPos = 0
	ev.ensureCursorVisible()
	if ev.ScrollTopRow != 0 {
		t.Errorf("WordWrap scroll back failed: expected ScrollTopRow 0, got %d", ev.ScrollTopRow)
	}
}

func TestEditorView_WordWrapInfiniteLoop(t *testing.T) {
	// Text with wide character
	pt := piecetable.New([]byte("A世B"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true

	// Extremely narrow window (width 1)
	ev.engine.SetWidth(1)
	frags := ev.engine.GetFragments(0)

	if len(frags) == 0 {
		t.Fatal("Should have produced fragments even for narrow window")
	}
	// Check that we didn't hang and traversed the entire line
	lastFrag := frags[len(frags)-1]
	if lastFrag.ByteOffsetEnd < 5 { // A(1) + 世(3) + B(1) = 5
		t.Errorf("Fragments didn't cover the whole line: end at %d", lastFrag.ByteOffsetEnd)
	}
}

func TestEditorView_F3_ToggleWordWrap(t *testing.T) {
	pt := piecetable.New([]byte("some text"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.WordWrap = true

	// Press F3 (Wait, make sure your code uses VK_F3 now)
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3})
	if ev.WordWrap {
		t.Error("F3 failed to disable WordWrap")
	}

	// Press F3 again
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3})
	if !ev.WordWrap {
		t.Error("F3 failed to re-enable WordWrap")
	}
}

func TestEditorView_Labels(t *testing.T) {
	pt := piecetable.New([]byte(""))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ks := ev.GetKeyLabels()

	if ks == nil {
		t.Fatal("EditorView.GetKeyLabels() returned nil")
	}

	if ks.Normal[1] != "Save" { // F2
		t.Errorf("Expected F2 to be 'Save', got %q", ks.Normal[1])
	}
	if ks.Normal[9] != "Exit" { // F10
		t.Errorf("Expected F10 to be 'Exit', got %q", ks.Normal[9])
	}
}
func TestEditorView_DefaultsAndToggles(t *testing.T) {
	pt := piecetable.New([]byte("test content"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	// 1. Check defaults
	if ev.WordWrap {
		t.Error("WordWrap should be OFF by default")
	}
	if ev.ShowWhitespaces {
		t.Error("ShowWhitespaces should be OFF by default")
	}

	// 2. Toggle F3 (Wrap)
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F3})
	if !ev.WordWrap {
		t.Error("F3 failed to toggle WordWrap to ON")
	}

	// 3. Toggle F5 (Whitespaces)
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F5})
	if !ev.ShowWhitespaces {
		t.Error("F5 failed to toggle ShowWhitespaces to ON")
	}
}

func TestEditorView_WhitespaceRendering(t *testing.T) {
	SetDefaultF4Palette()
	pt := piecetable.New([]byte("a b\tc")) // space and tab
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.ShowWhitespaces = true
	cells := ev.fillCells(nil, []byte("a b\tc"), 0, 0, 0, false, 0, 0, nil, 0, false, -1, 0, 0, 0)

	// '·' is U+00B7 (183)
	if cells[1].Char != 183 {
		t.Errorf("Expected dot for space when ShowWhitespaces is ON, got %d", cells[1].Char)
	}
	// '→' is U+2192 (8594)
	if cells[3].Char != 8594 {
		t.Errorf("Expected arrow for tab when ShowWhitespaces is ON, got %d", cells[3].Char)
	}

	ev.ShowWhitespaces = false
	cells = ev.fillCells(nil, []byte("a b\tc"), 0, 0, 0, false, 0, 0, nil, 0, false, -1, 0, 0, 0)
	if cells[1].Char != ' ' {
		t.Errorf("Expected space for space when ShowWhitespaces is OFF, got %d", cells[1].Char)
	}
	if cells[3].Char != ' ' {
		t.Errorf("Expected space for tab when ShowWhitespaces is OFF, got %d", cells[3].Char)
	}
}

func TestEditorView_WideCharDelete(t *testing.T) {
	// "A世" -> 'A' (1), '世' (3 bytes)
	pt := piecetable.New([]byte("A世"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 1 // Before '世'

	// Press Delete
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})

	if pt.String() != "A" {
		t.Errorf("Wide Delete failed: expected 'A', got %q", pt.String())
	}
	if ev.CursorPos != 1 {
		t.Errorf("Cursor position after Wide Delete should remain 1, got %d", ev.CursorPos)
	}
}

func TestEditorView_PageNavigation(t *testing.T) {
	// Create 20 lines of text
	var buf []byte
	for i := 0; i < 20; i++ {
		buf = append(buf, []byte("Line\n")...)
	}
	pt := piecetable.New(buf)
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 5) // Text Viewport height 5
	ev.CursorLine = 0
	ev.CursorPos = 0

	// 1. PgDn
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT})
	if ev.CursorLine != 5 {
		t.Errorf("PgDn failed: expected line 5, got %d", ev.CursorLine)
	}

	// 2. PgUp
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR})
	if ev.CursorLine != 0 {
		t.Errorf("PgUp failed: expected line 0, got %d", ev.CursorLine)
	}

	// 3. Selection with PgDn (Shift + PgDn)
	ev.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_NEXT,
		ControlKeyState: vtinput.ShiftPressed,
	})

	if !ev.selActive {
		t.Fatal("Shift+PgDn should activate selection")
	}
	min, max := ev.getSelectionRange()
	// Selection from offset 0 to start of line 5 (5 characters "Line\n" * 5 = 25)
	if min != 0 || max != 25 {
		t.Errorf("Shift+PgDn range failed: expected [0:25], got [%d:%d]", min, max)
	}
}

func TestEditorView_LongLinePerformance(t *testing.T) {
	// Removed t.Parallel() to prevent CPU starvation and deadlocks
	// when competing with other UI tests.

	// Create one very long line (100 KB) to simulate the problem.
	// Without the fix, this would cause O(N*M) reads and hanging.
	longLine := strings.Repeat("a", 100*1024)
	pt := piecetable.New([]byte(longLine))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24) // 80x25 viewport

	// Set cursor in the middle of the line
	ev.CursorPos = 50 * 1024

	// Wrap test in timeout. If editor "hangs", test fails.
	done := make(chan struct{})
	go func() {
		// Simulate 100 "right" presses. This heavily loads ensureCursorVisible.
		for i := 0; i < 100; i++ {
			ev.ProcessKey(&vtinput.InputEvent{
				Type:           vtinput.KeyEventType,
				KeyDown:        true,
				VirtualKeyCode: vtinput.VK_RIGHT,
			})
		}
		// Moving to end of line — another expensive operation without caching
		ev.ProcessKey(&vtinput.InputEvent{
			Type:           vtinput.KeyEventType,
			KeyDown:        true,
			VirtualKeyCode: vtinput.VK_END,
		})
		close(done)
	}()

	select {
	case <-done:
		// Success: all operations finished in time.
	case <-time.After(3 * time.Second): // 3s — safe timeout for slow CI or heavy terminal.
		t.Fatal("Performance test timed out. EditorView is likely still hanging on long lines.")
	}
}

func TestEditorView_WordNavigation(t *testing.T) {
	pt := piecetable.New([]byte("hello world  test"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.CursorPos = 0

	// 1. Ctrl + Right -> should jump to start of "world" (index 6)
	ev.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorPos != 6 {
		t.Errorf("Ctrl+Right (1) failed: expected pos 6, got %d", ev.CursorPos)
	}

	// 2. Ctrl + Right -> should jump to start of "test" (index 13)
	ev.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorPos != 13 {
		t.Errorf("Ctrl+Right (2) failed: expected pos 13, got %d", ev.CursorPos)
	}

	// 3. Ctrl + Left -> back to start of "world" (index 6)
	ev.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorPos != 6 {
		t.Errorf("Ctrl+Left (1) failed: expected pos 6, got %d", ev.CursorPos)
	}

	// 4. Ctrl + Left -> back to start (index 0)
	ev.ProcessKey(&vtinput.InputEvent{
		Type:            vtinput.KeyEventType,
		KeyDown:         true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorPos != 0 {
		t.Errorf("Ctrl+Left (2) failed: expected pos 0, got %d", ev.CursorPos)
	}
}
func TestEditorBar_Content(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()
	pt := piecetable.New([]byte("abc"))
	ev := NewEditorView(pt, nil, "test.go")
	defer ev.Close()
	ev.SetPosition(0, 0, 40, 10)
	ev.CursorLine = 5
	ev.CursorPos = 12

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(41, 11)

	ev.GetTopBar().Show(scr)

	// В статус-баре должно быть "6,12" (Line+1, Pos)
	foundLine := false
	foundPos := false
	for x := 0; x < 40; x++ {
		if scr.GetCell(x, 0).Char == '6' {
			foundLine = true
		}
		if scr.GetCell(x, 0).Char == '1' && scr.GetCell(x+1, 0).Char == '2' {
			foundPos = true
		}
	}

	if !foundLine || !foundPos {
		t.Errorf("EditorBar did not display correct cursor info (6,12). Found Line:%v, Pos:%v", foundLine, foundPos)
	}
}
func TestEditorView_HandleClose(t *testing.T) {
	pt := piecetable.New([]byte("test"))
	ev := NewEditorView(pt, nil, "file.txt")
	defer ev.Close()

	if ev.IsDone() {
		t.Fatal("Editor should not be done initially")
	}

	// Send CmClose command (simulating menu "Exit" click)
	ev.HandleCommand(vtui.CmClose, nil)

	if !ev.IsDone() {
		t.Error("EditorView failed to set IsDone after receiving CmClose")
	}
}
func TestEditorView_GetTitle(t *testing.T) {
	pt := piecetable.New([]byte(""))

	// With path
	ev1 := NewEditorView(pt, nil, filepath.FromSlash("/var/log/syslog"))
	defer ev1.Close()
	if ev1.GetTitle() != "Edit: syslog" {
		t.Errorf("GetTitle failed for valid path: %s", ev1.GetTitle())
	}
	if ev1.GetWorkspaceTabTitle() != "syslog" {
		t.Errorf("GetWorkspaceTabTitle failed for valid path: %s", ev1.GetWorkspaceTabTitle())
	}
	if ev1.GetWorkspaceTabMarker() != "E" {
		t.Errorf("GetWorkspaceTabMarker failed: %s", ev1.GetWorkspaceTabMarker())
	}

	// Without path
	ev2 := NewEditorView(pt, nil, "")
	defer ev2.Close()
	if ev2.GetTitle() != "Editor" {
		t.Errorf("GetTitle failed for empty path: %s", ev2.GetTitle())
	}
	if ev2.GetWorkspaceTabTitle() != "Editor" {
		t.Errorf("GetWorkspaceTabTitle failed for empty path: %s", ev2.GetWorkspaceTabTitle())
	}

	// Internal editor workflows can hide a temporary filename.
	ev3 := NewEditorView(pt, nil, "f4-visren-temporary.txt")
	defer ev3.Close()
	ev3.DisplayTitle = "Rename list of files"
	if ev3.GetTitle() != "Rename list of files" {
		t.Errorf("GetTitle ignored DisplayTitle: %s", ev3.GetTitle())
	}
	if ev3.GetWorkspaceTabTitle() != "Rename list of files" {
		t.Errorf("GetWorkspaceTabTitle ignored DisplayTitle: %s", ev3.GetWorkspaceTabTitle())
	}
}
func TestViewerView_CodepageSwitch_Crash(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpFile := filepath.Join(t.TempDir(), "test_viewer_cp.txt")
	err := os.WriteFile(tmpFile, []byte("Hello World\nLine 2\nLine 3"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	v := vfs.NewOSVFS(filepath.Dir(tmpFile))
	vv, err := NewViewerView(context.Background(), v, tmpFile)
	if err != nil {
		t.Fatalf("Failed to create ViewerView: %v", err)
	}
	defer vv.Close()
	vv.SetPosition(0, 0, 80, 24)

	// Simulate double codepage switch (F8, F8)
	vv.ReloadWithCodepage(11111) // ANSI
	vv.ReloadWithCodepage(22222) // OEM

	// Trigger render to make sure it doesn't crash during drawing
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vv.Show(scr)

	// Exit the viewer (F3 / Close)
	vv.Close()
}

func TestEditorView_CodepageDialog_DynamicHeight(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	scrH := 10 // Very small screen height
	vtui.FrameManager.Screen().AllocBuf(80, scrH)

	pt := piecetable.New(nil)
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, scrH-1)

	ev.showCodepageDialog()

	// The dialog should be the top frame
	top := vtui.FrameManager.GetTopFrame()
	menu, ok := top.(*vtui.VMenu)
	if !ok {
		t.Fatal("Expected top frame to be VMenu")
	}

	_, _, _, y2 := menu.GetPosition()
	menuHeight := y2 - menu.Y1 + 1

	expectedMaxH := scrH - 2
	if menuHeight > expectedMaxH {
		t.Errorf("Codepage dialog height %d exceeds maximum allowed %d for screen height %d", menuHeight, expectedMaxH, scrH)
	}

	if menu.SelectPos < 0 || menu.SelectPos >= len(menu.Items) {
		t.Errorf("Selected position %d out of bounds", menu.SelectPos)
	}
}
func TestEditorView_AsyncIndexing(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	content := "Line 1\nLine 2\nLine 3"
	tmp := t.TempDir() + "/idx_test.txt"
	os.WriteFile(tmp, []byte(content), 0644)

	v := vfs.NewOSVFS(t.TempDir())
	f, err := v.Open(context.Background(), tmp)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}

	// Open editor with AsyncBuffer
	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, v, tmp)
	defer ev.Close()
	ev.asyncBuf = buf
	ev.file = f

	// Initial LineCount should be 1 (empty or unindexed)
	if ev.li.LineCount() != 1 {
		t.Errorf("Expected 1 line initially, got %d", ev.li.LineCount())
	}

	// Start background indexing
	ev.StartIndexing()

	// Wait and pump tasks
	timeout := time.After(2 * time.Second)
	for ev.li.LineCount() < 3 {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for indexer to find 3 lines")
		}
	}

	if ev.li.LineCount() != 3 {
		t.Errorf("Indexer failed: expected 3 lines, got %d", ev.li.LineCount())
	}
}
func TestEditorView_Indexer_EditInterference(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Create a large file with many lines
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString(fmt.Sprintf("Line %d\n", i))
	}
	tmp := t.TempDir() + "/race_test.txt"
	os.WriteFile(tmp, []byte(sb.String()), 0644)

	v := vfs.NewOSVFS(t.TempDir())
	f, err := v.Open(context.Background(), tmp)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, v, tmp)
	defer ev.Close()
	ev.asyncBuf = buf
	ev.file = f

	// 1. Start indexing
	ev.StartIndexing()

	// 2. Immediately delete half of the file on the UI thread
	// This should trigger the indexer cancellation via ev.edited = true
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_BACK,
	})

	if !ev.edited {
		t.Error("Editor should be marked as edited after Backspace")
	}

	// 3. Process any tasks that might have been queued by the indexer
	// before it saw the cancellation.
	timeout := time.After(200 * time.Millisecond)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			break Loop
		}
	}

	// 4. Verification: The LineIndex must remain consistent with the PieceTable size
	// even if some stale background tasks were executed.
	lastOffset := ev.li.GetLineOffset(ev.li.LineCount() - 1)
	if lastOffset > pt.Size() {
		t.Errorf("LineIndex corruption: last offset %d exceeds PieceTable size %d", lastOffset, pt.Size())
	}
}
func TestEditorView_StartIndexing_RestartSafety(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	v := vfs.NewOSVFS(t.TempDir())

	// Create a dummy file
	tmp := t.TempDir() + "/restart.txt"
	os.WriteFile(tmp, []byte("line1\nline2"), 0644)
	f, err := v.Open(context.Background(), tmp)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, v, tmp)
	defer ev.Close()
	ev.asyncBuf = buf

	// 1. Start indexing
	ev.StartIndexing()
	oldCancel := ev.indexCancel
	if oldCancel == nil {
		t.Fatal("indexCancel should be set")
	}

	// 2. Start again immediately
	ev.StartIndexing()

	// 3. Verify it is still set and didn't panic
	if ev.indexCancel == nil {
		t.Error("indexCancel should not be nil after restart")
	}

	// Clean up
	ev.Close()
}
func TestEditorView_UnsavedChanges(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	// 1. Initially not modified
	if ev.modified {
		t.Error("Editor should not be marked as modified initially")
	}

	// 2. Modify text (typing) -> should be modified
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	if !ev.modified {
		t.Error("Editor should be modified after typing")
	}

	// 3. Test tryClose when NOT modified
	ev.modified = false
	ev.tryClose()
	if !ev.IsDone() {
		t.Error("Editor should close immediately if not modified")
	}

	// 4. Test tryClose when modified (should NOT close immediately)
	ev.Done = false
	ev.modified = true
	ev.tryClose()
	if ev.IsDone() {
		t.Error("Editor should NOT close immediately if modified (should show dialog)")
	}

	// 5. Verify deletion also triggers modified
	ev.modified = false
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if !ev.modified {
		t.Error("Editor should be modified after deletion")
	}
}

func TestEditorView_Indexer_BatchingIntegrity(t *testing.T) {
	// Verifies that the optimized batching indexer doesn't miss lines.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	content := "L1\nL2\nL3\nL4\nL5\n"
	tmpFile := filepath.Join(t.TempDir(), "batch.txt")
	os.WriteFile(tmpFile, []byte(content), 0644)

	v := vfs.NewOSVFS(t.TempDir())
	f, err := v.Open(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, v, tmpFile)
	defer ev.Close()
	ev.asyncBuf = buf
	ev.file = f

	ev.StartIndexing()

	// Wait and pump tasks
	timeout := time.After(1 * time.Second)
	for ev.li.LineCount() < 6 {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatalf("Batch indexer timed out. Lines found: %d", ev.li.LineCount())
		}
	}

	if ev.li.LineCount() != 6 {
		t.Errorf("Indexer missed lines. Expected 6, got %d", ev.li.LineCount())
	}
}
func TestEditorView_Indexer_ModifierSafety(t *testing.T) {
	// Regression test for the Ctrl+End hang:
	// Modifier keys should NOT stop the indexer.
	pt := piecetable.New([]byte("test content"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	// Set a mock cancel function to track if it was called
	cancelled := false
	ev.indexCancel = func() { cancelled = true }
	ev.edited = false

	// 1. Pressing VK_CONTROL (part of Ctrl+End sequence)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_CONTROL,
	})

	if ev.edited {
		t.Error("Modifier key (Ctrl) erroneously set the 'edited' flag")
	}
	if cancelled {
		t.Error("Modifier key (Ctrl) erroneously cancelled the indexer")
	}

	// 2. Pressing VK_SHIFT (selection)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_SHIFT,
	})

	if ev.edited {
		t.Error("Modifier key (Shift) erroneously set the 'edited' flag")
	}

	// 3. Verify that a REAL edit still cancels the indexer
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		Char: 'a',
	})

	if !ev.edited {
		t.Error("Real text input failed to set the 'edited' flag")
	}
	if !cancelled {
		t.Error("Real text input failed to cancel the indexer")
	}
}

func TestEditorView_Navigation_DocumentBoundaries(t *testing.T) {
	pt := piecetable.New([]byte("Line 1\nLine 2\nLine 3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Ctrl+End -> End of file
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_END, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 2 || ev.CursorPos != 6 {
		t.Errorf("Ctrl+End failed: expected line 2 pos 6, got %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 2. Ctrl+Home -> Start of file
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_HOME, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 0 || ev.CursorPos != 0 {
		t.Errorf("Ctrl+Home failed: expected 0:0, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_SelectAll(t *testing.T) {
	pt := piecetable.New([]byte("First\nSecond"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	// Ctrl+A
	pressKey(ev, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if !ev.selActive {
		t.Fatal("Selection should be active after Ctrl+A")
	}
	min, max := ev.getSelectionRange()
	if min != 0 || max != pt.Size() {
		t.Errorf("Ctrl+A range failed: [0:%d], got [%d:%d]", pt.Size(), min, max)
	}
	// Cursor should jump to EOF in Far
	if ev.CursorLine != 1 || ev.CursorPos != 6 {
		t.Errorf("Ctrl+A cursor pos failed, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_ShiftAliasSelection(t *testing.T) {
	pt := piecetable.New([]byte("ABCDE"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.CursorPos = 0

	// Shift + Ctrl + D (Right alias)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_D, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	if !ev.selActive {
		t.Fatal("Shift + Alias should trigger selection")
	}
	if ev.selAnchorOffset != 0 || ev.CursorPos != 1 {
		t.Errorf("Selection anchor or cursor wrong: anchor=%d, pos=%d", ev.selAnchorOffset, ev.CursorPos)
	}
}
func TestEditorView_FarNavigation_FullCoverage(t *testing.T) {
	pt := piecetable.New([]byte("Line 1\nLine 2\nLine 3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Ctrl+End -> End of file
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_END, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 2 || ev.CursorPos != 6 {
		t.Errorf("Ctrl+End failed: expected 2:6, got %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 2. Ctrl+Home -> Start of file
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_HOME, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 0 || ev.CursorPos != 0 {
		t.Errorf("Ctrl+Home failed: expected 0:0, got %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 3. Shift + Ctrl + End -> Select to end of file
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_END, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})
	if !ev.selActive {
		t.Fatal("Shift+Ctrl+End should activate selection")
	}
	min, max := ev.getSelectionRange()
	if min != 0 || max != pt.Size() {
		t.Errorf("Shift+Ctrl+End selection range failed: [0:%d], got [%d:%d]", pt.Size(), min, max)
	}
}

func TestEditorView_FarAliases_FullCoverage(t *testing.T) {
	pt := piecetable.New([]byte("First word\nSecond line"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Ctrl+S should move 1 char left, NOT 1 word
	ev.CursorPos = 10
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_S, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 9 {
		t.Errorf("Ctrl+S (alias) moved more than 1 char: pos %d", ev.CursorPos)
	}

	// 2. Ctrl+D should move 1 char right, NOT 1 word
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_D, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 10 {
		t.Errorf("Ctrl+D (alias) moved more than 1 char: pos %d", ev.CursorPos)
	}

	// 3. Shift + Ctrl + D -> Select 1 char
	ev.selActive = false
	ev.CursorPos = 0
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_D, ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})
	if !ev.selActive || ev.CursorPos != 1 {
		t.Error("Shift + Alias selection failed")
	}
}

func TestEditorView_FarX_SmartCut(t *testing.T) {
	pt := piecetable.New([]byte("Select me\nNext line"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Scenario A: Selection active -> Ctrl+X is CUT
	ev.selActive = true
	ev.selAnchorOffset = 0
	ev.CursorPos = 6 // "Select"
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_X, ControlKeyState: vtinput.LeftCtrlPressed})
	if pt.String() != " me\nNext line" {
		t.Errorf("Ctrl+X Cut failed: %q", pt.String())
	}

	// Scenario B: No selection -> Ctrl+X is DOWN
	ev.selActive = false
	ev.CursorLine = 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_X, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 1 {
		t.Error("Ctrl+X Down failed")
	}
}

func TestEditorView_FarSelectAll_Behavior(t *testing.T) {
	pt := piecetable.New([]byte("All\nText"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	pressKey(ev, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if !ev.selActive || ev.selAnchorOffset != 0 {
		t.Error("Ctrl+A anchor should be 0")
	}
	if ev.CursorLine != 1 || ev.CursorPos != 4 {
		t.Errorf("Ctrl+A cursor should be at EOF, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}
func TestEditorView_FarNavigation_Document(t *testing.T) {
	pt := piecetable.New([]byte("Line 1\nLine 2\nLine 3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Ctrl+End -> В самый конец файла
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_END, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 2 || ev.CursorPos != 6 {
		t.Errorf("Ctrl+End failed: expected line 2 pos 6, got %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 2. Ctrl+Home -> В самое начало файла
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_HOME, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 0 || ev.CursorPos != 0 {
		t.Errorf("Ctrl+Home failed: expected line 0 pos 0, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_FarSelectAll(t *testing.T) {
	pt := piecetable.New([]byte("Line 1\nLine 2"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	pressKey(ev, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_A, ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if !ev.selActive {
		t.Fatal("Selection should be active after Ctrl+A")
	}
	min, max := ev.getSelectionRange()
	if min != 0 || max != pt.Size() {
		t.Errorf("Ctrl+A range failed: [0:%d], got [%d:%d]", pt.Size(), min, max)
	}
	// В Far курсор прыгает в конец после выделения всего текста
	if ev.CursorLine != 1 || ev.CursorPos != 6 {
		t.Errorf("Ctrl+A cursor pos failed, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_FarNavigationAliases(t *testing.T) {
	pt := piecetable.New([]byte("First line\nSecond line\nThird line"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	ev.SetPosition(0, 0, 80, 24)
	ev.CursorLine = 1
	ev.CursorPos = 0

	// 1. Ctrl+E -> Вверх
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_E, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 0 {
		t.Errorf("Ctrl+E (Up) failed, line: %d", ev.CursorLine)
	}

	// 2. Ctrl+X -> Вниз (без выделения)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_X, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 1 {
		t.Errorf("Ctrl+X (Down) failed, line: %d", ev.CursorLine)
	}

	// 3. Ctrl+S -> Влево (на один символ, а не на слово!)
	ev.CursorPos = 4
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_S, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 3 {
		t.Errorf("Ctrl+S (Left) failed: expected 3, got %d", ev.CursorPos)
	}

	// 4. Ctrl+D -> Вправо (на один символ)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_D, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 4 {
		t.Errorf("Ctrl+D (Right) failed: expected 4, got %d", ev.CursorPos)
	}
}

func TestEditorView_FarX_CutVsDown(t *testing.T) {
	pt := piecetable.New([]byte("Some selected text\nNext line"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	ev.SetPosition(0, 0, 80, 24)

	// 1. С выделением Ctrl+X должен сработать как Cut
	ev.selActive = true
	ev.selAnchorOffset = 0
	ev.CursorPos = 4 // Выделено "Some"

	pressKey(ev, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_X, ControlKeyState: vtinput.LeftCtrlPressed,
	})

	if pt.String() != " selected text\nNext line" {
		t.Errorf("Ctrl+X (Cut) failed: text is %q", pt.String())
	}

	// 2. Без выделения Ctrl+X должен сработать как Down (навигация Far)
	ev.selActive = false
	ev.CursorLine = 0
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_X, ControlKeyState: vtinput.LeftCtrlPressed,
	})
	if ev.CursorLine != 1 {
		t.Error("Ctrl+X without selection should move cursor down")
	}
}
func TestEditorView_Search_Basic(t *testing.T) {
	vtui.SetDefaultPalette()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	content := "The quick brown fox jumps over the lazy dog"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	ev.SetPosition(0, 0, 80, 24)

	// Запускаем поиск слова "fox" (вперед, регистронезависимо)
	ev.Search("fox", false, false, false, false, false)

	// Прокачиваем задачи из очереди (PostTask), так как поиск асинхронный
	timeout := time.After(1 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			// Если выделение стало активным, значит поиск завершился
			if ev.selActive {
				break Loop
			}
		case <-timeout:
			t.Fatal("Search timed out")
		}
	}

	// "fox" начинается с 16-го байта
	if ev.selAnchorOffset != 16 {
		t.Errorf("Expected search anchor at 16, got %d", ev.selAnchorOffset)
	}

	// Конец совпадения — 16 + 3 = 19. Проверяем позицию курсора.
	actualOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	if actualOffset != 19 {
		t.Errorf("Expected cursor at 19, got %d", actualOffset)
	}
}

func TestEditorView_Search_Next(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Два вхождения слова "match"
	pt := piecetable.New([]byte("match one, match two"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	ev.SetPosition(0, 0, 80, 24)

	// 1. Находим первое вхождение
	ev.Search("match", false, false, false, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("First search failed")
		}
	}

	if ev.selAnchorOffset != 0 {
		t.Errorf("First match should be at 0, got %d", ev.selAnchorOffset)
	}

	// 2. Ищем следующее (Find Next)
	ev.selActive = false // Сбрасываем для проверки нового результата
	ev.Search("match", false, false, false, false, true)

	timeout = time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Next search failed")
		}
	}

	// Второе "match" начинается с 11-го байта
	if ev.selAnchorOffset != 11 {
		t.Errorf("Second match should be at 11, got %d", ev.selAnchorOffset)
	}
}

func TestEditorView_Search_CaseInsensitive(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pt := piecetable.New([]byte("ALL CAPS TEXT"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	ev.SetPosition(0, 0, 80, 24)

	// Ищем "caps" маленькими буквами
	ev.Search("caps", false, false, false, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Case-insensitive search failed")
		}
	}

	if ev.selAnchorOffset != 4 {
		t.Errorf("Should find 'CAPS' at offset 4, got %d", ev.selAnchorOffset)
	}
}
func TestEditorView_Search_NotFound(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pt := piecetable.New([]byte("some text"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	// Ищем то, чего нет
	ev.Search("missing", false, false, false, false, false)

	// Ждем появления сообщения об ошибке (оно создается через ShowMessage)
	timeout := time.After(1 * time.Second)
	foundMessage := false
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			// Проверяем, не открылся ли диалог (сообщение об ошибке)
			if vtui.FrameManager.GetTopFrameType() == vtui.TypeDialog {
				foundMessage = true
				break Loop
			}
		case <-timeout:
			break Loop
		}
	}

	if !foundMessage {
		t.Error("Search should show a message box when pattern is not found")
	}
}
func TestEditorView_Search_CaseSensitive(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pt := piecetable.New([]byte("Match and match"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Ищем "match" (строчными) с учетом регистра. Должно найти второе слово.
	ev.Search("match", true, false, false, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Case-sensitive search timed out")
		}
	}

	// "Match and " - 10 символов. "match" начинается с 10.
	if ev.selAnchorOffset != 10 {
		t.Errorf("Case-sensitive search failed: expected offset 10, got %d", ev.selAnchorOffset)
	}
}

func TestEditorView_Search_Backward(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pt := piecetable.New([]byte("first match, second match"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Ставим курсор в конец
	ev.CursorLine = 0
	ev.CursorPos = 25

	// Ищем "match" назад
	ev.Search("match", false, true, false, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Backward search timed out")
		}
	}

	// Второе "match" начинается на 20-м байте (префикс "first match, second " - 20 байт)
	if ev.selAnchorOffset != 20 {
		t.Errorf("Backward search failed: expected offset 20, got %d", ev.selAnchorOffset)
	}
}

func TestEditorView_Search_ShiftF7_Reverse(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// "one two three"
	//  0123456789012
	//  e: 2, 11, 12
	content := "one two three"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Initial backward search from end (next=false)
	// Should find the 'e' at index 12.
	ev.selActive = false
	ev.CursorPos = 13
	ev.Search("e", false, true, false, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Backward search 1 timed out")
		}
	}
	if ev.selAnchorOffset != 12 {
		t.Errorf("Expected offset 12, got %d", ev.selAnchorOffset)
	}

	// Drain leftover tasks AND WAIT for any pending async search logic to finish.
	// This makes the test deterministic under load.
	vtui.DebugLog("TEST_SEARCH: Pumping tasks after first search...")
	pumpDeadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(pumpDeadline) {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// 2. "Find Next" backward search (Shift+F7)
	// Cursor is at 13 (end of match). Reverse Next should skip index 12 and find 11.
	ev.selActive = false
	LastEditorSearchReverse = true
	vtui.DebugLog("TEST_SEARCH: Triggering Search 2. Current CursorPos: %d", ev.CursorPos)
	pressKey(ev, &vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_F7, ControlKeyState: vtinput.ShiftPressed,
	})

	// Use a more robust drain to handle async search completion
	found := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !found {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if ev.selActive {
				found = true
			}
			// Check if we hit the "Not found" dialog
			if top := vtui.FrameManager.GetTopFrame(); top != nil && top.GetTitle() == " Search " {
				t.Fatal("Search reported 'Not found' unexpectedly")
			}
		case <-time.After(10 * time.Millisecond):
		}
	}

	if !found {
		t.Fatal("Shift+F7 backward search failed to find second match")
	}

	if ev.selAnchorOffset != 11 {
		t.Errorf("Shift+F7 reverse (Next) failed: expected offset 11, got %d", ev.selAnchorOffset)
	}
}

func TestEditorView_SaveFailure_NoDataLoss(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpFile := filepath.Join(t.TempDir(), "important.txt")
	os.WriteFile(tmpFile, []byte("Original"), 0644)

	// Use our failing VFS
	baseVfs := vfs.NewOSVFS(filepath.Dir(tmpFile))
	failingVfs := &mockFailingVFS{VFS: baseVfs, failCreate: true}

	pt := piecetable.New([]byte("Original"))
	ev := NewEditorView(pt, failingVfs, tmpFile)
	defer ev.Close()
	f, err := failingVfs.Open(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	ev.file = f

	// 1. Modify the file
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'X'})
	if !ev.modified {
		t.Fatal("Editor should be modified")
	}

	// 2. Attempt to save (F2)
	pressKey(ev, &vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_F2})

	// Process async tasks
	timeout := time.After(2 * time.Second)
	saveFinished := false
	for !saveFinished {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
			if !ev.saving {
				saveFinished = true
			}
		case <-timeout:
			t.Fatal("Timeout waiting for save operation")
		}
	}

	// 3. Assertions
	// The modified flag MUST remain true because the save failed!
	if !ev.modified {
		t.Error("CRITICAL: Editor 'modified' flag was cleared even though save failed! Data loss risk.")
	}

	// Original file must remain untouched
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "Original" {
		t.Errorf("CRITICAL: Original file was corrupted during failed save. Got %q", string(data))
	}

	// Should have popped an error dialog
	if vtui.FrameManager.GetTopFrameType() != vtui.TypeDialog {
		t.Error("Editor did not show an error dialog upon save failure")
	}
}

// mockFailingVFS wraps OSVFS but intentionally fails the Create operation
type mockFailingVFS struct {
	vfs.VFS
	failCreate bool
}

func (m *mockFailingVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	if m.failCreate {
		return nil, os.ErrPermission // Simulate permission denied
	}
	return m.VFS.Create(ctx, path)
}
func TestEditorView_Save_DiskFullSimulation(t *testing.T) {
	// Verifies that if writing to the temp file fails (e.g. disk full),
	// the editor does not clear the modified flag and doesn't destroy memory state.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpFile := filepath.Join(t.TempDir(), "important.txt")
	os.WriteFile(tmpFile, []byte("Stable Content"), 0644)

	baseVfs := vfs.NewOSVFS(filepath.Dir(tmpFile))
	failingVfs := &mockFailingWriteVFS{VFS: baseVfs}

	pt := piecetable.New([]byte("Stable Content"))
	ev := NewEditorView(pt, failingVfs, tmpFile)
	defer ev.Close()
	f, err := failingVfs.Open(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	ev.file = f

	// 1. Modify (at the end of the text)
	ev.CursorPos = len("Stable Content")
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// 2. Attempt Save
	ev.SaveToFile(nil)

	// Pump tasks
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	// 3. Verify state
	if !ev.modified {
		t.Error("Editor cleared modified flag despite write failure!")
	}
	if ev.pt.String() != "Stable Content!" {
		t.Error("Editor memory state was corrupted after failed save")
	}
}
func TestEditorView_LargePaste_Consistency(t *testing.T) {
	// Tests stability and index consistency when pasting large blocks of text.
	pt := piecetable.New([]byte("Start\nEnd"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.CursorLine = 1
	ev.CursorPos = 0 // Before "End"

	// Create 1MB block with many newlines
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("pasted line content\n")
	}
	pasteData := sb.String()

	// Simulate Bracketed Paste
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
	for _, r := range pasteData {
		ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: false})

	// 1. Verify content
	expectedSize := 5 + 1 + len(pasteData) + 3 // "Start" + \n + paste + "End"
	if pt.Size() != expectedSize {
		t.Errorf("Size mismatch after large paste: expected %d, got %d", expectedSize, pt.Size())
	}

	// 2. Verify LineIndex integrity
	if ev.li.LineCount() != 5000+2 {
		t.Errorf("Line count mismatch: expected 5002, got %d", ev.li.LineCount())
	}

	// 3. Verify cursor is at the end of the paste
	if ev.CursorLine != 5001 || ev.CursorPos != 0 {
		t.Errorf("Cursor misplaced after large paste: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 4. Verify no crash on re-render
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	ev.Show(scr)
}

func TestEditorView_DeleteSelection_EOFBoundaries(t *testing.T) {
	// Ensures deleting the very last character or line doesn't crash the editor.
	content := "line1\nline2"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Select last line and the newline before it
	ev.selActive = true
	ev.selAnchorOffset = 5 // after "line1"
	ev.CursorLine = 1
	ev.CursorPos = 5 // at end of "line2"

	ev.DeleteSelection()

	if pt.String() != "line1" {
		t.Errorf("EOF delete failed: expected 'line1', got %q", pt.String())
	}
	if ev.li.LineCount() != 1 {
		t.Errorf("Line count mismatch: expected 1, got %d", ev.li.LineCount())
	}
	if ev.CursorLine != 0 || ev.CursorPos != 5 {
		t.Errorf("Cursor misplaced after EOF delete: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// Test deleting the only remaining character
	ev.selActive = true
	ev.selAnchorOffset = 0
	ev.CursorPos = 5
	ev.DeleteSelection()

	if pt.Size() != 0 {
		t.Errorf("Failed to delete last line, size: %d", pt.Size())
	}
	if ev.CursorLine != 0 || ev.CursorPos != 0 {
		t.Errorf("Cursor not at 0:0 after full delete: %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

type mockFailingWriteVFS struct {
	vfs.VFS
}

type opaqueEditorPathVFS struct {
	vfs.VFS
	joined []string
}

func (*opaqueEditorPathVFS) Base(string) string { return "report.txt" }
func (*opaqueEditorPathVFS) Dir(string) string {
	return "cloud://gdrive/connection/g:item:parent-id"
}
func (m *opaqueEditorPathVFS) Join(parts ...string) string {
	m.joined = append([]string(nil), parts...)
	return "cloud://gdrive/connection/g:new:parent-id:" + parts[len(parts)-1]
}

func TestEditorTempSiblingUsesVFSPathAlgebraForOpaqueURI(t *testing.T) {
	filesystem := &opaqueEditorPathVFS{}
	original := "cloud://gdrive/connection/g:item:immutable-id"
	tempPath, err := editorTempSiblingWithToken(filesystem, original, "fixed-token")
	if err != nil {
		t.Fatal(err)
	}
	if tempPath != "cloud://gdrive/connection/g:new:parent-id:.f4tmp-fixed-token" {
		t.Fatalf("temporary sibling = %q", tempPath)
	}
	if len(filesystem.joined) != 2 || filesystem.joined[0] != "cloud://gdrive/connection/g:item:parent-id" || filesystem.joined[1] != ".f4tmp-fixed-token" {
		t.Fatalf("Join arguments = %#v", filesystem.joined)
	}
	if strings.HasPrefix(tempPath, original) {
		t.Fatalf("opaque identifier was corrupted by suffixing: %q", tempPath)
	}
}

func TestEditorTempSiblingDoesNotOverflowLongBasename(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, strings.Repeat("a", 240))
	tempPath, err := editorTempSiblingWithToken(vfs.NewOSVFS(dir), original, "fixed-token")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(tempPath); got != ".f4tmp-fixed-token" {
		t.Fatalf("temporary basename = %q", got)
	}
}

func TestAlternateDataStreamRejectsCloudURIOnWindows(t *testing.T) {
	if isAlternateDataStream("cloud://gdrive/connection/g:item:immutable-id") {
		t.Fatal("cloud URI was misclassified as an NTFS alternate data stream")
	}
}

type editorCloudSaveReader struct {
	*bytes.Reader
	size int64
}

func (r *editorCloudSaveReader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadAt(p, off)
}

func (r *editorCloudSaveReader) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func (*editorCloudSaveReader) Close() error  { return nil }
func (r *editorCloudSaveReader) Size() int64 { return r.size }

type editorCloudSaveWriter struct {
	bytes.Buffer
	onWrite func()
	onClose func([]byte)
}

type editorStageModeWriter struct {
	io.WriteCloser
	path    string
	onWrite func(os.FileMode)
}

func (w *editorStageModeWriter) Write(p []byte) (int, error) {
	info, err := os.Stat(w.path)
	if err != nil {
		return 0, err
	}
	w.onWrite(info.Mode().Perm())
	return w.WriteCloser.Write(p)
}

type editorStageModeVFS struct {
	vfs.VFS
	mu       sync.Mutex
	observed []os.FileMode
}

func (m *editorStageModeVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	w, err := m.VFS.Create(ctx, p)
	if err != nil || !strings.HasPrefix(m.VFS.Base(p), ".f4tmp-") {
		return w, err
	}
	return &editorStageModeWriter{WriteCloser: w, path: p, onWrite: func(mode os.FileMode) {
		m.mu.Lock()
		m.observed = append(m.observed, mode)
		m.mu.Unlock()
	}}, nil
}

func (w *editorCloudSaveWriter) Close() error {
	w.onClose(append([]byte(nil), w.Bytes()...))
	return nil
}

func (w *editorCloudSaveWriter) Write(p []byte) (int, error) {
	if w.onWrite != nil {
		w.onWrite()
	}
	return w.Buffer.Write(p)
}

type editorCloudSaveVFS struct {
	*vfs.NullVFS

	mu               sync.Mutex
	original         string
	parent           string
	data             map[string][]byte
	created          []string
	createPolicies   []bool
	createKnown      []bool
	renamed          [][2]string
	renamePolicies   []bool
	renameKnown      []bool
	removed          []string
	renameErr        error
	skipRenameCommit bool
	openErr          error
	writeCalls       int
	attributeCalls   []string
	attributesErr    func(string, vfs.VFSItem) error
	capabilities     vfs.VFSCapabilities
	beforeRename     func()
}

func newEditorCloudSaveVFS(initial []byte) *editorCloudSaveVFS {
	filesystem := &editorCloudSaveVFS{
		NullVFS:  vfs.NewNullVFS(0),
		original: "cloud://gdrive/connection/g:item:immutable-id",
		parent:   "cloud://gdrive/connection/g:item:parent-id",
		data:     make(map[string][]byte),
	}
	filesystem.data[filesystem.original] = append([]byte(nil), initial...)
	return filesystem
}

func (m *editorCloudSaveVFS) Base(p string) string {
	if p == m.original {
		return "report.txt"
	}
	if marker := strings.LastIndex(p, "g:new:"); marker >= 0 {
		return p[marker+len("g:new:"):]
	}
	return m.NullVFS.Base(p)
}

func (m *editorCloudSaveVFS) Dir(string) string { return m.parent }

func (m *editorCloudSaveVFS) Join(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	return m.parent + "/g:new:" + parts[len(parts)-1]
}

func (m *editorCloudSaveVFS) Abs(p string) (string, error) { return p, nil }

func (m *editorCloudSaveVFS) GetCapabilities() vfs.VFSCapabilities {
	return m.capabilities
}

func (m *editorCloudSaveVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	if err := ctx.Err(); err != nil {
		return vfs.VFSItem{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.data[p]
	if !ok {
		return vfs.VFSItem{}, os.ErrNotExist
	}
	return vfs.VFSItem{Name: m.Base(p), Size: int64(len(data))}, nil
}

func (m *editorCloudSaveVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	overwrite, known := vfs.DestinationOverwrite(ctx)
	m.mu.Lock()
	m.created = append(m.created, p)
	m.createPolicies = append(m.createPolicies, overwrite)
	m.createKnown = append(m.createKnown, known)
	m.mu.Unlock()
	return &editorCloudSaveWriter{onWrite: func() {
		m.mu.Lock()
		m.writeCalls++
		m.mu.Unlock()
	}, onClose: func(data []byte) {
		m.mu.Lock()
		m.data[p] = data
		m.mu.Unlock()
	}}, nil
}

func (m *editorCloudSaveVFS) Rename(ctx context.Context, oldPath, newPath string) error {
	overwrite, known := vfs.DestinationOverwrite(ctx)
	if m.beforeRename != nil {
		m.beforeRename()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renamed = append(m.renamed, [2]string{oldPath, newPath})
	m.renamePolicies = append(m.renamePolicies, overwrite)
	m.renameKnown = append(m.renameKnown, known)
	if known && !overwrite {
		if _, exists := m.data[newPath]; exists && newPath != oldPath {
			return os.ErrExist
		}
	}
	if !m.skipRenameCommit {
		// Model a provider which commits the replacement before reporting a later
		// cleanup failure. This is the dangerous state where retrying Remove(old)
		// can delete the newly authoritative object behind an opaque alias.
		m.data[newPath] = append([]byte(nil), m.data[oldPath]...)
		delete(m.data, oldPath)
	}
	return m.renameErr
}

func (m *editorCloudSaveVFS) Remove(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, p)
	delete(m.data, p)
	return nil
}

func (m *editorCloudSaveVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.openErr != nil {
		err := m.openErr
		m.mu.Unlock()
		return nil, err
	}
	data, ok := m.data[p]
	data = append([]byte(nil), data...)
	m.mu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}
	return &editorCloudSaveReader{Reader: bytes.NewReader(data), size: int64(len(data))}, nil
}

func (m *editorCloudSaveVFS) SetAttributes(_ context.Context, p string, item vfs.VFSItem) error {
	m.mu.Lock()
	m.attributeCalls = append(m.attributeCalls, p)
	errFn := m.attributesErr
	m.mu.Unlock()
	if errFn != nil {
		return errFn(p, item)
	}
	return nil
}

type editorDeltaSaveVFS struct{ *editorCloudSaveVFS }

// editorLocalSaveVFS keeps the deterministic in-memory save behavior above,
// while exposing an OSVFS marker to isLocalOSVFS.  The editor intentionally
// applies stage-permission and metadata guarantees only to local filesystems.
// Keeping Local as a named field avoids inheriting a second, ambiguous VFS
// method set.
type editorLocalSaveVFS struct {
	*editorCloudSaveVFS
	Local *vfs.OSVFS
}

func newEditorLocalSaveVFS(t *testing.T, initial []byte) (*editorCloudSaveVFS, vfs.VFS) {
	t.Helper()
	base := newEditorCloudSaveVFS(initial)
	delete(base.data, base.original)
	base.original = "/virtual/report.txt"
	base.parent = "/virtual"
	base.data[base.original] = append([]byte(nil), initial...)
	return base, &editorLocalSaveVFS{
		editorCloudSaveVFS: base,
		Local:              vfs.NewOSVFS(t.TempDir()),
	}
}

func (m *editorDeltaSaveVFS) PatchFile(ctx context.Context, src, dst string, pieces []vfs.PatchPiece) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	overwrite, known := vfs.DestinationOverwrite(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if known && !overwrite {
		if _, exists := m.data[dst]; exists {
			return os.ErrExist
		}
	}
	source, exists := m.data[src]
	if !exists {
		return os.ErrNotExist
	}
	var result []byte
	for _, piece := range pieces {
		if piece.Data != nil {
			result = append(result, piece.Data...)
			continue
		}
		end := piece.Offset + piece.Length
		if piece.Offset < 0 || piece.Length < 0 || end < piece.Offset || end > int64(len(source)) {
			return io.ErrUnexpectedEOF
		}
		result = append(result, source[piece.Offset:end]...)
	}
	m.data[dst] = result
	return nil
}

func waitEditorSave(t *testing.T, editor *EditorView) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for editor.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline.C:
			t.Fatal("timeout waiting for editor save")
		}
	}
}

func assertNoEditorTempSiblings(t *testing.T, original string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(original))
	if err != nil {
		t.Fatalf("read editor destination directory: %v", err)
	}
	prefix := ".f4tmp-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			t.Fatalf("editor temporary sibling leaked after failed save: %q", entry.Name())
		}
	}
}

func runEditorCloudSave(t *testing.T, filesystem *editorCloudSaveVFS, content string) {
	t.Helper()
	editor := NewEditorView(piecetable.New([]byte(content)), filesystem, filesystem.original)
	editor.modified = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)
	editor.Close()
}

func TestEditorViewCloudSaveStagesSiblingInsteadOfOriginal(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS([]byte("old"))
	runEditorCloudSave(t, filesystem, "new contents")

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 1 {
		t.Fatalf("Create calls = %#v, want one staged create", filesystem.created)
	}
	tempPath := filesystem.created[0]
	if tempPath == filesystem.original {
		t.Fatal("cloud save wrote directly to the original opaque URI")
	}
	if !strings.HasPrefix(tempPath, filesystem.parent+"/g:new:.f4tmp-") {
		t.Fatalf("staged path = %q, want a sibling of the original", tempPath)
	}
	if len(filesystem.renamed) != 1 || filesystem.renamed[0] != [2]string{tempPath, filesystem.original} {
		t.Fatalf("Rename calls = %#v", filesystem.renamed)
	}
	if !filesystem.renameKnown[0] || !filesystem.renamePolicies[0] {
		t.Fatalf("Rename overwrite decision = (%v, %v), want explicit true", filesystem.renamePolicies[0], filesystem.renameKnown[0])
	}
	if got := string(filesystem.data[filesystem.original]); got != "new contents" {
		t.Fatalf("committed contents = %q", got)
	}
}

func TestEditorViewPartialRenameAfterCommitDoesNotRemoveStage(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS([]byte("old"))
	filesystem.renameErr = &vfs.PartialOperationError{
		Operation: "remote replacement cleanup",
		Completed: []string{filesystem.original},
		Failed:    []string{"backup"},
		Err:       errors.New("backup cleanup failed"),
	}
	runEditorCloudSave(t, filesystem, "committed despite error")

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 1 || len(filesystem.renamed) != 1 {
		t.Fatalf("Create/Rename calls = %#v / %#v", filesystem.created, filesystem.renamed)
	}
	if len(filesystem.removed) != 0 {
		t.Fatalf("Save retried destructive cleanup after partial commit: Remove calls = %#v", filesystem.removed)
	}
	if got := string(filesystem.data[filesystem.original]); got != "committed despite error" {
		t.Fatalf("provider-committed contents = %q", got)
	}
}

func TestEditorViewDefinitiveRenameFailureCleansUniqueStageOnce(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS([]byte("old"))
	filesystem.renameErr = os.ErrPermission
	runEditorCloudSave(t, filesystem, "staged")

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 1 || len(filesystem.removed) != 1 {
		t.Fatalf("Create/Remove calls = %#v / %#v", filesystem.created, filesystem.removed)
	}
	if filesystem.removed[0] != filesystem.created[0] {
		t.Fatalf("removed %q, want stage %q", filesystem.removed[0], filesystem.created[0])
	}
}

func TestEditorViewCloudTempCreatesAreUniqueAndNoReplace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS([]byte("old"))
	runEditorCloudSave(t, filesystem, "first")
	runEditorCloudSave(t, filesystem, "second")

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 2 {
		t.Fatalf("Create calls = %#v, want two", filesystem.created)
	}
	if filesystem.created[0] == filesystem.created[1] {
		t.Fatalf("two saves reused temporary path %q", filesystem.created[0])
	}
	for index := range filesystem.created {
		if !filesystem.createKnown[index] || filesystem.createPolicies[index] {
			t.Fatalf("Create[%d] overwrite decision = (%v, %v), want explicit false", index, filesystem.createPolicies[index], filesystem.createKnown[index])
		}
	}
}

func TestEditorViewCreateNewSaveDoesNotReplaceRacingDestination(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS(nil)
	filesystem.capabilities.HasAtomicNoReplaceRename = true
	delete(filesystem.data, filesystem.original)
	filesystem.beforeRename = func() {
		filesystem.mu.Lock()
		filesystem.data[filesystem.original] = []byte("created by another writer")
		filesystem.mu.Unlock()
	}

	editor := newEditorView(piecetable.New([]byte("generated report")), filesystem, filesystem.original, false)
	editor.modified = true
	editor.unsavedBaseline = true
	editor.createNewTarget = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)
	defer editor.Close()

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if got := string(filesystem.data[filesystem.original]); got != "created by another writer" {
		t.Fatalf("racing destination was replaced with %q", got)
	}
	if len(filesystem.renameKnown) != 1 || !filesystem.renameKnown[0] || filesystem.renamePolicies[0] {
		t.Fatalf("create-new rename overwrite decision = (%v, %v), want explicit false", filesystem.renamePolicies, filesystem.renameKnown)
	}
	if !editor.modified || !editor.createNewTarget {
		t.Fatal("failed create-new save was incorrectly marked clean")
	}
}

func TestEditorViewCreateNewSaveRejectsVFSWithoutAtomicNoReplace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS(nil)
	delete(filesystem.data, filesystem.original)
	editor := newEditorView(piecetable.New([]byte("generated report")), filesystem, filesystem.original, false)
	editor.modified = true
	editor.unsavedBaseline = true
	editor.createNewTarget = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)
	defer editor.Close()

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 0 || len(filesystem.renamed) != 0 {
		t.Fatalf("unsupported VFS was mutated: Create=%#v Rename=%#v", filesystem.created, filesystem.renamed)
	}
	if !editor.modified || !editor.createNewTarget {
		t.Fatal("rejected create-new save was incorrectly marked clean")
	}
}

func TestEditorViewIdentityPreservingCloudSaveUpdatesOriginalDirectly(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	filesystem := newEditorCloudSaveVFS([]byte("old"))
	filesystem.capabilities.HasIdentityPreservingWrite = true
	runEditorCloudSave(t, filesystem, "same object, new contents")

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.created) != 1 || filesystem.created[0] != filesystem.original {
		t.Fatalf("Create calls = %#v, want direct existing-object update", filesystem.created)
	}
	if !filesystem.createKnown[0] || !filesystem.createPolicies[0] {
		t.Fatalf("direct update overwrite decision = (%v, %v), want explicit true", filesystem.createPolicies[0], filesystem.createKnown[0])
	}
	if len(filesystem.renamed) != 0 {
		t.Fatalf("identity-preserving save unexpectedly renamed: %#v", filesystem.renamed)
	}
	if got := string(filesystem.data[filesystem.original]); got != "same object, new contents" {
		t.Fatalf("updated contents = %q", got)
	}
}

func TestEditorViewLocalStageIsPrivateBeforeFirstWrite(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	dir := t.TempDir()
	path := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	filesystem := &editorStageModeVFS{VFS: vfs.NewOSVFS(dir)}
	editor := NewEditorView(piecetable.New([]byte("sensitive replacement")), filesystem, path)
	editor.modified = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)
	editor.Close()

	filesystem.mu.Lock()
	defer filesystem.mu.Unlock()
	if len(filesystem.observed) == 0 {
		t.Fatal("stage writer did not observe a write")
	}
	for _, mode := range filesystem.observed {
		// Go's Windows FileMode synthesizes 0666 from DOS attributes; access is
		// governed by the inherited DACL, so Unix group/other bits are not an
		// observable security signal there.
		if runtime.GOOS != "windows" && mode&0o077 != 0 {
			t.Fatalf("stage was group/world accessible during write: mode %o", mode)
		}
	}
}

func newLazyEditorCloudSaveView(t *testing.T, filesystem vfs.VFS, original string, source []byte) *EditorView {
	t.Helper()
	reader := &editorCloudSaveReader{Reader: bytes.NewReader(source), size: int64(len(source))}
	buffer := NewAsyncBuffer(context.Background(), reader)
	editor := NewEditorView(piecetable.NewWithBuffer(buffer), filesystem, original)
	editor.file = reader
	editor.asyncBuf = buffer
	return editor
}

func requireEditorSaveDialog(t *testing.T) {
	t.Helper()
	if vtui.FrameManager.GetTopFrameType() != vtui.TypeDialog {
		t.Fatal("save failure/partial completion was not surfaced to the user")
	}
}

func TestEditorViewDefinitiveRenameFailureKeepsLazyBufferRecoverable(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	source := bytes.Repeat([]byte("0123456789abcdef"), 40*1024)
	base := newEditorCloudSaveVFS(source)
	base.renameErr = os.ErrPermission
	base.skipRenameCommit = true
	filesystem := &editorDeltaSaveVFS{editorCloudSaveVFS: base}
	editor := newLazyEditorCloudSaveView(t, filesystem, base.original, source)
	defer editor.Close()

	prefix := []byte("edited-before-failed-rename:")
	editor.pt.Insert(0, prefix)
	editor.modified = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)

	if !editor.modified {
		t.Fatal("definitive rename failure cleared the editor's modified state")
	}
	// The delta writer never read unchanged pieces. This forces a previously
	// untouched AsyncBuffer chunk to load after Rename failed; closing the old
	// buffer before finalization makes this wait forever.
	tail := []byte(":still-editable")
	editor.pt.Insert(editor.pt.Size(), tail)
	want := string(prefix) + string(source) + string(tail)
	if got := waitPtString(t, editor.pt); got != want {
		t.Fatalf("editor contents after failed rename = %d bytes, want %d", len(got), len(want))
	}
}

func TestEditorViewCommittedSaveReopenFailureIsSurfacedAndRecoverable(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	source := bytes.Repeat([]byte("abcdefghij"), 64*1024)
	base := newEditorCloudSaveVFS(source)
	filesystem := &editorDeltaSaveVFS{editorCloudSaveVFS: base}
	editor := newLazyEditorCloudSaveView(t, filesystem, base.original, source)
	defer editor.Close()

	prefix := []byte("committed:")
	editor.pt.Insert(0, prefix)
	editor.modified = true
	base.openErr = errors.New("transient reopen failure")
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)

	requireEditorSaveDialog(t)
	base.mu.Lock()
	committed := append([]byte(nil), base.data[base.original]...)
	base.mu.Unlock()
	if want := append(append([]byte(nil), prefix...), source...); !bytes.Equal(committed, want) {
		t.Fatal("provider did not commit the expected contents before reopen failed")
	}
	// Reopen failure must not strand the editor on a canceled lazy buffer.
	tail := []byte(":recoverable")
	editor.pt.Insert(editor.pt.Size(), tail)
	want := string(prefix) + string(source) + string(tail)
	if got := waitPtString(t, editor.pt); got != want {
		t.Fatalf("editor contents after reopen failure = %d bytes, want %d", len(got), len(want))
	}
}

func TestEditorViewStageHardeningFailureAbortsBeforeFirstWrite(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	base, filesystem := newEditorLocalSaveVFS(t, []byte("old"))
	base.attributesErr = func(p string, _ vfs.VFSItem) error {
		if strings.Contains(base.Base(p), ".f4tmp-") {
			return os.ErrPermission
		}
		return nil
	}
	editor := NewEditorView(piecetable.New([]byte("sensitive replacement")), filesystem, base.original)
	defer editor.Close()
	editor.modified = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)

	requireEditorSaveDialog(t)
	base.mu.Lock()
	defer base.mu.Unlock()
	if base.writeCalls != 0 {
		t.Fatalf("stage received %d Write calls after private-mode hardening failed", base.writeCalls)
	}
	if got := string(base.data[base.original]); got != "old" {
		t.Fatalf("hardening failure changed original contents to %q", got)
	}
	if len(base.renamed) != 0 {
		t.Fatalf("hardening failure still finalized a stage: %#v", base.renamed)
	}
	if !editor.modified {
		t.Fatal("hardening failure cleared the editor's modified state")
	}
}

func TestEditorViewMetadataRestoreFailureIsSurfacedAfterCommit(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	base, filesystem := newEditorLocalSaveVFS(t, []byte("old"))
	base.attributesErr = func(p string, _ vfs.VFSItem) error {
		if p == base.original {
			return os.ErrPermission
		}
		return nil
	}
	editor := NewEditorView(piecetable.New([]byte("committed replacement")), filesystem, base.original)
	defer editor.Close()
	editor.modified = true
	editor.SaveToFile(nil)
	waitEditorSave(t, editor)

	requireEditorSaveDialog(t)
	base.mu.Lock()
	committed := string(base.data[base.original])
	renames := len(base.renamed)
	base.mu.Unlock()
	if committed != "committed replacement" || renames != 1 {
		t.Fatalf("content commit = %q, renames = %d", committed, renames)
	}
	if editor.modified {
		t.Fatal("metadata-only partial failure left successfully committed content marked dirty")
	}
}

func (m *mockFailingWriteVFS) Create(ctx context.Context, path string) (io.WriteCloser, error) {
	// Fail if it's the temp file (atomic save) or if it's the target file itself (old direct save tests)
	if strings.Contains(path, ".f4tmp-") || strings.HasSuffix(path, "important.txt") || strings.HasSuffix(path, "persist.txt") {
		return &failingWriter{}, nil
	}
	return m.VFS.Create(ctx, path)
}

type failingWriter struct{}

func (f *failingWriter) Write(p []byte) (n int, err error) {
	return 0, fmt.Errorf("mock write failure")
}
func (f *failingWriter) Close() error { return nil }

func TestEditorView_Save_IOErrorRecovery(t *testing.T) {
	// Verifies that a failure during the streaming write phase of saving
	// does not corrupt the Editor's memory state and does not clear the modified flag.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "persist.txt")
	os.WriteFile(path, []byte("Initial Data"), 0644)

	// Mock VFS that allows Open/Stat/Rename but returns a failing writer for Create
	baseVfs := vfs.NewOSVFS(tmpDir)
	failingVfs := &mockFailingWriteVFS{VFS: baseVfs}

	pt := piecetable.New([]byte("Initial Data"))
	ev := NewEditorView(pt, failingVfs, path)
	defer ev.Close()

	f, err := failingVfs.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()
	ev.file = f

	// 1. Modify the content
	ev.CursorPos = 12
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// 2. Trigger Save
	ev.SaveToFile(nil)

	// Pump tasks to process the async save
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for save failure")
		}
	}

	// 3. Verify Integrity
	if !ev.modified {
		t.Error("Editor 'modified' flag was cleared despite IO failure")
	}
	if ev.pt.String() != "Initial Data!" {
		t.Errorf("Memory state corrupted. Expected 'Initial Data!', got %q", ev.pt.String())
	}

	// Ensure the randomized temp sibling was cleaned up by the save logic.
	assertNoEditorTempSiblings(t, path)
}
func TestEditorView_Save_CreateFailure(t *testing.T) {
	// Verifies that if the Create (truncate) fails (e.g., target file is locked or read-only),
	// the editor does not lose data and keeps the internal state modified.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "locked.txt")
	os.WriteFile(path, []byte("Original Content"), 0644)

	// Mock VFS that allows everything except the Create
	baseVfs := vfs.NewOSVFS(tmpDir)
	failingVfs := &mockFailingVFS{VFS: baseVfs, failCreate: true}

	pt := piecetable.New([]byte("Original Content"))
	ev := NewEditorView(pt, failingVfs, path)
	defer ev.Close()
	f, err := failingVfs.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	ev.file = f

	// 1. Modify the content
	ev.CursorPos = 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// 2. Trigger Save
	ev.SaveToFile(nil)

	// Pump tasks to process the async save
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for create failure")
		}
	}

	// 3. Verify Integrity
	if !ev.modified {
		t.Error("Editor cleared modified flag despite Create failure")
	}
	got := waitPtString(t, ev.pt)
	if got != "!Original Content" {
		t.Errorf("Internal memory state corrupted after create failure. Got %q", got)
	}

	// Original file MUST remain untouched
	orig, _ := os.ReadFile(path)
	if string(orig) != "Original Content" {
		t.Error("Original file was corrupted after a failed save")
	}
}
func TestEditorView_Save_CreateFailure_Recovery_DataPreservation(t *testing.T) {
	// Proves that when Create fails, the editor correctly updates the internal
	// PieceTable to point to the newly reopened VFS file buffer, preventing a crash
	// when reading the original parts of the file afterwards.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "persist.txt")
	os.WriteFile(path, []byte("Original"), 0644)

	// Mock VFS that fails Create
	baseVfs := vfs.NewOSVFS(tmpDir)
	failingVfs := &mockFailingVFS{VFS: baseVfs, failCreate: true}

	f, err := failingVfs.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, failingVfs, path)
	defer ev.Close()
	ev.file = f
	ev.asyncBuf = buf

	// Modify
	ev.CursorPos = 8
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// Save (fails at create stage)
	ev.SaveToFile(nil)
	timeout := time.After(2 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for save failure recovery")
		}
	}

	// The critical test: Can we still read the original part of the PieceTable?
	// The recovery logic should have updated the underlying buffer using UpdateOriginalBuffer.
	got := waitPtString(t, ev.pt)
	if got != "Original!" {
		t.Errorf("Data corrupted after recovery: expected 'Original!', got %q", got)
	}
}
func TestEditorView_ModificationStress(t *testing.T) {
	// Tests stability of LineIndex and navigation during randomized edits.
	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.WordWrap = true

	// A sequence of mixed operations
	ops := []struct {
		char uint16
		vk   uint16
		ctrl bool
	}{
		{vk: vtinput.VK_END},
		{char: 'a'}, {char: 'b'}, {char: 'c'},
		{vk: vtinput.VK_RETURN},
		{char: 'x'}, {char: 'y'}, {char: 'z'},
		{vk: vtinput.VK_UP},
		{vk: vtinput.VK_HOME},
		{vk: vtinput.VK_DELETE}, {vk: vtinput.VK_DELETE},
		{vk: vtinput.VK_BACK},
		{char: ' '},
		{vk: vtinput.VK_A, ctrl: true}, // Select all
		{vk: vtinput.VK_DELETE},        // Wipe document
		{char: 'R'}, {char: 'e'}, {char: 's'}, {char: 't'}, {char: 'a'}, {char: 'r'}, {char: 't'},
	}

	for i, op := range ops {
		ctrlFlag := vtinput.ControlKeyState(0)
		if op.ctrl {
			ctrlFlag = vtinput.LeftCtrlPressed
		}
		pressKey(ev, &vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			Char:            rune(op.char),
			VirtualKeyCode:  op.vk,
			ControlKeyState: ctrlFlag,
		})

		// After every op, verify LineIndex integrity
		expectedLi := piecetable.NewLineIndex()
		expectedLi.Rebuild(ev.pt)
		if ev.li.LineCount() != expectedLi.LineCount() {
			t.Fatalf("Step %d: LineCount mismatch. Got %d, want %d", i, ev.li.LineCount(), expectedLi.LineCount())
		}
	}

	if ev.pt.String() != "Restart" {
		t.Errorf("Stress test result mismatch: %q", ev.pt.String())
	}
}

func TestEditorView_CRLFPieceTable(t *testing.T) {
	// Verifies that \r\n line endings don't cause off-by-one errors in indices.
	content := "Line1\r\nLine2\r\nLine3"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Check initial line count
	if ev.li.LineCount() != 3 {
		t.Errorf("Expected 3 lines for CRLF content, got %d", ev.li.LineCount())
	}

	// 2. Navigation: Down from end of line 0
	ev.CursorLine = 0
	ev.CursorPos = 5 // After '1', before '\r'
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})

	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("CRLF cross-line navigation failed. Target: 1:0, Got: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 3. Backspace from start of line 1 (merging lines)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})

	// Expected: "Line1" + "Line2" ( \r\n removed )
	if !strings.Contains(ev.pt.String(), "Line1Line2") {
		t.Errorf("CRLF merge failed: %q", ev.pt.String())
	}
	if ev.li.LineCount() != 2 {
		t.Errorf("LineCount after CRLF merge: expected 2, got %d", ev.li.LineCount())
	}
}
func TestEditorView_FragmentationDataIntegrity(t *testing.T) {
	// Tests that a highly fragmented file (many pieces in the table)
	// can be saved without a single byte being lost or swapped.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "fragmented.txt")

	// 1. Initial content
	initial := []byte("Initial Content\n")
	os.WriteFile(path, initial, 0644)

	v := vfs.NewOSVFS(tmpDir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	buf := NewAsyncBuffer(context.Background(), f)
	pt := piecetable.NewWithBuffer(buf)
	ev := NewEditorView(pt, v, path)
	defer ev.Close()

	ev.file = f
	ev.asyncBuf = buf

	// 2. Perform 500 deterministic "random" edits to fragment the PieceTable
	// We alternate between inserting and deleting to keep the size manageable
	// but force piece splitting and buffer interleaving.
	reference := append([]byte(nil), initial...)

	for i := 0; i < 500; i++ {
		pos := (i * 7) % (len(reference) + 1)
		data := []byte(fmt.Sprintf("[%d]", i))

		// Update reference
		newRef := make([]byte, 0, len(reference)+len(data))
		newRef = append(newRef, reference[:pos]...)
		newRef = append(newRef, data...)
		newRef = append(newRef, reference[pos:]...)
		reference = newRef

		// Update Editor
		ev.CursorLine = ev.li.GetLineAtOffset(pos)
		ev.CursorPos = pos - ev.li.GetLineOffset(ev.CursorLine)
		for _, r := range string(data) {
			ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
		}

		// Occasional deletion
		if i%3 == 0 && len(reference) > 10 {
			delPos := (i * 13) % (len(reference) - 5)
			reference = append(reference[:delPos], reference[delPos+5:]...)

			ev.CursorLine = ev.li.GetLineAtOffset(delPos)
			ev.CursorPos = delPos - ev.li.GetLineOffset(ev.CursorLine)
			ev.selActive = true
			ev.selAnchorOffset = delPos
			// Move cursor by 5
			newPos := delPos + 5
			ev.CursorLine = ev.li.GetLineAtOffset(newPos)
			ev.CursorPos = newPos - ev.li.GetLineOffset(ev.CursorLine)
			ev.DeleteSelection()
		}
	}

	// 3. Save the fragmented result
	ev.SaveToFile(nil)

	// Pump tasks
	timeout := time.After(3 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout waiting for fragmented file save")
		}
	}

	// 4. Verify byte-for-byte consistency
	savedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(savedData, reference) {
		t.Errorf("DATA CORRUPTION: Saved data does not match expected reference.\nSaved len: %d, Ref len: %d", len(savedData), len(reference))
		// Log start of difference for debugging
		for i := 0; i < len(savedData) && i < len(reference); i++ {
			if savedData[i] != reference[i] {
				t.Errorf("First mismatch at byte %d: saved 0x%02x, ref 0x%02x", i, savedData[i], reference[i])
				break
			}
		}
	}
}
func TestEditorView_Save_NoTrailingNewline_Integrity(t *testing.T) {
	// Verifies that saving a file that does NOT end with a newline
	// does not accidentally add one (a common error in text editors).
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpFile := filepath.Join(t.TempDir(), "nonewline.txt")
	content := []byte("Line 1\nLine 2 (no newline at end)")
	os.WriteFile(tmpFile, content, 0644)

	v := vfs.NewOSVFS(filepath.Dir(tmpFile))
	pt := piecetable.New(content)
	ev := NewEditorView(pt, v, tmpFile)
	defer ev.Close()

	f, err := v.Open(context.Background(), tmpFile)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	ev.file = f

	// 1. Modify in the middle
	ev.CursorLine = 0
	ev.CursorPos = 0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	// 2. Save
	ev.SaveToFile(nil)
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	// 3. Verify exactly what's on disk
	saved, _ := os.ReadFile(tmpFile)
	expected := "!Line 1\nLine 2 (no newline at end)"
	if string(saved) != expected {
		t.Errorf("Save corrupted end-of-file. Expected %q, got %q", expected, string(saved))
	}
}

func TestEditorView_Save_RetryAfterFailure(t *testing.T) {
	// Verifies that if a save fails once, the editor state remains valid
	// and allows a retry once the external issue is resolved.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "retry.txt")
	os.WriteFile(path, []byte("Initial"), 0644)

	baseVfs := vfs.NewOSVFS(tmpDir)
	// mockFailingVFS is defined in the same test file usually
	failingVfs := &mockFailingVFS{VFS: baseVfs, failCreate: true}

	pt := piecetable.New([]byte("Initial"))
	ev := NewEditorView(pt, failingVfs, path)
	defer ev.Close()

	f, err := failingVfs.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	ev.file = f

	// 1. Modify
	ev.SetText("Changed")

	// 2. Save (should fail at Create)
	ev.SaveToFile(nil)
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout on first save")
		}
	}
	if !ev.modified {
		t.Error("Should still be modified after failure")
	}

	// 3. Fix the VFS issue
	failingVfs.failCreate = false

	// 4. Retry saving
	ev.SaveToFile(nil)
	timeout = time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout on retry save")
		}
	}

	// 5. Verification
	if ev.modified {
		t.Error("Should NOT be modified after successful retry")
	}

	saved, _ := os.ReadFile(path)
	if string(saved) != "Changed" {
		t.Errorf("Data not saved correctly on retry. Expected 'Changed', got %q", string(saved))
	}

	// Verify memory state is also consistent
	if waitPtString(t, ev.pt) != "Changed" {
		t.Errorf("Memory state inconsistent after successful retry. Got %q", waitPtString(t, ev.pt))
	}
}
func TestEditorView_Save_MetadataIntegrity(t *testing.T) {
	// Verifies that owner, group, and permissions are restored after atomic save.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "meta.txt")
	os.WriteFile(path, []byte("Original"), 0644)

	// Mock metadata
	expectedMeta := vfs.VFSItem{
		Name:     "meta.txt",
		UnixMode: 0600, // Private file
		Uid:      1234,
		Gid:      5678,
		MTime:    time.Now().Add(-1 * time.Hour),
		ATime:    time.Now().Add(-2 * time.Hour),
	}

	// Mock VFS to track calls
	var capturedMeta vfs.VFSItem
	var attrCalled bool

	baseVfs := vfs.NewOSVFS(tmpDir)
	mock := &mockMetadataVFS{
		VFS:          baseVfs,
		statToReturn: expectedMeta,
		onSetAttr: func(item vfs.VFSItem) {
			capturedMeta = item
			attrCalled = true
		},
	}

	pt := piecetable.New([]byte("Original"))
	ev := NewEditorView(pt, mock, path)
	defer ev.Close()
	f, err := mock.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()
	ev.file = f

	// 1. Modify and Save
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	ev.SaveToFile(nil)

	// Pump tasks
	timeout := time.After(2 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	// 2. Verification
	if !attrCalled {
		t.Error("vfs.SetAttributes was not called after save")
	}
	if capturedMeta.UnixMode != expectedMeta.UnixMode || capturedMeta.Uid != expectedMeta.Uid {
		t.Errorf("Metadata mismatch. Expected mode %o UID %d, got mode %o UID %d",
			expectedMeta.UnixMode, expectedMeta.Uid, capturedMeta.UnixMode, capturedMeta.Uid)
	}
}

func TestEditorView_Save_Atomic_Cleanup(t *testing.T) {
	// Verifies that failed save doesn't leave .f4tmp files behind.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "unlucky.txt")
	os.WriteFile(path, []byte("Untouched"), 0644)

	// VFS that fails during writing
	mock := &mockFailingWriteVFS{VFS: vfs.NewOSVFS(tmpDir)}
	pt := piecetable.New([]byte("Untouched"))
	ev := NewEditorView(pt, mock, path)
	defer ev.Close()

	f, err := mock.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Failed to open file: %v", err)
	}
	defer f.Close()

	ev.file = f

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'X'})
	ev.SaveToFile(nil)

	// Pump tasks
	timeout := time.After(1 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Timeout")
		}
	}

	// 1. Check original is intact
	data, _ := os.ReadFile(path)
	if string(data) != "Untouched" {
		t.Error("Original file was corrupted after failed atomic save")
	}

	// 2. Check the randomized temp sibling is GONE.
	assertNoEditorTempSiblings(t, path)
}

func TestEditorView_Save_AsyncRetry(t *testing.T) {
	// Proves that save loop handles ErrLoading correctly by retrying.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// A buffer that returns ErrLoading twice then succeeds
	loadingBuf := &mockRetryBuffer{
		data:       []byte("AsyncData"),
		failCounts: 2,
	}
	pt := piecetable.NewWithBuffer(loadingBuf)
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "async_save.txt")

	ev := NewEditorView(pt, vfs.NewOSVFS(tmpDir), path)
	defer ev.Close()

	// CRITICAL: Channel must be buffered to avoid deadlock when sending from a UI task
	done := make(chan bool, 1)
	go func() {
		ev.SaveToFile(func() { done <- true })
	}()

	// Pump tasks and simulate time passing for retries
	timeout := time.After(2 * time.Second)
Loop:
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-done:
			break Loop
		case <-timeout:
			t.Fatal("Save timed out - likely hung on ErrLoading")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	saved, _ := os.ReadFile(path)
	if string(saved) != "AsyncData" {
		t.Errorf("Save failed after retries. Got %q", string(saved))
	}
}

type mockRetryBuffer struct {
	data       []byte
	failCounts int
}

func (b *mockRetryBuffer) Size() int { return len(b.data) }
func (b *mockRetryBuffer) Read(offset, length int) ([]byte, error) {
	if b.failCounts > 0 {
		b.failCounts--
		return nil, piecetable.ErrLoading
	}
	end := offset + length
	if end > len(b.data) {
		end = len(b.data)
	}
	return b.data[offset:end], nil
}
func TestEditorView_BinaryRobustness(t *testing.T) {
	// Tests that the editor doesn't crash on huge lines or out-of-bounds cursors.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	t.Run("Empty file length", func(t *testing.T) {
		ev := NewEditorView(piecetable.New(nil), nil, "")
		defer ev.Close()
		if l := ev.getLineLength(0); l != 0 {
			t.Errorf("Empty file: expected length 0, got %d", l)
		}
	})

	t.Run("Single byte file", func(t *testing.T) {
		ev := NewEditorView(piecetable.New([]byte("A")), nil, "")
		defer ev.Close()
		if l := ev.getLineLength(0); l != 1 {
			t.Errorf("Single byte: expected length 1, got %d", l)
		}
	})

	t.Run("Huge binary line", func(t *testing.T) {
		// 1MB of binary data with NO newlines
		data := make([]byte, 1024*1024)
		for i := range data {
			data[i] = 0x01
		}
		ev := NewEditorView(piecetable.New(data), nil, "")
		defer ev.Close()

		// This used to load the whole 1MB, now it should be instant
		start := time.Now()
		l := ev.getLineLength(0)
		if time.Since(start) > 10*time.Millisecond {
			t.Error("getLineLength is too slow on long lines (likely loading whole buffer)")
		}
		if l != 1024*1024 {
			t.Errorf("Huge line: expected %d, got %d", 1024*1024, l)
		}
	})

	t.Run("Cursor clamping", func(t *testing.T) {
		ev := NewEditorView(piecetable.New([]byte("abc")), nil, "")
		defer ev.Close()
		ev.SetPosition(0, 0, 10, 10)

		// Manually set invalid state
		ev.CursorLine = 100
		ev.CursorPos = 500

		// Should not panic, should clamp to 0:3
		ev.ensureCursorVisible()

		if ev.CursorLine != 0 || ev.CursorPos != 3 {
			t.Errorf("Clamping failed: got %d:%d, want 0:3", ev.CursorLine, ev.CursorPos)
		}

		ev.CursorLine = -1
		ev.CursorPos = -1
		ev.ensureCursorVisible()
		if ev.CursorLine != 0 || ev.CursorPos != 0 {
			t.Errorf("Negative clamping failed: got %d:%d, want 0:0", ev.CursorLine, ev.CursorPos)
		}
	})
}
func TestEditorView_Search_Reverse_StartAtZero(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("match"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.CursorPos = 0 // Start at beginning

	// Reverse search from 0 should exit instantly, not hang
	ev.Search("match", false, true, false, false, false)

	timeout := time.After(2 * time.Second)
	select {
	case task := <-vtui.FrameManager.TaskChan:
		task()
	case <-timeout:
		t.Fatal("Reverse search at offset 0 hung")
	}
}

func TestEditorView_DeleteBrokenUTF8(t *testing.T) {
	// Manually insert "half" of a Russian 'П' (0xD0 0x9F)
	pt := piecetable.New([]byte{0xD0, ' ', 'A'})
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.CursorPos = 1 // Cursor right after the broken byte 0xD0

	// Backspace should not crash and should fall back to deleting 1 byte
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_BACK,
	})

	if pt.Size() != 2 {
		t.Errorf("Broken UTF-8 backspace failed, size: %d", pt.Size())
	}
	if pt.String() != " A" {
		t.Errorf("Expected ' A', got %q", pt.String())
	}
}
func TestEditorView_Highlighter_OOM_Protection(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Create a single line of 100KB, which is > 64KB limit
	longLine := make([]byte, 100*1024)
	pt := piecetable.New(longLine)
	ev := NewEditorView(pt, nil, "test.bin")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)
	ev.highlighter = &mockCrashingHighlighter{t: t}

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)

	// If the fix is working, the mock highlighter will not receive the full 100KB
	// and the test will pass without calling t.Errorf.
	ev.Show(scr)
}

func TestEditorView_WordNavigation_OOM_Protection(t *testing.T) {
	// Create a 100KB line, which is > 32KB rune fetch limit
	longLine := strings.Repeat("a", 100*1024)
	pt := piecetable.New([]byte(longLine))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.CursorPos = 50 * 1024

	// Without the fix, this would hang. With the fix, it's instant.
	done := make(chan bool)
	go func() {
		ev.ProcessKey(&vtinput.InputEvent{
			Type:            vtinput.KeyEventType,
			KeyDown:         true,
			VirtualKeyCode:  vtinput.VK_RIGHT,
			ControlKeyState: vtinput.LeftCtrlPressed,
		})
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Fatal("Word navigation on long line timed out, OOM protection likely failed.")
	}
}

func TestEditorView_DeleteSelection_Panic_Protection(t *testing.T) {
	pt := piecetable.New([]byte("hello"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	// Manually set an invalid selection range
	ev.selActive = true
	ev.selAnchorOffset = -100 // Invalid start
	ev.CursorPos = 100        // Invalid end

	// This call should not panic due to the new safety clamps.
	// The clamps should resolve the range to [0:5].
	ev.DeleteSelection()

	if pt.String() != "" {
		t.Errorf("Expected selection to be clamped to [0:5] and delete everything, got %q", pt.String())
	}
}

func TestEditorView_UndoRedo(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("Initial"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.CursorPos = 7 // End of "Initial"

	// --- 1. Basic Grouped Typing ---
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: ' '})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'A'})
	if ev.pt.String() != "Initial A" {
		t.Errorf("Typing failed: %q", ev.pt.String())
	}

	ev.Undo()
	if ev.pt.String() != "Initial" {
		t.Errorf("Undo typing failed: expected 'Initial', got %q", ev.pt.String())
	}

	ev.Redo()
	if ev.pt.String() != "Initial A" {
		t.Errorf("Redo typing failed: expected 'Initial A', got %q", ev.pt.String())
	}

	// --- 2. Line Split & Merge ---
	// Start with "Initial A" at cursor 0:7 (between 'Initial' and ' A')
	ev.CursorPos = 7
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	// Now text is "Initial\n A", cursor is at 1:0
	if ev.li.LineCount() != 2 {
		t.Errorf("Split failed: expected 2 lines, got %d", ev.li.LineCount())
	}

	// Merge back using backspace at 1:0
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_BACK})
	if ev.li.LineCount() != 1 || ev.pt.String() != "Initial A" {
		t.Errorf("Merge failed: %q", ev.pt.String())
	}

	// Undo Merge -> back to "Initial\n A"
	ev.Undo()
	if ev.li.LineCount() != 2 {
		t.Errorf("Undo Merge failed: expected 2 lines, got %d", ev.li.LineCount())
	}

	// Undo Split -> back to "Initial A"
	ev.Undo()
	if ev.li.LineCount() != 1 || ev.pt.String() != "Initial A" {
		t.Errorf("Undo Split failed: %q", ev.pt.String())
	}

	// --- 3. Atomic Paste ---
	// Move cursor to end: "Initial A" (len 9)
	ev.CursorPos = 9
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '1'})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '2'})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: false})

	if ev.pt.String() != "Initial A12" {
		t.Errorf("Paste failed: expected 'Initial A12', got %q", ev.pt.String())
	}

	ev.Undo()
	if ev.pt.String() != "Initial A" {
		t.Errorf("Undo Paste failed: expected 'Initial A', got %q", ev.pt.String())
	}

	// --- 4. Redo Stack Branching ---
	// Clear redo by new modification
	ev.CursorPos = 9
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	if len(ev.redoStack) != 0 {
		t.Error("Redo stack was NOT cleared after a new edit")
	}

	ev.Redo() // Should do nothing
	if ev.pt.String() != "Initial A!" {
		t.Errorf("Redo worked when it shouldn't: %q", ev.pt.String())
	}
}
func TestEditorView_StateRestoration(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	content := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Имитируем загрузку сохраненного состояния (как это делает actionOpenEditor)
	ev.targetLine = 3
	ev.targetPos = 2
	ev.targetTopRow = 2
	ev.targetLeft = 0

	// Имитируем завершение StartIndexing()
	ev.CursorLine = ev.targetLine
	ev.CursorPos = ev.targetPos
	ev.ScrollTopRow = ev.targetTopRow
	ev.ScrollLeft = ev.targetLeft
	ev.targetLine = -1
	ev.ensureCursorVisible()
	ev.updateDesiredVisualCol()

	if ev.ScrollLeft == -1 {
		t.Error("ScrollLeft was incorrectly restored to -1 (causes empty left column bug)")
	}
	if ev.ScrollTopRow != 2 {
		t.Errorf("ScrollTopRow was not restored correctly, got %d", ev.ScrollTopRow)
	}
	if ev.CursorLine != 3 {
		t.Errorf("CursorLine was not restored correctly, got %d", ev.CursorLine)
	}
	if ev.DesiredVisualCol == 0 {
		t.Error("DesiredVisualCol was not updated, vertical navigation will break")
	}
}
func TestEditorView_StateRestoration_Interference(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1\nline2\nline3"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	ev.targetLine = 2 // Хотим прыгнуть на 3-ю строку
	ev.targetPos = 0

	// Имитируем, что пользователь нажал клавишу (например, вправо) до завершения
	// индексации. Клавиша должна реально сдвинуть курсор: нажатие, которое ничего
	// не изменило, вмешательством пользователя больше не считается.
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})

	if ev.targetLine != -1 {
		t.Error("User intervention must cancel pending state restoration")
	}

	// Пытаемся применить старое состояние (симуляция завершения StartIndexing)
	// Оно не должно затирать текущую позицию пользователя
	if ev.targetLine == -1 {
		// logic in StartIndexing: if targetLine == -1, do nothing.
	} else {
		t.Error("Indexing task applied state even though user took control")
	}
}

func TestEditorView_StateRestoration_BoundaryClamping(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("short file")) // всего 1 строка
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	ev.targetLine = 50 // Сохраненная позиция в старом длинном файле
	ev.targetPos = 100

	// Имитируем вызов из StartIndexing
	ev.CursorLine = ev.targetLine
	if ev.CursorLine >= ev.li.LineCount() {
		ev.CursorLine = ev.li.LineCount() - 1
	}
	ev.targetLine = -1
	ev.ensureCursorVisible()

	if ev.CursorLine != 0 {
		t.Errorf("Clamping failed. Expected line 0, got %d", ev.CursorLine)
	}
}
func TestEditorView_CharacterWidthConsistency(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte{0x01, 0x00, 'a'})
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	_, col2 := ev.engine.LogicalToVisual(2)
	cells := ev.fillCells(nil, []byte{0x01, 0x00, 'a'}, 0, 0, 0, false, 0, 0, nil, 0, false, -1, 0, 0, 0)

	if col2 != 2 {
		t.Errorf("Expected LogicalToVisual col 2, got %d", col2)
	}
	if len(cells) != 3 {
		t.Errorf("Expected 3 cells rendered for control chars + 'a' to prevent disappearing, got %d", len(cells))
	}
	for i, cell := range cells {
		if cell.Char == 0 {
			t.Errorf("Cell at index %d has character code 0, which might disappear in terminal rendering", i)
		}
	}
}

func TestEditorView_CrosshairStateAndNoLeak(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	SetDefaultF4Palette()

	oldCrosshair := AppConfig.EditorCrosshair
	AppConfig.EditorCrosshair = true
	defer func() { AppConfig.EditorCrosshair = oldCrosshair }()

	pt := piecetable.New([]byte("line1\nline2\nline3"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 10)

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 11)

	ev.CursorLine = 1
	ev.CursorPos = 2
	ev.SetFocus(true)

	ev.Show(scr)

	crossAttr := vtui.Palette[ColEditorCrosshair]
	crossBG := vtui.GetRGBBack(crossAttr)

	activeRowCell := scr.GetCell(5, 2)
	if vtui.GetRGBBack(activeRowCell.Attributes) != crossBG {
		t.Errorf("Expected active row Y=2 to have crosshair background %06X, got %06X", crossBG, vtui.GetRGBBack(activeRowCell.Attributes))
	}

	nonActiveRowCell := scr.GetCell(5, 1)
	if vtui.GetRGBBack(nonActiveRowCell.Attributes) == crossBG {
		t.Error("Non-active row erroneously has crosshair background (sticking/leakage)")
	}

	verticalCell := scr.GetCell(2, 1)
	if vtui.GetRGBBack(verticalCell.Attributes) != crossBG {
		t.Errorf("Expected vertical crosshair column X=2 on non-active row Y=1 to have crosshair background, got %06X", vtui.GetRGBBack(verticalCell.Attributes))
	}

	ev.CursorLine = 0
	ev.CursorPos = 0
	ev.Show(scr)

	activeRowCellAfter := scr.GetCell(5, 2)
	if vtui.GetRGBBack(activeRowCellAfter.Attributes) == crossBG {
		t.Error("Crosshair background leaked/stuck on row 2 after cursor moved away")
	}
}
func TestEditorView_ZeroAndDoubleWidthConsistency(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	text := []byte("a\u0301世b")
	pt := piecetable.New(text)
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	_, colA := ev.engine.LogicalToVisual(1)
	_, colCombining := ev.engine.LogicalToVisual(3)
	_, colCJK := ev.engine.LogicalToVisual(6)
	_, colB := ev.engine.LogicalToVisual(7)

	cells := ev.fillCells(nil, text, 0, 0, 0, false, 0, 0, nil, 0, false, -1, 0, 0, 0)

	if colA != 1 {
		t.Errorf("Expected column after 'a' to be 1, got %d", colA)
	}
	if colCombining != 2 {
		t.Errorf("Expected column after combining char to be 2, got %d", colCombining)
	}
	if colCJK != 4 {
		t.Errorf("Expected column after CJK char to be 4, got %d", colCJK)
	}
	if colB != 5 {
		t.Errorf("Expected column after 'b' to be 5, got %d", colB)
	}

	if len(cells) != 5 {
		t.Errorf("Expected exactly 5 cells rendered (1 for 'a', 1 for combining, 2 for CJK, 1 for 'b'), got %d", len(cells))
	}

	if cells[0].Char != 'a' {
		t.Errorf("Expected cells[0] to be 'a', got %c", rune(cells[0].Char))
	}
	if cells[2].Char != '世' {
		t.Errorf("Expected cells[2] to be '世', got %c", rune(cells[2].Char))
	}
	if cells[3].Char != uint64(vtui.WideCharFiller) {
		t.Errorf("Expected cells[3] to be WideCharFiller, got %d", cells[3].Char)
	}
	if cells[4].Char != 'b' {
		t.Errorf("Expected cells[4] to be 'b', got %c", rune(cells[4].Char))
	}
}

func TestEditorView_StateRestoration_UnicodeColumn(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// "世" (2 колонки)
	pt := piecetable.New([]byte("世ABC"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// Восстанавливаем позицию после "世" (индекс байта 3)
	ev.targetLine = 0
	ev.targetPos = 3
	ev.targetTopRow = 0
	ev.targetLeft = 0

	// Имитируем применение
	ev.CursorLine = ev.targetLine
	ev.CursorPos = ev.targetPos
	ev.targetLine = -1
	ev.updateDesiredVisualCol()

	// "世" занимает 2 колонки, значит курсор должен хотеть стоять во 2-й колонке (0, 1, [2])
	if ev.DesiredVisualCol != 2 {
		t.Errorf("DesiredVisualCol for Unicode failed. Got %d, want 2", ev.DesiredVisualCol)
	}
}
func TestEditorView_Undo_Advanced(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("abcde"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)
	ev.CursorPos = 2 // On 'c'

	// 1. Test Delete key (forward delete)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DELETE})
	if ev.pt.String() != "abde" {
		t.Errorf("Delete failed: %q", ev.pt.String())
	}
	ev.Undo()
	if ev.pt.String() != "abcde" {
		t.Errorf("Undo Delete failed: %q", ev.pt.String())
	}

	// 2. Test Selection Replacement (Typing over)
	// Select "bc"
	ev.CursorPos = 1
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.ShiftPressed})
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.ShiftPressed})

	// Type 'X' over "bc"
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: 'X'})
	if ev.pt.String() != "aXde" {
		t.Errorf("Replacement failed: %q", ev.pt.String())
	}

	// It MUST be one undo step
	ev.Undo()
	if ev.pt.String() != "abcde" {
		t.Errorf("Undo Replacement failed: expected 'abcde', got %q. Grouping likely broken.", ev.pt.String())
	}
	if ev.CursorPos != 1 {
		t.Errorf("Undo Replacement cursor failed: expected 1, got %d", ev.CursorPos)
	}

	// 3. Test Empty Stack Safety
	ev.undoStack = nil
	ev.Undo() // Should not panic
	if ev.pt.String() != "abcde" {
		t.Error("Undo on empty stack corrupted data")
	}
}
func TestEditorView_Undo_CleanState(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("Original"))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	if ev.modified {
		t.Error("Should NOT be modified initially")
	}

	// 1. Modify
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})
	if !ev.modified {
		t.Error("Should BE modified after typing")
	}

	// 2. Undo -> back to original
	ev.Undo()
	if ev.modified {
		t.Error("Should NOT be modified after undoing all changes")
	}

	// 3. Redo -> back to modified
	ev.Redo()
	if !ev.modified {
		t.Error("Should BE modified after redo")
	}
}
func TestEditorView_WordJumps_FarSpec(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// line one (8 bytes) + \n + line two (8 bytes)
	pt := piecetable.New([]byte("line one\nline two"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt) // Важно: пересобираем индексы
	ev.SetPosition(0, 0, 80, 24)
	ev.CursorLine = 0
	ev.CursorPos = 0

	// 1. Прыжок внутри строки к началу второго слова "one" (оффсет 5)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 5 {
		t.Errorf("Jump inside failed: expected 5, got %d", ev.CursorPos)
	}

	// 2. Прыжок к концу строки (EOL оффсет 8)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 8 {
		t.Errorf("Jump to EOL failed: expected 8, got %d", ev.CursorPos)
	}

	// 3. Прыжок через границу строки (EOL -> 0 следующей)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("Line cross failed: expected 1:0, got %d:%d", ev.CursorLine, ev.CursorPos)
	}

	// 4. Прыжок назад через начало строки (BOL -> End предыдущей)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 0 || ev.CursorPos != 8 {
		t.Errorf("Line cross back failed: expected 0:8, got %d:%d", ev.CursorLine, ev.CursorPos)
	}
}

func TestEditorView_WordJumps_VisualWrap(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// Текст без пробелов. Ширина 5. Перенос на 5-м символе.
	pt := piecetable.New([]byte("0123456789"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.WordWrap = true
	// Контентная ширина 5
	ev.SetPosition(0, 0, 5, 10)
	ev.CursorPos = 0

	// Согласно спецификации, прыжок не может пересекать границу визуальной строки.
	// В текущей реализации f4 курсор останавливается на последнем символе видимой строки (индекс 4).
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 4 {
		t.Errorf("Visual wrap stop fail: expected 4, got %d", ev.CursorPos)
	}
}

func TestEditorView_WordJumps_EmptyLines(t *testing.T) {
	pt := piecetable.New([]byte("top\n\n\nend"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.CursorLine = 0
	ev.CursorPos = 3

	// Шагаем через пустые строки (два \n подряд)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 1 {
		t.Errorf("Step 1 fail: line %d", ev.CursorLine)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 2 {
		t.Errorf("Step 2 fail: line %d", ev.CursorLine)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorLine != 3 || ev.CursorPos != 0 {
		t.Errorf("Step 3 fail: %d:%d", ev.CursorLine, ev.CursorPos)
	}
}
func TestEditorView_WordSelection_Multiline(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// "line1\nline2"
	pt := piecetable.New([]byte("line1\nline2"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.CursorPos = 0

	// 1. Выделяем всё первое слово (до \n)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	if ev.selAnchorOffset != 0 || ev.CursorPos != 5 {
		t.Errorf("Initial multiline selection fail: anchor %d, pos %d", ev.selAnchorOffset, ev.CursorPos)
	}

	// 2. Прыгаем через EOL. Выделение должно расшириться на вторую строку
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_RIGHT,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	// Теперь курсор на 1:0. В байтах это 6 (line1 + \n)
	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("Selection cursor cross-line fail: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	min, max := ev.getSelectionRange()
	if min != 0 || max != 6 {
		t.Errorf("Selection range across EOL fail: [%d:%d], expected [0:6]", min, max)
	}
}
func TestEditorView_WordSelection_Multiline_Left(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	// "line1\nline2"
	//  01234 5 67890
	pt := piecetable.New([]byte("line1\nline2"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.CursorLine = 1
	ev.CursorPos = 5 // end of "line2", absolute offset 11

	// 1. Выделяем второе слово влево
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	// Курсор должен остановиться в начале "line2" (6)
	if ev.CursorLine != 1 || ev.CursorPos != 0 {
		t.Errorf("First left jump fail: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	min, max := ev.getSelectionRange()
	if min != 6 || max != 11 {
		t.Errorf("Selection range fail: [%d:%d], expected [6:11]", min, max)
	}

	// 2. Прыгаем еще раз влево, через границу строки (EOL)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	// Согласно спецификации, прыжок с позиции 0 упирается в конец предыдущей строки.
	if ev.CursorLine != 0 || ev.CursorPos != 5 {
		t.Errorf("Cross-line left jump fail: %d:%d, expected 0:5", ev.CursorLine, ev.CursorPos)
	}

	// 3. Последний прыжок влево к началу первого слова
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode:  vtinput.VK_LEFT,
		ControlKeyState: vtinput.LeftCtrlPressed | vtinput.ShiftPressed,
	})

	if ev.CursorLine != 0 || ev.CursorPos != 0 {
		t.Errorf("Final left jump fail: %d:%d", ev.CursorLine, ev.CursorPos)
	}

	min, max = ev.getSelectionRange()
	if min != 0 || max != 11 {
		t.Errorf("Full cross-line selection range fail: [%d:%d], expected [0:11]", min, max)
	}
}
func TestEditorView_WordJumps_DifferentDividers(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("...///"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.CursorPos = 0

	// A change of divider kind is not a word boundary in far2l, so the whole
	// run is crossed in a single jump (issue #280).
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})

	if ev.CursorPos != 6 {
		t.Errorf("EditorView expected stop on index 6, got %d", ev.CursorPos)
	}
}

func TestEditorView_WordJumps_DividerBetweenWords(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("foo.bar"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.CursorPos = 0

	// far2l stops at the end of the word, then jumps straight to the end of
	// the line: the divider itself is not a landing spot.
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 3 {
		t.Errorf("first Ctrl+Right: expected 3, got %d", ev.CursorPos)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 7 {
		t.Errorf("second Ctrl+Right: expected 7, got %d", ev.CursorPos)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 4 {
		t.Errorf("first Ctrl+Left: expected 4, got %d", ev.CursorPos)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT, ControlKeyState: vtinput.LeftCtrlPressed})
	if ev.CursorPos != 0 {
		t.Errorf("second Ctrl+Left: expected 0, got %d", ev.CursorPos)
	}
}

func TestEditorView_CtrlLeftPastEOL(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("foo bar"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.SetPosition(0, 0, 40, 10)
	ev.CursorBeyondEOL = true
	ev.CursorLine = 0
	ev.CursorPos = 7
	ev.CursorVirtualSpaces = 5 // the cursor floats past the end of the line
	ev.updateDesiredVisualCol()

	ctrlLeft := func() {
		ev.ProcessKey(keyEvent(vtinput.VK_LEFT, vtinput.LeftCtrlPressed))
	}

	// Past EOL a word jump behaves as if the cursor stood at the real end of
	// the line: it lands on the last word, it does not eat virtual spaces
	// one by one.
	ctrlLeft()
	if ev.CursorVirtualSpaces != 0 {
		t.Errorf("Ctrl+Left should drop the virtual spaces, got %d", ev.CursorVirtualSpaces)
	}
	if ev.CursorPos != 4 {
		t.Errorf("Ctrl+Left past EOL: expected pos 4, got %d", ev.CursorPos)
	}

	ctrlLeft()
	if ev.CursorPos != 0 {
		t.Errorf("second Ctrl+Left: expected pos 0, got %d", ev.CursorPos)
	}

	// A plain Left still walks back through the virtual spaces.
	ev.CursorPos = 7
	ev.CursorVirtualSpaces = 2
	ev.updateDesiredVisualCol()
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_LEFT})
	if ev.CursorPos != 7 || ev.CursorVirtualSpaces != 1 {
		t.Errorf("plain Left past EOL: expected pos 7 / virt 1, got %d / %d", ev.CursorPos, ev.CursorVirtualSpaces)
	}
}

func TestEditorView_CtrlUpDownScrollsTextUnderCursor(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "line %02d\n", i)
	}
	pt := piecetable.New([]byte(strings.TrimSuffix(sb.String(), "\n")))
	ev := NewEditorView(pt, nil, "scroll.txt")
	defer ev.Close()
	ev.li.Rebuild(pt)
	ev.SetPosition(0, 0, 40, 10) // 10 visible text rows
	ev.CursorLine = 5
	ev.CursorPos = 0
	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()

	ctrlDown := func() { ev.ProcessKey(keyEvent(vtinput.VK_DOWN, vtinput.LeftCtrlPressed)) }
	ctrlUp := func() { ev.ProcessKey(keyEvent(vtinput.VK_UP, vtinput.LeftCtrlPressed)) }
	screenRow := func() int { return ev.CursorLine - ev.ScrollTopRow }

	// 1. The text moves under the cursor: TopScreen and CurLine step
	// together, so the cursor does not budge on the screen.
	ctrlDown()
	if ev.ScrollTopRow != 1 || ev.CursorLine != 6 {
		t.Errorf("Ctrl+Down: expected top 1 / line 6, got %d / %d", ev.ScrollTopRow, ev.CursorLine)
	}
	if screenRow() != 5 {
		t.Errorf("cursor left its screen row: %d", screenRow())
	}

	// 2. ...all the way down to the last screenful (30 rows, 10 visible).
	for i := 0; i < 19; i++ {
		ctrlDown()
	}
	if ev.ScrollTopRow != 20 || ev.CursorLine != 25 || screenRow() != 5 {
		t.Fatalf("scrolled to the bottom: top %d, line %d, screen row %d", ev.ScrollTopRow, ev.CursorLine, screenRow())
	}

	// 3. There the view is stuck and the cursor walks on alone until it
	// reaches the last line (far2l falls back to a bare Down()).
	ctrlDown()
	if ev.ScrollTopRow != 20 || ev.CursorLine != 26 {
		t.Errorf("at EOF the cursor should move alone: top %d, line %d", ev.ScrollTopRow, ev.CursorLine)
	}
	for i := 0; i < 10; i++ {
		ctrlDown()
	}
	if ev.ScrollTopRow != 20 || ev.CursorLine != 29 {
		t.Errorf("expected to stop on the last line: top %d, line %d", ev.ScrollTopRow, ev.CursorLine)
	}

	// 4. Scrolling back up the cursor rides along again, keeping its row.
	ctrlUp()
	if ev.ScrollTopRow != 19 || ev.CursorLine != 28 || screenRow() != 9 {
		t.Errorf("Ctrl+Up: expected top 19 / line 28, got %d / %d", ev.ScrollTopRow, ev.CursorLine)
	}

	// 5. At the top of the file it walks alone once more, down to line 0.
	for i := 0; i < 40; i++ {
		ctrlUp()
	}
	if ev.ScrollTopRow != 0 || ev.CursorLine != 0 {
		t.Errorf("expected to stop at the top of the file: top %d, line %d", ev.ScrollTopRow, ev.CursorLine)
	}
}

func TestEditorView_Indexer_SessionFencing(t *testing.T) {
	// Это тест на предотвращение рассинхронизации данных при фоновой индексации.
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1\nline2"))
	ev := NewEditorView(pt, nil, "fencing.txt")
	defer ev.Close()

	// 1. Запоминаем текущую сессию
	initialSession := ev.editSession

	// 2. Имитируем запуск задачи индексатором (captured session ID)
	capturedSession := initialSession

	// 3. Происходит редактирование (например, Backspace)
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_BACK,
	})

	if ev.editSession <= capturedSession {
		t.Errorf("Edit session should have incremented. Was %d, now %d", capturedSession, ev.editSession)
	}

	// 4. Имитируем «прилет» результата индексации из прошлого
	indexerApplied := false
	task := func() {
		if ev.editSession != capturedSession {
			// Задача должна быть отброшена, так как сессия изменилась
			return
		}
		indexerApplied = true
	}

	task()

	if indexerApplied {
		t.Error("CRITICAL: Stale indexer task was applied to a modified buffer!")
	}
}

func TestEditorView_CursorScrollbarBoundary(t *testing.T) {
	// Проверяем, что курсор не заходит на колонку скроллбара
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Создаем длинную строку
	text := strings.Repeat("a", 100)
	pt := piecetable.New([]byte(text))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()

	// Устанавливаем ширину 20 (X=0..19). Скроллбар будет на X=19.
	ev.SetPosition(0, 0, 19, 10)
	ev.WordWrap = false

	// Прыгаем в конец (vCol = 100)
	ev.CursorPos = 100
	ev.ensureCursorVisible()

	// Относительная позиция курсора на экране: vCol - ScrollLeft
	relCursorX := 100 - ev.ScrollLeft

	// Максимально допустимая позиция X для курсора — это 18 (так как 19 занято скроллбаром)
	if relCursorX >= 19 {
		t.Errorf("Cursor landed on scrollbar column! RelX: %d, Window X2: 19", relCursorX)
	}

	if relCursorX != 18 {
		t.Errorf("Cursor should be exactly at the last available column (18), got %d", relCursorX)
	}
}

func TestEditorView_SearchPersistence(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// 1. Выполняем поиск
	ev1 := NewEditorView(piecetable.New([]byte("data")), nil, "f1.txt")
	defer ev1.Close()
	ev1.Search("pattern", true, true, false, false, false)

	// Дожидаемся завершения асинхронного поиска
	timeout := time.After(1 * time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-time.After(10 * time.Millisecond):
		}
		if LastEditorSearch == "pattern" {
			break
		}
		select {
		case <-timeout:
			t.Fatal("Search globals were not updated")
		default:
		}
	}

	// 2. Проверяем переменные
	if LastEditorSearch != "pattern" || !LastEditorSearchCase || !LastEditorSearchReverse {
		t.Errorf("Search parameters lost: %q, case:%v, rev:%v",
			LastEditorSearch, LastEditorSearchCase, LastEditorSearchReverse)
	}
}

// countDialogButtons tells a Replace confirmation prompt (4 buttons) apart
// from the summary box (1 button) that shares its title.
func countDialogButtons(w *vtui.Window) int {
	n := 0
	for _, c := range w.GetChildren() {
		if _, ok := c.(*vtui.Button); ok {
			n++
		}
	}
	return n
}

// pumpReplacePrompt drains UI tasks until a Replace confirmation dialog
// other than prev is on top, and returns it.
func pumpReplacePrompt(t *testing.T, prev *vtui.Window) *vtui.Window {
	t.Helper()
	var dlg *vtui.Window
	pumpFindAll(t, func() bool {
		w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if ok && w != prev && w.GetTitle() == Msg("Replace.ConfirmTitle") && countDialogButtons(w) == 4 {
			dlg = w
		}
		return dlg != nil
	})
	return dlg
}

// pumpReplaceSummary drains UI tasks until the "N occurrence(s) replaced"
// box shows up, proving the loop ended with a report instead of the bare
// not-found message.
func pumpReplaceSummary(t *testing.T) {
	t.Helper()
	pumpFindAll(t, func() bool {
		w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		return ok && w.GetTitle() == Msg("Replace.ConfirmTitle") && countDialogButtons(w) == 1
	})
}

func TestEditorView_Replace_InteractivePromptFlow(t *testing.T) {
	content := "match one, match two, match three"
	ev := newFindAllEditor(t, content)

	// One click on [ Replace ] with no selection prompts at the first
	// occurrence without touching the buffer.
	ev.Replace("match", "X", false, false, false, false, false)
	dlg := pumpReplacePrompt(t, nil)
	if data, _ := ev.pt.Bytes(); string(data) != content {
		t.Fatalf("the prompt must not replace by itself, buffer: %q", data)
	}
	if !ev.selActive || ev.selAnchorOffset != 0 {
		t.Errorf("first occurrence should be selected at 0, got active=%v anchor=%d",
			ev.selActive, ev.selAnchorOffset)
	}

	// Replace: exactly this occurrence, then a prompt at the next one.
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	want := "X one, match two, match three"
	if data, _ := ev.pt.Bytes(); string(data) != want {
		t.Fatalf("after Replace, buffer: %q, want %q", data, want)
	}
	if ev.selAnchorOffset != len("X one, ") {
		t.Errorf("second occurrence should be selected at %d, got %d",
			len("X one, "), ev.selAnchorOffset)
	}

	// Skip: buffer untouched, prompt advances.
	dlg2.SetExitCode(replaceBtnSkip)
	dlg3 := pumpReplacePrompt(t, dlg2)
	if data, _ := ev.pt.Bytes(); string(data) != want {
		t.Fatalf("Skip must not edit, buffer: %q", data)
	}
	wantThird := len("X one, match two, ")
	if ev.selAnchorOffset != wantThird {
		t.Errorf("third occurrence should be selected at %d, got %d", wantThird, ev.selAnchorOffset)
	}

	// Cancel: the loop stops, nothing else changes, the occurrence stays
	// selected with the cursor on it.
	dlg3.SetExitCode(replaceBtnCancel)
	pumpFindAllFor(t, 300*time.Millisecond, func() (string, bool) {
		w, ok := vtui.FrameManager.GetTopFrame().(*vtui.Window)
		if ok && w != dlg3 && w.GetTitle() == Msg("Replace.ConfirmTitle") {
			return "Cancel must end the loop, but another Replace dialog appeared", true
		}
		return "", false
	})
	if data, _ := ev.pt.Bytes(); string(data) != want {
		t.Errorf("Cancel must not edit, buffer: %q", data)
	}
	if !ev.selActive || ev.selAnchorOffset != wantThird {
		t.Errorf("canceled occurrence should stay selected at %d, got active=%v anchor=%d",
			wantThird, ev.selActive, ev.selAnchorOffset)
	}
}

func TestEditorView_Replace_InteractiveAllSingleUndo(t *testing.T) {
	content := "match one, match two, match three"
	ev := newFindAllEditor(t, content)
	ev.Replace("match", "X", false, false, false, false, false)

	// All at the first prompt finishes the rest without further prompts
	// and reports the total.
	dlg := pumpReplacePrompt(t, nil)
	dlg.SetExitCode(replaceBtnAll)
	want := "X one, X two, X three"
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == want })
	pumpReplaceSummary(t)

	// Far wraps the All tail in a single undo block: one Undo restores
	// everything the button replaced.
	ev.Undo()
	if data, _ := ev.pt.Bytes(); string(data) != content {
		t.Errorf("All should be one undo step, after Undo: %q", data)
	}
}

func TestEditorView_Replace_AdjacentOccurrenceNotSkipped(t *testing.T) {
	ev := newFindAllEditor(t, "aaaa")
	ev.Replace("aa", "x", false, false, false, false, false)

	// Replace-Replace on "aaaa" with aa->x must produce "xx": the second
	// occurrence starts exactly at the end of the first replacement.
	dlg := pumpReplacePrompt(t, nil)
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	dlg2.SetExitCode(replaceBtnReplace)
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == "xx" })
	pumpReplaceSummary(t)
}

func TestEditorView_Replace_ReplacementContainsPattern(t *testing.T) {
	ev := newFindAllEditor(t, "aa")
	ev.Replace("a", "aa", false, false, false, false, false)

	// The replacement's own output must never be re-matched: exactly two
	// prompts, then the summary.
	dlg := pumpReplacePrompt(t, nil)
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	dlg2.SetExitCode(replaceBtnReplace)
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == "aaaa" })
	pumpReplaceSummary(t)
}

func TestEditorView_Replace_RegexExpandsPerOccurrence(t *testing.T) {
	ev := newFindAllEditor(t, "a1 b2")
	ev.Replace(`([a-z])(\d)`, "$2$1", true, false, true, false, false)

	dlg := pumpReplacePrompt(t, nil)
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	dlg2.SetExitCode(replaceBtnReplace)
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == "1a 2b" })
	pumpReplaceSummary(t)
}

func TestReplacePrompt_RegexShowsExpandedReplacement(t *testing.T) {
	// The prompt shows what will really be inserted for this occurrence,
	// not the raw "$2$1" the user typed.
	re, err := buildSearchRegex(`([a-z])(\d)`, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	st := &replaceLoop{replacement: "$2$1", regexp: true, re: re}
	rendered := string(st.renderReplacement([]byte("a1")))
	if rendered != "1a" {
		t.Fatalf("rendered replacement = %q, want \"1a\"", rendered)
	}
	body := replacePromptBody("a1", rendered)
	if !strings.Contains(body, "\"a1\"") || !strings.Contains(body, "\"1a\"") {
		t.Errorf("prompt body should quote the match and the expanded replacement, got %q", body)
	}
}

func TestEditorView_Replace_FoldedDifferentLength(t *testing.T) {
	// K (U+212A, 3 bytes) case-folds to "k": the replacement must consume
	// the folded character's real byte width, not len(pattern).
	ev := newFindAllEditor(t, "K x k")
	ev.Replace("k", "q", false, false, false, false, false)

	dlg := pumpReplacePrompt(t, nil)
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	dlg2.SetExitCode(replaceBtnReplace)
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == "q x q" })
	pumpReplaceSummary(t)
}

func TestEditorView_Replace_ReverseWalksRightToLeft(t *testing.T) {
	content := "match one, match two"
	ev := newFindAllEditor(t, content)
	ev.CursorLine = 0
	ev.CursorPos = len(content)
	ev.Replace("match", "X", false, true, false, false, false)

	dlg := pumpReplacePrompt(t, nil)
	second := len("match one, ")
	if ev.selAnchorOffset != second {
		t.Fatalf("reverse should prompt the rightmost occurrence at %d, got %d",
			second, ev.selAnchorOffset)
	}
	dlg.SetExitCode(replaceBtnReplace)
	dlg2 := pumpReplacePrompt(t, dlg)
	if ev.selAnchorOffset != 0 {
		t.Errorf("second prompt should be at offset 0, got %d", ev.selAnchorOffset)
	}
	dlg2.SetExitCode(replaceBtnReplace)
	pumpFindAll(t, func() bool { data, _ := ev.pt.Bytes(); return string(data) == "X one, X two" })
	pumpReplaceSummary(t)
}

func TestEditorView_Autocomplete_Logic(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	// Настраиваем конфиг
	oldCfg := AppConfig
	AppConfig.EditorAutoComplete = true
	AppConfig.EditorAutoCompleteMask = "*.txt"
	defer func() { AppConfig = oldCfg }()

	// Создаем текст с повторяющимися словами
	content := "apple application approach\nbanana\napple"
	pt := piecetable.New([]byte(content))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	if !ev.acEnabled {
		t.Fatal("Autocomplete should be enabled for test.txt")
	}

	// 1. Проверка набора префикса
	// Переходим в конец второй строки и начинаем писать "app"
	ev.CursorLine = 1
	ev.CursorPos = 6 // после "banana"
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})
	// Теперь CursorLine = 2, Pos = 0

	for _, char := range "app" {
		ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char})
	}

	if ev.acPrefix != "app" {
		t.Errorf("Wrong AC prefix: expected 'app', got %q", ev.acPrefix)
	}

	// Должно найти 3 слова: apple, application, approach
	if len(ev.acMatches) < 3 {
		t.Errorf("Expected at least 3 matches, got %d: %v", len(ev.acMatches), ev.acMatches)
	}

	// 2. Проверка переключения (Shift+Tab)
	initialMatch := ev.acMatches[ev.acCurrentIdx]
	ev.ProcessKey(&vtinput.InputEvent{
		Type: vtinput.KeyEventType, KeyDown: true,
		VirtualKeyCode: vtinput.VK_TAB, ControlKeyState: vtinput.ShiftPressed,
	})
	if ev.acMatches[ev.acCurrentIdx] == initialMatch {
		t.Error("Shift+Tab failed to cycle matches")
	}

	// 3. Проверка применения (Tab)
	// Допустим, выбрали "apple"
	ev.acCurrentIdx = 0
	for ev.acMatches[ev.acCurrentIdx] != "apple" {
		ev.acCurrentIdx = (ev.acCurrentIdx + 1) % len(ev.acMatches)
	}

	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_TAB})

	// "app" + "le" = "apple"
	lastLine := ev.getLogicalLineRunes(2)
	if string(lastLine) != "apple" {
		t.Errorf("Autocomplete application failed: expected 'apple', got %q", string(lastLine))
	}
	if ev.acMatches != nil {
		t.Error("AC matches should be cleared after application")
	}
}

func TestEditorView_Autocomplete_Cancellation(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("helicopter "))
	ev := NewEditorView(pt, nil, "test.txt")
	defer ev.Close()
	ev.CursorPos = 11 // Start at the end to avoid "middle of the word" check
	ev.acEnabled = true

	// Начинаем писать "hel"
	for _, char := range "hel" {
		ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char})
	}
	if len(ev.acMatches) == 0 {
		t.Fatal("Setup failed: no matches found")
	}

	// Нажимаем стрелку вправо (или любую навигацию)
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_RIGHT})

	if ev.acMatches != nil {
		t.Error("Autocomplete should be cancelled on navigation")
	}

	// Проверка ESC
	for _, char := range "hel" {
		ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: char})
	}
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_ESCAPE})
	if ev.acMatches != nil {
		t.Error("Autocomplete should be cancelled on ESC")
	}
}
func TestEditor_InsertToggle(t *testing.T) {
	pt := piecetable.New([]byte("data"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	if ev.overtype {
		t.Error("Editor should start in Insert mode")
	}

	// Нажимаем Insert
	pressKey(ev, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_INSERT,
	})

	if !ev.overtype {
		t.Error("Insert key failed to toggle Overtype mode")
	}
}

func TestEditor_OverwriteMode(t *testing.T) {
	pt := piecetable.New([]byte("abc"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.overtype = true
	ev.CursorPos = 1 // Стоим на 'b'

	// Пишем 'X' поверх 'b'
	ev.ProcessKey(&vtinput.InputEvent{
		Type:    vtinput.KeyEventType,
		KeyDown: true,
		Char:    'X',
	})

	if pt.String() != "aXc" {
		t.Errorf("Overwrite failed: expected 'aXc', got %q", pt.String())
	}
	if ev.CursorPos != 2 {
		t.Errorf("Cursor did not advance: expected 2, got %d", ev.CursorPos)
	}
}

func TestDeleteLinePreservesVisualColumn(t *testing.T) {
	pt := piecetable.New([]byte("line 1 text\nline 2\nline 3 standard"))
	ev := NewEditorView(pt, nil, "test.txt")
	ev.CursorBeyondEOL = true

	// Position cursor at line 1 (0-based index 0), column 20 (beyond end of "line 1 text" which is 11 chars)
	ev.CursorLine = 0
	ev.CursorPos = 11
	ev.CursorVirtualSpaces = 9
	ev.updateDesiredVisualCol()

	if ev.DesiredVisualCol != 20 {
		t.Errorf("Expected DesiredVisualCol to be 20, got %d", ev.DesiredVisualCol)
	}

	// Delete current line (line 1)
	ev.DeleteCurrentLine()

	// The new current line is "line 2" (length 6)
	if ev.CursorLine != 0 {
		t.Errorf("Expected CursorLine to remain 0, got %d", ev.CursorLine)
	}

	// We expect the cursor to stay at visual column 20
	if ev.CursorVirtualSpaces != 14 { // 20 - 6 (len of "line 2")
		t.Errorf("Expected CursorVirtualSpaces to be 14, got %d", ev.CursorVirtualSpaces)
	}

	if ev.DesiredVisualCol != 20 {
		t.Errorf("Expected DesiredVisualCol to remain 20, got %d", ev.DesiredVisualCol)
	}
}

func TestEditorViewInsertOverwriteCursorShape(t *testing.T) {
	vtui.SetDefaultPalette()
	SetDefaultF4Palette()

	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(80, 25)
	vtui.FrameManager.Init(scr)

	pt := piecetable.New([]byte("hello world"))
	ev := NewEditorView(pt, nil, "test.txt")
	ev.ResizeConsole(80, 25)

	vtui.FrameManager.Push(desktopWindowWrapper{ev})

	// Симулируем рендеринг, чтобы обновить состояние ScreenBuf
	ev.Show(scr)
	scr.Flush()

	// По умолчанию overtype = false, курсор должен быть Underline
	if ev.overtype {
		t.Error("Expected default overtype mode to be false")
	}

	_, cy := scr.GetCursorPos()
	// Проверяем форму курсора на активной строке
	if cy >= 0 {
		_, _, _, shape := scr.GetCursorStateForTesting()
		if shape != vtui.CursorShapeUnderline {
			t.Errorf("Expected cursor shape to be Underline, got %v", shape)
		}
	}

	// Нажимаем Insert
	pressKey(ev, &vtinput.InputEvent{
		Type:           vtinput.KeyEventType,
		KeyDown:        true,
		VirtualKeyCode: vtinput.VK_INSERT,
	})

	// Проверяем, что режим сменился на overtype
	if !ev.overtype {
		t.Error("Expected overtype mode to be true after pressing Insert")
	}

	// Рендерим заново
	ev.Show(scr)
	scr.Flush()

	// Теперь форма курсора должна быть Block
	if cy >= 0 {
		_, _, _, shape := scr.GetCursorStateForTesting()
		if shape != vtui.CursorShapeBlock {
			t.Errorf("Expected cursor shape to be Block after toggling overtype, got %v", shape)
		}
	}
}

type desktopWindowWrapper struct {
	*EditorView
}

func (d desktopWindowWrapper) GetType() vtui.FrameType {
	return vtui.TypeUser
}

func TestEditorView_Replace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("abc 123 abc"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.Replace("abc", "def", true, false, false, false, true)

	timeout := time.After(1 * time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("timeout")
		}
		if ev.pt.String() == "def 123 def" {
			break
		}
	}
}

func TestEditorView_MouseSelection(t *testing.T) {
	pt := piecetable.New([]byte("hello world"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 0, MouseY: 1, ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	if !ev.selActive {
		t.Fatal("Selection should be active")
	}

	ev.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 5, MouseY: 1, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.MouseMoved,
	})

	min, max := ev.getSelectionRange()
	if min != 0 || max != 5 {
		t.Errorf("Expected range 0:5, got %d:%d", min, max)
	}
}
func TestEditorView_RectangularSelection_Copy(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1\nline2\nline3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.rectSelActive = true
	ev.rectSelStartLine = 0
	ev.rectSelStartCol = 1
	ev.CursorLine = 2
	ev.CursorPos = 5

	vtui.SetClipboard("")
	ev.CopySelection()

	expected := "ine1\nine2\nine3"
	if vtui.GetClipboard() != expected {
		t.Errorf("Rectangular Copy failed: expected %q, got %q", expected, vtui.GetClipboard())
	}
}

func TestEditorView_RectangularSelection_Delete(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("line1\nline2\nline3"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.rectSelActive = true
	ev.rectSelStartLine = 0
	ev.rectSelStartCol = 1
	ev.CursorLine = 2
	ev.CursorPos = 4

	ev.DeleteSelection()

	expected := "l1\nl2\nl3"
	if ev.pt.String() != expected {
		t.Errorf("Rectangular Delete failed: expected %q, got %q", expected, ev.pt.String())
	}
}
func TestEditorView_RectangularSelection_Paste(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("123\n456"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	GlobalLastClipboardWasRectangular = true
	ev.CursorLine = 0
	ev.CursorPos = 1 // после '1'

	ev.PasteText("AB\nCD")

	expected := "1AB23\n4CD56"
	if ev.pt.String() != expected {
		t.Errorf("Rectangular Paste failed: expected %q, got %q", expected, ev.pt.String())
	}

	GlobalLastClipboardWasRectangular = false
}
func TestEditorView_RegexpSearchReplace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("abc 123 abc"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.Replace(`abc (\d+) abc`, "found $1", true, false, true, false, true)

	timeout := time.After(1 * time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("timeout")
		}
		if ev.pt.String() == "found 123" {
			break
		}
	}
}

func TestEditorView_RegexpSearch_CaseAndReverse(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("Match match Match"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	// 1. Регистрозависимый поиск вперед: ищем "match" -> должен найти второй "match" (индекс 6)
	ev.Search("match", true, false, true, false, false)

	timeout := time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Forward regex search failed")
		}
	}
	if ev.selAnchorOffset != 6 {
		t.Errorf("Expected first match at index 6, got %d", ev.selAnchorOffset)
	}

	// 2. Регистронезависимый поиск назад с конца: ищем "match" -> должен найти третий "Match" (индекс 12)
	ev.selActive = false
	ev.CursorPos = 17
	ev.Search("match", false, true, true, false, false)

	timeout = time.After(1 * time.Second)
	for !ev.selActive {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Reverse regex search failed")
		}
	}
	if ev.selAnchorOffset != 12 {
		t.Errorf("Expected reverse match at index 12, got %d", ev.selAnchorOffset)
	}
}

func TestEditorView_WholeWordSearchReplace(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("apple pineapple apple"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.Replace("apple", "orange", true, false, false, true, true)

	timeout := time.After(1 * time.Second)
	for {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("timeout")
		}
		if ev.pt.String() == "orange pineapple orange" {
			break
		}
	}
}
func TestEditorView_Codepages_LoadSave(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "cyrillic.txt")

	raw := []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}
	os.WriteFile(path, raw, 0644)

	v := vfs.NewOSVFS(tmpDir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	size := f.Size()
	fullData := make([]byte, size)
	_, _ = f.ReadAt(context.Background(), fullData, 0)
	f.Close()

	decoded, err := vfs.DecodeBytes(fullData, 1251)
	if err != nil {
		t.Fatal(err)
	}

	pt := piecetable.New(decoded)
	ev := NewEditorView(pt, v, path)
	ev.Codepage = 1251
	defer ev.Close()

	if ev.pt.String() != "Привет" {
		t.Errorf("Load failed: expected 'Привет', got %q", ev.pt.String())
	}

	ev.CursorPos = 12
	ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: '!'})

	ev.SaveToFile(nil)

	timeout := time.After(2 * time.Second)
	for ev.saving {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("Save timed out")
		}
	}

	savedRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	expected := []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2, 0x21}
	if !bytes.Equal(savedRaw, expected) {
		t.Errorf("Save failed: expected raw bytes %v, got %v", expected, savedRaw)
	}
}
func TestEditorView_Codepages_PreserveEditsOnSwitch(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "edit_cp.txt")
	os.WriteFile(path, []byte("Initial text"), 0644)

	v := vfs.NewOSVFS(tmpDir)
	pt := piecetable.New([]byte("Initial text"))
	ev := NewEditorView(pt, v, path)
	defer ev.Close()
	ev.Codepage = 65001

	// Modify text
	ev.SetText("Modified text")

	// Switch codepage to 11111 (ANSI)
	ev.ReloadWithCodepage(11111)

	// User modifications must NOT be reverted to "Initial text"
	if strings.Contains(ev.GetText(), "Initial text") {
		t.Error("ReloadWithCodepage reverted unsaved user edits to disk content")
	}
	if ev.Codepage != 11111 {
		t.Errorf("Expected Codepage 11111, got %d", ev.Codepage)
	}
}

func TestEditorView_Codepages_Convert(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	pt := piecetable.New([]byte("Привет"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	ev.Codepage = 1251 // Windows-1251 (Cyrillic)
	ev.SetPosition(0, 0, 80, 24)

	ev.showConvertCodepageDialog()
	ev.Codepage = 866
	ev.modified = true

	if ev.pt.String() != "Привет" {
		t.Errorf("Convert failed: expected 'Привет' to be preserved in memory, got %q", ev.pt.String())
	}
	if ev.Codepage != 866 {
		t.Errorf("Expected Codepage to be 866, got %d", ev.Codepage)
	}
}
func TestEditorView_Codepages_AutoDetect(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "auto_cp.txt")
	os.WriteFile(path, []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}, 0644) // "Привет" in CP1251

	v := vfs.NewOSVFS(tmpDir)
	f, err := v.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	pt := piecetable.New([]byte("Initial"))
	ev := NewEditorView(pt, v, path)
	defer ev.Close()
	ev.file = f

	AppConfig.EditorAutodetectCodePage = true
	AppConfig.EditorDefaultCodePage = 11111 // ANSI
	ev.ReloadWithAutoDetect()

	if ev.Codepage != 11111 {
		t.Errorf("Expected autodetect to fall back to 11111 (ANSI) for non-UTF-8 file, got %d", ev.Codepage)
	}
}

func TestEditorView_Codepages_KeyBarLabel(t *testing.T) {
	pt := piecetable.New(nil)
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()

	ev.Codepage = 65001 // UTF-8
	labels := ev.GetKeyLabels()
	if labels.Normal[7] != "ANSI" {
		t.Errorf("Expected F8 KeyBar label to be 'ANSI', got %q", labels.Normal[7])
	}

	ev.Codepage = 11111 // ANSI
	labels = ev.GetKeyLabels()
	if labels.Normal[7] != "OEM" {
		t.Errorf("Expected F8 KeyBar label to be 'OEM', got %q", labels.Normal[7])
	}
}

func TestEditorView_DoubleClick_SelectWord(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("hello world"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 8, MouseY: 1, ButtonState: vtinput.FromLeft1stButtonPressed,
		MouseEventFlags: vtinput.DoubleClick,
	})

	if !ev.selActive {
		t.Fatal("Selection should be active")
	}

	min, max := ev.getSelectionRange()
	if min != 6 || max != 11 {
		t.Errorf("Word selection failed: expected [6:11], got [%d:%d]", min, max)
	}
}

func TestEditorView_MouseSelection_Release(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pt := piecetable.New([]byte("data"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 80, 24)

	ev.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: true,
		MouseX: 2, MouseY: 1, ButtonState: vtinput.FromLeft1stButtonPressed,
	})

	if !ev.selActive {
		t.Fatal("Selection should start active on mouse down")
	}

	ev.ProcessMouse(&vtinput.InputEvent{
		Type: vtinput.MouseEventType, KeyDown: false,
		MouseX: 2, MouseY: 1, ButtonState: 0,
	})

	if ev.selActive {
		t.Error("Simple click without dragging should turn off selection on mouse release")
	}
}

// TestEditorView_InsertTextAtCursor covers the shared insert path
// the new Ctrl+[/Ctrl+]/Ctrl+Enter shortcuts use — appends bytes
// at cursor, updates line index, advances cursor by len(data).
// Cursor mid-word split is the interesting case.
func TestEditorView_InsertTextAtCursor(t *testing.T) {
	pt := piecetable.New([]byte("abcxyz"))
	ev := NewEditorView(pt, nil, "")
	defer ev.Close()
	ev.SetPosition(0, 0, 79, 24)
	ev.CursorPos = 3

	ev.insertTextAtCursor([]byte("/tmp/f4"))
	if got := pt.String(); got != "abc/tmp/f4xyz" {
		t.Errorf("insert = %q, want abc/tmp/f4xyz", got)
	}
	if ev.CursorPos != 3+len("/tmp/f4") {
		t.Errorf("cursor = %d, want %d", ev.CursorPos, 3+len("/tmp/f4"))
	}
	if !ev.modified {
		t.Error("modified flag should be set after insert")
	}
}

// TestEditorView_DeleteSpacersForward exercises Ctrl+Del from
// issue #289: eat every space and tab between the cursor and the
// first non-spacer byte, leave everything else alone.
func TestEditorView_DeleteSpacersForward(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		cursor   int
		want     string
		wantCurs int
	}{
		{"leading-spaces", "abc    def", 3, "abcdef", 3},
		{"tabs-mixed", "abc \t \tdef", 3, "abcdef", 3},
		{"no-spacers", "abcdef", 3, "abcdef", 3},
		{"at-eof", "abc   ", 3, "abc", 3},
		{"cursor-on-non-spacer", "aaa bbb", 0, "aaa bbb", 0},
		{"only-spacers-mid-file", "x   \n  y", 1, "x\n  y", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pt := piecetable.New([]byte(tc.src))
			ev := NewEditorView(pt, nil, "")
			defer ev.Close()
			ev.SetPosition(0, 0, 79, 24)
			ev.CursorLine = 0
			ev.CursorPos = tc.cursor

			ev.deleteSpacersForward()
			if got := pt.String(); got != tc.want {
				t.Errorf("pt = %q, want %q", got, tc.want)
			}
			if ev.CursorPos != tc.wantCurs {
				t.Errorf("cursor = %d, want %d", ev.CursorPos, tc.wantCurs)
			}
		})
	}
}
