package terminal

import (
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os/exec"

	"github.com/unxed/goclip"
	"github.com/unxed/vtui"
)

// ClipboardContents captures the representations offered for one paste.
type ClipboardContents struct {
	Image    image.Image
	Text     string
	Files    []string
	FilesCut bool
}

// imageReaderRejected reports that goclip's image reader ran and exited with
// failure instead of failing to start. On macOS goclip reads the image through
// osascript, which exits 1 whenever the pasteboard holds no image (text, copied
// files, nothing): that is "no image", not a broken clipboard. Verified on a
// macOS runner for all three cases (f4#2007).
func imageReaderRejected(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

// imageDecodeFailed reports that goclip got bytes for an image target but they
// are not a decodable image. X11 selection owners such as xclip answer any
// requested target (image/png included) with whatever they hold, so a text-only
// clipboard arrives as "image: unknown format" (f4#2234). The error is only
// "no image" when text or files can still be pasted; ReadClipboardContents
// returns it unchanged when there is nothing else to paste.
func imageDecodeFailed(err error) bool {
	var pngFormat png.FormatError
	var pngUnsupported png.UnsupportedError
	var jpegFormat jpeg.FormatError
	var jpegUnsupported jpeg.UnsupportedError
	return errors.Is(err, image.ErrFormat) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.As(err, &pngFormat) || errors.As(err, &pngUnsupported) ||
		errors.As(err, &jpegFormat) || errors.As(err, &jpegUnsupported)
}

func ReadClipboardContents(ctx context.Context) (ClipboardContents, error) {
	contents, err := goclip.ReadContents(ctx)
	img := contents.Image
	decodeFailed := err != nil && imageDecodeFailed(err)
	noImage := errors.Is(err, goclip.ErrNoImage) || errors.Is(err, goclip.ErrUnsupportedFormat) || errors.Is(err, goclip.ErrUnavailable) || imageReaderRejected(err) || decodeFailed
	if err != nil && !noImage {
		return ClipboardContents{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClipboardContents{}, err
	}
	if img != nil {
		files, cut, _ := readNativeFileClipboard(ctx)
		if len(files) == 0 {
			payload := vtui.ParseURIList(contents.Text)
			files = payload.Paths
		}
		return ClipboardContents{Image: img, Text: contents.Text, Files: files, FilesCut: cut}, nil
	}

	// Keep vtui's far2l, authorization and terminal integrations for text.
	text := vtui.GetClipboard()
	files, cut, _ := readNativeFileClipboard(ctx)
	if len(files) == 0 {
		payload := vtui.ParseURIList(contents.Text)
		if len(payload.Paths) == 0 {
			payload = vtui.ParseURIList(text)
		}
		files = payload.Paths
	}
	if decodeFailed && text == "" && len(files) == 0 {
		return ClipboardContents{}, err
	}
	return ClipboardContents{Image: img, Text: text, Files: files, FilesCut: cut}, nil
}
