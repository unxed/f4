package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/media"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/internal/viewer"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type actionTestViewerProvider struct {
	name     string
	frame    vtui.Frame
	err      error
	request  vfs.ViewerRequest
	openCall int
}

func (provider *actionTestViewerProvider) Name() string                   { return provider.name }
func (provider *actionTestViewerProvider) Priority() int                  { return 1 }
func (provider *actionTestViewerProvider) CanView(vfs.ViewerRequest) bool { return true }
func (provider *actionTestViewerProvider) OpenViewer(_ context.Context, request vfs.ViewerRequest) (vtui.Frame, error) {
	provider.request = request
	provider.openCall++
	return provider.frame, provider.err
}

// viewByTypeRenderer draws nothing and claims to display images, which is all
// ScreenBuf.SupportsGraphics asks of a renderer.
type viewByTypeRenderer struct{}

func (viewByTypeRenderer) Render(buf, shadow []vtui.CharInfo, width, height int, forceRedraw bool) {}
func (viewByTypeRenderer) SetCursor(x, y int, visible bool, shape vtui.CursorShape)                {}
func (viewByTypeRenderer) SetPalette(palette *[256]uint32)                                         {}
func (viewByTypeRenderer) SetWindowTitle(title string)                                             {}
func (viewByTypeRenderer) Flush()                                                                  {}
func (viewByTypeRenderer) RenderGraphics(layer *vtui.GraphicsLayer, buf, shadow []vtui.CharInfo, width, height int, forceRedraw bool) {
}

// Viewer settings -> "Open images and video in their own viewers" (issue #991).
// On a screen that can show pictures, a PNG opened for viewing goes to the image
// viewer while the option is on, and to the text/hex viewer when it is off.
func TestOpenViewerFollowsOpenAsSupportedType(t *testing.T) {
	for _, tc := range []struct {
		name   string
		byType bool
	}{
		{name: "on", byType: true},
		{name: "off", byType: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 25)
			scr.Renderer = viewByTypeRenderer{}
			scr.Graphics().SetProtocol(vtui.GraphicsNative)
			scr.Graphics().SetCellSize(10, 20)
			vtui.FrameManager.Init(scr)
			testutil.DrainPendingTasks()
			theme.SetDefaultF4Palette()
			if !scr.SupportsGraphics() {
				t.Fatal("the test screen does not report graphics support; the option would not be what decides")
			}

			old := config.App.ViewerOpenAsSupportedType
			t.Cleanup(func() { config.App.ViewerOpenAsSupportedType = old })
			config.App.ViewerOpenAsSupportedType = tc.byType

			dir := t.TempDir()
			path := filepath.Join(dir, "picture.png")
			writeViewByTypePNG(t, path)

			pf := panel.NewPanelsFrame()
			defer pf.Close()
			pf.ResizeConsole(80, 25)

			openViewerInternal(pf, vfs.NewOSVFS(dir), path)

			var imageView *media.ImageView
			var textView *viewer.ViewerView
			timeout := time.After(5 * time.Second)
			for imageView == nil && textView == nil {
				select {
				case task := <-vtui.FrameManager.TaskChan:
					task()
				case <-timeout:
					t.Fatal("neither the image viewer nor the text viewer opened")
				}
				for _, s := range vtui.FrameManager.Screens {
					for _, f := range s.Frames {
						switch v := f.(type) {
						case *media.ImageView:
							imageView = v
						case *viewer.ViewerView:
							textView = v
						}
					}
				}
			}
			if tc.byType && imageView == nil {
				t.Fatal("with the option on the picture opened in the text viewer, want the image viewer")
			}
			if !tc.byType && textView == nil {
				t.Fatal("with the option off the picture opened in the image viewer, want the text viewer")
			}
			if imageView != nil {
				imageView.Close()
			}
			if textView != nil {
				textView.Close()
			}
		})
	}
}

func TestOpenViewerInternalForceHex(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()
	theme.SetDefaultF4Palette()

	old := config.App.ViewerOpenAsSupportedType
	t.Cleanup(func() { config.App.ViewerOpenAsSupportedType = old })
	config.App.ViewerOpenAsSupportedType = true

	dir := t.TempDir()
	path := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(path, []byte("plain text"), 0600); err != nil {
		t.Fatal(err)
	}

	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	openViewerInternalMode(pf, vfs.NewOSVFS(dir), path, true)

	var vv *viewer.ViewerView
	timeout := time.After(5 * time.Second)
	for vv == nil {
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-timeout:
			t.Fatal("hex viewer did not open")
		}
		if found, _ := findOpenedViewer(vfs.NewOSVFS(dir), path); found != nil {
			vv = found
		}
	}
	defer vv.Close()
	if !vv.HexMode {
		t.Fatal("forced hex viewer opened in text mode")
	}
}

func TestActionViewFileOpensRegisteredViewerProvider(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	testutil.DrainPendingTasks()
	provider := &actionTestViewerProvider{name: "action-test-viewer", frame: vtui.NewMarkdownView("provider", "")}
	registration, err := vfs.RegisterViewerProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	pf, _ := setupPanelWithFile(t, "picture.png")
	defer pf.Close()
	actionViewFile(pf)

	if provider.openCall != 1 {
		t.Fatalf("provider OpenViewer called %d times, want 1", provider.openCall)
	}
	if provider.request.Path != filepath.Join(provider.request.VFS.GetPath(), "picture.png") || provider.request.Item.Name != "picture.png" {
		t.Fatalf("provider received request %#v, want selected path and item", provider.request)
	}
	if got := vtui.FrameManager.GetTopFrame(); got != provider.frame {
		t.Fatalf("top frame = %T, want registered provider frame %T", got, provider.frame)
	}
}

func TestActionViewFileFallsBackWhenViewerProviderDeclines(t *testing.T) {
	withStubHistoryProvider(t)
	provider := &actionTestViewerProvider{name: "action-test-viewer-decline", err: vfs.ErrViewerUnsupported}
	registration, err := vfs.RegisterViewerProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	pf, _ := setupPanelWithFile(t, "note.txt")
	defer pf.Close()
	actionViewFile(pf)

	if provider.openCall != 1 {
		t.Fatalf("provider OpenViewer called %d times, want 1", provider.openCall)
	}
	assertViewerOpened(t, pf, "note.txt")
}

func writeViewByTypePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(60 * x), G: uint8(60 * y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
