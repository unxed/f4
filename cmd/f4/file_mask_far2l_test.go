package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// A destination that ends in a separator is a directory, even when its own
// name contains a wildcard. far2l reads the mask through PointToName, which
// yields an empty last component here, so ConvertWildcards returns FALSE and
// the path stays literal. The copy dialog opens on the passive panel's path
// with a separator appended, so this is the shape a panel sitting in such a
// directory produces on every F5.
func TestExecuteFileOp_WildcardDirectoryDestinationIsNotAMask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("'*' cannot appear in a Windows file name")
	}
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcRoot, "report.txt"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dstRoot, "notes*"), 0o700); err != nil {
		t.Fatal(err)
	}

	srcVfs := vfs.NewOSVFS(srcRoot)
	dstVfs := vfs.NewOSVFS(dstRoot)
	dest := filepath.Join(dstRoot, "notes*") + string(os.PathSeparator)
	done := make(chan struct{})
	ExecuteFileOpAt(nil, srcVfs, dstVfs, srcRoot, []string{"report.txt"}, dest, false, 2, func() { close(done) })
	waitForFileOpTest(t, done)

	want := filepath.Join(dstRoot, "notes*", "report.txt")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("copy into a wildcard-named directory did not land at %q: %v", want, err)
	}
	entries, err := os.ReadDir(dstRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "notes*" {
		t.Fatalf("destination root = %#v, want only the notes* directory; the "+
			"directory name was read as a rename mask", entries)
	}
}

// far2l applies the mask to the selected item only. Below it the tree is
// copied verbatim: ConvertWildcards is called once per selected name in
// ShellCopy's main loop and never inside the recursion.
func TestExecuteFileOp_MaskedTreeCopyRenamesOnlyTheSelectedItem(t *testing.T) {
	t.Cleanup(swapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	srcRoot := t.TempDir()
	dstRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcRoot, "docs.old", "inner.old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "docs.old", "top.txt"), []byte("top"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "docs.old", "inner.old", "deep.txt"), []byte("deep"), 0o600); err != nil {
		t.Fatal(err)
	}

	srcVfs := vfs.NewOSVFS(srcRoot)
	dstVfs := vfs.NewOSVFS(dstRoot)
	done := make(chan struct{})
	ExecuteFileOpAt(nil, srcVfs, dstVfs, srcRoot, []string{"docs.old"}, filepath.Join(dstRoot, "*.new"), false, 2, func() { close(done) })
	waitForFileOpTest(t, done)

	for _, rel := range []string{
		"docs.new",
		filepath.Join("docs.new", "top.txt"),
		filepath.Join("docs.new", "inner.old"),
		filepath.Join("docs.new", "inner.old", "deep.txt"),
	} {
		if _, err := os.Stat(filepath.Join(dstRoot, rel)); err != nil {
			t.Errorf("masked tree copy is missing %q: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dstRoot, "docs.new", "inner.new")); err == nil {
		t.Error("the mask reached a nested directory; far2l applies it to the selected item only")
	}
}

// A mask can collapse several sources onto one name: "?.bak" over ab.txt and
// ac.txt yields a.bak twice. far2l does nothing special about it — the second
// copy simply finds the file in place and goes through the ordinary overwrite
// prompt. This pins that the collision reaches the conflict decision instead
// of being silently renamed apart or silently clobbered.
func TestRecursiveCopy_MaskCollisionReachesTheOverwriteDecision(t *testing.T) {
	const mask = "?.bak"
	if first, second := applyFileMask("ab.txt", mask), applyFileMask("ac.txt", mask); first != "a.bak" || second != first {
		t.Fatalf("mask %q gave %q and %q, want both a.bak", mask, first, second)
	}

	newCase := func(t *testing.T) (vfs.VFS, string, vfs.VFS, string) {
		t.Helper()
		srcRoot := t.TempDir()
		dstRoot := t.TempDir()
		if err := os.WriteFile(filepath.Join(srcRoot, "ab.txt"), []byte("first"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcRoot, "ac.txt"), []byte("second"), 0o600); err != nil {
			t.Fatal(err)
		}
		return vfs.NewOSVFS(srcRoot), srcRoot, vfs.NewOSVFS(dstRoot), dstRoot
	}

	for _, tc := range []struct {
		name  string
		state *FileOpState
		want  string
	}{
		{"skip keeps the file already there", &FileOpState{SkipAll: true}, "first"},
		{"overwrite lets the second source win", &FileOpState{OverwriteAll: true}, "second"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcVfs, srcRoot, dstVfs, dstRoot := newCase(t)
			target := filepath.Join(dstRoot, applyFileMask("ab.txt", mask))
			for _, name := range []string{"ab.txt", "ac.txt"} {
				dest := filepath.Join(dstRoot, applyFileMask(name, mask))
				if err := recursiveCopy(context.Background(), srcVfs, filepath.Join(srcRoot, name), dstVfs, dest, tc.state, 0); err != nil {
					t.Fatalf("copy of %q: %v", name, err)
				}
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("%s = %q, want %q", target, got, tc.want)
			}
			entries, err := os.ReadDir(dstRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Errorf("destination = %#v, want the single collapsed name a.bak", entries)
			}
		})
	}
}
