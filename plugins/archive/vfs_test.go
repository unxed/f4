package archive

import (
	"compress/gzip"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sync"
	"sync/atomic"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/tar"
	"github.com/unxed/zip"
	"github.com/unxed/zipper/archive"
)

func TestArchiveVFS_PathSlashes(t *testing.T) {
	v := &ArchiveVFS{
		arcPath:   filepath.FromSlash("C:/path/to/archive.zip"),
		innerPath: "folder/file.txt",
	}

	path := v.GetPath()
	expected := filepath.Join(v.arcPath, filepath.FromSlash(v.innerPath))

	if path != expected {
		t.Errorf("ArchiveVFS.GetPath slashes mismatch.\nGot:      %q\nExpected: %q", path, expected)
	}
}

func TestArchiveVFS_LocalArchivePath(t *testing.T) {
	root := t.TempDir()
	v := &ArchiveVFS{
		parent:    vfs.NewOSVFS(root),
		arcPath:   filepath.Join("archives", "bundle.zip"),
		innerPath: "folder",
	}

	got, ok := v.LocalArchivePath()
	want := filepath.Join(root, "archives", "bundle.zip")
	if !ok || got != want {
		t.Fatalf("LocalArchivePath() = (%q, %t), want (%q, true)", got, ok, want)
	}
}

func TestArchiveVFS_LocalArchivePathRejectsNonLocalParent(t *testing.T) {
	v := &ArchiveVFS{arcPath: "remote.zip"}
	if got, ok := v.LocalArchivePath(); ok || got != "" {
		t.Fatalf("LocalArchivePath() = (%q, %t), want (empty, false)", got, ok)
	}
}

func TestArchiveVFS_SkipsExplicitRootEntryDuringScan(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "root-entry.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	entries := []struct {
		name string
		mode int64
		body string
	}{
		{name: "./", mode: 0755 | int64(fs.ModeDir)},
		{name: "./nested/", mode: 0755 | int64(fs.ModeDir)},
		{name: "./nested/file.txt", mode: 0644, body: "hello"},
	}
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	archiveVFS, err := NewArchiveVFS(vfs.NewOSVFS(tmpDir), tarPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := archiveVFS.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	var items []vfs.VFSItem
	if err := archiveVFS.ReadDir(context.Background(), archiveVFS.GetPath(), func(chunk []vfs.VFSItem) {
		items = append(items, chunk...)
	}); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name == "" || item.Name == "." || item.Name == ".." {
			t.Fatalf("root placeholder leaked into archive listing: %#v", item)
		}
	}

	stats, err := vfs.CalculateStats(context.Background(), archiveVFS, archiveVFS.GetPath(), []string{""}, nil)
	if err != nil {
		t.Fatalf("scanning archive with explicit root entry: %v", err)
	}
	if stats.Files != 1 || stats.Dirs != 2 || stats.Bytes != int64(len("hello")) {
		t.Fatalf("archive stats = %+v, want one file, two directories, and five bytes", stats)
	}
}

func TestArchiveVFS_RemoveCreatedZipDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("keep.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "keep"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	archiveVFS, err := NewArchiveVFS(vfs.NewOSVFS(tmpDir), "test.zip")
	if err != nil {
		t.Fatalf("create ArchiveVFS: %v", err)
	}
	t.Cleanup(func() {
		if err := archiveVFS.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	ctx := context.Background()
	dirPath := archiveVFS.Join(archiveVFS.GetPath(), "2del")
	if err := archiveVFS.MkDir(ctx, dirPath); err != nil {
		t.Fatalf("create archive directory: %v", err)
	}
	if err := archiveVFS.Remove(ctx, dirPath); err != nil {
		t.Fatalf("remove archive directory: %v", err)
	}

	var items []vfs.VFSItem
	if err := archiveVFS.ReadDir(ctx, archiveVFS.GetPath(), func(chunk []vfs.VFSItem) {
		items = append(items, chunk...)
	}); err != nil {
		t.Fatalf("read archive root: %v", err)
	}
	keepFound := false
	for _, item := range items {
		if item.Name == "2del" {
			t.Fatal("removed directory is still present in the archive")
		}
		if item.Name == "keep.txt" {
			keepFound = true
		}
	}
	if !keepFound {
		t.Fatal("unrelated archive file was not preserved")
	}
}

func TestArchiveVFS_PublicFallbackPathsUseNativeSeparators(t *testing.T) {
	v := &ArchiveVFS{arcPath: filepath.Join("archive-root", "bundle.zip"), innerPath: "."}
	joined := v.Join("outside", "folder", "file.txt")
	wantJoined := filepath.Join("outside", "folder", "file.txt")
	if joined != wantJoined {
		t.Fatalf("Join fallback = %q, want native path %q", joined, wantJoined)
	}
	if dir := v.Dir(joined); dir != filepath.Dir(wantJoined) {
		t.Fatalf("Dir fallback = %q, want native path %q", dir, filepath.Dir(wantJoined))
	}
	if os.PathSeparator == '\\' && strings.Contains(joined, "/") {
		t.Fatalf("Windows fallback path leaked forward separators: %q", joined)
	}
}

func TestArchiveVFS_Abs(t *testing.T) {
	root := t.TempDir()
	arcPath := filepath.Join(root, "test.zip")
	v := &ArchiveVFS{
		arcPath:   arcPath,
		innerPath: "folder",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Relative path inside archive",
			input:    "file.txt",
			expected: filepath.Join(arcPath, "folder", "file.txt"),
		},
		{
			name:     "Absolute path (full path with archive)",
			input:    filepath.Join(arcPath, "other"),
			expected: filepath.Join(arcPath, "other"),
		},
		{
			name:     "Root-style path inside archive",
			input:    filepath.Join(root, "manual", "root"),
			expected: filepath.Join(root, "manual", "root"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := filepath.Clean(tt.expected)
			got, _ := v.Abs(tt.input)
			if got != exp {
				t.Errorf("ArchiveVFS.Abs(%q): expected %q, got %q", tt.input, exp, got)
			}
		})
	}
}

func TestArchiveVFS_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	arcPath := filepath.Join(tmp, "test.zip")

	if err := os.WriteFile(arcPath, []byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	origInfo, _ := os.Stat(arcPath)

	v, err := NewArchiveVFS(&vfs.OSVFS{}, arcPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := v.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	wc, err := v.Create(context.Background(), v.Join(arcPath, "newfile.txt"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := wc.Write([]byte("some data")); err != nil {
		t.Fatal(err)
	}

	currentInfo, _ := os.Stat(arcPath)
	if currentInfo.Size() != origInfo.Size() {
		t.Error("Original archive size changed BEFORE Close() - not atomic!")
	}
	if err := wc.Close(); err != nil {
		t.Fatalf("commit atomic archive write: %v", err)
	}
}

func TestArchiveVFS_TempFileLeak(t *testing.T) {
	tmpDir := t.TempDir()

	zipPath := filepath.Join(tmpDir, "test_leak.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	vOuter, err := NewArchiveVFS(&vfs.OSVFS{}, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vOuter.Close(); err != nil {
			t.Errorf("close outer archive VFS: %v", err)
		}
	})

	rc, err := vOuter.Open(context.Background(), vOuter.Join(zipPath, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// Trigger lazy extraction which creates the temp file
	if _, err := rc.ReadAt(context.Background(), make([]byte, 1), 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}

	var tempFilePath string
	if wrapper, ok := rc.(*vfs.TempFileWrapper); ok {
		tempFilePath = wrapper.TempPath
	} else if wrapper, ok := rc.(interface{ TempPath() string }); ok {
		tempFilePath = wrapper.TempPath()
	} else {
		t.Fatalf("Expected wrapper with TempPath, got %T", rc)
	}

	if _, err := os.Stat(tempFilePath); os.IsNotExist(err) {
		t.Fatalf("Temp file was not created at expected path: %s", tempFilePath)
	}
	t.Logf("Temp file created successfully at: %s", tempFilePath)

	if err := rc.Close(); err != nil {
		t.Fatalf("close extracted temporary file: %v", err)
	}

	if _, err := os.Stat(tempFilePath); err == nil {
		_ = os.Remove(tempFilePath) // t.TempDir cleanup will retry
		t.Fatalf("TEST FAILED: Temp file %s was not deleted after Close()! Leak detected.", tempFilePath)
	}

	t.Log("SUCCESS: Temp file was properly deleted.")
}

// TestArchiveVFS_DeferredClose verifies that closing the ArchiveVFS is deferred
// while there are active readers or writers (grace period of inactivity).
func TestArchiveVFS_DeferredClose(t *testing.T) {
	oldIdleTTL := archiveVFSIdleTTL
	archiveVFSIdleTTL = 50 * time.Millisecond
	t.Cleanup(func() { archiveVFSIdleTTL = oldIdleTTL })

	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")

	// Create a zip with multiple files
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	w1, err := zw.Create("file1.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Write([]byte("content1")); err != nil {
		t.Fatal(err)
	}

	w2, err := zw.Create("file2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write([]byte("content2")); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// 1. Open the archive VFS
	vArc, err := NewArchiveVFS(&vfs.OSVFS{}, zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Open the first file (simulates beginning of copy/extraction)
	rc1, err := vArc.Open(context.Background(), vArc.Join(zipPath, "file1.txt"))
	if err != nil {
		_ = vArc.Close() // preserve the open failure
		t.Fatal(err)
	}
	if err := rc1.Close(); err != nil {
		t.Fatalf("close first archive reader: %v", err)
	}

	// 3. Simulate exiting the panel (closing the VFS) while extraction is active
	errClose := vArc.Close()
	if errClose != nil {
		t.Logf("vArc.Close() returned error: %v", errClose)
	}

	// 4. Try to open the second file immediately. It should succeed (grace period active)
	rc2, errRead2 := vArc.Open(context.Background(), vArc.Join(zipPath, "file2.txt"))
	if errRead2 != nil {
		t.Fatalf("BUG: Open file2 failed after VFS Close: %v. Expected to succeed due to active copy grace period.", errRead2)
	}
	if err := rc2.Close(); err != nil {
		t.Fatalf("close second archive reader: %v", err)
	}

	// 5. Wait for the shortened test TTL to expire and perform cleanup. Poll
	// the private state instead of sleeping for exactly the TTL: a timer can
	// legitimately run a little late on a busy Darwin or Windows runner.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		vArc.mu.Lock()
		cleaned := vArc.cleanupTimer == nil && vArc.fsys == nil && vArc.closer == nil
		vArc.mu.Unlock()
		if cleaned {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline.C:
			t.Fatal("timed out waiting for archive VFS cleanup")
		}
	}

	// 6. Try to open the file again. It should fail now as the VFS has been fully cleaned up.
	_, errRead3 := vArc.Open(context.Background(), vArc.Join(zipPath, "file1.txt"))
	if errRead3 == nil {
		t.Error("VFS failed to perform cleanup after inactivity grace period")
	}
}

type dummyFileInfo struct {
	name string
	size int64
}

func (d dummyFileInfo) Name() string       { return d.name }
func (d dummyFileInfo) Size() int64        { return d.size }
func (d dummyFileInfo) Mode() fs.FileMode  { return 0644 }
func (d dummyFileInfo) ModTime() time.Time { return time.Now() }
func (d dummyFileInfo) IsDir() bool        { return false }
func (d dummyFileInfo) Sys() any           { return nil }

type mockSlowFile struct {
	readBlock chan struct{}
}

func (m *mockSlowFile) Read(p []byte) (int, error) {
	<-m.readBlock
	return 0, io.EOF
}

func (m *mockSlowFile) Stat() (fs.FileInfo, error) {
	return dummyFileInfo{name: "somefile.txt", size: 100}, nil
}

func (m *mockSlowFile) Close() error {
	return nil
}

func TestArchiveReadWrapper_CloseNonBlocking(t *testing.T) {
	readBlock := make(chan struct{})
	f := &mockSlowFile{readBlock: readBlock}
	v := &ArchiveVFS{activeCount: 1}
	w := &archiveReadWrapper{
		v: v,
		f: f,
	}

	go func() {
		buf := make([]byte, 10)
		_, _ = w.ReadAt(context.Background(), buf, 0)
	}()

	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- w.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close archive read wrapper: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("archiveReadWrapper.Close() blocked because of active extraction holding the mutex!")
	}

	close(readBlock)
}

type mockSlowArchiveFS struct {
	archive.FileSystem
	openBlock chan struct{}
	readBlock chan struct{}
}

func (m *mockSlowArchiveFS) Open(name string) (fs.File, error) {
	if m.openBlock != nil {
		<-m.openBlock
	}
	return &mockSlowFile{readBlock: m.readBlock}, nil
}

func TestArchiveVFS_OpenCloseNonBlocking(t *testing.T) {
	openBlock := make(chan struct{})
	readBlock := make(chan struct{})

	v := &ArchiveVFS{
		fsys: &mockSlowArchiveFS{openBlock: openBlock, readBlock: readBlock},
	}

	ctx := context.WithValue(context.Background(), vfs.ReporterKey, &mockVFSProgressReporter{})

	openDone := make(chan struct{})
	var openErr error
	go func() {
		_, openErr = v.Open(ctx, "somefile.txt")
		close(openDone)
	}()

	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- v.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close archive VFS: %v", err)
		}
	case <-time.After(10 * time.Second):
		// Generous: on the happy path Close returns immediately, and a bound
		// this loose still catches the regression — Close blocking forever.
		t.Fatal("BUG REPRODUCED: ArchiveVFS.Close() blocked because v.Open() holds the mutex during extraction/seeking!")
	}

	close(openBlock)
	close(readBlock)
	<-openDone
	_ = openErr
}

// mockSeekingReporter is written from the progress ticker goroutine and read
// from the test goroutine, so its fields live behind a mutex.
type mockSeekingReporter struct {
	mu            sync.Mutex
	lastAction    string
	lastFilename  string
	lastTotalText string
	lastSpeedText string
}

func (r *mockSeekingReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (r *mockSeekingReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastAction = action
	r.lastFilename = filename
	r.lastTotalText = totalText
	r.lastSpeedText = speedText
}
func (r *mockSeekingReporter) IsCancelled() bool { return false }

// waitForStatus waits until the reporter shows a status with the given prefix
// alongside an elapsed-time field. The progress ticker fires every 250ms for
// as long as the phase lasts, so on any machine the status arrives eventually;
// sleeping a fixed interval and asserting — what this test used to do — is a
// bet on the scheduler that a loaded CI runner loses.
func (r *mockSeekingReporter) waitForStatus(t *testing.T, prefix string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		total, speed := r.lastTotalText, r.lastSpeedText
		r.mu.Unlock()
		if strings.HasPrefix(total, prefix) && strings.Contains(speed, "Time:") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no %q status with elapsed time; last total %q, speed %q", prefix, r.lastTotalText, r.lastSpeedText)
}

func TestArchiveVFS_Open_SeekingProgress(t *testing.T) {
	openBlock := make(chan struct{})
	readBlock := make(chan struct{})

	v := &ArchiveVFS{
		fsys: &mockSlowArchiveFS{openBlock: openBlock, readBlock: readBlock},
	}

	reporter := &mockSeekingReporter{}
	ctx := context.WithValue(context.Background(), vfs.ReporterKey, vfs.TaskReporter(reporter))

	openDone := make(chan struct{})
	var openErr error
	go func() {
		_, openErr = v.Open(ctx, "somefile.txt")
		close(openDone)
	}()

	// 1. While Open is blocked the ticker must report "Locating file..."
	// with an elapsed-time field.
	reporter.waitForStatus(t, "Locating file")
	close(openBlock)

	// 2. While Read is blocked it must report "Seeking/Decompressing...".
	reporter.waitForStatus(t, "Seeking/Decompressing")

	close(readBlock)
	<-openDone
	_ = openErr
}

type mockVFSProgressReporter struct {
	called  bool
	lastPct int
}

func (r *mockVFSProgressReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (r *mockVFSProgressReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	r.called = true
	r.lastPct = currentPct
}
func (r *mockVFSProgressReporter) IsCancelled() bool { return false }

func TestArchiveVFS_Open_ProgressReporting(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "progress_test.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("Progress test data")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	vArc, err := NewArchiveVFS(&vfs.OSVFS{}, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vArc.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	callbackCalled := false
	var lastCallbackPct int
	progressCB := func(msg string, percent int) {
		callbackCalled = true
		lastCallbackPct = percent
	}

	reporter := &mockVFSProgressReporter{}

	ctx := context.Background()
	ctx = context.WithValue(ctx, vfs.ProgressKey, vfs.ProgressCallback(progressCB))
	ctx = context.WithValue(ctx, vfs.ReporterKey, vfs.TaskReporter(reporter))

	rc, err := vArc.Open(ctx, vArc.Join(zipPath, "test.txt"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() { _ = rc.Close() }()

	if !callbackCalled {
		t.Error("ProgressCallback was not invoked during Open")
	}
	if !reporter.called {
		t.Error("TaskReporter was not invoked during Open")
	}
	if lastCallbackPct != 100 || reporter.lastPct != 100 {
		t.Errorf("Expected final progress to be 100%%, got Callback=%d%%, Reporter=%d%%", lastCallbackPct, reporter.lastPct)
	}

	buf := make([]byte, 18)
	n, err := rc.ReadAt(context.Background(), buf, 0)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if n != 18 || string(buf) != "Progress test data" {
		t.Errorf("Read data mismatch: got %q, want 'Progress test data'", string(buf[:n]))
	}
}

type dummyReporter struct{}

func (d *dummyReporter) UpdateScan(currentPath string, files, dirs int64) {}
func (d *dummyReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
}
func (d *dummyReporter) IsCancelled() bool { return false }

func TestArchiveVFSCopyBulk(t *testing.T) {
	tmpDir := t.TempDir()

	zipPath := filepath.Join(tmpDir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	zw := zip.NewWriter(f)
	w1, err := zw.Create("file1.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w1.Write([]byte("content1")); err != nil {
		t.Fatal(err)
	}

	w2, err := zw.Create("folder/file2.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write([]byte("content2")); err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parentVFS := vfs.NewOSVFS(tmpDir)
	archiveVFS, err := NewArchiveVFS(parentVFS, "test.zip")
	if err != nil {
		t.Fatalf("failed to create ArchiveVFS: %v", err)
	}
	t.Cleanup(func() {
		if err := archiveVFS.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	dstDir := filepath.Join(tmpDir, "extracted")
	dstVFS := vfs.NewOSVFS(dstDir)
	err = dstVFS.MkDir(context.Background(), dstDir)
	if err != nil {
		t.Fatal(err)
	}

	copier, ok := interface{}(archiveVFS).(vfs.BulkCopier)
	if !ok {
		t.Fatal("expected ArchiveVFS to implement BulkCopier")
	}

	err = copier.CopyBulk(context.Background(), []string{"file1.txt", "folder/file2.txt"}, dstVFS, dstDir, &dummyReporter{})
	if err != nil {
		t.Fatalf("Bulk copy failed: %v", err)
	}

	f1, err := dstVFS.Open(context.Background(), filepath.Join(dstDir, "file1.txt"))
	if err != nil {
		t.Fatal("file1.txt was not extracted")
	}
	defer func() { _ = f1.Close() }()
	data1, _ := io.ReadAll(ctxReader{r: f1, ctx: context.Background()})
	if string(data1) != "content1" {
		t.Errorf("expected content1, got %q", string(data1))
	}

	f2, err := dstVFS.Open(context.Background(), filepath.Join(dstDir, "folder/file2.txt"))
	if err != nil {
		t.Fatal("folder/file2.txt was not extracted")
	}
	defer func() { _ = f2.Close() }()
	data2, _ := io.ReadAll(ctxReader{r: f2, ctx: context.Background()})
	if string(data2) != "content2" {
		t.Errorf("expected content2, got %q", string(data2))
	}
}
func TestArchiveVFSCopyBulk_Tar(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "test.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}

	tw := tar.NewWriter(f)
	hdr1 := &tar.Header{
		Name: "file1.txt",
		Mode: 0644,
		Size: 8,
	}
	if err := tw.WriteHeader(hdr1); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("content1")); err != nil {
		t.Fatal(err)
	}

	hdr2 := &tar.Header{
		Name: "folder/file2.txt",
		Mode: 0644,
		Size: 8,
	}
	if err := tw.WriteHeader(hdr2); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("content2")); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parentVFS := vfs.NewOSVFS(tmpDir)
	archiveVFS, err := NewArchiveVFS(parentVFS, "test.tar")
	if err != nil {
		t.Fatalf("failed to create ArchiveVFS: %v", err)
	}
	t.Cleanup(func() {
		if err := archiveVFS.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	dstDir := filepath.Join(tmpDir, "extracted")
	dstVFS := vfs.NewOSVFS(dstDir)
	err = dstVFS.MkDir(context.Background(), dstDir)
	if err != nil {
		t.Fatal(err)
	}

	copier, ok := interface{}(archiveVFS).(vfs.BulkCopier)
	if !ok {
		t.Fatal("expected ArchiveVFS to implement BulkCopier")
	}

	err = copier.CopyBulk(context.Background(), []string{"file1.txt", "folder/file2.txt"}, dstVFS, dstDir, &dummyReporter{})
	if err != nil {
		t.Fatalf("Bulk copy failed: %v", err)
	}

	f1, err := dstVFS.Open(context.Background(), filepath.Join(dstDir, "file1.txt"))
	if err != nil {
		t.Fatal("file1.txt was not extracted")
	}
	defer func() { _ = f1.Close() }()
	data1, _ := io.ReadAll(ctxReader{r: f1, ctx: context.Background()})
	if string(data1) != "content1" {
		t.Errorf("expected content1, got %q", string(data1))
	}

	f2, err := dstVFS.Open(context.Background(), filepath.Join(dstDir, "folder/file2.txt"))
	if err != nil {
		t.Fatal("folder/file2.txt was not extracted")
	}
	defer func() { _ = f2.Close() }()
	data2, _ := io.ReadAll(ctxReader{r: f2, ctx: context.Background()})
	if string(data2) != "content2" {
		t.Errorf("expected content2, got %q", string(data2))
	}
}

func TestArchiveVFSCopyBulk_CompressedTar(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "file.txt", Mode: 0644, Size: 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	archiveVFS, err := NewArchiveVFS(vfs.NewOSVFS(tmpDir), "test.tar.gz")
	if err != nil {
		t.Fatalf("failed to create ArchiveVFS: %v", err)
	}
	t.Cleanup(func() { _ = archiveVFS.Close() })

	dstDir := filepath.Join(tmpDir, "extracted")
	dstVFS := vfs.NewOSVFS(dstDir)
	if err := dstVFS.MkDir(context.Background(), dstDir); err != nil {
		t.Fatal(err)
	}

	if err := archiveVFS.CopyBulk(context.Background(), []string{"file.txt"}, dstVFS, dstDir, &dummyReporter{}); err != nil {
		t.Fatalf("bulk copy from compressed tar failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "file.txt"))
	if err != nil {
		t.Fatal("file.txt was not extracted:", err)
	}
	if string(data) != "content" {
		t.Fatalf("extracted content = %q, want %q", data, "content")
	}
}

func TestArchiveVFSCopyBulk_ConcurrentQueue(t *testing.T) {
	tmpDir := t.TempDir()

	zipPath := filepath.Join(tmpDir, "test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w1, _ := zw.Create("file1.txt")
	if _, err := w1.Write([]byte("content1")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	parentVFS := vfs.NewOSVFS(tmpDir)
	archiveVFS, err := NewArchiveVFS(parentVFS, "test.zip")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := archiveVFS.Close(); err != nil {
			t.Errorf("close archive VFS: %v", err)
		}
	})

	absPath := archiveVFS.activePath()

	// 1. Manually lock the archive file path from the main thread
	if !vfs.GlobalArchiveLockManager.TryLock(absPath) {
		t.Fatal("expected to acquire lock")
	}

	dstDir := filepath.Join(tmpDir, "extracted")
	dstVFS := vfs.NewOSVFS(dstDir)
	if err := dstVFS.MkDir(context.Background(), dstDir); err != nil {
		t.Fatal(err)
	}

	copier := interface{}(archiveVFS).(vfs.BulkCopier)

	// 2. Start CopyBulk in a background goroutine.
	// It should block waiting for the lock.
	var wg sync.WaitGroup
	wg.Add(1)
	copyFinished := false

	go func() {
		defer wg.Done()
		// Auto-queue so the VFS doesn't block waiting for UI interaction.
		ctx := WithAutoQueue(context.Background())
		err := copier.CopyBulk(ctx, []string{"file1.txt"}, dstVFS, dstDir, &dummyReporter{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		copyFinished = true
	}()

	// Sleep to let the goroutine block on Lock() inside CopyBulk
	time.Sleep(50 * time.Millisecond)
	if copyFinished {
		t.Fatal("expected CopyBulk to be blocked on lock")
	}

	// 3. Unlock the file from the main thread, which should wake up the CopyBulk
	vfs.GlobalArchiveLockManager.Unlock(absPath)
	wg.Wait()

	if !copyFinished {
		t.Fatal("expected CopyBulk to finish successfully after unlock")
	}

	// Verify file is extracted
	f1, err := dstVFS.Open(context.Background(), filepath.Join(dstDir, "file1.txt"))
	if err != nil {
		t.Fatal("file1.txt was not extracted")
	}
	defer func() { _ = f1.Close() }()
	data1, _ := io.ReadAll(ctxReader{r: f1, ctx: context.Background()})
	if string(data1) != "content1" {
		t.Errorf("expected content1, got %q", string(data1))
	}
}

type mockTickerReporter struct {
	calls atomic.Int32
	ticks chan struct{}
}

func (m *mockTickerReporter) UpdateScan(string, int64, int64) {}
func (m *mockTickerReporter) UpdateTransfer(action, filename string, currentPct int, totalText string, totalPct int, speedText string) {
	m.calls.Add(1)
	if m.ticks != nil {
		select {
		case m.ticks <- struct{}{}:
		default:
		}
	}
}
func (m *mockTickerReporter) IsCancelled() bool { return false }

// TestIssue149_TimeTicksDuringBlockingIO ensures that the progress dialog receives
// continuous time updates even when the underlying archive I/O is blocked.
// This addresses the "Time updates unevenly/jumps" user claim.
func TestIssue149_TimeTicksDuringBlockingIO(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	rep := &mockTickerReporter{ticks: make(chan struct{}, 4)}

	getStatus := func() (string, string, int) {
		return "Extracting", "file.txt", 50
	}

	go runProgressTicker(ctx, done, rep, getStatus)

	// Wait for four updates while the ticker remains blocked in its simulated
	// I/O phase. Waiting for actual ticks avoids coupling this test to scheduler
	// timing on slower CI runners.
	const expectedTicks = 4
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	for i := 0; i < expectedTicks; i++ {
		select {
		case <-rep.ticks:
		case <-timeout.C:
			close(done)
			t.Fatalf("Expected progress ticker to fire at least %d times during blocking I/O, but got %d", expectedTicks, rep.calls.Load())
		}
	}
	close(done)

	if calls := rep.calls.Load(); calls < expectedTicks {
		t.Errorf("Expected progress ticker to fire at least %d times during blocking I/O, but got %d", expectedTicks, calls)
	}
}
