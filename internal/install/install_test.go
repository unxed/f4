package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHasDirOnPath(t *testing.T) {
	cases := []struct {
		name    string
		pathEnv string
		dir     string
		want    bool
	}{
		{"missing", "/usr/bin:/bin", "/home/u/.local/bin", false},
		{"present", "/usr/bin:/home/u/.local/bin:/bin", "/home/u/.local/bin", true},
		{"present with trailing slash on PATH entry", "/usr/bin:/home/u/.local/bin/:/bin", "/home/u/.local/bin", true},
		{"present with trailing slash on dir", "/usr/bin:/home/u/.local/bin:/bin", "/home/u/.local/bin/", true},
		{"empty PATH", "", "/home/u/.local/bin", false},
		{"blank entries ignored", "::/home/u/.local/bin::", "/home/u/.local/bin", true},
		{"prefix but not equal", "/home/u/.local/bin2", "/home/u/.local/bin", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := HasDirOnPath(c.pathEnv, c.dir); got != c.want {
				t.Errorf("HasDirOnPath(%q, %q) = %v, want %v", c.pathEnv, c.dir, got, c.want)
			}
		})
	}
}

func TestDetectShellProfile(t *testing.T) {
	home := "/home/u"
	cases := []struct {
		shellEnv string
		wantOK   bool
		wantName string
		wantPath string
	}{
		{"/bin/bash", true, "bash", filepath.Join(home, ".bashrc")},
		{"/usr/bin/zsh", true, "zsh", filepath.Join(home, ".zshrc")},
		{"/usr/local/bin/fish", true, "fish", filepath.Join(home, ".config", "fish", "config.fish")},
		{"/bin/dash", false, "", ""},
		{"", false, "", ""},
		{"  /bin/bash  ", true, "bash", filepath.Join(home, ".bashrc")},
	}
	for _, c := range cases {
		t.Run(c.shellEnv, func(t *testing.T) {
			profile, ok := DetectShellProfile(home, c.shellEnv)
			if ok != c.wantOK {
				t.Fatalf("DetectShellProfile(%q) ok = %v, want %v", c.shellEnv, ok, c.wantOK)
			}
			if !ok {
				return
			}
			if profile.Shell != c.wantName || profile.Path != c.wantPath {
				t.Errorf("DetectShellProfile(%q) = %+v, want shell %q path %q", c.shellEnv, profile, c.wantName, c.wantPath)
			}
		})
	}
}

func TestPathLine(t *testing.T) {
	home := "/home/u"
	bash := ShellProfile{Shell: "bash", Path: "/home/u/.bashrc"}
	fish := ShellProfile{Shell: "fish", Path: "/home/u/.config/fish/config.fish"}

	if got, want := PathLine(bash, home, "/home/u/.local/bin"), `export PATH="$HOME/.local/bin:$PATH"`; got != want {
		t.Errorf("bash line = %q, want %q", got, want)
	}
	if got, want := PathLine(fish, home, "/home/u/.local/bin"), `set -gx PATH $HOME/.local/bin $PATH`; got != want {
		t.Errorf("fish line = %q, want %q", got, want)
	}
	// A directory outside home is shown verbatim, not forced under $HOME.
	if got, want := PathLine(bash, home, "/opt/f4/bin"), `export PATH="/opt/f4/bin:$PATH"`; got != want {
		t.Errorf("outside-home line = %q, want %q", got, want)
	}
	// An unrecognized shell (zero ShellProfile) still gets a usable line.
	if got, want := PathLine(ShellProfile{}, home, "/home/u/.local/bin"), `export PATH="$HOME/.local/bin:$PATH"`; got != want {
		t.Errorf("unknown-shell line = %q, want %q", got, want)
	}
}

func TestChooseInstallDirPrefersPreferred(t *testing.T) {
	home := t.TempDir()
	calls := []string{}
	ensure := func(dir string) (bool, error) {
		calls = append(calls, dir)
		return false, nil
	}
	dir, fellBack, err := ChooseInstallDir(home, ensure)
	if err != nil {
		t.Fatalf("ChooseInstallDir: %v", err)
	}
	if fellBack {
		t.Errorf("fellBack = true, want false")
	}
	if want := PreferredDir(home); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if len(calls) != 1 || calls[0] != PreferredDir(home) {
		t.Errorf("ensureDir calls = %v, want exactly [%q]", calls, PreferredDir(home))
	}
}

func TestChooseInstallDirFallsBackWhenPreferredFails(t *testing.T) {
	home := t.TempDir()
	ensure := func(dir string) (bool, error) {
		if dir == PreferredDir(home) {
			return false, errors.New("read-only filesystem")
		}
		return false, nil
	}
	dir, fellBack, err := ChooseInstallDir(home, ensure)
	if err != nil {
		t.Fatalf("ChooseInstallDir: %v", err)
	}
	if !fellBack {
		t.Errorf("fellBack = false, want true")
	}
	if want := FallbackDir(home); dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
}

func TestChooseInstallDirFailsWhenBothFail(t *testing.T) {
	home := t.TempDir()
	ensure := func(dir string) (bool, error) { return false, errors.New("nope") }
	if _, _, err := ChooseInstallDir(home, ensure); err == nil {
		t.Fatal("ChooseInstallDir: want error when both directories fail, got nil")
	}
}

func TestEnsureDirCreatesAndDetectsExisting(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "bin")

	existed, err := EnsureDir(dir)
	if err != nil {
		t.Fatalf("EnsureDir(create): %v", err)
	}
	if existed {
		t.Errorf("existed = true on first creation, want false")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("EnsureDir did not create a directory at %s: %v", dir, err)
	}

	existed, err = EnsureDir(dir)
	if err != nil {
		t.Fatalf("EnsureDir(existing): %v", err)
	}
	if !existed {
		t.Errorf("existed = false on second call, want true")
	}
}

func TestEnsureDirRejectsFileInThePlaceOfADir(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "notadir")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil { // #nosec G306 -- test fixture, not sensitive
		t.Fatal(err)
	}
	if _, err := EnsureDir(path); err == nil {
		t.Fatal("EnsureDir: want error when path is a plain file, got nil")
	}
}

func TestCopyExecutablePreservesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f4-src")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "f4")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := CopyExecutable(src, dst); err != nil {
		t.Fatalf("CopyExecutable: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "#!/bin/sh\necho hi\n" {
		t.Errorf("dst content = %q", data)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("dst mode = %v, want executable bits set", info.Mode())
	}
}

func TestCopyExecutableSetsExecBitEvenWhenSourceLacksIt(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f4-src")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "f4")

	if err := CopyExecutable(src, dst); err != nil {
		t.Fatalf("CopyExecutable: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("dst mode = %v, want executable bits set despite non-executable source", info.Mode())
	}
}

func TestCopyExecutableOverwritesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "f4-src")
	if err := os.WriteFile(src, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "f4")
	if err := os.WriteFile(dst, []byte("old binary, longer content"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := CopyExecutable(src, dst); err != nil {
		t.Fatalf("CopyExecutable: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new binary" {
		t.Errorf("dst content = %q, want %q", data, "new binary")
	}
	if _, err := os.Stat(dst + ".new"); !os.IsNotExist(err) {
		t.Errorf("temp file %s.new left behind: %v", dst, err)
	}
}

func TestAppendProfileLineCreatesFileAndAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	line := `export PATH="$HOME/.local/bin:$PATH"`

	added, err := AppendProfileLine(path, line)
	if err != nil {
		t.Fatalf("AppendProfileLine(new file): %v", err)
	}
	if !added {
		t.Errorf("added = false on new file, want true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != line+"\n" {
		t.Errorf("content = %q, want %q", data, line+"\n")
	}
}

func TestAppendProfileLineIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".zshrc")
	line := `export PATH="$HOME/.local/bin:$PATH"`

	if _, err := AppendProfileLine(path, line); err != nil {
		t.Fatalf("first AppendProfileLine: %v", err)
	}
	added, err := AppendProfileLine(path, line)
	if err != nil {
		t.Fatalf("second AppendProfileLine: %v", err)
	}
	if added {
		t.Errorf("added = true on second call, want false (already present)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != line+"\n" {
		t.Errorf("content after two calls = %q, want exactly one line %q", got, line+"\n")
	}
}

func TestAppendProfileLinePreservesExistingContentAndAddsNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(path, []byte("# my existing config, no trailing newline"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := `export PATH="$HOME/.local/bin:$PATH"`

	added, err := AppendProfileLine(path, line)
	if err != nil {
		t.Fatalf("AppendProfileLine: %v", err)
	}
	if !added {
		t.Errorf("added = false, want true")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# my existing config, no trailing newline\n" + line + "\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", data, want)
	}
}

func TestAppendProfileLineCreatesFishConfigDir(t *testing.T) {
	home := t.TempDir()
	profile, ok := DetectShellProfile(home, "/usr/bin/fish")
	if !ok {
		t.Fatal("DetectShellProfile(fish) = not ok")
	}
	line := PathLine(profile, home, PreferredDir(home))

	added, err := AppendProfileLine(profile.Path, line)
	if err != nil {
		t.Fatalf("AppendProfileLine: %v", err)
	}
	if !added {
		t.Errorf("added = false, want true")
	}
	if _, err := os.Stat(profile.Path); err != nil {
		t.Fatalf("fish config not created: %v", err)
	}
}
