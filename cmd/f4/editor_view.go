package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/charlievieth/strcase"
	"github.com/coregx/coregex"
	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/textlayout"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

var (
	LastEditorSearch          string
	LastEditorReplace         string
	LastEditorSearchCase      bool
	LastEditorSearchReverse   bool
	LastEditorSearchRegexp    bool
	LastEditorSearchWholeWord bool
	LastEditorSearchHex       bool
)
var GlobalLastClipboardWasRectangular bool

// EditorView is a text editor component.
type EditorView struct {
	vtui.BaseFrame
	topBar  *TopBar
	menuBar *vtui.MenuBar
	pt      *piecetable.PieceTable
	li      *piecetable.LineIndex
	engine  *textlayout.WrapEngine

	ScrollTopRow int // Индекс первой видимой ВИЗУАЛЬНОЙ строки
	ScrollLeft   int // Горизонтальный скролл (когда WordWrap=false)

	WordWrap           bool
	wordWrapSuppressed bool // Unsafe binary/long-line content forbids re-enabling wrapping.
	binaryFile         bool // Binary files stay editable as text, but syntax parsers must not scan them.
	HexMode            bool
	DecodeMode         bool
	HexTopOffset       int
	HexNibble          int // 0 = high nibble, 1 = low nibble
	DisasmMode         int // 16, 32 or 64; 0 while undecided (see disasm.go)
	overtype           bool
	modified           bool
	closeDlg           *vtui.Window
	CursorLine         int // Текущая логическая строка (для плагинов)
	CursorPos          int // Позиция в байтах (для плагинов)
	DesiredVisualCol   int // Колонка, в которую мы хотим попасть при навигации Up/Down

	ShowWhitespaces    bool
	selActive          bool
	selAnchorOffset    int // Абсолютное смещение начала выделения
	rectSelActive      bool
	rectSelStartLine   int
	rectSelStartCol    int
	mouseRectSelecting bool
	hoverURL           string
	hoverURLStart      int
	editSession        int // Unique ID to fence background tasks

	pasting     bool
	saving      bool
	edited      bool
	pasteBuffer []rune
	asyncBuf    *AsyncBuffer
	// mapped is the file itself, mapped read-only, when the editor could take
	// that route: the piece table's original buffer is then a window onto it
	// rather than a copy assembled from chunks. Nil for everything else.
	mapped *MappedFile
	// mapFaulted records that reading through the mapping has already failed
	// once, so the report goes out once rather than on every repaint.
	mapFaulted  bool
	indexCancel context.CancelFunc
	// indexWG joins cancelled indexers before Close tears down their buffers.
	indexWG sync.WaitGroup
	// indexing is true from StartIndexing until that run ends, by completion
	// or by cancellation. indexCancel cannot answer the question: it is left
	// set after a normal finish, so it reads as "indexing forever".
	indexing bool
	// indexStatus and its subscribers are touched only on the UI thread: the
	// scan reports through tasks posted there, and the paint path reads it.
	indexStatus     IndexStatus
	indexSubs       map[int]func(IndexStatus)
	indexSubID      int
	indexNotifiedAt time.Time
	// indexResume debounces restarting the scan after an edit, so that typing
	// does not start and cancel a goroutine per keystroke.
	indexResume *time.Timer

	// searchSnapshot caches the assembled buffer one search pass works on,
	// for the buffers that cannot be scanned in place. Every search used to
	// rebuild it, which on a large file is the whole file copied again per
	// F7 and, worse, per confirmation of an interactive Replace. It is keyed
	// by editSession, so any edit retires it. The mutex is what makes it
	// readable from the background scan goroutines.
	searchSnapMu      sync.Mutex
	searchSnapshot    []byte
	searchSnapSession int

	renderBytes []byte          // Reusable buffer for text data
	renderCells []vtui.CharInfo // Reusable buffer for row rendering

	vfs         vfs.VFS
	filePath    string
	file        vfs.ReadAtCloser
	scrollBar   *vtui.ScrollBar
	highlighter vtui.Highlighter
	lineStates  []any // Cache of highlighter states per logical line

	// Highlighting arrives late and all at once; these ease it in. fadeReg
	// pins one heartbeat animation per viewer.
	syntaxFadeStart time.Time
	fadeBuf         []uint64
	fadeReg         bool

	// Undo/Redo
	undoStack  []editorState
	redoStack  []editorState
	inGroup    bool
	lastOp     undoOpType
	cleanState piecetable.TableState // State of the file on disk
	// unsavedBaseline keeps a create-new buffer dirty until its first
	// successful save, even when edit + Undo restores the supplied content.
	unsavedBaseline bool
	// createNewTarget forbids replacing a path that appeared after this
	// editor was opened. Plugin-generated reports use this for their first
	// save; normal editors continue to replace the file they opened.
	createNewTarget bool
	// utf8BOM records that the source file had a UTF-8 BOM. The marker is
	// hidden from the logical editor buffer but is preserved when saving.
	utf8BOM bool
	// omitUnicodeBOM is the Save As dialog's BOM checkbox turned off for a
	// UTF-16/UTF-32 codepage, whose encoders otherwise always write one.
	omitUnicodeBOM bool

	// Autocomplete state
	acEnabled    bool
	acPrefix     string
	acMatches    []string
	acCurrentIdx int

	targetLine   int
	targetPos    int
	targetTopRow int
	targetLeft   int
	// targetOffset is a position known only as a byte offset — where a viewer
	// was looking, or where a hex cursor sat — for a buffer whose index has
	// not reached it. The scan turns it into a line and a column when it reads
	// past it; -1 is none. It is the byte-offset twin of targetLine, and only
	// one of the two is ever set.
	targetOffset int
	Codepage     int
	// codepageRaw is the byte stream that codepage switching reinterprets.
	// The piece table stores decoded UTF-8 text, so encoding that text through
	// every intermediate codepage makes a cycle lossy (especially UTF-8 ->
	// ANSI -> OEM -> UTF-8). Keep one source snapshot until the buffer changes.
	codepageRaw []byte
	// DisplayTitle overrides the temporary filename in frame and top-bar
	// titles. It is used by internal editor round-trip workflows.
	DisplayTitle string

	TabSize             int
	ExpandTabs          int
	AutoIndent          bool
	CursorBeyondEOL     bool
	CursorVirtualSpaces int
	UseEditorConfig     bool

	highlighting    bool
	highlightCancel context.CancelFunc

	// Colorer parses outside the UI goroutine. These fields are UI-owned and
	// make an in-flight parse visible/cancellable without blocking rendering.
	colorerIndexing bool
	colorerProgress int
	colorerTotal    int
	colorerCancel   func()
	colorerWorkID   uint64

	// OnClose, if set, fires once after the editor has been torn down.
	// Used by callers (e.g. the user menu's Ctrl+F4 handler) that want
	// to react to the file content once the user is done editing.
	OnClose func()
}

func (ev *EditorView) ApplyEditorConfig() {
	if !ev.UseEditorConfig || ev.vfs == nil || ev.filePath == "" {
		return
	}

	dir := ev.vfs.Dir(ev.filePath)
	filename := ev.vfs.Base(ev.filePath)

	configPath := ev.vfs.Join(dir, ".editorconfig")
	f, err := ev.vfs.Open(context.Background(), configPath)
	if err != nil {
		return
	}
	defer f.Close()

	data := make([]byte, 64*1024)
	n, _ := f.Read(context.Background(), data)
	content := string(data[:n])

	lines := strings.Split(content, "\n")
	inMatchingSection := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := line[1 : len(line)-1]
			matched, _ := filepath.Match(section, filename)
			if section == "*" || matched {
				inMatchingSection = true
			} else {
				inMatchingSection = false
			}
			continue
		}

		if !inMatchingSection {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			switch key {
			case "indent_style":
				switch val {
				case "space":
					ev.ExpandTabs = 1
				case "tab":
					ev.ExpandTabs = 0
				}
			case "indent_size", "tab_width":
				if size, err := strconv.Atoi(val); err == nil && size > 0 {
					ev.TabSize = size
					ev.engine.SetTabSize(size)
				}
			}
		}
	}
}

type undoOpType int

const (
	opNone undoOpType = iota
	opTyping
	opOther
)

type editorState struct {
	table piecetable.TableState
	line  int
	pos   int
}

func (ev *EditorView) ConfirmClose() bool {
	if !ev.modified {
		return true
	}
	ev.closeDlg = ev.tryClose()
	return false
}

func (ev *EditorView) Close() {
	if GlobalFileState != nil && ev.filePath != "" {
		GlobalFileState.SaveEditorStateAsync(FileStateKey(ev.vfs, ev.filePath), ev.CursorLine, ev.CursorPos, ev.ScrollTopRow, ev.ScrollLeft, ev.WordWrap)
	}
	if ev.highlightCancel != nil {
		ev.highlightCancel()
		ev.highlightCancel = nil
	}
	if ev.indexCancel != nil {
		ev.indexCancel()
	}
	if ev.indexResume != nil {
		ev.indexResume.Stop()
		ev.indexResume = nil
	}
	ev.indexWG.Wait()
	if ev.asyncBuf != nil {
		ev.asyncBuf.Close()
	}
	// Every window the piece table handed out points into the mapping, so this
	// has to come after the background work above has been told to stop. A
	// straggler that still reads is what the fault guards are there for.
	if ev.mapped != nil {
		_ = ev.mapped.Close()
		ev.mapped = nil
	}
	if ev.file != nil {
		ev.file.Close()
	}
	if closer, ok := ev.highlighter.(io.Closer); ok {
		closer.Close()
	}
	var size int
	if ev.pt != nil {
		size = ev.pt.Size()
	}
	ev.undoStack = nil
	ev.redoStack = nil
	ev.lineStates = nil
	ev.renderBytes = nil
	ev.renderCells = nil
	ev.pasteBuffer = nil
	// searchSnapMu owns searchSnapshot: a search pass still assembling its
	// buffer writes it from its own goroutine, so clearing it bare races that
	// write even when the search is on its way out.
	ev.dropSearchSnapshot()
	ev.fadeBuf = nil
	ev.scrollBar = nil
	ev.BaseFrame.Close()
	if ev.OnClose != nil {
		ev.OnClose()
	}
	ReleaseHeavyMemory(int64(size))
}

func NewEditorView(pt *piecetable.PieceTable, v vfs.VFS, path string) *EditorView {
	return newEditorView(pt, v, path, true, true)
}

// NewEditorViewIndexedLater builds an editor with an empty line index, for a
// file whose index StartIndexing owns: one backed by a mapping or by a chunk
// buffer.
//
// Building it here would mean reading the whole file before the editor can
// appear, on the thread that would have to draw it — which is what opening a
// mapped 8 GB file used to do, for twenty seconds. The background scan reads
// the same bytes without holding the UI, reports progress, and can be
// cancelled; there is no reason to do the work twice, and every reason not to
// do it here.
func NewEditorViewIndexedLater(pt *piecetable.PieceTable, v vfs.VFS, path string) *EditorView {
	return newEditorView(pt, v, path, true, false)
}

// editorBufferHasNUL is the cheap constructor-time binary hint. The opener's
// 16 KiB probe remains authoritative for files loaded from disk; this probe
// also covers editors constructed directly in tests or by plugins.
func editorBufferHasNUL(pt *piecetable.PieceTable) bool {
	if pt == nil || pt.Size() == 0 {
		return false
	}
	take := min(16*1024, pt.Size())
	data, err := pt.GetRange(0, take)
	return err == nil && bytes.IndexByte(data, 0) >= 0
}

func newEditorView(pt *piecetable.PieceTable, v vfs.VFS, path string, useEditorConfig, buildIndex bool) *EditorView {
	li := piecetable.NewLineIndex()
	if buildIndex {
		li.Rebuild(pt)
	}
	ev := &EditorView{
		pt:              pt,
		li:              li,
		engine:          textlayout.NewWrapEngine(pt, li),
		vfs:             v,
		filePath:        path,
		WordWrap:        false,
		ShowWhitespaces: false,
		cleanState:      pt.GetState(),
		targetLine:      -1,
		targetOffset:    -1,
		targetPos:       -1,
		targetTopRow:    -1,
		targetLeft:      -1,
		TabSize:         AppConfig.EditorTabSize,
		ExpandTabs:      AppConfig.EditorExpandTabs,
		AutoIndent:      AppConfig.EditorAutoIndent,
		CursorBeyondEOL: AppConfig.EditorCursorBeyondEOL,
		UseEditorConfig: useEditorConfig && AppConfig.EditorUseEditorConfig,
		Codepage:        65001,
		binaryFile:      editorBufferHasNUL(pt),
	}
	if ev.TabSize <= 0 {
		ev.TabSize = 8
	}
	ev.engine.SetTabSize(ev.TabSize)
	ev.ApplyEditorConfig()
	// Determine if AC should be enabled for this file
	ev.acEnabled = false
	if AppConfig.EditorAutoComplete && path != "" {
		masks := strings.Split(AppConfig.EditorAutoCompleteMask, ";")
		fileName := strings.ToLower(filepath.Base(path))
		for _, mask := range masks {
			mask = strings.TrimSpace(mask)
			if mask == "" {
				continue
			}
			matched, _ := filepath.Match(strings.ToLower(mask), fileName)
			if matched {
				ev.acEnabled = true
				break
			}
		}
	}
	switch {
	case strings.EqualFold(AppConfig.EditorHighlighter, "None"):
		ev.highlighter = nil
	case strings.EqualFold(AppConfig.EditorHighlighter, "Colorer") && ev.binaryFile:
		// A binary remains editable in text mode, but feeding its bytes to a
		// syntax parser can turn one long line into an unbounded CPU task.
		ev.highlighter = nil
	case strings.EqualFold(AppConfig.EditorHighlighter, "Colorer") && SchemasExist():
		firstLine := ""
		if probeLen := pt.Size(); probeLen > 0 {
			if probeLen > 1024 {
				probeLen = 1024
			}
			b, _ := pt.GetRange(0, probeLen)
			firstLine = string(b)
			if idx := strings.IndexAny(firstLine, "\r\n"); idx >= 0 {
				firstLine = firstLine[:idx]
			}
		}
		ev.highlighter = newColorerHighlighter(ev, filepath.Base(path), firstLine, vtui.GetHighlighter(path, ""))
	default:
		ev.highlighter = vtui.GetHighlighter(path, "")
	}
	if ch, ok := ev.highlighter.(*ColorerHighlighter); ok {
		ch.beginColorerStartup()
	}
	vtui.DebugLog("EDITOR_INIT: Path=%q, Highlighter=%T", path, ev.highlighter)
	ev.scrollBar = vtui.NewScrollBar(0, 0, 0)
	ev.scrollBar.ColorIdx = ColEditorScrollbar
	ev.scrollBar.SetOwner(ev)
	ev.scrollBar.OnScroll = func(v int) {
		if ev.HexMode || ev.DecodeMode {
			if ev.HexMode {
				ev.HexTopOffset = v &^ 0xF
			} else {
				ev.HexTopOffset = v
			}
			vtui.FrameManager.Redraw()
			return
		}
		ev.ScrollTopRow = v
		height := ev.Y2 - ev.Y1
		if height > 0 {
			startLogLine, _ := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow)
			endLogLine, _ := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow + height - 1)
			if ev.CursorLine < startLogLine {
				ev.CursorLine = startLogLine
				ev.CursorPos = 0
				ev.updateDesiredVisualCol()
			} else if ev.CursorLine > endLogLine {
				ev.CursorLine = endLogLine
				ev.CursorPos = 0
				ev.updateDesiredVisualCol()
			}
		}
		vtui.FrameManager.Redraw()
	}
	ev.menuBar = vtui.NewMenuBar(nil)

	ev.topBar = NewTopBar(
		func() string {
			base := ""
			if ev.DisplayTitle != "" {
				base = ev.DisplayTitle
			} else {
				base = displayFileTitle(ev.vfs, ev.filePath)
			}
			return " " + base
		},
		func() string { return ev.editorStatusText() },
	)
	ev.topBar.ColorIdx = ColEditorStatus
	ev.topBar.SetVisible(true)
	ev.SetCanFocus(true)
	ev.SetFocus(true)
	return ev
}

// GetTopBar возвращает верхнюю панель для тестов
func (ev *EditorView) GetTopBar() *TopBar {
	return ev.topBar
}

// SetText replaces the entire content of the editor.
func (ev *EditorView) SetText(text string) {
	ev.cancelIndexing()
	ev.edited = true
	ev.retireEditSession()
	ev.codepageRaw = nil

	ev.pt = piecetable.New([]byte(text))
	ev.noteIndexRebuilt(ev.li.Rebuild(ev.pt))
	ev.CursorLine = 0
	ev.CursorPos = 0
	ev.engine.SetPointers(ev.pt, ev.li)
	ev.modified = true
}

func (ev *EditorView) clearCaches() {
	ev.engine.InvalidateCache()
	// Undo, redo and a reload replace the text wholesale. Colorer caches
	// colours by line number and has no way to notice that on its own.
	if ch, ok := ev.highlighter.(*ColorerHighlighter); ok {
		ch.DropFrom(0)
	}
}
func (ev *EditorView) saveUndo(op undoOpType) {
	if ev.inGroup {
		return
	}

	ev.redoStack = nil // Redo stack MUST be cleared on any new modification

	// If we are about to modify a selection, the "home" position for Undo
	// is the start of that selection.
	line, pos := ev.CursorLine, ev.CursorPos
	if ev.selActive {
		minOff, _ := ev.getSelectionRange()
		line = ev.li.GetLineAtOffset(minOff)
		pos = minOff - ev.li.GetLineOffset(line)
	}

	state := editorState{
		table: ev.pt.GetState(),
		line:  line,
		pos:   pos,
	}

	// Simple grouping for typing: don't push new state if we are just typing characters consecutively
	if op == opTyping && ev.lastOp == opTyping && len(ev.undoStack) > 0 {
		return
	}

	ev.undoStack = append(ev.undoStack, state)
	if len(ev.undoStack) > 1000 {
		ev.undoStack = ev.undoStack[1:]
	}
	ev.lastOp = op
	ev.modified = true // Mark as dirty, will be re-evaluated on Undo/Redo
}

func (ev *EditorView) Undo() {
	if len(ev.undoStack) == 0 {
		vtui.DebugLog("EDITOR: Undo called but stack is empty")
		return
	}

	ev.edited = true
	ev.codepageRaw = nil
	ev.cancelIndexing()
	ev.retireEditSession()

	// Save current state to redo stack
	ev.redoStack = append(ev.redoStack, editorState{
		table: ev.pt.GetState(),
		line:  ev.CursorLine,
		pos:   ev.CursorPos,
	})

	// Restore last state
	last := len(ev.undoStack) - 1
	state := ev.undoStack[last]
	ev.undoStack = ev.undoStack[:last]

	ev.pt.LoadState(state.table)
	ev.noteIndexRebuilt(ev.li.Rebuild(ev.pt))
	ev.CursorLine = state.line
	ev.CursorPos = state.pos

	ev.clearCaches()
	// Intelligent modified flag: if structure matches clean state, it's not modified
	ev.modified = ev.unsavedBaseline || !ev.pt.GetState().Equals(ev.cleanState)
	ev.lastOp = opNone
	ev.ensureCursorVisible()
	vtui.DebugLog("EDITOR: Executed Undo, remaining: %d, modified: %v", len(ev.undoStack), ev.modified)
}

func (ev *EditorView) Redo() {
	if len(ev.redoStack) == 0 {
		vtui.DebugLog("EDITOR: Redo called but stack is empty")
		return
	}

	ev.edited = true
	ev.codepageRaw = nil
	ev.cancelIndexing()
	ev.retireEditSession()

	// Save current state to undo stack
	ev.undoStack = append(ev.undoStack, editorState{
		table: ev.pt.GetState(),
		line:  ev.CursorLine,
		pos:   ev.CursorPos,
	})

	last := len(ev.redoStack) - 1
	state := ev.redoStack[last]
	ev.redoStack = ev.redoStack[:last]

	ev.pt.LoadState(state.table)
	ev.noteIndexRebuilt(ev.li.Rebuild(ev.pt))
	ev.CursorLine = state.line
	ev.CursorPos = state.pos

	ev.clearCaches()
	// Intelligent modified flag
	ev.modified = ev.unsavedBaseline || !ev.pt.GetState().Equals(ev.cleanState)
	ev.lastOp = opNone
	ev.ensureCursorVisible()
	vtui.DebugLog("EDITOR: Executed Redo, remaining: %d, modified: %v", len(ev.redoStack), ev.modified)
}
func (ev *EditorView) invalidateStates(fromLine int) {
	if ev.highlightCancel != nil {
		ev.highlightCancel()
		ev.highlightCancel = nil
	}
	if fromLine < len(ev.lineStates) {
		ev.lineStates = ev.lineStates[:fromLine]
	}
	if ch, ok := ev.highlighter.(*ColorerHighlighter); ok {
		ch.DropFrom(fromLine)
	}
}

// lineTextForHighlight returns one logical line as the highlighters see it:
// the terminator included, and a long binary line cut short so no parser is
// ever handed a megabyte. Not ok means the text is not available yet — the
// piece table is still loading it — and the caller should leave the line plain
// and come back on the next frame.
func (ev *EditorView) lineTextForHighlight(idx int) (string, bool) {
	if idx < 0 || idx >= ev.li.LineCount() {
		return "", false
	}
	start := ev.li.GetLineOffset(idx)
	end := ev.pt.Size()
	if idx+1 < ev.li.LineCount() {
		end = ev.li.GetLineOffset(idx + 1)
	}
	if end-start > 64*1024 {
		end = start + 64*1024
	}
	if end <= start {
		return "", true
	}
	data, err := ev.pt.GetRange(start, end-start)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// Background highlighting runs in slices on the UI thread: highlighters are
// not thread safe and the Colorer session is a pooled external resource. What
// a slice may cost is therefore a wall-clock budget, not a line count. The
// same 2500 lines are a few milliseconds of Chroma and roughly a hundred of
// Colorer, so a fixed count is either a wasted frame or a visible freeze,
// depending on which highlighter happens to be selected.
const (
	// hlSliceBudget is the longest stall one slice may put on the UI thread.
	hlSliceBudget = 4 * time.Millisecond
	// hlClockStride is how many lines pass between two clock readings inside
	// a slice. Reading the clock per line is measurable next to a fast
	// highlighter; eight lines keep the overshoot small even for a slow one.
	hlClockStride = 8
	// Share of wall-clock time, in percent, the walker may occupy.
	hlDutyIndexing = 10 // line index still building: it owns the machine
	hlDutyVisible  = 50 // viewport is waiting for colours, the user sees it
	hlDutyAhead    = 25 // walking past the viewport, nobody is looking yet
	// Bounds for the gap between two slices.
	hlIdleMin = 1 * time.Millisecond
	hlIdleMax = 100 * time.Millisecond
	// A slice that highlighted nothing is waiting for the piece table to
	// load. Retry, but give up after a while and let the next render restart
	// the walker instead of polling the task queue forever.
	hlStallIdle      = 10 * time.Millisecond
	hlMaxStallSlices = 100
)

// hlNow feeds the slice budget clock. Tests swap it for a fake advanced by
// the highlighter itself, so budget behavior is asserted deterministically
// instead of against the wall clock of a loaded CI machine.
var hlNow = time.Now

// highlightSlicePlan is what one slice did and when the next one may start.
// It crosses from the UI thread back to the walker goroutine, which is why
// every view field the decision depends on is read inside the slice itself.
type highlightSlicePlan struct {
	done  bool
	lines int
	work  time.Duration
	idle  time.Duration
}

// highlightDuty reports the share of wall-clock time the walker may spend on
// the UI thread.
//
// While the line index is still being built the walker yields to it. The
// scroll bar, Ctrl+End and any restore of a saved position all wait for the
// index, and on a 38MB log that wait is what the user actually notices;
// colours arriving a few seconds later are not.
func highlightDuty(behindViewport, indexing bool) int {
	switch {
	case indexing:
		return hlDutyIndexing
	case behindViewport:
		return hlDutyVisible
	default:
		return hlDutyAhead
	}
}

// highlightIdleGap turns the time a slice spent into the pause that keeps the
// walker at the requested duty cycle.
func highlightIdleGap(work time.Duration, duty int) time.Duration {
	if duty >= 100 {
		return 0
	}
	if duty <= 0 {
		return hlIdleMax
	}
	idle := work * time.Duration(100-duty) / time.Duration(duty)
	if idle < hlIdleMin {
		idle = hlIdleMin
	}
	if idle > hlIdleMax {
		idle = hlIdleMax
	}
	return idle
}

// usesStateChain reports whether a highlighter's state can be carried in
// ev.lineStates at all.
//
// Colorer's cannot. What it returns is the line number; the parser state lives
// inside the wasm session as a cache that only moves forward. Walking the file
// for it therefore builds nothing, and it is not free: every line handed to
// the session is copied into its line source and grows the parse cache beside
// it, and the walk leaves the session parked ahead of the viewport, so the
// next frame has to rewind it to draw. The session belongs to whoever is
// drawing. See HIGHLIGHT.md, phase 5.
func usesStateChain(h vtui.Highlighter) bool {
	if h == nil {
		return false
	}
	_, isColorer := h.(*ColorerHighlighter)
	return !isColorer
}

// highlightSlice extends the state chain for at most hlSliceBudget and
// reports what it managed. It runs on the UI thread, so the budget is exactly
// the stall the user can feel; how many lines fit into it is left to the
// highlighter.
func (ev *EditorView) highlightSlice(bgAttr uint64) highlightSlicePlan {
	var plan highlightSlicePlan

	// The highlighter can be replaced under a running walker: the Colorer
	// session finishes loading in the background and takes the place of the
	// fallback it started with.
	if !usesStateChain(ev.highlighter) {
		plan.done = true
		return plan
	}

	lineCount := ev.li.LineCount()
	cIdx := len(ev.lineStates)
	if cIdx >= lineCount {
		plan.done = !ev.indexing
		return plan
	}

	// Sampled here, on the thread that owns these fields. The previous
	// version read the viewport from the walker goroutine while the UI thread
	// was writing it, and GetLogLineAtVisualRow updates the wrap engine's
	// caches as a side effect.
	startLogLine, _ := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow)
	behind := cIdx < startLogLine

	start := hlNow()
	for cIdx < lineCount {
		lStart := ev.li.GetLineOffset(cIdx)
		lineLen := ev.getLineLength(cIdx)
		if lineLen > 64*1024 {
			lineLen = 64 * 1024
		}

		var prevState any
		if cIdx > 0 {
			prevState = ev.lineStates[cIdx-1]
		}

		lineData, err := ev.pt.GetRange(lStart, lineLen)
		if err == piecetable.ErrLoading {
			break
		}

		_, nextState := ev.highlighter.Highlight(string(lineData), prevState, bgAttr)
		ev.lineStates = append(ev.lineStates, nextState)
		cIdx++
		plan.lines++

		if plan.lines%hlClockStride == 0 && hlNow().Sub(start) >= hlSliceBudget {
			break
		}
	}
	plan.work = hlNow().Sub(start)
	plan.done = cIdx >= ev.li.LineCount() && !ev.indexing

	if plan.lines == 0 {
		plan.idle = hlStallIdle
	} else {
		plan.idle = highlightIdleGap(plan.work, highlightDuty(behind, ev.indexing))
	}

	vStart, _ := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow)
	vEnd := vStart + (ev.Y2 - ev.Y1) + 1
	if cIdx >= vStart && cIdx-plan.lines <= vEnd+400 {
		vtui.FrameManager.Redraw()
	}
	return plan
}

func (ev *EditorView) startHighlighting() {
	if ev.highlighter == nil || ev.highlighting {
		return
	}
	if !usesStateChain(ev.highlighter) {
		return
	}
	if len(ev.lineStates) >= ev.li.LineCount() {
		return
	}

	ev.highlighting = true
	sessionID := ev.editSession

	ctx, cancel := context.WithCancel(context.Background())
	ev.highlightCancel = cancel

	// Taken here rather than inside the goroutine below. Highlighting outlives
	// this call, and reading the global from the worker races anything that
	// reassigns vtui.FrameManager while the pass is still running.
	frames := vtui.FrameManager

	go func() {
		defer func() {
			frames.PostTask(func() {
				ev.highlighting = false
				frames.Redraw()
			})
		}()

		bgAttr := ColorerEditorBaseAttr(vtui.Palette[ColEditorText])

		startedAt := time.Now()
		walked := 0
		stalls := 0
		var uiTime time.Duration

		defer func() {
			vtui.DebugLog("EDITOR: Highlight walker stopped: %d lines, %v on the UI thread, %v wall clock",
				walked, uiTime, time.Since(startedAt))
		}()

		plans := make(chan highlightSlicePlan, 1)

		for {
			if ctx.Err() != nil || ev.IsDone() {
				return
			}

			frames.PostTask(func() {
				plan := highlightSlicePlan{done: true}
				defer func() { plans <- plan }()

				if ctx.Err() != nil {
					return
				}
				if ev.editSession != sessionID || ev.highlighter == nil {
					cancel() // Self-terminate stale worker loop
					return
				}
				plan = ev.highlightSlice(bgAttr)
			})

			var plan highlightSlicePlan
			select {
			case <-ctx.Done():
				return
			case plan = <-plans:
			}

			walked += plan.lines
			uiTime += plan.work

			if plan.lines == 0 {
				stalls++
			} else {
				stalls = 0
			}

			if plan.done || (stalls >= hlMaxStallSlices && !ev.indexing) {
				return
			}
			if ctx.Err() != nil || ev.IsDone() {
				return
			}

			time.Sleep(plan.idle)
		}
	}()
}
func (ev *EditorView) ensureEngineWidth() {
	width := ev.X2 - ev.X1 + 1
	if ev.scrollBar != nil {
		width--
	}
	if width < 1 {
		width = 1
	}
	ev.engine.SetWidth(width)
	ev.engine.ToggleWrap(ev.WordWrap)
}

func (ev *EditorView) updateDesiredVisualCol() {
	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	_, vCol := ev.engine.LogicalToVisual(curOffset)
	ev.DesiredVisualCol = vCol + ev.CursorVirtualSpaces
}
func (ev *EditorView) renderHex(scr *vtui.ScreenBuf, width, contentHeight int) {
	bgAttr := ColorerEditorBaseAttr(vtui.Palette[ColEditorText])
	offAttr := vtui.Palette[ColEditorStatus]
	currOffset := ev.HexTopOffset
	absPos := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos

	for y := 0; y < contentHeight; y++ {
		if currOffset > ev.pt.Size() {
			break
		}
		if currOffset == ev.pt.Size() && currOffset != 0 {
			if absPos != currOffset {
				break
			}
		}

		take := 16
		if currOffset+take > ev.pt.Size() {
			take = ev.pt.Size() - currOffset
		}

		var data []byte
		if take > 0 {
			var err error
			data, err = ev.pt.GetRange(currOffset, take)
			if err == piecetable.ErrLoading {
				scr.Write(ev.X1, ev.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", bgAttr))
				break
			}
		}

		line := fmt.Sprintf("%010X: ", currOffset)
		scr.Write(ev.X1, ev.Y1+1+y, vtui.StringToCharInfo(line, offAttr))

		// Hex part
		for i := 0; i < 16; i++ {
			cx := ev.X1 + 12 + i*3
			if i >= 8 {
				cx++
			}
			if i < len(data) {
				hexStr := fmt.Sprintf("%02X", data[i])
				scr.Write(cx, ev.Y1+1+y, vtui.StringToCharInfo(hexStr, bgAttr))
			}

			// Cursor
			if ev.IsFocused() && currOffset+i == absPos {
				scr.SetCursorPos(cx+ev.HexNibble, ev.Y1+1+y)
				scr.SetCursorVisible(true)
				scr.SetCursorShape(vtui.CursorShapeUnderline)
			}
		}

		// EOF Cursor
		if ev.IsFocused() && currOffset+len(data) == absPos && len(data) < 16 {
			cx := ev.X1 + 12 + len(data)*3
			if len(data) >= 8 {
				cx++
			}
			scr.SetCursorPos(cx+ev.HexNibble, ev.Y1+1+y)
			scr.SetCursorVisible(true)
			scr.SetCursorShape(vtui.CursorShapeUnderline)
		}

		// ASCII part
		asciiStartX := ev.X1 + 12 + 50
		scr.Write(asciiStartX-2, ev.Y1+1+y, vtui.StringToCharInfo("│ ", offAttr))
		for i := 0; i < len(data); i++ {
			r := rune(data[i])
			if r < 32 || r > 126 {
				r = '.'
			}
			cellAttr := bgAttr
			if ev.IsFocused() && currOffset+i == absPos {
				cellAttr = vtui.Palette[vtui.ColDialogEditSelected]
			}
			scr.Write(asciiStartX+i, ev.Y1+1+y, []vtui.CharInfo{{Char: uint64(r), Attributes: cellAttr}})
		}
		currOffset += 16
	}
}
func (ev *EditorView) renderDecode(scr *vtui.ScreenBuf, width, contentHeight int) {
	bgAttr := ColorerEditorBaseAttr(vtui.Palette[ColEditorText])
	offAttr := vtui.Palette[ColEditorStatus]
	currOffset := ev.HexTopOffset
	absPos := int(ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos)

	for y := 0; y < contentHeight; y++ {
		if currOffset >= ev.pt.Size() {
			break
		}
		if currOffset == ev.pt.Size() && currOffset != 0 {
			if absPos != currOffset {
				break
			}
		}

		data, err := ev.decodeBytes(currOffset, disasmMaxInstLen)
		if err == piecetable.ErrLoading {
			scr.Write(ev.X1, ev.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", bgAttr))
			break
		}

		if len(data) == 0 && currOffset < ev.pt.Size() {
			break
		}

		// Bytes the piece table would not hand over still take a line one
		// byte wide, so the walk cannot stall on them.
		instLen := 1
		asmStr := ""
		if len(data) > 0 {
			asmStr, instLen = disasmInstruction(data, ev.disasmMode(), int64(currOffset))
		}

		line := fmt.Sprintf("%010X: ", currOffset)

		cellAttr := bgAttr
		if ev.IsFocused() && absPos >= currOffset && absPos < currOffset+instLen {
			cellAttr = vtui.Palette[vtui.ColDialogEditSelected]
		}

		scr.Write(ev.X1, ev.Y1+1+y, vtui.StringToCharInfo(line, offAttr))

		hexStr := ""
		for i := 0; i < instLen; i++ {
			if i < len(data) {
				hexStr += fmt.Sprintf("%02X", data[i])
				if i < instLen-1 {
					hexStr += " "
				}
			}
		}
		scr.Write(ev.X1+12, ev.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf("%-24s", hexStr), cellAttr))
		scr.Write(ev.X1+38, ev.Y1+1+y, vtui.StringToCharInfo(asmStr, cellAttr))
		scr.FillRect(ev.X1+38+len(asmStr), ev.Y1+1+y, ev.X2, ev.Y1+1+y, ' ', cellAttr)

		if ev.IsFocused() && absPos >= currOffset && absPos < currOffset+instLen {
			byteOffset := absPos - currOffset
			cx := ev.X1 + 12 + byteOffset*3
			scr.SetCursorPos(cx+ev.HexNibble, ev.Y1+1+y)
			scr.SetCursorVisible(true)
			scr.SetCursorShape(vtui.CursorShapeUnderline)
		}

		currOffset += instLen
	}
}

// decodeBytes reads up to n bytes at off for the decode view. GetRange
// answers a range that runs past the end of the buffer with nothing at
// all, so the request is cut to what is there first: an instruction
// window at the last bytes of a file must still see those bytes.
func (ev *EditorView) decodeBytes(off, n int) ([]byte, error) {
	if size := ev.pt.Size(); off+n > size {
		n = size - off
	}
	if n <= 0 {
		return nil, nil
	}
	return ev.pt.GetRange(off, n)
}

// disasmMode returns the processor mode the decode view uses. An editor
// opened without a header (showEditor reads one) decides it here, from the
// buffer's first bytes, the first time an instruction is needed.
func (ev *EditorView) disasmMode() int {
	if !disasmModeValid(ev.DisasmMode) {
		header, _ := ev.decodeBytes(0, 1024)
		ev.DisasmMode = detectX86Mode(header)
	}
	return ev.DisasmMode
}

// cycleDisasmMode switches the decode view to the next processor mode in
// the 64 -> 32 -> 16 -> 64 cycle and returns the mode now in effect. The
// cursor keeps its byte offset; the line it lands on is whatever
// instruction the new mode reads there.
func (ev *EditorView) cycleDisasmMode() int {
	ev.DisasmMode = nextDisasmMode(ev.disasmMode())
	ev.ensureCursorVisible()
	return ev.DisasmMode
}

// decodeStep returns how many bytes the instruction at off occupies in the
// current mode: the distance to the next line of the decode view. It is
// zero at the end of the buffer or while the bytes at off are still being
// fetched.
func (ev *EditorView) decodeStep(off int) int {
	data, _ := ev.decodeBytes(off, disasmMaxInstLen)
	return disasmInstLen(data, ev.disasmMode())
}

func hexCharToByte(c rune) byte {
	if c >= '0' && c <= '9' {
		return byte(c - '0')
	}
	if c >= 'a' && c <= 'f' {
		return byte(c - 'a' + 10)
	}
	if c >= 'A' && c <= 'F' {
		return byte(c - 'A' + 10)
	}
	return 0
}

func (ev *EditorView) processKeyHex(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}
	absPos := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	size := ev.pt.Size()

	syncCursor := func() {
		if absPos < 0 {
			absPos = 0
		}
		if absPos > size {
			absPos = size
		}
		ev.CursorLine = ev.li.GetLineAtOffset(absPos)
		ev.CursorPos = absPos - ev.li.GetLineOffset(ev.CursorLine)
		ev.ensureCursorVisible()
		vtui.FrameManager.Redraw()
	}

	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0

	switch e.VirtualKeyCode {
	case vtinput.VK_LEFT:
		if ev.HexNibble == 1 {
			ev.HexNibble = 0
		} else if absPos > 0 {
			absPos--
			ev.HexNibble = 1
		}
		syncCursor()
		return true
	case vtinput.VK_RIGHT:
		if absPos == size {
			return true
		}
		if ev.HexNibble == 0 {
			ev.HexNibble = 1
		} else if absPos < size {
			absPos++
			ev.HexNibble = 0
		}
		syncCursor()
		return true
	case vtinput.VK_UP:
		if ev.DecodeMode {
			absPos -= 1
		} else {
			absPos -= 16
		}
		if absPos < 0 {
			absPos = 0
		}
		syncCursor()
		return true
	case vtinput.VK_DOWN:
		if ev.DecodeMode {
			absPos += ev.decodeStep(absPos)
		} else {
			absPos += 16
		}
		if absPos > size {
			absPos = size
		}
		syncCursor()
		return true
	case vtinput.VK_HOME:
		if ctrl {
			absPos = 0
		} else {
			absPos = absPos &^ 0xF
		}
		ev.HexNibble = 0
		syncCursor()
		return true
	case vtinput.VK_END:
		if ctrl {
			absPos = size
		} else {
			absPos = (absPos &^ 0xF) + 15
			if absPos > size {
				absPos = size
			}
		}
		ev.HexNibble = 0
		syncCursor()
		return true
	case vtinput.VK_PRIOR:
		height := ev.Y2 - ev.Y1
		absPos -= 16 * height
		syncCursor()
		return true
	case vtinput.VK_NEXT:
		height := ev.Y2 - ev.Y1
		absPos += 16 * height
		syncCursor()
		return true
	case vtinput.VK_DELETE:
		if !shift && !ctrl && absPos < size {
			ev.noteBufferEdit()
			ev.saveUndo(opOther)
			ev.modified = true
			ev.pt.Delete(absPos, 1)
			ev.li.UpdateAfterDelete(absPos, 1)
			ev.clearCaches()
			size--
			syncCursor()
		}
		return true
	case vtinput.VK_BACK:
		if absPos > 0 {
			ev.noteBufferEdit()
			ev.saveUndo(opOther)
			ev.modified = true
			ev.pt.Delete(absPos-1, 1)
			ev.li.UpdateAfterDelete(absPos-1, 1)
			ev.clearCaches()
			absPos--
			size--
			syncCursor()
		}
		return true
	}

	if (e.Char >= '0' && e.Char <= '9') || (e.Char >= 'a' && e.Char <= 'f') || (e.Char >= 'A' && e.Char <= 'F') {
		val := hexCharToByte(e.Char)
		ev.noteBufferEdit()
		ev.saveUndo(opTyping)
		ev.modified = true

		if absPos == size {
			b := byte(0)
			if ev.HexNibble == 0 {
				b = val << 4
			} else {
				b = val
			}
			ev.pt.Insert(absPos, []byte{b})
			ev.li.UpdateAfterInsert(absPos, []byte{b})
			size++
		} else {
			data, _ := ev.pt.GetRange(absPos, 1)
			b := byte(0)
			if len(data) > 0 {
				b = data[0]
			}
			if ev.HexNibble == 0 {
				b = (b & 0x0F) | (val << 4)
			} else {
				b = (b & 0xF0) | val
			}
			ev.pt.Delete(absPos, 1)
			ev.li.UpdateAfterDelete(absPos, 1)
			ev.pt.Insert(absPos, []byte{b})
			ev.li.UpdateAfterInsert(absPos, []byte{b})
		}
		ev.clearCaches()

		if ev.HexNibble == 0 {
			ev.HexNibble = 1
		} else {
			ev.HexNibble = 0
			absPos++
		}
		syncCursor()
		return true
	}

	return false
}

func (ev *EditorView) Show(scr *vtui.ScreenBuf) {
	ev.ScreenObject.Show(scr)
	if ev.topBar != nil {
		ev.topBar.Show(scr)
	}
	ev.DisplayObject(scr)
}

func (ev *EditorView) DisplayObject(scr *vtui.ScreenBuf) {
	defer ev.guardMapping("rendering")()
	if !ev.IsVisible() || ev.pasting {
		return
	}

	ev.ensureEngineWidth()
	height := ev.Y2 - ev.Y1
	width := ev.X2 - ev.X1 + 1
	if ev.scrollBar != nil {
		width--
	}

	bgAttr := ColorerEditorBaseAttr(vtui.Palette[ColEditorText])
	selAttr := vtui.Palette[vtui.ColDialogEditSelected]

	if ev.saving {
		scr.FillRect(ev.X1, ev.Y1+1, ev.X2, ev.Y2, ' ', bgAttr)
		scr.Write(ev.X1, ev.Y1+1, vtui.StringToCharInfo(" [ Saving... ] ", bgAttr))
		return
	}

	// A saved position deep in the file is restored only once the background
	// indexer has reached that line. Painting the top of the file until then
	// means the whole screen jumps the moment it does, which reads as a
	// flicker and undoes the point of easing the colours in. An empty area
	// costs the same wait and stays quiet.
	if ev.targetLine != -1 {
		scr.FillRect(ev.X1, ev.Y1+1, ev.X2, ev.Y2, ' ', bgAttr)
		msg := " [ Loading... ] "
		cx := ev.X1 + (width-len(msg))/2
		cy := ev.Y1 + 1 + height/2
		if cx >= ev.X1 && cy <= ev.Y2 {
			scr.Write(cx, cy, vtui.StringToCharInfo(msg, bgAttr))
		}
		return
	}

	// Clear the entire editor text area
	scr.FillRect(ev.X1, ev.Y1+1, ev.X2, ev.Y2, ' ', bgAttr)

	// Hex/decode views are byte-addressed and do not need the text layout. In
	// particular, an unindexed binary file looks like one very long text line
	// to WrapEngine; asking it for the caret position here would read a large
	// chunk before the hex view gets a chance to render.
	if ev.HexMode {
		ev.renderHex(scr, width, height-1)
		if ev.scrollBar != nil && ev.pt.Size() > 0 {
			maxOffset := int(ev.pt.Size())
			contentHeight := ev.Y2 - ev.Y1
			if contentHeight > 0 {
				lastLineOffset := int((ev.pt.Size() - 1) &^ 0xF)
				maxOffset = lastLineOffset - (contentHeight-1)*16
				if maxOffset < 0 {
					maxOffset = 0
				}
			}
			ev.scrollBar.SetParams(ev.HexTopOffset, 0, maxOffset)
			ev.scrollBar.Show(scr)
		}
		return
	}
	if ev.DecodeMode {
		ev.renderDecode(scr, width, height-1)
		if ev.scrollBar != nil && ev.pt.Size() > 0 {
			ev.scrollBar.SetParams(ev.HexTopOffset, 0, ev.pt.Size())
			ev.scrollBar.Show(scr)
		}
		return
	}

	// Text mode alone needs the text layout and crosshair coordinates.
	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	curVRow, curVCol := ev.engine.LogicalToVisual(curOffset)

	crossVRow, crossVCol := -1, -1
	var horzCrossAttr, vertCrossAttr uint64
	if showHorz, showVert, hAttr, vAttr := EditorCrossAttrs(); ev.IsFocused() {
		if showHorz {
			crossVRow = curVRow
			horzCrossAttr = hAttr
		}
		if showVert {
			crossVCol = curVCol + ev.CursorVirtualSpaces
			vertCrossAttr = vAttr
		}
	}

	// Horizontal line
	if crossVRow != -1 {
		cy := ev.Y1 + 1 + crossVRow - ev.ScrollTopRow
		if cy >= ev.Y1+1 && cy <= ev.Y2 {
			scr.FillRect(ev.X1, cy, ev.X1+width-1, cy, ' ', horzCrossAttr)
		}
	}

	// Vertical line
	if crossVCol != -1 {
		cx := ev.X1 + crossVCol - ev.ScrollLeft
		if cx >= ev.X1 && cx < ev.X1+width {
			scr.FillRect(cx, ev.Y1+1, cx, ev.Y2, ' ', vertCrossAttr)
		}
	}

	scr.PushClipRect(ev.X1, ev.Y1+1, ev.X1+width-1, ev.Y2)

	// 2. Отрисовка
	startLogLine, startFragIdx := ev.engine.GetLogLineAtVisualRow(ev.ScrollTopRow)
	rowsRendered := 0

	for logIdx := startLogLine; logIdx < ev.li.LineCount(); logIdx++ {
		lineStart := ev.li.GetLineOffset(logIdx)
		lineLen := 0
		if logIdx+1 < ev.li.LineCount() {
			lineLen = ev.li.GetLineOffset(logIdx+1) - lineStart
		} else {
			lineLen = ev.pt.Size() - lineStart
		}
		lineLinks := ev.urlLinksForLine(lineStart, lineStart+lineLen)

		// Stateful Highlighting
		var lineSyntax []uint64
		if !ev.binaryFile {
			if ch, isColorer := ev.highlighter.(*ColorerHighlighter); isColorer {
				// Colorer is addressed by line number: its parser state cannot
				// be carried in ev.lineStates, so it keeps its own anchor near
				// the viewport instead. See HIGHLIGHT.md, phase 5.
				if text, ok := ev.lineTextForHighlight(logIdx); ok {
					lineSyntax = ch.HighlightLine(logIdx, text, bgAttr)
				}
			} else if ev.highlighter != nil {
				// Catch up synchronously only if the uncomputed gap is small (<= 50 lines).
				// For large jumps, render unhighlighted immediately and compute in background.
				const syncHighlightGapLimit = 50
				if logIdx >= len(ev.lineStates)+syncHighlightGapLimit {
					ev.startHighlighting()

					// Allow stateless highlighters (like Chroma) to provide instant colors
					if _, isColorer := ev.highlighter.(*ColorerHighlighter); !isColorer {
						lStart := ev.li.GetLineOffset(logIdx)
						highlightLen := lineLen
						if highlightLen > 64*1024 {
							highlightLen = 64 * 1024
						}
						lineData, _ := ev.pt.GetRange(lStart, highlightLen)
						lineSyntax, _ = ev.highlighter.Highlight(string(lineData), nil, bgAttr)
					}
				} else {
					for len(ev.lineStates) <= logIdx {
						currIdx := len(ev.lineStates)
						lStart := ev.li.GetLineOffset(currIdx)
						lEnd := ev.pt.Size()
						if currIdx+1 < ev.li.LineCount() {
							lEnd = ev.li.GetLineOffset(currIdx + 1)
						}
						// Prevent highlighter from crashing on huge binary lines
						if lEnd-lStart > 64*1024 {
							lEnd = lStart + 64*1024
						}

						var prevState any
						if currIdx > 0 {
							prevState = ev.lineStates[currIdx-1]
						}

						lineData, err := ev.pt.GetRange(lStart, lEnd-lStart)
						if err == piecetable.ErrLoading {
							break // Wait for data
						}

						attrs, nextState := ev.highlighter.Highlight(string(lineData), prevState, bgAttr)
						ev.lineStates = append(ev.lineStates, nextState)
						if currIdx == logIdx {
							lineSyntax = attrs
						}
					}
					if logIdx < len(ev.lineStates) && lineSyntax == nil {
						// State was already cached, but we need the actual attributes for the current visible line
						lStart := ev.li.GetLineOffset(logIdx)
						// Re-apply highlighter OOM protection for the rendering path
						highlightLen := lineLen
						if highlightLen > 64*1024 {
							highlightLen = 64 * 1024
						}
						lineData, _ := ev.pt.GetRange(lStart, highlightLen)
						var prevState any
						if logIdx > 0 {
							prevState = ev.lineStates[logIdx-1]
						}
						lineSyntax, _ = ev.highlighter.Highlight(string(lineData), prevState, bgAttr)
					}
				}
			}
		}

		frags := ev.engine.GetFragments(logIdx)
		baseVRow := ev.engine.GetRowOffset(logIdx)
		// vtui.DebugLog("EDITOR_RENDER: Line %d, Frags: %d, BaseVRow: %d", logIdx, len(frags), baseVRow)
		runesProcessedInLine := 0

		for fIdx, frag := range frags {
			if logIdx == startLogLine && fIdx < startFragIdx {
				// Пропускаем подсветку для фрагментов выше области видимости
				fragData, _ := ev.pt.GetRange(frag.ByteOffsetStart, frag.ByteOffsetEnd-frag.ByteOffsetStart)
				runesProcessedInLine += len([]rune(string(fragData)))
				continue
			}

			absVRow := baseVRow + fIdx
			currY := ev.Y1 + 1 + rowsRendered

			ev.renderBytes = ev.renderBytes[:0]
			var err error
			ev.renderBytes, err = ev.pt.AppendRange(ev.renderBytes, frag.ByteOffsetStart, frag.ByteOffsetEnd-frag.ByteOffsetStart)

			fragRuneCount := len([]rune(string(ev.renderBytes)))

			if err == piecetable.ErrLoading {
				scr.Write(ev.X1-ev.ScrollLeft, currY, vtui.StringToCharInfo(" [ Loading... ] ", bgAttr))
				runesProcessedInLine += fragRuneCount
				rowsRendered++
				if rowsRendered >= height {
					goto DoneRendering
				}
				continue
			}

			selMin, selMax := ev.getSelectionRange()

			// Вырезаем кусок атрибутов именно для этого фрагмента
			var fragSyntax []uint64
			if runesProcessedInLine < len(lineSyntax) {
				end := runesProcessedInLine + fragRuneCount
				if end > len(lineSyntax) {
					end = len(lineSyntax)
				}
				fragSyntax = lineSyntax[runesProcessedInLine:end]
			}
			runesProcessedInLine += fragRuneCount

			isCrossRow := (absVRow == crossVRow)
			ev.renderCells = ev.fillCellsWithLinks(ev.renderCells, ev.renderBytes, bgAttr, selAttr, frag.ByteOffsetStart, ev.selActive, selMin, selMax, ev.fadeSyntax(fragSyntax, bgAttr), lineLinks, 0, isCrossRow, crossVCol, horzCrossAttr, vertCrossAttr, absVRow)

			scr.Write(ev.X1-ev.ScrollLeft, currY, ev.renderCells)

			lineBg := bgAttr
			if logIdx < len(ev.lineStates) {
				if ch, ok := ev.highlighter.(*ColorerHighlighter); ok {
					lineBg = ch.GetLineBackground(logIdx, bgAttr)
				}
			}
			fillBg := lineBg
			if isCrossRow && horzCrossAttr != 0 {
				if horzCrossAttr&vtui.IsBgRGB != 0 {
					fillBg = vtui.SetRGBBack(fillBg, vtui.GetRGBBack(horzCrossAttr))
				} else {
					fillBg = vtui.SetIndexBack(fillBg, vtui.GetIndexBack(horzCrossAttr))
				}
			}
			startX := ev.X1 - ev.ScrollLeft + len(ev.renderCells)
			if startX < ev.X1 {
				startX = ev.X1
			}
			maxX := ev.X1 + width - 1
			if startX <= maxX {
				scr.FillRect(startX, currY, maxX, currY, ' ', fillBg)
			}

			if absVRow == curVRow {
				scr.SetCursorPos(ev.X1+curVCol+ev.CursorVirtualSpaces-ev.ScrollLeft, currY)
				scr.SetCursorVisible(true)
				if ev.overtype {
					scr.SetCursorShape(vtui.CursorShapeBlock)
				} else {
					scr.SetCursorShape(vtui.CursorShapeUnderline)
				}
			}

			rowsRendered++
			if rowsRendered >= height {
				goto DoneRendering
			}
		}
	}

DoneRendering:
	// 3. Draw Autocomplete Ghost Text
	if ev.acEnabled && len(ev.acMatches) > 0 && ev.IsFocused() && !ev.pasting {
		match := ev.acMatches[ev.acCurrentIdx]
		if len(match) > len(ev.acPrefix) {
			tail := match[len(ev.acPrefix):]
			// Calculate exact visual position of cursor
			curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			vRow, vCol := ev.engine.LogicalToVisual(curOffset)

			drawY := ev.Y1 + 1 + vRow - ev.ScrollTopRow
			drawX := ev.X1 + vCol - ev.ScrollLeft

			// Draw if visible
			if drawY >= ev.Y1+1 && drawY <= ev.Y2 {
				// We use DimColor of the standard text to make it look like a ghost suggestion
				ghostAttr := vtui.DimColor(vtui.Palette[ColCommandLineUserScreen])
				// Ensure it doesn't leak out of the editor frame
				maxLen := ev.X2 - drawX
				if ev.scrollBar != nil {
					maxLen--
				}

				if maxLen > 0 {
					displayTail := tail
					if len([]rune(displayTail)) > maxLen {
						displayTail = string([]rune(displayTail)[:maxLen])
					}
					scr.Write(drawX, drawY, vtui.StringToCharInfo(displayTail, ghostAttr))
				}
			}
		}
	}

	scr.PopClipRect()

	if ev.scrollBar != nil {
		totalRows := ev.engine.GetTotalVisualRows()
		if totalRows > height {
			ev.scrollBar.SetParams(ev.ScrollTopRow, 0, totalRows-height)
			ev.scrollBar.Show(scr)
		}
	}
}

// VetoActionKey reports modal input states in which the editor must see
// the key before the global hotkey dispatcher. While autocomplete is
// active, the keys it consumes (Tab/Esc) or uses to dismiss itself
// (navigation, Enter) belong to the editor's own ProcessKey. Escape also
// belongs here while indexing so it can cancel the scan rather than reach a
// frame-level action.
func (ev *EditorView) VetoActionKey(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.KeyEventType || !e.KeyDown {
		return false
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && (ev.colorerIndexing || ev.indexing) {
		return true
	}
	if !ev.acEnabled || len(ev.acMatches) == 0 {
		return false
	}
	switch e.VirtualKeyCode {
	case vtinput.VK_TAB, vtinput.VK_ESCAPE, vtinput.VK_RETURN,
		vtinput.VK_UP, vtinput.VK_DOWN, vtinput.VK_LEFT, vtinput.VK_RIGHT,
		vtinput.VK_HOME, vtinput.VK_END, vtinput.VK_PRIOR, vtinput.VK_NEXT:
		return true
	}
	return false
}

// ProcessKey wraps the editor's own key handling so that a restore still
// waiting for the background index is abandoned only when the key actually did
// something. Over FISH+ the index needs seconds to reach a position saved deep
// in a file, and a key pressed inside that window used to throw the position
// away even when it moved nothing: Up at the top of a file left the user at the
// top of the file, which is precisely where they did not want to be.
func (ev *EditorView) ProcessKey(e *vtinput.InputEvent) bool {
	if ev.targetLine == -1 {
		return ev.processKeyInner(e)
	}

	line, pos, edited := ev.CursorLine, ev.CursorPos, ev.edited
	handled := ev.processKeyInner(e)
	if ev.targetLine != -1 && (ev.CursorLine != line || ev.CursorPos != pos || ev.edited != edited) {
		ev.targetLine = -1 // User took control, abort target jump
		ev.ensureCursorVisible()
	}
	return handled
}

func (ev *EditorView) processKeyInner(e *vtinput.InputEvent) bool {

	ev.ensureEngineWidth()
	if ev.saving {
		return true
	}
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0

	// 1. Processing Bracketed Paste (events arrive outside KeyDown)
	if e.Type == vtinput.PasteEventType {
		if e.PasteStart {
			ev.pasting = true
			ev.pasteBuffer = nil
		} else {
			ev.pasting = false
			if len(ev.pasteBuffer) > 0 {
				ev.noteBufferEdit()

				ev.saveUndo(opOther)
				if ev.selActive {
					ev.inGroup = true
					ev.DeleteSelection()
					ev.inGroup = false
				}

				offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
				data := []byte(string(ev.pasteBuffer))
				ev.pt.Insert(offset, data)
				ev.li.UpdateAfterInsert(offset, data)
				ev.invalidateStates(ev.CursorLine)
				ev.engine.InvalidateFrom(ev.CursorLine)

				newOffset := offset + len(data)
				ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
				ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
				ev.modified = true
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
			}
		}
		return true
	}

	// 2. Accumulating characters in paste mode
	if ev.pasting {
		if e.Type == vtinput.KeyEventType && e.KeyDown {
			if e.Char != 0 {
				// Handle system line breaks inside the paste
				switch e.Char {
				case '\r':
					// Ignore \r to prevent double line breaks
				case '\n':
					ev.pasteBuffer = append(ev.pasteBuffer, '\n')
				default:
					ev.pasteBuffer = append(ev.pasteBuffer, e.Char)
				}
			} else if e.VirtualKeyCode == vtinput.VK_RETURN {
				ev.pasteBuffer = append(ev.pasteBuffer, '\n')
			}
		}
		return true
	}

	if ev.HexMode || ev.DecodeMode {
		if ev.processKeyHex(e) {
			return true
		}
		// Prevent fallthrough to text editing keys
		switch {
		case e.VirtualKeyCode == vtinput.VK_W &&
			e.ControlKeyState&(vtinput.LeftCtrlPressed|vtinput.RightCtrlPressed) != 0:
			return false // Let the framework handle Ctrl+W
		case e.VirtualKeyCode >= vtinput.VK_F1 && e.VirtualKeyCode <= vtinput.VK_F12:
			// KeyBar clicks inject F-key events after the frame-manager filter
			// has already been bypassed. zoin-bot routes those events through
			// the configured editor action before yielding to framework fallbacks.
			if MacroMgr.LookupHotkey(e) {
				return true
			}
			return false // Let framework fallbacks handle unbound F-keys
		}
		if MacroMgr.LookupHotkey(e) {
			return true
		}
		return true // Consume all other keys in Hex mode so they don't insert text
	}

	// 3. Regular key processing
	if !e.KeyDown {
		return false
	}

	shift := (e.ControlKeyState & vtinput.ShiftPressed) != 0
	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	//alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0

	// --- Autocomplete Interception ---
	if ev.acEnabled && len(ev.acMatches) > 0 {
		switch e.VirtualKeyCode {
		case vtinput.VK_TAB:
			if (e.ControlKeyState & vtinput.ShiftPressed) != 0 {
				// Shift+Tab: cycle matches
				ev.acCurrentIdx = (ev.acCurrentIdx + 1) % len(ev.acMatches)
				vtui.FrameManager.Redraw()
				return true
			} else if (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed | vtinput.LeftAltPressed | vtinput.RightAltPressed)) == 0 {
				// Tab: Apply completion
				match := ev.acMatches[ev.acCurrentIdx]
				tail := match[len(ev.acPrefix):]
				ev.acMatches = nil // Clear state
				if tail == "" {
					return true
				}

				ev.noteBufferEdit()
				ev.saveUndo(opTyping)
				ev.modified = true
				offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
				data := []byte(tail)
				ev.pt.Insert(offset, data)
				ev.li.UpdateAfterInsert(offset, data)
				ev.invalidateStates(ev.CursorLine)
				ev.engine.InvalidateFrom(ev.CursorLine)
				ev.CursorPos += len(data)
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
				return true
			}
		case vtinput.VK_ESCAPE:
			// Esc: Dismiss autocomplete
			ev.acMatches = nil
			vtui.FrameManager.Redraw()
			return true
		}

		// Any movement or non-character key clears the AC state
		if e.VirtualKeyCode == vtinput.VK_UP || e.VirtualKeyCode == vtinput.VK_DOWN ||
			e.VirtualKeyCode == vtinput.VK_LEFT || e.VirtualKeyCode == vtinput.VK_RIGHT ||
			e.VirtualKeyCode == vtinput.VK_HOME || e.VirtualKeyCode == vtinput.VK_END ||
			e.VirtualKeyCode == vtinput.VK_PRIOR || e.VirtualKeyCode == vtinput.VK_NEXT ||
			e.VirtualKeyCode == vtinput.VK_RETURN {
			ev.acMatches = nil
		}
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && ev.colorerIndexing {
		ev.cancelColorer()
		vtui.FrameManager.Redraw()
		return true
	}
	if e.VirtualKeyCode == vtinput.VK_ESCAPE && ev.indexing {
		ev.targetOffset = -1
		ev.targetLine = -1
		ev.cancelIndexing()
		vtui.FrameManager.Redraw()
		return true
	}

	// Allow FrameManager to handle Ctrl+Tab for workspace switching
	if e.VirtualKeyCode == vtinput.VK_TAB && ctrl {
		return false
	}

	handleNav := func() {
		if alt {
			ev.selActive = false
			if !ev.rectSelActive {
				ev.rectSelActive = true
				ev.rectSelStartLine = ev.CursorLine
				ev.rectSelStartCol = ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
			}
		} else if shift {
			ev.rectSelActive = false
			if !ev.selActive {
				ev.selActive = true
				ev.selAnchorOffset = ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			}
		} else {
			ev.selActive = false
			ev.rectSelActive = false
		}
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_UP, vtinput.VK_E:
		if e.VirtualKeyCode == vtinput.VK_E && !ctrl {
			break
		}
		// Ctrl+Up scrolls the text under the cursor, which keeps its row
		// on the screen (far2l editor.cpp, Editor::ScrollUp). The Ctrl+E
		// alias stays a plain cursor movement.
		if ctrl && !shift && !alt && e.VirtualKeyCode == vtinput.VK_UP {
			ev.scrollViewBy(-1)
			return true
		}
		handleNav()
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		if vRow > 0 {
			newOffset := ev.engine.VisualToLogical(vRow-1, ev.DesiredVisualCol)
			ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
			ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)

			lineLen := ev.getLineLength(ev.CursorLine)
			vtui.DebugLog("DEBUG_UP: TargetRow:%d DesiredCol:%d ResultPos:%d LineLen:%d", vRow-1, ev.DesiredVisualCol, ev.CursorPos, lineLen)
			if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
				_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
				if ev.DesiredVisualCol > endVCol {
					ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
				} else {
					ev.CursorVirtualSpaces = 0
				}
				vtui.DebugLog("DEBUG_UP_VIRT: EndVCol:%d ResultVirt:%d", endVCol, ev.CursorVirtualSpaces)
			} else {
				ev.CursorVirtualSpaces = 0
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DOWN, vtinput.VK_X:
		if e.VirtualKeyCode == vtinput.VK_X {
			if !ctrl {
				break
			}
			// Ctrl+X is delegated to the Cut action when text is
			// selected; without a selection it falls through and acts
			// as the classic Ctrl+E/Ctrl+X down-movement alias.
			if ev.selActive || ev.rectSelActive {
				RunAction("Editor.Cut")
				return true
			}
		}
		// Ctrl+Down scrolls the text under the cursor, which keeps its row
		// on the screen (far2l editor.cpp, Editor::ScrollDown). The Ctrl+X
		// alias stays a plain cursor movement.
		if ctrl && !shift && !alt && e.VirtualKeyCode == vtinput.VK_DOWN {
			ev.scrollViewBy(1)
			return true
		}
		handleNav()
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		newOffset := ev.engine.VisualToLogical(vRow+1, ev.DesiredVisualCol)
		ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
		ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)

		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
			_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
			vtui.DebugLog("DEBUG_DOWN: TargetRow:%d DesiredCol:%d ResultPos:%d LineLen:%d EndVCol:%d", vRow+1, ev.DesiredVisualCol, ev.CursorPos, lineLen, endVCol)
			if ev.DesiredVisualCol > endVCol {
				ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
			} else {
				ev.CursorVirtualSpaces = 0
			}
			vtui.DebugLog("DEBUG_DOWN_VIRT: ResultVirt:%d", ev.CursorVirtualSpaces)
		} else {
			vtui.DebugLog("DEBUG_DOWN_NO_VIRT: TargetRow:%d ResultPos:%d LineLen:%d", vRow+1, ev.CursorPos, lineLen)
			ev.CursorVirtualSpaces = 0
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_PRIOR: // PgUp
		handleNav()
		height := ev.Y2 - ev.Y1
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		newVRow := vRow - height
		if newVRow < 0 {
			newVRow = 0
		}
		newOffset := ev.engine.VisualToLogical(newVRow, ev.DesiredVisualCol)
		ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
		ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)

		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
			_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
			if ev.DesiredVisualCol > endVCol {
				ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
			} else {
				ev.CursorVirtualSpaces = 0
			}
		} else {
			ev.CursorVirtualSpaces = 0
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_NEXT: // PgDn
		handleNav()
		height := ev.Y2 - ev.Y1
		curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		vRow, _ := ev.engine.LogicalToVisual(curOffset)
		newVRow := vRow + height
		totalVRows := ev.engine.GetTotalVisualRows()
		if newVRow >= totalVRows {
			newVRow = totalVRows - 1
		}
		newOffset := ev.engine.VisualToLogical(newVRow, ev.DesiredVisualCol)
		ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
		ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)

		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
			_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
			if ev.DesiredVisualCol > endVCol {
				ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
			} else {
				ev.CursorVirtualSpaces = 0
			}
		} else {
			ev.CursorVirtualSpaces = 0
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_LEFT, vtinput.VK_S:
		isAlias := e.VirtualKeyCode == vtinput.VK_S
		if isAlias && !ctrl {
			break
		}
		handleNav()
		// Jump by word only if it's the real Left arrow + Ctrl
		wordJump := ctrl && !isAlias
		if ev.CursorVirtualSpaces > 0 {
			if !wordJump {
				ev.CursorVirtualSpaces--
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
				return true
			}
			// Past EOL a word jump behaves as if the cursor stood at the
			// real end of the line: the virtual spaces are dropped first
			// and the jump then starts from the last character.
			ev.CursorVirtualSpaces = 0
		}

		if wordJump {
			if ev.CursorPos > 0 {
				runes := ev.getLogicalLineRunes(ev.CursorLine)
				currRuneIdx := 0
				byteAcc := 0
				for i, r := range runes {
					if byteAcc >= ev.CursorPos {
						currRuneIdx = i
						break
					}
					byteAcc += utf8.RuneLen(r)
					if i == len(runes)-1 {
						currRuneIdx = len(runes)
					}
				}

				if currRuneIdx > 0 {
					lineStart := ev.li.GetLineOffset(ev.CursorLine)
					startVRow, _ := ev.engine.LogicalToVisual(lineStart + ev.CursorPos)

					currRuneIdx--
					ev.CursorPos = 0
					for i := 0; i < currRuneIdx; i++ {
						ev.CursorPos += utf8.RuneLen(runes[i])
					}
					if shift {
						handleNav()
					}

					vRow, _ := ev.engine.LogicalToVisual(lineStart + ev.CursorPos)
					if vRow == startVRow {
						for currRuneIdx > 0 {
							prev, curr := runes[currRuneIdx-1], runes[currRuneIdx]
							if stopBeforeRuneLeft(prev, curr, shift) {
								break
							}
							currRuneIdx--
							ev.CursorPos = 0
							for i := 0; i < currRuneIdx; i++ {
								ev.CursorPos += utf8.RuneLen(runes[i])
							}
							if shift {
								handleNav()
							}
							vRow, _ = ev.engine.LogicalToVisual(lineStart + ev.CursorPos)
							if vRow != startVRow {
								break
							}
						}
					}
				}
			} else if ev.CursorLine > 0 {
				ev.CursorLine--
				ev.CursorPos = ev.getLineLength(ev.CursorLine)
			}
		} else {
			// Left and Right move through the text in logical order, one
			// cluster at a time, whatever direction the cluster is drawn in.
			// Inside a right to left word the caret therefore walks
			// leftwards on screen and turns back at the word's edges, as it
			// does in Notepad (unxed/f4#546); the visual column comes from
			// the caret map of textlayout.
			if ev.CursorPos > 0 {
				lineStart := ev.li.GetLineOffset(ev.CursorLine)
				ev.CursorPos = ev.previousGraphemeBoundaryInLine(lineStart, ev.CursorPos)
			} else if ev.CursorLine > 0 {
				ev.CursorLine--
				ev.CursorPos = ev.getLineLength(ev.CursorLine)
			}
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RIGHT, vtinput.VK_D:
		isAlias := e.VirtualKeyCode == vtinput.VK_D
		if isAlias && !ctrl {
			break
		}
		handleNav()
		lineLen := ev.getLineLength(ev.CursorLine)
		// Jump by word only if it's the real Right arrow + Ctrl
		if ctrl && !isAlias {
			ev.CursorVirtualSpaces = 0
			if ev.CursorPos < lineLen {
				runes := ev.getLogicalLineRunes(ev.CursorLine)
				currRuneIdx := len(runes)
				byteAcc := 0
				for i, r := range runes {
					if byteAcc >= ev.CursorPos {
						currRuneIdx = i
						break
					}
					byteAcc += utf8.RuneLen(r)
				}

				if currRuneIdx < len(runes) {
					lineStart := ev.li.GetLineOffset(ev.CursorLine)
					startVRow, _ := ev.engine.LogicalToVisual(lineStart + ev.CursorPos)

					currRuneIdx++
					ev.CursorPos = 0
					for i := 0; i < currRuneIdx; i++ {
						ev.CursorPos += utf8.RuneLen(runes[i])
					}
					if shift {
						handleNav()
					}

					vRow, _ := ev.engine.LogicalToVisual(lineStart + ev.CursorPos)
					if vRow == startVRow {
						for currRuneIdx < len(runes) {
							prev, curr := runes[currRuneIdx-1], runes[currRuneIdx]
							if stopBeforeRuneRight(prev, curr, shift) {
								break
							}

							currRuneIdx++
							ev.CursorPos = 0
							for i := 0; i < currRuneIdx; i++ {
								ev.CursorPos += utf8.RuneLen(runes[i])
							}
							if shift {
								handleNav()
							}

							vRow, _ = ev.engine.LogicalToVisual(lineStart + ev.CursorPos)
							if vRow != startVRow {
								// Revert to the end of the previous visual line
								currRuneIdx--
								ev.CursorPos = 0
								for i := 0; i < currRuneIdx; i++ {
									ev.CursorPos += utf8.RuneLen(runes[i])
								}
								if shift {
									handleNav()
								}
								break
							}
						}
					}
				}
			} else if ev.CursorLine < ev.li.LineCount()-1 {
				ev.CursorLine++
				ev.CursorPos = 0
			}
		} else {
			if ev.CursorPos < lineLen {
				lineStart := ev.li.GetLineOffset(ev.CursorLine)
				ev.CursorPos = ev.nextGraphemeBoundaryInLine(lineStart, lineLen, ev.CursorPos)
			} else if ev.CursorLine < ev.li.LineCount()-1 {
				if ev.CursorBeyondEOL {
					ev.CursorVirtualSpaces++
				} else {
					ev.CursorLine++
					ev.CursorPos = 0
					ev.CursorVirtualSpaces = 0
				}
			} else if ev.CursorBeyondEOL {
				ev.CursorVirtualSpaces++
			}
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_HOME:
		handleNav()
		if ctrl {
			ev.CursorLine = 0
		}
		ev.CursorPos = 0
		ev.CursorVirtualSpaces = 0
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_END:
		handleNav()
		if ctrl {
			ev.CursorLine = ev.li.LineCount() - 1
		}
		ev.CursorPos = ev.getLineLength(ev.CursorLine)
		ev.CursorVirtualSpaces = 0
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_BACK:
		// A vertical block is a selection too. It lives in rectSelActive
		// rather than selActive, and checking only the latter is what made
		// Del eat the character under the cursor while a block was up.
		if ev.selActive || ev.rectSelActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset > 0 {
				ev.noteBufferEdit()
				ev.saveUndo(opOther)
				ev.modified = true
				if ev.CursorPos == 0 {
					prevLen := ev.getLineLength(ev.CursorLine - 1)
					delLen := 1
					if offset >= 2 {
						prefix, _ := ev.pt.GetRange(offset-2, 2)
						if len(prefix) == 2 && prefix[0] == '\r' && prefix[1] == '\n' {
							delLen = 2
						}
					}
					ev.pt.Delete(offset-delLen, delLen)
					ev.li.UpdateAfterDelete(offset-delLen, delLen)
					ev.invalidateStates(ev.CursorLine - 1)
					ev.engine.InvalidateFrom(ev.CursorLine - 1)
					ev.CursorLine--
					ev.CursorPos = prevLen
				} else {
					deleteStart := ev.li.GetLineOffset(ev.CursorLine) + ev.previousDeletionBoundaryInLine(ev.li.GetLineOffset(ev.CursorLine), ev.CursorPos)
					// Backspace deletes the preceding logical text, even when the
					// line is painted right-to-left. A visual-left move from the
					// logical end of an RTL line is already at the screen edge and
					// therefore cannot identify the character Backspace must remove.
					ev.pt.Delete(deleteStart, offset-deleteStart)
					ev.li.UpdateAfterDelete(deleteStart, offset-deleteStart)
					ev.CursorLine = ev.li.GetLineAtOffset(deleteStart)
					ev.CursorPos = deleteStart - ev.li.GetLineOffset(ev.CursorLine)
					minLine := ev.CursorLine
					ev.invalidateStates(minLine)
					ev.engine.InvalidateFrom(minLine)
				}
			}
		}
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_DELETE:
		// Shift+Del is Cut, and Cut it stays even if the hotkey dispatcher
		// never resolved it: far2l copies the block to the clipboard and only
		// then deletes it, and does nothing at all when no block is marked
		// (editor.cpp, KEY_SHIFTDEL). Falling through to the plain delete below
		// destroys a selection with no clipboard copy — unrecoverable — so the
		// modified key is answered here rather than left to key naming, which
		// differs between input backends. Ctrl+Del is likewise handled instead
		// of eating the character under the cursor.
		if shift && !ctrl && !alt {
			if ev.selActive || ev.rectSelActive {
				ev.CopySelection()
				ev.DeleteSelection()
			}
			ev.updateDesiredVisualCol()
			ev.ensureCursorVisible()
			return true
		}
		if ctrl && !shift && !alt {
			ev.deleteSpacersForward()
			ev.updateDesiredVisualCol()
			ev.ensureCursorVisible()
			return true
		}
		if ev.selActive || ev.rectSelActive {
			ev.DeleteSelection()
		} else {
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
			if offset < ev.pt.Size() {
				ev.noteBufferEdit()
				ev.saveUndo(opOther)
				ev.modified = true
				deleteEnd := offset + 1
				if ev.CursorPos < ev.getLineLength(ev.CursorLine) {
					// Delete follows the logical text direction. A visual-right
					// move from the logical start of an RTL line is already at the
					// screen edge and would turn this into a no-op.
					lineStart := ev.li.GetLineOffset(ev.CursorLine)
					next := ev.nextGraphemeBoundaryInLine(lineStart, ev.getLineLength(ev.CursorLine), ev.CursorPos)
					deleteEnd = lineStart + next
				}
				ev.pt.Delete(offset, deleteEnd-offset)
				ev.li.UpdateAfterDelete(offset, deleteEnd-offset)
				ev.invalidateStates(ev.CursorLine)
				ev.engine.InvalidateFrom(ev.CursorLine)
			}
		}
		ev.ensureCursorVisible()
		return true

	case vtinput.VK_RETURN:
		ev.noteBufferEdit()
		ev.saveUndo(opOther)
		if ev.selActive || ev.rectSelActive {
			ev.inGroup = true
			ev.DeleteSelection()
			ev.inGroup = false
		}
		ev.modified = true
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		var indent []byte
		if ev.AutoIndent {
			lineRunes := ev.getLogicalLineRunes(ev.CursorLine)
			for _, r := range lineRunes {
				if r == ' ' || r == '\t' {
					indent = append(indent, []byte(string(r))...)
				} else {
					break
				}
			}
		}

		if ev.CursorVirtualSpaces > 0 {
			spaces := []byte(strings.Repeat(" ", ev.CursorVirtualSpaces))
			ev.pt.Insert(offset, spaces)
			ev.li.UpdateAfterInsert(offset, spaces)
			offset += ev.CursorVirtualSpaces
			ev.CursorVirtualSpaces = 0
		}

		ev.pt.Insert(offset, []byte("\n"))
		ev.li.UpdateAfterInsert(offset, []byte("\n"))
		ev.engine.InvalidateFrom(ev.CursorLine)
		ev.CursorLine++
		ev.CursorPos = 0
		ev.DesiredVisualCol = 0

		if len(indent) > 0 {
			offset = ev.li.GetLineOffset(ev.CursorLine)
			ev.pt.Insert(offset, indent)
			ev.li.UpdateAfterInsert(offset, indent)
			ev.CursorPos += len(indent)
			ev.updateDesiredVisualCol()
		}

		ev.ensureCursorVisible()
		return true

	case vtinput.VK_TAB:
		if !shift && !ctrl && !alt {
			ev.noteBufferEdit()
			ev.saveUndo(opTyping)
			if ev.selActive || ev.rectSelActive {
				ev.inGroup = true
				ev.DeleteSelection()
				ev.inGroup = false
			}
			ev.modified = true
			offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos

			var data []byte
			if ev.ExpandTabs > 0 {
				_, vCol := ev.engine.LogicalToVisual(offset)
				vCol += ev.CursorVirtualSpaces
				spaces := ev.TabSize - (vCol % ev.TabSize)
				vtui.DebugLog("DEBUG_TAB_EXPAND: Offset:%d BaseVCol:%d Virt:%d ResultSpaces:%d (TabSize:%d)", offset, vCol-ev.CursorVirtualSpaces, ev.CursorVirtualSpaces, spaces, ev.TabSize)
				data = []byte(strings.Repeat(" ", spaces))
			} else {
				data = []byte("\t")
			}

			if ev.CursorVirtualSpaces > 0 {
				virtSpaces := []byte(strings.Repeat(" ", ev.CursorVirtualSpaces))
				ev.pt.Insert(offset, virtSpaces)
				ev.li.UpdateAfterInsert(offset, virtSpaces)
				offset += ev.CursorVirtualSpaces
				ev.CursorPos += ev.CursorVirtualSpaces
				ev.CursorVirtualSpaces = 0
			}

			ev.pt.Insert(offset, data)
			ev.li.UpdateAfterInsert(offset, data)
			ev.invalidateStates(ev.CursorLine)
			ev.engine.InvalidateFrom(ev.CursorLine)
			ev.CursorPos += len(data)
			ev.updateDesiredVisualCol()
			ev.ensureCursorVisible()

			if ev.acEnabled {
				ev.updateAutocomplete()
			}
			return true
		}
	}

	if e.Char != 0 && !ctrl {
		ev.noteBufferEdit()
		ev.saveUndo(opTyping)
		if ev.selActive || ev.rectSelActive {
			ev.inGroup = true
			ev.DeleteSelection()
			ev.inGroup = false
		}
		ev.modified = true
		offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
		if ev.CursorVirtualSpaces > 0 {
			spaces := []byte(strings.Repeat(" ", ev.CursorVirtualSpaces))
			ev.pt.Insert(offset, spaces)
			ev.li.UpdateAfterInsert(offset, spaces)
			offset += ev.CursorVirtualSpaces
			ev.CursorPos += ev.CursorVirtualSpaces
			ev.CursorVirtualSpaces = 0
		}

		data := []byte(string(e.Char))

		if ev.overtype {
			lineLen := ev.getLineLength(ev.CursorLine)
			if ev.CursorPos < lineLen {
				size := 1
				lineStart := ev.li.GetLineOffset(ev.CursorLine)
				if ev.CursorPos < lineLen {
					next := ev.nextGraphemeBoundaryInLine(lineStart, lineLen, ev.CursorPos)
					if next > ev.CursorPos {
						size = next - ev.CursorPos
					}
				}
				ev.pt.Delete(offset, size)
				ev.li.UpdateAfterDelete(offset, size)
			}
		}

		ev.pt.Insert(offset, data)
		ev.li.UpdateAfterInsert(offset, data)
		ev.invalidateStates(ev.CursorLine)
		ev.engine.InvalidateFrom(ev.CursorLine)
		ev.CursorPos += len(data)
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()

		if ev.acEnabled {
			ev.updateAutocomplete()
		}
		return true
	}

	// Injected-event fallback: KeyBar mouse clicks reach ProcessKey via
	// InjectEvents, which skips FrameManager.EventFilter and therefore the
	// hotkey manager. Route them through the same lookup so clicking F2/F7/
	// F10/… on the bottom bar triggers the configured Editor action.
	if MacroMgr.LookupHotkey(e) {
		return true
	}

	return false
}

type editorTextCluster struct {
	text      string
	width     int
	byteStart int
	byteEnd   int
	runeStart int
	runeEnd   int
}

// editorVisualClusters uses textlayout's shared grapheme and BiDi order for
// painting. zoin-bot keeps byte and rune boundaries from the document intact.
func editorVisualClusters(text string) []editorTextCluster {
	base := textlayout.VisualClustersInVisualOrder(text)
	clusters := make([]editorTextCluster, 0, len(base))
	for _, cluster := range base {
		clusters = append(clusters, editorTextCluster{
			text:      cluster.Text,
			width:     cluster.Width,
			byteStart: cluster.Start,
			byteEnd:   cluster.End,
			runeStart: cluster.RuneStart,
			runeEnd:   cluster.RuneEnd,
		})
	}
	return clusters
}

func (ev *EditorView) fillCells(target []vtui.CharInfo, data []byte, defaultAttr, selAttr uint64, offset int, selActive bool, selMin, selMax int, syntax []uint64, startVisualCol int, isCrossRow bool, crossVCol int, horzCrossAttr, vertCrossAttr uint64, visualRow int) []vtui.CharInfo {
	return ev.fillCellsWithLinks(target, data, defaultAttr, selAttr, offset, selActive, selMin, selMax, syntax, nil, startVisualCol, isCrossRow, crossVCol, horzCrossAttr, vertCrossAttr, visualRow)
}

func (ev *EditorView) fillCellsWithLinks(target []vtui.CharInfo, data []byte, defaultAttr, selAttr uint64, offset int, selActive bool, selMin, selMax int, syntax []uint64, links []urlLink, startVisualCol int, isCrossRow bool, crossVCol int, horzCrossAttr, vertCrossAttr uint64, visualRow int) []vtui.CharInfo {
	target = target[:0]
	text := string(data)
	clusters := editorVisualClusters(text)
	visualCol := startVisualCol
	tabSize := ev.TabSize
	if tabSize <= 0 {
		tabSize = 8
	}

	for _, cluster := range clusters {
		var w int
		displayText, sanitizedWidth := vtui.SanitizeCluster(cluster.text)
		if cluster.text == "\t" {
			w = tabSize - (visualCol % tabSize)
			displayText = " "
			if ev.ShowWhitespaces {
				displayText = "→"
			}
		} else {
			if sanitizedWidth == 0 {
				continue
			}
			w = sanitizedWidth
			if cluster.text == " " && ev.ShowWhitespaces {
				displayText = "·"
			}
		}
		if w <= 0 {
			w = 1
		}

		attr := defaultAttr
		if cluster.runeStart < len(syntax) {
			attr = syntax[cluster.runeStart]
		}
		if ev.hoverURL != "" {
			if link, ok := urlLinkAt(links, offset+cluster.byteStart); ok && link.URL == ev.hoverURL {
				attr |= vtui.CommonLvbUnderscore
			}
		}

		if isCrossRow && horzCrossAttr != 0 {
			if horzCrossAttr&vtui.IsBgRGB != 0 {
				attr = vtui.SetRGBBack(attr, vtui.GetRGBBack(horzCrossAttr))
			} else {
				attr = vtui.SetIndexBack(attr, vtui.GetIndexBack(horzCrossAttr))
			}
		}

		if ev.rectSelActive {
			minY, maxY := ev.rectSelStartLine, ev.CursorLine
			if minY > maxY {
				minY, maxY = maxY, minY
			}
			minX, maxX := ev.rectSelStartCol, ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
			if minX > maxX {
				minX, maxX = maxX, minX
			}
			if visualRow >= minY && visualRow <= maxY && visualCol < maxX && visualCol+w > minX {
				attr = selAttr
			}
		} else if selActive {
			absStart := offset + cluster.byteStart
			absEnd := offset + cluster.byteEnd
			if absStart < selMax && absEnd > selMin {
				attr = selAttr
			}
		}

		cells := vtui.AppendCluster(nil, displayText, w, attr)
		if displayText == " " && w > 1 {
			cells = make([]vtui.CharInfo, 0, w)
			for i := 0; i < w; i++ {
				cells = append(cells, vtui.CharInfo{Char: ' ', Attributes: attr})
			}
		}
		for i := range cells {
			cellAttr := cells[i].Attributes
			if !isCrossRow && visualCol+i == crossVCol && vertCrossAttr != 0 {
				if vertCrossAttr&vtui.IsBgRGB != 0 {
					cellAttr = vtui.SetRGBBack(cellAttr, vtui.GetRGBBack(vertCrossAttr))
				} else {
					cellAttr = vtui.SetIndexBack(cellAttr, vtui.GetIndexBack(vertCrossAttr))
				}
			}
			cells[i].Attributes = cellAttr
		}
		target = append(target, cells...)
		visualCol += w
	}
	return target
}

// syncVirtualSpaces re-derives CursorVirtualSpaces after a vertical move:
// when the cursor lands on a shorter line and "cursor beyond EOL" is on, the
// desired visual column is kept alive by virtual spaces past the line end.
func (ev *EditorView) syncVirtualSpaces() {
	lineLen := ev.getLineLength(ev.CursorLine)
	if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
		_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
		if ev.DesiredVisualCol > endVCol {
			ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
			return
		}
	}
	ev.CursorVirtualSpaces = 0
}

// scrollViewBy scrolls the text by delta visual rows under a cursor that
// keeps its row on the screen: far2l's Editor::ScrollUp/ScrollDown move
// TopScreen and CurLine together, preserving the cell column. Only when the
// view cannot scroll any further — the top of the file is already shown, or
// the last screenful is — do they fall back to a bare Up()/Down() and let the
// cursor walk on alone. Nothing happens once the cursor sits on the first or
// last row. The selection is left untouched: scrolling is not a navigation.
func (ev *EditorView) scrollViewBy(delta int) {
	height := ev.Y2 - ev.Y1
	if height <= 0 {
		return
	}
	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	vRow, _ := ev.engine.LogicalToVisual(curOffset)
	totalRows := ev.engine.GetTotalVisualRows()

	targetRow := vRow + delta
	if targetRow < 0 || targetRow >= totalRows {
		return
	}

	maxTop := max(totalRows-height, 0)
	ev.ScrollTopRow = min(max(ev.ScrollTopRow+delta, 0), maxTop)

	newOffset := ev.engine.VisualToLogical(targetRow, ev.DesiredVisualCol)
	ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
	ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
	ev.syncVirtualSpaces()
	ev.ensureCursorVisible()
	vtui.FrameManager.Redraw()
}

// restoreTargetPos applies the saved position once the line it targets is
// indexed; the indexer's batches call it, and so does StartIndexing's early
// return for fully read files where no scan will ever run.
func (ev *EditorView) restoreTargetPos() bool {
	if ev.targetLine == -1 {
		return false
	}
	ev.CursorLine = max(min(ev.targetLine, ev.li.LineCount()-1), 0)
	if ev.asyncBuf != nil && ev.targetPos >= 0 {
		_, _ = ev.asyncBuf.Read(ev.li.GetLineOffset(ev.CursorLine)+ev.targetPos, 4096)
	}
	ev.CursorPos = ev.targetPos
	ev.ScrollTopRow = ev.targetTopRow
	ev.ScrollLeft = max(ev.targetLeft, 0)
	ev.targetLine = -1
	ev.ensureCursorVisible()
	ev.updateDesiredVisualCol()
	return true
}

// awaitOffset asks for the cursor to land at a byte offset the index cannot
// place yet, and reports whether the wait was needed. The alternative, when
// the index stops short of the offset, is to answer with the last line it
// knows and a column counted from there — which on a large file is a column
// measured in gigabytes.
func (ev *EditorView) awaitOffset(offset int) bool {
	if offset < 0 {
		return false
	}
	if ev.ensureIndexedTo(offset) {
		ev.targetOffset = -1
		line := ev.li.GetLineAtOffset(offset)
		ev.CursorLine = line
		ev.CursorPos = offset - ev.li.GetLineOffset(line)
		ev.updateDesiredVisualCol()
		return false
	}
	// Until the scan gets there the cursor sits at the top rather than
	// somewhere arbitrary, and the file is readable while it waits.
	ev.targetOffset = offset
	ev.targetLine = -1
	ev.CursorLine = 0
	ev.CursorPos = 0
	if !ev.indexing && !ev.indexIsComplete() {
		ev.StartIndexing()
	}
	return true
}

// awaitOffsetAsync moves to an offset without doing the initial index read on
// the UI goroutine. This is used for mode changes where indexing a large file
// can otherwise make the editor appear to hang.
func (ev *EditorView) awaitOffsetAsync(offset int) bool {
	if offset < 0 {
		return false
	}
	if ev.indexIsComplete() || ev.li.GetLineOffset(ev.li.LineCount()-1) > offset {
		ev.targetOffset = -1
		line := ev.li.GetLineAtOffset(offset)
		ev.CursorLine = line
		ev.CursorPos = offset - ev.li.GetLineOffset(line)
		ev.updateDesiredVisualCol()
		return false
	}
	ev.targetOffset = offset
	ev.targetLine = -1
	ev.CursorLine = 0
	ev.CursorPos = 0
	if !ev.indexing {
		ev.StartIndexing()
	}
	return true
}

// resolveTargetOffset places the cursor once the scan has read past the offset
// it was asked to wait for. The scan's batches call it, as they call
// restoreTargetPos for a target that was known as a line all along.
//
// "Read past" means the index holds a line that starts after the offset, or
// describes the whole buffer: only then is the line the offset falls in the
// last word on the subject, rather than the last line the index happens to
// know.
func (ev *EditorView) resolveTargetOffset() bool {
	if ev.targetOffset < 0 {
		return false
	}
	last := ev.li.GetLineOffset(ev.li.LineCount() - 1)
	if last <= ev.targetOffset && !ev.indexIsComplete() {
		return false
	}
	offset := min(ev.targetOffset, max(ev.pt.Size()-1, 0))
	ev.targetOffset = -1
	line := ev.li.GetLineAtOffset(offset)
	ev.CursorLine = line
	ev.CursorPos = offset - ev.li.GetLineOffset(line)
	ev.ensureCursorVisible()
	ev.updateDesiredVisualCol()
	vtui.FrameManager.Redraw()
	return true
}

func (ev *EditorView) ensureCursorVisible() {
	if ev.targetLine != -1 {
		return // Skip clamping and scrolling while waiting for the target line to be indexed
	}
	if ev.WordWrap && ev.currentLineUnsafeForWordWrap() {
		ev.disableUnsafeWordWrap()
	}

	if ev.HexMode || ev.DecodeMode {
		height := ev.Y2 - ev.Y1
		if height <= 0 {
			return
		}
		absPos := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos

		if ev.HexMode {
			row := absPos / 16
			topRow := ev.HexTopOffset / 16

			if row < topRow {
				ev.HexTopOffset = row * 16
			} else if row >= topRow+height {
				ev.HexTopOffset = (row - height + 1) * 16
			}
		} else {
			curr := ev.HexTopOffset
			visible := false
			for y := 0; y < height; y++ {
				if curr >= ev.pt.Size() {
					break
				}
				instLen := ev.decodeStep(curr)
				if instLen == 0 {
					instLen = 1
				}
				if absPos >= curr && absPos < curr+instLen {
					visible = true
					break
				}
				curr += instLen
			}
			if !visible {
				ev.HexTopOffset = absPos
			}
		}
		return
	}

	// Safety constraints for binary files or corrupted indices
	if ev.CursorLine < 0 {
		ev.CursorLine = 0
	}
	if ev.CursorLine >= ev.li.LineCount() {
		ev.CursorLine = ev.li.LineCount() - 1
	}

	lineLen := ev.getLineLength(ev.CursorLine)
	if ev.CursorPos < 0 {
		ev.CursorPos = 0
	}
	if ev.CursorPos > lineLen {
		ev.CursorPos = lineLen
	}

	curOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	vRow, vCol := ev.engine.LogicalToVisual(curOffset)
	vCol += ev.CursorVirtualSpaces

	width := ev.X2 - ev.X1 + 1
	height := ev.Y2 - ev.Y1

	if ev.scrollBar != nil {
		width--
	}
	if width <= 0 || height <= 0 {
		return
	}

	// 1. Вертикальный скролл
	if vRow < ev.ScrollTopRow {
		ev.ScrollTopRow = vRow
	} else if vRow >= ev.ScrollTopRow+height {
		ev.ScrollTopRow = vRow - height + 1
	}

	// 2. Горизонтальный скролл (только если WordWrap выключен)
	if !ev.WordWrap {
		if vCol < ev.ScrollLeft {
			ev.ScrollLeft = vCol
		} else if vCol >= ev.ScrollLeft+width {
			ev.ScrollLeft = vCol - width + 1
		}
		if ev.ScrollLeft < 0 {
			ev.ScrollLeft = 0
		}
	} else {
		ev.ScrollLeft = 0
	}
}

func (ev *EditorView) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return false
	}
	if e.ButtonState != 0 && ev.targetLine != -1 {
		ev.targetLine = -1
		ev.ensureCursorVisible()
	}

	if e.WheelDirection == 0 {
		if changed := ev.updateURLHover(int(e.MouseX), int(e.MouseY)); changed {
			vtui.FrameManager.Redraw()
		}
		if ctrlMouseClick(e) {
			if link, ok := ev.urlLinkAtMouse(int(e.MouseX), int(e.MouseY)); ok {
				openExternalURLAsync(link.URL)
				return true
			}
		}
	}

	// A rectangular mouse drag keeps ownership of the gesture even when a
	// backend reports motion without the button bit. On release, copy the
	// block and leave it highlighted so it remains useful for follow-up edit
	// operations.
	if ev.mouseRectSelecting {
		if e.MouseEventFlags&vtinput.MouseMoved != 0 {
			if ev.updateCursorFromMouse(int(e.MouseX), int(e.MouseY)) {
				vtui.FrameManager.Redraw()
			}
			return true
		}
		if e.ButtonState == 0 || !e.KeyDown {
			ev.mouseRectSelecting = false
			if ev.rectSelActive {
				atStart := ev.rectSelStartLine == ev.CursorLine &&
					ev.rectSelStartCol == ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
				if atStart {
					ev.rectSelActive = false
				} else {
					ev.CopySelection()
				}
			}
			vtui.FrameManager.Redraw()
			return true
		}
	}

	if ev.scrollBar != nil && ev.scrollBar.ProcessMouse(e) {
		return true
	}

	if e.WheelDirection != 0 {
		speed := AppConfig.WheelEditorDown
		vk := uint16(vtinput.VK_DOWN)
		if e.WheelDirection > 0 {
			speed = AppConfig.WheelEditorUp
			vk = vtinput.VK_UP
		}
		for i := 0; i < wheelScrollLines(speed); i++ {
			ev.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk})
		}
		return true
	}

	switch e.ButtonState {
	case vtinput.FromLeft1stButtonPressed:
		mx, my := int(e.MouseX), int(e.MouseY)
		if mx >= ev.X1 && mx <= ev.X2 && my >= ev.Y1+1 && my <= ev.Y2 {
			visualCol := mx - ev.X1 + ev.ScrollLeft
			visualRow := my - (ev.Y1 + 1) + ev.ScrollTopRow
			offset := ev.snapMouseOffsetToClusterBoundary(ev.engine.VisualToLogical(visualRow, visualCol))

			if e.MouseEventFlags&vtinput.DoubleClick != 0 {
				ev.CursorLine = ev.li.GetLineAtOffset(offset)
				ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
				ev.selectWordUnderCursor()
			} else if editorBlockMouseSelection(e) {
				ev.selActive = false
				ev.rectSelActive = false
				ev.CursorLine = ev.li.GetLineAtOffset(offset)
				ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
				ev.updateDesiredVisualCol()

				if e.MouseEventFlags&vtinput.MouseMoved == 0 {
					ev.rectSelActive = true
					ev.rectSelStartLine = ev.CursorLine
					ev.rectSelStartCol = visualCol
					ev.mouseRectSelecting = true
				}
			} else {
				if !ev.selActive || e.MouseEventFlags&vtinput.MouseMoved == 0 {
					ev.selActive = false
					ev.rectSelActive = false
					ev.CursorLine = ev.li.GetLineAtOffset(offset)
					ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
					ev.updateDesiredVisualCol()

					if e.MouseEventFlags&vtinput.MouseMoved == 0 {
						ev.selActive = true
						ev.selAnchorOffset = offset
					}
				} else if ev.selActive && e.MouseEventFlags&vtinput.MouseMoved != 0 {
					ev.CursorLine = ev.li.GetLineAtOffset(offset)
					ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
					ev.updateDesiredVisualCol()
				}
			}
			ev.ensureCursorVisible()
			vtui.FrameManager.Redraw()
			return true
		}
	case vtinput.RightmostButtonPressed:
		mx, my := int(e.MouseX), int(e.MouseY)
		if mx >= ev.X1 && mx <= ev.X2 && my >= ev.Y1+1 && my <= ev.Y2 {
			visualCol := mx - ev.X1 + ev.ScrollLeft
			visualRow := my - (ev.Y1 + 1) + ev.ScrollTopRow
			offset := ev.snapMouseOffsetToClusterBoundary(ev.engine.VisualToLogical(visualRow, visualCol))

			if !ev.rectSelActive || e.MouseEventFlags&vtinput.MouseMoved == 0 {
				ev.selActive = false
				ev.rectSelActive = false
				ev.CursorLine = ev.li.GetLineAtOffset(offset)
				ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
				ev.updateDesiredVisualCol()

				if e.MouseEventFlags&vtinput.MouseMoved == 0 {
					ev.rectSelActive = true
					ev.rectSelStartLine = ev.CursorLine
					ev.rectSelStartCol = visualCol
					ev.mouseRectSelecting = true
				}
			} else if ev.rectSelActive && e.MouseEventFlags&vtinput.MouseMoved != 0 {
				ev.CursorLine = ev.li.GetLineAtOffset(offset)
				ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
				ev.updateDesiredVisualCol()
			}
			ev.ensureCursorVisible()
			vtui.FrameManager.Redraw()
			return true
		}
	case 0:
		if ev.selActive && ev.selAnchorOffset == ev.li.GetLineOffset(ev.CursorLine)+ev.CursorPos {
			ev.selActive = false
			vtui.FrameManager.Redraw()
		}
		if ev.rectSelActive && ev.rectSelStartLine == ev.CursorLine && ev.rectSelStartCol == ev.getVisualColOf(ev.CursorLine, ev.CursorPos) {
			ev.rectSelActive = false
			vtui.FrameManager.Redraw()
		}
	}

	return false
}

func editorBlockMouseSelection(e *vtinput.InputEvent) bool {
	mods := e.ControlKeyState
	return mods&(vtinput.LeftAltPressed|vtinput.RightAltPressed) != 0 &&
		mods&vtinput.ShiftPressed != 0
}

func (ev *EditorView) updateCursorFromMouse(mx, my int) bool {
	if mx < ev.X1 || mx > ev.X2 || my < ev.Y1+1 || my > ev.Y2 {
		return false
	}
	visualCol := mx - ev.X1 + ev.ScrollLeft
	visualRow := my - (ev.Y1 + 1) + ev.ScrollTopRow
	offset := ev.snapMouseOffsetToClusterBoundary(ev.engine.VisualToLogical(visualRow, visualCol))
	ev.CursorLine = ev.li.GetLineAtOffset(offset)
	ev.CursorPos = offset - ev.li.GetLineOffset(ev.CursorLine)
	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
	return true
}

func (ev *EditorView) urlLinkAtMouse(mx, my int) (urlLink, bool) {
	link, _, ok := ev.urlLinkLocationAtMouse(mx, my)
	return link, ok
}

func (ev *EditorView) urlLinkLocationAtMouse(mx, my int) (urlLink, int, bool) {
	if ev.HexMode || ev.DecodeMode || mx < ev.X1 || mx > ev.X2 || my < ev.Y1+1 || my > ev.Y2 {
		return urlLink{}, 0, false
	}
	width := ev.X2 - ev.X1 + 1
	if ev.scrollBar != nil {
		width--
	}
	if mx >= ev.X1+width {
		return urlLink{}, 0, false
	}
	visualCol := mx - ev.X1 + ev.ScrollLeft
	visualRow := my - (ev.Y1 + 1) + ev.ScrollTopRow
	offset := ev.engine.VisualToLogical(visualRow, visualCol)
	line := ev.li.GetLineAtOffset(offset)
	lineStart := ev.li.GetLineOffset(line)
	lineEnd := ev.pt.Size()
	if line+1 < ev.li.LineCount() {
		lineEnd = ev.li.GetLineOffset(line + 1)
	}
	rel := offset - lineStart
	links := ev.urlLinksNearOffset(lineStart, lineEnd, rel)
	if link, ok := urlLinkAt(links, rel); ok {
		return link, lineStart + link.Start, true
	}
	if rel > 0 {
		if link, ok := urlLinkAt(links, rel-1); ok {
			return link, lineStart + link.Start, true
		}
	}
	return urlLink{}, 0, false
}

func (ev *EditorView) updateURLHover(mx, my int) bool {
	var next string
	start := -1
	if link, linkStart, ok := ev.urlLinkLocationAtMouse(mx, my); ok {
		next = link.URL
		start = linkStart
	}
	if next == ev.hoverURL && start == ev.hoverURLStart {
		ev.hoverURLStart = start
		return false
	}
	ev.hoverURL = next
	ev.hoverURLStart = start
	return true
}

func (ev *EditorView) urlLinksNearOffset(lineStart, lineEnd, rel int) []urlLink {
	if lineEnd <= lineStart {
		return nil
	}
	readStart, readEnd := lineStart, lineEnd
	if readEnd-readStart > maxURLScanBytes {
		readStart = lineStart + rel - 4096
		if readStart < lineStart {
			readStart = lineStart
		}
		readEnd = readStart + maxURLScanBytes
		if readEnd > lineEnd {
			readEnd = lineEnd
			readStart = readEnd - maxURLScanBytes
			if readStart < lineStart {
				readStart = lineStart
			}
		}
	}
	data, err := ev.pt.GetRange(readStart, readEnd-readStart)
	if err != nil {
		return nil
	}
	links := findURLLinks(string(data))
	base := readStart - lineStart
	for i := range links {
		links[i].Start += base
		links[i].End += base
	}
	return links
}

func (ev *EditorView) urlLinksForLine(lineStart, lineEnd int) []urlLink {
	if ev.hoverURL == "" || ev.hoverURLStart < lineStart || ev.hoverURLStart >= lineEnd {
		return nil
	}
	return ev.urlLinksNearOffset(lineStart, lineEnd, ev.hoverURLStart-lineStart)
}

func (ev *EditorView) SetPosition(x1, y1, x2, y2 int) {
	ev.ScreenObject.SetPosition(x1, y1, x2, y2)
	if ev.topBar != nil {
		ev.topBar.SetPosition(x1, y1, x2, y1)
	}
	if ev.menuBar != nil {
		ev.menuBar.SetPosition(x1, y1, x2, y1)
	}
	if ev.scrollBar != nil {
		ev.scrollBar.SetPosition(x2, y1+1, x2, y2)
		ev.scrollBar.PgStep = y2 - y1
	}
	ev.ensureEngineWidth()
	ev.ensureCursorVisible()
}

func (ev *EditorView) ResizeConsole(w, h int) {
	// Редактор в f4 занимает всё пространство до KeyBar (h-1)
	ev.SetPosition(0, vtui.FrameManager.WorkspaceTopInset(), w-1, h-2)
}

// GetMenuBar returns the editor's menu bar. Items are regenerated from
// the action registry on every call, so shortcuts and toggle states are
// always current.
func (ev *EditorView) GetMenuBar() *vtui.MenuBar {
	ev.menuBar.Items = BuildMenuBarItems("Editor")
	return ev.menuBar
}

// The async buffer hands data over as it loads. A single fixed sleep is a
// hard cap on indexing throughput: when the loader delivers pieces smaller
// than one chunk, the indexer spends the whole load asleep rather than
// scanning. Backing off geometrically from a short first wait keeps that loss
// bounded without turning a slow VFS into a busy loop.
const (
	indexPollMin = 200 * time.Microsecond
	indexPollMax = 5 * time.Millisecond
)

func nextIndexPoll(cur time.Duration) time.Duration {
	next := cur * 2
	if next < indexPollMin {
		next = indexPollMin
	}
	if next > indexPollMax {
		next = indexPollMax
	}
	return next
}

// indexScanBuffers holds the read buffers scans borrow, so that resuming after
// every burst of typing does not allocate a fresh four megabytes each time.
var indexScanBuffers sync.Pool

// indexReadChunk is how much of the file the scan asks for at a time when it
// reads the file rather than the mapping. Large enough that the kernel reads
// ahead of it, small enough that cancelling the scan is still immediate.
const indexReadChunk = 4 << 20

// fileChunkReader fills dst from the file for a stretch of the text, and
// reports false when that stretch has to be read through the piece table.
type fileChunkReader func(ctx context.Context, dst []byte, offset, length int) (int, bool)

// chunkReader captures the buffer, the file and the mapping as they stand, and
// returns a reader that fills dst from the file itself for a stretch of text
// the piece table says is still the original's — or one that always declines,
// when the file cannot answer for this buffer.
//
// A mapped file can be scanned by walking the mapping, which is what the piece
// table hands out, but walking it means a page fault per page and each fault
// waits for its own page. Asking the file for megabytes at a time is the same
// bytes at the speed the disk can deliver them: the 8 GB test file indexes in
// ~4 s instead of ~18. The mapping is undisturbed; it stays what the paint
// path reads, where a window onto the file is exactly what is wanted.
//
// Capturing is the point of the closure. A scan runs on its own goroutine
// while the UI thread may reload the file in another codepage or swap the
// buffer after a Replace All, and both leave the mapping and the descriptor in
// place while the text is no longer the file's. Reading the fields per chunk
// would race, and worse: a position in the new buffer is not a position in the
// file, so the scan would index one thing's bytes at another thing's offsets
// and quietly hand back line starts that are in the middle of lines. Hence the
// identity check — the original buffer has to *be* the mapping.
func (ev *EditorView) chunkReader() fileChunkReader {
	decline := func(context.Context, []byte, int, int) (int, bool) { return 0, false }

	pt, file, mapped := ev.pt, ev.file, ev.mapped
	if pt == nil || file == nil || mapped == nil {
		return decline
	}
	mapping := mapped.Bytes()
	// What the mapping skipped. A file that opens with a UTF-8 byte-order
	// mark is mapped from after it, so a position in this buffer is that
	// many bytes earlier than the same byte's position in the file. Reading
	// the file at buffer positions indexed the text three bytes out of step
	// and every line but the first lost its first three bytes, which is what
	// turned "разработанный" into "?зработанный" on screen. Captured here
	// with the rest, for the reasons above.
	base := mapped.FileOffset()
	orig, ok := pt.GetOriginalBuffer().(piecetable.MemoryBuffer)
	if !ok || len(mapping) == 0 || len(orig) != len(mapping) || &orig[0] != &mapping[0] {
		return decline
	}

	return func(ctx context.Context, dst []byte, offset, length int) (int, bool) {
		if dst == nil || length <= 0 || length > len(dst) {
			return 0, false
		}
		// False for anything that has been typed over, so edited text is
		// scanned through the piece table and the offsets keep describing the
		// buffer rather than the file.
		fileOffset, ok := pt.OriginalRange(offset, length)
		if !ok {
			return 0, false
		}
		n, err := file.ReadAt(ctx, dst[:length], int64(fileOffset)+base)
		if err != nil && err != io.EOF && ctx.Err() == nil {
			vtui.DebugLog("EDITOR_INDEX: reading %d bytes at %d returned %d and %v",
				length, int64(fileOffset)+base, n, err)
		}
		if n <= 0 {
			return 0, false
		}
		// A short read is still progress: the caller takes what arrived and
		// comes back for the rest.
		return n, true
	}
}

func (ev *EditorView) StartIndexing() {
	// Hex/decode render by byte offset: the index is only needed if the user
	// switches back to text, and scanning a big binary is a full read.
	if ev.HexMode || ev.DecodeMode {
		ev.targetLine = -1
		return
	}
	if ev.indexCancel != nil {
		ev.indexCancel()
	}
	ev.retireEditSession()
	sessionID := ev.editSession
	ev.probeUnsafeWordWrap()
	// Fully decoded files have their line index built with them; they still
	// need the safety scan before wrapping can be enabled. A mapped file has
	// neither: it starts with an empty index the scan below fills, and that
	// scan runs the same safety check on every chunk it reads.
	//
	// The table is read here, on the UI thread, and handed to the scans
	// below rather than reached through ev.pt from inside them. A piece
	// table is safe to share -- it locks its own pieces -- but the field
	// that names one is not: SetText builds a replacement and assigns it,
	// so a scan that dereferenced the field per chunk would sooner or later
	// follow it into a table New is still filling in. One pointer taken
	// once is also one consistent view of the text, which is what the scan
	// wants anyway.
	pt := ev.pt
	if ev.asyncBuf == nil && ev.mapped == nil {
		// Fully read files (non-UTF-8 codepages) have a complete index and no
		// line scan; resolve a pending restore here or Loading waits forever.
		ev.restoreTargetPos()
		ev.resolveTargetOffset()
		ctx, cancel := context.WithCancel(context.Background())
		ev.indexCancel = cancel
		ev.indexWG.Add(1)
		go func() {
			defer ev.indexWG.Done()
			// zoin-bot keeps mapped-file faults recoverable in the fully-read
			// indexing path just like in the lazy-buffer indexing goroutine.
			defer ev.guardMapping("indexing fully-read buffer")()
			ev.scanFullyReadForUnsafeWordWrap(ctx, sessionID, pt)
		}()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	ev.indexCancel = cancel
	ev.indexing = true
	ev.setIndexStatus(IndexStatus{
		Phase:   IndexScanning,
		Total:   int64(pt.Size()),
		Scanned: int64(ev.li.GetLineOffset(ev.li.LineCount() - 1)),
		Lines:   ev.li.LineCount(),
	})

	// Captured here, on the UI thread, so the scan reads one consistent view
	// of the buffer, the file and the mapping rather than whatever they are
	// when each chunk comes round. The buffer and the index join pt above
	// for the same reason: the fields naming them are the UI thread's.
	readFromFile := ev.chunkReader()
	buf := ev.asyncBuf
	li := ev.li
	// The logical size, not the file's: the scan reads the text as it is
	// now, including whatever has been typed into it.
	maxSize := pt.Size()

	ev.indexWG.Add(1)
	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	uiFrames := vtui.FrameManager
	go func() {
		defer ev.indexWG.Done()
		defer ev.guardMapping("indexing")()
		startedAt := time.Now()
		vtui.DebugLog("EDITOR_INDEX: Start indexing with targetLine=%d", ev.targetLine)
		indexed, batches := 0, 0
		var waited time.Duration
		// How far the scan has read, for the task below that settles the phase
		// after this goroutine is gone.
		var scannedTo atomic.Int64

		// Runs on completion and on cancellation alike, so the flag the
		// highlight walker throttles on can never stay stuck at true.
		defer func() {
			reached := scannedTo.Load()
			uiFrames.PostTask(func() {
				if ev.editSession == sessionID {
					ev.indexing = false
					st := ev.indexStatus
					st.Lines = ev.li.LineCount()
					st.Scanned = reached
					switch {
					case ctx.Err() != nil:
						// Cancelled, so the index is simply short: whatever
						// stopped it decides whether a resume follows.
						st.Phase = IndexIdle
					case reached >= st.Total:
						st.Phase = IndexComplete
					default:
						st.Phase = IndexFailed
					}
					ev.setIndexStatus(st)
				}
				vtui.DebugLog("EDITOR: Indexer stopped: %d lines in %v, %v of it waiting for data, %d UI batches",
					indexed, time.Since(startedAt), waited, batches)
			})
		}()

		poll := indexPollMin

		if indexer, ok := ev.vfs.(vfs.LineIndexer); ok && ev.Codepage == 65001 {
			vtui.DebugLog("EDITOR_INDEX: Using remote LineIndexer")
			var currentLine = int64(li.LineCount() + 1)
			remoteLineStart := int64(li.GetLineOffset(li.LineCount() - 1))
			const batchSize = 100000
			remoteSuccess := true
			wrapUnsafe := false
			for {
				if ctx.Err() != nil || ev.IsDone() {
					return
				}
				res, err := indexer.LineIndex(ctx, ev.filePath, currentLine, batchSize)
				if err != nil {
					vtui.DebugLog("EDITOR_INDEX: Remote LineIndex failed: %v, falling back to local", err)
					remoteSuccess = false
					break
				}

				if len(res.Offsets) > 0 {
					for _, off := range res.Offsets {
						if editorWrapIntervalUnsafe(remoteLineStart, off) {
							if !wrapUnsafe {
								ev.postUnsafeWordWrap(sessionID, ctx)
								wrapUnsafe = true
							}
							break
						}
						remoteLineStart = off
					}
					batchOffsets := make([]int, 0, len(res.Offsets))
					for _, off := range res.Offsets {
						batchOffsets = append(batchOffsets, int(off))
					}

					uiFrames.PostTask(func() {
						if ctx.Err() != nil || ev.editSession != sessionID {
							return
						}
						lastLineBefore := li.LineCount() - 1
						li.AppendOffsets(batchOffsets, maxSize)

						if ev.targetLine != -1 && (li.LineCount() > ev.targetLine || len(res.Offsets) < batchSize) {
							ev.restoreTargetPos()
						}
						// Unconditional: a position waiting as a byte offset
						// has no target line to be reached, which is the whole
						// reason it is waiting.
						ev.resolveTargetOffset()

						ev.engine.InvalidateFrom(lastLineBefore)
						if ev.highlighter != nil && !ev.highlighting && len(ev.lineStates) < li.LineCount() {
							ev.startHighlighting()
						}
						uiFrames.Redraw()
					})

					indexed += len(batchOffsets)
					batches++
					currentLine += int64(len(res.Offsets))
				}

				if len(res.Offsets) < batchSize || (res.Total >= 0 && currentLine > res.Total) {
					break
				}
			}

			if remoteSuccess {
				if editorWrapIntervalUnsafe(remoteLineStart, int64(maxSize)) && !wrapUnsafe {
					ev.postUnsafeWordWrap(sessionID, ctx)
				}
				scannedTo.Store(int64(maxSize))
				uiFrames.PostTask(func() {
					if ctx.Err() == nil && ev.editSession == sessionID {
						if ev.restoreTargetPos() {
							uiFrames.Redraw()
						}
						ev.resolveTargetOffset()
						if ev.highlighter != nil && !ev.highlighting && len(ev.lineStates) < li.LineCount() {
							ev.startHighlighting()
						}
					}
				})
				return
			}
		}

		// Resume from the last line already indexed rather than from the top:
		// the offsets before it were kept correct through any edits, and the
		// text is read through the piece table, so they still describe it.
		absPos := 0
		if li.LineCount() > 1 {
			absPos = li.GetLineOffset(li.LineCount() - 1)
		}
		// Record the starting point too, so that a resume with nothing left to
		// read still reports having reached the end rather than looking as
		// though it stopped short.
		scannedTo.Store(int64(absPos))
		// 256KB chunks to match AsyncBuffer, which is what the piece table
		// reads through when the file cannot be read directly.
		const fallbackChunk = 256 * 1024

		// A file that can be read directly is read in much larger pieces —
		// see chunkReader. The buffer it lands in is borrowed for the scan
		// rather than allocated per scan: a resume fires 400 ms after every
		// burst of typing, and 4 MB a time adds up.
		scanBuf, _ := indexScanBuffers.Get().(*[]byte)
		if scanBuf == nil {
			b := make([]byte, indexReadChunk)
			scanBuf = &b
		}
		defer indexScanBuffers.Put(scanBuf)

		pendingOffsets := make([]int, 0, 10000)
		lineLen := 0
		wrapUnsafe := false

		for absPos < maxSize {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if ev.IsDone() {
				return
			}

			// Prefetching still goes to the chunk buffer, which is what has
			// latency to hide; the read itself goes through the piece table,
			// so the offsets this produces describe the text as edited rather
			// than the file as it sits on disk. That is what lets a scan
			// interrupted by an edit resume instead of starting over.
			if buf != nil {
				// 16 chunks of 256KB = 4MB read-ahead sliding window.
				readAhead := 16
				for i := 0; i < readAhead; i++ {
					p := absPos + i*fallbackChunk
					if p < maxSize {
						_, _ = buf.Read(p, fallbackChunk)
					}
				}
			}
			var data []byte
			var err error
			if n, ok := readFromFile(ctx, *scanBuf, absPos, min(indexReadChunk, maxSize-absPos)); ok {
				data = (*scanBuf)[:n]
			} else {
				// Reading through the piece table copies what it hands back,
				// so it asks for a chunk rather than a whole window.
				take := min(fallbackChunk, maxSize-absPos)
				if view, ok := pt.View(absPos, take); ok {
					data = view
				} else {
					data, err = pt.GetRange(absPos, take)
				}
			}
			if err == piecetable.ErrLoading {
				time.Sleep(poll)
				waited += poll
				poll = nextIndexPoll(poll)
				continue
			}
			poll = indexPollMin
			if err != nil {
				break
			}
			var unsafe bool
			lineLen, unsafe = scanEditorWrapSafety(data, lineLen)
			if unsafe && !wrapUnsafe {
				ev.postUnsafeWordWrap(sessionID, ctx)
				wrapUnsafe = true
			}

			pendingOffsets = piecetable.AppendNewlineOffsets(pendingOffsets, data, absPos)

			absPos += len(data)
			scannedTo.Store(int64(absPos))

			// Update UI in 5000-line batches, or immediately when targetLine is reached, to avoid UI thread congestion
			if len(pendingOffsets) >= 5000 || absPos >= maxSize || (ev.targetLine != -1 && li.LineCount()+len(pendingOffsets) > ev.targetLine) {
				currentBatch := pendingOffsets
				batchEnd := absPos
				indexed += len(currentBatch)
				batches++
				pendingOffsets = make([]int, 0, 10000)

				uiFrames.PostTask(func() {
					if ctx.Err() != nil || ev.editSession != sessionID {
						return
					}
					// Incremental update: we only need to invalidate visual cache
					// from the line that was previously the "last" one.
					lastLineBefore := li.LineCount() - 1
					li.AppendOffsets(currentBatch, maxSize)

					if ev.targetLine != -1 && (li.LineCount() > ev.targetLine || batchEnd >= maxSize) {
						ev.restoreTargetPos()
					}
					ev.resolveTargetOffset()

					ev.engine.InvalidateFrom(lastLineBefore)
					if ev.highlighter != nil && !ev.highlighting && len(ev.lineStates) < li.LineCount() {
						ev.startHighlighting()
					}
					// Say how far it has got. Nothing did, so the status line
					// held whatever the scan started from — 0% for the whole of
					// a fresh scan, which on a file big enough to watch reads as
					// a scan that is stuck rather than one that is working.
					ev.noteIndexProgress(int64(batchEnd), li.LineCount())
					uiFrames.Redraw()
				})
			}
		}

		uiFrames.PostTask(func() {
			if ctx.Err() == nil && ev.editSession == sessionID {
				if ev.restoreTargetPos() {
					uiFrames.Redraw()
				}
				ev.resolveTargetOffset()
				if ev.highlighter != nil && !ev.highlighting && len(ev.lineStates) < li.LineCount() {
					ev.startHighlighting()
				}
			}
		})
	}()
}

func (ev *EditorView) HandleCommand(cmd int, args any) bool {
	if cmd == vtui.CmClose {
		ev.closeDlg = ev.tryClose()
		return true
	}
	if cmd == CmSwitchToViewer {
		actionSwitchEditorToViewer(ev)
		return true
	}
	if cmd == CmSearch {
		ev.showSearchDialog()
		return true
	}
	if cmd == CmReplace {
		ev.showReplaceDialog()
		return true
	}
	if handleWorkspaceForkCommand(cmd, args) {
		return true
	}
	return ev.BaseFrame.HandleCommand(cmd, args)
}

func (ev *EditorView) tryClose() *vtui.Window {
	if ev.closeDlg != nil && !ev.closeDlg.IsDone() {
		return ev.closeDlg
	}
	if !ev.modified {
		ev.Close()
		return nil
	}

	msg := "The file has been modified.\nDo you want to save it?"
	dlg := vtui.ShowMessageOn(ev, " Confirm ", msg, []string{"&Save", "&Don't Save", "Cancel"})
	dlg.OnResult = func(code int) {
		switch code {
		case 0: // Save
			ev.SaveToFile(func() {
				ev.Close()
			})
		case 1: // Don't save
			ev.Close()
		}
	}
	return dlg
}

func (ev *EditorView) GetKeyLabels() *vtui.KeySet {
	nextCp := vfs.GetNextFastSwitchCodepage(ev.Codepage)
	nextCpName := vfs.DisplayCodepageName(nextCp)

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.EditorF1"), Msg("KeyBar.EditorF2"), Msg("KeyBar.EditorF3"),
			"", Msg("KeyBar.EditorF5"), Msg("KeyBar.F3"), Msg("KeyBar.EditorF7"), nextCpName, "", Msg("KeyBar.EditorF10"),
		},
	}
	res := KeyBarLabelsForArea("Editor", fallbacks)
	if hm := GlobalHotkeysMgr; hm != nil {
		if hm.GetAction("Editor", "F8") == "Editor.CodepageNext" {
			res.Normal[7] = nextCpName
		}
	}
	return res
}
func (ev *EditorView) showSearchDialog() {
	dlgW, dlgH := 66, 15
	dlg := vtui.NewCenteredDialog(dlgW, dlgH, Msg("Viewer.SearchTitle"))
	dlg.ShowClose = true

	lblPrompt := vtui.NewLabel(0, 0, Msg("Search.Prompt"), nil)
	editPattern := vtui.NewEdit(0, 0, 40, LastEditorSearch)
	attachHistoryUseLast(editPattern, searchTextHistoryID)
	editPattern.SelectAll()
	lblPrompt.FocusLink = editPattern
	dlg.SetFocusedItem(editPattern)

	chkCase := vtui.NewCheckbox(0, 0, Msg("Search.CaseSensitive"), false)
	if LastEditorSearchCase {
		chkCase.State = 1
	}

	chkWholeWord := vtui.NewCheckbox(0, 0, Msg("Search.WholeWords"), false)
	if LastEditorSearchWholeWord {
		chkWholeWord.State = 1
	}

	chkReverse := vtui.NewCheckbox(0, 0, Msg("Search.Reverse"), false)
	if LastEditorSearchReverse {
		chkReverse.State = 1
	}

	chkRegexp := vtui.NewCheckbox(0, 0, Msg("Search.Regex"), false)
	if LastEditorSearchRegexp {
		chkRegexp.State = 1
	}

	chkHex := vtui.NewCheckbox(0, 0, Msg("Search.HexPattern"), false)
	if LastEditorSearchHex {
		chkHex.State = 1
	}

	btnFind := vtui.NewButton(0, 0, Msg("Search.BtnFind"))
	btnFind.IsDefault = true
	btnAll := vtui.NewButton(0, 0, Msg("Search.BtnAll"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	dlg.AddItem(lblPrompt)
	dlg.AddItem(editPattern)
	dlg.AddItem(chkCase)
	dlg.AddItem(chkWholeWord)
	dlg.AddItem(chkReverse)
	dlg.AddItem(chkRegexp)
	dlg.AddItem(btnFind)
	dlg.AddItem(btnAll)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dlgW-4, dlgH-4)
	vbox.Add(lblPrompt, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editPattern, vtui.Margins{Top: 1}, vtui.AlignFill)

	col1 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col1.Add(chkCase, vtui.Margins{}, vtui.AlignLeft)
	col1.Add(chkWholeWord, vtui.Margins{Top: 1}, vtui.AlignLeft)
	col1.Add(chkReverse, vtui.Margins{Top: 1}, vtui.AlignLeft)

	col2 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col2.Add(chkRegexp, vtui.Margins{}, vtui.AlignLeft)

	rowChecks := vtui.NewHBoxLayout(0, 0, dlgW-4, 5)
	rowChecks.Add(col1, vtui.Margins{}, vtui.AlignFill)
	rowChecks.Add(col2, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnFind, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnAll, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	saveSearchParams := func() {
		LastEditorSearch = editPattern.GetText()
		commitHistory(editPattern, LastEditorSearch)
		LastEditorSearchCase = chkCase.State == 1
		LastEditorSearchReverse = chkReverse.State == 1
		LastEditorSearchRegexp = chkRegexp.State == 1
		LastEditorSearchWholeWord = chkWholeWord.State == 1
		SaveSession()
	}
	btnFind.OnClick = func() {
		LastEditorSearchHex = chkHex.State == 1
		saveSearchParams()
		dlg.Close()
		ev.Search(LastEditorSearch, LastEditorSearchCase, LastEditorSearchReverse, LastEditorSearchRegexp, LastEditorSearchWholeWord, false)
	}
	btnAll.OnClick = func() {
		LastEditorSearchHex = chkHex.State == 1
		saveSearchParams()
		dlg.Close()
		// FindAll doesn't support hex pattern search yet
		ev.FindAll(LastEditorSearch, LastEditorSearchCase, LastEditorSearchRegexp, LastEditorSearchWholeWord)
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}

// replaceAllFold is a case-insensitive ReplaceAll under Unicode simple
// case-folding (strings.EqualFold semantics). strcase locates matches in
// the original string — no lowered copy, so no offset drift — and a folded
// match can be a different byte length than old (K U+212A matches "k"),
// which is why the consumed length comes from CutPrefix per match.
func replaceAllFold(s, old, new string) string {
	if old == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	start := 0
	for {
		idx := strcase.Index(s[start:], old)
		if idx == -1 {
			b.WriteString(s[start:])
			break
		}
		pos := start + idx
		after, ok := strcase.CutPrefix(s[pos:], old)
		if !ok {
			// Cannot happen: Index just matched at pos. Bail rather than
			// loop forever if the two ever disagree.
			b.WriteString(s[start:])
			break
		}
		b.WriteString(s[start:pos])
		b.WriteString(new)
		start = len(s) - len(after)
	}
	return b.String()
}

func (ev *EditorView) Replace(pattern, replacement string, caseSensitive, reverse, regexp, wholeWord, all bool) {
	if pattern == "" {
		return
	}

	searchPattern := pattern
	if LastEditorSearchHex {
		var err error
		searchPattern, err = parseHexPatternToRegex(pattern)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid hex pattern:\n%v", err), []string{"&Ok"})
			return
		}
		regexp = true
		wholeWord = false
		caseSensitive = true

		repBytes, err := parseHexReplacement(replacement)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid hex replacement:\n%v", err), []string{"&Ok"})
			return
		}
		var escapedRepl strings.Builder
		for _, b := range repBytes {
			switch b {
			case '$':
				escapedRepl.WriteString("$$")
			case '\\':
				escapedRepl.WriteString("\\\\")
			default:
				escapedRepl.WriteByte(b)
			}
		}
		replacement = escapedRepl.String()
	}

	// Only whole-word matching needs the regex engine (for the \b wrapping);
	// literal replace, case-sensitive or folded, is handled without it.
	var re *coregex.Regex
	if regexp || wholeWord {
		var err error
		re, err = buildSearchRegex(searchPattern, caseSensitive, regexp, wholeWord)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid regular expression:\n%v", err), []string{"&Ok"})
			return
		}
	}

	if all {
		session := ev.editSession
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			defer ev.guardMapping("replacing")()
			// Dropping this error used to turn a not-yet-loaded buffer into an
			// empty one, so [ Replace all ] on a large file reported "not
			// found" and changed nothing.
			bytes, errBytes := ev.searchBuffer(ctx, session)
			if errBytes != nil {
				if ctx.Err() != nil {
					return
				}
				ctx.RunOnUI(func() {
					vtui.ShowMessage(" Error ", "Failed to read file buffer.", []string{"&Ok"})
				})
				return
			}
			text := bytesToString(bytes)
			var newText string
			switch {
			case re != nil:
				newText = string(renderReplacement(re, regexp, bytes, []byte(replacement)))
			case caseSensitive:
				newText = strings.ReplaceAll(text, pattern, replacement)
			default:
				newText = replaceAllFold(text, pattern, replacement)
			}
			if newText != text {
				ctx.RunOnUI(func() {
					ev.saveUndo(opOther)
					ev.SetText(newText)
					ev.modified = true
					ev.ensureCursorVisible()
					vtui.FrameManager.Redraw()
				})
			} else {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(Msg("Replace.ConfirmTitle"), Msg("Search.NotFound"), []string{Msg("vtui.Ok")})
				})
			}
		})
		return
	}

	// Interactive path, ported from Far Manager: every occurrence is
	// confirmed with a Replace / All / Skip / Cancel dialog; replacing
	// without confirmation is only reachable through [ Replace all ].
	st := &replaceLoop{
		ev:            ev,
		pattern:       pattern,
		replacement:   replacement,
		caseSensitive: caseSensitive,
		reverse:       reverse,
		regexp:        regexp,
		wholeWord:     wholeWord,
		re:            re,
		session:       ev.editSession,
	}
	// A selection that is exactly one occurrence (made deliberately by the
	// user, or left by a previous Cancel) is prompted as-is: re-searching
	// from its start could pick a wider match (regex "a+" around a
	// one-character selection) and silently replace more than was selected.
	if ev.selActive {
		selMin, selMax := ev.getSelectionRange()
		if data, err := ev.pt.Bytes(); err == nil && selMax > selMin &&
			selectionIsMatch(data[selMin:selMax], pattern, caseSensitive, re) {
			st.promptAt(data, selMin, selMax-selMin)
			return
		}
	}
	st.searchFrom = ev.searchSeedOffset(reverse, true)
	st.findNext()
}

// noteBufferEdit records that the buffer is about to change: the background
// line indexer is canceled and every editSession fence held by an in-flight
// task goes stale.
func (ev *EditorView) noteBufferEdit() {
	ev.codepageRaw = nil
	// A position waiting on the scan describes the text as it was; typing has
	// since moved it, and the cursor is where the user put it.
	ev.targetOffset = -1

	if !ev.edited {
		ev.edited = true
	}
	// The running scan is carrying offsets it worked out before this edit, so
	// it has to stop — but only for as long as it takes the edit to settle.
	// The offsets already in the index were moved along with the text by
	// UpdateAfterInsert and UpdateAfterDelete, and the scan reads the text
	// through the piece table, so it can pick up from the last line it knows.
	ev.cancelIndexing()
	ev.retireEditSession()
	ev.scheduleIndexResume()
}

// cancelIndexing stops a running scan and records that nothing is scanning.
// The scan's own cleanup cannot do that: it is fenced by editSession, and
// every caller here retires the session next, so the goroutine's deferred
// "indexing = false" would be refused as stale and the flag would read true
// for the rest of the session — which made scheduleIndexResume decline to
// restart and left awaitIndexForResults waiting for a scan that was gone.
func (ev *EditorView) cancelIndexing() {
	if ev.indexCancel != nil {
		ev.indexCancel()
		ev.indexCancel = nil
	}
	if !ev.indexing {
		return
	}
	ev.indexing = false
	if st := ev.indexStatus; st.Phase == IndexScanning {
		st.Phase = IndexIdle
		ev.setIndexStatus(st)
	}
}

// noteIndexRebuilt records what li.Rebuild managed. A rebuild that walked the
// whole buffer leaves the index complete and no scan owed; without saying so,
// a rebuilt index kept the phase of whatever scan it interrupted and the
// editor went on waiting for a scan that would never come.
//
// A rebuild that stopped short — a lazily loaded file with a chunk still on
// its way — must not be recorded as complete, or every reader of the index
// believes lines that are not there. That one is handed back to the scan,
// which resumes from where the rebuild got to.
func (ev *EditorView) noteIndexRebuilt(complete bool) {
	ev.indexing = false
	size := int64(ev.pt.Size())
	if !complete {
		ev.setIndexStatus(IndexStatus{
			Phase:   IndexIdle,
			Total:   size,
			Scanned: int64(ev.li.GetLineOffset(ev.li.LineCount() - 1)),
			Lines:   ev.li.LineCount(),
		})
		ev.StartIndexing()
		return
	}
	ev.setIndexStatus(IndexStatus{
		Phase:   IndexComplete,
		Total:   size,
		Scanned: size,
		Lines:   ev.li.LineCount(),
	})
}

// awaitIndexForResults blocks a search task until the line index can answer
// for offset end: the scan has read past it, the index describes the whole
// buffer, or nothing is filling it any more.
//
// A search reads the whole buffer, but it reports what it found as lines and
// columns, and those come from the index. Asking an index that stops short
// about an offset past its end answers with its last line and a column counted
// from there — so a Find All list opened while a file was still being scanned
// put every occurrence on line 1, with the whole file as its text. Waiting for
// the scan that is already reading those bytes is also what keeps the reading
// off the UI thread: the alternative, counting the gap when the answer is
// needed, is the freeze this whole change set exists to remove. Waiting only
// as far as the match, rather than for the whole scan, is what lets a hit on
// line 3 of an 8 GB file show up before the scan has read the other 8 GB.
//
// It reports false when the wait ended for any other reason — the task was
// cancelled, or the editor closed under it.
func (ev *EditorView) awaitIndexForResults(ctx *vtui.TaskContext, end int) bool {
	// Buffered, so that a late notification never blocks the UI thread on a
	// task that has already given up.
	ready := make(chan bool, 1)

	ctx.RunOnUI(func() {
		if ev.IsDone() {
			ready <- false
			return
		}
		// Nothing scanning means nothing is coming: an editor over a buffer
		// held in memory has its index built with it and never starts one.
		reached := func(st IndexStatus) bool {
			return st.Phase == IndexComplete || !ev.indexing || st.Scanned >= int64(end)
		}
		if reached(ev.indexStatus) {
			ready <- true
			return
		}
		var unsubscribe func()
		unsubscribe = ev.SubscribeIndex(func(st IndexStatus) {
			if reached(st) {
				unsubscribe()
				ready <- true
			}
		})
	})

	select {
	case ok := <-ready:
		return ok
	case <-ctx.Done():
		return false
	}
}

// bufferRange returns a stretch of the buffer for a caller that only reads it,
// without copying when the piece table can hand out a window. On a mapped file
// that window is the file, so reading a line to paint it touches the pages of
// that line and no others.
//
// It answers nil for a range that is not there, which every caller treats as
// an empty line rather than an error: these are paint paths.
func (ev *EditorView) bufferRange(offset, length int) []byte {
	size := ev.pt.Size()
	if offset < 0 || offset >= size || length <= 0 {
		return nil
	}
	if offset+length > size {
		length = size - offset
	}
	if view, ok := ev.pt.View(offset, length); ok {
		return view
	}
	data, err := ev.pt.GetRange(offset, length)
	if err != nil {
		return nil
	}
	return data
}

// readSearchWindow fills dst with one window of the buffer, preferring the
// file itself over the mapping for the same reason the scan does. read and pt
// are the caller's captured view of the buffer, so that a pass over a large
// file cannot end up mixing two of them. The result may alias dst or the
// buffer, and is only valid until the next call.
func (ev *EditorView) readSearchWindow(ctx context.Context, read fileChunkReader,
	pt *piecetable.PieceTable, dst []byte, offset, length int) ([]byte, error) {

	if n, ok := read(ctx, dst, offset, length); ok {
		return dst[:n], nil
	}
	if view, ok := pt.View(offset, length); ok {
		return view, nil
	}
	// An edited stretch, or a chunk buffer that has not fetched this yet. The
	// wait is the one readSearchSnapshot does, for the same reason.
	deadline := time.Now().Add(searchSnapshotStall)
	for {
		data, err := pt.GetRange(offset, length)
		if err == nil {
			return data, nil
		}
		if err != piecetable.ErrLoading {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(searchSnapshotPoll)
	}
}

// ensureIndexedToLine extends the line index until it describes the given
// line, when the running scan has not reached it yet. It is ensureIndexedTo
// asked the other way round, for a caller that knows which line it wants
// rather than which offset — switching to the viewer, which has to say where
// in the file the cursor was.
//
// A line the user has actually visited is already indexed, so this normally
// does nothing; it exists for the moment just after opening, when the cursor
// has been placed by a restored position and the scan is still on its way
// there.
func (ev *EditorView) ensureIndexedToLine(line int) {
	if line < 0 || line < ev.li.LineCount() {
		return
	}
	ev.extendIndexUntil(func(_ int, lines int) bool { return lines > line })
}

// ensureIndexedTo extends the line index far enough to describe offset, when
// the running scan has not reached it yet. It counts newlines from the last
// line the index knows about, which is the same work the scan would do — done
// here, now, because a caller is about to ask a question the index cannot
// otherwise answer.
//
// It runs on the UI thread and only ever covers the gap up to one offset, so
// the cost is the distance between the scan's position and the match, not the
// size of the file.
// It reports whether the index now describes that offset; false means the
// buffer could not be read that far — a lazily loaded file whose chunk has not
// arrived — and the caller must not turn the offset into a line.
func (ev *EditorView) ensureIndexedTo(offset int) bool {
	if offset < 0 {
		return false
	}
	return ev.extendIndexUntil(func(pos int, _ int) bool { return pos > offset })
}

// extendIndexUntil counts newlines from the last line the index knows about
// until done says so, given the position the count has reached and how many
// lines the index would hold if the pending ones were applied. It reports
// whether it got there: false means the buffer ran out first, or a chunk of
// it was still loading, and the index is still short.
//
// It reads the buffer on the UI thread, mapping and all, so the fault guard
// is armed the same as for every other reader of the mapping.
func (ev *EditorView) extendIndexUntil(done func(pos, lines int) bool) bool {
	if ev.indexIsComplete() {
		return true
	}
	defer ev.guardMapping("extending the line index")()

	pos := ev.li.GetLineOffset(ev.li.LineCount() - 1)
	if pos < 0 {
		return false
	}

	const step = 256 * 1024
	pending := make([]int, 0, 1024)
	// An index that already reaches the caller's mark got there without
	// reading anything, which is still getting there.
	reached := done(pos, ev.li.LineCount())
	for !done(pos, ev.li.LineCount()+len(pending)) {
		take := min(step, ev.pt.Size()-pos)
		if take <= 0 {
			break
		}
		data, err := ev.pt.GetRange(pos, take)
		if err != nil || len(data) == 0 {
			if err == piecetable.ErrLoading {
				vtui.DebugLog("EDITOR_INDEX: extending the index stopped at %d: chunk still loading", pos)
			}
			break
		}
		pending = piecetable.AppendNewlineOffsets(pending, data, pos)
		pos += len(data)
		reached = done(pos, ev.li.LineCount()+len(pending))
	}
	if len(pending) > 0 {
		ev.li.AppendOffsets(pending, ev.pt.Size())
	}
	return reached
}

// indexResumeDelay is how long the editor waits for typing to stop before
// resuming a scan. Restarting on every keystroke would spend more time
// starting and cancelling goroutines than reading.
const indexResumeDelay = 400 * time.Millisecond

// scheduleIndexResume arranges for an interrupted scan to continue once the
// edits stop arriving. A file whose index is already complete needs nothing:
// the incremental updates keep it that way.
func (ev *EditorView) scheduleIndexResume() {
	if ev.indexResume != nil {
		ev.indexResume.Stop()
	}
	// Read on the goroutine that starts this work, not inside it: the
	// work outlives the call, and reading the global from it races
	// anything that reassigns vtui.FrameManager meanwhile.
	uiFrames := vtui.FrameManager
	ev.indexResume = time.AfterFunc(indexResumeDelay, func() {
		uiFrames.PostTask(func() {
			if ev.IsDone() || ev.indexing || ev.indexIsComplete() {
				return
			}
			ev.StartIndexing()
		})
	})
}

// replaceRange replaces bytes [min, max) with repBytes as a single
// undoable edit and leaves the cursor at the end of the replacement.
func (ev *EditorView) replaceRange(min, max int, repBytes []byte) {
	ev.replaceSpans([]matchSpan{{min, max - min}}, [][]byte{repBytes})
}

// replaceSpans replaces each span (ascending, non-overlapping) with its
// rendered replacement as one undoable edit; the cursor lands at the end of
// the last replacement. Splicing per span keeps memory at the size of the
// replacements instead of the whole covered region.
func (ev *EditorView) replaceSpans(spans []matchSpan, renders [][]byte) {
	if len(spans) == 0 {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.modified = true

	// Splice bottom-up so the still-pending spans keep their offsets.
	for i := len(spans) - 1; i >= 0; i-- {
		s := spans[i]
		ev.pt.Delete(s.Off, s.Len)
		ev.li.UpdateAfterDelete(s.Off, s.Len)
		ev.pt.Insert(s.Off, renders[i])
		ev.li.UpdateAfterInsert(s.Off, renders[i])
	}

	// Invalidate from the edit, not from the cursor: a reverse replace
	// edits text above the current line.
	startLine := ev.li.GetLineAtOffset(spans[0].Off)
	ev.invalidateStates(startLine)
	ev.engine.InvalidateFrom(startLine)

	last := len(spans) - 1
	newCursorOff := spans[last].Off + len(renders[last])
	ev.CursorLine = ev.li.GetLineAtOffset(newCursorOff)
	ev.CursorPos = newCursorOff - ev.li.GetLineOffset(ev.CursorLine)

	ev.selActive = false
	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
	vtui.FrameManager.Redraw()
}

func (ev *EditorView) showReplaceDialog() {
	dlgW, dlgH := 66, 19
	dlg := vtui.NewCenteredDialog(dlgW, dlgH, Msg("Replace.Title"))
	dlg.ShowClose = true

	lblPrompt := vtui.NewLabel(0, 0, Msg("Search.Prompt"), nil)
	editPattern := vtui.NewEdit(0, 0, 40, LastEditorSearch)
	attachHistoryUseLast(editPattern, searchTextHistoryID)
	editPattern.SelectAll()
	lblPrompt.FocusLink = editPattern
	dlg.SetFocusedItem(editPattern)

	lblReplace := vtui.NewLabel(0, 0, Msg("Replace.Prompt"), nil)
	editReplace := vtui.NewEdit(0, 0, 40, LastEditorReplace)
	// Plain DIF_HISTORY here, no DIF_USELASTHISTORY: silently pre-filling a
	// replacement makes it far too easy to overwrite text with a stale string.
	attachHistory(editReplace, replaceTextHistoryID)
	editReplace.SelectAll()

	chkCase := vtui.NewCheckbox(0, 0, Msg("Search.CaseSensitive"), false)
	if LastEditorSearchCase {
		chkCase.State = 1
	}

	chkWholeWord := vtui.NewCheckbox(0, 0, Msg("Search.WholeWords"), false)
	if LastEditorSearchWholeWord {
		chkWholeWord.State = 1
	}

	chkReverse := vtui.NewCheckbox(0, 0, Msg("Search.Reverse"), false)
	if LastEditorSearchReverse {
		chkReverse.State = 1
	}

	chkRegexp := vtui.NewCheckbox(0, 0, Msg("Search.Regex"), false)
	if LastEditorSearchRegexp {
		chkRegexp.State = 1
	}

	chkHex := vtui.NewCheckbox(0, 0, Msg("Search.HexPattern"), false)
	if LastEditorSearchHex {
		chkHex.State = 1
	}

	btnReplace := vtui.NewButton(0, 0, Msg("Replace.BtnReplace"))
	btnReplaceAll := vtui.NewButton(0, 0, Msg("Replace.BtnReplaceAll"))
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	btnReplace.IsDefault = true

	dlg.AddItem(lblPrompt)
	dlg.AddItem(editPattern)
	dlg.AddItem(lblReplace)
	dlg.AddItem(editReplace)
	dlg.AddItem(chkCase)
	dlg.AddItem(chkWholeWord)
	dlg.AddItem(chkReverse)
	dlg.AddItem(chkRegexp)
	dlg.AddItem(btnReplace)
	dlg.AddItem(btnReplaceAll)
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dlgW-4, dlgH-4)
	vbox.Add(lblPrompt, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editPattern, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Add(lblReplace, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(editReplace, vtui.Margins{Top: 1}, vtui.AlignFill)

	col1 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col1.Add(chkCase, vtui.Margins{}, vtui.AlignLeft)
	col1.Add(chkWholeWord, vtui.Margins{Top: 1}, vtui.AlignLeft)
	col1.Add(chkReverse, vtui.Margins{Top: 1}, vtui.AlignLeft)

	col2 := vtui.NewVBoxLayout(0, 0, (dlgW-4)/2, 5)
	col2.Add(chkRegexp, vtui.Margins{}, vtui.AlignLeft)

	col2.Add(chkHex, vtui.Margins{Top: 1}, vtui.AlignLeft)

	col2.Add(chkHex, vtui.Margins{Top: 1}, vtui.AlignLeft)

	rowChecks := vtui.NewHBoxLayout(0, 0, dlgW-4, 5)
	rowChecks.Add(col1, vtui.Margins{}, vtui.AlignFill)
	rowChecks.Add(col2, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowChecks, vtui.Margins{Top: 1}, vtui.AlignFill)

	hbox := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Spacing = 2
	hbox.Add(btnReplace, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnReplaceAll, vtui.Margins{}, vtui.AlignTop)
	hbox.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	doReplace := func(all bool) {
		LastEditorSearch = editPattern.GetText()
		LastEditorReplace = editReplace.GetText()
		commitHistory(editPattern, LastEditorSearch)
		commitHistory(editReplace, LastEditorReplace)
		LastEditorSearchCase = chkCase.State == 1
		LastEditorSearchReverse = chkReverse.State == 1
		LastEditorSearchRegexp = chkRegexp.State == 1
		LastEditorSearchWholeWord = chkWholeWord.State == 1
		SaveSession()
		dlg.Close()
		LastEditorSearchHex = chkHex.State == 1
		ev.Replace(LastEditorSearch, LastEditorReplace, LastEditorSearchCase, LastEditorSearchReverse, LastEditorSearchRegexp, LastEditorSearchWholeWord, all)
	}
	btnReplace.OnClick = func() { doReplace(false) }
	btnReplaceAll.OnClick = func() { doReplace(true) }
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}

func (ev *EditorView) ReloadWithCodepage(cpID int) {
	if ev.Codepage == cpID {
		return
	}

	// The piece table contains decoded text, not the original file bytes.
	// Build the source stream once and reinterpret that same stream on every
	// subsequent switch. Re-encoding the result of the previous switch loses
	// information when the intermediate codepage cannot represent all glyphs.
	rawData := ev.codepageRaw
	if rawData == nil {
		bytes, err := ev.pt.Bytes()
		if err != nil {
			return
		}

		rawData, err = vfs.EncodeBytes(bytes, ev.Codepage)
		if err != nil {
			rawData = bytes // Fallback
		}
		ev.codepageRaw = append([]byte(nil), rawData...)
	}

	decoded, err := vfs.DecodeBytes(rawData, cpID)
	if err != nil {
		return
	}

	wasModified := ev.modified
	ev.saveUndo(opOther)

	oldLine := ev.CursorLine
	oldPos := ev.CursorPos
	oldScrollTop := ev.ScrollTopRow
	oldScrollLeft := ev.ScrollLeft
	oldVirtualSpaces := ev.CursorVirtualSpaces

	ev.SetText(string(decoded))
	// SetText invalidates edit-derived snapshots. Restore the source snapshot
	// because this wholesale replacement is the codepage view itself, not a
	// user edit.
	ev.codepageRaw = rawData

	ev.CursorLine = oldLine
	if ev.CursorLine >= ev.li.LineCount() {
		ev.CursorLine = ev.li.LineCount() - 1
	}
	if ev.CursorLine < 0 {
		ev.CursorLine = 0
	}
	ev.CursorPos = oldPos
	if ev.CursorPos > ev.getLineLength(ev.CursorLine) {
		ev.CursorPos = ev.getLineLength(ev.CursorLine)
	}
	ev.CursorVirtualSpaces = oldVirtualSpaces
	ev.ScrollTopRow = oldScrollTop
	ev.ScrollLeft = oldScrollLeft

	ev.Codepage = cpID
	ev.modified = wasModified

	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
	vtui.FrameManager.Redraw()
}

func (ev *EditorView) ReloadWithAutoDetect() {
	if ev.file == nil {
		return
	}
	size := ev.file.Size()
	detectLen := 16 * 1024
	if int64(detectLen) > size {
		detectLen = int(size)
	}
	header := make([]byte, detectLen)
	n, _ := ev.file.ReadAt(context.Background(), header, 0)
	header = header[:n]

	// The user asked for this file to be detected, so detect it -- the
	// global switch decides what happens at open, not here (#875).
	cpID := vfs.DetectEncoding(header, true, AppConfig.EditorDefaultCodePage)
	saveCodepageOverride(ev.vfs, ev.filePath, 0)
	ev.ReloadWithCodepage(cpID)
}

func (ev *EditorView) showCodepageDialog() {
	_, overridden := rememberedCodepage(ev.vfs, ev.filePath)
	items, currIdx := vfs.BuildCodepageMenuItems(ev.Codepage, !overridden)
	menu := newCodepageMenu(Msg("Codepage.Title"), items)

	// Per file, as in Far's Shift+F8; the global editor settings are not
	// touched from here (#875, see the viewer's dialog for why).
	menu.OnAction = func(idx int) {
		menu.Close()
		if idx >= 0 && idx < len(menu.Items) {
			if cpID, ok := menu.Items[idx].UserData.(int); ok {
				if cpID == vfs.CodepageAutoDetect {
					ev.ReloadWithAutoDetect()
				} else {
					saveCodepageOverride(ev.vfs, ev.filePath, cpID)
					ev.ReloadWithCodepage(cpID)
				}
			}
		}
	}
	menu.SetSelectPos(currIdx)
	vtui.FrameManager.Push(menu)
}
func (ev *EditorView) showConvertCodepageDialog() {
	items, _ := vfs.BuildCodepageMenuItems(ev.Codepage, false)
	converts := make([]vtui.MenuItem, 0, len(items))
	for _, item := range items {
		if item.UserData == vfs.CodepageAutoDetect {
			continue // Skip auto-detect
		}
		converts = append(converts, item)
	}
	menu := newCodepageMenu(Msg("Codepage.ConvertTitle"), converts)

	menu.OnAction = func(idx int) {
		menu.Close()
		if idx >= 0 && idx < len(menu.Items) {
			if cpID, ok := menu.Items[idx].UserData.(int); ok {
				ev.codepageRaw = nil
				ev.Codepage = cpID
				ev.modified = true
				showToast(fmt.Sprintf("Will be saved as: %s", vfs.DisplayCodepageName(cpID)), 2*time.Second)
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
				vtui.FrameManager.Redraw()
			}
		}
	}

	selIdx := 0
	for i, item := range menu.Items {
		if item.UserData == ev.Codepage {
			selIdx = i
			break
		}
	}
	menu.SetSelectPos(selIdx)
	vtui.FrameManager.Push(menu)
}

func (ev *EditorView) selectWordUnderCursor() {
	lineStart := ev.li.GetLineOffset(ev.CursorLine)
	lineRunes := ev.getLogicalLineRunes(ev.CursorLine)
	byteAcc := 0
	runeIdx := -1
	for i, r := range lineRunes {
		if byteAcc > ev.CursorPos {
			break
		}
		runeIdx = i
		byteAcc += utf8.RuneLen(r)
	}
	if runeIdx == -1 {
		runeIdx = len(lineRunes) - 1
	}

	if runeIdx >= 0 && runeIdx < len(lineRunes) {
		startIdx := runeIdx
		for startIdx > 0 && getCharCategory(lineRunes[startIdx-1]) == catWord {
			startIdx--
		}
		endIdx := runeIdx
		for endIdx < len(lineRunes) && getCharCategory(lineRunes[endIdx]) == catWord {
			endIdx++
		}

		startOff := 0
		for i := 0; i < startIdx; i++ {
			startOff += utf8.RuneLen(lineRunes[i])
		}
		endOff := startOff
		for i := startIdx; i < endIdx; i++ {
			endOff += utf8.RuneLen(lineRunes[i])
		}

		ev.selActive = true
		ev.selAnchorOffset = lineStart + startOff
		ev.CursorPos = endOff
		ev.updateDesiredVisualCol()
		ev.ensureCursorVisible()
	}
}
func (ev *EditorView) getLogicalLineRunes(line int) []rune {
	lineStart := ev.li.GetLineOffset(line)
	lineLen := ev.getLineLength(line)
	// Prevent OOM on huge binary lines for word navigation
	const maxRuneFetch = 32 * 1024
	if lineLen > maxRuneFetch {
		lineLen = maxRuneFetch
	}
	lineData, _ := ev.pt.GetRange(lineStart, lineLen)
	return []rune(string(lineData))
}
func (ev *EditorView) getLineLength(line int) int {
	if line < 0 || line >= ev.li.LineCount() {
		return 0
	}
	start := ev.li.GetLineOffset(line)
	size := ev.pt.Size()
	end := size
	if line+1 < ev.li.LineCount() {
		end = ev.li.GetLineOffset(line + 1)
	}

	totalLen := end - start
	if totalLen <= 0 {
		return 0
	}

	// Use a small buffer to check just the end of the line for line breaks
	// to avoid loading massive binary lines entirely.
	checkLen := 2
	if totalLen < checkLen {
		checkLen = totalLen
	}

	data, err := ev.pt.GetRange(start+totalLen-checkLen, checkLen)
	if err != nil || len(data) == 0 {
		return totalLen
	}

	// Safely decrease length if there are line breaks at the end.
	// We work with the end of the returned buffer.
	if data[len(data)-1] == '\n' {
		totalLen--
		if len(data) > 1 && data[len(data)-2] == '\r' {
			totalLen--
		}
	}
	return totalLen
}

// nopWriteCloser stands in for the destination handle when the file has
// already been assembled by the file system itself.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// patchPiecesFromTable describes the edited text as pieces of the file on
// disk plus the bytes that are new, which is exactly what a piece table is:
// a piece pointing at the original buffer is a range of the file, and one
// pointing at the add buffer is text the user typed.
//
// It is only meaningful while the original buffer still is the file. That
// holds for a raw UTF-8 load; with any other codepage the buffer holds
// decoded text whose offsets have nothing to do with the bytes on disk.
func patchPiecesFromTable(pt *piecetable.PieceTable) ([]vfs.PatchPiece, bool) {
	state := pt.GetState()
	pieces := make([]vfs.PatchPiece, 0, len(state.Pieces))
	logical := 0
	for _, p := range state.Pieces {
		if p.Buf == piecetable.Original {
			pieces = append(pieces, vfs.PatchPiece{Offset: int64(p.Start), Length: int64(p.Length)})
		} else {
			data, err := pt.GetRange(logical, p.Length)
			if err != nil {
				return nil, false
			}
			pieces = append(pieces, vfs.PatchPiece{
				Length: int64(len(data)),
				Data:   append([]byte(nil), data...),
			})
		}
		logical += p.Length
	}
	return pieces, true
}

// editorTempSibling returns a temporary name through the VFS path algebra
// instead of appending bytes to the serialized path. Cloud files use opaque
// canonical URIs (for example g:item:<id>), where `path + ".f4tmp"` corrupts
// the identifier rather than naming a sibling object.
func editorTempSiblingWithToken(filesystem vfs.VFS, filePath, token string) (string, error) {
	if filesystem == nil || filePath == "" {
		return "", os.ErrInvalid
	}
	if token == "" || strings.ContainsAny(token, `/\\:`) {
		return "", errors.New("invalid editor temporary-file token")
	}
	name := filesystem.Base(filePath)
	parent := filesystem.Dir(filePath)
	if name == "" || parent == "" {
		return "", errors.New("cannot resolve a sibling temporary path")
	}
	// Keep the component independent of the original basename. Appending a
	// token to a valid 240-byte filename can exceed the common 255-byte limit.
	// A unique hidden sibling plus explicit no-overwrite creation also prevents
	// concurrent editors (or a user file) from clobbering staged data.
	tempPath := filesystem.Join(parent, ".f4tmp-"+token)
	if tempPath == "" || tempPath == filePath {
		return "", errors.New("cannot create a distinct sibling temporary path")
	}
	return tempPath, nil
}

func cleanupEditorStage(filesystem vfs.VFS, tempPath string) {
	if filesystem == nil || tempPath == "" {
		return
	}
	// Cleanup is a single best-effort mutation detached from the task Cancel.
	// The VFS session lifetime still gates CloudFox operations internally.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = filesystem.Remove(cleanupCtx, tempPath)
}

func editorTempSibling(filesystem vfs.VFS, filePath string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate editor temporary-file name: %w", err)
	}
	return editorTempSiblingWithToken(filesystem, filePath, hex.EncodeToString(random[:]))
}

func (ev *EditorView) SaveToFile(afterSave func()) {
	ev.saveToFile(afterSave, false)
}

// saveToFile writes the buffer to ev.filePath. fullWrite disables the
// in-place and delta paths that describe the edit as pieces of the file on
// disk: after Save As (#899) the pieces still point into the file that was
// opened, while ev.filePath is a different file, or the same file that is
// about to change codepage or BOM. Sending such pieces to the destination
// would assemble it from the wrong bytes.
func (ev *EditorView) saveToFile(afterSave func(), fullWrite bool) {
	if ev.filePath == "" || ev.vfs == nil || ev.saving {
		return
	}

	ev.saving = true
	ev.edited = true
	vtui.DebugLog("EDITOR: Saving %s...", ev.filePath)

	// Stop indexing to prevent async reads on closed buffers
	ev.cancelIndexing()

	// Capture visible offset for preloading before we destroy the current engine
	visStart := ev.engine.VisualToLogical(ev.ScrollTopRow, 0)
	createNewTarget := ev.createNewTarget
	filePath := ev.filePath

	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		// The writer reads the unchanged pieces straight out of the mapping.
		defer ev.guardMapping("saving")()
		// To preserve original file ownership, permissions and xattrs (crucial for root-owned files),
		// we write directly to the original file instead of using atomic rename via temp file.
		oldAsync := ev.asyncBuf
		oldFile := ev.file
		//needsBufferRecovery := oldAsync != nil && ev.pt.GetOriginalBuffer() == oldAsync

		capabilities := ev.vfs.GetCapabilities()
		// Capture original metadata to restore it after atomic rename
		originalStat, statErr := ev.vfs.Stat(ctx.Context, filePath)
		if createNewTarget {
			var destinationErr error
			switch {
			case statErr == nil:
				destinationErr = vfs.ErrDestinationExists
			case !errors.Is(statErr, os.ErrNotExist):
				destinationErr = statErr
			}
			if destinationErr != nil {
				ctx.RunOnUI(func() {
					ev.saving = false
					vtui.ShowMessage(" Error ", fmt.Sprintf("Cannot create the new file without replacing an existing target:\n%v", destinationErr), []string{"&Ok"})
				})
				return
			}
			if !capabilities.HasAtomicNoReplaceRename {
				ctx.RunOnUI(func() {
					ev.saving = false
					vtui.ShowMessage(" Error ", fmt.Sprintf("This file system cannot safely create the new file without replacing a concurrent destination:\n%v", vfs.ErrNoReplaceUnsupported), []string{"&Ok"})
				})
				return
			}
		}

		// Google Drive can update media on the existing object ID. Prefer that
		// explicit capability over generic temp+Rename: replacing the Drive item
		// would sever permissions, share links, shortcuts, comments and history.
		identityPreservingWrite := capabilities.HasIdentityPreservingWrite && !createNewTarget
		// A colon denotes an NTFS alternate stream only for a local OS VFS.
		// Treating cloud:// as an ADS used to bypass staging and overwrite remote
		// objects directly on Windows.
		useTemp := !identityPreservingWrite && (!isLocalOSVFS(ev.vfs) || !isAlternateDataStream(filePath))
		tempPath := ""
		finalFilePath := filePath
		var f io.WriteCloser
		var err error
		if useTemp {
			tempPath, err = editorTempSibling(ev.vfs, filePath)
		}

		// A file system that can assemble a file out of pieces of another
		// one saves the unchanged parts from ever crossing the network. It
		// needs a second path to build into, which the temp file already
		// provides, and a buffer whose offsets still describe the file on
		// disk, which is only true for a raw UTF-8 load.
		saved := false
		if err == nil {
			if patcher, ok := ev.vfs.(vfs.InPlacePatcher); ok && ev.Codepage == 65001 && !ev.utf8BOM && !createNewTarget && !fullWrite {
				if pieces, ok := patchPiecesFromTable(ev.pt); ok {
					perr := patcher.PatchInPlace(ctx.Context, filePath, pieces)
					if perr == nil {
						saved = true
						// This one writes through to the destination itself, so
						// no stage was ever created. Leaving useTemp set sent
						// the finalize step on to rename a path that does not
						// exist, which failed the save — with the new content
						// already on disk — for every edit that kept the file's
						// length, and for saving an unmodified buffer.
						useTemp = false
						tempPath = ""
					} else {
						vtui.DebugLog("EDITOR: in-place patch unavailable: %v", perr)
					}
				}
			}
		}

		if !saved && err == nil {
			if delta, isDelta := ev.vfs.(vfs.DeltaWriter); isDelta && useTemp && ev.Codepage == 65001 && !ev.utf8BOM && !fullWrite {
				if pieces, ok := patchPiecesFromTable(ev.pt); ok {
					perr := delta.PatchFile(vfs.WithDestinationOverwrite(ctx.Context, false), filePath, tempPath, pieces)
					if perr == nil {
						saved = true
					} else {
						vtui.DebugLog("EDITOR: delta save unavailable, writing in full: %v", perr)
					}
				}
			}
		}

		if saved {
			f = nopWriteCloser{}
		} else if useTemp && err == nil {
			f, err = ev.vfs.Create(vfs.WithDestinationOverwrite(ctx.Context, false), tempPath)
		} else if err == nil {
			f, err = ev.vfs.Create(vfs.WithDestinationOverwrite(ctx.Context, !createNewTarget), filePath)
		}
		if err == nil && useTemp && !saved {
			// os.Create-style APIs commonly start at 0666/umask (often 0644).
			// Tighten the stage before the first byte of potentially sensitive
			// content is written. Providers without Unix modes safely ignore this.
			stageAttrErr := ev.vfs.SetAttributes(ctx.Context, tempPath, vfs.VFSItem{UnixMode: 0o600, Uid: -1, Gid: -1})
			if stageAttrErr != nil && isLocalOSVFS(ev.vfs) {
				_ = f.Close()
				cleanupEditorStage(ev.vfs, tempPath)
				f = nil
				err = fmt.Errorf("secure editor temporary file: %w", stageAttrErr)
			}
		}

		if err != nil {
			ctx.RunOnUI(func() {
				ev.saving = false
				if useTemp {
					vtui.DebugLog("EDITOR: Failed to create temp file for saving: %v", err)
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to create temporary file:\n%v", err), []string{"&Ok"})
				} else {
					vtui.DebugLog("EDITOR: Failed to open file for direct saving: %v", err)
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to open file for writing:\n%v", err), []string{"&Ok"})
				}
			})
			return
		}

		var saveErr error
		if saved {
			// The file system already wrote it.
		} else if ev.Codepage == 65001 {
			if ev.utf8BOM {
				if _, errWrite := f.Write([]byte{0xEF, 0xBB, 0xBF}); errWrite != nil {
					saveErr = errWrite
				}
			}
			curr := 0
			total := ev.pt.Size()
			for curr < total && saveErr == nil {
				if ctx.Err() != nil {
					saveErr = ctx.Err()
					break
				}
				take := 256 * 1024
				if curr+take > total {
					take = total - curr
				}
				data, errRange := ev.pt.GetRange(curr, take)
				if errRange == piecetable.ErrLoading {
					time.Sleep(20 * time.Millisecond)
					continue
				}
				if errRange != nil {
					saveErr = errRange
					break
				}
				if _, errWrite := f.Write(data); errWrite != nil {
					saveErr = errWrite
					break
				}
				curr += len(data)
			}
		} else {
			bytes, errBytes := ev.pt.Bytes()
			if errBytes != nil {
				saveErr = errBytes
			} else {
				encoded, errEnc := vfs.EncodeBytes(bytes, ev.Codepage)
				if errEnc != nil {
					saveErr = errEnc
				} else {
					if ev.omitUnicodeBOM {
						encoded = stripEncodedBOM(encoded, ev.Codepage)
					}
					_, saveErr = f.Write(encoded)
				}
			}
		}
		// The last chunk of a buffering writer is only sent by Close, so a
		// save is not finished until Close has succeeded. Over a network file
		// system, dropping this error means reporting a save that lost its
		// tail.
		if cerr := f.Close(); cerr != nil && saveErr == nil {
			saveErr = cerr
		}

		if saveErr != nil {
			if useTemp && tempPath != "" {
				cleanupEditorStage(ev.vfs, tempPath)
			}
			ctx.RunOnUI(func() {
				ev.saving = false
				vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to save data:\n%v", saveErr), []string{"&Ok"})
			})
			return
		}

		// Success: finalize the save atomically. Keep the old read handles alive
		// through Rename and the subsequent reopen. Remote mutations do not need
		// them closed, and on an error the current piece table may still depend on
		// that lazy AsyncBuffer. Closing it early made the editor unreadable and
		// unretryable after a failed finalization.
		oldBackingClosed := false
		if useTemp {
			// Windows does not allow replacing a local file while our reader handle
			// is open. A full local save has already read every piece still referenced
			// by the piece table, so its AsyncBuffer remains a usable recovery snapshot
			// after cancellation. Remote VFS handles stay open: their Rename can have
			// an unknown/partial outcome and closing a lazy source there would strand
			// the editor precisely when recovery matters most.
			if isLocalOSVFS(ev.vfs) {
				if oldAsync != nil {
					oldAsync.Close()
				}
				if oldFile != nil {
					_ = oldFile.Close()
				}
				oldBackingClosed = true
			}
			renameCtx := vfs.WithDestinationOverwrite(ctx.Context, !createNewTarget)
			if err := ev.vfs.Rename(renameCtx, tempPath, filePath); err != nil {
				// Do not remove the staged path after an uncertain/partial rename.
				// A remote provider may have committed the move and merely lost the
				// response (or failed while removing its backup). In that state the
				// temp alias can already identify the newly saved authoritative file;
				// deleting it here would turn a save error into data loss. The random
				// sibling name also makes leaving an unconfirmed stage non-destructive.
				// Definitive pre-commit errors are safe to clean once; uncertain,
				// partial and canceled operations must remain untouched.
				if !operationMustNotRetry(err) {
					cleanupEditorStage(ev.vfs, tempPath)
				}
				ctx.RunOnUI(func() {
					ev.saving = false
					vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to finalize save (rename failed):\n%v", err), []string{"&Ok"})
				})
				return
			}
			// Replacement can change a remote object's immutable identity (Google
			// Drive moves the new temp object into place and deletes the old ID).
			// Persist the VFS-remapped canonical path so reopen/history survives a
			// later session instead of retaining the deleted object URI.
			if canonical, absErr := ev.vfs.Abs(filePath); absErr == nil && canonical != "" {
				finalFilePath = canonical
			}
		}

		// Restore original metadata (owner, group, perms, times). Remote VFSes may
		// explicitly not support attributes; local failures are a committed but
		// user-visible partial save and must not be silently ignored.
		var metadataErr error
		if statErr == nil {
			if attrErr := ev.vfs.SetAttributes(ctx.Context, finalFilePath, originalStat); attrErr != nil && isLocalOSVFS(ev.vfs) {
				metadataErr = attrErr
			}
		}

		newFile, err := ev.vfs.Open(ctx.Context, finalFilePath)
		var newPt *piecetable.PieceTable
		var newEngine *textlayout.WrapEngine
		var newBuf *AsyncBuffer
		var newMapped *MappedFile

		if err == nil {
			newUTF8BOM := ev.Codepage == 65001 && ev.utf8BOM
			if ev.Codepage == 65001 {
				// Re-map rather than fall back to lazy chunks: a saved file is
				// as mappable as the one that was opened, and dropping to the
				// chunk buffer here would quietly cost every later search the
				// copy that mapping avoids.
				if ev.mapped != nil && AppConfig.EditorMemoryMap {
					mapOffset := int64(0)
					if newUTF8BOM {
						mapOffset = vfs.UTF8BOMSize
					}
					if m, mapErr := MapEditorFileWithOffset(ev.vfs, newFile, mapOffset); mapErr == nil {
						newMapped = m
						newPt = piecetable.New(m.Bytes())
					}
				}
				if newPt == nil {
					fileOffset := int64(0)
					if newUTF8BOM {
						fileOffset = vfs.UTF8BOMSize
					}
					newBuf = NewAsyncBufferWithOffset(ctx.Context, newFile, fileOffset)
					newPt = piecetable.NewWithBuffer(newBuf)
				}
			} else {
				size := newFile.Size()
				fullData := make([]byte, size)
				_, _ = newFile.ReadAt(ctx.Context, fullData, 0)
				decoded, errDec := vfs.DecodeBytes(fullData, ev.Codepage)
				if errDec != nil {
					decoded = fullData
				}
				newPt = piecetable.New(decoded)
			}
			// Reuse the existing LineIndex since the logical content is identical
			newEngine = textlayout.NewWrapEngine(newPt, ev.li)
			// A confirmed replacement and a confirmed new backing make it safe to
			// release the old lazy reader. If reopen failed, retaining it is what
			// keeps the in-memory edit session usable.
			if oldAsync != nil && !oldBackingClosed {
				oldAsync.Close()
			}
			if oldFile != nil && !oldBackingClosed {
				_ = oldFile.Close()
			}
		}

		// PRELOAD CACHE TO PREVENT SCREEN FLICKER
		// This MUST be outside RunOnUI to prevent blocking the main thread for 500ms.
		if newBuf != nil {
			for i := 0; i < 50; i++ { // max 500ms
				if ctx.Err() != nil {
					break
				}
				_, e := newBuf.Read(visStart, 4096)
				if e != piecetable.ErrLoading {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}

		ctx.RunOnUI(func() {
			ev.saving = false
			ev.filePath = finalFilePath
			if err == nil {
				vtui.DebugLog("EDITOR: Successfully saved %s (%d bytes)", ev.filePath, ev.pt.Size())
			}
			vtui.FrameManager.Broadcast(CmFileChanged, nil)

			if err == nil {
				ev.modified = false
				ev.unsavedBaseline = false
				ev.createNewTarget = false
				if afterSave != nil {
					afterSave()
				}
				ev.file = newFile
				ev.asyncBuf = newBuf
				ev.utf8BOM = ev.Codepage == 65001 && ev.utf8BOM
				// The old mapping described the file as it was before the
				// save. Nothing reads through it once the piece table below
				// points at the new one, and this runs on the UI thread, so
				// the render path cannot be inside it either.
				if ev.mapped != nil && ev.mapped != newMapped {
					_ = ev.mapped.Close()
				}
				ev.mapped = newMapped
				ev.pt = newPt
				ev.codepageRaw = nil
				ev.cleanState = newPt.GetState()
				ev.engine = newEngine
				ev.retireEditSession()
				ev.ensureEngineWidth()
				ev.edited = false
				// Saving cancels the scan, and the buffer it was reading has
				// just been replaced by the reopened file. Without this a file
				// saved while it was still being indexed stays half indexed
				// for the rest of the session: everything past the point the
				// scan had reached is simply not there.
				if !ev.indexIsComplete() {
					ev.StartIndexing()
				}
				if metadataErr != nil {
					vtui.ShowMessage(" Warning ", fmt.Sprintf("File content was saved, but original metadata could not be restored:\n%v", metadataErr), []string{"&Ok"})
				}
			} else {
				// The content mutation already committed. Keep the old backing alive,
				// mark the current logical state clean, and report only the failed
				// reopen; retrying the save mutation would be unsafe and unnecessary.
				ev.modified = false
				ev.unsavedBaseline = false
				ev.createNewTarget = false
				ev.cleanState = ev.pt.GetState()
				ev.codepageRaw = nil
				ev.edited = false
				vtui.ShowMessage(" Warning ", fmt.Sprintf("File content was saved, but the file could not be reopened:\n%v", err), []string{"&Ok"})
			}
		})
	})
}

func (ev *EditorView) getSelectionRange() (int, int) {
	if !ev.selActive {
		return 0, 0
	}
	cursorOffset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	min, max := ev.selAnchorOffset, cursorOffset
	if min > max {
		min, max = max, min
	}
	return min, max
}

// findPanelsFrameAnyScreen locates the PanelsFrame the user is
// currently working in. The active screen is consulted first: with
// several workspaces open (Ctrl+N) every screen has a PanelsFrame of
// its own, and answering with whichever one happens to sit earliest
// in the slice is how hotkeys ended up hiding panels in one workspace
// while the user was looking at another (issue #424).
//
// When the active screen has none — a full-screen editor or viewer is
// added via AddScreen, so it becomes the active screen while the
// panels stay on the one before — the search walks outwards in
// most-recently-used order. SwitchScreen moves the screen it switches
// to to the end of the slice, so walking down from ActiveIdx visits
// the workspaces in the order the user last used them, and the editor
// finds the panels it was opened from rather than the oldest ones.
func findPanelsFrameAnyScreen() *PanelsFrame {
	if vtui.FrameManager == nil {
		return nil
	}
	screens := vtui.FrameManager.Screens
	if len(screens) == 0 {
		return nil
	}

	pick := func(idx int) *PanelsFrame {
		if idx < 0 || idx >= len(screens) || screens[idx] == nil {
			return nil
		}
		frames := screens[idx].Frames
		for i := len(frames) - 1; i >= 0; i-- {
			if pf, ok := frames[i].(*PanelsFrame); ok && !pf.closed {
				return pf
			}
		}
		return nil
	}

	active := vtui.FrameManager.ActiveIdx
	if pf := pick(active); pf != nil {
		return pf
	}
	for i := active - 1; i >= 0; i-- {
		if pf := pick(i); pf != nil {
			return pf
		}
	}
	for i := active + 1; i < len(screens); i++ {
		if pf := pick(i); pf != nil {
			return pf
		}
	}
	return nil
}

// leftPanelPathForEditor / rightPanelPathForEditor return the
// on-screen visually-left / visually-right file panel's path, or
// "" if the panels frame isn't available. Deliberately uses the
// same visualLeftFSP / visualRightFSP resolvers PanelsFrame uses
// for its own Ctrl+[/Ctrl+] command-line binds, so after Ctrl+U
// the editor stays in lock-step with the panel behaviour instead
// of routing by stale slot index.
func leftPanelPathForEditor() string {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return ""
	}
	if fsp := pf.visualLeftFSP(); fsp != nil {
		return fsp.vfs.GetPath()
	}
	return ""
}

func rightPanelPathForEditor() string {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return ""
	}
	if fsp := pf.visualRightFSP(); fsp != nil {
		return fsp.vfs.GetPath()
	}
	return ""
}

// activePanelNameForEditor returns the currently-selected file name
// on the active file panel, or "" if there isn't one. Not
// distinguishing "no selection" from "no panel" — both yield ""
// which the caller treats as a no-op.
func activePanelNameForEditor() string {
	pf := findPanelsFrameAnyScreen()
	if pf == nil {
		return ""
	}
	fsp := pf.getActivePanel()
	if fsp == nil {
		return ""
	}
	return fsp.GetSelectedName()
}

// insertTextAtCursor writes bytes at the cursor position, taking
// care of an active selection (deleted first), any accumulated
// virtual-space column past EOL (materialised as real spaces before
// the insert), and the bookkeeping the rest of the editor expects
// after an edit — undo checkpoint, line-index update, cache
// invalidation, cursor advance, autocomplete refresh.
func (ev *EditorView) insertTextAtCursor(data []byte) {
	if len(data) == 0 {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	if ev.selActive || ev.rectSelActive {
		ev.inGroup = true
		ev.DeleteSelection()
		ev.inGroup = false
	}
	ev.modified = true
	offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	if ev.CursorVirtualSpaces > 0 {
		virtSpaces := []byte(strings.Repeat(" ", ev.CursorVirtualSpaces))
		ev.pt.Insert(offset, virtSpaces)
		ev.li.UpdateAfterInsert(offset, virtSpaces)
		offset += ev.CursorVirtualSpaces
		ev.CursorPos += ev.CursorVirtualSpaces
		ev.CursorVirtualSpaces = 0
	}
	ev.pt.Insert(offset, data)
	ev.li.UpdateAfterInsert(offset, data)
	ev.invalidateStates(ev.CursorLine)
	ev.engine.InvalidateFrom(ev.CursorLine)
	ev.CursorPos += len(data)
	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
	if ev.acEnabled {
		ev.updateAutocomplete()
	}
}

// deleteSpacersForward removes every run of spaces and tabs starting
// at the cursor, stopping at the first non-spacer byte (or EOF). No-
// op when the cursor is already on a non-spacer. Matches FAR's
// Ctrl+Del behaviour word-for-word — "spacer" is the same tokeniser
// term the issue uses.
func (ev *EditorView) deleteSpacersForward() {
	offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	total := ev.pt.Size()
	if offset >= total {
		return
	}
	count := 0
	for offset+count < total {
		b, _ := ev.pt.GetRange(offset+count, 1)
		if len(b) == 0 || (b[0] != ' ' && b[0] != '\t') {
			break
		}
		count++
	}
	if count == 0 {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.modified = true
	ev.pt.Delete(offset, count)
	ev.li.UpdateAfterDelete(offset, count)
	ev.invalidateStates(ev.CursorLine)
	ev.engine.InvalidateFrom(ev.CursorLine)
	ev.ensureCursorVisible()
}

func (ev *EditorView) CopySelection() {
	if ev.rectSelActive {
		GlobalLastClipboardWasRectangular = true
		minY, maxY := ev.rectSelStartLine, ev.CursorLine
		if minY > maxY {
			minY, maxY = maxY, minY
		}
		minX, maxX := ev.rectSelStartCol, ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
		if minX > maxX {
			minX, maxX = maxX, minX
		}

		var lines []string
		for y := minY; y <= maxY; y++ {
			lineStart := ev.li.GetLineOffset(y)
			lineLen := ev.getLineLength(y)
			lineData, _ := ev.pt.GetRange(lineStart, lineLen)

			var piece []rune
			visualCol := 0
			clusters := editorGraphemes(lineData)
			tabSize := ev.TabSize
			if tabSize <= 0 {
				tabSize = 8
			}

			for _, cluster := range clusters {
				rw := cluster.width
				if cluster.text == "\t" {
					rw = tabSize - (visualCol % tabSize)
				}

				if visualCol >= minX && visualCol < maxX {
					piece = append(piece, []rune(cluster.text)...)
				} else if visualCol < minX && visualCol+rw > minX {
					piece = append(piece, ' ')
				}

				visualCol += rw
				if visualCol >= maxX {
					break
				}
			}

			if visualCol < maxX {
				needed := maxX - visualCol
				if visualCol < minX {
					needed = maxX - minX
				}
				piece = append(piece, []rune(strings.Repeat(" ", needed))...)
			}

			lines = append(lines, string(piece))
		}

		text := strings.Join(lines, "\n")
		vtui.SetClipboard(text)
		return
	}

	min, max := ev.getSelectionRange()
	if max > min {
		GlobalLastClipboardWasRectangular = false
		data, _ := ev.pt.GetRange(min, max-min)
		if data != nil {
			text := string(data)
			vtui.SetClipboard(text)
			vtui.DebugLog("EDITOR: Copied %d bytes to clipboard", max-min)
		}
	}
}

func (ev *EditorView) PasteRectangular(text string, targetCol int) {
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return
	}
	neededLines := ev.CursorLine + len(lines)
	mutates := neededLines > ev.li.LineCount()
	if !mutates {
		for _, line := range lines {
			if line != "" {
				mutates = true
				break
			}
		}
	}
	if !mutates {
		return
	}

	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.modified = true

	for ev.li.LineCount() < neededLines {
		offset := ev.pt.Size()
		ev.pt.Insert(offset, []byte("\n"))
		ev.li.UpdateAfterInsert(offset, []byte("\n"))
	}
	ev.engine.InvalidateFrom(ev.CursorLine)

	for i := len(lines) - 1; i >= 0; i-- {
		y := ev.CursorLine + i
		lineStart := ev.li.GetLineOffset(y)
		lineLen := ev.getLineLength(y)
		lineData, _ := ev.pt.GetRange(lineStart, lineLen)

		visualCol := 0
		clusters := editorGraphemes(lineData)
		tabSize := ev.TabSize
		if tabSize <= 0 {
			tabSize = 8
		}

		insertByteOff := -1
		byteAcc := 0

		for _, cluster := range clusters {
			rw := cluster.width
			if cluster.text == "\t" {
				rw = tabSize - (visualCol % tabSize)
			}

			if visualCol >= targetCol && insertByteOff == -1 {
				insertByteOff = byteAcc
				break
			}

			visualCol += rw
			byteAcc = cluster.end
		}

		if insertByteOff == -1 {
			padding := strings.Repeat(" ", targetCol-visualCol)
			insertOff := lineStart + byteAcc
			ev.pt.Insert(insertOff, []byte(padding+lines[i]))
			ev.li.UpdateAfterInsert(insertOff, []byte(padding+lines[i]))
		} else {
			insertOff := lineStart + insertByteOff
			ev.pt.Insert(insertOff, []byte(lines[i]))
			ev.li.UpdateAfterInsert(insertOff, []byte(lines[i]))
		}
	}

	ev.invalidateStates(ev.CursorLine)
	ev.engine.InvalidateFrom(ev.CursorLine)
	ev.ensureCursorVisible()
}

func (ev *EditorView) PasteText(text string) {
	if GlobalLastClipboardWasRectangular {
		targetCol := ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
		ev.PasteRectangular(text, targetCol)
		return
	}
	if text == "" && !ev.selActive {
		return
	}

	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.inGroup = true
	if ev.selActive {
		ev.DeleteSelection()
	}
	ev.inGroup = false

	offset := ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
	data := []byte(text)
	ev.pt.Insert(offset, data)
	ev.li.UpdateAfterInsert(offset, data)
	ev.invalidateStates(ev.CursorLine)
	ev.engine.InvalidateFrom(ev.CursorLine)

	newOffset := offset + len(data)
	ev.CursorLine = ev.li.GetLineAtOffset(newOffset)
	ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)
	ev.modified = true
	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
}

func (ev *EditorView) DeleteSelection() {
	if ev.rectSelActive {
		minY, maxY := ev.rectSelStartLine, ev.CursorLine
		if minY > maxY {
			minY, maxY = maxY, minY
		}
		minX, maxX := ev.rectSelStartCol, ev.getVisualColOf(ev.CursorLine, ev.CursorPos)
		if minX > maxX {
			minX, maxX = maxX, minX
		}

		mutated := false

		for y := maxY; y >= minY; y-- {
			lineStart := ev.li.GetLineOffset(y)
			lineLen := ev.getLineLength(y)
			lineData, _ := ev.pt.GetRange(lineStart, lineLen)

			visualCol := 0
			tabSize := ev.TabSize
			if tabSize <= 0 {
				tabSize = 8
			}

			startByte := -1
			endByte := -1
			byteAcc := 0

			for _, cluster := range editorGraphemes(lineData) {
				rw := cluster.width
				if cluster.text == "\t" {
					rw = tabSize - (visualCol % tabSize)
				}

				if visualCol >= minX && startByte == -1 {
					startByte = byteAcc
				}
				if visualCol >= maxX && endByte == -1 {
					endByte = byteAcc
				}

				visualCol += rw
				byteAcc = cluster.end
			}

			if startByte != -1 {
				if endByte == -1 {
					endByte = byteAcc
				}
				if endByte > startByte {
					if !mutated {
						if !ev.inGroup {
							ev.noteBufferEdit()
						}
						ev.saveUndo(opOther)
						ev.modified = true
						mutated = true
					}
					delOff := lineStart + startByte
					delLen := endByte - startByte
					ev.pt.Delete(delOff, delLen)
					ev.li.UpdateAfterDelete(delOff, delLen)
				}
			}
		}

		if mutated {
			ev.invalidateStates(minY)
			ev.engine.InvalidateFrom(minY)
		}
		ev.rectSelActive = false
		ev.ensureCursorVisible()
		return
	}

	min, max := ev.getSelectionRange()
	if min < 0 {
		min = 0
	}
	if max > ev.pt.Size() {
		max = ev.pt.Size()
	}
	if max > min {
		if !ev.inGroup {
			ev.noteBufferEdit()
		}

		ev.saveUndo(opOther)

		ev.modified = true
		ev.pt.Delete(min, max-min)
		ev.li.UpdateAfterDelete(min, max-min)
		ev.clearCaches()
		ev.selActive = false
		ev.CursorLine = ev.li.GetLineAtOffset(min)
		ev.CursorPos = min - ev.li.GetLineOffset(ev.CursorLine)
	}
}

func (ev *EditorView) DeleteCurrentLine() {
	if ev.pt.Size() == 0 {
		return
	}
	ev.noteBufferEdit()
	ev.saveUndo(opOther)
	ev.modified = true

	lineStart := ev.li.GetLineOffset(ev.CursorLine)
	lineEnd := ev.pt.Size()

	lastLineDeleted := false

	if ev.CursorLine+1 < ev.li.LineCount() {
		lineEnd = ev.li.GetLineOffset(ev.CursorLine + 1)
		ev.pt.Delete(lineStart, lineEnd-lineStart)
		ev.li.UpdateAfterDelete(lineStart, lineEnd-lineStart)
	} else {
		lastLineDeleted = true
		if ev.CursorLine > 0 {
			prevLineLen := ev.getLineLength(ev.CursorLine - 1)
			prevLineStart := ev.li.GetLineOffset(ev.CursorLine - 1)
			deleteStart := prevLineStart + prevLineLen

			ev.pt.Delete(deleteStart, lineEnd-deleteStart)
			ev.li.UpdateAfterDelete(deleteStart, lineEnd-deleteStart)

			ev.CursorLine--
			ev.CursorPos = prevLineLen
		} else {
			ev.pt.Delete(0, lineEnd)
			ev.li.UpdateAfterDelete(0, lineEnd)
			ev.CursorPos = 0
		}
	}

	ev.invalidateStates(ev.CursorLine)
	ev.engine.InvalidateFrom(ev.CursorLine)

	if !lastLineDeleted {
		vRow := ev.engine.GetRowOffset(ev.CursorLine)
		newOffset := ev.engine.VisualToLogical(vRow, ev.DesiredVisualCol)
		ev.CursorPos = newOffset - ev.li.GetLineOffset(ev.CursorLine)

		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos == lineLen && ev.CursorBeyondEOL {
			_, endVCol := ev.engine.LogicalToVisual(ev.li.GetLineOffset(ev.CursorLine) + lineLen)
			if ev.DesiredVisualCol > endVCol {
				ev.CursorVirtualSpaces = ev.DesiredVisualCol - endVCol
			} else {
				ev.CursorVirtualSpaces = 0
			}
		} else {
			ev.CursorVirtualSpaces = 0
		}
	} else {
		lineLen := ev.getLineLength(ev.CursorLine)
		if ev.CursorPos > lineLen {
			ev.CursorPos = lineLen
		}
		ev.CursorVirtualSpaces = 0
		ev.updateDesiredVisualCol()
	}

	ev.ensureCursorVisible()
}

func (ev *EditorView) GetType() vtui.FrameType { return vtui.TypeUser + 2 }
func (ev *EditorView) IsBusy() bool            { return ev.pasting || ev.saving }
func (ev *EditorView) GetTitle() string {
	if ev.DisplayTitle != "" {
		return ev.DisplayTitle
	}
	if ev.filePath != "" {
		return "Edit: " + filepath.Base(ev.filePath)
	}
	return "Editor"
}

// GetWorkspaceTabTitle provides a compact title for the workspace
// tab bar while leaving GetTitle available for contexts that need the fuller
// textual description.
func (ev *EditorView) GetWorkspaceTabTitle() string {
	if ev.DisplayTitle != "" {
		return ev.DisplayTitle
	}
	if ev.filePath != "" {
		return filepath.Base(ev.filePath)
	}
	return "Editor"
}

func (ev *EditorView) GetWorkspaceTabMarker() string { return "E" }

// buildSearchRegex compiles an editor search pattern with the rules shared
// by Find, Find All and Replace: non-regex input is quoted literally, case
// insensitivity becomes an (?i) prefix and whole-word wraps the pattern in
// word boundaries.
func buildSearchRegex(pattern string, caseSensitive, useRegex, wholeWord bool) (*coregex.Regex, error) {
	finalPattern := pattern
	if !useRegex {
		finalPattern = coregex.QuoteMeta(pattern)
	}
	if !caseSensitive {
		finalPattern = "(?i)" + finalPattern
	}
	if wholeWord {
		finalPattern = `\b(?:` + finalPattern + `)\b`
	}
	return coregex.Compile(finalPattern)
}

// showSearchProgressDialog shows the cancelable " Searching... " popup used
// by Find and Find All while the buffer scan runs in the background.
func showSearchProgressDialog(pattern string) (dlg *vtui.Window, btnCancel *vtui.Button) {
	dlg = vtui.NewCenteredDialog(50, 8, Msg("Search.Searching"))
	lbl := vtui.NewLabel(0, 0, fmt.Sprintf("Looking for: %s", pattern), nil)
	dlg.AddItem(lbl)
	btnCancel = vtui.NewButton(0, 0, Msg("vtui.Cancel"))
	dlg.AddItem(btnCancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, 50-4, 8-4)
	vbox.Add(lbl, vtui.Margins{}, vtui.AlignCenter)
	vbox.Add(btnCancel, vtui.Margins{Top: 1}, vtui.AlignCenter)
	vbox.Apply()

	vtui.FrameManager.AddScreenHeadless(dlg)
	return dlg, btnCancel
}

// runSearchWithProgress shows the progress popup and runs worker in the
// background. The cancel wiring lives here, on the UI thread: the Cancel
// button and any other close of the dialog (Esc, F10, a border click) all
// cancel the task, so a dismissed search can never resurface its result.
// Wiring after RunAsync is race-free because queued UI tasks only run once
// the current one returns. Must be called on the UI thread; workers close
// dlg from their RunOnUI callback and must check ctx.Err() before acting.
func runSearchWithProgress(pattern string, worker func(ctx *vtui.TaskContext, dlg *vtui.Window)) (*vtui.Window, *vtui.TaskContext) {
	dlg, btnCancel := showSearchProgressDialog(pattern)
	ctx := vtui.RunAsync(func(c *vtui.TaskContext) { worker(c, dlg) })
	btnCancel.OnClick = func() { ctx.Cancel(); dlg.Close() }
	dlg.OnResult = func(int) { ctx.Cancel() }
	return dlg, ctx
}

// searchSnapshotChunk bounds how much readSearchSnapshot asks for per read.
// A lazily loaded buffer starts a fetch for every missing chunk it is asked
// about, so requesting the whole file at once would spawn one goroutine per
// chunk of it; stepping forward in bounded reads keeps that fan-out small.
const searchSnapshotChunk = 256 * 1024

// searchSnapshotPoll is how long to wait before retrying a read whose
// backing chunk has not arrived yet.
const searchSnapshotPoll = 10 * time.Millisecond

// searchSnapshotStall gives up once no data at all has arrived for this
// long, so a buffer whose fetches keep failing ends in the caller's error
// dialog instead of spinning behind the progress popup forever.
const searchSnapshotStall = 30 * time.Second

// searchSnapshotReadAhead is how many later steps are poked when a read finds
// its data missing. Reading strictly in order would spend one poll interval
// per chunk of the file, since a step is exactly one chunk wide and its fetch
// only starts when that step is reached; poking ahead keeps several fetches
// in flight without reverting to a fan-out over the whole file.
const searchSnapshotReadAhead = 8

// readSearchSnapshot assembles the whole buffer for one search pass, waiting
// for lazily loaded data instead of giving up the moment it is missing.
//
// pt.Bytes() reads every piece in one go and fails as soon as one of them is
// still unfetched, which on a freshly opened large file is the normal state:
// showEditor only prewarms the first chunk, so any search started before the
// background indexer had walked the file died with "Failed to read file
// buffer". Reading forward in bounded steps and retrying on ErrLoading lets
// the reads themselves pull the file in, the way StartIndexing does.
//
// This blocks, and the chunks it waits for are stored by tasks posted to the
// UI thread, so it must only be called from a background goroutine: on the UI
// thread it would be waiting for work that only the UI thread can do.
func readSearchSnapshot(ctx *vtui.TaskContext, pt *piecetable.PieceTable) ([]byte, error) {
	size := pt.Size()
	if size == 0 {
		return nil, nil
	}

	// An unedited file backed by memory is one contiguous piece, so the scan
	// can run on the buffer itself. Assembling a copy of it would cost the
	// file size in allocation and two passes over it before the search even
	// starts. The window is read-only, which every caller here honours.
	if data, ok := pt.View(0, size); ok {
		return data, nil
	}

	res := make([]byte, 0, size)
	lastProgress := time.Now()

	for len(res) < size {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		take := min(size-len(res), searchSnapshotChunk)
		// GetRange reports a partly loaded range as one failure and keeps no
		// partial result, so a retry re-reads this step from its start.
		chunk, err := pt.GetRange(len(res), take)
		if err == piecetable.ErrLoading {
			if time.Since(lastProgress) > searchSnapshotStall {
				return nil, err
			}
			// Asking for the later steps is what starts their fetches, so the
			// wait below overlaps with them instead of following them.
			for i := 1; i <= searchSnapshotReadAhead; i++ {
				ahead := len(res) + i*searchSnapshotChunk
				if ahead >= size {
					break
				}
				_, _ = pt.GetRange(ahead, min(size-ahead, searchSnapshotChunk))
			}
			time.Sleep(searchSnapshotPoll)
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			// No error and nothing read: the backing buffer is shorter than
			// the table claims. Search what there is instead of spinning.
			break
		}

		res = append(res, chunk...)
		lastProgress = time.Now()
	}

	return res, nil
}

// searchBuffer returns the buffer one search pass scans, reusing the previous
// pass's when the text has not changed since. It must be called from a
// background goroutine, like readSearchSnapshot itself; session is the
// editSession read on the UI thread before the scan was started, and is what
// decides whether a cached buffer still describes the current text.
//
// A buffer that can be scanned in place costs nothing to reassemble, so it is
// never cached: pt.View hands back the same window every time.
func (ev *EditorView) searchBuffer(ctx *vtui.TaskContext, session int) ([]byte, error) {
	size := ev.pt.Size()
	if size == 0 {
		return nil, nil
	}
	if data, ok := ev.pt.View(0, size); ok {
		return data, nil
	}

	ev.searchSnapMu.Lock()
	if ev.searchSnapshot != nil && ev.searchSnapSession == session && len(ev.searchSnapshot) == size {
		data := ev.searchSnapshot
		ev.searchSnapMu.Unlock()
		return data, nil
	}
	ev.searchSnapMu.Unlock()

	data, err := readSearchSnapshot(ctx, ev.pt)
	if err != nil {
		return nil, err
	}

	ev.searchSnapMu.Lock()
	ev.searchSnapshot = data
	ev.searchSnapSession = session
	ev.searchSnapMu.Unlock()
	return data, nil
}

// retireEditSession invalidates everything keyed to the text as it was: the
// fence in-flight background tasks compare themselves against moves, and the
// cached search buffer is released rather than left to outlive the edit that
// retired it. Call it from the UI thread wherever the buffer changes.
func (ev *EditorView) retireEditSession() {
	ev.editSession++
	ev.dropSearchSnapshot()
}

// dropSearchSnapshot releases the cached search buffer. Keying it by
// editSession is what makes a stale one unusable; dropping it is what stops a
// file-sized allocation from outliving the edit that retired it.
func (ev *EditorView) dropSearchSnapshot() {
	ev.searchSnapMu.Lock()
	ev.searchSnapshot = nil
	ev.searchSnapMu.Unlock()
}

// bytesToString views bytes as a string without copying them. Every caller
// here only reads, and the bytes are either a private snapshot or a window
// into a buffer nothing writes through.
func bytesToString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// #nosec G103 -- the returned string is read-only and every caller retains b for the full lifetime of the synchronous search.
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// findMatch locates one occurrence of pattern in data: forward from
// startOff, or backward from just before it when reverse is set; next
// additionally skips a match starting exactly at startOff. The offset is
// -1 when nothing matches. The returned length is the matched byte length,
// which for regex or case-folded matches can differ from len(pattern).
func findMatch(data []byte, pattern string, caseSensitive, reverse, regexp, wholeWord, next bool, startOff int) (int, int, error) {
	foundOffset := -1
	matchLen := len(pattern)
	totalSize := len(data)

	// Only whole-word matching needs the regex engine (for the \b
	// wrapping); literal search, case-sensitive or folded, is handled
	// below without it.
	if regexp || wholeWord {
		re, err := buildSearchRegex(pattern, caseSensitive, regexp, wholeWord)
		if err != nil {
			return -1, 0, err
		}

		if !reverse {
			currOff := startOff
			if next {
				currOff++
			}
			if currOff < totalSize {
				loc := re.FindIndex(data[currOff:])
				if loc != nil {
					foundOffset = currOff + loc[0]
					matchLen = loc[1] - loc[0]
				}
			}
		} else {
			currOff := startOff
			if next {
				currOff--
			}
			if currOff > totalSize {
				currOff = totalSize
			}
			if currOff > 0 {
				// FindAllIndex scans left-to-right without overlap, so for
				// self-overlapping patterns (e.g. "яя" in "яяя") this can
				// land left of the true rightmost hit. Accepted: unlike the
				// old ToLower+LastIndex path it cannot corrupt byte offsets.
				locs := re.FindAllIndex(data[:currOff], -1)
				if len(locs) > 0 {
					last := locs[len(locs)-1]
					foundOffset = last[0]
					matchLen = last[1] - last[0]
				}
			}
		}
		return foundOffset, matchLen, nil
	}

	text := bytesToString(data)
	index, lastIndex := strings.Index, strings.LastIndex
	if !caseSensitive {
		// strcase folds while scanning the original text, so the offsets
		// it returns need no translation.
		index, lastIndex = strcase.Index, strcase.LastIndex
	}
	if !reverse {
		currOff := startOff
		if next {
			currOff++
		}
		if currOff < len(text) {
			idx := index(text[currOff:], pattern)
			if idx != -1 {
				foundOffset = currOff + idx
			}
		}
	} else {
		currOff := startOff
		if next {
			currOff--
		}
		if currOff > len(text) {
			currOff = len(text)
		}
		if currOff > 0 {
			idx := lastIndex(text[:currOff], pattern)
			if idx != -1 {
				foundOffset = idx
			}
		}
	}
	// A folded match can differ in byte length from the pattern
	// (K U+212A matches "k"), so measure what it consumed.
	if foundOffset != -1 && !caseSensitive {
		if after, ok := strcase.CutPrefix(text[foundOffset:], pattern); ok {
			matchLen = len(text) - foundOffset - len(after)
		}
	}
	return foundOffset, matchLen, nil
}

// searchSeedOffset returns the offset a buffer scan starts from. An active
// selection — typically the previously found or confirmed match — takes
// precedence over the raw cursor: a replace scan (includeSelection) starts
// at the selection's near edge so the highlighted occurrence is prompted
// again, while a plain search starts at its end and continues past it,
// exactly as if the cursor sat there.
func (ev *EditorView) searchSeedOffset(reverse, includeSelection bool) int {
	if ev.selActive {
		selMin, selMax := ev.getSelectionRange()
		if includeSelection && !reverse {
			return selMin
		}
		return selMax
	}
	return ev.li.GetLineOffset(ev.CursorLine) + ev.CursorPos
}

func parseHexPatternToRegex(pattern string) (string, error) {
	var re strings.Builder
	re.WriteString("(?s)") // dot matches newline
	tokens := strings.Fields(pattern)
	for _, t := range tokens {
		if t == "?" || t == "??" {
			re.WriteString(".")
		} else {
			if len(t) == 1 {
				t = "0" + t
			}
			b, err := hex.DecodeString(t)
			if err != nil {
				return "", fmt.Errorf("invalid hex token: %s", t)
			}
			fmt.Fprintf(&re, "\\x%02x", b[0])
		}
	}
	return re.String(), nil
}

func parseHexReplacement(repl string) ([]byte, error) {
	var res []byte
	tokens := strings.Fields(repl)
	for _, t := range tokens {
		if len(t) == 1 {
			t = "0" + t
		}
		b, err := hex.DecodeString(t)
		if err != nil {
			return nil, fmt.Errorf("invalid hex token: %s", t)
		}
		res = append(res, b[0])
	}
	return res, nil
}
func (ev *EditorView) Search(pattern string, caseSensitive, reverse, regexp, wholeWord, next bool) {
	if pattern == "" {
		return
	}
	if LastEditorSearch != pattern || LastEditorSearchCase != caseSensitive || LastEditorSearchReverse != reverse || LastEditorSearchRegexp != regexp || LastEditorSearchWholeWord != wholeWord {
		LastEditorSearch = pattern
		LastEditorSearchCase = caseSensitive
		LastEditorSearchReverse = reverse
		LastEditorSearchRegexp = regexp
		LastEditorSearchWholeWord = wholeWord
		SaveSession()
	}

	searchPattern := pattern
	if LastEditorSearchHex {
		var err error
		searchPattern, err = parseHexPatternToRegex(pattern)
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid hex pattern:\n%v", err), []string{"&Ok"})
			return
		}
		regexp = true
		wholeWord = false
		caseSensitive = true
	}

	vtui.DebugLog("EDITOR_SEARCH: Starting for %q (sensitive=%v, reverse=%v, regexp=%v, wholeWord=%v, next=%v)",
		pattern, caseSensitive, reverse, regexp, wholeWord, next)

	vtui.FrameManager.PostTask(func() {
		// Read on the UI thread, before the background scan starts.
		startOff := ev.searchSeedOffset(reverse, false)
		session := ev.editSession
		delegate, canDelegate := ev.searchDelegation(regexp, wholeWord)

		runSearchWithProgress(searchPattern, func(ctx *vtui.TaskContext, dlg *vtui.Window) {
			defer ev.guardMapping("searching")()

			// A file system that can search its own copy answers without the
			// file having to travel. Anything it cannot answer for certain
			// falls through to the scan below.
			if canDelegate {
				off, mLen, ok := ev.searchViaFileSystem(ctx.Context, delegate, searchPattern,
					caseSensitive, reverse, next, startOff)
				logSearchDelegation(ok, searchPattern)
				if ok {
					indexed := off == -1 || ev.awaitIndexForResults(ctx, off+mLen)
					ctx.RunOnUI(func() {
						canceled := ctx.Err() != nil
						dlg.Close()
						if canceled || !indexed || ev.editSession != session {
							return
						}
						ev.selectFoundPattern(off, mLen)
					})
					return
				}
			}

			bytes, errBytes := ev.searchBuffer(ctx, session)
			if errBytes != nil {
				if ctx.Err() != nil {
					return // canceled; the dialog is already closing
				}
				ctx.RunOnUI(func() {
					dlg.Close()
					vtui.ShowMessage(" Error ", "Failed to read file buffer.", []string{"&Ok"})
				})
				return
			}

			foundOffset, matchLen, err := findMatch(bytes, pattern, caseSensitive, reverse, regexp, wholeWord, next, startOff)
			if err != nil {
				ctx.RunOnUI(func() {
					dlg.Close()
					vtui.ShowMessage(" Error ", fmt.Sprintf("Invalid regular expression:\n%v", err), []string{"&Ok"})
				})
				return
			}

			// The match is about to become a cursor position, which is a line
			// and a column the index has to answer for.
			indexed := true
			if foundOffset != -1 {
				indexed = ev.awaitIndexForResults(ctx, foundOffset+matchLen)
			}

			ctx.RunOnUI(func() {
				// Closing the dialog cancels the task via OnResult, so the
				// cancellation state must be read first: a search the user
				// dismissed neither jumps nor complains.
				canceled := ctx.Err() != nil
				dlg.Close()
				if canceled || !indexed {
					return
				}
				if foundOffset != -1 {
					ev.selectFoundPattern(foundOffset, matchLen)
				} else {
					vtui.ShowMessage(Msg("Search.Title"), Msg("Search.NotFound"), []string{Msg("vtui.Ok")})
				}
			})
		})
	})
}

// selectFoundPattern selects a match found by Search or FindAll: the match
// becomes the active selection and the cursor lands at its end.
func (ev *EditorView) selectFoundPattern(off, length int) {
	ev.selActive = true
	ev.selAnchorOffset = off

	end := off + length
	// A search reads the whole buffer, so it can find a match in text the
	// index has not reached yet. Asking an index that stops short where such
	// an offset lives answers with its last line and a column counted from
	// there, which is not a position in the file at all.
	ev.ensureIndexedTo(end)
	ev.CursorLine = ev.li.GetLineAtOffset(end)
	ev.CursorPos = end - ev.li.GetLineOffset(ev.CursorLine)

	ev.updateDesiredVisualCol()
	ev.ensureCursorVisible()
	vtui.FrameManager.Redraw()
}

// updateAutocomplete scans nearby lines for words matching the current prefix.
func (ev *EditorView) updateAutocomplete() {
	ev.acMatches = nil
	if ev.CursorPos == 0 {
		return
	}

	lineLen := ev.getLineLength(ev.CursorLine)
	lineStart := ev.li.GetLineOffset(ev.CursorLine)

	// Disable if we are in the middle of a word (peek at the character under cursor)
	if ev.CursorPos < lineLen {
		dataUnder, _ := ev.pt.GetRange(lineStart+ev.CursorPos, 4)
		if len(dataUnder) > 0 {
			r, _ := utf8.DecodeRune(dataUnder)
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
				return
			}
		}
	}

	lineData, _ := ev.pt.GetRange(lineStart, ev.CursorPos)
	if len(lineData) == 0 {
		return
	}

	runes := []rune(string(lineData))

	// Find prefix by going backwards until non-word character
	prefixStart := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		r := runes[i]
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			break
		}
		prefixStart = i
	}

	if prefixStart == len(runes) {
		return // No word char before cursor
	}

	ev.acPrefix = string(runes[prefixStart:])

	// Minimum prefix length to trigger suggestions (like in far2l)
	if len([]rune(ev.acPrefix)) < 2 {
		return
	}

	// Scan lines around cursor
	maxDelta := 256
	startL := ev.CursorLine - maxDelta
	if startL < 0 {
		startL = 0
	}
	endL := ev.CursorLine + maxDelta
	if endL >= ev.li.LineCount() {
		endL = ev.li.LineCount() - 1
	}

	seen := make(map[string]bool)
	var currentLineMatches []string
	var otherLineMatches []string

	// Fast word extractor
	extractWords := func(lineIdx int) {
		lStart := ev.li.GetLineOffset(lineIdx)
		lLen := ev.getLineLength(lineIdx)
		// Limit to 512 bytes per line to avoid lag on minified files
		if lLen > 512 {
			lLen = 512
		}

		data, _ := ev.pt.GetRange(lStart, lLen)
		if len(data) == 0 {
			return
		}

		lRunes := []rune(string(data))
		wordStart := -1

		for i, r := range lRunes {
			isWord := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
			if isWord {
				if wordStart == -1 {
					wordStart = i
				}
			} else {
				if wordStart != -1 {
					word := string(lRunes[wordStart:i])
					if strings.HasPrefix(word, ev.acPrefix) && word != ev.acPrefix && !seen[word] {
						seen[word] = true
						if lineIdx == ev.CursorLine {
							currentLineMatches = append(currentLineMatches, word)
						} else {
							otherLineMatches = append(otherLineMatches, word)
						}
					}
					wordStart = -1
				}
			}
		}
		// Check tail
		if wordStart != -1 {
			word := string(lRunes[wordStart:])
			if strings.HasPrefix(word, ev.acPrefix) && word != ev.acPrefix && !seen[word] {
				seen[word] = true
				if lineIdx == ev.CursorLine {
					currentLineMatches = append(currentLineMatches, word)
				} else {
					otherLineMatches = append(otherLineMatches, word)
				}
			}
		}
	}

	// Prioritize current line, then others
	extractWords(ev.CursorLine)
	for i := startL; i <= endL; i++ {
		if i != ev.CursorLine {
			extractWords(i)
		}
	}

	if len(currentLineMatches) > 0 || len(otherLineMatches) > 0 {
		ev.acMatches = append(currentLineMatches, otherLineMatches...)
		ev.acCurrentIdx = 0
	}
}

func isAlternateDataStream(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// URI schemes contain a colon but can never denote an NTFS stream.
	if strings.Contains(path, "://") {
		return false
	}
	vol := filepath.VolumeName(path)
	rest := path
	if vol != "" {
		rest = strings.TrimPrefix(path, vol)
	}
	return strings.Contains(rest, ":")
}

func (ev *EditorView) getVisualColOf(line, pos int) int {
	offset := ev.li.GetLineOffset(line) + pos
	_, vCol := ev.engine.LogicalToVisual(offset)
	return vCol
}
