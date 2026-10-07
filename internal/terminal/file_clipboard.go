package terminal

import (
	"context"
	"errors"
	"time"

	"github.com/unxed/vtui"
)

var errFileClipboardUnavailable = errors.New("file clipboard is unavailable")

// SetF4FileClipboard publishes both the existing text representation and the
// native file representation. The text copy remains the fallback for
// terminals and clipboard providers which do not understand files.
func SetF4FileClipboard(text string, paths []string, cut bool) {
	SetF4Clipboard(text)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := setFileClipboard(ctx, text, paths, cut); err != nil {
		vtui.DebugLog("clipboard files: %v", err)
	}
}
