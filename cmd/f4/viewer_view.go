package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/piecetable"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// ViewerView is a high-performance file viewer component.
type ViewerView struct {
	vtui.BaseFrame
	topBar  *TopBar
	menuBar *vtui.MenuBar
	backend *ViewerBackend
	vfs     vfs.VFS
	path    string

	HexMode bool
	// hexAuto records that hex mode came from the binary check rather than
	// from the user. Only an automatic verdict may be revisited when the
	// codepage changes: a hex view the user asked for has to survive an F8.
	hexAuto    bool
	DecodeMode bool
	WrapMode   bool
	// DisasmMode is the processor mode the decode view disassembles in:
	// 16, 32 or 64, or 0 while undecided. See disasm.go.
	DisasmMode int
	TopOffset  int64 // Current byte offset of the first visible line

	// For Text mode: offsets of lines currently on screen
	lineOffsets         []int64
	rowCells            []vtui.CharInfo
	visibleURLRows      [][]urlCellRange
	hoverURL            string
	eofVisible          bool
	lastKnownSize       int64
	lastSearch          string
	lastSearchOffset    int64
	lastSearchTopOffset int64
	lastSearchFound     bool
	lastSearchMatchLen  int64
	lastSearchCase      bool
	lastSearchReverse   bool
	lastSearchRegexp    bool
	lastSearchWholeWord bool

	scrollBar *vtui.ScrollBar

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

	cpID := vfs.DetectEncoding(header, AppConfig.ViewerAutodetectCodePage, AppConfig.ViewerDefaultCodePage)
	if remembered, ok := rememberedCodepage(v, path); ok {
		cpID = remembered
	}
	binary := viewerHeaderLooksBinary(header, cpID)
	if binary {
		// Binary data has no text codepage to materialize. Keeping the remote
		// handle lets the hex viewer fetch only its small visible windows.
		cpID = 65001
	}
	dataOffset := int64(0)
	if cpID == 65001 && !binary && vfs.HasUTF8BOM(header) {
		dataOffset = vfs.UTF8BOMSize
	}

	backend, err := newViewerBackend(ctx, v, path, f, cpID, dataOffset)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	vv := &ViewerView{
		backend:  backend,
		vfs:      v,
		path:     path,
		HexMode:  binary,
		hexAuto:  binary,
		WrapMode: true,
		// The decode view's processor mode is read off the same header the
		// binary check used, here where the whole header is in hand: the
		// backend serves the decode view through a moving cache window,
		// which need not cover offset 0 by the time the mode is wanted.
		DisasmMode: detectX86Mode(header),
		Codepage:   cpID,
	}
	vv.scrollBar = vtui.NewScrollBar(0, 0, 0)
	vv.scrollBar.ColorIdx = ColViewerScrollbar
	vv.scrollBar.SetOwner(vv)
	vv.scrollBar.OnScroll = func(v int) {
		newOff := int64(v)
		if vv.HexMode {
			newOff &= ^int64(0xF)
		} else {
			// Optimization: during fast drag, don't FindLineStart every pixel
			// unless we are close to the target or moving slowly.
			// For now, simple snap.
			newOff = vv.backend.FindLineStart(newOff)
		}
		if newOff != vv.TopOffset {
			vv.TopOffset = newOff
			vtui.FrameManager.Redraw()
		}
	}
	vv.scrollBar.OnStep = func(step int) {
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
	vv.topBar = NewTopBar(
		func() string {
			base := displayFileTitle(vv.vfs, vv.path)
			return " " + base
		},
		func() string {
			percent := 0
			size := vv.backend.Size()
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
			mode := Msg("Viewer.ModeText")
			if vv.DecodeMode {
				mode = disasmModeLabel(vv.disasmMode())
			} else if vv.HexMode {
				mode = Msg("Viewer.ModeHex")
			}
			cpName := vfs.DisplayCodepageName(vv.Codepage)
			return fmt.Sprintf(" %s │ %s │ %d%%     ", cpName, mode, percent)
		},
	)
	vv.topBar.SetVisible(true)
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

// refreshFromFile re-measures the file and redraws when it moved. The
// auto-scroll in DisplayObject does the rest: a viewer sitting at the end of
// the file follows it, and one parked further up stays exactly where the
// reader left it and only gets an honest scrollbar and percentage.
func (vv *ViewerView) refreshFromFile() {
	if vv.backend == nil || vv.Busy {
		return
	}
	if !vv.backend.Refresh(context.Background()) {
		return
	}
	if size := vv.backend.Size(); vv.TopOffset > size {
		// The file was truncated or rotated away under the viewport, and the
		// offset it was showing no longer exists.
		vv.TopOffset = 0
		vv.lastKnownSize = size
		vv.eofVisible = false
	}
	vtui.FrameManager.Redraw()
}

// reload rereads the file on demand. Unlike the poll it drops the window cache
// even when the length did not change, so a file rewritten in place -- same
// size, different bytes -- also shows its new contents.
func (vv *ViewerView) reload() {
	if vv.backend == nil {
		return
	}
	vv.backend.Refresh(context.Background())
	vv.backend.DropCache()
	if size := vv.backend.Size(); vv.TopOffset > size {
		vv.TopOffset = 0
		vv.eofVisible = false
	}
	if vv.eofVisible {
		vv.jumpToEnd()
		return
	}
	vtui.FrameManager.Redraw()
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
	return looksBinary(decoded)
}

func (vv *ViewerView) SetPosition(x1, y1, x2, y2 int) {
	vv.ScreenObject.SetPosition(x1, y1, x2, y2)
	if vv.topBar != nil {
		vv.topBar.SetPosition(x1, y1, x2, y1)
	}
	if vv.menuBar != nil {
		vv.menuBar.SetPosition(x1, y1, x2, y1)
	}
	if vv.scrollBar != nil {
		vv.scrollBar.SetPosition(x2, y1+1, x2, y2)
	}
}

// GetMenuBar returns the viewer's menu bar. Items are regenerated from
// the action registry on every call, so shortcuts and toggle states are
// always current.
func (vv *ViewerView) GetMenuBar() *vtui.MenuBar {
	vv.menuBar.Items = BuildMenuBarItems("Viewer")
	return vv.menuBar
}

func (vv *ViewerView) HandleCommand(cmd int, args any) bool {
	if cmd == vtui.CmClose {
		vv.Close()
		return true
	}
	if cmd == CmSwitchToEditor {
		actionSwitchViewerToEditor(vv)
		return true
	}
	if cmd == CmSearch {
		actionViewerSearch(vv)
		return true
	}
	if handleWorkspaceForkCommand(cmd, args) {
		return true
	}
	return vv.BaseFrame.HandleCommand(cmd, args)
}

func (vv *ViewerView) Show(scr *vtui.ScreenBuf) {
	vv.ScreenObject.Show(scr)
	if vv.topBar != nil {
		vv.topBar.Show(scr)
	}
	vv.DisplayObject(scr)
}

func (vv *ViewerView) DisplayObject(scr *vtui.ScreenBuf) {
	if !vv.IsVisible() {
		return
	}

	// AUTO-SCROLL LOGIC (tail -f)
	currentSize := vv.backend.Size()
	if vv.eofVisible && currentSize > vv.lastKnownSize && !vv.Busy {
		vv.lastKnownSize = currentSize
		vv.jumpToEnd()
		return
	}
	vv.lastKnownSize = currentSize

	width := vv.X2 - vv.X1 + 1
	if vv.scrollBar != nil {
		width-- // Не рисуем текст поверх скроллбара
	}
	height := vv.Y2 - vv.Y1 + 1
	contentHeight := height - 1

	bgAttr := vtui.Palette[ColViewerText]

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

	if vv.scrollBar != nil && vv.backend.Size() > 0 {
		maxOffset := int(vv.backend.Size())
		if vv.HexMode {
			contentHeight := vv.Y2 - vv.Y1
			if contentHeight > 0 {
				lastLineOffset := int((vv.backend.Size() - 1) &^ 0xF)
				maxOffset = lastLineOffset - (contentHeight-1)*16
				if maxOffset < 0 {
					maxOffset = 0
				}
			}
		}
		vv.scrollBar.SetParams(int(vv.TopOffset), 0, maxOffset)
		vv.scrollBar.Show(scr)
	}
}

func (vv *ViewerView) renderHex(scr *vtui.ScreenBuf, width, contentHeight int) {
	attr := vtui.Palette[ColViewerText]
	offAttr := vtui.Palette[ColViewerArrows]

	currOffset := vv.TopOffset &^ 0xF // Align to 16 bytes
	//lastRowWasEOF := false

	for y := 0; y < contentHeight; y++ {
		if currOffset >= vv.backend.Size() {
			//lastRowWasEOF = true
			break
		}

		data, err := vv.backend.ReadAt(currOffset, 16)
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
	vv.eofVisible = (currOffset >= vv.backend.Size())
}
func (vv *ViewerView) renderDecode(scr *vtui.ScreenBuf, width, contentHeight int) {
	attr := vtui.Palette[ColViewerText]
	offAttr := vtui.Palette[ColViewerArrows]
	currOffset := vv.TopOffset

	for y := 0; y < contentHeight; y++ {
		if currOffset >= vv.backend.Size() {
			break
		}

		data, err := vv.backend.ReadAt(currOffset, disasmMaxInstLen)
		if err == piecetable.ErrLoading {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", attr))
			break
		}
		if err != nil && len(data) == 0 {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf(" [ Error: %v ] ", err), attr))
			break
		}

		asmStr, instLen := disasmInstruction(data, vv.disasmMode(), currOffset)

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
	vv.eofVisible = (currOffset >= vv.backend.Size())
}

// disasmMode returns the processor mode the decode view uses. A view built
// without a header (NewViewerView reads one) decides it here, from the
// file's first bytes, the first time an instruction is needed.
func (vv *ViewerView) disasmMode() int {
	if !disasmModeValid(vv.DisasmMode) {
		header, _ := vv.backend.ReadAt(0, 1024)
		vv.DisasmMode = detectX86Mode(header)
	}
	return vv.DisasmMode
}

// cycleDisasmMode switches the decode view to the next processor mode in
// the 64 -> 32 -> 16 -> 64 cycle and returns the mode now in effect.
func (vv *ViewerView) cycleDisasmMode() int {
	vv.DisasmMode = nextDisasmMode(vv.disasmMode())
	return vv.DisasmMode
}

// decodeStep returns how many bytes the instruction at off occupies in the
// current mode: the distance to the next line of the decode view. It is
// zero while the bytes at off are still being fetched.
func (vv *ViewerView) decodeStep(off int64) int64 {
	data, _ := vv.backend.ReadAt(off, disasmMaxInstLen)
	return int64(disasmInstLen(data, vv.disasmMode()))
}

func (vv *ViewerView) renderText(scr *vtui.ScreenBuf, width, contentHeight int) {

	attr := vtui.Palette[ColViewerText]
	currOffset := vv.TopOffset
	vv.lineOffsets = vv.lineOffsets[:0]
	vv.visibleURLRows = vv.visibleURLRows[:0]
	//lastRowWasEOF := false

	for y := 0; y < contentHeight; y++ {
		vv.lineOffsets = append(vv.lineOffsets, currOffset)
		if currOffset >= vv.backend.Size() {
			vv.visibleURLRows = append(vv.visibleURLRows, nil)
			//lastRowWasEOF = true
			break
		}

		// Read a generous chunk to handle wrapping. The row helper keeps
		// combining sequences and script conjuncts atomic.
		data, err := vv.backend.ReadAt(currOffset, width*4)
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
		if AppConfig.EditorTabSize > 0 {
			tabSize = AppConfig.EditorTabSize
		}
		row := layoutViewerTextRow(data, width, tabSize, vv.WrapMode)

		// Build []vtui.CharInfo for the line
		var cellByteOffsets []int
		vv.rowCells, cellByteOffsets = viewerTextCells(string(data[:row.textLen]), attr, tabSize, width)
		if vv.lastSearchFound && vv.lastSearch != "" {
			matchStart := vv.lastSearchOffset
			matchLen := vv.lastSearchMatchLen
			// Keep manually constructed ViewerViews and old sessions safe: a
			// literal match used to derive its end from the pattern itself.
			if matchLen <= 0 {
				matchLen = int64(len(vv.lastSearch))
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
					vtui.Palette[ColViewerSelectedText],
				)
			}
		}

		rowLinks := urlCellRanges(string(data[:row.textLen]), cellByteOffsets)
		applyURLHoverAttr(vv.rowCells, rowLinks, vv.hoverURL)
		vv.visibleURLRows = append(vv.visibleURLRows, rowLinks)
		scr.Write(vv.X1, vv.Y1+1+y, vv.rowCells)
		currOffset += int64(row.lineLen)

		if !row.foundNewline && !vv.WrapMode {
			// In no-wrap mode, we must consume until the actual newline
			tempOff := currOffset
			for {
				b, err := vv.backend.ReadAt(tempOff, 1024)
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
	}
	vv.eofVisible = (currOffset >= vv.backend.Size())
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
			if vv.TopOffset+16 < vv.backend.Size() {
				vv.TopOffset += 16
			}
		} else if len(vv.lineOffsets) > 1 {
			vv.TopOffset = vv.lineOffsets[1]
		} else {
			// Fail-safe: if lineOffsets not populated (e.g. before first render),
			// try to proactively find the next line start from current offset.
			width := vv.X2 - vv.X1 + 1
			if vv.scrollBar != nil {
				width--
			}
			data, err := vv.backend.ReadAt(vv.TopOffset, width*4)
			if err == nil && len(data) > 0 {
				tabSize := 8
				if AppConfig.EditorTabSize > 0 {
					tabSize = AppConfig.EditorTabSize
				}
				row := layoutViewerTextRow(data, width, tabSize, vv.WrapMode)
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
			vv.TopOffset = vv.backend.FindLineStart(vv.TopOffset - 1)
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
			if vv.TopOffset >= vv.backend.Size() {
				vv.TopOffset = vv.backend.Size() - 1
			}
		} else if vv.HexMode {
			vv.TopOffset += 16 * contentHeight
			if vv.TopOffset >= vv.backend.Size() {
				vv.TopOffset = (vv.backend.Size() - 1) &^ 0xF
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
				vv.TopOffset = vv.backend.FindLineStart(vv.TopOffset - 1)
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
			vv.askGoto()
			return true
		}
	}

	// Injected-event fallback: KeyBar mouse clicks reach ProcessKey via
	// InjectEvents, which skips FrameManager.EventFilter and therefore the
	// hotkey manager. Route them through the same lookup so clicking F2/F5/
	// F7/… on the bottom bar triggers the configured Viewer action.
	if MacroMgr.LookupHotkey(e) {
		return true
	}

	return false
}

// askGoto prompts for a position. In text mode that is a line number, which
// only means something once someone has counted the newlines; in hex mode it
// is a byte offset, which needs no counting at all.
func (vv *ViewerView) askGoto() {
	if vv.HexMode || vv.DecodeMode {
		title, prompt := gotoText("Viewer.GotoOffsetTitle", " Go to offset "), gotoText("Viewer.GotoOffsetPrompt", "Byte offset:")
		showGotoOffsetDialog(vv, title, prompt, vv.TopOffset, func(offset int64) {
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
		size := vv.backend.Size()
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
		off, ok := vv.backend.LineStart(ctx.Context, n)
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
	if vv.backend != nil {
		vv.backend.Refresh(context.Background())
	}

	contentHeight := int64(vv.Y2 - vv.Y1)
	if vv.HexMode {
		if vv.backend.Size() == 0 {
			vv.TopOffset = 0
		} else {
			lastLineOffset := (vv.backend.Size() - 1) &^ 0xF
			vv.TopOffset = lastLineOffset - (contentHeight-1)*16
			if vv.TopOffset < 0 {
				vv.TopOffset = 0
			}
		}
		return
	}

	if vv.backend.Size() == 0 {
		vv.TopOffset = 0
		return
	}

	vv.Busy = true
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		defer ctx.RunOnUI(func() { vv.Busy = false })
		width := vv.X2 - vv.X1 + 1
		if vv.scrollBar != nil {
			width--
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
		startOff := vv.backend.Size() - chunkSize
		if startOff < 0 {
			startOff = 0
		}

		for {
			if ctx.Err() != nil {
				return
			}
			_, err := vv.backend.ReadAt(startOff, 1024)
			if err != piecetable.ErrLoading {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		var offsets []int64
		currOff := startOff

		tabSize := 8
		if AppConfig.EditorTabSize > 0 {
			tabSize = AppConfig.EditorTabSize
		}
		for currOff < vv.backend.Size() {
			if ctx.Err() != nil {
				return
			}
			data, err := vv.backend.ReadAt(currOff, 64*1024)
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
				if vv.WrapMode {
					maxRowData := width * 4
					if maxRowData < len(rowData) {
						rowData = rowData[:maxRowData]
					}
				}
				row := layoutViewerTextRow(rowData, width, tabSize, vv.WrapMode)
				scanPos += row.lineLen
				if !row.foundNewline && !vv.WrapMode {
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

	f, err := vv.vfs.Open(context.Background(), vv.path)
	if err != nil {
		return
	}

	header, err := viewerDetectionHeader(context.Background(), f)
	if err != nil {
		_ = f.Close()
		return
	}

	hexMode := vv.HexMode
	if vv.hexAuto {
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
	dataOffset := int64(0)
	if backendCP == 65001 && !hexMode &&
		!viewerHeaderLooksBinary(header, backendCP) && vfs.HasUTF8BOM(header) {
		dataOffset = vfs.UTF8BOMSize
	}
	backend, err := newViewerBackend(context.Background(), vv.vfs, vv.path, f, backendCP, dataOffset)
	if err != nil {
		_ = f.Close()
		return
	}

	oldBackend := vv.backend
	oldOffset := vv.TopOffset
	oldSize := int64(0)
	if oldBackend != nil {
		oldSize = oldBackend.Size()
	}
	vv.backend = backend
	vv.Codepage = cpID
	vv.HexMode = hexMode
	newSize := vv.backend.Size()
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
			vv.TopOffset = vv.backend.FindLineStart(oldOffset)
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
func newViewerBackend(ctx context.Context, owner vfs.VFS, path string, f vfs.ReadAtCloser, cpID int, dataOffset int64) (*ViewerBackend, error) {
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
			file:         &vfs.MemoryReadAtCloser{Data: decoded},
			size:         int64(len(decoded)),
			path:         path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}, nil
	}

	logicalSize := f.Size() - dataOffset
	if logicalSize < 0 {
		logicalSize = 0
	}
	bCtx, bCancel := context.WithCancel(context.Background())
	backend := &ViewerBackend{
		file:         f,
		size:         logicalSize,
		path:         path,
		owner:        owner,
		codepage:     cpID,
		dataOffset:   dataOffset,
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
	f, err := vv.vfs.Open(context.Background(), vv.path)
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
	cpID := vfs.DetectEncoding(header, true, AppConfig.ViewerDefaultCodePage)
	saveCodepageOverride(vv.vfs, vv.path, 0)
	vv.ReloadWithCodepage(cpID)
}

func (vv *ViewerView) showCodepageDialog() {
	_, overridden := rememberedCodepage(vv.vfs, vv.path)
	items, currIdx := vfs.BuildCodepageMenuItems(vv.Codepage, !overridden)
	menu := newCodepageMenu(Msg("Codepage.Title"), items)

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
					saveCodepageOverride(vv.vfs, vv.path, cpID)
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
		if ctrlMouseClick(e) {
			if link, ok := vv.urlLinkAtMouse(int(e.MouseX), int(e.MouseY)); ok {
				openExternalURLAsync(link.URL)
				return true
			}
		}
	}
	if vv.scrollBar != nil && vv.scrollBar.ProcessMouse(e) {
		return true
	}
	if e.WheelDirection != 0 {
		vv.hoverURL = ""
		speed := AppConfig.WheelViewerDown
		vk := uint16(vtinput.VK_DOWN)
		if e.WheelDirection > 0 {
			speed = AppConfig.WheelViewerUp
			vk = vtinput.VK_UP
		}
		for i := 0; i < wheelScrollLines(speed); i++ {
			vv.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, VirtualKeyCode: vk})
		}
		return true
	}
	return false
}

func (vv *ViewerView) urlLinkAtMouse(mx, my int) (urlCellRange, bool) {
	if vv.HexMode || vv.DecodeMode || mx < vv.X1 || mx > vv.X2 || my < vv.Y1+1 || my > vv.Y2 {
		return urlCellRange{}, false
	}
	row := my - (vv.Y1 + 1)
	if row < 0 || row >= len(vv.visibleURLRows) {
		return urlCellRange{}, false
	}
	col := mx - vv.X1
	for _, link := range vv.visibleURLRows[row] {
		if col >= link.Start && col < link.End {
			return link, true
		}
	}
	return urlCellRange{}, false
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
	vv.SetPosition(0, vtui.FrameManager.WorkspaceTopInset(), w-1, h-2)
}

func (vv *ViewerView) Close() {
	vv.stopTailWatch()
	if GlobalFileState != nil && vv.path != "" {
		GlobalFileState.SaveViewerStateAsync(FileStateKey(vv.vfs, vv.path), vv.TopOffset, vv.WrapMode, vv.HexMode)
	}
	var size int64
	if vv.backend != nil {
		size = vv.backend.Size()
		vv.backend.Close()
	}
	vv.lineOffsets = nil
	vv.rowCells = nil
	vv.scrollBar = nil
	vv.BaseFrame.Close()
	if vv.OnClose != nil {
		vv.OnClose()
	}
	ReleaseHeavyMemory(size)
}

func (vv *ViewerView) GetKeyLabels() *vtui.KeySet {
	nextCp := vfs.GetNextFastSwitchCodepage(vv.Codepage)
	nextCpName := vfs.DisplayCodepageName(nextCp)

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.ViewerF1"), Msg("KeyBar.ViewerF2"), Msg("KeyBar.ViewerF3"), Msg("KeyBar.ViewerF4"),
			"", Msg("KeyBar.F4"), Msg("KeyBar.ViewerF7"), nextCpName, "", Msg("KeyBar.ViewerF10"),
		},
		Alt: vtui.KeyBarLabels{
			"", "", "", "", "", "", "", Msg("KeyBar.ViewerAltF8"), "", "",
		},
	}
	res := KeyBarLabelsForArea("Viewer", fallbacks)
	if hm := GlobalHotkeysMgr; hm != nil {
		if hm.GetAction("Viewer", "F8") == "Viewer.CodepageNext" {
			res.Normal[7] = nextCpName
		}
	}
	return res
}

func (vv *ViewerView) GetType() vtui.FrameType { return vtui.TypeUser + 3 }
func (vv *ViewerView) GetTitle() string {
	if vv.path != "" {
		return "View: " + filepath.Base(vv.path)
	}
	return "Viewer"
}

// GetWorkspaceTabTitle provides a compact title for the workspace
// tab bar while leaving GetTitle available for contexts that need the fuller
// textual description.
func (vv *ViewerView) GetWorkspaceTabTitle() string {
	if vv.path != "" {
		return filepath.Base(vv.path)
	}
	return "Viewer"
}
func (vv *ViewerView) GetWorkspaceTabMarker() string { return "V" }
