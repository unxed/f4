package viewer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

type recordingColorizer struct {
	context []string
	lines   []WindowLine
	closed  bool
	base    uint64
	baseSet bool
}

func (r *recordingColorizer) Request(context []string, lines []WindowLine) {
	r.context, r.lines = context, lines
}

func (r *recordingColorizer) LineAttrs(offset int64, text string) []uint64 {
	attrs := make([]uint64, len([]rune(text)))
	for i := range attrs {
		attrs[i] = 0x4200 + uint64(i)
	}
	return attrs
}

func (r *recordingColorizer) BaseAttr() (uint64, bool) { return r.base, r.baseSet }

func (r *recordingColorizer) Close() { r.closed = true }

// The viewer hands the colorizer the logical lines on screen with the lines
// above as context, and paints each cell with its rune's colour, wrapped rows
// included.
func TestViewerHighlightsTheLinesOnScreen(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("zero\none\ntwo abcdefgh\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldAuto, oldDefault := config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage
	config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = false, 65001
	t.Cleanup(func() { config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = oldAuto, oldDefault })

	rec := &recordingColorizer{}
	old := NewWindowColorizer
	NewWindowColorizer = func(string, string, uint64, func()) WindowColorizer { return rec }
	t.Cleanup(func() { NewWindowColorizer = old })

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	// The backend loads through UI tasks; run them until the file is there.
	deadline := time.After(2 * time.Second)
	for {
		if _, err := vv.Backend.ReadAt(0, int(vv.Backend.Size())); err == nil {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the file did not load")
		case <-time.After(5 * time.Millisecond):
		}
	}
	vv.WrapMode = true
	vv.TopOffset = 5 // "one"
	vv.SetPosition(0, 0, 9, 6)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(10, 7)
	vv.renderText(scr, 8, 4)

	if len(rec.context) != 1 || rec.context[0] != "zero" {
		t.Errorf("context %q, want the line above", rec.context)
	}
	if len(rec.lines) < 2 || rec.lines[0] != (WindowLine{Offset: 5, Text: "one"}) || rec.lines[1] != (WindowLine{Offset: 9, Text: "two abcdefgh"}) {
		t.Fatalf("lines %+v", rec.lines)
	}
	// Row 2 is the wrapped continuation of "two abcdefgh": its first cell is
	// the rune after the first row's eight.
	cont := scr.GetCell(vv.X1, vv.Y1+1+2)
	if cont.Attributes != 0x4200+8 {
		t.Errorf("continuation cell %q attr %#x, want the ninth rune's colour", rune(cont.Char), cont.Attributes)
	}
	vv.Close()
	if !rec.closed {
		t.Error("the colorizer was not closed with the viewer")
	}
}

// Once the colorizer says what it draws highlighted lines over, the rest of
// the text view and the scrollbar are drawn over it too. Left on Viewer.Text
// they showed as strips around lines Colorer paints on its style's own
// background (#1232).
func TestViewerDrawsAroundHighlightedLinesOnTheirBackground(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	theme.SetDefaultF4Palette()
	savedText, savedBar := vtui.Palette[theme.ColViewerText], vtui.Palette[theme.ColViewerScrollbar]
	t.Cleanup(func() {
		vtui.Palette[theme.ColViewerText], vtui.Palette[theme.ColViewerScrollbar] = savedText, savedBar
	})
	vtui.Palette[theme.ColViewerText] = vtui.SetRGBBoth(0, 0xEEEEEC, 0x555753)
	vtui.Palette[theme.ColViewerScrollbar] = vtui.SetRGBBoth(0, 0x34E2E2, 0x555753)

	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("zero\none\ntwo\nthree\nfour\nfive\nsix\nseven\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldAuto, oldDefault := config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage
	config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = false, 65001
	t.Cleanup(func() { config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = oldAuto, oldDefault })

	const colorerBg = 0x101010
	rec := &recordingColorizer{base: vtui.SetRGBBoth(0, 0xEEEEEC, colorerBg), baseSet: true}
	old := NewWindowColorizer
	NewWindowColorizer = func(string, string, uint64, func()) WindowColorizer { return rec }
	t.Cleanup(func() { NewWindowColorizer = old })

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := vv.Backend.ReadAt(0, int(vv.Backend.Size())); err == nil {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the file did not load")
		case <-time.After(5 * time.Millisecond):
		}
	}
	vv.WrapMode = true
	vv.SetPosition(0, 0, 9, 6)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(10, 7)
	vv.SetVisible(true)
	vv.DisplayObject(scr) // the first frame starts the colorizer
	vv.DisplayObject(scr)

	for _, c := range []struct {
		what string
		x, y int
	}{{"after the text of a line", 6, 1}, {"in the scrollbar", 9, 3}} {
		if bg := vtui.GetRGBBack(scr.GetCell(c.x, c.y).Attributes); bg != colorerBg {
			t.Errorf("background %s is #%06x, want the highlighted lines' #%06x", c.what, bg, colorerBg)
		}
	}
}

// f4 #1413: RefreshHighlighting drops the colorizer a window already built,
// so the next redraw calls NewWindowColorizer again — the mechanism
// Viewer.ToggleHighlighting (CtrlL) relies on to take effect on an already
// open viewer instead of only on the next one.
func TestViewerRefreshHighlightingRebuildsTheColorizer(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldAuto, oldDefault := config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage
	config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = false, 65001
	t.Cleanup(func() { config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = oldAuto, oldDefault })

	builds := 0
	var last *recordingColorizer
	old := NewWindowColorizer
	NewWindowColorizer = func(string, string, uint64, func()) WindowColorizer {
		builds++
		last = &recordingColorizer{}
		return last
	}
	t.Cleanup(func() { NewWindowColorizer = old })

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := vv.Backend.ReadAt(0, int(vv.Backend.Size())); err == nil {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the file did not load")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if got := vv.windowColorizer(); got == nil || builds != 1 {
		t.Fatalf("windowColorizer() built %d colorizer(s), want exactly 1", builds)
	}
	first := last
	if second := vv.windowColorizer(); second != first || builds != 1 {
		t.Fatalf("a second call rebuilt the colorizer (builds=%d) instead of reusing it", builds)
	}

	vv.RefreshHighlighting()
	if !first.closed {
		t.Error("RefreshHighlighting did not close the old colorizer")
	}
	if got := vv.windowColorizer(); got == nil || got == first || builds != 2 {
		t.Fatalf("after RefreshHighlighting, windowColorizer() built %d colorizer(s) (want 2) or reused the old one", builds)
	}
}

// f4 #1413: turning highlighting off then straight back on, without
// scrolling, used to leave the viewer dark. renderText skips re-requesting a
// window whose lines hash the same as the last one it asked a colorizer for
// (vv.highlightKey), so it never sends a request when what is on screen has
// not changed since the last time it was highlighted — even though
// RefreshHighlighting just swapped in a brand new, still-empty colorizer for
// the old one Close()d. Highlighting only reappeared once the user scrolled
// and the hash changed, which read as "выключает сразу, а включает с
// нескольких попыток".
func TestViewerRefreshHighlightingRerequestsAnUnchangedScreen(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("zero\none\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldAuto, oldDefault := config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage
	config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = false, 65001
	t.Cleanup(func() { config.App.ViewerAutodetectCodePage, config.App.ViewerDefaultCodePage = oldAuto, oldDefault })

	var built []*recordingColorizer
	old := NewWindowColorizer
	NewWindowColorizer = func(string, string, uint64, func()) WindowColorizer {
		rec := &recordingColorizer{}
		built = append(built, rec)
		return rec
	}
	t.Cleanup(func() { NewWindowColorizer = old })

	vv, err := NewViewerView(context.Background(), vfs.NewOSVFS(root), path)
	if err != nil {
		t.Fatalf("NewViewerView: %v", err)
	}
	defer vv.Close()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := vv.Backend.ReadAt(0, int(vv.Backend.Size())); err == nil {
			break
		}
		select {
		case task := <-vtui.FrameManager.TaskChan:
			task()
		case <-deadline:
			t.Fatal("the file did not load")
		case <-time.After(5 * time.Millisecond):
		}
	}
	vv.WrapMode = true
	vv.SetPosition(0, 0, 9, 6)
	scr := vtui.NewSilentScreenBuf()
	scr.AllocBuf(10, 7)

	// First render: on-screen lines are requested from the first colorizer.
	vv.renderText(scr, 8, 4)
	if len(built) != 1 || built[0].lines == nil {
		t.Fatalf("first render: built %d colorizer(s), first got a request = %v", len(built), len(built) == 1 && built[0].lines != nil)
	}

	// Toggle off, then on, exactly as CtrlL does, without moving the view.
	// Neither call rebuilds the colorizer by itself — windowColorizer only
	// does that lazily, from the next renderText below — so builds is still
	// 1 here.
	vv.RefreshHighlighting()
	vv.RefreshHighlighting()

	// Same lines on screen as before: renderText must build a second
	// colorizer and hand it a request, not skip the request because the
	// lines hash the same as they did for the first colorizer.
	vv.renderText(scr, 8, 4)
	if len(built) != 2 {
		t.Fatalf("built %d colorizer(s) across both renders, want 2", len(built))
	}
	if built[1].lines == nil {
		t.Error("the new colorizer never got a Request after RefreshHighlighting, though the screen did not change")
	}
}
