package terminal

import (
	"encoding/base64"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/f4/internal/toast"
	"strings"

	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/terminal/far2ldnd"
	"github.com/unxed/f4/internal/textlayout"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// StyleChange фиксирует момент смены атрибутов в байтовом потоке лога.
type StyleChange struct {
	Offset int
	Attr   uint64
}

// TerminalView объединяет классическую сетку CharInfo и бесконечный лог.
type TerminalView struct {
	vtui.ScreenObject
	mu sync.Mutex

	// --- Состояние для ANSI Парсера (Grid) ---
	Lines        [][]vtui.CharInfo
	AltLines     [][]vtui.CharInfo
	WrapFlags    []bool // Tracks soft wrap state for each visual row
	UseAltScreen bool

	ScrollTop    int
	ScrollBottom int

	Width   int
	Height  int
	CursorX int
	CursorY int
	// CursorVisible is the terminal application's DECTCEM state. The host
	// screen cursor is reset by the frame manager before every paint, so the
	// terminal view must Apply this state whenever it is focused.
	CursorVisible bool

	// Состояние терминала (сохранение координат)
	savedX, savedY       int
	decSavedX, decSavedY int
	Palette              [256]uint32

	// --- Бесконечный лог (History & Reflow) ---
	Pt              *piecetable.PieceTable
	Li              *piecetable.LineIndex
	Engine          *textlayout.WrapEngine
	GridHistory     [][]vtui.CharInfo
	GridHistoryWrap []bool

	styles   []StyleChange
	lastAttr uint64

	// Скроллинг истории (визуальный ряд)
	ScrollTopRow int

	Title                 string
	Win32InputMode        bool
	BracketedPasteMode    bool
	ApplicationCursorKeys bool
	// KittyFlags is written by the ansi-parsing goroutine (handleCSI in
	// ansi.go, on the local shell's PTY read loop -- see InitPTY in
	// internal/panel/frame.go) and read from the UI goroutine's key-handling
	// path (frame.go's HandleKey, hotkey_conditions.go) as well as by tests
	// driving InitPTY end to end, so unlike the rest of this grid/session
	// state it needs its own cross-goroutine synchronization rather than
	// tv.mu: an atomic.Int32, mirroring how AnsiParser guards its own
	// cross-goroutine flags (syncEchoTracked/syncEchoArms in ansi.go).
	KittyFlags      atomic.Int32
	KittyFlagsStack []int
	// kittyBeforeCommand and kittyCommandRunning scope the shell's own kitty
	// flags to its prompt: they hold the flags in force when a command started
	// (OSC 133;C) so that the command runs with none and the shell gets them
	// back when it ends (OSC 133;D). See HandleOSC133.
	kittyBeforeCommand  atomic.Int32
	kittyCommandRunning atomic.Bool
	AutoWrap            bool
	SixelDisplayMode    bool
	MouseTrackingMode   int
	MouseSGRMode        bool

	clipboardChunks []byte
	ClipboardReader func() string
	ClipboardWriter func(string)
	Pty             PtyBackend
	kitty           *KittyGraphics
	// Unicode placeholders (kitty_placeholder.go): the virtual placements a
	// program has made, what the marks after each placeholder cell said, and
	// the placeholder the next mark belongs to.
	virtual     map[uint32]kittyVirtual
	phMeta      map[*vtui.CharInfo]map[int]placeholderCell
	phLast      placeholderRun
	Images      []terminalImage
	kittyKeySeq uint64
	CellW       int
	CellH       int

	Muted         bool
	lastCharWasCR bool

	// DefaultColors draws the terminal's default foreground and background
	// as the host terminal's own default colours (see hostDefaultColors),
	// for the mirror of a host console shown beside a hidden panel (#1675).
	DefaultColors bool

	// syncScrollBudget is how many scrolls caused by a line feed on the last
	// row are still to be kept out after an excised directory-sync echo, and
	// syncScrollUntil is when the budget lapses whatever happens (#1673).
	syncScrollBudget int
	syncScrollUntil  time.Time

	// reflow makes a width change re-wrap the primary screen and GridHistory
	// by the view's own wrap flags; see view_reflow.go. The owner sets it per
	// session, because the flags only mean something when the stream delivers
	// long lines whole.
	reflow bool

	// suppressEraseHistory stays set after a primary-screen reflow until the
	// next printable cell arrives. ConPTY/OpenConsole commonly repaints a
	// resized screen with one or more ED2 sequences before redrawing it. The
	// rows have already been preserved by reflow; pushing the same viewport
	// again would manufacture duplicate logical output in scrollback.
	suppressEraseHistory bool

	// promptOverlaysLastRow records that f4 paints its own command line over
	// the grid's bottom row, so that row belongs to the shell prompt alone.
	// Written by the layout on the UI goroutine, read by the parser on the
	// PTY goroutine; both go through the view's mutex.
	promptOverlaysLastRow bool

	authCache map[string]int

	// dnd is the terminal side of the far2l drag-and-drop protocol, see
	// far2l_dnd.go. It guards itself.
	dnd dndServer

	OnTitleChange func(string)
	OnBusyChange  func(bool)
	// OnShellMark receives every OSC 133 mark (A, B, C, D, E...) together
	// with the cursor state at that moment. It runs on the PTY goroutine.
	OnShellMark func(mark string, snap PromptSnapshot)
	// OnCursorShown fires on DECTCEM set (ESC[?25h), the end of a ConPTY frame.
	OnCursorShown func()

	// --- Mouse-driven text selection over the visible viewport ---
	// Coordinates are absolute (screen) columns/rows, chosen so the
	// highlight stays visually anchored while PTY output scrolls the
	// underlying grid — matches xterm-style selection semantics.
	SelActive  bool
	selStartX  int
	selStartY  int
	selEndX    int
	selEndY    int
	SelBlock   bool
	showOffset int // last vertical "visual gravity" offset applied in Show
	// gravityLow is the lowest row the primary screen has reached since it
	// was last cleared, reset or resized. Gravity measures from it, not from
	// the lowest row that holds text right now: a program redrawing its
	// progress erases the rows below and writes them again, and measuring
	// from the text alone made the whole screen jump down and back (f4#1749).
	gravityLow int
	hoverURL   string
}

func NewTerminalView(w, h int) *TerminalView {
	tv := &TerminalView{
		Width:     w,
		Height:    h,
		AutoWrap:  true,
		authCache: make(map[string]int),
	}
	tv.ResetBuffer(w, h)
	return tv
}

func (tv *TerminalView) ReadClipboard() string {
	if tv.ClipboardReader != nil {
		return tv.ClipboardReader()
	}
	return vtui.GetClipboard()
}

func (tv *TerminalView) writeClipboard(text string) {
	if tv.ClipboardWriter != nil {
		tv.ClipboardWriter(text)
		return
	}
	SetF4Clipboard(text)
}

// CopySelectionToClipboard keeps terminal selection copies on f4's clipboard
// path while allowing tests to intercept the write without touching the host
// clipboard.
func (tv *TerminalView) CopySelectionToClipboard(text string) {
	if tv.ClipboardWriter != nil {
		tv.ClipboardWriter(text)
		return
	}
	SetF4Clipboard(text)
}

func (tv *TerminalView) CloneStateFrom(other *TerminalView) {
	other.FlushLog()
	other.mu.Lock()
	defer other.mu.Unlock()
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// 1. Match dimensions and re-allocate grids
	tv.Width = other.Width
	tv.Height = other.Height

	allocGrid := func(src [][]vtui.CharInfo) [][]vtui.CharInfo {
		dst := make([][]vtui.CharInfo, len(src))
		for y := range src {
			dst[y] = make([]vtui.CharInfo, len(src[y]))
			copy(dst[y], src[y])
		}
		return dst
	}
	tv.Lines = allocGrid(other.Lines)
	tv.AltLines = allocGrid(other.AltLines)
	tv.WrapFlags = make([]bool, len(other.WrapFlags))
	copy(tv.WrapFlags, other.WrapFlags)
	tv.reflow = other.reflow

	tv.GridHistory = make([][]vtui.CharInfo, len(other.GridHistory))
	for i := range other.GridHistory {
		tv.GridHistory[i] = make([]vtui.CharInfo, len(other.GridHistory[i]))
		copy(tv.GridHistory[i], other.GridHistory[i])
	}
	tv.GridHistoryWrap = make([]bool, len(other.GridHistoryWrap))
	copy(tv.GridHistoryWrap, other.GridHistoryWrap)

	// 2. Deep copy the PieceTable (History)
	bytes, _ := other.Pt.Bytes()
	tv.Pt = piecetable.New(bytes)

	// 3. Re-initialize indices and engine to point to the NEW pt
	tv.Li = piecetable.NewLineIndex()
	tv.Li.Rebuild(tv.Pt)
	tv.Engine = textlayout.NewWrapEngine(tv.Pt, tv.Li)
	tv.Engine.SetWidth(tv.Width)

	// 4. Copy terminal state metadata
	tv.styles = append([]StyleChange(nil), other.styles...)
	tv.authCache = make(map[string]int)
	for k, v := range other.authCache {
		tv.authCache[k] = v
	}
	tv.lastAttr = other.lastAttr
	tv.Palette = other.Palette
	tv.CursorX, tv.CursorY = other.CursorX, other.CursorY
	tv.CursorVisible = other.CursorVisible
	tv.UseAltScreen = other.UseAltScreen
	tv.ScrollTop, tv.ScrollBottom = other.ScrollTop, other.ScrollBottom
	tv.KittyFlags.Store(other.KittyFlags.Load())
	tv.KittyFlagsStack = append([]int(nil), other.KittyFlagsStack...)
	// Selection coordinates belong to the old viewport and are not part of
	// the cloned terminal state. Keeping them would paint a stale highlight
	// over the clone's first screen.
	tv.SelActive = false
	// The PTY is an ownership handle, not terminal display state. A cloned
	// PanelsFrame starts its own shell asynchronously; copying this field would
	// briefly route input from the clone into the source workspace until that
	// shell wins the race.
	tv.Images = append([]terminalImage(nil), other.Images...)

	vtui.DebugLog("TERM_VIEW: CloneStateFrom completed. Cleaning active row for new shell.")

	// Clear the active visual row and reset horizontal cursor
	if tv.CursorY >= 0 && tv.CursorY < len(tv.Lines) {
		for x := range tv.Lines[tv.CursorY] {
			tv.Lines[tv.CursorY][x] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
		}
	}
	tv.CursorX = 0
}

func (tv *TerminalView) ResetBuffer(w, h int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	// RIS replaces both terminal screens, so any screen-coordinate selection
	// from before the reset is no longer meaningful.
	tv.SelActive = false
	tv.gravityLow = 0

	// Инициализация PieceTable (только один раз)
	if tv.Pt == nil {
		tv.Pt = piecetable.New([]byte{})
		tv.Li = piecetable.NewLineIndex()
		tv.Engine = textlayout.NewWrapEngine(tv.Pt, tv.Li)
		tv.styles = []StyleChange{{0, DefaultTermAttr}}
		tv.lastAttr = DefaultTermAttr
	}
	tv.Engine.SetWidth(w)

	// A reset puts sixel scrolling back on, which is its default state.
	tv.SixelDisplayMode = false

	// Создание сеток (Grid)
	makeBuf := func() [][]vtui.CharInfo {
		b := make([][]vtui.CharInfo, h)
		for i := range b {
			b[i] = make([]vtui.CharInfo, w)
			for j := range b[i] {
				b[i][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
			}
		}
		return b
	}

	tv.Lines = makeBuf()
	tv.AltLines = makeBuf()
	tv.WrapFlags = make([]bool, h)
	tv.Images = nil
	tv.virtual, tv.phMeta, tv.phLast = nil, nil, placeholderRun{}

	// Сброс параметров прокрутки и курсора
	tv.Width, tv.Height = w, h
	tv.ScrollTop = 0
	tv.ScrollBottom = h - 1
	tv.CursorX = 0
	tv.CursorY = h - 1 // Восстановлено выравнивание по нижнему краю для правильного визуала (прилипание к низу)
	tv.CursorVisible = true
	tv.lastCharWasCR = true
	vtui.DebugLog("TERM_VIEW: ResetBuffer to %dx%d. VTE Mirror initialized at bottom (%d)", w, h, tv.CursorY)

	// Палитра по умолчанию (ANSI order)
	copy(tv.Palette[:], vtui.XTerm256Palette[:])
	tv.Palette[0] = theme.Far2lPalette[0] // Black
	tv.Palette[1] = theme.Far2lPalette[4] // Red
	tv.Palette[2] = theme.Far2lPalette[2] // Green
	tv.Palette[3] = theme.Far2lPalette[6] // Yellow
	tv.Palette[4] = theme.Far2lPalette[1] // Blue
	tv.Palette[5] = theme.Far2lPalette[5] // Magenta
	tv.Palette[6] = theme.Far2lPalette[3] // Cyan
	tv.Palette[7] = theme.Far2lPalette[7] // White
	for i := 0; i < 8; i++ {
		winIdx := []int{0, 4, 2, 6, 1, 5, 3, 7}[i]
		tv.Palette[i+8] = theme.Far2lPalette[winIdx+8]
	}
}

func (tv *TerminalView) GetBuffer() [][]vtui.CharInfo {
	if tv.UseAltScreen {
		return tv.AltLines
	}
	return tv.Lines
}

func (tv *TerminalView) SetMuted(muted bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.Muted = muted
}
func (tv *TerminalView) PrintCleanCommand(cleanCmd string) {
	// Печатаем команду строго там, где сейчас находится курсор терминала (у промпта)
	for _, r := range cleanCmd {
		tv.PutChar(r, DefaultTermAttr)
	}
	tv.PutChar('\r', DefaultTermAttr)
	tv.PutChar('\n', DefaultTermAttr)
	tv.FlushLog()
}
func (tv *TerminalView) FlushLog() {}
func (tv *TerminalView) rowHasText(y int) bool {
	if y < 0 || y >= tv.Height {
		return false
	}
	for x := 0; x < tv.Width; x++ {
		if tv.Lines[y][x].Char != ' ' || tv.Lines[y][x].Attributes != DefaultTermAttr {
			return true
		}
	}
	return false
}
func (tv *TerminalView) pushRowToGridHistory(y int) {
	lineCopy := make([]vtui.CharInfo, len(tv.Lines[y]))
	copy(lineCopy, tv.Lines[y])
	tv.GridHistory = append(tv.GridHistory, lineCopy)
	tv.GridHistoryWrap = append(tv.GridHistoryWrap, tv.WrapFlags[y])

	tv.trimGridHistoryLocked()
}

func (tv *TerminalView) extrudeGridHistoryRow(idx int) {
	line := tv.GridHistory[idx]
	isWrapped := tv.GridHistoryWrap[idx]

	lastChar := len(line) - 1
	for lastChar >= 0 && isTrailingBlank(line[lastChar]) {
		lastChar--
	}

	var sb strings.Builder
	for i := 0; i <= lastChar; i++ {
		if isWrapPad(line[i]) {
			continue
		}
		// Saving attributes for the log
		if line[i].Attributes != tv.lastAttr {
			tv.styles = append(tv.styles, StyleChange{Offset: int(tv.Pt.Size()) + sb.Len(), Attr: line[i].Attributes})
			tv.lastAttr = line[i].Attributes
		}
		sb.WriteString(vtui.CellString(line[i].Char))
	}
	if !isWrapped {
		sb.WriteRune('\n')
	}
	text := sb.String()
	if len(text) > 0 {
		offset := tv.Pt.Size()
		tv.Pt.Insert(offset, []byte(text))
		tv.Li.UpdateAfterInsert(offset, []byte(text))
		tv.Engine.InvalidateFrom(tv.Li.LineCount() - 2)
	}
}

func (tv *TerminalView) GetAllLogBytes() []byte {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	hist, _ := tv.Pt.Bytes()
	var sb strings.Builder
	sb.Write(hist)

	for i := 0; i < len(tv.GridHistory); i++ {
		line := tv.GridHistory[i]
		isWrapped := tv.GridHistoryWrap[i]
		lastChar := len(line) - 1
		for lastChar >= 0 && isTrailingBlank(line[lastChar]) {
			lastChar--
		}
		for j := 0; j <= lastChar; j++ {
			if !isWrapPad(line[j]) {
				sb.WriteString(vtui.CellString(line[j].Char))
			}
		}
		if !isWrapped {
			sb.WriteRune('\n')
		}
	}

	if !tv.UseAltScreen {
		lastValidRow := 0
		for y := tv.Height - 1; y >= 0; y-- {
			if tv.rowHasText(y) {
				lastValidRow = y
				break
			}
		}
		if tv.CursorY > lastValidRow {
			lastValidRow = tv.CursorY
		}

		firstValidRow := 0
		if len(tv.GridHistory) == 0 && tv.Pt.Size() == 0 {
			for y := 0; y <= lastValidRow; y++ {
				if tv.rowHasText(y) || y == tv.CursorY {
					firstValidRow = y
					break
				}
			}
		}

		for y := firstValidRow; y <= lastValidRow && y < tv.Height; y++ {
			line := tv.Lines[y]
			isWrapped := tv.WrapFlags[y]

			lastChar := len(line) - 1
			for lastChar >= 0 && isTrailingBlank(line[lastChar]) {
				lastChar--
			}

			for i := 0; i <= lastChar; i++ {
				if !isWrapPad(line[i]) {
					sb.WriteString(vtui.CellString(line[i].Char))
				}
			}
			if !isWrapped && y < lastValidRow {
				sb.WriteRune('\n')
			}
		}
	}
	return []byte(sb.String())
}

func (tv *TerminalView) PutChar(r rune, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()

	if tv.Muted {
		return
	}

	if r != '\r' && r != '\n' {
		tv.syncScrollBudget = 0
	}
	if r == '\r' {
		// vtui.DebugLog("TERM_VIEW: CR (CursorX: %d -> 0)", tv.CursorX)
		tv.CursorX = 0
		tv.lastCharWasCR = true
		return
	}
	if r == '\n' {
		// vtui.DebugLog("TERM_VIEW: LF (CursorY: %d -> %d)", tv.CursorY, tv.CursorY+1)
		if !tv.UseAltScreen && tv.CursorY >= 0 && tv.CursorY < tv.Height {
			// A line feed is a hard break. Only the terminal's own autowrap
			// marks a row as wrapped; nothing is guessed from the shape of
			// the stream (docs/CONPTY_RESEARCH.md section 7).
			tv.WrapFlags[tv.CursorY] = false
		}
		tv.newline()
		return
	}
	if r == '\b' {
		if tv.CursorX > 0 {
			tv.CursorX--
		}
		return
	}
	if r == '\t' {
		tv.CursorX = (tv.CursorX + 8) & ^7
		if tv.CursorX >= tv.Width {
			tv.CursorX = tv.Width - 1
		}
		return
	}
	if r < 0x20 {
		return
	}
	// A mark after a Unicode placeholder belongs to it and takes no cell.
	if tv.placeholderMark(r) {
		return
	}
	tv.phLast = placeholderRun{}

	w := runewidth.RuneWidth(r)
	if w <= 0 {
		w = 1
	}

	if tv.CursorX >= tv.Width {
		if tv.AutoWrap {
			if !tv.UseAltScreen && tv.CursorY >= 0 && tv.CursorY < tv.Height {
				tv.WrapFlags[tv.CursorY] = true // Soft wrap (reached edge)
			}
			tv.newline()
		} else {
			tv.CursorX = tv.Width - 1 // Overwrite last character instead of wrapping
		}
	} else if w > 1 && tv.AutoWrap && tv.CursorX > 0 && tv.CursorX+w > tv.Width && w <= tv.Width {
		// A wide character that does not fit the columns left starts the
		// next row, as in xterm. It used to be dropped. On the primary screen
		// the columns it could not use are padded with cells that are not
		// text, so the row reads back, and re-wraps, without a space that was
		// never printed.
		if tv.CursorY >= 0 && tv.CursorY < tv.Height {
			buf := tv.GetBuffer()
			for x := tv.CursorX; x < tv.Width && x < len(buf[tv.CursorY]); x++ {
				if tv.UseAltScreen {
					buf[tv.CursorY][x] = vtui.CharInfo{Char: ' ', Attributes: attr}
				} else {
					buf[tv.CursorY][x] = wrapPadCell
				}
			}
			if !tv.UseAltScreen {
				tv.WrapFlags[tv.CursorY] = true
			}
		}
		tv.newline()
	}

	buf := tv.GetBuffer()
	if tv.CursorY >= 0 && tv.CursorY < tv.Height && tv.CursorX >= 0 && tv.CursorX+w <= tv.Width {
		buf[tv.CursorY][tv.CursorX] = vtui.CharInfo{Char: uint64(r), Attributes: attr}
		for i := 1; i < w; i++ {
			buf[tv.CursorY][tv.CursorX+i] = vtui.CharInfo{Char: vtui.WideCharFiller, Attributes: attr}
		}
		if r == kittyPlaceholderRune {
			tv.placeholderStarted(tv.CursorY, tv.CursorX)
		}
		tv.CursorX += w
		tv.suppressEraseHistory = false
	}
	tv.lastCharWasCR = false
}

// syncScrollWindow bounds how long after an excised sync echo the scrolls of
// its two line feeds are still kept out.
const syncScrollWindow = time.Second

// SuppressSyncScroll keeps the next n line-feed scrolls off the screen. It is
// called when f4 has cut the echo of its own directory-sync line out of the
// stream: the echo's row is erased, cmd.exe then ends that line and prints a
// blank one before the prompt, and on the bottom row each of those scrolls
// the console up for output the user never sees -- so every panel toggle that
// changed the directory moved the console one line up (#1673). The prompt is
// drawn on the erased row instead, which is where ConPTY draws it too. Text
// arriving ends the exemption; so does the time window.
func (tv *TerminalView) SuppressSyncScroll(n int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.syncScrollBudget = n
	tv.syncScrollUntil = time.Now().Add(syncScrollWindow)
}

func (tv *TerminalView) newline() {
	// vtui.DebugLog("TERM: newline at Y=%d (ScrollBottom=%d)", tv.CursorY, tv.ScrollBottom)
	tv.CursorX = 0
	if tv.syncScrollBudget > 0 && tv.CursorY == tv.ScrollBottom && !tv.UseAltScreen {
		if time.Now().Before(tv.syncScrollUntil) {
			tv.syncScrollBudget--
			return
		}
		tv.syncScrollBudget = 0
	}
	tv.CursorY++
	if tv.CursorY > tv.ScrollBottom {
		tv.scrollUp(tv.ScrollTop, tv.ScrollBottom, 1)
		tv.CursorY = tv.ScrollBottom
	}
}
func (tv *TerminalView) ReverseIndex() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	if tv.CursorY == tv.ScrollTop {
		tv.scrollDown(tv.ScrollTop, tv.ScrollBottom, 1)
	} else if tv.CursorY > 0 {
		tv.CursorY--
	}
}

func (tv *TerminalView) Index() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	tv.CursorY++
	if tv.CursorY > tv.ScrollBottom {
		tv.scrollUp(tv.ScrollTop, tv.ScrollBottom, 1)
		tv.CursorY = tv.ScrollBottom
	}
}

func (tv *TerminalView) NextLine() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	tv.CursorX = 0
	tv.CursorY++
	if tv.CursorY > tv.ScrollBottom {
		tv.scrollUp(tv.ScrollTop, tv.ScrollBottom, 1)
		tv.CursorY = tv.ScrollBottom
	}
}

func (tv *TerminalView) scrollUp(top, bottom, n int) {
	buf := tv.GetBuffer()
	if top < 0 {
		top = 0
	}
	if bottom >= len(buf) {
		bottom = len(buf) - 1
	}
	if top >= bottom {
		return
	}

	for i := 0; i < n; i++ {
		if !tv.UseAltScreen && top == 0 {
			// Не пушим пустые строки в лог, если он еще девственно чист
			// Это предотвращает появление 23 пустых строк при старте bash
			if len(tv.GridHistory) > 0 || tv.Pt.Size() > 0 || tv.rowHasText(top) {
				vtui.DebugLog("TERM_VIEW: ScrollUp extruding row %d to history", top)
				tv.pushRowToGridHistory(top)
			} else {
				vtui.DebugLog("TERM_VIEW: ScrollUp skipped extruding row %d (Extrusion Guard active)", top)
			}
		}
		recycledLine := buf[top]
		copy(buf[top:bottom], buf[top+1:bottom+1])
		buf[bottom] = recycledLine
		for j := range buf[bottom] {
			buf[bottom][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
		}
		if !tv.UseAltScreen {
			copy(tv.WrapFlags[top:bottom], tv.WrapFlags[top+1:bottom+1])
			tv.WrapFlags[bottom] = false
		}
	}
	tv.kittyScrollPlacements(top, bottom, n)
}
func (tv *TerminalView) ScrollDown(top, bottom, n int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	tv.scrollDown(top, bottom, n)
}

func (tv *TerminalView) scrollDown(top, bottom, n int) {
	buf := tv.GetBuffer()
	if top < 0 {
		top = 0
	}
	if bottom >= len(buf) {
		bottom = len(buf) - 1
	}
	if top >= bottom {
		return
	}

	for i := 0; i < n; i++ {
		recycledLine := buf[bottom]
		for y := bottom; y > top; y-- {
			buf[y] = buf[y-1]
		}
		buf[top] = recycledLine
		for j := range buf[top] {
			buf[top][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
		}
		if !tv.UseAltScreen {
			for y := bottom; y > top; y-- {
				tv.WrapFlags[y] = tv.WrapFlags[y-1]
			}
			tv.WrapFlags[top] = false
		}
	}
	tv.kittyScrollPlacements(top, bottom, -n)
}

func (tv *TerminalView) DeleteCharacters(n int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	buf := tv.GetBuffer()
	if tv.CursorY < 0 || tv.CursorY >= len(buf) {
		return
	}
	line := buf[tv.CursorY]
	if tv.CursorX < 0 || tv.CursorX >= tv.Width {
		return
	}

	if tv.CursorX+n < len(line) {
		copy(line[tv.CursorX:], line[tv.CursorX+n:])
	}

	clearStart := len(line) - n
	if clearStart < tv.CursorX {
		clearStart = tv.CursorX
	}
	for i := clearStart; i < len(line); i++ {
		line[i] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) InsertBlankCharacters(n int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	buf := tv.GetBuffer()
	if tv.CursorY < 0 || tv.CursorY >= len(buf) {
		return
	}
	line := buf[tv.CursorY]
	if tv.CursorX < 0 || tv.CursorX >= tv.Width {
		return
	}

	if tv.CursorX+n < len(line) {
		copy(line[tv.CursorX+n:], line[tv.CursorX:])
	}

	end := tv.CursorX + n
	if end > len(line) {
		end = len(line)
	}
	for i := tv.CursorX; i < end; i++ {
		line[i] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) SetCursor(x, y int) {
	// vtui.DebugLog("TERM: SetCursor to (%d,%d)", x, y)
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	if x < 0 {
		x = 0
	}
	if x >= tv.Width {
		x = tv.Width - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= tv.Height {
		y = tv.Height - 1
	}
	tv.CursorX, tv.CursorY = x, y
	if x == 0 {
		tv.lastCharWasCR = true
	}
}

// SetCursorVisible applies DECTCEM (CSI ? 25 h/l) to the terminal state.
// It is separate from the host ScreenBuf cursor: the latter is reset before
// every frame and is only updated while this terminal view owns focus.
func (tv *TerminalView) SetCursorVisible(visible bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.CursorVisible = visible
}

func (tv *TerminalView) SaveCursor() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	tv.decSavedX, tv.decSavedY = tv.CursorX, tv.CursorY
}

func (tv *TerminalView) RestoreCursor() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	tv.CursorX, tv.CursorY = tv.decSavedX, tv.decSavedY
}

func (tv *TerminalView) RepeatLastChar(n int, r rune, attr uint64) {
	for i := 0; i < n; i++ {
		tv.PutChar(r, attr)
	}
}

func (tv *TerminalView) EraseCharacter(n int, attr uint64) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	buf := tv.GetBuffer()
	if tv.CursorY < 0 || tv.CursorY >= len(buf) {
		return
	}
	line := buf[tv.CursorY]
	for i := 0; i < n && (tv.CursorX+i) < len(line); i++ {
		line[tv.CursorX+i] = vtui.CharInfo{Char: ' ', Attributes: attr}
	}
}

func (tv *TerminalView) EraseDisplay(mode int, attr uint64) {
	// vtui.DebugLog("TERM_VIEW: EraseDisplay mode=%d", mode)
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	if mode == 2 {
		// ED 2 replaces the visible screen. Do not keep painting the old
		// screen-coordinate selection over the newly cleared contents.
		tv.SelActive = false
	}
	if (mode == 2 || mode == 3) && !tv.UseAltScreen {
		// A cleared screen starts over: what comes next sinks to the bottom.
		tv.gravityLow = 0
	}

	if (mode == 2 || mode == 3) && !tv.UseAltScreen && !tv.suppressEraseHistory {
		// Сохраняем экран в историю перед очисткой (игнорируя пустоту снизу)
		lastRow := -1
		for y := 0; y < tv.Height; y++ {
			if tv.rowHasText(y) {
				lastRow = y
			}
		}
		// vtui.DebugLog("TERM_VIEW: EraseDisplay(%d) pushing viewport up to row %d to history", mode, lastRow)
		for y := 0; y <= lastRow; y++ {
			tv.pushRowToGridHistory(y)
		}
		for i := range tv.WrapFlags {
			tv.WrapFlags[i] = false
		}
	}

	buf := tv.GetBuffer()
	switch mode {
	case 2:
		tv.CursorX = 0
		tv.CursorY = 0
		tv.lastCharWasCR = true
		tv.kittyClearPlacements(tv.UseAltScreen)
		for i := range buf {
			for j := range buf[i] {
				buf[i][j] = vtui.CharInfo{Char: ' ', Attributes: attr}
			}
		}
	case 0:
		if tv.CursorY >= 0 && tv.CursorY < tv.Height {
			line := buf[tv.CursorY]
			for j := (tv.CursorX); j < len(line); j++ {
				if j >= 0 {
					line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
				}
			}
			if !tv.UseAltScreen {
				tv.WrapFlags[tv.CursorY] = false
			}
		}
		for i := tv.CursorY + 1; i < tv.Height; i++ {
			if i >= 0 && i < len(buf) {
				line := buf[i]
				for j := range line {
					line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
				}
				if !tv.UseAltScreen {
					tv.WrapFlags[i] = false
				}
			}
		}
	case 1:
		// ED 1 ("Erase Above"): everything from the top-left corner through
		// the cursor, inclusive, is blanked; rows below the cursor are left
		// untouched. This mirrors case 0 (erase from cursor to the bottom)
		// and EraseLine's own mode 1, which already implements the
		// equivalent "start of line to cursor" rule. It was missing here,
		// so a program sending CSI 1 J (e.g. `clear` on some shells, or an
		// editor repainting the top of the screen) saw no effect at all.
		if tv.CursorY >= 0 && tv.CursorY < tv.Height {
			line := buf[tv.CursorY]
			end := tv.CursorX + 1
			if end > len(line) {
				end = len(line)
			}
			for j := 0; j < end; j++ {
				line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
			}
			if !tv.UseAltScreen {
				tv.WrapFlags[tv.CursorY] = false
			}
		}
		for i := 0; i < tv.CursorY; i++ {
			if i >= 0 && i < len(buf) {
				line := buf[i]
				for j := range line {
					line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
				}
				if !tv.UseAltScreen {
					tv.WrapFlags[i] = false
				}
			}
		}
	}
}

func (tv *TerminalView) EraseLine(mode int, attr uint64) {
	// vtui.DebugLog("TERM_VIEW: EraseLine mode=%d at Y=%d (X=%d)", mode, tv.CursorY, tv.CursorX)
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.Muted {
		return
	}
	buf := tv.GetBuffer()
	if tv.CursorY < 0 || tv.CursorY >= len(buf) {
		return
	}
	line := buf[tv.CursorY]
	start, end := 0, len(line)
	switch mode {
	case 0:
		start = tv.CursorX
	case 1:
		end = tv.CursorX + 1
	}
	for j := start; j < end; j++ {
		if j >= 0 && j < len(line) {
			line[j] = vtui.CharInfo{Char: ' ', Attributes: attr}
		}
	}
	if !tv.UseAltScreen && (mode == 2 || (mode == 0 && tv.CursorX == 0)) {
		tv.WrapFlags[tv.CursorY] = false
	}
}

func (tv *TerminalView) SetAltScreen(enable bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	// Logged under the lock: this runs on the read loop while Resize on the
	// UI goroutine moves the cursor, and the race detector said so.
	vtui.DebugLog("TERM_VIEW: Switching screen buffer. AltScreen enabled: %v (Current Cursor: %d,%d)", enable, tv.CursorX, tv.CursorY)
	if tv.UseAltScreen == enable {
		return
	}
	// The alternate and primary screens have independent contents and can
	// have different geometry, so a selection cannot safely cross the switch.
	tv.SelActive = false
	if enable {
		tv.savedX, tv.savedY = tv.CursorX, tv.CursorY
		tv.CursorX, tv.CursorY = 0, 0
	} else {
		tv.CursorX, tv.CursorY = tv.savedX, tv.savedY
		// The alternate screen is discarded rather than remembered: the
		// next program to raise it is given an empty one, and the erase on
		// the way in proves it. Its pictures go with it, or they would keep
		// their pixels alive for as long as the session lasts.
		tv.kittyClearPlacements(true)
	}
	tv.UseAltScreen = enable
}

func (tv *TerminalView) getAttrAt(offset int) uint64 {
	idx := sort.Search(len(tv.styles), func(i int) bool {
		return tv.styles[i].Offset > offset
	})
	if idx > 0 {
		return tv.styles[idx-1].Attr
	}
	return DefaultTermAttr
}

func (tv *TerminalView) Show(scr *vtui.ScreenBuf) {
	tv.ScreenObject.Show(scr)

	scr.ActivePalette = &tv.Palette
	// Terminal content must always be rendered without Early Binding
	// to allow the host terminal to use its native indexed palette.
	prevOverlay := scr.OverlayMode
	scr.SetOverlayMode(false)
	defer func() { scr.SetOverlayMode(prevOverlay) }()

	tv.mu.Lock()
	defer tv.mu.Unlock()

	// Очищаем всю область терминала черным цветом
	// The placement layer needs the real size of a cell to turn the pixel
	// geometry of an image into a rectangle of cells, and so does the
	// program running in the terminal, which learns it from the Pty.
	if cw, ch := scr.Graphics().CellSize(); cw > 0 && ch > 0 && (cw != tv.CellW || ch != tv.CellH) {
		tv.CellW, tv.CellH = cw, ch
		tv.syncPtyPixelSize()
		// A span we chose ourselves was worked out from the old cell, and
		// on the new one it would stretch the picture.
		tv.kittyRecomputeSpans()
	}

	fillAttr := DefaultTermAttr
	if tv.DefaultColors {
		fillAttr = hostDefaultColors(fillAttr)
	}
	scr.FillRect(tv.X1, tv.Y1, tv.X1+tv.Width-1, tv.Y1+tv.Height-1, ' ', fillAttr)

	buf := tv.Lines
	if tv.UseAltScreen {
		buf = tv.AltLines
	}

	offset := 0
	if !tv.UseAltScreen {
		lowestRow := 0
		for y := tv.Height - 1; y >= 0; y-- {
			if tv.rowHasText(y) {
				lowestRow = y
				break
			}
		}
		if tv.CursorY > lowestRow {
			lowestRow = tv.CursorY // Убеждаемся, что курсор также остается видимым
		}
		// A picture occupies rows that hold no text of their own, and
		// gravity must not push it out of the visible area.
		for i := range tv.Images {
			if tv.Images[i].Alt {
				continue
			}
			if bottom := tv.Images[i].Row + tv.Images[i].Rows - 1; bottom > lowestRow {
				lowestRow = bottom
			}
		}
		// Rows erased for a redraw keep their place, as on a real screen.
		if tv.gravityLow > lowestRow && tv.gravityLow < tv.Height {
			lowestRow = tv.gravityLow
		}
		tv.gravityLow = lowestRow
		// Visual Gravity: сдвигаем весь активный рендер вниз, если он не достает до дна
		if lowestRow < tv.Height-1 {
			offset = (tv.Height - 1) - lowestRow
		}
	}
	// The one draw-side fact worth a line: most of the screen is blank while
	// the model still holds rows. That pairing is the black area of 6.16 and
	// cannot be inferred from any model-side counter.
	if offset > tv.Height/2 && len(tv.GridHistory) > 0 && offset != tv.showOffset {
		rowsWithText := 0
		for y := 0; y < tv.Height && y < len(tv.Lines); y++ {
			if tv.rowHasText(y) {
				rowsWithText++
			}
		}
		vtui.DebugLog("REFLOW_SHOW: %dx%d drawn with %d blank rows on top, %d rows of text, history %d",
			tv.Width, tv.Height, offset, rowsWithText, len(tv.GridHistory))
	}
	tv.showOffset = offset

	for y, line := range buf {
		if y >= tv.Height {
			break
		}
		drawY := tv.Y1 + y + offset
		if tv.UseAltScreen {
			drawY = tv.Y1 + y
		}
		// Проверка выхода за пределы экрана
		if drawY >= tv.Y1 && drawY <= tv.Y1+tv.Height-1 {
			drawLine := append([]vtui.CharInfo(nil), line...)
			for i := range drawLine {
				if drawLine[i].Char == kittyPlaceholderRune {
					drawLine[i].Char = ' ' // the picture is drawn over the cell
				}
			}
			if tv.DefaultColors {
				for i := range drawLine {
					drawLine[i].Attributes = hostDefaultColors(drawLine[i].Attributes)
				}
			}
			viewer.ApplyURLHoverAttr(drawLine, viewer.UrlCellRangesFromCells(line), tv.hoverURL)
			scr.Write(tv.X1, drawY, drawLine)
		}
	}

	tv.kittyDrawPlacements(scr, offset)

	if tv.SelActive {
		tv.paintSelectionHighlight(scr)
	}

	if tv.IsVisible() && tv.IsFocused() && tv.CursorVisible {
		cursorDrawY := tv.Y1 + tv.CursorY + offset
		if tv.UseAltScreen {
			cursorDrawY = tv.Y1 + tv.CursorY
		}
		if cursorDrawY >= tv.Y1 && cursorDrawY <= tv.Y1+tv.Height-1 {
			scr.SetCursorPos(tv.X1+tv.CursorX, cursorDrawY)
			scr.SetCursorVisible(true)
		}
	} else if tv.IsVisible() && tv.IsFocused() {
		scr.SetCursorVisible(false)
	}
}

// SetPosition keeps a mouse selection tied to the viewport it came from.
// ResizeConsole changes the terminal's position before it knows whether the
// dimensions also changed, so clearing only from Resize would leave a stale
// highlight when panels are hidden or restored at the same size.
func (tv *TerminalView) SetPosition(x1, y1, x2, y2 int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.X1 != x1 || tv.Y1 != y1 || tv.X2 != x2 || tv.Y2 != y2 {
		tv.SelActive = false
	}
	tv.ScreenObject.SetPosition(x1, y1, x2, y2)
}

// selectionScreenRect returns the normalised screen-coordinate
// rectangle currently painted as selection, clamped to the terminal's
// visible area. Ok is false when there's no active selection.
func (tv *TerminalView) selectionScreenRect() (x1, y1, x2, y2 int, ok bool) {
	if !tv.SelActive {
		return 0, 0, 0, 0, false
	}
	x1, x2 = tv.selStartX, tv.selEndX
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	y1, y2 = tv.selStartY, tv.selEndY
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if x1 < tv.X1 {
		x1 = tv.X1
	}
	if y1 < tv.Y1 {
		y1 = tv.Y1
	}
	if x2 > tv.X1+tv.Width-1 {
		x2 = tv.X1 + tv.Width - 1
	}
	if y2 > tv.Y1+tv.Height-1 {
		y2 = tv.Y1 + tv.Height - 1
	}
	if x1 > x2 || y1 > y2 {
		return 0, 0, 0, 0, false
	}
	return x1, y1, x2, y2, true
}

// paintSelectionHighlight inverts fg↔bg for every cell inside the
// selection area. Stream selections span from the start column on the
// first row to the end column on the last row, filling middle rows
// edge-to-edge; block selections are strict rectangles.
func (tv *TerminalView) paintSelectionHighlight(scr *vtui.ScreenBuf) {
	x1, y1, x2, y2, ok := tv.selectionScreenRect()
	if !ok {
		return
	}
	rowLeft := func(y int) int {
		if tv.SelBlock {
			return x1
		}
		if y == y1 && !singleRow(y1, y2) {
			return normalizedStart(tv.selStartX, tv.selStartY, tv.selEndX, tv.selEndY, true)
		}
		if y == y1 && singleRow(y1, y2) {
			return x1
		}
		return tv.X1
	}
	rowRight := func(y int) int {
		if tv.SelBlock {
			return x2
		}
		if y == y2 && !singleRow(y1, y2) {
			return normalizedStart(tv.selStartX, tv.selStartY, tv.selEndX, tv.selEndY, false)
		}
		if y == y2 && singleRow(y1, y2) {
			return x2
		}
		return tv.X1 + tv.Width - 1
	}
	for y := y1; y <= y2; y++ {
		l, r := rowLeft(y), rowRight(y)
		if l < tv.X1 {
			l = tv.X1
		}
		if r > tv.X1+tv.Width-1 {
			r = tv.X1 + tv.Width - 1
		}
		for x := l; x <= r; x++ {
			ci := scr.GetCell(x, y)
			ci.Attributes = InvertAttrColors(ci.Attributes)
			scr.Write(x, y, []vtui.CharInfo{ci})
		}
	}
}

func singleRow(y1, y2 int) bool { return y1 == y2 }

// normalizedStart returns the column that starts / ends a stream
// selection across multiple rows. When returnStart is true it returns
// the left edge on the row that owns the top-most anchor; otherwise
// it returns the right edge on the row that owns the bottom-most.
func normalizedStart(sx, sy, ex, ey int, returnStart bool) int {
	topX, botX := sx, ex
	if sy > ey {
		topX, botX = ex, sx
	}
	if returnStart {
		return topX
	}
	return botX
}

// HasSelection reports whether the terminal currently owns a
// user-driven text selection over its viewport.
func (tv *TerminalView) HasSelection() bool {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return tv.SelActive
}

// StartSelection begins a new selection anchored at the given
// screen-absolute cell. block controls stream vs. rectangular mode.
func (tv *TerminalView) StartSelection(x, y int, block bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.SelActive = true
	tv.SelBlock = block
	tv.selStartX, tv.selStartY = x, y
	tv.selEndX, tv.selEndY = x, y
}

// ExtendSelection moves the loose end of an active selection.
func (tv *TerminalView) ExtendSelection(x, y int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if !tv.SelActive {
		return
	}
	tv.selEndX, tv.selEndY = x, y
}

// ClearSelection drops the highlight without touching the clipboard.
func (tv *TerminalView) ClearSelection() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.SelActive = false
}

// SelectionIsEmpty reports whether the current selection covers a
// single cell (i.e. a click without drag).
func (tv *TerminalView) SelectionIsEmpty() bool {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return !tv.SelActive || (tv.selStartX == tv.selEndX && tv.selStartY == tv.selEndY)
}

// gridRowForScreenY maps a screen-absolute Y to the index into
// tv.Lines / tv.AltLines that is currently visible at that row, or
// -1 if the row falls outside the visible viewport.
func (tv *TerminalView) gridRowForScreenY(y int) int {
	off := 0
	if !tv.UseAltScreen {
		off = tv.showOffset
	}
	logical := y - tv.Y1 - off
	if logical < 0 || logical >= tv.Height {
		return -1
	}
	return logical
}

// ExtractSelection returns the plain-text content of the current
// selection, read from the terminal's own grid. Trailing spaces on
// stream-selected rows are trimmed; block selections keep alignment.
// WideCharFiller cells are skipped so wide glyphs don't emit stray
// runes.
func (tv *TerminalView) ExtractSelection() string {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if !tv.SelActive {
		return ""
	}
	x1, y1, x2, y2, ok := tv.selectionScreenRect()
	if !ok {
		return ""
	}

	buf := tv.Lines
	if tv.UseAltScreen {
		buf = tv.AltLines
	}

	var sb strings.Builder
	for y := y1; y <= y2; y++ {
		gy := tv.gridRowForScreenY(y)
		if gy < 0 || gy >= len(buf) {
			if y < y2 {
				sb.WriteByte('\n')
			}
			continue
		}
		row := buf[gy]

		var l, r int
		if tv.SelBlock {
			l, r = x1, x2
		} else if y1 == y2 {
			l, r = x1, x2
		} else if y == y1 {
			l = normalizedStart(tv.selStartX, tv.selStartY, tv.selEndX, tv.selEndY, true)
			r = tv.X1 + tv.Width - 1
		} else if y == y2 {
			l = tv.X1
			r = normalizedStart(tv.selStartX, tv.selStartY, tv.selEndX, tv.selEndY, false)
		} else {
			l = tv.X1
			r = tv.X1 + tv.Width - 1
		}
		if l < tv.X1 {
			l = tv.X1
		}
		if r > tv.X1+tv.Width-1 {
			r = tv.X1 + tv.Width - 1
		}

		var line strings.Builder
		for x := l; x <= r; x++ {
			if x-tv.X1 >= len(row) {
				break
			}
			if cell := row[x-tv.X1]; !isWrapPad(cell) {
				line.WriteString(vtui.CellString(cell.Char))
			}
		}
		if !tv.SelBlock {
			sb.WriteString(strings.TrimRight(line.String(), " "))
		} else {
			sb.WriteString(line.String())
		}
		if y < y2 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// SelectWordAt sets a selection covering the whitespace-delimited
// word under the given screen cell. If the cell is whitespace,
// nothing changes.
func (tv *TerminalView) SelectWordAt(x, y int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	gy := tv.gridRowForScreenY(y)
	if gy < 0 {
		return
	}
	buf := tv.Lines
	if tv.UseAltScreen {
		buf = tv.AltLines
	}
	if gy >= len(buf) {
		return
	}
	row := buf[gy]
	col := x - tv.X1
	if col < 0 || col >= len(row) {
		return
	}
	isWord := func(ch uint64) bool {
		if ch == 0 || ch == vtui.WideCharFiller {
			return false
		}
		return ch != ' '
	}
	if !isWord(row[col].Char) {
		return
	}
	left := col
	for left > 0 && isWord(row[left-1].Char) {
		left--
	}
	right := col
	for right < len(row)-1 && isWord(row[right+1].Char) {
		right++
	}
	tv.SelActive = true
	tv.SelBlock = false
	tv.selStartX, tv.selStartY = tv.X1+left, y
	tv.selEndX, tv.selEndY = tv.X1+right, y
}

// SelectLineAt selects the whole visible row at the given screen Y.
func (tv *TerminalView) SelectLineAt(y int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	if tv.gridRowForScreenY(y) < 0 {
		return
	}
	tv.SelActive = true
	tv.SelBlock = false
	tv.selStartX, tv.selStartY = tv.X1, y
	tv.selEndX, tv.selEndY = tv.X1+tv.Width-1, y
}

// InTerminalArea reports whether a screen cell falls inside the
// terminal's visible viewport.
func (tv *TerminalView) InTerminalArea(x, y int) bool {
	return x >= tv.X1 && x <= tv.X1+tv.Width-1 && y >= tv.Y1 && y <= tv.Y1+tv.Height-1
}

func (tv *TerminalView) urlAtScreenCell(x, y int) (viewer.UrlCellRange, bool) {
	if !tv.InTerminalArea(x, y) {
		return viewer.UrlCellRange{}, false
	}
	row := tv.gridRowForScreenY(y)
	if row < 0 {
		return viewer.UrlCellRange{}, false
	}
	buf := tv.Lines
	if tv.UseAltScreen {
		buf = tv.AltLines
	}
	if row >= len(buf) {
		return viewer.UrlCellRange{}, false
	}
	col := x - tv.X1
	for _, link := range viewer.UrlCellRangesFromCells(buf[row]) {
		if col >= link.Start && col < link.End {
			return link, true
		}
	}
	return viewer.UrlCellRange{}, false
}

// UpdateURLHover tracks the URL under the pointer without changing terminal
// selection state. It returns true when a repaint is needed.
func (tv *TerminalView) UpdateURLHover(x, y int) bool {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	var next string
	if link, ok := tv.urlAtScreenCell(x, y); ok {
		next = link.URL
	}
	if next == tv.hoverURL {
		return false
	}
	tv.hoverURL = next
	return true
}

func (tv *TerminalView) URLAt(x, y int) (string, bool) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	link, ok := tv.urlAtScreenCell(x, y)
	if !ok {
		return "", false
	}
	return link.URL, true
}

// GridHistory is the editable tail of the log: oracle corrections can still
// update its row boundaries. Older rows are extruded into the PieceTable,
// where a soft wrap is already encoded by the absence of a newline. Keeping a
// hard bound also makes a full-history reflow cheap enough for every resize.
// maxGridHistoryLines bounds the history in *logical* lines, and
// maxGridHistoryRowsHard bounds the physical rows only to keep memory finite.
//
// A row cap is the wrong bound for a buffer whose row count depends on the
// current width, and that is the whole of issue #425's scrollback loss. The
// same text occupies 2029 rows at 120 columns and 2068 at 37; each narrowing
// pass therefore produced more rows than it consumed, evicted the oldest to
// stay under a row cap, and the re-wrap -- which reads only GridHistory --
// could never get them back. A resize drag is hundreds of passes, so the
// history ground itself away: measured at 107973 cells falling to 54757, and
// eventually to nothing (docs/TERMINAL_CONPTY_FINDINGS.md 6.11).
//
// Counted in logical lines, the same text is the same size at every width, so
// dragging a window edge evicts nothing at all. The hard row ceiling is
// deliberately far above any width's expansion of maxGridHistoryLines: it is
// there for a pathological width, not as a working limit.
const (
	maxGridHistoryLines    = 2000
	maxGridHistoryRowsHard = 20000
)

// OnAltScreen reports whether the alternate screen is active, under the
// view's mutex: the read loop asks, and the parser on the UI goroutine
// writes it.
func (tv *TerminalView) OnAltScreen() bool {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return tv.UseAltScreen
}

// Size reports the grid's size under the view's mutex. The read loop needs
// it for log lines and must not hold the mutex across parsing, and Resize on
// the UI goroutine writes the fields it reads: an unlocked read there is a
// data race the detector catches on every corner-drag test.
func (tv *TerminalView) Size() (int, int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return tv.Width, tv.Height
}

func (tv *TerminalView) Resize(w, h int) {
	if tv.Width == w && tv.Height == h {
		return
	}

	tv.mu.Lock()
	defer tv.mu.Unlock()

	// Resizing changes the mapping between screen coordinates and grid cells;
	// retaining the old selection is what lets it spill into a new layout.
	tv.SelActive = false
	tv.gravityLow = 0

	tv.Engine.SetWidth(w)

	// A width change re-wraps the primary screen when the session delivers
	// long lines whole (view_reflow.go). A height-only change does not: the
	// rows keep their contents, and the path below moves them between the
	// viewport and GridHistory, which keeps the vertical "accordion"
	// behaviour lossless.
	if tv.reflow && !tv.UseAltScreen && w != tv.Width && w > 0 && h > 0 && tv.Width > 0 && tv.Height > 0 {
		tv.reflowResizeLocked(w, h)
		return
	}

	// The branch that does *not* re-wrap: the re-wrap is off, or only the
	// height changed. It moves rows between the viewport and history by
	// hand, so it reports when it actually moves any.
	historyBefore := len(tv.GridHistory)
	defer func() {
		if len(tv.GridHistory) != historyBefore {
			vtui.DebugLog("REFLOW_RESIZE: no re-wrap; %dx%d -> %dx%d; history %d -> %d rows",
				tv.Width, tv.Height, w, h, historyBefore, len(tv.GridHistory))
		}
	}()

	makeBuf := func() [][]vtui.CharInfo {
		b := make([][]vtui.CharInfo, h)
		for i := range b {
			b[i] = make([]vtui.CharInfo, w)
			for j := range b[i] {
				b[i][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
			}
		}
		return b
	}

	newLines := makeBuf()
	newAltLines := makeBuf()
	newWrap := make([]bool, h)

	// 1. Сохраняем основной экран (Primary Screen).
	yOffset := 0
	yShift := 0

	if !tv.UseAltScreen {
		if h < tv.Height {
			lostRows := tv.Height - h
			for y := 0; y < lostRows; y++ {
				if tv.rowHasText(y) {
					tv.pushRowToGridHistory(y)
				}
			}
			yOffset = lostRows
		} else if h > tv.Height {
			yShift = h - tv.Height
			pullCount := yShift
			if pullCount > len(tv.GridHistory) {
				pullCount = len(tv.GridHistory)
			}

			startIdx := len(tv.GridHistory) - pullCount
			dstStart := yShift - pullCount

			// Возвращаем строки из GridHistory обратно на экран
			for i := 0; i < pullCount; i++ {
				dstY := dstStart + i
				srcLine := tv.GridHistory[startIdx+i]
				copyLen := w
				if len(srcLine) > copyLen {
					copyLen = len(srcLine) // Horizontal Preservation
				}
				newLines[dstY] = make([]vtui.CharInfo, copyLen)
				copy(newLines[dstY], srcLine)
				for j := len(srcLine); j < copyLen; j++ {
					newLines[dstY][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
				}
				newWrap[dstY] = tv.GridHistoryWrap[startIdx+i]
			}
			tv.GridHistory = tv.GridHistory[:startIdx]
			tv.GridHistoryWrap = tv.GridHistoryWrap[:startIdx]

			// Заполняем пустоты сверху
			for dstY := 0; dstY < dstStart; dstY++ {
				newLines[dstY] = make([]vtui.CharInfo, w)
				for j := 0; j < w; j++ {
					newLines[dstY][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
				}
			}
		}
	}

	// Копируем видимые строки в новую сетку, сохраняя данные, вышедшие за пределы ширины окна
	for dstY := yShift; dstY < h; dstY++ {
		srcY := dstY - yShift + yOffset
		if srcY >= 0 && srcY < tv.Height {
			srcLine := tv.Lines[srcY]
			copyLen := w
			if len(srcLine) > copyLen {
				copyLen = len(srcLine) // Horizontal Preservation
			}
			newLines[dstY] = make([]vtui.CharInfo, copyLen)
			copy(newLines[dstY], srcLine)
			for j := len(srcLine); j < copyLen; j++ {
				newLines[dstY][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
			}
			newWrap[dstY] = tv.WrapFlags[srcY]
		} else {
			newLines[dstY] = make([]vtui.CharInfo, w)
			for j := 0; j < w; j++ {
				newLines[dstY][j] = vtui.CharInfo{Char: ' ', Attributes: DefaultTermAttr}
			}
		}
	}

	// 2. Сохраняем содержимое AltScreen (для TUI приложений типа nano/mc).
	minH := h
	if tv.Height < minH {
		minH = tv.Height
	}
	for y := 0; y < minH; y++ {
		copyLen := w
		if tv.Width < w {
			copyLen = tv.Width
		}
		copy(newAltLines[y][:copyLen], tv.AltLines[y][:copyLen])
	}

	tv.Lines = newLines
	tv.AltLines = newAltLines
	tv.WrapFlags = newWrap

	tv.Width = w
	tv.Height = h
	tv.ScrollTop = 0
	tv.ScrollBottom = h - 1

	// The pictures follow the text through the reflow, and then take
	// whatever room the new size gives them.
	tv.kittyResizePlacements(yShift-yOffset, h)
	tv.kittyRecomputeSpans()

	if !tv.UseAltScreen {
		tv.CursorY = tv.CursorY - yOffset + yShift
		if tv.CursorY < 0 {
			tv.CursorY = 0
		}
		if tv.CursorY >= h {
			tv.CursorY = h - 1
		}
	} else {
		if tv.CursorY >= h {
			tv.CursorY = h - 1
		}
	}

	if tv.CursorX >= w {
		tv.CursorX = w - 1
	}
	tv.lastCharWasCR = (tv.CursorX == 0)
}

// unwrapLocked flattens the primary screen into logical lines, joining rows
// that a soft wrap split. It reaches back into GridHistory for the rows that
// wrap into the top of the viewport, so that a line broken across the boundary
// is re-wrapped as the single line it is.
//
// The cursor is carried as an offset inside its logical line rather than as a
// pair of coordinates. Recomputing (x, y) arithmetically is what makes a live
// reflow desync from the shell; pinning the cursor to a position in the text
// survives the relayout, and the shell redraws its prompt on SIGWINCH anyway.
// trimGridHistoryLocked enforces the history bound, evicting whole logical
// lines rather than rows.
//
// Evicting a single row would strand the rest of its wrapped line, which is
// worse than dropping the line: the re-wrap would join the fragment to
// whatever now precedes it. So each eviction removes a complete logical line,
// leading rows first, and stops as soon as the bound is met.
// extrusionsLogged bounds the per-row extrusion log: a long run extrudes
// thousands of rows and one line each would bury everything else.
var extrusionsLogged int

func (tv *TerminalView) trimGridHistoryLocked() {
	for tv.historyLogicalLinesLocked() > maxGridHistoryLines ||
		len(tv.GridHistory) > maxGridHistoryRowsHard {
		evicted := 0
		for len(tv.GridHistory) > 0 {
			wrapped := tv.GridHistoryWrap[0]
			if extrusionsLogged < 5 {
				extrusionsLogged++
				vtui.DebugLog("REFLOW_DROP: history is over its %d-line bound; extruding %q to the PieceTable",
					maxGridHistoryLines, clipRowText(tv.GridHistory[0]))
			}
			tv.extrudeGridHistoryRow(0)
			tv.GridHistory = tv.GridHistory[1:]
			tv.GridHistoryWrap = tv.GridHistoryWrap[1:]
			evicted++
			if !wrapped {
				// That row ended a logical line: one whole line is gone.
				break
			}
		}
		if evicted == 0 {
			return
		}
	}
}

// historyLogicalLinesLocked counts the logical lines in GridHistory: a row
// whose wrap flag is false ends one. A trailing run of wrapped rows is a line
// still being written, and counts as one.
func (tv *TerminalView) historyLogicalLinesLocked() int {
	n := 0
	for _, wrapped := range tv.GridHistoryWrap {
		if !wrapped {
			n++
		}
	}
	// A trailing run of wrapped rows is a line still being written; it has no
	// terminating row yet, so count it once.
	if len(tv.GridHistoryWrap) > 0 && tv.GridHistoryWrap[len(tv.GridHistoryWrap)-1] {
		n++
	}
	return n
}

// clipRowText renders a row's text for a log line, short enough to read.
func clipRowText(row []vtui.CharInfo) string {
	var b []rune
	for _, c := range row {
		if c.Char != 0 {
			b = append(b, rune(c.Char))
		}
		if len(b) >= 60 {
			break
		}
	}
	return strings.TrimRight(string(b), " ")
}

// ResetKeyboardProtocols turns off the keyboard encodings a shell may have
// switched on with DECSET, so that the next shell is typed to in plain VT.
//
// These modes belong to the program that asked for them, but one TerminalView
// serves every shell in the panel, so they outlive it. A far2l that dies
// without resetting them leaves f4 encoding every later keystroke as a win32
// input event, which the plain shell on the other side prints as text --
// ";0;0;0;0_" and no working Enter.
//
// Call this when a session ends or the panel switches shells, never on a
// timer: a live far2l needs these modes for as long as it runs.
func (tv *TerminalView) ResetKeyboardProtocols() {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	tv.Win32InputMode = false
	tv.KittyFlags.Store(0)
	tv.kittyCommandRunning.Store(false)
	tv.ApplicationCursorKeys = false
}

// KittyEnableDisambiguateSeq is the request a program writes to its own
// stdout to opt into the kitty keyboard protocol's "disambiguate escape
// codes" flag (bit 1 of the flag set): CSI = 1 ; 1 u, mode 1 ("set"),
// replacing whatever flags were active with just that bit. It is the same
// sequence far2l sends on its own when it starts.
//
// f4#128, RUP step 1: rather than only reacting to a nested program's own
// request, f4 feeds this exact sequence through the ordinary ansi parser (the
// one that would parse it out of a real program's output) right after a
// fresh local shell's PTY comes up, before any output of its own has
// arrived. A bare bash/zsh never sends this itself, so without this a plain
// shell session never gets a Ctrl+Tab distinguishable from Tab (see
// ResetKeyboardProtocols and the Ctrl+Tab-forwarding check in
// internal/panel/frame.go) until some nested program (far2l) requests the
// protocol on its own.
//
// Only the disambiguate bit is set, deliberately the narrowest flag the
// protocol offers: it is enough to tell Ctrl+Tab apart from Tab, and it
// leaves the "report event types" / "report all keys" / "report associated
// text" bits off, which otherwise would turn ordinary key-up events and
// plain typing into escape codes a bare shell never asked to parse either.
//
// Known limitation: this only ever changes what TerminalView believes,
// which is accurate for f4's own emulation (nothing here depends on the
// real host terminal that f4 itself runs inside, and there is no risk of a
// false positive from *that* direction -- f4 is both the "sender" and the
// "receiver" of this sequence, in-process). What it cannot promise is that
// whatever the shell is currently running actually understands the
// resulting CSI-u encoding for a chord it did not itself negotiate: a
// bare readline (bash/zsh, or a plain `python3`/`mysql` prompt started
// inside that shell) does not parse kitty's disambiguated Ctrl+<letter>
// codes, so those specific chords can misbehave in such a program for as
// long as nothing else has overridden these flags. There is no
// confirmation/query round trip here to gate on -- building one (and,
// longer term, scoping the flag to only be active while the shell itself
// is at its prompt, the way kitty's own shell integration pushes and pops
// it around running a foreign command) is left to a later step.
const KittyEnableDisambiguateSeq = "\x1b[=1;1u"

func (tv *TerminalView) IsModal() bool         { return false }
func (tv *TerminalView) RequestFocus() bool    { return true }
func (tv *TerminalView) Close()                {}
func (tv *TerminalView) GetWindowNumber() int  { return 0 }
func (tv *TerminalView) SetWindowNumber(n int) {}

func (tv *TerminalView) HandleFar2lAPC(s string) {
	// vtui.DebugLog("TERM_APC: Incoming Far2l sequence: %q", s)
	// Robustness: skip any garbage before the actual marker
	idx := strings.Index(s, "far2l")
	if idx == -1 {
		return
	}
	s = s[idx:]

	if s == "far2l1" {
		if tv.Pty != nil {
			_, _ = tv.Pty.Write([]byte("\x1b_far2lok\x07"))
		}
	} else if s == "far2l0" {
		// Switching the extensions off revokes every drop offer (§ 12 of
		// the DnD specification).
		tv.dndReset()
	} else if s == "far2lok" {
		// Acknowledgement from the host terminal. This is not for the internal shell to process visually.
		// Consume and do nothing.
	} else if strings.HasPrefix(s, "far2l:") {
		b64 := s[6:]
		if m := len(b64) % 4; m != 0 {
			b64 += strings.Repeat("=", 4-m)
		}
		// A DnD request is recognised by its last bytes and checked against
		// its frame limit before the rest is decoded; its replies keep the
		// order of the requests. The wire length counts ESC _ and a BEL.
		if rid, cmd, ok := dndPeek(b64); ok && cmd == far2ldnd.InteractDND {
			tv.dndAccept(rid, len("\x1b_")+len(s)+1, b64)
			return
		}
		decoded, _ := base64.StdEncoding.DecodeString(b64)
		if len(decoded) > 0 {
			go tv.ProcessFar2lInteract(decoded)
		}
	}
}

// CellSize reports the pixel size of one character cell as the host renderer
// last told us, falling back to the size the terminal advertises when nobody
// knows better.
func (tv *TerminalView) CellSize() (int, int) {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return tv.cellSizeUnsafe()
}

func (tv *TerminalView) cellSizeUnsafe() (int, int) {
	cw, ch := tv.CellW, tv.CellH
	if cw <= 0 {
		cw = kittyFallbackCellW
	}
	if ch <= 0 {
		ch = kittyFallbackCellH
	}
	return cw, ch
}

// syncPtyPixelSize tells the child how large its terminal is in pixels. The
// caller holds the lock.
func (tv *TerminalView) syncPtyPixelSize() {
	if tv.Pty == nil {
		return
	}
	sizer, ok := tv.Pty.(PtyPixelSizer)
	if !ok {
		return
	}
	cw, ch := tv.cellSizeUnsafe()
	sizer.SetSizePixels(tv.Width, tv.Height, tv.Width*cw, tv.Height*ch)
}

// kittyGraphics lazily creates the receiver of the kitty graphics protocol:
// a session that never sends an image never pays for one.
func (tv *TerminalView) kittyGraphics() *KittyGraphics {
	tv.mu.Lock()
	kg := tv.kitty
	tv.mu.Unlock()
	if kg != nil {
		return kg
	}

	kg = NewKittyGraphics(func(b []byte) {
		if tv.Pty != nil {
			_, _ = tv.Pty.Write(b)
		}
	})
	// The placement layer is attached before the receiver becomes reachable,
	// so the two locks are never taken at the same time.
	kg.SetDisplay(kittyDisplay{tv})

	tv.mu.Lock()
	if tv.kitty == nil {
		tv.kitty = kg
	}
	kg = tv.kitty
	tv.mu.Unlock()
	return kg
}

// HandleKittyAPC consumes one graphics escape code, without the leading G.
func (tv *TerminalView) HandleKittyAPC(s string) {
	tv.kittyGraphics().Handle(s)
}

// PromptSnapshot is where the cursor stood, and what the shell had printed
// before it, at the moment an OSC 133 mark crossed the parser. The prompt
// text is the run of cells that ends at the cursor, joined across soft-wrapped
// rows, so that a prompt longer than the window still compares whole.
type PromptSnapshot struct {
	Row, Col int
	Text     string
}

// textBeforeCursorLocked returns the text on the cursor's row up to the
// cursor, with the previous rows prepended for as long as they soft-wrap
// into it. Trailing blanks of the row stay out of the answer.
func (tv *TerminalView) textBeforeCursorLocked() string {
	buf := tv.GetBuffer()
	y, x := tv.CursorY, tv.CursorX
	if y < 0 || y >= len(buf) {
		return ""
	}
	if x > len(buf[y]) {
		x = len(buf[y])
	}
	var parts []string
	parts = append(parts, CellsText(buf[y][:x]))
	for y > 0 && !tv.UseAltScreen && y-1 < len(tv.WrapFlags) && tv.WrapFlags[y-1] {
		y--
		parts = append(parts, CellsText(buf[y]))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "")
}

func CellsText(cells []vtui.CharInfo) string {
	var sb strings.Builder
	for _, c := range cells {
		if c.Char == vtui.WideCharFiller || isWrapPad(c) {
			continue
		}
		sb.WriteString(vtui.CellString(c.Char))
	}
	return sb.String()
}

// PromptSnapshot captures the cursor and the text in front of it.
func (tv *TerminalView) PromptSnapshot() PromptSnapshot {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return PromptSnapshot{Row: tv.CursorY, Col: tv.CursorX, Text: tv.textBeforeCursorLocked()}
}

func (tv *TerminalView) HandleOSC133(payload string) {
	vtui.DebugLog("TERM_OSC133: %s", payload)
	if tv.OnShellMark != nil {
		mark, _, _ := strings.Cut(payload, ";")
		tv.OnShellMark(mark, tv.PromptSnapshot())
	}
	if payload == "C" {
		// The flags now in force are the shell's own (f4 seeds them for a fresh
		// local shell, KittyEnableDisambiguateSeq). A command the shell starts
		// does not speak the protocol, so Ctrl+C must reach it as 0x03, not as
		// CSI 99;5u (f4#1693); a program that wants the protocol asks for it
		// itself after this point.
		if !tv.kittyCommandRunning.Swap(true) {
			tv.kittyBeforeCommand.Store(tv.KittyFlags.Swap(0))
		}
		tv.SetMuted(false)
		if tv.OnBusyChange != nil {
			tv.OnBusyChange(true)
		}
	} else if payload == "D" || strings.HasPrefix(payload, "D;") {
		// Whatever the command left switched on is dropped, and the shell's
		// own flags come back for its prompt.
		if tv.kittyCommandRunning.Swap(false) {
			tv.KittyFlags.Store(tv.kittyBeforeCommand.Load())
		}
		tv.EnsureFreshPromptLine()
		if tv.OnBusyChange != nil {
			tv.OnBusyChange(false)
		}
	}
}

// SetPromptOverlaysLastRow tells the view whether f4's own command line is
// painted over the grid's bottom row. The layout knows; the view has to,
// because the shell prompt is what that row is expected to hold.
func (tv *TerminalView) SetPromptOverlaysLastRow(overlays bool) {
	tv.mu.Lock()
	tv.promptOverlaysLastRow = overlays
	tv.mu.Unlock()
}

// EnsureFreshPromptLine opens a new line for the prompt if the command that
// just finished left the cursor mid-row.
//
// A command whose output does not end in a newline -- `cat` on a file with no
// trailing newline, `printf foo`, `echo -n` -- leaves the cursor after the
// last character it printed, and the shell appends its next prompt right
// there. That row is the one f4 covers with its own command line to hide the
// native prompt, so the tail of the output disappears with it: issue #863,
// where a two-line file showed only its first line while `cat file > out`
// wrote both.
//
// zsh solves the same problem for ordinary terminals with its PROMPT_SP
// filler, printing an inverse marker and a carriage return so the prompt
// starts clean. f4 *is* the terminal, so it can simply open the line itself,
// at the D marker its own wrapper emits, before the prompt bytes arrive. The
// grid then holds the output on one row and the prompt on the next, which is
// exactly what the overlay expects.
func (tv *TerminalView) EnsureFreshPromptLine() {
	tv.mu.Lock()
	skip := tv.Muted || tv.UseAltScreen || !tv.promptOverlaysLastRow || tv.CursorX == 0
	tv.mu.Unlock()
	if skip {
		return
	}
	tv.NextLine()
}
func (tv *TerminalView) ProcessFar2lInteract(data []byte) {
	if n := len(data); n >= 2 && data[n-2] == far2ldnd.InteractDND {
		tv.dndServe(data)
		return
	}
	stk := (*vtinput.Far2lStack)(&data)
	id := stk.PopU8()
	cmd := stk.PopU8()
	// vtui.DebugLog("TERM_APC: ProcessFar2lInteract: cmd=%c, id=%d", cmd, id)

	reply := vtinput.Far2lStack{}

	switch cmd {
	case 'c': // Clipboard
		sub := stk.PopU8()
		// vtui.DebugLog("TERM_APC: Clipboard sub-command: %c", sub)
		switch sub {
		case 'o':
			clientID := stk.PopString()
			tv.mu.Lock()
			auth, cached := tv.authCache[clientID]
			tv.mu.Unlock()

			if !cached {
				if vtui.GlobalClipboardAccessManager != nil {
					auth = vtui.GlobalClipboardAccessManager.Authorize(clientID)
					if auth != 0 {
						tv.mu.Lock()
						tv.authCache[clientID] = auth
						tv.mu.Unlock()
					}
				}
			}

			respAuth := auth
			if auth == -1 {
				respAuth = 1 // Tell child success, we'll handle it locally
			}
			reply.PushU64(2) // FARTTY_FEATCLIP_CHUNKED_SET
			reply.PushU8(uint8(respAuth))
		case 'c':
			tv.mu.Lock()
			tv.clipboardChunks = nil
			tv.mu.Unlock()
			reply.PushU8(1)
		case 'e':
			if tv.ClipboardWriter != nil {
				tv.ClipboardWriter("")
			} else {
				SetF4Clipboard("")
			}
			tv.mu.Lock()
			tv.clipboardChunks = nil
			tv.mu.Unlock()
			reply.PushU8(1)
		case 'a':
			_ = stk.PopU32() // fmt
			reply.PushU8(1)
		case 'S':
			size := stk.PopU16()
			tv.mu.Lock()
			if size == 0 {
				tv.clipboardChunks = nil
			} else {
				chunk := stk.PopBytes(int(size) << 8)
				tv.clipboardChunks = append(tv.clipboardChunks, chunk...)
			}
			tv.mu.Unlock()
		case 's':
			_ = stk.PopU32() // fmt
			len := stk.PopU32()
			textBytes := stk.PopBytes(int(len))
			tv.mu.Lock()
			fullData := append(tv.clipboardChunks, textBytes...)
			tv.clipboardChunks = nil
			tv.mu.Unlock()
			tv.writeClipboard(string(fullData))
			// Guest expects: dataID (U64) + status (U8)
			reply.PushU64(0)
			reply.PushU8(1)
		case 'g':
			_ = stk.PopU32() // fmt
			clipData := tv.ReadClipboard()
			if len(clipData) > 64*1024 {
				clipData = clipData[:64*1024]
			}
			// Guest expects: dataID (U64) + data (Bytes) + length (U32)
			reply.PushU64(0)
			reply.PushBytes([]byte(clipData))
			// #nosec G115 -- clipData is capped to 64 KiB immediately above.
			reply.PushU32(uint32(len(clipData)))
		case 'i':
			_ = stk.PopU32()
			reply.PushU64(0)
		case 'r':
			_ = stk.PopString()
			reply.PushU32(0xC000)
		}
	case 'w': // Window size
		reply.PushU16(ptyPixels(tv.Height))
		reply.PushU16(ptyPixels(tv.Width))
	case 'h': // Cursor height
		_ = stk.PopU8()
	case 'n': // Desktop notification
		text := stk.PopString()
		title := stk.PopString()
		vtui.FrameManager.PostTask(func() {
			toast.Show(title+": "+text, 3*time.Second)
		})
	case 'f': // FKey titles
		for i := 0; i < 12; i++ {
			state := stk.PopU8()
			if state != 0 {
				_ = stk.PopString() // Just pop, we can ignore it for now or implement KeyBar update
			}
		}
		reply.PushU8(1)
	case 'x': // Extra features
		feats := stk.PopU64()
		if feats&2 != 0 { // FARTTY_FEAT_TERMINAL_SIZE
			tv.SendFar2lTerminalSize()
		}
	case 'p': // Palette info
		reply.PushU8(0)  // reserved
		reply.PushU8(24) // bits
	case 'i': // FARTTY_INTERACT_IMAGE, see far2l_image.go
		tv.handleFar2lImage(stk, &reply)
	}

	if len(reply) > 0 || id != 0 {
		reply.PushU8(id)
		b64 := base64.StdEncoding.EncodeToString(reply)
		if tv.Pty != nil {
			// Reply from terminal to app MUST NOT have a colon after 'far2l'.
			// The colon is used as a discriminator: 'far2l:' indicates a Request,
			// while 'far2l' (without colon) indicates a reply.
			_, _ = tv.Pty.Write([]byte("\x1b_far2l" + b64 + "\x07"))
		}
	}
}

func (tv *TerminalView) SendFar2lTerminalSize() {
	stk := vtinput.Far2lStack{}
	stk.PushU16(ptyPixels(tv.Height))
	stk.PushU16(ptyPixels(tv.Width))
	stk.PushU8('S')
	b64 := base64.StdEncoding.EncodeToString(stk)
	if tv.Pty != nil {
		_, _ = tv.Pty.Write([]byte("\x1b_f2l:" + b64 + "\x07"))
	}
}
