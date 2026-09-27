package multiarc

import (
	"context"
	"reflect"
	"testing"
	"time"
)

const sampleSevenZipListing = `7-Zip 23.01

Scanning the drive for archives:
1 file, 1234 bytes

Listing archive: a.7z

--
Path = a.7z
Type = 7z
Physical Size = 1234

----------
Path = readme.txt
Folder = -
Size = 42
Packed Size = 40
Modified = 2024-01-15 10:30:00
Attributes = A
CRC = DEADBEEF
Encrypted = -
Method = LZMA2:19
Block = 0

Path = sub
Folder = +
Size = 0
Packed Size = 0
Modified = 2024-01-15 10:31:00
Attributes = D
CRC =
Encrypted = -
Method =
Block =

Path = sub/data.bin
Folder = -
Size = 100
Packed Size = 90
Modified = 2024-01-15 10:32:00
Attributes = A
CRC = 12345678
Encrypted = -
Method = LZMA2:19
Block = 0
`

func TestParseSevenZipListing(t *testing.T) {
	entries := parseSevenZipListing([]byte(sampleSevenZipListing))
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %#v", len(entries), entries)
	}
	byPath := map[string]entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	readme, ok := byPath["readme.txt"]
	if !ok {
		t.Fatal("missing readme.txt")
	}
	if readme.IsDir {
		t.Error("readme.txt should not be a directory")
	}
	if !readme.SizeKnown || readme.Size != 42 {
		t.Errorf("readme.txt size = %d (known=%v), want 42", readme.Size, readme.SizeKnown)
	}
	wantMTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	if !readme.MTime.Equal(wantMTime) {
		t.Errorf("readme.txt mtime = %v, want %v", readme.MTime, wantMTime)
	}

	sub, ok := byPath["sub"]
	if !ok || !sub.IsDir {
		t.Fatalf("sub should be a directory: %#v ok=%v", sub, ok)
	}

	data, ok := byPath["sub/data.bin"]
	if !ok || data.IsDir || data.Size != 100 {
		t.Fatalf("sub/data.bin: %#v ok=%v", data, ok)
	}

	// The archive-level header block (it carries "Type") must not surface
	// as a member named "a.7z".
	if _, ok := byPath["a.7z"]; ok {
		t.Error("the archive's own header block should not become an entry")
	}
}

func TestSevenZipBackendExtractOne(t *testing.T) {
	var gotBin string
	var gotArgs []string
	withFakeTools(t, func(name string) (string, error) {
		if name == "7z" {
			return "/usr/bin/7z", nil
		}
		return "", errNotFoundStub
	}, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotBin, gotArgs = name, args
		return nil, nil, nil
	})

	if err := (sevenZipBackend{}).extractOne(context.Background(), "/a.7z", "/dest", "sub/data.bin"); err != nil {
		t.Fatalf("extractOne: %v", err)
	}
	if gotBin != "7z" {
		t.Fatalf("bin = %q, want 7z", gotBin)
	}
	want := []string{"x", "-y", "-o/dest", "/a.7z", "--", "sub/data.bin"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
}

// A member named "-x" is a switch to 7-Zip unless "--" comes first, and one
// named "@list" a list file even after "--" in some builds, so it goes in a
// list file of its own; a "*" or "?" in a name needs -spd to stay literal.
func TestSevenZipBackendExtractOneOddNames(t *testing.T) {
	f := &fakeArchiver{tools: map[string]bool{"7z": true}}
	f.install(t)
	for _, member := range []string{"@list.txt", "-x", "w*ld"} {
		if err := (sevenZipBackend{bin: "7z"}).extractOne(context.Background(), "/a.7z", "/dest", member); err != nil {
			t.Fatalf("extractOne %s: %v", member, err)
		}
	}
	want := []string{
		"7z x -y -o/dest -scsUTF-8 /a.7z @[@list.txt]",
		"7z x -y -o/dest /a.7z -- -x",
		"7z x -y -o/dest -spd /a.7z -- w*ld",
	}
	if got := f.commands(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}

// 7-Zip on Windows prints member paths with backslashes. Path is normalized
// for the tree, but Raw keeps the name as printed: that is the spelling a
// later 7z command is handed back.
func TestParseSevenZipListingKeepsRawName(t *testing.T) {
	listing := "Path = a.7z\nType = 7z\n\nPath = sub\\data.bin\nFolder = -\nSize = 1\n"
	entries := parseSevenZipListing([]byte(listing))
	if len(entries) != 1 {
		t.Fatalf("entries = %#v, want one", entries)
	}
	if entries[0].Path != "sub/data.bin" || entries[0].Raw != "sub\\data.bin" {
		t.Fatalf("entry = %#v, want Path sub/data.bin and Raw sub\\data.bin", entries[0])
	}
}

func TestSevenZipBackendUnavailable(t *testing.T) {
	withFakeTools(t, func(string) (string, error) { return "", errNotFoundStub }, nil)
	if _, err := (sevenZipBackend{}).list(context.Background(), "/a.7z"); err == nil {
		t.Fatal("expected an error when no 7z binary is on PATH")
	}
}
