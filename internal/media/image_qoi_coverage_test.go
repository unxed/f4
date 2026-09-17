package media

import (
	"encoding/binary"
	"strings"
	"testing"
)

func qoiHeader(width, height uint32, channels byte) []byte {
	out := []byte("qoif")
	out = binary.BigEndian.AppendUint32(out, width)
	out = binary.BigEndian.AppendUint32(out, height)
	return append(out, channels, 0)
}

func TestDecodeQOI_RGBAndLuma(t *testing.T) {
	rgb := qoiHeader(1, 1, 3)
	rgb = append(rgb, qoiOpRGB, 7, 8, 9)
	surf, err := decodeQOI(rgb)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, a := surf.PixelAt(0, 0); r != 7 || g != 8 || b != 9 || a != 255 {
		t.Fatalf("RGB pixel = %d %d %d %d", r, g, b, a)
	}

	luma := qoiHeader(2, 1, 4)
	luma = append(luma, qoiOpRGBA, 100, 100, 100, 17)
	// dg=0, dr=0, db=0: the second pixel exercises the two-byte luma opcode.
	luma = append(luma, qoiOpLuma|32, 0x88)
	surf, err = decodeQOI(luma)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, a := surf.PixelAt(1, 0); r != 100 || g != 100 || b != 100 || a != 17 {
		t.Fatalf("luma pixel = %d %d %d %d", r, g, b, a)
	}
}

func TestDecodeQOIRejectsInvalidHeaderGeometryAndOpcodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"channels", qoiHeader(1, 1, 2), "channel count"},
		{"width", qoiHeader(0, 1, 4), "no size"},
		{"height", qoiHeader(1, 0, 4), "no size"},
		{"too large", qoiHeader(imageMaxPixels+1, 1, 4), "too large"},
		{"no pixels", qoiHeader(1, 1, 4), "ends too early"},
		{"short RGB", append(qoiHeader(1, 1, 4), qoiOpRGB, 1, 2), "ends too early"},
		{"short RGBA", append(qoiHeader(1, 1, 4), qoiOpRGBA, 1, 2, 3), "ends too early"},
		{"short luma", append(qoiHeader(1, 1, 4), qoiOpLuma, 1), "ends too early"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeQOI(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeQOI error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
