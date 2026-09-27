package multiarc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCall is one command a backend ran through runToolIn.
type fakeCall struct {
	dir  string
	name string
	args []string
}

// fakeArchiver stands in for the archivers on PATH. It records every
// command and imitates what each does to files -- a compressor renames its
// input to or from its extension, "tar -r" and "tar --delete" append a
// marker to the archive they are given, "tar -c ... @orig" writes the
// original's content plus a marker -- so a test can follow a write through
// every step and check what finally lands on the archive's path.
type fakeArchiver struct {
	tools         map[string]bool  // on PATH
	tarVersion    string           // "tar --version" stdout
	tarVersionErr string           // "tar --version" stderr
	fail          map[string]error // "<tool> <first arg>" -> error to fail with
	// writeArchives makes zip and 7z write too: the archive named right
	// before "--" gets "|<tool>:<names>" appended, created if need be. Off,
	// they only record the command, which lets a test name an archive in a
	// directory that does not exist.
	writeArchives bool
	calls         []fakeCall
}

func (f *fakeArchiver) install(t *testing.T) {
	t.Helper()
	withFakeRunner(t, f.lookup, f.run)
}

func (f *fakeArchiver) lookup(name string) (string, error) {
	if f.tools[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errNotFoundStub
}

// commands returns every recorded call as "name arg arg...", skipping the
// "tar --version" probes, which say nothing about what was written.
func (f *fakeArchiver) commands() []string {
	var out []string
	for _, c := range f.calls {
		if c.name == "tar" && len(c.args) == 1 && c.args[0] == "--version" {
			continue
		}
		out = append(out, strings.Join(append([]string{c.name}, c.args...), " "))
	}
	return out
}

func (f *fakeArchiver) run(_ context.Context, dir, name string, args ...string) ([]byte, []byte, error) {
	args = append([]string(nil), args...)
	if isSevenZipName(name) && contains(args, "-scsUTF-8") {
		// A 7z list file is gone once the command returns: record the names
		// it held, as "@[a,b]", in place of its random path.
		if last := args[len(args)-1]; strings.HasPrefix(last, "@") {
			data, err := os.ReadFile(last[1:])
			if err != nil {
				return nil, nil, err
			}
			args[len(args)-1] = "@[" + strings.Join(strings.Fields(string(data)), ",") + "]"
		}
	}
	f.calls = append(f.calls, fakeCall{dir: dir, name: name, args: args})
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	if err, ok := f.fail[name+" "+first]; ok {
		return nil, []byte(name + " says no"), err
	}
	switch name {
	case "tar":
		return f.runTar(dir, args)
	case "gzip", "bzip2", "xz", "zstd", "lzip":
		return nil, nil, fakeCompressor(dir, name, args)
	case "zip", "7z", "7za", "7zr":
		if f.writeArchives {
			return nil, nil, fakeZipOr7z(dir, name, args)
		}
	}
	return nil, nil, nil
}

// fakeZipOr7z appends "|<tool>:<names>" to the archive argument, the one
// right before "--" or before the list file run recorded as "@[names]".
func fakeZipOr7z(dir, name string, args []string) error {
	for i, a := range args {
		if a == "--" && i > 0 {
			return appendFile(resolveIn(dir, args[i-1]), "|"+name+":"+strings.Join(args[i+1:], ","))
		}
	}
	if n := len(args); n > 1 && strings.HasPrefix(args[n-1], "@[") {
		return appendFile(resolveIn(dir, args[n-2]), "|"+name+":"+strings.TrimSuffix(strings.TrimPrefix(args[n-1], "@["), "]"))
	}
	return nil
}

func isSevenZipName(name string) bool {
	return name == "7z" || name == "7za" || name == "7zr"
}

// resolveIn is p as a tool running in dir would open it.
func resolveIn(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func (f *fakeArchiver) runTar(dir string, args []string) ([]byte, []byte, error) {
	if len(args) == 1 && args[0] == "--version" {
		return []byte(f.tarVersion), []byte(f.tarVersionErr), nil
	}
	archive := ""
	var names []string
	for i, a := range args {
		if a == "-f" && i+1 < len(args) {
			archive = args[i+1]
		}
		if a == "--" {
			names = args[i+1:]
			break
		}
	}
	target := resolveIn(dir, archive)
	switch {
	case contains(args, "-r"):
		return nil, nil, appendFile(target, "|r:"+strings.Join(names, ","))
	case contains(args, "--delete"):
		return nil, nil, appendFile(target, "|d:"+strings.Join(names, ","))
	case args[0] == "-c":
		content := ""
		if len(names) > 0 && strings.HasPrefix(names[0], "@") {
			orig, err := os.ReadFile(names[0][1:])
			if err != nil {
				return nil, nil, err
			}
			content, names = string(orig), names[1:]
		}
		// #nosec G703 -- target is the -f argument of a command the code under test built for a t.TempDir work directory.
		return nil, nil, os.WriteFile(target, []byte(content+"|c:"+strings.Join(names, ",")), 0o600)
	}
	return nil, nil, nil
}

var fakeCompressorExt = map[string]string{"gzip": ".gz", "bzip2": ".bz2", "xz": ".xz", "zstd": ".zst", "lzip": ".lz"}

// fakeCompressor renames its last argument the way the real tool would:
// "-d" strips the extension, anything else adds it.
func fakeCompressor(dir, name string, args []string) error {
	file := filepath.Join(dir, args[len(args)-1])
	ext := fakeCompressorExt[name]
	if contains(args, "-d") {
		return os.Rename(file, strings.TrimSuffix(file, ext))
	}
	return os.Rename(file, file+ext)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// fakeArchive writes an archive file named name holding content into a
// fresh directory, and returns its path.
func fakeArchive(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// readArchive returns the archive's content, failing the test if it is
// gone.
func readArchive(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// assertNoScratchLeft fails when a work or staging directory is still next
// to the archive at p.
func assertNoScratchLeft(t *testing.T, p string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".f4-multiarc-*"))
	if len(left) != 0 {
		t.Errorf("scratch directories left next to %s: %v", p, left)
	}
}
