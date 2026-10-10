// Command icons regenerates every platform icon from
// internal/gui/assets/icon/f4.svg and optional size-specific
// internal/gui/assets/icon/f4-N.svg overrides.
//
// Run it from anywhere in the repository with:
//
//	go generate
//
// or directly with:
//
//	go -C tools/icons run .
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const winresVersion = "v0.3.3"

var sizes = []int{16, 24, 28, 30, 32, 36, 42, 48, 56, 64, 128, 256, 512, 1024}

var windowsSizes = []int{16, 24, 28, 30, 32, 36, 42, 48, 56, 64, 128, 256}

// macOS app icons follow Apple's grid: on a 1024-pixel canvas the body is
// 824 pixels wide and centered, leaving 100 pixels of transparent margin on
// every side. Every system icon is drawn that way, so the Dock, Finder and
// Cmd-Tab size them alike. f4.svg fills its whole canvas, which suits Linux
// and Windows; put into the icns unchanged it showed up about a quarter
// larger than its neighbours wherever macOS used it as is (the icon stamped
// onto a bare binary), and macOS 26 shrank and cropped it into a squircle
// where it judged the bundle icon non-conforming.
const (
	macGridCanvas = 1024
	macGridBody   = 824
)

// icnsSizes are the canvas sizes the icns container carries.
var icnsSizes = []int{16, 32, 64, 128, 256, 512, 1024}

func main() {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	check(err)
	iconDir := filepath.Join(root, "internal", "gui", "assets", "icon")
	outDir := filepath.Join(root, "internal", "gui", "assets", "icon", "generated")
	check(os.MkdirAll(outDir, 0o755))

	pngs := make(map[int][]byte, len(sizes))
	for _, size := range sizes {
		source, err := sourceForSize(iconDir, size)
		check(err)
		data, err := renderPNG(source, size)
		check(err)
		pngs[size] = data
		check(writeFile(filepath.Join(outDir, fmt.Sprintf("f4-%d.png", size)), data))
	}

	macPNGs := make(map[int][]byte, len(icnsSizes))
	for _, size := range icnsSizes {
		body := macBodySize(size)
		source, err := macSourceForBody(iconDir, body)
		check(err)
		data, err := renderPaddedPNG(source, size, body)
		check(err)
		macPNGs[size] = data
	}

	check(writeFile(filepath.Join(outDir, "f4.ico"), makeICO(pngs)))
	xp, err := makeXPICO(pngs)
	check(err)
	check(writeFile(filepath.Join(outDir, "f4-xp.ico"), xp))
	check(writeFile(filepath.Join(outDir, "f4.icns"), makeICNS(macPNGs)))
	check(makeWindowsResources(root, filepath.Join(outDir, "f4.ico")))
	fmt.Println("generated platform icon resources")
}

func sourceForSize(iconDir string, size int) (string, error) {
	specific := filepath.Join(iconDir, fmt.Sprintf("f4-%d.svg", size))
	if _, err := os.Stat(specific); err == nil {
		return specific, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect size-specific SVG %q: %w", specific, err)
	}
	return filepath.Join(iconDir, "f4.svg"), nil
}

// macBodySize is the width of the icon body on a macOS canvas of the given
// size, scaled from Apple's 824-of-1024 grid. The margin it leaves is kept
// even on both sides so the body stays centered on whole pixels.
func macBodySize(canvas int) int {
	body := (canvas*macGridBody + macGridCanvas/2) / macGridCanvas
	if (canvas-body)%2 != 0 {
		body++
	}
	return body
}

// macSourceForBody picks the artwork for a macOS body of the given width.
// The size-specific SVGs are hinted for small pixel grids, and the small
// macOS bodies (14, 26, 52 pixels) fall between their sizes, so the nearest
// one within a few pixels is used; larger bodies take f4.svg.
func macSourceForBody(iconDir string, body int) (string, error) {
	best, bestDiff := "", 5
	for _, n := range []int{16, 24, 30, 32, 36, 42} {
		diff := n - body
		if diff < 0 {
			diff = -diff
		}
		if diff >= bestDiff {
			continue
		}
		specific := filepath.Join(iconDir, fmt.Sprintf("f4-%d.svg", n))
		if _, err := os.Stat(specific); err == nil {
			best, bestDiff = specific, diff
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect size-specific SVG %q: %w", specific, err)
		}
	}
	if best != "" {
		return best, nil
	}
	return filepath.Join(iconDir, "f4.svg"), nil
}

func renderPNG(source string, size int) ([]byte, error) {
	img, err := renderImage(source, size)
	if err != nil {
		return nil, err
	}
	return encodePNG(img)
}

// renderPaddedPNG draws the artwork body pixels wide in the center of a
// transparent canvas-sized image.
func renderPaddedPNG(source string, canvas, body int) ([]byte, error) {
	art, err := renderImage(source, body)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, canvas, canvas))
	offset := (canvas - body) / 2
	draw.Draw(img, art.Bounds().Add(image.Pt(offset, offset)), art, image.Point{}, draw.Src)
	return encodePNG(img)
}

func renderImage(source string, size int) (*image.RGBA, error) {
	f, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	icon, err := oksvg.ReadIconStream(f)
	if err != nil {
		return nil, fmt.Errorf("parse SVG: %w", err)
	}
	icon.SetTarget(0, 0, float64(size), float64(size))
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	dasher := rasterx.NewDasher(size, size, scanner)
	icon.Draw(dasher, 1)
	return img, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func makeICO(images map[int][]byte) []byte {
	// ICO supports PNG payloads. A zero width/height byte means 256 pixels.
	headerSize := 6 + 16*len(windowsSizes)
	var out bytes.Buffer
	writeLE(&out, uint16(0))
	writeLE(&out, uint16(1))
	writeLE(&out, uint16(len(windowsSizes)))
	offset := headerSize
	for _, size := range windowsSizes {
		dimension := byte(size)
		if size == 256 {
			dimension = 0
		}
		out.WriteByte(dimension)
		out.WriteByte(dimension)
		out.WriteByte(0)
		out.WriteByte(0)
		writeLE(&out, uint16(1))
		writeLE(&out, uint16(32))
		writeLE(&out, uint32(len(images[size])))
		writeLE(&out, uint32(offset))
		offset += len(images[size])
	}
	for _, size := range windowsSizes {
		out.Write(images[size])
	}
	return out.Bytes()
}

// xpSizes are the icon sizes Windows XP draws.
var xpSizes = []int{16, 24, 32, 48}

// makeXPICO is f4.ico for the legacy windows/386 build (f4#897, item 1):
// Windows XP cannot read PNG-compressed icon images, which arrived with
// Vista, and shows the default icon instead. Each image here is a 32-bit
// DIB with alpha, which XP reads, plus the 1-bit AND mask it still wants.
func makeXPICO(images map[int][]byte) ([]byte, error) {
	var bodies [][]byte
	for _, size := range xpSizes {
		img, err := png.Decode(bytes.NewReader(images[size]))
		if err != nil {
			return nil, fmt.Errorf("icon %d: %w", size, err)
		}
		bodies = append(bodies, iconDIB(img, size))
	}
	var out bytes.Buffer
	writeLE(&out, uint16(0))
	writeLE(&out, uint16(1))
	writeLE(&out, uint16(len(xpSizes)))
	offset := 6 + 16*len(xpSizes)
	for i, size := range xpSizes {
		out.WriteByte(byte(size))
		out.WriteByte(byte(size))
		out.WriteByte(0)
		out.WriteByte(0)
		writeLE(&out, uint16(1))
		writeLE(&out, uint16(32))
		writeLE(&out, uint32(len(bodies[i])))
		writeLE(&out, uint32(offset))
		offset += len(bodies[i])
	}
	for _, body := range bodies {
		out.Write(body)
	}
	return out.Bytes(), nil
}

// iconDIB is one icon image as a BITMAPINFOHEADER, the BGRA pixels bottom
// up, and the AND mask (a set bit is a transparent pixel), rows padded to
// four bytes.
func iconDIB(img image.Image, size int) []byte {
	maskStride := (size + 31) / 32 * 4
	var out bytes.Buffer
	writeLE(&out, uint32(40))
	writeLE(&out, int32(size))
	writeLE(&out, int32(2*size)) // the colour image and the mask
	writeLE(&out, uint16(1))
	writeLE(&out, uint16(32))
	writeLE(&out, uint32(0)) // BI_RGB
	writeLE(&out, uint32(size*size*4+maskStride*size))
	for i := 0; i < 4; i++ {
		writeLE(&out, uint32(0))
	}
	bounds := img.Bounds()
	mask := make([]byte, maskStride*size)
	for y := size - 1; y >= 0; y-- {
		row := size - 1 - y
		for x := 0; x < size; x++ {
			c := color.NRGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA)
			out.Write([]byte{c.B, c.G, c.R, c.A})
			if c.A == 0 {
				mask[row*maskStride+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	out.Write(mask)
	return out.Bytes()
}

func makeICNS(images map[int][]byte) []byte {
	// Modern macOS accepts PNG-compressed icon elements in an ICNS container.
	types := []struct {
		name string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"icp6", 64},
		{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	total := 8
	for _, entry := range types {
		total += 8 + len(images[entry.size])
	}
	var out bytes.Buffer
	out.WriteString("icns")
	writeBE(&out, uint32(total))
	for _, entry := range types {
		out.WriteString(entry.name)
		writeBE(&out, uint32(8+len(images[entry.size])))
		out.Write(images[entry.size])
	}
	return out.Bytes()
}

func makeWindowsResources(root, icon string) error {
	args := []string{
		"run", "github.com/tc-hib/go-winres@" + winresVersion,
		"simply",
		"--arch", "amd64,arm64",
		"--out", "rsrc",
		"--manifest", "gui",
		"--product-name", "f4",
		"--file-description", "f4 file manager",
		"--original-filename", "f4.exe",
		"--icon", icon,
	}
	cmd := exec.Command("go", args...)
	// rsrc_windows_*.syso must sit in the main package directory to be linked.
	cmd.Dir = filepath.Join(root, "cmd", "f4")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("generate Windows resources: %w", err)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

func writeLE(w io.Writer, value any) {
	check(binary.Write(w, binary.LittleEndian, value))
}

func writeBE(w io.Writer, value any) {
	check(binary.Write(w, binary.BigEndian, value))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "icon generator:", err)
		os.Exit(1)
	}
}
