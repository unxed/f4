package app

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/vtui"
)

// f4#1842 (Alexey5112): a screenshot or files copied in a file manager are
// pasted into the AI chat as files of ai://ctx, not as text.
func TestAIClipboardAttachesImageAndFiles(t *testing.T) {
	dir := t.TempDir()
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, aiAttachFileLimit+1), 0o600); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)

	session := vtvibe.NewSession()
	attached, failed := aiAttachClipboard(context.Background(), session, terminal.ClipboardContents{
		Image: img,
		Files: []string{note, dir, big},
	})
	if len(attached) != 2 || !strings.HasSuffix(attached[0], ".png") || attached[1] != "note.txt" {
		t.Errorf("attached = %v, want the screenshot PNG and note.txt", attached)
	}
	if len(failed) != 2 {
		t.Errorf("failed = %v, want the folder and the file that is too large", failed)
	}
	files := strings.Join(session.ContextFiles(), ",")
	if !strings.Contains(files, "note.txt") || !strings.Contains(files, ".png") {
		t.Errorf("ai://ctx holds %s", files)
	}

	// Text alone is not attached: it goes to the input line.
	if a, f := aiAttachClipboard(context.Background(), session, terminal.ClipboardContents{Text: "just text"}); len(a)+len(f) != 0 {
		t.Errorf("text alone attached %v / %v", a, f)
	}
	cp := &AIChatPanel{ChatWindow: vtui.NewChatWindow(0, 0, 40, 10, "AI")}
	cp.typeText("just text")
	if got := cp.Input.GetText(); got != "just text" {
		t.Errorf("input line = %q, want the pasted text", got)
	}
}
