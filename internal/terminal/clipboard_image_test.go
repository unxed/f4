package terminal

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os/exec"
	"testing"

	"github.com/unxed/goclip"
	"github.com/unxed/vtui"
)

type imageClipboardFixture struct {
	contents goclip.Contents
	err      error
}

func (*imageClipboardFixture) Name() string              { return "clipboard-fixture" }
func (*imageClipboardFixture) Available() bool           { return true }
func (*imageClipboardFixture) ReadText() (string, error) { return "stale", nil }
func (*imageClipboardFixture) WriteText(string) error    { return nil }
func (*imageClipboardFixture) Clear() error              { return nil }
func (d *imageClipboardFixture) ReadContents(context.Context) (goclip.Contents, error) {
	return d.contents, d.err
}

func TestReadClipboardContentsUsesActualImageTextAndPreservesTextFallback(t *testing.T) {
	before := goclip.ActiveDriver()
	defer goclip.SetActiveDriver(before)
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	goclip.SetActiveDriver(&imageClipboardFixture{contents: goclip.Contents{Image: img, Text: "actual"}})
	got, err := ReadClipboardContents(context.Background())
	if err != nil || got.Image != img || got.Text != "actual" {
		t.Fatalf("contents = %+v, %v", got, err)
	}
	vtui.SetClipboard("fallback")
	goclip.SetActiveDriver(goclip.NewFileDriver(""))
	got, err = ReadClipboardContents(context.Background())
	if err != nil || got.Image != nil || got.Text != "fallback" {
		t.Fatalf("text fallback = %+v, %v", got, err)
	}
	failure := errors.New("invalid image data")
	goclip.SetActiveDriver(&imageClipboardFixture{err: failure})
	if _, err := ReadClipboardContents(context.Background()); !errors.Is(err, failure) {
		t.Fatal("image read error hidden", err)
	}
}

// On macOS goclip reads images through osascript, which exits 1 when the
// pasteboard holds text, files or nothing. That must not block pasting them
// (f4#2007); a failure to run the reader at all still must be shown.
func TestReadClipboardContentsImageReaderExitMeansNoImage(t *testing.T) {
	before := goclip.ActiveDriver()
	defer goclip.SetActiveDriver(before)
	exit := fmt.Errorf("reading clipboard via /usr/bin/osascript: %w", &exec.ExitError{})
	goclip.SetActiveDriver(&imageClipboardFixture{err: exit})
	vtui.SetClipboard("typed text")
	got, err := ReadClipboardContents(context.Background())
	if err != nil || got.Image != nil || got.Text != "typed text" {
		t.Fatalf("reader exit = %+v, %v", got, err)
	}
	missing := fmt.Errorf("reading clipboard via /usr/bin/osascript: %w", &exec.Error{Name: "osascript", Err: exec.ErrNotFound})
	goclip.SetActiveDriver(&imageClipboardFixture{err: missing})
	if _, err := ReadClipboardContents(context.Background()); !errors.Is(err, exec.ErrNotFound) {
		t.Fatal("reader that cannot start was hidden", err)
	}
}

// X11 selection owners such as xclip answer an image/png request with their
// text, so goclip's decoder fails with "image: unknown format" although the
// clipboard holds only text (f4#2234). That must paste the text; with nothing
// else to paste the decode error is still shown.
func TestReadClipboardContentsUndecodableImageFallsBackToText(t *testing.T) {
	before := goclip.ActiveDriver()
	defer goclip.SetActiveDriver(before)
	cases := map[string]error{
		"unknown format":   fmt.Errorf("reading clipboard image: %w", image.ErrFormat),
		"truncated":        fmt.Errorf("reading clipboard image: %w", io.ErrUnexpectedEOF),
		"png corrupt":      fmt.Errorf("reading clipboard image: %w", png.FormatError("bad chunk")),
		"png unsupported":  fmt.Errorf("reading clipboard image: %w", png.UnsupportedError("bit depth")),
		"jpeg corrupt":     fmt.Errorf("reading clipboard image: %w", jpeg.FormatError("bad marker")),
		"jpeg unsupported": fmt.Errorf("reading clipboard image: %w", jpeg.UnsupportedError("progressive")),
	}
	for name, decodeErr := range cases {
		t.Run(name, func(t *testing.T) {
			if !imageDecodeFailed(decodeErr) {
				t.Fatalf("%v not classified as a decode failure", decodeErr)
			}
			goclip.SetActiveDriver(&imageClipboardFixture{err: decodeErr})
			vtui.SetClipboard("/home/user/Downloads")
			got, err := ReadClipboardContents(context.Background())
			if err != nil || got.Image != nil || got.Text != "/home/user/Downloads" {
				t.Fatalf("text-only clipboard = %+v, %v", got, err)
			}
			vtui.SetClipboard("")
			if _, err := ReadClipboardContents(context.Background()); !errors.Is(err, decodeErr) {
				t.Fatalf("empty clipboard hid the decode error: %v", err)
			}
		})
	}
	if imageDecodeFailed(errors.New("invalid image data")) {
		t.Fatal("unrelated error classified as a decode failure")
	}
}
