package app

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/pdftext"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/vtvibe"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

// aiReadClipboard reads what the clipboard holds; a variable for tests.
var aiReadClipboard = terminal.ReadClipboardContents

// aiAttachFileLimit is the largest copied file put into ai://ctx as it is,
// the size of a file in the AI panel's tree.
const aiAttachFileLimit = 8 << 20

// PasteClipboard is Ctrl+V and Shift+Ins in the AI chat (Panel.Paste hands
// them here). An image on the clipboard -- a screenshot -- is put into
// ai://ctx as a PNG file, and so are files copied in a file manager, a PDF as
// its text, without saving them anywhere first (Alexey5112, f4#1842). Plain
// text is typed into the input line, as before.
func (cp *AIChatPanel) PasteClipboard() bool {
	session := cp.getSession()
	done := terminal.TrackClipboardRead()
	vtui.RunAsync(func(task *vtui.TaskContext) {
		defer done()
		defer task.Cancel()
		contents, err := aiReadClipboard(task.Context)
		var attached, failed []string
		if err == nil {
			attached, failed = aiAttachClipboard(task.Context, session, contents)
		}
		task.RunOnUI(func() {
			if len(attached) > 0 {
				session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.ClipboardAttached"), strings.Join(attached, ", ")))
			}
			if len(failed) > 0 {
				session.Note("assistant", fmt.Sprintf(i18n.Msg("AI.ClipboardNotAttached"), strings.Join(failed, "; ")))
			}
			if len(attached) > 0 || len(failed) > 0 {
				return
			}
			text := contents.Text
			if err != nil || text == "" {
				text = vtui.GetClipboard()
			}
			cp.typeText(text)
		})
	})
	return true
}

// typeText puts text into the input line the way a terminal paste does.
func (cp *AIChatPanel) typeText(text string) {
	if text == "" || cp.Input == nil {
		return
	}
	cp.Input.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType, PasteStart: true})
	for _, r := range text {
		cp.Input.ProcessKey(&vtinput.InputEvent{Type: vtinput.KeyEventType, KeyDown: true, Char: r})
	}
	cp.Input.ProcessKey(&vtinput.InputEvent{Type: vtinput.PasteEventType})
}

// aiAttachClipboard puts the clipboard's image and files into ai://ctx and
// returns the names they got there, and what could not be attached and why.
// Text alone attaches nothing.
func aiAttachClipboard(ctx context.Context, session *vtvibe.Session, contents terminal.ClipboardContents) (attached, failed []string) {
	add := func(name string, data []byte) {
		got, err := session.AddContextFile(name, data)
		if err != nil {
			failed = append(failed, name+": "+err.Error())
			return
		}
		attached = append(attached, got)
	}
	if contents.Image != nil {
		var buf bytes.Buffer
		if err := png.Encode(&buf, contents.Image); err != nil {
			failed = append(failed, "image: "+err.Error())
		} else {
			add("clipboard-"+time.Now().Format("20060102-150405")+".png", buf.Bytes())
		}
	}
	for _, file := range contents.Files {
		if ctx.Err() != nil {
			break
		}
		name := filepath.Base(file)
		data, err := aiAttachableFile(file)
		if err != nil {
			failed = append(failed, name+": "+err.Error())
			continue
		}
		if strings.EqualFold(filepath.Ext(name), ".pdf") {
			res, err := pdftext.Extract(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				failed = append(failed, name+": "+err.Error())
				continue
			}
			// The model reads text, not the bytes of a PDF.
			add(name+".md", []byte(pdftext.Report(res, name)))
			continue
		}
		if len(data) > aiAttachFileLimit {
			failed = append(failed, fmt.Sprintf("%s: %s", name, i18n.Msg("AI.ClipboardTooLarge")))
			continue
		}
		add(name, data)
	}
	return attached, failed
}

// aiAttachableFile reads a copied file; a folder is not attached.
func aiAttachableFile(file string) ([]byte, error) {
	st, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s", i18n.Msg("AI.ClipboardFolder"))
	}
	if st.Size() > pdftext.MaxFileSize {
		return nil, fmt.Errorf("%s", i18n.Msg("AI.ClipboardTooLarge"))
	}
	return os.ReadFile(file) // #nosec G304 -- a file the user copied to the clipboard
}
