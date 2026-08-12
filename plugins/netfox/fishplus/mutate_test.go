package fishplus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMutationsAgainstLocalShell(t *testing.T) {
	c := newLocalShellClient(t)
	ctx := context.Background()
	root := t.TempDir()

	nested := filepath.Join(root, "one two", "three")
	if err := c.MkDir(ctx, nested); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if info, err := os.Stat(nested); err != nil || !info.IsDir() {
		t.Fatalf("mkdir did not create %q: %v", nested, err)
	}
	// Creating it again must not fail, the panel may race with itself.
	if err := c.MkDir(ctx, nested); err != nil {
		t.Errorf("mkdir of an existing directory: %v", err)
	}

	file := filepath.Join(nested, "a file.txt")
	if err := os.WriteFile(file, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "one two", "moved file.txt")
	if err := c.Rename(ctx, file, moved); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(file); err == nil {
		t.Error("the source of a rename survived")
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("rename did not produce %q: %v", moved, err)
	}

	if err := c.Chmod(ctx, moved, 0100600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	info, err := os.Stat(moved)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	if err := c.Chmod(ctx, moved, 0644); err != nil {
		t.Fatalf("chmod back: %v", err)
	}

	// The rename above moved the file out of "three" and into its parent,
	// so the parent is the one that is not empty now.
	if err := c.RemoveDir(ctx, filepath.Dir(moved)); err == nil {
		t.Error("rmdir removed a directory that is not empty")
	}
	if err := c.Remove(ctx, moved); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(moved); err == nil {
		t.Error("rm left the file behind")
	}
	// A missing file is not an error: rm -f is what the helper runs.
	if err := c.Remove(ctx, moved); err != nil {
		t.Errorf("rm of a missing file: %v", err)
	}
	if err := c.RemoveDir(ctx, nested); err != nil {
		t.Fatalf("rmdir of an empty directory: %v", err)
	}

	deep := filepath.Join(root, "tree", "a", "b")
	if err := c.MkDir(ctx, deep); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "leaf"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveAll(ctx, filepath.Join(root, "tree")); err != nil {
		t.Fatalf("rmtree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tree")); err == nil {
		t.Error("rmtree left the tree behind")
	}
}

func TestMutationsRequireSafePaths(t *testing.T) {
	c := newLocalShellClient(t)
	ctx := context.Background()
	dir := t.TempDir()

	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}

	// filepath.Join would clean the ".." away, which is exactly the kind of
	// path the helper must not have to trust, so these are built by hand.
	unsafe := []string{"", "/", "relative/path", dir + "/../escape", "/..", dir + "/.."}
	for _, p := range unsafe {
		if err := c.MkDir(ctx, p); err == nil {
			t.Errorf("mkdir accepted %q", p)
		}
		if err := c.Remove(ctx, p); err == nil {
			t.Errorf("rm accepted %q", p)
		}
		if err := c.RemoveDir(ctx, p); err == nil {
			t.Errorf("rmdir accepted %q", p)
		}
		if err := c.RemoveAll(ctx, p); err == nil {
			t.Errorf("rmtree accepted %q", p)
		}
		if err := c.Chmod(ctx, p, 0644); err == nil {
			t.Errorf("chmod accepted %q", p)
		}
		if err := c.Rename(ctx, victim, p); err == nil {
			t.Errorf("mv accepted %q as a destination", p)
		}
		if err := c.Rename(ctx, p, filepath.Join(dir, "landing")); err == nil {
			t.Errorf("mv accepted %q as a source", p)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("a refused mutation moved the file anyway: %v", err)
	}

	// A name that merely starts with dots is not a ".." component and must
	// stay usable, or the guard has grown too wide.
	dotty := filepath.Join(dir, "..hidden")
	if err := os.WriteFile(dotty, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.Chmod(ctx, dotty, 0600); err != nil {
		t.Errorf("a name starting with dots was refused: %v", err)
	}
	if err := c.Remove(ctx, dotty); err != nil {
		t.Errorf("removing a name starting with dots: %v", err)
	}

	resp, err := c.Session().ExecPath(ctx, "chmod", dir, "99z")
	if err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if resp.OK() {
		t.Error("chmod accepted a mode that is not octal")
	}
}

// TestChownAgainstLocalShell can only ask for what the test user is allowed
// to do, so it re-applies the ownership the file already has; that still
// exercises the argument shape, and running as root exercises a real change.
func TestChownAgainstLocalShell(t *testing.T) {
	c := newLocalShellClient(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "owned file")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !c.Session().Features().Has("chown") {
		t.Skip("no chown on this host")
	}

	if err := c.Chown(ctx, file, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("chown to the current owner: %v", err)
	}
	e, err := c.Lstat(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if e.Uid != os.Getuid() || e.Gid != os.Getgid() {
		t.Errorf("owner = %d:%d, want %d:%d", e.Uid, e.Gid, os.Getuid(), os.Getgid())
	}

	if err := c.Chown(ctx, file, -1, -1); err != nil {
		t.Errorf("chown with nothing to change: %v", err)
	}

	if os.Getuid() == 0 {
		if err := c.Chown(ctx, file, 12345, 12346); err != nil {
			t.Fatalf("chown as root: %v", err)
		}
		if e, err = c.Lstat(ctx, file); err != nil {
			t.Fatal(err)
		}
		if e.Uid != 12345 || e.Gid != 12346 {
			t.Errorf("owner = %d:%d, want 12345:12346", e.Uid, e.Gid)
		}
		if err := c.Chown(ctx, file, -1, 12347); err != nil {
			t.Fatalf("chown group only: %v", err)
		}
		if e, err = c.Lstat(ctx, file); err != nil {
			t.Fatal(err)
		}
		if e.Uid != 12345 || e.Gid != 12347 {
			t.Errorf("owner = %d:%d, want 12345:12347", e.Uid, e.Gid)
		}
	}

	if err := c.Chown(ctx, "relative/path", os.Getuid(), -1); err == nil {
		t.Error("a relative path was accepted")
	}
	if got, err := c.Session().Ping(ctx, "alive"); err != nil || got != "alive" {
		t.Fatalf("session out of sync after chown: %q %v", got, err)
	}
}

func TestChtimesAgainstLocalShell(t *testing.T) {
	c := newLocalShellClient(t)
	checkChtimes(t, c)
}

// TestChtimesOnSimulatedBSDHost puts stubs in front of touch and date that
// behave the way macOS and BSD do: no epoch for touch -d, and an epoch for
// date -r instead of date -d. That is the branch no GNU box would take.
func TestChtimesOnSimulatedBSDHost(t *testing.T) {
	realTouch, err := exec.LookPath("touch")
	if err != nil {
		t.Skip("no touch on this host")
	}
	realDate, err := exec.LookPath("date")
	if err != nil {
		t.Skip("no date on this host")
	}
	bin := t.TempDir()
	stubs := map[string]string{
		"touch": "#!/bin/sh\nfor a in \"$@\"; do\n case $a in -d) exit 1 ;; esac\ndone\nexec " + realTouch + " \"$@\"\n",
		"date":  "#!/bin/sh\ncase $1 in\n -r) shift; e=$1; shift; exec " + realDate + " -d \"@$e\" \"$@\" ;;\n -d) exit 1 ;;\nesac\nexec " + realDate + " \"$@\"\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	c := newLocalShellClientEnv(t, "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := exec.Command(filepath.Join(bin, "touch"), "-d", "@0", filepath.Join(t.TempDir(), "x")).CombinedOutput(); err == nil {
		t.Fatalf("the touch stub accepted -d: %s", out)
	}
	checkChtimes(t, c)
}

func checkChtimes(t *testing.T, c *Client) {
	t.Helper()
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "timed file")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !c.Session().Features().Has("touch") {
		t.Skip("no touch on this host")
	}

	mtime := time.Unix(1400000000, 0)
	atime := time.Unix(1300000000, 0)
	if err := c.Chtimes(ctx, file, mtime, atime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	e, err := c.Lstat(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if e.MTime.Unix() != mtime.Unix() {
		t.Errorf("mtime = %v, want %v", e.MTime, mtime)
	}
	if e.ATime.Unix() != atime.Unix() {
		t.Errorf("atime = %v, want %v", e.ATime, atime)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Unix() != mtime.Unix() {
		t.Errorf("mtime seen locally = %v, want %v", info.ModTime(), mtime)
	}

	// Both timestamps at once is the one call path that touches neither -m
	// nor -a, and it is what copying a file uses most of the time.
	same := time.Unix(1234567890, 0)
	if err := c.Chtimes(ctx, file, same, same); err != nil {
		t.Fatalf("chtimes with equal times: %v", err)
	}
	if e, err = c.Lstat(ctx, file); err != nil {
		t.Fatal(err)
	}
	if e.MTime.Unix() != same.Unix() || e.ATime.Unix() != same.Unix() {
		t.Errorf("times = %v / %v, want %v", e.MTime, e.ATime, same)
	}

	// The modification time alone, with the access time left as it is.
	if err := c.Chtimes(ctx, file, mtime, time.Time{}); err != nil {
		t.Fatalf("chtimes mtime only: %v", err)
	}
	if e, err = c.Lstat(ctx, file); err != nil {
		t.Fatal(err)
	}
	if e.MTime.Unix() != mtime.Unix() {
		t.Errorf("mtime = %v, want %v", e.MTime, mtime)
	}
	if e.ATime.Unix() != same.Unix() {
		t.Errorf("atime = %v, want it untouched at %v", e.ATime, same)
	}

	if err := c.Chtimes(ctx, file, time.Time{}, time.Time{}); err != nil {
		t.Errorf("chtimes with nothing to change: %v", err)
	}
	if got, err := c.Session().Ping(ctx, "alive"); err != nil || got != "alive" {
		t.Fatalf("session out of sync after utime: %q %v", got, err)
	}
}

func TestSymlinkAgainstLocalShell(t *testing.T) {
	c := newLocalShellClient(t)
	ctx := context.Background()
	root := t.TempDir()

	if !c.CanSymlink() {
		t.Skip("no ln on this machine")
	}

	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	// A relative target is the ordinary case and the one a path guard would
	// wrongly refuse, so it goes first.
	link := filepath.Join(root, "relative link")
	if err := c.Symlink(ctx, link, "target.txt"); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != "target.txt" {
		t.Errorf("target = %q, want %q", got, "target.txt")
	}
	// It has to resolve, which is what tells a stored string from a working link.
	if body, err := os.ReadFile(link); err != nil || string(body) != "hi" {
		t.Errorf("reading through the link: %q, %v", body, err)
	}
	// And the helper must report it as a link rather than as its target.
	if e, err := c.Lstat(ctx, link); err != nil {
		t.Errorf("lstat: %v", err)
	} else if !e.IsSymlink() {
		t.Errorf("Mode = %o, want a symlink", e.Mode)
	}

	// A target that does not exist is legal; a dangling link is a link.
	dangling := filepath.Join(root, "dangling")
	if err := c.Symlink(ctx, dangling, "nothing here"); err != nil {
		t.Fatalf("symlink to a missing target: %v", err)
	}
	if got, err := os.Readlink(dangling); err != nil || got != "nothing here" {
		t.Errorf("dangling target = %q, %v", got, err)
	}

	// So is a target with .. in it, which the guard must not touch.
	updots := filepath.Join(root, "updots")
	if err := c.Symlink(ctx, updots, "../elsewhere/file"); err != nil {
		t.Fatalf("symlink to a .. target: %v", err)
	}
	if got, err := os.Readlink(updots); err != nil || got != "../elsewhere/file" {
		t.Errorf(".. target = %q, %v", got, err)
	}

	// The target travels as a path line, so one with a newline in it has to
	// survive the escaping the same way any other path does.
	weird := filepath.Join(root, "weird target")
	if err := c.Symlink(ctx, weird, "two\nlines"); err != nil {
		t.Fatalf("symlink to a target with a newline: %v", err)
	}
	if got, err := os.Readlink(weird); err != nil || got != "two\nlines" {
		t.Errorf("newline target = %q, %v", got, err)
	}

	// An existing link path is refused rather than replaced.
	if err := c.Symlink(ctx, link, "target.txt"); err == nil {
		t.Error("creating a link over an existing one succeeded")
	}
	// Including when it is a directory, which is the case where ln would
	// otherwise put the link inside it instead of failing.
	dir := filepath.Join(root, "a dir")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := c.Symlink(ctx, dir, "target.txt"); err == nil {
		t.Error("creating a link over an existing directory succeeded")
	}
	if _, err := os.Lstat(filepath.Join(dir, "target.txt")); err == nil {
		t.Error("the link was created inside the directory")
	}

	// An empty target is unspecified in POSIX, so it is refused here.
	if err := c.Symlink(ctx, filepath.Join(root, "empty"), ""); err == nil {
		t.Error("an empty target was accepted")
	}
	// And the guard still applies to the link itself.
	if err := c.Symlink(ctx, "relative/link", "target.txt"); err == nil {
		t.Error("a relative link path was accepted")
	}

	// Every refusal above must have left the session usable.
	if err := c.Session().Noop(ctx); err != nil {
		t.Errorf("noop after the refusals: %v", err)
	}
}
