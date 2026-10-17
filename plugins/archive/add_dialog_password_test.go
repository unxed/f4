package archive

import (
	stdzip "archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/sevenzip"
	"github.com/unxed/vtui"
)

// The password reaches the archiver for the formats that can be encrypted
// and only for them (f4#2002, part 2).
func TestAddArchivePasswordOnlyForEncryptableFormats(t *testing.T) {
	for _, tc := range []struct {
		ext  string
		want string
	}{{".zip", "secret"}, {".7z", "secret"}, {".tar.gz", ""}} {
		opts := addArchiveSettings{Format: addArchiveFormatIndex(t, tc.ext), Password: "secret"}.options()
		if opts.Password != tc.want {
			t.Errorf("%s: password %q, want %q", tc.ext, opts.Password, tc.want)
		}
	}
}

// Switching to a format without encryption clears and disables the password;
// "Show password" shows it in clear.
func TestAddArchiveDialogPasswordFollowsTheFormat(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	d := newAddArchiveDialog("docs.zip", func(addArchiveSettings) {})
	if d.password.IsDisabled() || !d.password.PasswordMode {
		t.Fatal("zip: the password is asked for, masked")
	}
	d.password.SetText("secret")
	d.showPassword.State = 1
	d.showPassword.OnChange(1)
	if d.password.PasswordMode {
		t.Error("Show password left the password masked")
	}

	tgz := addArchiveFormatIndex(t, ".tar.gz")
	d.format.Menu.SetSelectPos(tgz)
	d.format.Menu.OnAction(tgz)
	if !d.password.IsDisabled() || d.password.GetText() != "" || !d.showPassword.IsDisabled() {
		t.Error("tar.gz cannot be encrypted: the password must be cleared and disabled")
	}
}

func addArchiveWithSettings(t *testing.T, settings addArchiveSettings) string {
	t.Helper()
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	old := showAddArchiveDialog
	t.Cleanup(func() { showAddArchiveDialog = old })
	showAddArchiveDialog = func(_ vfs.App, _ string, _ bool, done func(addArchiveSettings)) { done(settings) }

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), bytes.Repeat([]byte("data "), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &mockAppForProgress{t: t, activeVfs: vfs.NewOSVFS(dir), passiveVfs: vfs.NewOSVFS(dir), names: []string{"file1.txt"}, done: make(chan struct{})}
	actionAddArchive(app)
	<-app.done
	return filepath.Join(dir, settings.Name)
}

// A zip made with a password has its files encrypted.
func TestAddArchiveZipWithPasswordIsEncrypted(t *testing.T) {
	path := addArchiveWithSettings(t, addArchiveSettings{Name: "out.zip", Format: addArchiveFormatIndex(t, ".zip"), Password: "secret"})
	r, err := stdzip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if len(r.File) == 0 {
		t.Fatal("the archive is empty")
	}
	for _, f := range r.File {
		if f.Flags&0x1 == 0 {
			t.Errorf("%s is not encrypted", f.Name)
		}
	}
}

// A 7z made with a password cannot be read without it and can with it.
func TestAddArchive7zWithPasswordIsEncrypted(t *testing.T) {
	path := addArchiveWithSettings(t, addArchiveSettings{Name: "out.7z", Format: addArchiveFormatIndex(t, ".7z"), Solid: true, Password: "secret"})
	read := func(r *sevenzip.ReadCloser) ([]byte, error) {
		if len(r.File) == 0 {
			t.Fatal("the archive is empty")
		}
		rc, err := r.File[0].Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return io.ReadAll(rc)
	}

	if r, err := sevenzip.OpenReader(path); err == nil {
		data, rerr := read(r)
		_ = r.Close()
		if rerr == nil && bytes.Contains(data, []byte("data")) {
			t.Fatal("the files of the archive could be read without the password")
		}
	}
	r, err := sevenzip.OpenReaderWithPassword(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	data, err := read(r)
	if err != nil || !bytes.HasPrefix(data, []byte("data ")) {
		t.Fatalf("with the password: %q, %v", data[:min(len(data), 10)], err)
	}
}
