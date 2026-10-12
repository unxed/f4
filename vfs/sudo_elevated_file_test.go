package vfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// connectTestElevated starts a dispatcher in this process and returns a
// client connected to it, the way UAC would leave things on Windows.
func connectTestElevated(t *testing.T) *SudoClient {
	t.Helper()
	oldLaunch := launchElevated
	t.Cleanup(func() { launchElevated = oldLaunch })
	t.Setenv("TMPDIR", "/tmp")
	launchElevated = func(_, sock, token string) error {
		go func() { _ = RunElevatedDispatcher(sock, token, ElevatedFileOps) }()
		return nil
	}
	c := &SudoClient{appPath: "f4.exe", consented: true}
	c.mu.Lock()
	err := c.connectElevatedLocked()
	c.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.conn.Close() })
	return c
}

// f4#1768: on Windows OSVFS reads and writes a protected file through the
// elevated dispatcher by requests.
func TestElevatedFileReadsAndWritesThroughTheDispatcher(t *testing.T) {
	c := connectTestElevated(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := openElevatedFile(c, path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Size() != 10 {
		t.Fatalf("size %d", f.Size())
	}
	p := make([]byte, 4)
	if n, err := f.ReadAt(context.Background(), p, 3); n != 4 || err != nil || string(p) != "3456" {
		t.Fatalf("ReadAt = %d %q %v", n, p[:n], err)
	}
	p = make([]byte, 8)
	if n, err := f.ReadAt(context.Background(), p, 6); n != 4 || err != io.EOF || string(p[:n]) != "6789" {
		t.Fatalf("ReadAt at the end = %d %q %v", n, p[:n], err)
	}
	var got bytes.Buffer
	buf := make([]byte, 3)
	for {
		n, err := f.Read(context.Background(), buf)
		got.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != "0123456789" {
		t.Fatalf("Read gave %q", got.String())
	}

	w := newElevatedWriter(c, path, 0o600)
	for _, part := range []string{"new ", "text", "!"} {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new text!" {
		t.Fatalf("after the writer: %q", data)
	}

	// A file nothing is written to is still created, empty.
	empty := filepath.Join(dir, "empty.txt")
	if err := newElevatedWriter(c, empty, 0o600).Close(); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(empty); err != nil || st.Size() != 0 {
		t.Fatalf("empty file: %v %v", st, err)
	}

	// More than one chunk goes in several requests.
	big := bytes.Repeat([]byte("x"), SudoChunkLimit+10)
	w = newElevatedWriter(c, path, 0o600)
	if _, err := w.Write(big); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Size() != int64(len(big)) {
		t.Fatalf("big file size %d, want %d", st.Size(), len(big))
	}
}

// The editor stages its save in a fresh sibling created with O_EXCL; through
// the dispatcher that create must fail for a taken name and leave it alone.
func TestElevatedExclusiveCreateRefusesATakenName(t *testing.T) {
	c := connectTestElevated(t)
	stage := filepath.Join(t.TempDir(), ".f4tmp-stage")
	if err := c.CreateExclusive(stage, 0o600); err != nil {
		t.Fatal(err)
	}
	w := newElevatedWriter(c, stage, 0o600)
	w.sent = true // as OSVFS.Create leaves it after CreateExclusive
	if _, err := w.Write([]byte("staged")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateExclusive(stage, 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second exclusive create: %v, want ErrExist", err)
	}
	// #nosec G304 -- the path is inside the private test temp directory.
	if got, err := os.ReadFile(stage); err != nil || string(got) != "staged" {
		t.Fatalf("the taken file changed: %q %v", got, err)
	}
}

// Unpacking an update or a plugin into a protected folder writes through
// SudoClient.CreateFile; on Windows that goes by requests, and the contents
// are there once Close returns.
func TestElevatedCreateFileWritesByRequestsOnWindowsPath(t *testing.T) {
	c := connectTestElevated(t)
	oldUAC := connectsByUAC
	t.Cleanup(func() { connectsByUAC = oldUAC })
	connectsByUAC = func() bool { return true }

	target := filepath.Join(t.TempDir(), "plugin.bin")
	if err := os.WriteFile(target, []byte("old contents, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := c.CreateFile(target, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, isFile := w.(*os.File); isFile {
		t.Fatal("got a descriptor on the Windows path")
	}
	if _, err := io.Copy(w, bytes.NewReader([]byte("new"))); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- the path is inside the private test temp directory.
	if got, err := os.ReadFile(target); err != nil || string(got) != "new" {
		t.Fatalf("after CreateFile: %q %v", got, err)
	}
}
