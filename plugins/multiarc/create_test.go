package multiarc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

var workDirPattern = regexp.MustCompile(`[^ ]*\.f4-multiarc-[0-9]+`)

// placeholders rewrites commands so they compare across runs: the random
// work directory becomes <work> and the panel directory <src>.
func placeholders(cmds []string, srcDir string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		c = workDirPattern.ReplaceAllString(c, "<work>")
		out[i] = strings.ReplaceAll(c, srcDir, "<src>")
	}
	return out
}

func toolSet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// TestCreateArchivePicksTheTool walks the format/tool matrix Add to archive
// supports: which tool builds each format, with what command, and that the
// result lands on the target's path.
func TestCreateArchivePicksTheTool(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		name    string
		target  string
		tools   []string
		version string
		stderr  string
		want    []string
		dirs    []string // "src" or "work": where each command ran
		content string   // what the fake tools leave in the archive
	}{
		{
			name: "zip with Info-ZIP", target: "out.zip", tools: []string{"zip", "7z", "tar"}, version: bsdTarVersion,
			want: []string{"zip -q -r -nw <work>" + sep + "out.zip -- a.txt @x"}, dirs: []string{"src"},
			content: "|zip:a.txt,@x",
		},
		{
			name: "zip with 7za", target: "out.jar", tools: []string{"7za"},
			want: []string{"7za a -tzip -y -scsUTF-8 <work>" + sep + "out.jar @[a.txt,@x]"}, dirs: []string{"src"},
			content: "|7za:a.txt,@x",
		},
		{
			name: "zip with bsdtar, as on stock Windows", target: "out.zip", tools: []string{"tar"}, version: winTarVersion,
			want: []string{"tar -c --format zip -f <work>" + sep + "out.zip -C <src> -- a.txt ./@x"}, dirs: []string{"work"},
			content: "|c:a.txt,./@x",
		},
		{
			name: "7z with 7zr", target: "out.7z", tools: []string{"7zr"},
			want: []string{"7zr a -t7z -y -scsUTF-8 <work>" + sep + "out.7z @[a.txt,@x]"}, dirs: []string{"src"},
			content: "|7zr:a.txt,@x",
		},
		{
			name: "plain tar with GNU tar", target: "out.tar", tools: []string{"tar"}, version: gnuTarVersion,
			want: []string{"tar -c -f <work>" + sep + "out.tar -C <src> -- a.txt @x"}, dirs: []string{"work"},
			content: "|c:a.txt,@x",
		},
		{
			name: "tar.gz with bsdtar's own zlib", target: "out.tar.gz", tools: []string{"tar"}, version: winTarVersion,
			want: []string{"tar -c -z -f <work>" + sep + "out.tar.gz -C <src> -- a.txt ./@x"}, dirs: []string{"work"},
			content: "|c:a.txt,./@x",
		},
		{
			name: "tar.xz with GNU tar and xz", target: "out.tar.xz", tools: []string{"tar", "xz"}, version: gnuTarVersion,
			want: []string{"tar -c -f <work>" + sep + "work.tar -C <src> -- a.txt @x", "xz -f work.tar"}, dirs: []string{"work", "work"},
			content: "|c:a.txt,@x",
		},
		{
			name: "tgz with BusyBox tar and gzip", target: "out.tgz", tools: []string{"tar", "gzip"}, stderr: busyBoxTarVersion,
			want: []string{"tar -c -f <work>" + sep + "work.tar -C <src> -- a.txt @x", "gzip -f work.tar"}, dirs: []string{"work", "work"},
			content: "|c:a.txt,@x",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeArchiver{tools: toolSet(c.tools...), tarVersion: c.version, tarVersionErr: c.stderr, writeArchives: true}
			f.install(t)
			srcDir := t.TempDir()
			target := filepath.Join(t.TempDir(), c.target)
			if err := createArchive(context.Background(), srcDir, []string{"a.txt", "@x"}, target); err != nil {
				t.Fatalf("createArchive: %v", err)
			}
			if got := placeholders(f.commands(), srcDir); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("commands =\n\t%q\nwant\n\t%q", got, c.want)
			}
			var ran []fakeCall
			for _, call := range f.calls {
				isProbe := call.name == "tar" && len(call.args) == 1
				if !isProbe {
					ran = append(ran, call)
				}
			}
			for i, where := range c.dirs {
				inWork := workDirPattern.MatchString(ran[i].dir)
				if (where == "work") != inWork || (where == "src" && ran[i].dir != srcDir) {
					t.Errorf("command %d ran in %q, want the %s directory", i, ran[i].dir, where)
				}
			}
			if got := readArchive(t, target); got != c.content {
				t.Errorf("archive = %q, want %q", got, c.content)
			}
			assertNoScratchLeft(t, target)
		})
	}
}

func TestCreateArchiveRefusals(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		tools   []string
		version string
		want    string
	}{
		{"zip with GNU tar only", "out.zip", []string{"tar", "gzip"}, gnuTarVersion, "needs zip, 7z, 7za or bsdtar"},
		{"7z with nothing", "out.7z", nil, "", "needs 7z, 7za or 7zr"},
		{"tar with no tar", "out.tar", []string{"zip"}, "", "needs tar on PATH"},
		{"tar.zst without zstd", "out.tar.zst", []string{"tar"}, gnuTarVersion, "needs zstd on PATH"},
		{"tar.xz on a bsdtar without liblzma", "out.txz", []string{"tar"}, winTarVersion, "needs xz on PATH"},
		{"an unknown suffix lists what can be made", "out.rar", []string{"tar", "gzip"}, gnuTarVersion, "one of .tar.gz, .tar"},
		{"nothing at all", "out.rar", nil, "", errNoCreator.Error()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeArchiver{tools: toolSet(c.tools...), tarVersion: c.version, writeArchives: true}
			f.install(t)
			target := filepath.Join(t.TempDir(), c.target)
			err := createArchive(context.Background(), t.TempDir(), []string{"a"}, target)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("createArchive = %v, want an error containing %q", err, c.want)
			}
			if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
				t.Errorf("a refused create left %s behind", target)
			}
		})
	}
}

func TestDefaultCreateSuffix(t *testing.T) {
	cases := []struct {
		tools   []string
		version string
		stderr  string
		want    string
	}{
		{[]string{"zip"}, "", "", ".zip"},
		{[]string{"tar", "gzip"}, "", busyBoxTarVersion, ".tar.gz"},
		{[]string{"7zr"}, "", "", ".7z"},
		{[]string{"tar"}, "", busyBoxTarVersion, ".tar"},
		{[]string{"tar"}, winTarVersion, "", ".zip"},
		{nil, "", "", ".zip"},
	}
	for _, c := range cases {
		f := &fakeArchiver{tools: toolSet(c.tools...), tarVersion: c.version, tarVersionErr: c.stderr}
		f.install(t)
		if got := defaultCreateSuffix(context.Background()); got != c.want {
			t.Errorf("tools %v: defaultCreateSuffix = %q, want %q", c.tools, got, c.want)
		}
	}
}

// An archive created inside one of the folders being archived would be
// packed into itself while it is being written.
func TestCreateArchiveRefusesTargetInsideSelection(t *testing.T) {
	f := &fakeArchiver{tools: toolSet("zip"), writeArchives: true}
	f.install(t)
	srcDir := t.TempDir()
	err := createArchive(context.Background(), srcDir, []string{"a.txt", "dir"}, filepath.Join(srcDir, "dir", "out.zip"))
	if err == nil || !strings.Contains(err.Error(), "which is being archived") {
		t.Fatalf("createArchive = %v, want a refusal", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("commands ran: %q", f.commands())
	}
}

// A failed or canceled create leaves an existing archive of that name --
// the one the user agreed to overwrite -- exactly as it was.
func TestCreateArchiveFailureKeepsExistingTarget(t *testing.T) {
	target := fakeArchive(t, "out.zip", "OLD")
	f := &fakeArchiver{tools: toolSet("zip"), writeArchives: true, fail: map[string]error{"zip -q": errors.New("exit status 12")}}
	f.install(t)
	err := createArchive(context.Background(), t.TempDir(), []string{"a"}, target)
	if err == nil || !strings.Contains(err.Error(), "zip says no") {
		t.Fatalf("createArchive = %v, want zip's own failure", err)
	}
	if got := readArchive(t, target); got != "OLD" {
		t.Fatalf("target = %q, want it untouched", got)
	}
	assertNoScratchLeft(t, target)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	f.fail = nil
	if err := createArchive(canceled, t.TempDir(), []string{"a"}, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("createArchive after cancel = %v, want context.Canceled", err)
	}
	if got := readArchive(t, target); got != "OLD" {
		t.Fatalf("target = %q after a canceled create, want it untouched", got)
	}
	assertNoScratchLeft(t, target)
}
