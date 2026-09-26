package app

import (
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/vtui"
)

type coverageBatch46PositionRenderer struct {
	x, y int
	ok   bool
}

func (coverageBatch46PositionRenderer) Render([]vtui.CharInfo, []vtui.CharInfo, int, int, bool) {}
func (coverageBatch46PositionRenderer) SetCursor(int, int, bool, vtui.CursorShape)              {}
func (coverageBatch46PositionRenderer) SetPalette(*[256]uint32)                                 {}
func (coverageBatch46PositionRenderer) SetWindowTitle(string)                                   {}
func (coverageBatch46PositionRenderer) Flush()                                                  {}

func (r coverageBatch46PositionRenderer) WindowPosition() (int, int, bool) {
	return r.x, r.y, r.ok
}

func prepareWindowCaptureCoverageBatch46(t *testing.T, backend string, renderer vtui.SurfaceRenderer) {
	t.Helper()
	oldFrameManager := vtui.FrameManager
	oldBackend := vtui.ActiveBackend()
	oldApp := config.App
	vtui.FrameManager = vtui.NewFrameManager()
	managedFrameManager := vtui.FrameManager
	t.Cleanup(func() {
		managedFrameManager.Shutdown()
		vtui.FrameManager = oldFrameManager
		vtui.SetActiveBackend(oldBackend)
		config.App = oldApp
	})

	scr := vtui.NewSilentScreenBuf()
	scr.Renderer = renderer
	vtui.FrameManager.Init(scr)
	vtui.SetActiveBackend(backend)
}

func TestCaptureCurrentWindowSizeSkipsEmptyBackendCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "", nil)
	config.App.GuiCols, config.App.GuiRows = 11, 12
	if captureCurrentWindowSize() {
		t.Fatal("captureCurrentWindowSize changed settings for an empty backend")
	}
	if config.App.GuiCols != 11 || config.App.GuiRows != 12 {
		t.Fatalf("GUI size changed unexpectedly: %dx%d", config.App.GuiCols, config.App.GuiRows)
	}
}

func TestCaptureCurrentWindowSizeSkipsNilFrameManagerCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", nil)
	vtui.FrameManager = nil
	if captureCurrentWindowSize() {
		t.Fatal("captureCurrentWindowSize reported a change without a FrameManager")
	}
}

func TestCaptureCurrentWindowSizeSkipsUnchangedSizeCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", nil)
	config.App.GuiCols = vtui.FrameManager.GetScreenSize()
	config.App.GuiRows = vtui.FrameManager.GetScreenHeight()
	if captureCurrentWindowSize() {
		t.Fatal("captureCurrentWindowSize reported an unchanged size")
	}
}

func TestCaptureCurrentWindowSizeStoresChangedSizeCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", nil)
	vtui.FrameManager.Resize(123, 45)
	config.App.GuiCols, config.App.GuiRows = 1, 1
	if !captureCurrentWindowSize() {
		t.Fatal("captureCurrentWindowSize did not report a changed size")
	}
	if config.App.GuiCols != 123 || config.App.GuiRows != 45 {
		t.Fatalf("captured GUI size = %dx%d, want 123x45", config.App.GuiCols, config.App.GuiRows)
	}
}

func TestCaptureCurrentWindowPositionSkipsEmptyBackendCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "", coverageBatch46PositionRenderer{x: 4, y: 5, ok: true})
	if captureCurrentWindowPosition() {
		t.Fatal("captureCurrentWindowPosition changed settings for an empty backend")
	}
}

func TestCaptureCurrentWindowPositionSkipsNilFrameManagerCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", nil)
	vtui.FrameManager = nil
	if captureCurrentWindowPosition() {
		t.Fatal("captureCurrentWindowPosition reported a change without a FrameManager")
	}
}

func TestCaptureCurrentWindowPositionSkipsUnsupportedRendererCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", nil)
	if captureCurrentWindowPosition() {
		t.Fatal("captureCurrentWindowPosition reported a position for a renderer without position support")
	}
}

func TestCaptureCurrentWindowPositionStoresCoordinatesCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", coverageBatch46PositionRenderer{x: 17, y: 29, ok: true})
	config.App.GuiPositionSaved = false
	if !captureCurrentWindowPosition() {
		t.Fatal("captureCurrentWindowPosition did not report a new position")
	}
	if !config.App.GuiPositionSaved || config.App.GuiPosX != 17 || config.App.GuiPosY != 29 {
		t.Fatalf("captured GUI position = (%d,%d), saved=%v; want (17,29), saved=true", config.App.GuiPosX, config.App.GuiPosY, config.App.GuiPositionSaved)
	}
}

func TestCaptureCurrentWindowPositionSkipsUnchangedCoordinatesCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "gui", coverageBatch46PositionRenderer{x: 17, y: 29, ok: true})
	config.App.GuiPositionSaved = true
	config.App.GuiPosX, config.App.GuiPosY = 17, 29
	if captureCurrentWindowPosition() {
		t.Fatal("captureCurrentWindowPosition reported unchanged coordinates")
	}
}

func TestSaveSessionWithOptionsSkipsDisabledGroupsCoverageBatch46(t *testing.T) {
	prepareWindowCaptureCoverageBatch46(t, "", nil)
	config.App.AutoSaveDialogSettings = false
	saveSessionWithOptions(false, false, false)
}
