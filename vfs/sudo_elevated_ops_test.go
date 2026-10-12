package vfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// f4#1768 step 3: the elevated dispatcher answers the path operations itself.
func TestElevatedFileOpsPathOperations(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdMkDir, Path: sub, Mode: 0o755}); r.Error != "" {
		t.Fatalf("MkDir: %s", r.Error)
	}
	file := filepath.Join(sub, "a.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := ElevatedFileOps(SudoRequest{Cmd: CmdStat, Path: file})
	if r.Error != "" || r.Item.Name != "a.txt" || r.Item.Size != 5 || r.Item.IsDir {
		t.Fatalf("Stat = %+v", r)
	}
	r = ElevatedFileOps(SudoRequest{Cmd: CmdReadDir, Path: sub})
	if r.Error != "" || len(r.Items) != 1 || r.Items[0].Name != "a.txt" {
		t.Fatalf("ReadDir = %+v", r)
	}

	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	item := r.Items[0]
	item.MTime, item.ATime = when, when
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdSetAttributes, Path: file, Item: item}); r.Error != "" {
		t.Fatalf("SetAttributes: %s", r.Error)
	}
	if st, err := os.Stat(file); err != nil || !st.ModTime().Equal(when) {
		t.Errorf("modification time after SetAttributes = %v, %v", st.ModTime(), err)
	}

	other := filepath.Join(sub, "b.txt")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdRename, Path: file, Path2: other, Flags: SudoRenameNoReplace}); r.Error == "" {
		t.Error("a no-replace rename over an existing file succeeded")
	}
	moved := filepath.Join(sub, "c.txt")
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdRename, Path: file, Path2: moved}); r.Error != "" {
		t.Fatalf("Rename: %s", r.Error)
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdRemove, Path: sub}); r.Error != "" {
		t.Fatalf("Remove: %s", r.Error)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("the folder is still there after Remove: %v", err)
	}

	if r := ElevatedFileOps(SudoRequest{Cmd: CmdOpen, Path: moved}); r.Error == "" {
		t.Error("Open was accepted although contents cannot travel by descriptor yet")
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdStat, Path: filepath.Join(dir, "missing")}); r.Error == "" {
		t.Error("Stat of a missing file reported no error")
	}
}

// f4#1768 step 3b: a file's contents go by requests -- written in chunks,
// read back in chunks, the end of the file said with IsEOF.
func TestElevatedFileOpsReadWriteByRequests(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(file, []byte("old contents that must go"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdWriteAt, Path: file, Data: []byte("hello "), Flags: SudoWriteTruncate, Mode: 0o600}); r.Error != "" {
		t.Fatalf("first chunk: %s", r.Error)
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdWriteAt, Path: file, Offset: 6, Data: []byte("world")}); r.Error != "" {
		t.Fatalf("second chunk: %s", r.Error)
	}
	if got, _ := os.ReadFile(file); string(got) != "hello world" {
		t.Fatalf("file holds %q", got)
	}

	// The request travels as a JSON frame: the bytes must survive it.
	var buf bytes.Buffer
	if err := writeSudoFrame(&buf, SudoRequest{Cmd: CmdReadAt, Path: file, Offset: 0, Size: 6}); err != nil {
		t.Fatal(err)
	}
	var req SudoRequest
	if err := readSudoFrame(&buf, &req); err != nil {
		t.Fatal(err)
	}
	r := ElevatedFileOps(req)
	if r.Error != "" || string(r.Data) != "hello " || r.IsEOF {
		t.Fatalf("first read = %q eof %v err %q", r.Data, r.IsEOF, r.Error)
	}
	if err := writeSudoFrame(&buf, r); err != nil {
		t.Fatal(err)
	}
	var back SudoResponse
	if err := readSudoFrame(&buf, &back); err != nil || string(back.Data) != "hello " {
		t.Fatalf("answer after the frame = %q, %v", back.Data, err)
	}
	r = ElevatedFileOps(SudoRequest{Cmd: CmdReadAt, Path: file, Offset: 6, Size: 100})
	if r.Error != "" || string(r.Data) != "world" || !r.IsEOF {
		t.Fatalf("last read = %q eof %v err %q", r.Data, r.IsEOF, r.Error)
	}

	if r := ElevatedFileOps(SudoRequest{Cmd: CmdReadAt, Path: file, Size: SudoChunkLimit + 1}); r.Error == "" {
		t.Error("a read over the chunk limit was accepted")
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdReadAt, Path: file + ".missing", Size: 10}); r.Error == "" {
		t.Error("reading a missing file reported no error")
	}
}

// f4#1768: the client decides by the kind of an error the dispatcher names,
// not by its text, which on Windows is not the Unix one.
func TestSudoErrorKindTravelsWithTheAnswer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdStat, Path: missing}); r.Errno != "ENOENT" {
		t.Errorf("Stat of a missing file: Errno %q", r.Errno)
	}
	if r := ElevatedFileOps(SudoRequest{Cmd: CmdReadAt, Path: missing, Size: 1}); r.Errno != "ENOENT" {
		t.Errorf("ReadAt of a missing file: Errno %q", r.Errno)
	}
	windowsText := "CreateFile " + missing + ": The system cannot find the file specified."
	if err := newSudoRemoteErrorCode(windowsText, "ENOENT"); !errors.Is(err, os.ErrNotExist) || err.Error() != windowsText {
		t.Errorf("ENOENT with a Windows text = %v", err)
	}
	if err := newSudoRemoteErrorCode("Access is denied.", "EACCES"); !errors.Is(err, os.ErrPermission) {
		t.Errorf("EACCES = %v", err)
	}
	if err := newSudoRemoteErrorCode("whatever", "EEXIST_DEST"); !errors.Is(err, ErrDestinationExists) {
		t.Errorf("EEXIST_DEST = %v", err)
	}
	if err := newSudoRemoteErrorCode("plain failure", ""); errors.Is(err, os.ErrNotExist) || err.Error() != "plain failure" {
		t.Errorf("no code = %v", err)
	}
}
