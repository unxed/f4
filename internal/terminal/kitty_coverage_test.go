package terminal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

type kittyCoverageDisplay struct {
	puts     int
	deletes  int
	drops    []uint32
	putError string
	orphaned []uint32
}

func (d *kittyCoverageDisplay) Put(*kittyImage, kittyCommand) string {
	d.puts++
	return d.putError
}

func (d *kittyCoverageDisplay) Delete(kittyCommand) []uint32 {
	d.deletes++
	return append([]uint32(nil), d.orphaned...)
}

func (d *kittyCoverageDisplay) DropImage(id uint32) {
	d.drops = append(d.drops, id)
}

func TestKittyCoverageCommandAndPayloadEdges(t *testing.T) {
	cmd := parseKittyCommand("a=,malformed,f=bad,u=nope;AAAA")
	if got := cmd.Char('a', 't'); got != 't' {
		t.Fatalf("empty character value = %q, want default", got)
	}
	if got := cmd.Int('f', 32); got != 32 {
		t.Fatalf("invalid integer = %d, want default", got)
	}
	if got := cmd.Uint32('u', 9); got != 9 {
		t.Fatalf("invalid unsigned integer = %d, want default", got)
	}

	var transfer kittyTransfer
	if err := transfer.appendPayload("SGV", false); err != nil {
		t.Fatalf("first payload fragment: %v", err)
	}
	if transfer.tail != "SGV" || len(transfer.data) != 0 {
		t.Fatalf("fragment state = data %q tail %q", transfer.data, transfer.tail)
	}
	if err := transfer.appendPayload("sbG8=", true); err != nil {
		t.Fatalf("final payload fragment: %v", err)
	}
	if got := string(transfer.data); got != "Hello" {
		t.Fatalf("assembled payload = %q, want Hello", got)
	}
	if err := transfer.appendPayload("!", true); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
}

func TestKittyCoverageRejectsBadContinuationAndSurfaceData(t *testing.T) {
	var answers bytes.Buffer
	kg := NewKittyGraphics(func(data []byte) { answers.Write(data) })

	kg.Handle("a=t,i=1,f=32,s=1,v=1,m=1;AAAA")
	kg.Handle("m=0;!")
	if !strings.Contains(answers.String(), "i=1;EINVAL:the payload is not valid base64") {
		t.Fatalf("bad continuation answer = %q", answers.String())
	}

	kg.Handle("a=t,i=2,f=32,s=1,v=1,m=1;AAAA")
	kg.Handle("a=T;AAAA")
	if kg.xfer != nil {
		t.Fatal("a new action must abort the old transfer")
	}

	cases := []struct {
		name string
		cmd  string
		data []byte
		want string
	}{
		{"missing dimensions", "f=32,s=0,v=1", nil, "the image dimensions are missing"},
		{"too many pixels", "f=32,s=65536,v=1", nil, "the image is too large"},
		{"truncated pixels", "f=32,s=2,v=1", []byte{1, 2, 3}, "the pixel data is truncated"},
		{"bad encoded image", "f=100", []byte("not an image"), "the image could not be decoded"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, got := kittySurface(parseKittyCommand(test.cmd), test.data); got != "EINVAL:"+test.want {
				t.Fatalf("surface error = %q, want %q", got, "EINVAL:"+test.want)
			}
		})
	}
	if _, err := kittyInflate([]byte("not zlib")); err == nil {
		t.Fatal("invalid compressed data was accepted")
	}
}

func TestKittyCoverageFileSafetyAndRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pixels.bin")
	if err := os.WriteFile(path, []byte{1, 2, 3, 4}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := kittyReadFile(path, 'f', 1, 2); err != nil || !bytes.Equal(got, []byte{2, 3}) {
		t.Fatalf("offset and size read = %v, %v", got, err)
	}

	for _, bad := range []string{"", "/proc/self/status", "/sys/kernel", "/dev/null", filepath.Join(dir, "missing")} {
		if _, err := kittyReadFile(bad, 'f', 0, 0); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := kittyReadFile(dir, 'f', 0, 0); err == nil {
		t.Fatal("a directory was accepted as an image file")
	}

	tempDir, err := os.MkdirTemp("", "tty-graphics-protocol-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })
	tempPath := filepath.Join(tempDir, "payload")
	if err := os.WriteFile(tempPath, []byte{7, 8, 9}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := kittyReadFile(tempPath, 't', 0, 0); err != nil {
		t.Fatalf("temporary protocol file: %v", err)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatal("temporary protocol file was not removed")
	}
	if kittyIsTempPath(filepath.Join(dir, "ordinary-file")) {
		t.Fatal("ordinary temporary path was treated as a protocol file")
	}
}

func TestKittyCoverageDisplayStoreAndDelete(t *testing.T) {
	var answers bytes.Buffer
	display := &kittyCoverageDisplay{putError: "EIO:display failed", orphaned: []uint32{30}}
	kg := NewKittyGraphics(func(data []byte) { answers.Write(data) })
	kg.SetDisplay(display)
	surface := vtui.NewImageSurfaceFromPix(1, 1, 4, []byte{1, 2, 3, 4})

	kg.store(parseKittyCommand("i=7,I=4"), surface)
	kg.Handle("a=p,i=7,p=3")
	if display.puts != 1 || !strings.Contains(answers.String(), "EIO:display failed") {
		t.Fatalf("display error handling: puts=%d answer=%q", display.puts, answers.String())
	}
	kg.store(parseKittyCommand("i=7"), surface)
	if len(display.drops) == 0 || display.drops[0] != 7 {
		t.Fatalf("replacement did not drop old image: %#v", display.drops)
	}

	kg.store(parseKittyCommand("i=8,I=5"), surface)
	kg.Handle("a=p,I=5")
	if display.puts != 2 {
		t.Fatalf("number lookup did not call display: %d puts", display.puts)
	}
	kg.Handle("a=d,d=N,I=5")
	if kg.Image(8) != nil {
		t.Fatal("delete by image number did not free the image")
	}

	kg.store(parseKittyCommand("i=20"), surface)
	kg.Handle("a=d,d=R,x=20,y=20")
	if kg.Image(20) != nil {
		t.Fatal("range delete did not free the image")
	}

	kg.store(parseKittyCommand("i=30"), surface)
	kg.Handle("a=d,d=X")
	if kg.Image(30) != nil {
		t.Fatal("orphan cleanup did not free the image")
	}
	if display.deletes != 3 {
		t.Fatalf("display delete calls = %d, want 3", display.deletes)
	}
}
