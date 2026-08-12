package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtinput"
	"github.com/unxed/vtui"
)

func TestImageGalleryLayoutAndScrolling(t *testing.T) {
	g := &imageGallery{}
	g.layout(80, 23)
	if g.cols != 4 || g.rows != 2 {
		t.Fatalf("an 80x23 window holds a 4x2 grid of tiles, got %dx%d", g.cols, g.rows)
	}

	// Twenty pictures are five rows of four, of which two fit on screen.
	g.scrollTo(19, 20)
	if g.top != 3 {
		t.Errorf("the last row has to come into view, got top %d", g.top)
	}
	g.scrollTo(0, 20)
	if g.top != 0 {
		t.Errorf("the first row has to come into view, got top %d", g.top)
	}

	// A grid that is not even full has nothing to scroll.
	g.scrollTo(2, 3)
	if g.top != 0 {
		t.Errorf("a grid with three pictures must not scroll, got top %d", g.top)
	}

	g.move(-100, 20)
	if g.cursor != 0 {
		t.Errorf("the cursor stops at the first picture, got %d", g.cursor)
	}
	g.move(100, 20)
	if g.cursor != 19 {
		t.Errorf("the cursor stops at the last picture, got %d", g.cursor)
	}
}

func TestImageViewGallerySelectionReachesThePanel(t *testing.T) {
	withStubPipeline(t, 8, 8)

	iv := newTestImageView(t, 40, 20)
	iv.path = "b.png"
	iv.SetSiblings([]string{"a.png", "b.png", "c.png"}, 1)

	var reported []string
	iv.OnSelect = func(path string, on bool) {
		reported = append(reported, fmt.Sprintf("%s=%v", path, on))
	}

	press := func(vk uint16) {
		t.Helper()
		if !iv.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vk}) {
			t.Fatalf("key %d was not handled", vk)
		}
	}

	press(vtinput.VK_F12)
	if iv.gal == nil || iv.gal.cursor != 1 {
		t.Fatal("the grid opens on the picture that was on screen")
	}

	press(vtinput.VK_INSERT)
	if !iv.selected["b.png"] {
		t.Error("Ins did not pick the picture under the cursor")
	}
	if iv.gal.cursor != 2 {
		t.Errorf("Ins moves on, the cursor is at %d", iv.gal.cursor)
	}

	press(vtinput.VK_INSERT)
	press(vtinput.VK_DELETE)
	if iv.selected["c.png"] {
		t.Error("Del did not unpick the picture under the cursor")
	}

	want := []string{"b.png=true", "c.png=true", "c.png=false"}
	if strings.Join(reported, " ") != strings.Join(want, " ") {
		t.Errorf("the panel was told %v, expected %v", reported, want)
	}

	// Escape leaves the grid without leaving the viewer.
	press(vtinput.VK_ESCAPE)
	if iv.gal != nil {
		t.Error("Escape did not close the grid")
	}
	if iv.IsDone() {
		t.Error("Escape closed the whole viewer instead of the grid")
	}
}

func TestImageViewGalleryDrawsATileForEachPicture(t *testing.T) {
	withStubPipeline(t, 8, 8)

	scr := newImageTestScreen(t)
	iv := newTestImageView(t, 40, 20)
	iv.path = "a.png"
	iv.SetSiblings([]string{"a.png", "b.png", "c.png"}, 0)
	iv.ToggleGallery()

	// Two thumbnails have arrived; fetching the third is a background job
	// that has no business running on the drawing path, and this test is not
	// about it.
	iv.gal.thumbs["a.png"] = vtui.NewImageSurface(8, 8)
	iv.gal.thumbs["b.png"] = vtui.NewImageSurface(8, 8)
	iv.gal.asked["c.png"] = true

	scr.Graphics().BeginFrame()
	iv.Show(scr)
	scr.Graphics().EndFrame()

	if n := scr.Graphics().Len(); n != 2 {
		t.Errorf("two thumbnails are ready, %d were placed", n)
	}

	// Every tile is captioned, whether its thumbnail has arrived or not.
	row := ScreenRow(scr, imageTileRows, 0, 79)
	for _, name := range []string{"a.png", "b.png", "c.png"} {
		if !strings.Contains(row, name) {
			t.Errorf("the caption row is %q, without %s", row, name)
		}
	}
}

func TestImageViewGalleryEnterOpensTheCursor(t *testing.T) {
	withStubPipeline(t, 20, 10)

	iv := newTestImageView(t, 100, 100)
	iv.path = "a.png"
	iv.SetSiblings([]string{"a.png", "b.png"}, 0)
	if res := ImagePipe.LoadSync(nil, nil, "b.png"); res.Err != nil {
		t.Fatalf("b.png: %v", res.Err)
	}

	iv.ToggleGallery()
	iv.gal.move(1, 2)
	iv.ProcessKey(&vtinput.InputEvent{KeyDown: true, VirtualKeyCode: vtinput.VK_RETURN})

	if iv.gal != nil {
		t.Error("Enter has to leave the grid")
	}
	if iv.path != "b.png" || iv.index != 1 {
		t.Errorf("Enter opened %q at %d", iv.path, iv.index)
	}
}

func TestPanelSelectionByName(t *testing.T) {
	fp := &FileSystemPanel{
		entries: []*fileEntry{
			{VFSItem: vfs.VFSItem{Name: "..", IsDir: true}},
			{VFSItem: vfs.VFSItem{Name: "a.png"}},
		},
		selectedItems: map[string]bool{},
	}

	if !fp.SetSelectedByName("a.png", true) {
		t.Fatal("the panel does show that entry")
	}
	if !fp.IsNameSelected("a.png") {
		t.Error("the entry did not get picked")
	}
	if fp.SetSelectedByName("gone.png", true) {
		t.Error("a name the panel does not show cannot be picked")
	}
	fp.SetSelectedByName("a.png", false)
	if fp.IsNameSelected("a.png") {
		t.Error("the entry did not get unpicked")
	}
}
