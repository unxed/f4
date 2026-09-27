package multiarc

import (
	"context"
	"path/filepath"
	"testing"
)

// createRealRoundTrip creates an archive named name from a small tree with
// the tools on PATH, then opens it through multiarc and reads a member
// back. Names starting with "-" and "@" ride along: each tool has its own
// way of mistaking one for an option or a list file.
func createRealRoundTrip(t *testing.T, name string) {
	t.Helper()
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"top.txt":       "top",
		"-dash.txt":     "dash",
		"@at.txt":       "at",
		"dir/sub/b.txt": "b",
	})
	target := filepath.Join(t.TempDir(), name)
	if err := createArchive(context.Background(), src, []string{"top.txt", "-dash.txt", "@at.txt", "dir"}, target); err != nil {
		t.Fatalf("createArchive %s: %v", name, err)
	}
	t.Cleanup(closeSharedMultiArcTempDirs)
	v := openReal(t, target)
	for p, want := range map[string]string{"/top.txt": "top", "/-dash.txt": "dash", "/@at.txt": "at", "/dir/sub/b.txt": "b"} {
		if got := readMember(t, v, p); got != want {
			t.Errorf("%s: %s = %q, want %q", name, p, got, want)
		}
	}
	assertNoScratchLeft(t, target)
}

func TestCreateRealZipWithInfoZip(t *testing.T) {
	requireRealTool(t, "zip", "unzip")
	createRealRoundTrip(t, "x.zip")
}

func TestCreateRealZipWithSevenZip(t *testing.T) {
	requireRealTool(t, "unzip")
	for _, bin := range []string{"7z", "7za"} {
		if toolAvailable(bin) {
			pathOnly(t, map[string]string{"unzip": "unzip", bin: bin})
			createRealRoundTrip(t, "x.zip")
			return
		}
	}
	t.Skip("neither 7z nor 7za is on PATH")
}

// Stock Windows 10+ has tar.exe, which is bsdtar, and neither zip nor 7z:
// it still makes a zip.
func TestCreateRealZipWithBSDTar(t *testing.T) {
	unzip := realToolPath(t, "unzip") // before useRealBSDTar may narrow PATH
	useRealBSDTar(t)
	pathOnly(t, map[string]string{"unzip": unzip, "tar": realToolPath(t, "tar")})
	createRealRoundTrip(t, "x.zip")
}

func TestCreateRealSevenZip(t *testing.T) {
	if _, ok := sevenZipTool(); !ok {
		t.Skip("no 7z, 7za or 7zr on PATH")
	}
	createRealRoundTrip(t, "x.7z")
}

func TestCreateRealTarballs(t *testing.T) {
	requireRealTool(t, "tar")
	info := probeTar(context.Background())
	for _, name := range []string{"x.tar", "x.tar.gz", "x.tar.bz2", "x.tar.xz"} {
		t.Run(name, func(t *testing.T) {
			// Without bsdtar's own library, making the tarball and reading it
			// back both need the stand-alone compressor.
			if comp, plain, _ := tarCompressionFor(name); !plain {
				builtIn := info.flavor == tarBSD && bsdCanCompress(info, comp)
				if !builtIn && !toolAvailable(comp.tool) {
					t.Skipf("%s is not on PATH", comp.tool)
				}
			}
			createRealRoundTrip(t, name)
		})
	}
}

// The same with bsdtar as tar, using its own compression libraries.
func TestCreateRealTarballsWithBSDTar(t *testing.T) {
	useRealBSDTar(t)
	createRealRoundTrip(t, "x.tar.gz")
}

// realToolPath is where the current PATH finds name, for pathOnly to link
// again under the same name.
func realToolPath(t *testing.T, name string) string {
	t.Helper()
	p, err := lookupTool(name)
	if err != nil {
		t.Skipf("%s is not on PATH", name)
	}
	if toolCannotStart(p) {
		t.Skipf("%s is on PATH but cannot start", name)
	}
	return p
}
