package panel

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/internal/toast"
	"github.com/unxed/f4/vfs"
)

// fileClipboard is what "Copy files to clipboard" and "Cut files to
// clipboard" leave behind (f4#1767): the files themselves stay where they
// are, and the paste that follows copies or moves them into the directory of
// the active panel, through the same file operations as F5 and F6.
//
// The system clipboard gets the paths of the files as text, so other programs
// see something useful. That text is also how a paste knows the clipboard is
// still ours: when the system clipboard holds anything else by then, the user
// has copied something newer, and the remembered files are dropped instead of
// being pasted over it.
type fileClipboard struct {
	source vfs.VFS
	base   string
	names  []string
	paths  []string
	cut    bool
	text   string
}

// runFileClipboardOp starts the copy or move a paste stands for. It is a
// variable so a test can look at the request instead of starting a transfer.
var runFileClipboardOp = func(source, target vfs.VFS, base string, names []string, dest string, move bool, done func()) {
	go fileops.ExecuteFileOpAt(source, target, base, names, dest, move, config.App.DefaultFileOpMode, done)
}

// PanelCanCopyFilesToClipboard reports whether the active panel names
// anything to put on the clipboard. A command line with text in it keeps
// Ctrl+C for itself: that text, not the files, is what the key is aimed at.
func PanelCanCopyFilesToClipboard(pf *PanelsFrame) bool {
	if pf == nil || !pf.ShowPanels {
		return false
	}
	active := pf.GetActivePanel()
	if active == nil || active.Vfs == nil || !pf.CmdLine.IsEmpty() {
		return false
	}
	return len(active.GetSelectedNames()) > 0
}

// ActionCopyFilesToClipboard remembers the selected files of the active
// panel (the one under the cursor when nothing is marked) for a later paste;
// with cut set, the paste moves them instead of copying.
func ActionCopyFilesToClipboard(pf *PanelsFrame, cut bool) bool {
	if pf == nil || !pf.ShowPanels {
		return false
	}
	active := pf.GetActivePanel()
	if active == nil || active.Vfs == nil {
		return false
	}
	names := active.GetSelectedNames()
	if len(names) == 0 {
		return false
	}
	base := active.Vfs.GetPath()
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = active.Vfs.Join(base, name)
	}
	clip := &fileClipboard{
		source: active.Vfs,
		base:   base,
		names:  names,
		paths:  paths,
		cut:    cut,
		text:   strings.Join(paths, "\n"),
	}
	pf.fileClip = clip
	terminal.SetF4FileClipboard(clip.text, paths, cut)

	key := "Panel.FileClipboard.CopiedFmt"
	if cut {
		key = "Panel.FileClipboard.CutFmt"
	}
	toast.Show(fmt.Sprintf(i18n.Msg(key), len(names)), 3*time.Second)
	return true
}

// normalizeClipboardText makes two readings of the same clipboard compare
// equal however a terminal or a clipboard tool treated the line breaks and the
// trailing newline.
func normalizeClipboardText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, "\r\n")
}

// pasteFileClipboard is the first thing a paste asks, with what the system
// clipboard holds (text) or why it could not be read (readErr). It reports
// whether the paste was spent on the remembered files; false sends the paste
// on to the usual text and image handling.
func (pf *PanelsFrame) pasteFileClipboard(text string, readErr error) bool {
	return pf.pasteFileClipboardContents(terminal.ClipboardContents{Text: text}, readErr)
}

func sameFileClipboardPaths(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if filepath.Clean(left[i]) != filepath.Clean(right[i]) {
			return false
		}
	}
	return true
}

func (pf *PanelsFrame) pasteFileClipboardContents(contents terminal.ClipboardContents, readErr error) bool {
	clip := pf.fileClip
	// Text typed on the command line is what Ctrl+V is aimed at then.
	if !pf.CmdLine.IsEmpty() {
		return false
	}
	if clip != nil {
		matches := len(contents.Files) > 0 && sameFileClipboardPaths(contents.Files, clip.paths)
		// An unreadable clipboard (no clipboard tool, a terminal that refuses)
		// cannot say the files are stale, and the remembered files are still what
		// the user asked for.
		if !matches && readErr == nil && normalizeClipboardText(contents.Text) != normalizeClipboardText(clip.text) {
			pf.fileClip = nil
			clip = nil
		}
		if clip != nil {
			return pf.pasteRememberedFileClipboard(clip)
		}
	}
	if len(contents.Files) == 0 {
		return false
	}
	return pf.pasteExternalFileClipboard(contents.Files, contents.FilesCut)
}

func (pf *PanelsFrame) pasteRememberedFileClipboard(clip *fileClipboard) bool {
	target := pf.GetActivePanel()
	if target == nil || target.Vfs == nil {
		return false
	}
	dest := target.Vfs.GetPath()
	if dest != "" && !strings.HasSuffix(dest, "/") && !strings.HasSuffix(dest, "\\") {
		sep := "/"
		if _, local := target.Vfs.(*vfs.OSVFS); local && runtime.GOOS == "windows" {
			sep = "\\"
		}
		dest += sep
	}
	if clip.cut {
		// A move takes the files away from where they were remembered, so the
		// clipboard has nothing left to paste again.
		pf.fileClip = nil
	}
	done := func() {
		if !pf.Closed {
			pf.RefreshAll()
		}
	}
	runFileClipboardOp(clip.source, target.Vfs, clip.base, clip.names, dest, clip.cut, done)
	return true
}

func (pf *PanelsFrame) pasteExternalFileClipboard(paths []string, move bool) bool {
	target := pf.GetActivePanel()
	if target == nil || target.Vfs == nil {
		return false
	}
	dest := target.Vfs.GetPath()
	if dest != "" && !strings.HasSuffix(dest, "/") && !strings.HasSuffix(dest, "\\") {
		sep := "/"
		if _, local := target.Vfs.(*vfs.OSVFS); local && runtime.GOOS == "windows" {
			sep = "\\"
		}
		dest += sep
	}
	groups := make(map[string][]string)
	var order []string
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || path == "" || !filepath.IsAbs(path) {
			continue
		}
		dir, name := filepath.Dir(path), filepath.Base(path)
		if _, ok := groups[dir]; !ok {
			order = append(order, dir)
		}
		groups[dir] = append(groups[dir], name)
	}
	if len(order) == 0 {
		return false
	}
	done := func() {
		if !pf.Closed {
			pf.RefreshAll()
		}
	}
	for _, dir := range order {
		source := vfs.NewOSVFS(dir)
		runFileClipboardOp(source, target.Vfs, dir, groups[dir], dest, move, done)
	}
	return true
}
