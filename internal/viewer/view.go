package viewer

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/numeric"
	"github.com/unxed/f4/internal/piecetable"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// ViewerView is a high-performance file viewer component.
type ViewerView struct {
	// Syntax highlighting (see highlight.go): the colorizer, created once,
	// and the window last handed to it.
	highlight      WindowColorizer
	highlightTried bool
	highlightKey   uint64

	vtui.BaseFrame
	TopBar  *TopBar
	menuBar *vtui.MenuBar
	Backend *ViewerBackend
	VFS     vfs.VFS
	Path    string

	HexMode bool
	// HexAuto records that hex mode came from the binary check rather than
	// from the user. Only an automatic verdict may be revisited when the
	// codepage changes: a hex view the user asked for has to survive an F8.
	HexAuto    bool
	DecodeMode bool
	WrapMode   bool
	// AnsiMode draws the colour sequences of terminal output (SGR) as colours
	// instead of showing them as text (f4#1705); see ansi.go.
	AnsiMode bool
	// DisasmMode is the processor mode the decode view disassembles in:
	// 16, 32 or 64, or 0 while undecided. See disasm.go.
	DisasmMode int
	TopOffset  int64 // Current byte offset of the first visible line

	// For Text mode: offsets of lines currently on screen
	lineOffsets         []int64
	rowCells            []vtui.CharInfo
	visibleURLRows      [][]UrlCellRange
	hoverURL            string
	eofVisible          bool
	lastKnownSize       int64
	LastSearch          string
	LastSearchOffset    int64
	LastSearchTopOffset int64
	LastSearchFound     bool
	LastSearchMatchLen  int64
	LastSearchCase      bool
	LastSearchReverse   bool
	LastSearchRegexp    bool
	LastSearchWholeWord bool

	ScrollBar *vtui.ScrollBar

	// tailStop closes when the viewer stops watching the file for changes.
	// Nil means nothing is watching -- a ViewerView built directly, as the
	// tests do, never starts the poll.
	tailStop chan struct{}

	OnClose  func()
	Codepage int
}

func NewViewerView(ctx context.Context, v vfs.VFS, path string) (*ViewerView, error) {
	f, err := v.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	header, err := viewerDetectionHeader(ctx, f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	cpID := vfs.DetectEncoding(header, config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage)
	if remembered, ok := fileops.RememberedCodepage(v, path); ok {
		cpID = remembered
	}
	binary := viewerHeaderLooksBinary(header, cpID)
	if binary {
		// Binary data has no text codepage to materialize. Keeping the remote
		// handle lets the hex viewer fetch only its small visible windows.
		cpID = 65001
	}
	DataOffset := int64(0)
	if cpID == 65001 && !binary && vfs.HasUTF8BOM(header) {
		DataOffset = vfs.UTF8BOMSize
	}

	backend, err := newViewerBackend(ctx, v, path, f, cpID, DataOffset)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	vv := &ViewerView{
		Backend:  backend,
		VFS:      v,
		Path:     path,
		HexMode:  binary,
		HexAuto:  binary,
		WrapMode: true,
		// The decode view's processor mode is read off the same header the
		// binary check used, here where the whole header is in hand: the
		// backend serves the decode view through a moving cache window,
		// which need not cover offset 0 by the time the mode is wanted.
		DisasmMode: DetectX86Mode(header),
		Codepage:   cpID,
	}
	vv.ScrollBar = vtui.NewScrollBar(0, 0, 0)
	vv.ScrollBar.ColorIdx = theme.ColViewerScrollbar
	vv.ScrollBar.Attr = func() uint64 {
		return theme.OnTextBackground(theme.ColViewerScrollbar, theme.ColViewerText, vv.textAttr())
	}
	vv.ScrollBar.SetOwner(vv)
	vv.ScrollBar.OnScroll = func(v int) {
		newOff := int64(v)
		if vv.HexMode {
			newOff &= ^int64(0xF)
		} else {
			// Optimization: during fast drag, don't FindLineStart every pixel
			// unless we are close to the target or moving slowly.
			// For now, simple snap.
			newOff = vv.Backend.FindLineStart(newOff)
		}
		if newOff != vv.TopOffset {
			vv.TopOffset = newOff
			vtui.FrameManager.Redraw()
		}
	}
	vv.ScrollBar.OnStep = func(step int) {
		// Used for arrows and track clicks: perform logical steps
		switch step {
		case -1:
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_UP})
		case 1:
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_DOWN})
		case -2:
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_PRIOR})
		case 2:
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vtinput.VK_NEXT})
		}
		vtui.FrameManager.Redraw()
	}
	vv.menuBar = vtui.NewMenuBar(nil)
	vv.TopBar = NewTopBar(
		func() string {
			base := DisplayFileTitle(vv.VFS, vv.Path)
			return " " + base
		},
		func() string {
			percent := 0
			size := vv.Backend.Size()
			if size > 0 {
				viewHeightBytes := int64(vv.Y2 - vv.Y1)
				if vv.HexMode {
					viewHeightBytes *= 16
				} else {
					viewHeightBytes *= 80
				}
				if size <= viewHeightBytes {
					percent = 100
				} else {
					denominator := size - viewHeightBytes
					percent = int((vv.TopOffset * 100) / denominator)
				}
				if percent < 0 {
					percent = 0
				}
				if percent > 100 {
					percent = 100
				}
			}
			mode := i18n.Msg("Viewer.ModeText")
			if vv.DecodeMode {
				mode = DisasmModeLabel(vv.disasmMode())
			} else if vv.HexMode {
				mode = i18n.Msg("Viewer.ModeHex")
			}
			cpName := vfs.DisplayCodepageName(vv.Codepage)
			return fmt.Sprintf(" %s │ %s │ %d%%     ", cpName, mode, percent)
		},
	)
	vv.TopBar.SetVisible(true)
	vv.SetCanFocus(true)
	vv.SetFocus(true)
	vv.startTailWatch()
	return vv, nil
}

// viewerTailPollInterval is how often an open viewer looks at the file it is
// showing to see whether it changed. tail -f sleeps a second between looks;
// half of that keeps a log on screen feeling live without the poll itself
// becoming the workload.
const viewerTailPollInterval = 500 * time.Millisecond

// startTailWatch begins watching the file for changes. What it costs is one
// re-measure of an already-open handle per tick, and on a file system whose
// handles cannot do that -- a remote one -- it costs nothing at all, because
// ViewerBackend.Refresh is then a no-op. Nothing is read, and nothing is
// redrawn, until the file actually moves.
func (vv *ViewerView) startTailWatch() {
	if vv.tailStop != nil {
		return
	}
	stop := make(chan struct{})
	vv.tailStop = stop

	// Read the frame manager here, on the goroutine that starts the poll: the
	// poll outlives this call, and reading the global from inside it races
	// anything that reassigns vtui.FrameManager while it is still running.
	frames := vtui.FrameManager
	go func() {
		ticker := time.NewTicker(viewerTailPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				frames.PostTask(func() {
					// The viewer may have closed between the tick and this
					// task reaching the UI thread.
					if vv.tailStop == stop {
						vv.refreshFromFile()
					}
				})
			}
		}
	}()
}

// stopTailWatch puts the poll away. Closing the channel is what the goroutine
// is waiting on, so it stops at once rather than at the end of the interval,
// and a closed viewer leaves nothing running behind it.
func (vv *ViewerView) stopTailWatch() {
	if vv.tailStop == nil {
		return
	}
	close(vv.tailStop)
	vv.tailStop = nil
}

// refreshFromFile re-measures the file and redraws when it moved. A viewer
// sitting at the end of the file follows it, and one parked further up stays
// exactly where the reader left it and only gets an honest scrollbar and
// percentage.
//
// Either way the refresh dropped the window cache, so the rows on screen have
// to be fetched again before they can be painted. The viewer is held unpainted
// until they are (holdUntilCached): a plain Redraw painted the frame in
// between, with the text replaced by "[ Loading... ]", once per growth of the
// file -- a log being written to made the whole viewer blink about once a
// second while it was read from the top (#1624).
func (vv *ViewerView) refreshFromFile() {
	if vv.Backend == nil || vv.Busy {
		return
	}
	before := vv.Backend.Size()
	if !vv.Backend.Refresh(context.Background()) {
		return
	}
	size := vv.Backend.Size()
	if vv.TopOffset > size {
		// The file was truncated or rotated away under the viewport, and the
		// offset it was showing no longer exists.
		vv.TopOffset = 0
		vv.lastKnownSize = size
		vv.eofVisible = false
	} else if vv.eofVisible && size > before {
		vv.followTail()
		return
	}
	vv.holdUntilCached(vv.TopOffset)
}

// followTail moves a viewer that was showing the end of the file to the file's
// new end, and does it before the viewer is painted again.
//
// This runs from the poll, on the UI thread, between two frames. The frame
// manager asks the top frame IsBusy before it paints anything, and skips the
// whole frame while it is: so everything the move needs -- laying out the
// last rows in the background, fetching the tail window the refresh just
// dropped -- happens with the old tail still on screen, and the next frame
// painted is the new tail.
//
// Following used to be started from DisplayObject instead, in the middle of a
// frame the frame manager had already begun: the desktop under the viewer was
// painted, the viewer returned without painting, and every growth of the file
// put one empty viewer on screen before the new tail (#428). In hex mode the
// frame after that also showed "Loading..." while the dropped window came
// back.
func (vv *ViewerView) followTail() {
	vv.jumpToEnd()
	if vv.Busy {
		// Text layout runs in the background and repaints when it is done.
		return
	}
	// Hex mode places the viewport synchronously, but the bytes under it were
	// dropped from the cache by the refresh that noticed the growth.
	vv.holdUntilCached(vv.TopOffset)
}

// holdUntilCached keeps the viewer busy -- and so unpainted, while it is the
// top frame -- until the backend has the bytes at off, then repaints. When
// they are already there it only repaints.
func (vv *ViewerView) holdUntilCached(off int64) {
	backend := vv.Backend
	if _, err := backend.ReadAt(off, 1); err != piecetable.ErrLoading {
		vtui.FrameManager.Redraw()
		return
	}
	vv.Busy = true
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		defer ctx.RunOnUI(func() {
			vv.Busy = false
			vtui.FrameManager.Redraw()
		})
		// A closed viewer cancels the backend's context, and its fetches
		// then never land; stop waiting for them.
		for ctx.Err() == nil && backend.ctx.Err() == nil {
			if _, err := backend.ReadAt(off, 1); err != piecetable.ErrLoading {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

// Reload rereads the file on demand. Unlike the poll it drops the window cache
// even when the length did not change, so a file rewritten in place -- same
// size, different bytes -- also shows its new contents. Like the poll, it keeps
// the old contents on screen until the new ones are there to paint.
func (vv *ViewerView) Reload() {
	if vv.Backend == nil {
		return
	}
	vv.Backend.Refresh(context.Background())
	vv.Backend.DropCache()
	if size := vv.Backend.Size(); vv.TopOffset > size {
		vv.TopOffset = 0
		vv.eofVisible = false
	}
	if vv.eofVisible {
		vv.followTail()
		return
	}
	vv.holdUntilCached(vv.TopOffset)
}

// viewerDetectionHeader reads the prefix every codepage decision is made on.
// One helper so that opening a file, switching its codepage and going back to
// auto-detect all look at exactly the same bytes.
func viewerDetectionHeader(ctx context.Context, f vfs.ReadAtCloser) ([]byte, error) {
	size := f.Size()
	detectLen := 16 * 1024
	if int64(detectLen) > size {
		detectLen = int(size)
	}
	header := make([]byte, detectLen)
	n, err := f.ReadAt(ctx, header, 0)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read file header: %w", err)
	}
	return header[:n], nil
}

func viewerHeaderLooksBinary(header []byte, cpID int) bool {
	decoded := header
	if cpID != 65001 {
		if converted, err := vfs.DecodeBytes(header, cpID); err == nil {
			decoded = converted
		}
	}
	return LooksBinary(decoded)
}

func (vv *ViewerView) SetPosition(x1, y1, x2, y2 int) {
	vv.ScreenObject.SetPosition(x1, y1, x2, y2)
	if vv.TopBar != nil {
		vv.TopBar.SetPosition(x1, y1, x2, y1)
	}
	if vv.menuBar != nil {
		vv.menuBar.SetPosition(x1, y1, x2, y1)
	}
	if vv.ScrollBar != nil {
		vv.ScrollBar.SetPosition(x2, y1+1, x2, y2)
	}
}

// GetMenuBar returns the viewer's menu bar. Items are regenerated from
// the action registry on every call, so shortcuts and toggle states are
// always current.
func (vv *ViewerView) GetMenuBar() *vtui.MenuBar {
	if App != nil {
		vv.menuBar.Items = App.MenuBarItems("Viewer")
	}
	if !vv.menuBar.Active {
		// As in the editor: far2l's ViewerShellOptions opens File on every
		// F9, and vtui's F9 fallback opens whatever SelectPos holds (#1144).
		vv.menuBar.SelectPos = 0
	}
	return vv.menuBar
}

func (vv *ViewerView) HandleCommand(cmd int, args any) bool {
	if cmd == vtui.CmClose {
		vv.Close()
		return true
	}
	if App != nil && App.HandleCommand(vv, cmd, args) {
		return true
	}
	return vv.BaseFrame.HandleCommand(cmd, args)
}

func (vv *ViewerView) Show(scr *vtui.ScreenBuf) {
	vv.ScreenObject.Show(scr)
	if vv.TopBar != nil {
		vv.TopBar.Show(scr)
	}
	if vv.menuBarPinned() {
		vv.GetMenuBar().Show(scr)
	}
	vv.DisplayObject(scr)
}

func (vv *ViewerView) DisplayObject(scr *vtui.ScreenBuf) {
	if !vv.IsVisible() {
		return
	}

	// A size that moved without going through the poll -- a handle whose
	// Size changes on its own -- is only noticed here. The frame manager has
	// already painted what lies under the viewer by now, so the frame is
	// painted in full first and the jump comes after it: returning without
	// painting puts an empty viewer on screen. The poll does not come through
	// here; it follows before the frame starts, see followTail.
	currentSize := vv.Backend.Size()
	if vv.eofVisible && currentSize > vv.lastKnownSize && !vv.Busy {
		defer vv.followTail()
	}
	vv.lastKnownSize = currentSize

	width := vv.X2 - vv.X1 + 1
	if vv.ScrollBar != nil {
		width-- // Не рисуем текст поверх скроллбара
	}
	height := vv.Y2 - vv.Y1 + 1
	contentHeight := height - 1

	bgAttr := vv.textAttr()

	// 1. Draw Background
	scr.FillRect(vv.X1, vv.Y1+1, vv.X2, vv.Y2, ' ', bgAttr)

	if vv.Busy {
		scr.Write(vv.X1, vv.Y1+1, vtui.StringToCharInfo(" [ Loading... ] ", bgAttr))
		return
	}

	if contentHeight > 0 {
		if vv.DecodeMode {
			vv.renderDecode(scr, width, contentHeight)
		} else if vv.HexMode {
			vv.renderHex(scr, width, contentHeight)
		} else {
			vv.renderText(scr, width, contentHeight)
		}
	}

	if vv.ScrollBar != nil && vv.Backend.Size() > 0 {
		maxOffset := int(vv.Backend.Size())
		if vv.HexMode {
			contentHeight := vv.Y2 - vv.Y1
			if contentHeight > 0 {
				lastLineOffset := int((vv.Backend.Size() - 1) &^ 0xF)
				maxOffset = lastLineOffset - (contentHeight-1)*16
				if maxOffset < 0 {
					maxOffset = 0
				}
			}
		}
		vv.ScrollBar.SetParams(int(vv.TopOffset), 0, maxOffset)
		vv.ScrollBar.Show(scr)
	}
}

func (vv *ViewerView) renderHex(scr *vtui.ScreenBuf, width, contentHeight int) {
	attr := vtui.Palette[theme.ColViewerText]
	offAttr := vtui.Palette[theme.ColViewerArrows]

	currOffset := vv.TopOffset &^ 0xF // Align to 16 bytes
	//lastRowWasEOF := false

	for y := 0; y < contentHeight; y++ {
		if currOffset >= vv.Backend.Size() {
			//lastRowWasEOF = true
			break
		}

		data, err := vv.Backend.ReadAt(currOffset, 16)
		if err == piecetable.ErrLoading {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", attr))
			break
		}
		if err != nil {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf(" [ Error: %v ] ", err), attr))
			break
		}

		line := fmt.Sprintf("%010X: ", currOffset)
		scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(line, offAttr))

		// Hex part
		hexStr := ""
		for i := 0; i < 16; i++ {
			if i < len(data) {
				hexStr += fmt.Sprintf("%02X ", data[i])
			} else {
				hexStr += "   "
			}
			if i == 7 {
				hexStr += " "
			}
		}
		scr.Write(vv.X1+12, vv.Y1+1+y, vtui.StringToCharInfo(hexStr, attr))

		// ASCII part
		asciiStr := "│ "
		for i := 0; i < len(data); i++ {
			r := rune(data[i])
			if r < 32 || r > 126 {
				r = '.'
			}
			asciiStr += string(r)
		}
		scr.Write(vv.X1+12+50, vv.Y1+1+y, vtui.StringToCharInfo(asciiStr, attr))

		currOffset += 16
	}
	vv.eofVisible = (currOffset >= vv.Backend.Size())
}
func (vv *ViewerView) renderDecode(scr *vtui.ScreenBuf, width, contentHeight int) {
	attr := vtui.Palette[theme.ColViewerText]
	offAttr := vtui.Palette[theme.ColViewerArrows]
	currOffset := vv.TopOffset

	for y := 0; y < contentHeight; y++ {
		if currOffset >= vv.Backend.Size() {
			break
		}

		data, err := vv.Backend.ReadAt(currOffset, DisasmMaxInstLen)
		if err == piecetable.ErrLoading {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", attr))
			break
		}
		if err != nil && len(data) == 0 {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf(" [ Error: %v ] ", err), attr))
			break
		}

		asmStr, instLen := DisasmInstruction(data, vv.disasmMode(), currOffset)

		line := fmt.Sprintf("%010X: ", currOffset)
		scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(line, offAttr))

		hexStr := ""
		for i := 0; i < instLen; i++ {
			hexStr += fmt.Sprintf("%02X ", data[i])
		}
		scr.Write(vv.X1+12, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf("%-24s", hexStr), attr))
		scr.Write(vv.X1+38, vv.Y1+1+y, vtui.StringToCharInfo(asmStr, attr))

		currOffset += int64(instLen)
	}
	vv.eofVisible = (currOffset >= vv.Backend.Size())
}

// disasmMode returns the processor mode the decode view uses. A view built
// without a header (NewViewerView reads one) decides it here, from the
// file's first bytes, the first time an instruction is needed.
func (vv *ViewerView) disasmMode() int {
	if !DisasmModeValid(vv.DisasmMode) {
		header, _ := vv.Backend.ReadAt(0, 1024)
		vv.DisasmMode = DetectX86Mode(header)
	}
	return vv.DisasmMode
}

// CycleDisasmMode switches the decode view to the next processor mode in
// the 64 -> 32 -> 16 -> 64 cycle and returns the mode now in effect.
func (vv *ViewerView) CycleDisasmMode() int {
	vv.DisasmMode = NextDisasmMode(vv.disasmMode())
	return vv.DisasmMode
}

// decodeStep returns how many bytes the instruction at off occupies in the
// current mode: the distance to the next line of the decode view. It is
// zero while the bytes at off are still being fetched.
func (vv *ViewerView) decodeStep(off int64) int64 {
	data, _ := vv.Backend.ReadAt(off, DisasmMaxInstLen)
	return int64(DisasmInstLen(data, vv.disasmMode()))
}

// rowReadSize is how many bytes to read to lay out one screen row. Escape
// sequences take bytes and no room, so an ANSI-mode row needs a longer read.
func (vv *ViewerView) rowReadSize(width int) int {
	if vv.AnsiMode {
		return width * 16
	}
	return width * 4
}

func (vv *ViewerView) renderText(scr *vtui.ScreenBuf, width, contentHeight int) {

	currOffset := vv.TopOffset
	// Highlighting follows logical lines: lineStart is where the line of the
	// current row begins, and every line on screen is collected for the
	// colorizer.
	hl := vv.windowColorizer()
	if vv.AnsiMode {
		// The colours are the file's own.
		hl = nil
	}
	attr := vv.textAttr()
	ansiState := attr
	var hlLines []WindowLine
	hlTexts := map[int64]string{}
	lineStart, hlOK := int64(0), hl != nil
	if hlOK {
		lineStart, hlOK = vv.highlightLineStart(currOffset)
	}
	vv.lineOffsets = vv.lineOffsets[:0]
	vv.visibleURLRows = vv.visibleURLRows[:0]
	//lastRowWasEOF := false

	for y := 0; y < contentHeight; y++ {
		vv.lineOffsets = append(vv.lineOffsets, currOffset)
		if currOffset >= vv.Backend.Size() {
			vv.visibleURLRows = append(vv.visibleURLRows, nil)
			//lastRowWasEOF = true
			break
		}

		// Read a generous chunk to handle wrapping. The row helper keeps
		// combining sequences and script conjuncts atomic.
		data, err := vv.Backend.ReadAt(currOffset, vv.rowReadSize(width))
		if err == piecetable.ErrLoading {
			vv.visibleURLRows = append(vv.visibleURLRows, nil)
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", attr))
			break
		}
		if err != nil {
			vv.visibleURLRows = append(vv.visibleURLRows, nil)
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf(" [ Error: %v ] ", err), attr))
			break
		}
		if len(data) == 0 {
			vv.visibleURLRows = append(vv.visibleURLRows, nil)
			break
		}

		tabSize := 8
		if config.App.EditorTabSize > 0 {
			tabSize = config.App.EditorTabSize
		}
		row := layoutViewerTextRowANSI(data, width, tabSize, vv.WrapMode, vv.AnsiMode)

		// Build []vtui.CharInfo for the line
		var cellByteOffsets []int
		if vv.AnsiMode {
			vv.rowCells, cellByteOffsets = ansiRowCells(data[:row.textLen], attr, &ansiState, tabSize, width)
		} else {
			vv.rowCells, cellByteOffsets = viewerTextCells(string(data[:row.textLen]), attr, tabSize, width)
		}
		if hlOK {
			text, seen := hlTexts[lineStart]
			if !seen {
				if text, hlOK = vv.highlightLineAt(lineStart); hlOK {
					hlTexts[lineStart] = text
					hlLines = append(hlLines, WindowLine{Offset: lineStart, Text: text})
				}
			}
			if attrs := hl.LineAttrs(lineStart, text); hlOK && attrs != nil {
				applyViewerHighlight(vv.rowCells, string(data[:row.textLen]), cellByteOffsets, text, int(currOffset-lineStart), attrs)
			}
		}
		if vv.LastSearchFound && vv.LastSearch != "" {
			matchStart := vv.LastSearchOffset
			matchLen := vv.LastSearchMatchLen
			// Keep manually constructed ViewerViews and old sessions safe: a
			// literal match used to derive its end from the pattern itself.
			if matchLen <= 0 {
				matchLen = int64(len(vv.LastSearch))
			}
			matchEnd := matchStart + matchLen
			rowStart := currOffset
			rowEnd := rowStart + int64(row.textLen)
			if matchStart < rowEnd && matchEnd > rowStart {
				applyViewerSearchAttr(
					vv.rowCells,
					string(data[:row.textLen]),
					cellByteOffsets,
					int(matchStart-rowStart),
					int(matchEnd-rowStart),
					vtui.Palette[theme.ColViewerSelectedText],
				)
			}
		}

		rowLinks := urlCellRanges(string(data[:row.textLen]), cellByteOffsets)
		ApplyURLHoverAttr(vv.rowCells, rowLinks, vv.hoverURL)
		vv.visibleURLRows = append(vv.visibleURLRows, rowLinks)
		scr.Write(vv.X1, vv.Y1+1+y, vv.rowCells)
		currOffset += int64(row.lineLen)

		if !row.foundNewline && !vv.WrapMode {
			// In no-wrap mode, we must consume until the actual newline
			tempOff := currOffset
			for {
				b, err := vv.Backend.ReadAt(tempOff, 1024)
				if err != nil || len(b) == 0 {
					break
				}
				found := false
				for i, char := range b {
					if char == '\n' {
						tempOff += int64(i + 1)
						found = true
						break
					}
				}
				if found {
					break
				}
				tempOff += int64(len(b))
			}
			currOffset = tempOff
		}
		if row.foundNewline || !vv.WrapMode {
			lineStart = currOffset
		}
	}
	vv.eofVisible = (currOffset >= vv.Backend.Size())
	if hlOK && len(hlLines) > 0 {
		if key := highlightWindowKey(hlLines); key != vv.highlightKey {
			vv.highlightKey = key
			hl.Request(vv.highlightLinesBefore(hlLines[0].Offset, viewerHighlightContextLines), hlLines)
		}
	}
}

func (vv *ViewerView) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}

	ctrl := (e.ControlKeyState & (vtinput.LeftCtrlPressed | vtinput.RightCtrlPressed)) != 0
	alt := (e.ControlKeyState & (vtinput.LeftAltPressed | vtinput.RightAltPressed)) != 0
	if e.VirtualKeyCode == vtinput.VK_TAB && ctrl {
		return false
	}

	//height := int64(vv.Y2 - vv.Y1 + 1)
	step := int64(1)
	if vv.HexMode {
		step = 16
	}

	contentHeight := int64(vv.Y2 - vv.Y1) // height - 1 (status line)

	switch e.VirtualKeyCode {
	case vtinput.VK_DOWN:
		if vv.eofVisible {
			return true // Prevent scrolling past End of File
		}
		if vv.DecodeMode {
			vv.TopOffset += vv.decodeStep(vv.TopOffset)
		} else if vv.HexMode {
			if vv.TopOffset+16 < vv.Backend.Size() {
				vv.TopOffset += 16
			}
		} else if len(vv.lineOffsets) > 1 {
			vv.TopOffset = vv.lineOffsets[1]
		} else {
			// Fail-safe: if lineOffsets not populated (e.g. before first render),
			// try to proactively find the next line start from current offset.
			width := vv.X2 - vv.X1 + 1
			if vv.ScrollBar != nil {
				width--
			}
			data, err := vv.Backend.ReadAt(vv.TopOffset, vv.rowReadSize(width))
			if err == nil && len(data) > 0 {
				tabSize := 8
				if config.App.EditorTabSize > 0 {
					tabSize = config.App.EditorTabSize
				}
				row := layoutViewerTextRowANSI(data, width, tabSize, vv.WrapMode, vv.AnsiMode)
				if row.lineLen > 0 {
					vv.TopOffset += int64(row.lineLen)
				}
			}
		}
		return true

	case vtinput.VK_UP:
		if vv.DecodeMode {
			vv.TopOffset -= 1
		} else if vv.HexMode {
			vv.TopOffset -= step
		} else {
			vv.TopOffset = vv.Backend.FindLineStart(vv.TopOffset - 1)
		}
		if vv.TopOffset < 0 {
			vv.TopOffset = 0
		}
		return true

	case vtinput.VK_NEXT: // PgDn
		if vv.DecodeMode {
			for i := 0; i < int(contentHeight); i++ {
				vv.TopOffset += vv.decodeStep(vv.TopOffset)
			}
			if vv.TopOffset >= vv.Backend.Size() {
				vv.TopOffset = vv.Backend.Size() - 1
			}
		} else if vv.HexMode {
			vv.TopOffset += 16 * contentHeight
			if vv.TopOffset >= vv.Backend.Size() {
				vv.TopOffset = (vv.Backend.Size() - 1) &^ 0xF
				if vv.TopOffset < 0 {
					vv.TopOffset = 0
				}
			}
		} else if len(vv.lineOffsets) > 0 {
			vv.TopOffset = vv.lineOffsets[len(vv.lineOffsets)-1]
		}
		return true

	case vtinput.VK_PRIOR: // PgUp
		if vv.DecodeMode {
			vv.TopOffset -= 15 * contentHeight
		} else if vv.HexMode {
			vv.TopOffset -= step * contentHeight
		} else {
			for i := 0; i < int(contentHeight); i++ {
				vv.TopOffset = vv.Backend.FindLineStart(vv.TopOffset - 1)
			}
		}
		if vv.TopOffset < 0 {
			vv.TopOffset = 0
		}
		return true

	case vtinput.VK_HOME:
		vv.TopOffset = 0
		return true

	case vtinput.VK_END:
		vv.jumpToEnd()
		return true

	case vtinput.VK_F8:
		if alt {
			vv.AskGoto()
			return true
		}
	}

	// Injected-event fallback: KeyBar mouse clicks reach ProcessKey via
	// InjectEvents, which skips FrameManager.EventFilter and therefore the
	// hotkey manager. Route them through the same lookup so clicking F2/F5/
	// F7/… on the bottom bar triggers the configured Viewer action.
	if App != nil && App.LookupHotkey(e) {
		return true
	}

	return false
}

// AskGoto prompts for a position. In text mode that is a line number, which
// only means something once someone has counted the newlines; in hex mode it
// is a byte offset, which needs no counting at all.
func (vv *ViewerView) AskGoto() {
	if vv.HexMode || vv.DecodeMode {
		title, prompt := dialog.GotoText("Viewer.GotoOffsetTitle", " Go to offset "), dialog.GotoText("Viewer.GotoOffsetPrompt", "Byte offset:")
		dialog.ShowGotoOffset(vv, title, prompt, vv.TopOffset, func(offset int64) {
			vv.gotoPosition(offset)
		})
		return
	}
	title, prompt := " Go to line ", "Line number:"
	vtui.InputBoxOn(vv, title, prompt, "", func(s string) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil || n < 0 {
			return
		}
		vv.gotoPosition(n)
	})
}

func (vv *ViewerView) gotoPosition(n int64) {
	if vv.HexMode || vv.DecodeMode {
		size := vv.Backend.Size()
		if size == 0 {
			n = 0
		}
		if n >= size {
			n = size - 1
		}
		if n < 0 {
			n = 0
		}
		if vv.HexMode {
			vv.TopOffset = n &^ 0xF
		} else {
			vv.TopOffset = n
		}
		vtui.FrameManager.Redraw()
		return
	}

	// Finding a line can mean a remote round trip or, on a file system that
	// cannot index, a walk over the file, so it does not happen on the UI
	// thread and the user can cancel it.
	vv.Busy = true
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		off, ok := vv.Backend.LineStart(ctx.Context, n)
		ctx.RunOnUI(func() {
			vv.Busy = false
			if !ok {
				if ctx.Err() == nil {
					vtui.ShowMessageOn(vv, " Go to line ",
						fmt.Sprintf("Line %d is past the end of the file.", n), []string{"&Ok"})
				}
				return
			}
			vv.TopOffset = off
			vv.eofVisible = false
			vtui.FrameManager.Redraw()
		})
	})
}
func (vv *ViewerView) jumpToEnd() {
	// End has to mean the end of the file as it is now, not as it was when
	// the viewer opened it. That is what mc does, and it makes End the manual
	// way to catch up with a growing log even where the automatic follow does
	// not apply -- after scrolling up, say, or on a file system whose handles
	// cannot re-measure themselves.
	if vv.Backend != nil {
		vv.Backend.Refresh(context.Background())
		// The end about to be shown is the end as measured now, so this size
		// is known and DisplayObject has nothing left to follow.
		vv.lastKnownSize = vv.Backend.Size()
	}

	contentHeight := int64(vv.Y2 - vv.Y1)
	if vv.HexMode {
		if vv.Backend.Size() == 0 {
			vv.TopOffset = 0
		} else {
			lastLineOffset := (vv.Backend.Size() - 1) &^ 0xF
			vv.TopOffset = lastLineOffset - (contentHeight-1)*16
			if vv.TopOffset < 0 {
				vv.TopOffset = 0
			}
		}
		return
	}

	if vv.Backend.Size() == 0 {
		vv.TopOffset = 0
		return
	}

	// Everything the layout needs from the viewer is read here, on the UI
	// goroutine that starts the task, and not inside it. The viewer's fields
	// belong to the UI goroutine: Close clears ScrollBar, SetPosition moves
	// X1/X2, the wrap toggle flips WrapMode and a codepage switch replaces
	// Backend, all while this layout may still be running. The task used to
	// read them itself, and -race caught it reading ScrollBar after the
	// viewer had been closed under it.
	backend := vv.Backend
	width := vv.X2 - vv.X1 + 1
	if vv.ScrollBar != nil {
		width--
	}
	wrapMode := vv.WrapMode
	ansiMode := vv.AnsiMode
	rowRead := vv.rowReadSize(width)
	tabSize := 8
	if config.App.EditorTabSize > 0 {
		tabSize = config.App.EditorTabSize
	}

	vv.Busy = true
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		defer ctx.RunOnUI(func() { vv.Busy = false })
		// A closed backend -- the viewer closed, or a codepage switch
		// replaced it -- cancels its context, and its fetches then never
		// land: ReadAt keeps answering ErrLoading and both loops below would
		// wait for it forever. Stop instead, as holdUntilCached does.
		stopped := func() bool {
			return ctx.Err() != nil || backend.ctx.Err() != nil
		}

		chunkSize := contentHeight * int64(width) * 4
		if chunkSize < 16*1024 {
			chunkSize = 16 * 1024
		}

		// Ctrl+End must be a random-access operation. A remote line index has
		// to scan the entire file to count its lines; for a binary file that
		// usually yields line 1 at offset 0 and makes the viewer download the
		// whole file as well. One backend cache window is enough to lay out the
		// final screen, including wrapped text, and maps to a bounded FISH+
		// range read regardless of the file size.
		const tailWindow = 192 * 1024
		if chunkSize < tailWindow {
			chunkSize = tailWindow
		}
		startOff := backend.Size() - chunkSize
		if startOff < 0 {
			startOff = 0
		}

		for {
			if stopped() {
				return
			}
			_, err := backend.ReadAt(startOff, 1024)
			if err != piecetable.ErrLoading {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		var offsets []int64
		currOff := startOff

		for currOff < backend.Size() {
			if stopped() {
				return
			}
			data, err := backend.ReadAt(currOff, 64*1024)
			if err == piecetable.ErrLoading {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err != nil || len(data) == 0 {
				break
			}

			scanPos := 0
			for scanPos < len(data) {
				offsets = append(offsets, currOff+int64(scanPos))
				rowData := data[scanPos:]
				if wrapMode {
					maxRowData := rowRead
					if maxRowData < len(rowData) {
						rowData = rowData[:maxRowData]
					}
				}
				row := layoutViewerTextRowANSI(rowData, width, tabSize, wrapMode, ansiMode)
				scanPos += row.lineLen
				if !row.foundNewline && !wrapMode {
					break
				}
				if row.lineLen == 0 {
					break
				}
			}
			currOff += int64(scanPos)
		}

		ctx.RunOnUI(func() {
			if int64(len(offsets)) <= contentHeight {
				vv.TopOffset = startOff
			} else {
				vv.TopOffset = offsets[len(offsets)-int(contentHeight)]
			}
			vtui.FrameManager.Redraw()
		})
	})
}
func (vv *ViewerView) ReloadWithCodepage(cpID int) {
	if vv.Codepage == cpID {
		return
	}

	f, err := vv.VFS.Open(context.Background(), vv.Path)
	if err != nil {
		return
	}

	header, err := viewerDetectionHeader(context.Background(), f)
	if err != nil {
		_ = f.Close()
		return
	}

	hexMode := vv.HexMode
	if vv.HexAuto {
		// The hex view was the binary check's guess, so the codepage the
		// user just picked gets to overturn it. Without this, a file the
		// check misread -- UTF-16 with no byte-order mark, say -- had no way
		// back to text: choosing its codepage relabelled the status bar and
		// changed nothing else on screen.
		hexMode = viewerHeaderLooksBinary(header, cpID)
	}

	backendCP := cpID
	if hexMode {
		// Hex mode displays raw bytes; changing the label must not replace
		// those bytes with a decoded text stream.
		backendCP = 65001
	}
	DataOffset := int64(0)
	if backendCP == 65001 && !hexMode &&
		!viewerHeaderLooksBinary(header, backendCP) && vfs.HasUTF8BOM(header) {
		DataOffset = vfs.UTF8BOMSize
	}
	backend, err := newViewerBackend(context.Background(), vv.VFS, vv.Path, f, backendCP, DataOffset)
	if err != nil {
		_ = f.Close()
		return
	}

	oldBackend := vv.Backend
	oldOffset := vv.TopOffset
	oldSize := int64(0)
	if oldBackend != nil {
		oldSize = oldBackend.Size()
	}
	vv.Backend = backend
	vv.Codepage = cpID
	vv.HexMode = hexMode
	newSize := vv.Backend.Size()
	if newSize <= 0 {
		vv.TopOffset = 0
	} else {
		// TopOffset is an offset in the decoded stream. Its byte density can
		// change when the same raw file is viewed as CP1251, CP866, UTF-8,
		// or UTF-16, so carrying the old value verbatim can put the viewport
		// past EOF. Preserve the relative position first, then snap to a line.
		if oldSize > 0 && oldSize != newSize {
			oldOffset = oldOffset * newSize / oldSize
		}
		if oldOffset < 0 {
			oldOffset = 0
		}
		if oldOffset >= newSize {
			oldOffset = newSize - 1
		}
		if vv.HexMode {
			vv.TopOffset = oldOffset &^ 0xF
		} else {
			vv.TopOffset = vv.Backend.FindLineStart(oldOffset)
		}
	}

	if oldBackend != nil {
		oldBackend.Close()
	}
	vtui.FrameManager.Redraw()
}

// newViewerBackend gives text mode one consistent coordinate system. A
// ViewerView's offsets are offsets in the UTF-8 stream it renders, not offsets
// in the raw file. Keeping a raw CP1251/CP866/UTF-16 window while exposing its
// decoded bytes made every multi-byte character change the meaning of the
// next offset; the cursor eventually ran past EOF, especially after Ctrl+End
// followed by an F8 switch. Non-UTF-8 files are materialized into the same
// memory-backed stream that the old viewer used, while UTF-8 keeps the lazy
// windowed backend for large files and remote VFSes.
func newViewerBackend(ctx context.Context, owner vfs.VFS, path string, f vfs.ReadAtCloser, cpID int, DataOffset int64) (*ViewerBackend, error) {
	if cpID != 65001 {
		size := f.Size()
		maxInt := int64(int(^uint(0) >> 1))
		if size < 0 || size > maxInt {
			return nil, fmt.Errorf("viewer: file is too large to decode: %d bytes", size)
		}
		raw := make([]byte, int(size))
		n, err := f.ReadAt(ctx, raw, 0)
		if err != nil && err != io.EOF {
			return nil, err
		}
		decoded, err := vfs.DecodeBytes(raw[:n], cpID)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
		bCtx, bCancel := context.WithCancel(context.Background())
		return &ViewerBackend{
			File:         &vfs.MemoryReadAtCloser{Data: decoded},
			size:         int64(len(decoded)),
			path:         path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}, nil
	}

	logicalSize := f.Size() - DataOffset
	if logicalSize < 0 {
		logicalSize = 0
	}
	bCtx, bCancel := context.WithCancel(context.Background())
	backend := &ViewerBackend{
		File:         f,
		size:         logicalSize,
		path:         path,
		owner:        owner,
		codepage:     cpID,
		DataOffset:   DataOffset,
		totalLines:   -1,
		totalForSize: -1,
		ctx:          bCtx,
		cancelCtx:    bCancel,
	}
	if indexer, ok := owner.(vfs.LineIndexer); ok {
		backend.indexer = indexer
	}
	return backend, nil
}

func (vv *ViewerView) ReloadWithAutoDetect() {
	f, err := vv.VFS.Open(context.Background(), vv.Path)
	if err != nil {
		return
	}
	defer f.Close()

	header, err := viewerDetectionHeader(context.Background(), f)
	if err != nil {
		return
	}

	// The user asked for this file to be detected, so detect it -- the
	// global switch decides what happens at open, not here (#875).
	cpID := vfs.DetectEncoding(header, true, config.App.ViewerDefaultCodePage)
	fileops.SaveCodepageOverride(vv.VFS, vv.Path, 0)
	vv.ReloadWithCodepage(cpID)
}

func (vv *ViewerView) ShowCodepageDialog() {
	_, overridden := fileops.RememberedCodepage(vv.VFS, vv.Path)
	items, currIdx := vfs.BuildCodepageMenuItems(vv.Codepage, !overridden)
	menu := dialog.NewCodepageMenu(i18n.Msg("Codepage.Title"), items)

	// This menu is about the file on screen, as Shift+F8 is in Far: a
	// codepage picked here is remembered for this file, and Auto-detect
	// forgets that and detects it again. Neither touches the global
	// viewer settings -- flipping AutodetectCodePage off and rewriting the
	// default codepage from here is what made every later file open in
	// whatever the previous one was switched to (#875).
	menu.OnAction = func(idx int) {
		menu.Close()
		if idx >= 0 && idx < len(menu.Items) {
			if cpID, ok := menu.Items[idx].UserData.(int); ok {
				if cpID == vfs.CodepageAutoDetect {
					vv.ReloadWithAutoDetect()
				} else {
					fileops.SaveCodepageOverride(vv.VFS, vv.Path, cpID)
					vv.ReloadWithCodepage(cpID)
				}
			}
		}
	}
	menu.SetSelectPos(currIdx)
	vtui.FrameManager.Push(menu)
}

func (vv *ViewerView) ProcessMouse(e *vtinput.InputEvent) bool {
	if e.Type != vtinput.MouseEventType {
		return false
	}
	if e.WheelDirection == 0 {
		if changed := vv.updateURLHover(int(e.MouseX), int(e.MouseY)); changed {
			vtui.FrameManager.Redraw()
		}
		if CtrlMouseClick(e) {
			if link, ok := vv.urlLinkAtMouse(int(e.MouseX), int(e.MouseY)); ok {
				OpenExternalURLAsync(link.URL)
				return true
			}
		}
	}
	if vv.ScrollBar != nil && vv.ScrollBar.ProcessMouse(e) {
		return true
	}
	if e.WheelDirection != 0 {
		vv.hoverURL = ""
		speed := config.App.WheelViewerDown
		vk := uint16(vtinput.VK_DOWN)
		if e.WheelDirection > 0 {
			speed = config.App.WheelViewerUp
			vk = vtinput.VK_UP
		}
		for i := 0; i < config.WheelScrollLines(speed); i++ {
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk})
		}
		return true
	}
	return false
}

func (vv *ViewerView) urlLinkAtMouse(mx, my int) (UrlCellRange, bool) {
	if vv.HexMode || vv.DecodeMode || mx < vv.X1 || mx > vv.X2 || my < vv.Y1+1 || my > vv.Y2 {
		return UrlCellRange{}, false
	}
	row := my - (vv.Y1 + 1)
	if row < 0 || row >= len(vv.visibleURLRows) {
		return UrlCellRange{}, false
	}
	col := mx - vv.X1
	for _, link := range vv.visibleURLRows[row] {
		if col >= link.Start && col < link.End {
			return link, true
		}
	}
	return UrlCellRange{}, false
}

func (vv *ViewerView) updateURLHover(mx, my int) bool {
	var next string
	if link, ok := vv.urlLinkAtMouse(mx, my); ok {
		next = link.URL
	}
	if next == vv.hoverURL {
		return false
	}
	vv.hoverURL = next
	return true
}
func (vv *ViewerView) ResizeConsole(w, h int) {
	top := vtui.FrameManager.WorkspaceTopInset()
	if !config.App.AlwaysShowMenuBar || vv.menuBar == nil {
		vv.SetPosition(0, top, w-1, h-2)
		return
	}
	// AlwaysShowMenuBar keeps the menu bar on the workspace's top row, the row
	// it has over the panels and the terminal too, and the viewer starts below
	// it with its title bar, which the bar would otherwise cover (issue #1153).
	vv.SetPosition(0, top+1, w-1, h-2)
	vv.menuBar.SetPosition(0, top, w-1, top)
}

// menuBarPinned reports whether ResizeConsole has given the menu bar a row of
// its own above the title bar. SetPosition alone puts the bar on the title
// row, where F9 raises it over the title while AlwaysShowMenuBar is off.
func (vv *ViewerView) menuBarPinned() bool {
	if vv.menuBar == nil {
		return false
	}
	_, menuY, _, _ := vv.menuBar.GetPosition()
	_, y1, _, _ := vv.GetPosition()
	return menuY < y1
}

func (vv *ViewerView) Close() {
	if vv.highlight != nil {
		vv.highlight.Close()
		vv.highlight = nil
	}
	vv.stopTailWatch()
	if fileops.GlobalFileState != nil && vv.Path != "" {
		fileops.GlobalFileState.SaveViewerStateAsync(fileops.FileStateKey(vv.VFS, vv.Path), vv.TopOffset, vv.WrapMode, vv.HexMode)
	}
	var size int64
	if vv.Backend != nil {
		size = vv.Backend.Size()
		// Closing on the way out; a failure here has nothing left to report to.
		_ = vv.Backend.Close()
	}
	vv.lineOffsets = nil
	vv.rowCells = nil
	vv.ScrollBar = nil
	vv.BaseFrame.Close()
	if vv.OnClose != nil {
		vv.OnClose()
	}
	numeric.ReleaseHeavyMemory(size)
}

func (vv *ViewerView) GetKeyLabels() *vtui.KeySet {
	nextCp := vfs.GetNextFastSwitchCodepage(vv.Codepage)
	nextCpName := vfs.DisplayCodepageName(nextCp)

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			i18n.Msg("KeyBar.ViewerF1"), i18n.Msg("KeyBar.ViewerF2"), i18n.Msg("KeyBar.ViewerF3"), i18n.Msg("KeyBar.ViewerF4"),
			"", i18n.Msg("KeyBar.F4"), i18n.Msg("KeyBar.ViewerF7"), nextCpName, "", i18n.Msg("KeyBar.ViewerF10"),
		},
		Alt: vtui.KeyBarLabels{
			"", "", "", "", "", "", "", i18n.Msg("KeyBar.ViewerAltF8"), "", "",
		},
	}
	if App == nil {
		return fallbacks
	}
	res := App.KeyBarLabels("Viewer", fallbacks)
	if App.ActionForKey("Viewer", "F8") == "Viewer.CodepageNext" {
		res.Normal[7] = nextCpName
	}
	return res
}

func (vv *ViewerView) GetType() vtui.FrameType { return vtui.TypeUser + 3 }
func (vv *ViewerView) GetTitle() string {
	if vv.Path != "" {
		return "View: " + filepath.Base(vv.Path)
	}
	return "Viewer"
}

// GetWorkspaceTabTitle provides a compact title for the workspace
// tab bar while leaving GetTitle available for contexts that need the fuller
// textual description.
func (vv *ViewerView) GetWorkspaceTabTitle() string {
	if vv.Path != "" {
		return filepath.Base(vv.Path)
	}
	return "Viewer"
}
func (vv *ViewerView) GetWorkspaceTabMarker() string { return "V" }
