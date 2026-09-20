package archive

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A program that holds "PK" headers as data of its own is not a self-extracting
// archive: with no end record at its tail the zip reader would search the
// whole file for entries, for minutes on a big executable (#1272).
func TestMaterializeSFXIgnoresAZipHeaderInAnExecutablePayload(t *testing.T) {
	stub := bytes.Repeat([]byte("machine code "), 200)
	payload := append([]byte("PK\x03\x04compressed data that is not a zip entry"), bytes.Repeat([]byte{0xA5}, 4096)...)
	path := filepath.Join(t.TempDir(), "installer.exe")
	if err := os.WriteFile(path, append(stub, payload...), 0o600); err != nil {
		t.Fatal(err)
	}

	embedded, backing, closer, err := materializeLocalSFX(path)
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		_ = closer.Close()
	}
	if embedded.format != "" || backing != path {
		t.Fatalf("a payload header was taken for an archive: %+v, backing %q", embedded, backing)
	}
}

func TestZipEndRecordAtTail(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	empty := append([]byte("PK\x05\x06"), make([]byte, 18)...) // an empty archive: the record alone
	withComment := append(append([]byte("PK\x05\x06"), make([]byte, 16)...), 3, 0, 'a', 'b', 'c')
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"end record only", empty, true},
		{"after a stub", append(bytes.Repeat([]byte{'x'}, 100000), empty...), true},
		{"with a comment", withComment, true},
		{"comment longer than the file", append(append([]byte("PK\x05\x06"), make([]byte, 16)...), 200, 0, 'a'), false},
		{"no record", []byte("PK\x03\x04 just a local header and more"), false},
		{"record too far from the end", append(append([]byte{}, empty...), bytes.Repeat([]byte{'x'}, 70000)...), false},
	}
	for _, tc := range cases {
		if got := zipEndRecordAtTail(write(tc.name, tc.data)); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
