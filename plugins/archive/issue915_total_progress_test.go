package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unxed/archives"
	"github.com/unxed/zip"
)

// issue915ProgressRecorder keeps every transfer update Shift-F3 produces, so a
// test can look at how the two bars moved rather than only at where they
// stopped.
type issue915ProgressRecorder struct {
	mu      sync.Mutex
	updates []issue915ProgressUpdate
}

type issue915ProgressUpdate struct {
	name       string
	currentPct int
	totalText  string
	totalPct   int
}

func (r *issue915ProgressRecorder) UpdateScan(string, int64, int64) {}

func (r *issue915ProgressRecorder) UpdateTransfer(_ string, filename string, currentPct int, totalText string, totalPct int, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, issue915ProgressUpdate{
		name:       filename,
		currentPct: currentPct,
		totalText:  totalText,
		totalPct:   totalPct,
	})
}

func (r *issue915ProgressRecorder) IsCancelled() bool { return false }

func (r *issue915ProgressRecorder) snapshot() []issue915ProgressUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]issue915ProgressUpdate(nil), r.updates...)
}

// issue915BuildArchive writes an archive whose members are far larger than the
// archive file itself. That gap is what used to break the overall bar: counting
// consumed archive bytes against the archive size reaches 100% during the first
// member and stays there.
func issue915BuildArchive(t *testing.T, dir, name string, format archives.Archiver, members, memberSize int) (string, int64) {
	t.Helper()

	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, memberSize)
	for index := range payload {
		payload[index] = byte(index % 61)
	}
	names := make(map[string]string, members)
	for index := 0; index < members; index++ {
		memberName := fmt.Sprintf("member%02d.bin", index)
		if err := os.WriteFile(filepath.Join(source, memberName), payload, 0o644); err != nil {
			t.Fatal(err)
		}
		names[filepath.Join(source, memberName)] = memberName
	}

	files, err := archives.FilesFromDisk(context.Background(), nil, names)
	if err != nil {
		t.Fatal(err)
	}

	archivePath := filepath.Join(dir, name)
	output, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := format.Archive(context.Background(), output, files); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath, int64(members) * int64(memberSize)
}

func TestIssue915TestArchiveTotalProgressCountsMemberVolume(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		file   string
		format archives.Archiver
	}{
		{name: "zip", file: "fixture.zip", format: archives.Zip{Compression: zip.Deflate}},
		{name: "sevenzip", file: "fixture.7z", format: archives.SevenZip{}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const members, memberSize = 4, 512 * 1024
			archivePath, uncompressed := issue915BuildArchive(t, t.TempDir(), testCase.file, testCase.format, members, memberSize)

			stat, err := os.Stat(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			if stat.Size() >= uncompressed/2 {
				t.Fatalf("fixture is not compressible enough to be meaningful: %d bytes on disk, %d bytes of members", stat.Size(), uncompressed)
			}

			recorder := &issue915ProgressRecorder{}
			if err := testArchiveOnce(context.Background(), archivePath, "", recorder); err != nil {
				t.Fatalf("testArchiveOnce() error = %v", err)
			}

			updates := recorder.snapshot()
			if len(updates) < 3 {
				t.Fatalf("got %d progress updates, want the pass to report while it runs", len(updates))
			}

			wantDenominator := formatSize(uncompressed)
			previous := 0
			partial := false
			for index, update := range updates {
				if update.totalPct < 0 {
					t.Fatalf("update %d hid the overall bar: %+v", index, update)
				}
				if update.totalPct < previous {
					t.Fatalf("overall progress went backwards at update %d: %d after %d", index, update.totalPct, previous)
				}
				previous = update.totalPct
				if update.totalPct > 0 && update.totalPct < 100 {
					partial = true
				}
				if !strings.HasSuffix(update.totalText, "/ "+wantDenominator) {
					t.Fatalf("update %d counts against the wrong total: %q, want it to end in %q", index, update.totalText, "/ "+wantDenominator)
				}
			}
			if !partial {
				t.Fatalf("overall progress never showed a partial value: %+v", updates)
			}
			if final := updates[len(updates)-1]; final.totalPct != 100 {
				t.Fatalf("overall progress finished at %d%%, want 100%%", final.totalPct)
			}

			// The regression itself: the first member must not already report
			// the whole archive as done.
			if first := updates[1]; first.totalPct >= 100 {
				t.Fatalf("overall progress reached %d%% on the first member: %+v", first.totalPct, first)
			}
		})
	}
}

// TestIssue915TestArchiveTotalProgressWithoutDirectory covers the formats that
// cannot be listed without decoding them: tar and everything built on it. They
// keep the older, coarser measure, and it still has to run from start to end.
func TestIssue915TestArchiveTotalProgressWithoutDirectory(t *testing.T) {
	const members, memberSize = 4, 512 * 1024
	format := archives.CompressedArchive{Compression: archives.Gz{}, Archival: archives.Tar{}}
	archivePath, _ := issue915BuildArchive(t, t.TempDir(), "fixture.tar.gz", format, members, memberSize)

	recorder := &issue915ProgressRecorder{}
	if err := testArchiveOnce(context.Background(), archivePath, "", recorder); err != nil {
		t.Fatalf("testArchiveOnce() error = %v", err)
	}

	updates := recorder.snapshot()
	if len(updates) < 3 {
		t.Fatalf("got %d progress updates, want the pass to report while it runs", len(updates))
	}
	previous := 0
	for index, update := range updates {
		if update.totalPct < previous {
			t.Fatalf("overall progress went backwards at update %d: %d after %d", index, update.totalPct, previous)
		}
		previous = update.totalPct
	}
	if final := updates[len(updates)-1]; final.totalPct != 100 {
		t.Fatalf("overall progress finished at %d%%, want 100%%", final.totalPct)
	}
}

// TestIssue915TestArchiveTotalsSurviveADamagedDirectory keeps the listing pass
// from taking over the operation: an archive whose directory cannot be read is
// exactly what Shift-F3 exists to report on, so testing still has to run and
// still has to fail with the read error rather than with a listing error.
func TestIssue915TestArchiveTotalsSurviveADamagedDirectory(t *testing.T) {
	dir := t.TempDir()
	archivePath, _ := issue915BuildArchive(t, dir, "fixture.zip", archives.Zip{Compression: zip.Deflate}, 2, 64*1024)

	contents, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	// Damage the member payloads while leaving the local headers and the
	// central directory in place.
	for offset := len(contents) / 3; offset < len(contents)/2; offset++ {
		contents[offset] ^= 0xFF
	}
	if err := os.WriteFile(archivePath, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	recorder := &issue915ProgressRecorder{}
	err = testArchiveOnce(context.Background(), archivePath, "", recorder)
	if err == nil {
		t.Fatal("testArchiveOnce() reported a damaged archive as healthy")
	}
	if len(recorder.snapshot()) == 0 {
		t.Fatal("testArchiveOnce() reported no progress at all for a damaged archive")
	}
}
