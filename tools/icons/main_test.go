package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/srwiley/oksvg"
)

func TestICOContainer(t *testing.T) {
	images := fakeImages()
	data := makeICO(images)
	if got := binary.LittleEndian.Uint16(data[2:4]); got != 1 {
		t.Fatalf("ICO type = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(data[4:6]); got != uint16(len(windowsSizes)) {
		t.Fatalf("ICO image count = %d, want %d", got, len(windowsSizes))
	}
	wantWidths := []byte{16, 24, 28, 30, 32, 36, 42, 48, 56, 64, 128, 0}
	for index := range windowsSizes {
		entry := data[6+index*16:]
		if entry[0] != wantWidths[index] {
			t.Fatalf("ICO entry %d width = %d, want %d", index, entry[0], wantWidths[index])
		}
		length := binary.LittleEndian.Uint32(entry[8:12])
		offset := binary.LittleEndian.Uint32(entry[12:16])
		if int(offset+length) > len(data) {
			t.Fatalf("ICO entry %d points outside the file", index)
		}
	}
}

func TestSourceForSize(t *testing.T) {
	iconDir := t.TempDir()
	common := filepath.Join(iconDir, "f4.svg")
	specific := filepath.Join(iconDir, "f4-30.svg")
	if err := os.WriteFile(common, []byte("common"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(specific, []byte("specific"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := sourceForSize(iconDir, 30)
	if err != nil {
		t.Fatal(err)
	}
	if got != specific {
		t.Fatalf("sourceForSize(30) = %q, want %q", got, specific)
	}

	got, err = sourceForSize(iconDir, 48)
	if err != nil {
		t.Fatal(err)
	}
	if got != common {
		t.Fatalf("sourceForSize(48) = %q, want %q", got, common)
	}
}

func TestRenderScalesStrokesAndGradients(t *testing.T) {
	if oksvg.DefaultStyle.LineWidth != 1 {
		t.Fatalf("SVG default stroke width = %v, want 1", oksvg.DefaultStyle.LineWidth)
	}
	source := filepath.Join("..", "..", "internal", "gui", "assets", "icon", "f4.svg")
	data24, err := renderPNG(source, 24)
	if err != nil {
		t.Fatal(err)
	}
	data48, err := renderPNG(source, 48)
	if err != nil {
		t.Fatal(err)
	}
	img24, err := png.Decode(bytes.NewReader(data24))
	if err != nil {
		t.Fatal(err)
	}
	img48, err := png.Decode(bytes.NewReader(data48))
	if err != nil {
		t.Fatal(err)
	}

	// The yellow line centered at (5, 4.5) in SVG coordinates must double
	// its raster thickness when output dimensions double.
	stroke24 := coloredPixelsInColumn(img24, 5, 2, 7)
	stroke48 := coloredPixelsInColumn(img48, 10, 5, 14)
	if stroke24 != 1 || stroke48 != 2 {
		t.Fatalf("stroke thickness differs from SVG/ImageMagick: 24px=%d (want 1), 48px=%d (want 2)", stroke24, stroke48)
	}

	// Empty background points near the top and bottom must retain the radial
	// gradient instead of collapsing to a single stop color.
	top := img48.At(20, 4)
	bottom := img48.At(20, 32)
	tr, tg, tb, _ := top.RGBA()
	br, bg, bb, _ := bottom.RGBA()
	if tr == br && tg == bg && tb == bb {
		t.Fatal("background gradient collapsed to a flat color")
	}
}

func coloredPixelsInColumn(img interface {
	Bounds() image.Rectangle
	At(x, y int) color.Color
}, x, minY, maxY int) int {
	count := 0
	for y := minY; y < maxY; y++ {
		r, g, b, _ := img.At(x, y).RGBA()
		if r > 0x6000 && g > 0x6000 && b < 0x6000 {
			count++
		}
	}
	return count
}

func TestICNSContainer(t *testing.T) {
	data := makeICNS(fakeImages())
	if string(data[:4]) != "icns" {
		t.Fatalf("ICNS magic = %q", data[:4])
	}
	if got := binary.BigEndian.Uint32(data[4:8]); got != uint32(len(data)) {
		t.Fatalf("ICNS declared length = %d, actual = %d", got, len(data))
	}
	for offset := 8; offset < len(data); {
		if offset+8 > len(data) {
			t.Fatal("truncated ICNS element header")
		}
		length := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		if length < 8 || offset+length > len(data) {
			t.Fatalf("invalid ICNS element length %d at offset %d", length, offset)
		}
		if !bytes.HasPrefix(data[offset+8:offset+length], []byte("png")) {
			t.Fatalf("ICNS element at offset %d does not contain test payload", offset)
		}
		offset += length
	}
}

func fakeImages() map[int][]byte {
	images := make(map[int][]byte, len(sizes))
	for _, size := range sizes {
		images[size] = []byte("png payload")
	}
	return images
}

func TestMacBodySizeFollowsAppleGrid(t *testing.T) {
	want := map[int]int{1024: 824, 512: 412, 256: 206, 128: 104, 64: 52, 32: 26, 16: 14}
	for canvas, body := range want {
		if got := macBodySize(canvas); got != body {
			t.Errorf("macBodySize(%d) = %d, want %d", canvas, got, body)
		}
		if (canvas-macBodySize(canvas))%2 != 0 {
			t.Errorf("macBodySize(%d) leaves an uneven margin", canvas)
		}
	}
}

func TestRenderPaddedPNGLeavesTransparentMargin(t *testing.T) {
	source := filepath.Join("..", "..", "internal", "gui", "assets", "icon", "f4.svg")
	data, err := renderPaddedPNG(source, 128, macBodySize(128))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 128 || b.Dy() != 128 {
		t.Fatalf("canvas = %v, want 128x128", b)
	}
	margin := (128 - macBodySize(128)) / 2
	for _, p := range []image.Point{{0, 0}, {margin - 1, 64}, {64, margin - 1}, {127 - margin + 1, 64}, {64, 127}} {
		if _, _, _, a := img.At(p.X, p.Y).RGBA(); a != 0 {
			t.Errorf("pixel %v in the margin has alpha %d, want 0", p, a)
		}
	}
	if _, _, _, a := img.At(64, 64).RGBA(); a == 0 {
		t.Error("center of the body is transparent")
	}
}

// f4#897, item 1: Windows XP reads no PNG-compressed icon image, so the
// legacy build carries f4-xp.ico, made of 32-bit DIBs.
func TestXPICOHasNoPNGImages(t *testing.T) {
	images := map[int][]byte{}
	for _, size := range xpSizes {
		img := image.NewNRGBA(image.Rect(0, 0, size, size))
		img.Set(1, 1, color.NRGBA{R: 255, A: 255}) // the rest stays transparent
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		images[size] = buf.Bytes()
	}
	data, err := makeXPICO(images)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(data[4:6]); got != uint16(len(xpSizes)) {
		t.Fatalf("XP ICO image count = %d, want %d", got, len(xpSizes))
	}
	for index, size := range xpSizes {
		entry := data[6+index*16:]
		offset := binary.LittleEndian.Uint32(entry[12:16])
		body := data[offset:]
		if bytes.HasPrefix(body, []byte("\x89PNG")) {
			t.Fatalf("XP ICO entry %d is PNG", index)
		}
		if binary.LittleEndian.Uint32(body[0:4]) != 40 || int32(binary.LittleEndian.Uint32(body[4:8])) != int32(size) ||
			int32(binary.LittleEndian.Uint32(body[8:12])) != int32(2*size) || binary.LittleEndian.Uint16(body[14:16]) != 32 {
			t.Fatalf("XP ICO entry %d header is not a 32-bit DIB of %d pixels", index, size)
		}
		maskStride := (size + 31) / 32 * 4
		if got, want := binary.LittleEndian.Uint32(entry[8:12]), uint32(40+size*size*4+maskStride*size); got != want {
			t.Fatalf("XP ICO entry %d length = %d, want %d", index, got, want)
		}
	}
}

// The checked-in f4-xp.ico is the one the checked-in PNGs make; run with
// F4_UPDATE_XP_ICO=1 to write it again after the PNGs change.
func TestXPICOIsCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "gui", "assets", "icon", "generated")
	images := map[int][]byte{}
	for _, size := range xpSizes {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("f4-%d.png", size))) // #nosec G304 -- the repository's own generated icons
		if err != nil {
			t.Fatal(err)
		}
		images[size] = data
	}
	want, err := makeXPICO(images)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "f4-xp.ico")
	if os.Getenv("F4_UPDATE_XP_ICO") == "1" {
		if err := os.WriteFile(path, want, 0o644); err != nil { // #nosec G306 -- a checked-in asset
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path) // #nosec G304 -- the repository's own generated icon
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("f4-xp.ico is out of date; run this test with F4_UPDATE_XP_ICO=1")
	}
}
