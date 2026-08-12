package main

import (
	"strings"
	"testing"

	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// restoreBars keeps the whole screen flag from leaking between tests: it
// lives on the frame manager, not on the frame.
func restoreBars(t *testing.T) {
	t.Helper()
	was := vtui.FrameManager.HideBars
	t.Cleanup(func() { vtui.FrameManager.HideBars = was })
}

func TestImageViewFullScreenIsAToggle(t *testing.T) {
	restoreBars(t)
	scr := newImageTestScreen(t)
	iv := newTestImageView(t, 100, 100)

	p, ok := iv.placementFor(scr)
	if !ok {
		t.Fatal("layout failed")
	}
	if p.Rows != 23 {
		t.Fatalf("with both bars the picture gets 23 rows, got %d", p.Rows)
	}

	e := &vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_F}
	e.ControlKeyState |= vtinput.LeftCtrlPressed
	if !iv.ProcessKey(e) {
		t.Fatal("Ctrl+F was not handled")
	}
	if !iv.full || !vtui.FrameManager.HideBars {
		t.Fatal("Ctrl+F did not reach the whole screen mode")
	}

	if !iv.ProcessKey(&vtinput.InputEvent{KeyDown: true, Char: 'f'}) {
		t.Fatal("F was not handled")
	}
	if iv.full || vtui.FrameManager.HideBars {
		t.Error("leaving the whole screen mode must give the bars back")
	}
	if p, _ = iv.placementFor(scr); p.Rows != 23 {
		t.Errorf("back to 23 rows, got %d", p.Rows)
	}
}

func TestImageViewCloseGivesTheBarsBack(t *testing.T) {
	restoreBars(t)
	iv := newTestImageView(t, 10, 10)

	iv.SetFullScreen(true)
	if !vtui.FrameManager.HideBars {
		t.Fatal("the flag did not reach the manager")
	}
	iv.Close()
	if vtui.FrameManager.HideBars {
		t.Error("a closed viewer must not leave the key bar hidden")
	}
}

func TestImageViewOverlayLines(t *testing.T) {
	iv := newTestImageView(t, 320, 200)
	iv.path = "photo.png"
	iv.decoder = "png"
	iv.fileSize, iv.sizeKnown = 4096, true

	lines := iv.overlayLines()
	if len(lines) != 5 {
		t.Fatalf("an unturned picture describes itself in five lines: %v", lines)
	}
	if lines[0] != "photo.png" || lines[1] != "320x200" || lines[3] != "png" {
		t.Errorf("the panel says %v", lines)
	}
	if !strings.Contains(lines[2], "4.0") {
		t.Errorf("the file size line is %q", lines[2])
	}

	// The panel describes what is on screen, not what came out of the decoder.
	iv.Rotate(90)
	lines = iv.overlayLines()
	if lines[1] != "200x320" {
		t.Errorf("after a quarter turn the size line is %q", lines[1])
	}
	if len(lines) != 6 || !strings.Contains(lines[5], "90") {
		t.Errorf("a turned picture has to say so: %v", lines)
	}

	unasked := newTestImageView(t, 8, 8)
	if got := unasked.overlayLines()[2]; got != "unknown size" {
		t.Errorf("a size nobody has asked the file system for is %q", got)
	}
}

func TestImageViewOverlayGoesOverThePicture(t *testing.T) {
	scr := newImageTestScreen(t)
	iv := newTestImageView(t, 100, 100)
	iv.path = "photo.png"
	iv.decoder = "png"

	if p, _ := iv.placementFor(scr); p.ZIndex != 0 {
		t.Fatalf("without the overlay the placement keeps the default z index, got %d", p.ZIndex)
	}

	if !iv.ProcessKey(&vtinput.InputEvent{KeyDown: true, Char: 'i'}) {
		t.Fatal("I was not handled")
	}
	p, _ := iv.placementFor(scr)
	if p.ZIndex >= 0 {
		t.Errorf("with the overlay up the picture has to go under the glyphs, got z %d", p.ZIndex)
	}

	scr.Graphics().BeginFrame()
	iv.Show(scr)
	scr.Graphics().EndFrame()

	if row := ScreenRow(scr, 1, 0, 20); !strings.Contains(row, "photo.png") {
		t.Errorf("the first line of the panel is %q", row)
	}
	if row := ScreenRow(scr, 2, 0, 20); !strings.Contains(row, "100x100") {
		t.Errorf("the second line of the panel is %q", row)
	}

	e := &vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_I}
	e.ControlKeyState |= vtinput.LeftCtrlPressed
	if !iv.ProcessKey(e) || iv.overlay {
		t.Error("Ctrl+I must switch the panel off again")
	}
}
