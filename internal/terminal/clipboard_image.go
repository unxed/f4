package terminal

import (
	"context"
	"errors"
	"image"

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

func ReadClipboardContents(ctx context.Context) (ClipboardContents, error) {
	contents, err := goclip.ReadContents(ctx)
	img := contents.Image
	if err != nil && !errors.Is(err, goclip.ErrNoImage) && !errors.Is(err, goclip.ErrUnsupportedFormat) && !errors.Is(err, goclip.ErrUnavailable) {
		return ClipboardContents{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClipboardContents{}, err
	}
	if img != nil {
		files, cut, _ := readFileClipboard(ctx)
		if len(files) == 0 {
			payload := vtui.ParseURIList(contents.Text)
			files = payload.Paths
		}
		return ClipboardContents{Image: img, Text: contents.Text, Files: files, FilesCut: cut}, nil
	}

	// Keep vtui's far2l, authorization and terminal integrations for text.
	text := vtui.GetClipboard()
	files, cut, _ := readFileClipboard(ctx)
	if len(files) == 0 {
		payload := vtui.ParseURIList(contents.Text)
		if len(payload.Paths) == 0 {
			payload = vtui.ParseURIList(text)
		}
		files = payload.Paths
	}
	return ClipboardContents{Image: img, Text: text, Files: files, FilesCut: cut}, nil
}
