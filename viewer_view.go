package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
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

	HexMode   bool
	WrapMode  bool
	TopOffset int64 // Current byte offset of the first visible line

	// For Text mode: offsets of lines currently on screen
	lineOffsets         []int64
	eofVisible          bool
	lastKnownSize       int64
	lastSearch          string
	lastSearchOffset    int64
	lastSearchTopOffset int64
	lastSearchFound     bool

	scrollBar *vtui.ScrollBar

	OnClose  func()
	Codepage int
}

func NewViewerView(ctx context.Context, v vfs.VFS, path string) (*ViewerView, error) {
	f, err := v.Open(ctx, path)
	if err != nil {
		return nil, err
	}

	size := f.Size()
	detectLen := 16 * 1024
	if int64(detectLen) > size {
		detectLen = int(size)
	}
	header := make([]byte, detectLen)
	if _, err := f.ReadAt(ctx, header, 0); err != nil && err != io.EOF {
		_ = f.Close()
		return nil, fmt.Errorf("read file header: %w", err)
	}

	cpID := vfs.DetectEncoding(header, AppConfig.ViewerAutodetectCodePage, AppConfig.ViewerDefaultCodePage)
	binary := viewerHeaderLooksBinary(header, cpID)
	if binary {
		// Binary data has no text codepage to materialize. Keeping the remote
		// handle lets the hex viewer fetch only its small visible windows.
		cpID = 65001
	}

	var backend *ViewerBackend
	bCtx, bCancel := context.WithCancel(context.Background())
	if cpID == 65001 {
		backend = &ViewerBackend{
			file:         f,
			size:         size,
			path:         path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}
		if indexer, ok := v.(vfs.LineIndexer); ok {
			backend.indexer = indexer
		}
	} else {
		fullData := make([]byte, size)
		_, _ = f.ReadAt(ctx, fullData, 0)
		f.Close()

		decoded, err := vfs.DecodeBytes(fullData, cpID)
		if err != nil {
			decoded = fullData
			cpID = 65001
		}
		memFile := &vfs.MemoryReadAtCloser{Data: decoded}
		backend = &ViewerBackend{
			file:         memFile,
			size:         int64(len(decoded)),
			path:         path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}
	}

	vv := &ViewerView{
		backend:  backend,
		vfs:      v,
		path:     path,
		HexMode:  binary,
		WrapMode: true,
		Codepage: cpID,
	}
	vv.scrollBar = vtui.NewScrollBar(0, 0, 0)
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
			base := ""
			if vv.vfs != nil {
				base = vv.vfs.Base(vv.path)
			} else {
				base = filepath.Base(vv.path)
			}
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
			if vv.HexMode {
				mode = Msg("Viewer.ModeHex")
			}
			cpName := vfs.DisplayCodepageName(vv.Codepage)
			return fmt.Sprintf(" %s │ %s │ %d%%     ", cpName, mode, percent)
		},
	)
	vv.topBar.SetVisible(true)
	vv.SetCanFocus(true)
	vv.SetFocus(true)
	return vv, nil
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
	if cmd == CmSearch {
		actionViewerSearch(vv)
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
		if vv.HexMode {
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

func (vv *ViewerView) renderText(scr *vtui.ScreenBuf, width, contentHeight int) {

	attr := vtui.Palette[ColViewerText]
	currOffset := vv.TopOffset
	vv.lineOffsets = vv.lineOffsets[:0]
	//lastRowWasEOF := false

	for y := 0; y < contentHeight; y++ {
		vv.lineOffsets = append(vv.lineOffsets, currOffset)
		if currOffset >= vv.backend.Size() {
			//lastRowWasEOF = true
			break
		}

		// Read a generous chunk to handle wrapping
		data, err := vv.backend.ReadAt(currOffset, width*4)
		if err == piecetable.ErrLoading {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(" [ Loading... ] ", attr))
			break
		}
		if err != nil {
			scr.Write(vv.X1, vv.Y1+1+y, vtui.StringToCharInfo(fmt.Sprintf(" [ Error: %v ] ", err), attr))
			break
		}
		if len(data) == 0 {
			break
		}

		lineLen := 0
		textLen := 0
		visualWidth := 0
		foundNewline := false
		tabSize := 8
		if AppConfig.EditorTabSize > 0 {
			tabSize = AppConfig.EditorTabSize
		}

		for lineLen < len(data) {
			r, size := utf8.DecodeRune(data[lineLen:])
			if r == '\n' {
				lineLen += size
				foundNewline = true
				break
			}
			if r == '\r' {
				lineLen += size
				continue
			}

			rw := 1
			if r == '\t' {
				rw = tabSize - (visualWidth % tabSize)
			} else {
				rw = runewidth.RuneWidth(r)
				if rw <= 0 {
					rw = 1
				}
			}
			if vv.WrapMode && visualWidth+rw > width {
				// Wrap occurred
				break
			}
			visualWidth += rw
			lineLen += size
			textLen = lineLen
		}

		// Build []vtui.CharInfo for the line
		var cells []vtui.CharInfo
		lineBytes := data[:textLen]
		visualCol := 0

		for len(lineBytes) > 0 {
			r, size := utf8.DecodeRune(lineBytes)
			lineBytes = lineBytes[size:]

			if r == '\t' {
				w := tabSize - (visualCol % tabSize)
				for i := 0; i < w; i++ {
					cells = append(cells, vtui.CharInfo{Char: ' ', Attributes: attr})
				}
				visualCol += w
			} else {
				displayRune, w := vtui.SanitizeRune(r)
				if r < 0x20 || r == 0x7F {
					displayRune = ' '
				}
				if w > 0 {
					charVal := uint64(displayRune)
					for i := 0; i < w; i++ {
						cells = append(cells, vtui.CharInfo{Char: charVal, Attributes: attr})
						charVal = uint64(vtui.WideCharFiller)
					}
					visualCol += w
				}
			}
		}

		scr.Write(vv.X1, vv.Y1+1+y, cells)
		currOffset += int64(lineLen)

		if !foundNewline && !vv.WrapMode {
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
		if vv.HexMode {
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
				lineLen := 0
				visualWidth := 0
				tabSize := 8
				if AppConfig.EditorTabSize > 0 {
					tabSize = AppConfig.EditorTabSize
				}
				for lineLen < len(data) {
					r, size := utf8.DecodeRune(data[lineLen:])
					if r == '\n' {
						lineLen += size
						break
					}
					rw := 1
					if r == '\t' {
						rw = tabSize - (visualWidth % tabSize)
					} else {
						rw = runewidth.RuneWidth(r)
						if rw <= 0 {
							rw = 1
						}
					}
					if vv.WrapMode && visualWidth+rw > width {
						break
					}
					visualWidth += rw
					lineLen += size
				}
				if lineLen > 0 {
					vv.TopOffset += int64(lineLen)
				}
			}
		}
		return true

	case vtinput.VK_UP:
		if vv.HexMode {
			vv.TopOffset -= step
		} else {
			vv.TopOffset = vv.backend.FindLineStart(vv.TopOffset - 1)
		}
		if vv.TopOffset < 0 {
			vv.TopOffset = 0
		}
		return true

	case vtinput.VK_NEXT: // PgDn
		if vv.HexMode {
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
		if vv.HexMode {
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
	title, prompt := " Go to line ", "Line number:"
	if vv.HexMode {
		title, prompt = " Go to offset ", "Byte offset:"
	}
	vtui.InputBoxOn(vv, title, prompt, "", func(s string) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil || n < 0 {
			return
		}
		vv.gotoPosition(n)
	})
}

func (vv *ViewerView) gotoPosition(n int64) {
	if vv.HexMode {
		size := vv.backend.Size()
		if n >= size {
			n = size - 1
		}
		if n < 0 {
			n = 0
		}
		vv.TopOffset = n &^ 0xF
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
				lineLen := 0
				visualWidth := 0
				foundNewline := false
				for scanPos+lineLen < len(data) {
					r, size := utf8.DecodeRune(data[scanPos+lineLen:])
					if r == '\n' {
						lineLen += size
						foundNewline = true
						break
					}
					if r == '\r' {
						lineLen += size
						continue
					}
					rw := 1
					if r == '\t' {
						rw = tabSize - (visualWidth % tabSize)
					} else {
						rw = runewidth.RuneWidth(r)
						if rw <= 0 {
							rw = 1
						}
					}
					if vv.WrapMode && visualWidth+rw > width {
						break
					}
					visualWidth += rw
					lineLen += size
				}
				scanPos += lineLen
				if !foundNewline && !vv.WrapMode {
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

	size := f.Size()
	var backend *ViewerBackend
	if cpID == 65001 {
		bCtx, bCancel := context.WithCancel(context.Background())
		backend = &ViewerBackend{
			file:         f,
			size:         size,
			path:         vv.path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}
		if indexer, ok := vv.vfs.(vfs.LineIndexer); ok {
			backend.indexer = indexer
		}
	} else {
		defer f.Close()
		fullData := make([]byte, size)
		_, _ = f.ReadAt(context.Background(), fullData, 0)

		decoded, err := vfs.DecodeBytes(fullData, cpID)
		if err != nil {
			decoded = fullData
			cpID = 65001
		}
		memFile := &vfs.MemoryReadAtCloser{Data: decoded}
		bCtx, bCancel := context.WithCancel(context.Background())
		backend = &ViewerBackend{
			file:         memFile,
			size:         int64(len(decoded)),
			path:         vv.path,
			totalLines:   -1,
			totalForSize: -1,
			ctx:          bCtx,
			cancelCtx:    bCancel,
		}
	}

	oldBackend := vv.backend
	vv.backend = backend
	vv.Codepage = cpID
	vv.TopOffset = vv.backend.FindLineStart(vv.TopOffset)

	if oldBackend != nil {
		oldBackend.Close()
	}
	vtui.FrameManager.Redraw()
}

func (vv *ViewerView) ReloadWithAutoDetect() {
	f, err := vv.vfs.Open(context.Background(), vv.path)
	if err != nil {
		return
	}
	defer f.Close()

	size := f.Size()
	detectLen := 16 * 1024
	if int64(detectLen) > size {
		detectLen = int(size)
	}
	header := make([]byte, detectLen)
	_, _ = f.ReadAt(context.Background(), header, 0)

	cpID := vfs.DetectEncoding(header, AppConfig.ViewerAutodetectCodePage, AppConfig.ViewerDefaultCodePage)
	vv.ReloadWithCodepage(cpID)
}

func (vv *ViewerView) showCodepageDialog() {
	items, currIdx := vfs.BuildCodepageMenuItems(vv.Codepage, AppConfig.ViewerAutodetectCodePage)
	menu := vtui.NewVMenu(Msg("Codepage.Title"))
	for _, item := range items {
		menu.AddItem(item)
	}

	w, h := 45, len(items)+2
	scrW := vtui.FrameManager.GetScreenSize()
	scrH := vtui.FrameManager.GetScreenHeight()
	maxH := scrH - 2
	if maxH < 5 {
		maxH = 5
	}
	if h > maxH {
		h = maxH
	}
	x := (scrW - w) / 2
	y := (scrH - h) / 2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	menu.SetPosition(x, y, x+w-1, y+h-1)

	menu.OnAction = func(idx int) {
		menu.Close()
		if idx >= 0 && idx < len(menu.Items) {
			if cpID, ok := menu.Items[idx].UserData.(int); ok {
				if cpID == -1 {
					AppConfig.ViewerAutodetectCodePage = !AppConfig.ViewerAutodetectCodePage
					SaveConfig()
					vv.ReloadWithAutoDetect()
				} else {
					AppConfig.ViewerAutodetectCodePage = false
					AppConfig.ViewerDefaultCodePage = cpID
					SaveConfig()
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
	if vv.scrollBar != nil && vv.scrollBar.ProcessMouse(e) {
		return true
	}
	if e.WheelDirection != 0 {
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
func (vv *ViewerView) ResizeConsole(w, h int) {
	vv.SetPosition(0, vtui.FrameManager.WorkspaceTopInset(), w-1, h-2)
}

func (vv *ViewerView) Close() {
	if GlobalFileState != nil && vv.path != "" {
		GlobalFileState.SaveViewerStateAsync(FileStateKey(vv.vfs, vv.path), vv.TopOffset, vv.WrapMode, vv.HexMode)
	}
	if vv.backend != nil {
		vv.backend.Close()
	}
	vv.BaseFrame.Close()
	if vv.OnClose != nil {
		vv.OnClose()
	}
}

func (vv *ViewerView) GetKeyLabels() *vtui.KeySet {
	nextCp := vfs.GetNextFastSwitchCodepage(vv.Codepage)
	nextCpName := vfs.DisplayCodepageName(nextCp)

	fallbacks := &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.ViewerF1"), Msg("KeyBar.ViewerF2"), Msg("KeyBar.ViewerF3"), Msg("KeyBar.ViewerF4"),
			"", "", Msg("KeyBar.ViewerF7"), nextCpName, "", Msg("KeyBar.ViewerF10"),
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
