package vfs

import (
	"context"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestNullVFS_DirectoryListing(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	var files []string
	if err := v.ReadDir(ctx, "/", func(items []VFSItem) {
		for _, item := range items {
			files = append(files, item.Name)
		}
	}); err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	// 6 static files + 'upload' + 'scenarios' = 8
	if len(files) != 8 {
		t.Errorf("Expected 8 items in root, got %d", len(files))
	}
}

func TestNullVFS_Stat(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	// 1. Root static file
	stat, err := v.Stat(ctx, "/10MB.bin")
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat.Size != 10*1024*1024 {
		t.Errorf("Expected 10MB size, got %d", stat.Size)
	}

	// 2. Upload dir
	statDir, _ := v.Stat(ctx, "/upload")
	if !statDir.IsDir {
		t.Error("/upload should be a directory")
	}

	// 3. Non-existent / uploaded file
	statDummy, _ := v.Stat(ctx, "/upload/test.txt")
	if statDummy.Size != 0 {
		t.Error("Dummy uploaded file should have size 0")
	}
}

func TestNullVFS_Throttling(t *testing.T) {
	// Speed: 10 MB/s
	speed := int64(10 * 1024 * 1024)
	v := NewNullVFS(speed)
	ctx := context.Background()

	f, err := v.Open(ctx, "/10MB.bin")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	buf := make([]byte, 1024*1024) // 1 MB chunk

	start := time.Now()
	n, err := f.Read(ctx, buf)
	duration := time.Since(start)

	if err != nil || n != len(buf) {
		t.Fatalf("Read failed. n=%d, err=%v", n, err)
	}

	// 1 MB at 10 MB/s should take ~100ms
	expectedMs := 100
	actualMs := int(duration.Milliseconds())

	// Allow some jitter
	if actualMs < expectedMs-20 || actualMs > expectedMs+50 {
		t.Errorf("Throttling inaccurate: expected ~%dms, got %dms", expectedMs, actualMs)
	}
}

func TestNullVFS_Writer(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	// Prevent overwriting static root files
	_, err := v.Create(ctx, "/10MB.bin")
	if err == nil {
		t.Error("Create should prevent overwriting static files at root")
	}

	// Allow creating in upload
	w, err := v.Create(ctx, "/upload/test.txt")
	if err != nil {
		t.Fatalf("Create failed in upload dir: %v", err)
	}

	n, err := w.Write([]byte("test data"))
	if err != nil || n != 9 {
		t.Errorf("Write failed: n=%d, err=%v", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestNullVFS_ReadZeroes(t *testing.T) {
	v := NewNullVFS(0)
	f, _ := v.Open(context.Background(), "/1KB.bin")

	buf := []byte{1, 2, 3, 4, 5}
	n, _ := f.Read(context.Background(), buf)

	if n != 5 {
		t.Errorf("Expected to read 5 bytes, got %d", n)
	}

	// Buffer should be overwritten with zeroes
	for i, b := range buf {
		if b != 0 {
			t.Errorf("Buffer at %d was not zeroed: %d", i, b)
		}
	}
}
func TestNullVFS_EOF(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()
	f, _ := v.Open(ctx, "/1KB.bin")

	// Seek to end
	buf := make([]byte, 10)
	n, err := f.ReadAt(ctx, buf, 1024)
	if n != 0 || err != io.EOF {
		t.Errorf("Expected EOF at offset 1024, got n=%d, err=%v", n, err)
	}
}

func TestNullVFS_Cancellation(t *testing.T) {
	// Speed 1 byte / second
	v := NewNullVFS(1)
	ctx, cancel := context.WithCancel(context.Background())

	f, _ := v.Open(ctx, "/1KB.bin")

	// Trigger cancellation in 50ms
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	buf := make([]byte, 100)
	_, err := f.Read(ctx, buf)
	duration := time.Since(start)

	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	// Should return almost immediately (~50ms), not in 100 seconds
	if duration > 500*time.Millisecond {
		t.Errorf("Cancellation took too long: %v", duration)
	}
}

func TestNullVFS_BasicMethods(t *testing.T) {
	v := NewNullVFS(0)

	if !v.IsAtRoot() {
		t.Error("Should be at root initially")
	}

	if v.GetPath() != filepath.FromSlash("/") {
		t.Errorf("Expected path /, got %s", v.GetPath())
	}

	if err := v.SetPath("/upload"); err != nil {
		t.Fatalf("SetPath(/upload) failed: %v", err)
	}
	if v.IsAtRoot() {
		t.Error("Should not be at root after SetPath(/upload)")
	}

	if v.Join("/a", "b") != "/a/b" {
		t.Errorf("Join failed: %s", v.Join("/a", "b"))
	}

	if v.Base("/upload/file.bin") != "file.bin" {
		t.Errorf("Base failed: %s", v.Base("/upload/file.bin"))
	}

	if v.Dir("/upload/file.bin") != "/upload" {
		t.Errorf("Dir failed: %s", v.Dir("/upload/file.bin"))
	}

	clone := v.Clone()
	if clone.GetPath() != filepath.FromSlash("/") { // Clones start at root by default in NewNullVFS
		t.Errorf("Clone path mismatch: %s", clone.GetPath())
	}

	if v.ParentVFS() != nil {
		t.Error("NullVFS should not have a parent")
	}
}

func TestNullVFS_NativeSlashes(t *testing.T) {
	v := NewNullVFS(0)
	if err := v.SetPath("/test/path"); err != nil {
		t.Fatalf("SetPath(/test/path) failed: %v", err)
	}

	path := v.GetPath()
	expected := filepath.FromSlash("/test/path")

	if path != expected {
		t.Errorf("NullVFS GetPath failed native slashes check. Got %q, expected %q", path, expected)
	}
}
func TestNullVFS_Scenarios(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	t.Run("IOPS Scenario", func(t *testing.T) {
		var items []VFSItem
		if err := v.ReadDir(ctx, "/scenarios/iops", func(chunk []VFSItem) {
			items = append(items, chunk...)
		}); err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		if len(items) != 10000 {
			t.Errorf("Expected 10000 files in IOPS scenario, got %d", len(items))
		}
	})

	t.Run("Deep Scenario", func(t *testing.T) {
		stat, err := v.Stat(ctx, "/scenarios/deep/next_level/next_level/file_0.txt")
		if err != nil || stat.Size != 512 {
			t.Errorf("Deep path resolution failed: %v, size: %d", err, stat.Size)
		}
	})

	t.Run("Speed Zoning", func(t *testing.T) {
		fSlow, _ := v.Open(ctx, "/scenarios/slow/test.bin")
		if fSlow.(*nullReader).speed != 128*1024 {
			t.Error("Slow zone speed not applied")
		}

		fFast, _ := v.Open(ctx, "/scenarios/fast/test.bin")
		if fFast.(*nullReader).speed != 500*1024*1024 {
			t.Error("Fast zone speed not applied")
		}
	})
}
func TestNullVFS_MetadataLatency(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	// 1. Stat in normal zone should be fast
	start := time.Now()
	_, _ = v.Stat(ctx, "/1MB.bin")
	if time.Since(start) > 20*time.Millisecond {
		t.Errorf("Stat in root took too long: %v", time.Since(start))
	}

	// 2. Stat in slow zone should be slow (~100ms)
	start = time.Now()
	_, _ = v.Stat(ctx, "/scenarios/slow/test.bin")
	dur := time.Since(start)
	if dur < 90*time.Millisecond {
		t.Errorf("Stat in slow zone was too fast: %v", dur)
	}
}

func TestNullVFS_MetadataMutationLatency(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	// 1. MkDir in slow zone
	start := time.Now()
	_ = v.MkDir(ctx, "/scenarios/slow/newdir")
	if time.Since(start) < 90*time.Millisecond {
		t.Errorf("MkDir in slow zone was too fast: %v", time.Since(start))
	}

	// 2. Remove in slow zone
	start = time.Now()
	_ = v.Remove(ctx, "/scenarios/slow/file.txt")
	if time.Since(start) < 90*time.Millisecond {
		t.Errorf("Remove in slow zone was too fast: %v", time.Since(start))
	}

	// 3. Rename (Double throttle if both paths are in slow zone)
	start = time.Now()
	_ = v.Rename(ctx, "/scenarios/slow/old", "/scenarios/slow/new")
	dur := time.Since(start)
	// Rename calls throttleMeta twice (for old and new path)
	if dur < 180*time.Millisecond {
		t.Errorf("Rename in slow zone should take ~200ms, got %v", dur)
	}
}

func TestNullVFS_MetadataCancellation(t *testing.T) {
	v := NewNullVFS(0)

	// A context that is already cancelled must not wait out the simulated
	// latency at all. This does not depend on goroutine scheduling, so it
	// is the check that proves the throttle selects on ctx.Done().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_ = v.MkDir(ctx, "/scenarios/slow/cancelled_op")
	if dur := time.Since(start); dur >= 80*time.Millisecond {
		t.Errorf("Metadata operation ignored an already cancelled context, took %v", dur)
	}

	// Cancellation while the simulated latency is active. The wait must end
	// promptly after the cancel, measured from when the cancel actually
	// happened: on a loaded CI runner the cancelling goroutine can be
	// scheduled tens of milliseconds late, and measuring from the start of
	// the operation turned that into a false failure (darwin/amd64 CI took
	// 126ms end to end with a cancel that simply came late).
	ctx, cancel = context.WithCancel(context.Background())
	var cancelledAt atomic.Int64
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancelledAt.Store(time.Now().UnixNano())
		cancel()
	}()
	_ = v.MkDir(ctx, "/scenarios/slow/cancelled_op")
	end := time.Now()
	if at := cancelledAt.Load(); at != 0 {
		if since := end.Sub(time.Unix(0, at)); since >= 80*time.Millisecond {
			t.Errorf("Metadata operation did not respect context cancellation, returned %v after cancel", since)
		}
	}
	cancel()
}
func TestNullVFS_OpenCreateMetadataLatency(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	t.Run("Open in slow zone", func(t *testing.T) {
		start := time.Now()
		f, err := v.Open(ctx, "/scenarios/slow/test.bin")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()

		dur := time.Since(start)
		if dur < 90*time.Millisecond {
			t.Errorf("Open in slow zone was too fast: %v", dur)
		}
	})

	t.Run("Create in slow zone", func(t *testing.T) {
		start := time.Now()
		// We use /upload prefix because root files are protected in NullVFS
		w, err := v.Create(ctx, "/upload/scenarios/slow/newfile.bin")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := w.Close(); err != nil {
				t.Errorf("close writer: %v", err)
			}
		})

		dur := time.Since(start)
		if dur < 90*time.Millisecond {
			t.Errorf("Create in slow zone was too fast: %v", dur)
		}
	})
}
func TestNullVFS_ReadDirPaging(t *testing.T) {
	v := NewNullVFS(0)
	ctx := context.Background()

	t.Run("Paging in IOPS scenario", func(t *testing.T) {
		chunkCount := 0
		itemCount := 0
		err := v.ReadDir(ctx, "/scenarios/iops", func(items []VFSItem) {
			chunkCount++
			itemCount += len(items)
		})
		if err != nil {
			t.Fatal(err)
		}

		// 1000 items in chunks of 100 = 10 chunks
		if itemCount != 10000 {
			t.Errorf("Expected 10000 items, got %d", itemCount)
		}
		if chunkCount != 100 {
			t.Errorf("Expected 100 chunks, got %d", chunkCount)
		}
	})

	t.Run("Paging delay in slow zone", func(t *testing.T) {
		start := time.Now()
		chunkCount := 0
		itemCount := 0
		_ = v.ReadDir(ctx, "/scenarios/iops/slow", func(items []VFSItem) {
			chunkCount++
			itemCount += len(items)
		})
		dur := time.Since(start)

		if itemCount != 10000 {
			t.Errorf("Expected 10000 items, got %d", itemCount)
		}

		// Initial meta throttle (100ms) + 9 inter-chunk delays (9 * 20ms = 180ms)
		// Total should be ~280ms. We use 250ms as a safe bound for CI.
		if dur < 250*time.Millisecond {
			t.Errorf("ReadDir paging in slow zone was too fast: %v (expected ~280ms)", dur)
		}
	})
}
