//go:build !lite

package netfox

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/unxed/f4/vfs"
)

// fakeRenameClient is an SFTP v3 server: rename refuses an existing
// destination, and posix-rename exists only when posix is set.
type fakeRenameClient struct {
	files    map[string]string
	dirs     map[string]bool
	posix    bool
	failFrom string // a rename from this path fails (to test the rollback)
	calls    []string
}

func (f *fakeRenameClient) Rename(from, to string) error {
	f.calls = append(f.calls, "rename "+from+" "+to)
	if from == f.failFrom {
		return errors.New("sftp: \"Failure\" (SSH_FX_FAILURE)")
	}
	if _, ok := f.files[to]; ok || f.dirs[to] {
		return errors.New("sftp: \"Failure\" (SSH_FX_FAILURE)")
	}
	f.files[to] = f.files[from]
	delete(f.files, from)
	return nil
}

func (f *fakeRenameClient) PosixRename(from, to string) error {
	f.calls = append(f.calls, "posix "+from+" "+to)
	f.files[to] = f.files[from]
	delete(f.files, from)
	return nil
}

func (f *fakeRenameClient) HasExtension(name string) (string, bool) {
	return "1", f.posix && name == sftpPosixRenameExtension
}

func (f *fakeRenameClient) Lstat(p string) (os.FileInfo, error) {
	if f.dirs[p] {
		return fakeRenameInfo{dir: true}, nil
	}
	if _, ok := f.files[p]; ok {
		return fakeRenameInfo{}, nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeRenameClient) Remove(p string) error {
	delete(f.files, p)
	return nil
}

type fakeRenameInfo struct {
	os.FileInfo
	dir bool
}

func (i fakeRenameInfo) IsDir() bool { return i.dir }

func newFakeRenameClient(posix bool) *fakeRenameClient {
	return &fakeRenameClient{
		files: map[string]string{"/d/f": "old", "/d/.f.tmp": "new"},
		dirs:  map[string]bool{"/d/dir": true},
		posix: posix,
	}
}

func TestSFTPRenameOverExistingFile(t *testing.T) {
	// Without permission to overwrite the plain rename is used and refuses.
	c := newFakeRenameClient(true)
	if err := sftpRename(c, "/d/.f.tmp", "/d/f", false); err == nil {
		t.Fatal("rename over an existing file succeeded without overwrite permission")
	}

	// With the extension the file is replaced in one step.
	c = newFakeRenameClient(true)
	if err := sftpRename(c, "/d/.f.tmp", "/d/f", true); err != nil {
		t.Fatalf("posix-rename: %v", err)
	}
	if c.files["/d/f"] != "new" || len(c.files) != 1 {
		t.Fatalf("files after posix-rename = %v", c.files)
	}

	// Without the extension the original is moved aside, then dropped.
	c = newFakeRenameClient(false)
	if err := sftpRename(c, "/d/.f.tmp", "/d/f", true); err != nil {
		t.Fatalf("fallback rename: %v", err)
	}
	if c.files["/d/f"] != "new" || len(c.files) != 1 {
		t.Fatalf("files after fallback = %v (calls %v)", c.files, c.calls)
	}
}

func TestSFTPRenameFallbackRestoresTheOriginal(t *testing.T) {
	c := newFakeRenameClient(false)
	c.failFrom = "/d/.f.tmp"
	if err := sftpRename(c, "/d/.f.tmp", "/d/f", true); err == nil {
		t.Fatal("rename succeeded although the staged file could not be moved")
	}
	if c.files["/d/f"] != "old" || c.files["/d/.f.tmp"] != "new" || len(c.files) != 2 {
		t.Fatalf("original not restored: %v (calls %v)", c.files, c.calls)
	}
}

func TestSFTPRenameDoesNotReplaceADirectory(t *testing.T) {
	c := newFakeRenameClient(false)
	if err := sftpRename(c, "/d/.f.tmp", "/d/dir", true); err == nil {
		t.Fatal("a directory was replaced by a file")
	}
	if len(c.files) != 2 {
		t.Fatalf("files = %v", c.files)
	}
}

func TestSFTPVFSRenameOverwritesWhenAllowed(t *testing.T) {
	client, _ := startTestSFTPServer(t)
	v := &SFTPVFS{client: client}
	for name, body := range map[string]string{"/a.txt": "old", "/b.tmp": "new"} {
		wf, err := client.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wf.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := wf.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Rename(vfs.WithDestinationOverwrite(context.Background(), true), "/b.tmp", "/a.txt"); err != nil {
		t.Fatalf("Rename over an existing file = %v", err)
	}
	if _, err := client.Lstat("/b.tmp"); err == nil {
		t.Fatal("staged file still exists")
	}
	st, err := client.Lstat("/a.txt")
	if err != nil || st.Size() != 3 {
		t.Fatalf("a.txt = %v, %v", st, err)
	}
}
