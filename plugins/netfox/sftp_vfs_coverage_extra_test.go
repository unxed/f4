package netfox

import (
	"bytes"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestSFTPVFSCodepageAndCommandArgumentContracts(t *testing.T) {
	v := &SFTPVFS{codepage: "1251", path: "/home/user"}
	encoded, err := vfs.EncodeBytes([]byte("Привет"), 1251)
	if err != nil {
		t.Fatal(err)
	}
	if got := []byte(v.encodePath("Привет")); !bytes.Equal(got, encoded) {
		t.Fatalf("encoded SFTP path = %x, want %x", got, encoded)
	}
	if got, err := v.EncodeCommandListANSI([]byte("имя.txt")); err != nil || !bytes.Equal(got, mustSFTPEncode(t, "имя.txt", 1251)) {
		t.Fatalf("encoded command list = %x, err=%v", got, err)
	}
	plain := &SFTPVFS{}
	input := []byte("plain")
	copyOfInput, err := plain.EncodeCommandListANSI(input)
	if err != nil || !bytes.Equal(copyOfInput, input) {
		t.Fatalf("plain command list = %q, err=%v", copyOfInput, err)
	}
	copyOfInput[0] = 'P'
	if input[0] != 'p' {
		t.Fatal("EncodeCommandListANSI returned the caller's buffer")
	}

	if got := quoteSFTPCommandArgument("a'b"); got != `'a'"'"'b'` {
		t.Fatalf("quoted SFTP argument = %q", got)
	}
	if v.GetPath() != "/home/user" || v.IsAtRoot() || !v.SupportsConcurrentCalls() {
		t.Fatal("SFTP path/concurrency contract is incorrect")
	}
	if caps := v.GetCapabilities(); !caps.HasRandomAccess || !caps.HasUnixPermissions || !caps.HasWrite {
		t.Fatalf("SFTP capabilities = %#v", caps)
	}
}

func mustSFTPEncode(t *testing.T, text string, cp int) []byte {
	t.Helper()
	encoded, err := vfs.EncodeBytes([]byte(text), cp)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSFTPCommandOutputChunkEndPreservesRuneBoundaries(t *testing.T) {
	data := append(bytes.Repeat([]byte{'x'}, 3), []byte("яz")...)
	if got := sftpCommandOutputChunkEnd(data, 4); got != 3 {
		t.Fatalf("chunk end = %d, want 3 before the UTF-8 rune", got)
	}
	if got := sftpCommandOutputChunkEnd([]byte("short"), 10); got != 5 {
		t.Fatalf("short chunk end = %d, want 5", got)
	}
}
