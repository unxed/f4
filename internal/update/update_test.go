package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestUpdater_ParseUpdateHelperArgs(t *testing.T) {
	archive, kind, found, err := ParseHelperArgs([]string{HelperFlag, `C:\Users\Test User\f4-update.archive`, "zip"})
	if err != nil || !found {
		t.Fatalf("ParseHelperArgs() failed: found=%v err=%v", found, err)
	}
	if archive != `C:\Users\Test User\f4-update.archive` || kind != "zip" {
		t.Fatalf("ParseHelperArgs() = %q, %q; want archive path and zip", archive, kind)
	}

	if _, _, found, err := ParseHelperArgs([]string{HelperFlag, "archive.zip"}); !found || err == nil {
		t.Fatalf("malformed helper invocation: found=%v err=%v", found, err)
	}
	if _, _, found, err := ParseHelperArgs([]string{"--gui=win32"}); found || err != nil {
		t.Fatalf("normal invocation parsed as update helper: found=%v err=%v", found, err)
	}
}

func TestUpdater_ParseRestoreHelperArgs(t *testing.T) {
	backup, found, err := ParseRestoreHelperArgs([]string{RestoreHelperFlag, `C:\Users\Test User\f4-update-backup`})
	if err != nil || !found {
		t.Fatalf("ParseRestoreHelperArgs() failed: found=%v err=%v", found, err)
	}
	if backup != `C:\Users\Test User\f4-update-backup` {
		t.Fatalf("ParseRestoreHelperArgs() = %q; want backup path", backup)
	}

	if _, found, err := ParseRestoreHelperArgs([]string{RestoreHelperFlag}); !found || err == nil {
		t.Fatalf("malformed restore helper invocation: found=%v err=%v", found, err)
	}
	if _, found, err := ParseRestoreHelperArgs([]string{"--gui=win32"}); found || err != nil {
		t.Fatalf("normal invocation parsed as restore helper: found=%v err=%v", found, err)
	}
}

func TestUpdater_ManualBuildVersionUsesBuildTimestamp(t *testing.T) {
	manual := Build{Version: "manual-build-sha", TimeText: "2026-08-21T12:00:00Z"}

	newerLocalBuild := Release{TagName: "v0.2.0-beta", PublishedAt: "2026-08-20T12:00:00Z"}
	if stableReleaseNeedsUpdate(newerLocalBuild, manual, "") {
		t.Fatal("a manual build newer than the release must not request a downgrade")
	}

	newerRelease := Release{TagName: "v0.2.0-beta", PublishedAt: "2026-08-22T12:00:00Z"}
	if !stableReleaseNeedsUpdate(newerRelease, manual, "") {
		t.Fatal("a release newer than a manual build must be offered")
	}

	exact := Build{Version: "v0.2.0-beta", IsRelease: true, TimeText: manual.TimeText}
	if stableReleaseNeedsUpdate(newerRelease, exact, "") {
		t.Fatal("an exact release build must not request an update to itself")
	}

	// LastVersion is the "already installed" marker and suppresses the offer
	// on its own, whatever the build calls itself.
	if stableReleaseNeedsUpdate(newerRelease, manual, "v0.2.0-beta") {
		t.Fatal("an already installed release must not be offered again")
	}
}

func TestFormatBuildTimeUsesOneClockForVCSAndNightlyMetadata(t *testing.T) {
	fromVCS := FormatBuildTime("2026-08-23T06:49:17Z")
	fromReleaseBody := FormatBuildTime("2026-08-23 06:49:17")
	if fromVCS != fromReleaseBody {
		t.Fatalf("VCS time %q and release-body time %q diverged", fromVCS, fromReleaseBody)
	}
	if got := FormatBuildTime("not a timestamp"); got != "not a timestamp" {
		t.Fatalf("invalid timestamp = %q, want unchanged input", got)
	}
}

// targz packs files, name to content, the way a release archive holds them.
func targz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// installFixture puts an "old" f4 in a scratch directory and points the
// updater at it.
func installFixture(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "f4")
	if err := os.WriteFile(exe, []byte("old f4"), 0o755); err != nil { // #nosec G306 -- the fixture stands for an executable binary.
		t.Fatal(err)
	}
	oldExecutable, oldCheck := Executable, CheckInstalled
	Executable = func() (string, error) { return exe, nil }
	// The archives here hold text, not a program to start.
	CheckInstalled = func() error { return nil }
	t.Cleanup(func() { Executable, CheckInstalled = oldExecutable, oldCheck })
	return exe
}

// #1656: the updater downloaded android-plugin-linux-amd64.tar.gz in place of
// f4's own archive, unpacked it next to f4 and reported the update installed.
// An archive that leaves the running binary as it was is not an update.
func TestInstallFailsWhenTheArchiveLeavesTheBinary(t *testing.T) {
	exe := installFixture(t)

	err := Install(targz(t, map[string]string{"android-plugin": "a plugin"}), "targz")
	if err == nil {
		t.Fatal("Install() of an archive without f4 reported success")
	}
	if !strings.Contains(err.Error(), filepath.Base(exe)) {
		t.Errorf("error %q does not name the binary it failed to replace", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old f4" {
		t.Errorf("binary = %q, want it untouched", got)
	}
}

// The new binary here has the old one's size and may share its modification
// time on a coarse clock; the file being a new one is what tells.
func TestInstallReplacesTheBinary(t *testing.T) {
	exe := installFixture(t)

	if err := Install(targz(t, map[string]string{"f4": "new f4", "lang/ru.lng": "x"}), "targz"); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new f4" {
		t.Errorf("binary = %q, want the archive's", got)
	}
}
