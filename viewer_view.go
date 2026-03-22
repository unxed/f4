package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
	"github.com/mattn/go-runewidth"
)

// ViewerView is a high-performance file viewer component.
type ViewerView struct {
	vtui.ScreenObject
	backend *ViewerBackend
	path    string

	HexMode   bool
	WrapMode  bool
	TopOffset int64 // Current byte offset of the first visible line

	// For Text mode: offsets of lines currently on screen
	lineOffsets []int64
	done        bool
}

func NewViewerView(path string) (*ViewerView, error) {
	backend, err := NewViewerBackend(path)
	if err != nil {
		return nil, err
	}
	vv := &ViewerView{
		backend:  backend,
		path:     path,
		WrapMode: true,
	}
	vv.SetCanFocus(true)
	vv.SetFocus(true)
	return vv, nil
}

func (vv *ViewerView) Show(scr *vtui.ScreenBuf) {
	vv.ScreenObject.Show(scr)
	vv.DisplayObject(scr)
}

func (vv *ViewerView) DisplayObject(scr *vtui.ScreenBuf) {
	if !vv.IsVisible() {
		return
	}

	width := vv.X2 - vv.X1 + 1
	height := vv.Y2 - vv.Y1 + 1

	bgAttr := vtui.Palette[ColViewerText]

	// 1. Draw Background
	scr.FillRect(vv.X1, vv.Y1, vv.X2, vv.Y2, ' ', bgAttr)

	if vv.HexMode {
		vv.renderHex(scr, width, height)
	} else {
		vv.renderText(scr, width, height)
	}

	vv.drawStatus(scr)
}

func (vv *ViewerView) renderHex(scr *vtui.ScreenBuf, width, height int) {
	attr := vtui.Palette[ColViewerText]
	offAttr := vtui.Palette[ColViewerArrows]

	currOffset := vv.TopOffset &^ 0xF // Align to 16 bytes

	for y := 0; y < height; y++ {
		if currOffset >= vv.backend.Size() {
			break
		}

		data, _ := vv.backend.ReadAt(currOffset, 16)
		line := fmt.Sprintf("%010X: ", currOffset)
		scr.Write(vv.X1, vv.Y1+y, vtui.StringToCharInfo(line, offAttr))

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
		scr.Write(vv.X1+12, vv.Y1+y, vtui.StringToCharInfo(hexStr, attr))

		// ASCII part
		asciiStr := "│ "
		for i := 0; i < len(data); i++ {
			r := rune(data[i])
			if r < 32 || r > 126 {
				r = '.'
			}
			asciiStr += string(r)
		}
		scr.Write(vv.X1+12+50, vv.Y1+y, vtui.StringToCharInfo(asciiStr, attr))

		currOffset += 16
	}
}

func (vv *ViewerView) renderText(scr *vtui.ScreenBuf, width, height int) {
	attr := vtui.Palette[ColViewerText]
	currOffset := vv.TopOffset
	vv.lineOffsets = vv.lineOffsets[:0]

	for y := 0; y < height; y++ {
		vv.lineOffsets = append(vv.lineOffsets, currOffset)
		if currOffset >= vv.backend.Size() {
			break
		}

		// Read a generous chunk to handle wrapping
		data, _ := vv.backend.ReadAt(currOffset, width*4)
		if len(data) == 0 {
			break
		}

		lineLen := 0
		visualWidth := 0
		foundNewline := false

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

			rw := runewidth.RuneWidth(r)
			if vv.WrapMode && visualWidth+rw > width {
				// Wrap occurred
				break
			}
			visualWidth += rw
			lineLen += size
		}

		scr.Write(vv.X1, vv.Y1+y, vtui.StringToCharInfo(string(data[:lineLen]), attr))
		currOffset += int64(lineLen)

		if !foundNewline && !vv.WrapMode {
			// In no-wrap mode, we must consume until the actual newline
			tempOff := currOffset
			for {
				b, err := vv.backend.ReadAt(tempOff, 1024)
				if err != nil || len(b) == 0 { break }
				found := false
				for i, char := range b {
					if char == '\n' {
						tempOff += int64(i + 1)
						found = true
						break
					}
				}
				if found { break }
				tempOff += int64(len(b))
			}
			currOffset = tempOff
		}
	}
}

func (vv *ViewerView) drawStatus(scr *vtui.ScreenBuf) {

	attr := vtui.Palette[ColViewerStatus]
	scr.FillRect(vv.X1, vv.Y1, vv.X2, vv.Y1, ' ', attr)

	percent := 0
	if vv.backend.Size() > 0 {
		// Calculate percentage based on current view position relative to total file size.
		// We add the view height so that 100% is reached when the bottom of the file
		// is visible, not just the top.
		viewHeight := int64(vv.Y2 - vv.Y1)
		curr := vv.TopOffset + viewHeight
		if curr > vv.backend.Size() {
			curr = vv.backend.Size()
		}
		percent = int((curr * 100) / vv.backend.Size())
	}

	mode := Msg("Viewer.ModeText")
	if vv.HexMode {
		mode = Msg("Viewer.ModeHex")
	}

	status := fmt.Sprintf(" %s │ %s │ %d%% ", vv.path, mode, percent)
	scr.Write(vv.X1, vv.Y1, vtui.StringToCharInfo(status, attr))
}

func (vv *ViewerView) ProcessKey(e *vtinput.InputEvent) bool {
	if !e.KeyDown {
		return false
	}

	height := int64(vv.Y2 - vv.Y1 + 1)
	step := int64(1)
	if vv.HexMode {
		step = 16
	}

	switch e.VirtualKeyCode {
	case vtinput.VK_ESCAPE, vtinput.VK_F10, vtinput.VK_F3:
		vv.done = true
		return true

	case vtinput.VK_F2:
		vv.WrapMode = !vv.WrapMode
		return true

	case vtinput.VK_F4:
		vv.HexMode = !vv.HexMode
		if vv.HexMode {
			vv.TopOffset &= ^0xF
		}
		return true

	case vtinput.VK_DOWN:
		if vv.HexMode {
			vv.TopOffset += step
		} else if len(vv.lineOffsets) > 1 {
			vv.TopOffset = vv.lineOffsets[1]
		}
		if vv.TopOffset >= vv.backend.Size() {
			vv.TopOffset = vv.backend.Size() - 1
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
			vv.TopOffset += step * height
		} else if len(vv.lineOffsets) > 0 {
			// Use the offset of the last visible line to scroll
			vv.TopOffset = vv.lineOffsets[len(vv.lineOffsets)-1]
		}
		if vv.TopOffset >= vv.backend.Size() {
			vv.TopOffset = vv.backend.Size() - 1
		}
		return true

	case vtinput.VK_PRIOR: // PgUp
		if vv.HexMode {
			vv.TopOffset -= step * height
		} else {
			for i := 0; i < int(height); i++ {
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
		if vv.HexMode {
			vv.TopOffset = (vv.backend.Size() - 1) & ^0xF
		} else {
			vv.TopOffset = vv.backend.FindLineStart(vv.backend.Size() - 1)
		}
		return true
	}

	return false
}

func (vv *ViewerView) ProcessMouse(e *vtinput.InputEvent) bool { return false }
func (vv *ViewerView) ResizeConsole(w, h int)                 { vv.SetPosition(0, 0, w-1, h-2) }
func (vv *ViewerView) GetType() vtui.FrameType               { return vtui.TypeUser + 3 }
func (vv *ViewerView) SetExitCode(c int)                     { vv.done = true }
func (vv *ViewerView) IsDone() bool                          { return vv.done }
func (vv *ViewerView) IsBusy() bool                          { return false }
func (vv *ViewerView) IsModal() bool                         { return false }
func (vv *ViewerView) GetWindowNumber() int                  { return 0 }
func (vv *ViewerView) SetWindowNumber(n int)                 {}
func (vv *ViewerView) RequestFocus() bool                    { return true }
func (vv *ViewerView) Close()                                { vv.done = true }
func (vv *ViewerView) HasShadow() bool                       { return false }
func (vv *ViewerView) GetKeyLabels() *vtui.KeySet {
	return &vtui.KeySet{
		Normal: vtui.KeyBarLabels{
			Msg("KeyBar.ViewerF1"), Msg("KeyBar.ViewerF2"), Msg("KeyBar.ViewerF3"), Msg("KeyBar.ViewerF4"),
			"", "", Msg("KeyBar.ViewerF7"), "", "", Msg("KeyBar.ViewerF10"),
		},
	}
}