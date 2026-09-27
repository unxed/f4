package observer_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/unxed/f4/plugins/observer"
)

// fixturePath is built by scripts/build_observer_test_wasm.sh (wasi-sdk) in
// CI before `go test` runs; see .github/workflows/quick.yml and
// testdata/stub/observer_stub.c. It is never checked into the repository
// (see .gitignore), so a local `go test` without that step skips instead of
// failing.
const fixturePath = "testdata/observer_stub.wasm"

func loadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("%s is missing; build it with scripts/build_observer_test_wasm.sh (requires wasi-sdk) before running this test -- CI does this in quick.yml/build.yml", fixturePath)
		}
		t.Fatalf("reading %s: %v", fixturePath, err)
	}
	return b
}

// memReaderAt is an in-memory observer.ReaderAt, standing in for a real
// vfs.ReadAtCloser so this test does not need a file on disk (the point of
// fsbridge.go is that it never needs one either).
type memReaderAt struct {
	data   []byte
	pos    int64
	closed bool
}

func newMemReaderAt(data []byte) *memReaderAt {
	return &memReaderAt{data: data}
}

func (m *memReaderAt) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (m *memReaderAt) Read(ctx context.Context, p []byte) (int, error) {
	n, err := m.ReadAt(ctx, p, m.pos)
	m.pos += int64(n)
	return n, err
}

func (m *memReaderAt) Close() error {
	m.closed = true
	return nil
}

func (m *memReaderAt) Size() int64 { return int64(len(m.data)) }

func TestLoadSubModule(t *testing.T) {
	wasmBytes := loadFixture(t)
	ctx := context.Background()

	mod, err := observer.LoadModule(ctx, wasmBytes, nil, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	info, err := mod.LoadSubModule("")
	if err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	if info.ApiVersion != observer.ActualAPIVersion {
		t.Errorf("ApiVersion = %d, want %d", info.ApiVersion, observer.ActualAPIVersion)
	}
	if want := observer.MakeModuleVersion(1, 0); info.ModuleVersion != want {
		t.Errorf("ModuleVersion = 0x%x, want 0x%x", info.ModuleVersion, want)
	}

	wantGUID := observer.GUID{
		Data1: 0xF4013701,
		Data2: 0x5453,
		Data3: 0x4255,
		Data4: [8]byte{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6, 0xA7},
	}
	if info.ModuleId != wantGUID {
		t.Errorf("ModuleId = %+v, want %+v", info.ModuleId, wantGUID)
	}

	wantCbs := observer.ModuleCbs{
		OpenStorage:  0xAAAA0001,
		CloseStorage: 0xAAAA0002,
		GetItem:      0xAAAA0003,
		ExtractItem:  0xAAAA0004,
		PrepareFiles: 0xAAAA0005,
	}
	if info.ApiFuncs != wantCbs {
		t.Errorf("ApiFuncs = %+v, want %+v", info.ApiFuncs, wantCbs)
	}
}

func TestOpenStorageRecognizedFile(t *testing.T) {
	wasmBytes := loadFixture(t)
	ctx := context.Background()

	magic := []byte("F4OBSV01")
	content := append(append([]byte{}, magic...), []byte(" payload recognized by the test stub")...)
	ra := newMemReaderAt(content)
	defer func() { _ = ra.Close() }() // SingleFileFS never closes it; see fsbridge.go.
	mount := observer.NewSingleFileFS(ctx, "target.bin", ra)

	var progressCalls int
	var gotSignalContext uint32
	var gotBytesDone int64
	progress := func(signalContext uint32, bytesDone int64) int32 {
		progressCalls++
		gotSignalContext = signalContext
		gotBytesDone = bytesDone
		return 1
	}

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, progress)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	data := []byte{0xAB, 0xCD, 0xEF, 0x01}
	res, err := mod.OpenStorage(observer.StorageOpenParams{
		FilePath: "/target.bin",
		Data:     data,
	})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORSuccess {
		t.Fatalf("Code = %d, want SORSuccess (%d)", res.Code, observer.SORSuccess)
	}
	if res.Storage == 0 {
		t.Error("Storage handle is 0, want a nonzero opaque handle")
	}
	if res.Info.Format != "F4OBSTUB" {
		t.Errorf("Info.Format = %q, want %q", res.Info.Format, "F4OBSTUB")
	}
	if res.Info.Compression != "store" {
		t.Errorf("Info.Compression = %q, want %q", res.Info.Compression, "store")
	}

	if progressCalls != 1 {
		t.Fatalf("progress called %d times, want 1", progressCalls)
	}
	if gotSignalContext != 0 {
		t.Errorf("progress signalContext = %d, want 0", gotSignalContext)
	}
	if gotBytesDone != int64(len(data)) {
		t.Errorf("progress bytesDone = %d, want %d", gotBytesDone, len(data))
	}

	// These accessors round-trip through the module's own state, confirming
	// StorageOpenParams.Data/DataSize marshaled correctly.
	if n, err := mod.CallNoArgInt32("f4observer_last_data_size"); err != nil {
		t.Errorf("f4observer_last_data_size: %v", err)
	} else if n != int32(len(data)) { // #nosec G115 -- data is the fixed 4-byte literal above, well inside int32.
		t.Errorf("last_data_size = %d, want %d", n, len(data))
	}
	if n, err := mod.CallNoArgInt32("f4observer_last_data_first_byte"); err != nil {
		t.Errorf("f4observer_last_data_first_byte: %v", err)
	} else if n != int32(data[0]) {
		t.Errorf("last_data_first_byte = %d, want %d", n, data[0])
	}

	if err := mod.CloseStorage(res.Storage); err != nil {
		t.Fatalf("CloseStorage: %v", err)
	}
	if n, err := mod.CallNoArgInt32("f4observer_close_count"); err != nil {
		t.Errorf("f4observer_close_count: %v", err)
	} else if n != 1 {
		t.Errorf("close_count = %d, want 1", n)
	}

}

func TestOpenStorageUnrecognizedFile(t *testing.T) {
	wasmBytes := loadFixture(t)
	ctx := context.Background()

	content := []byte("this content does not start with the stub's magic at all")
	ra := newMemReaderAt(content)
	mount := observer.NewSingleFileFS(ctx, "target.bin", ra)

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{
		FilePath: "/target.bin",
		// Deliberately claims the magic here: the stub must decide from the
		// actual file content it reads through the WASI mount, not from
		// this inline head, so a mismatch between the two proves the mount
		// -- not just the struct fields -- works.
		Data: []byte("F4OBSV01"),
	})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORInvalidFile {
		t.Fatalf("Code = %d, want SORInvalidFile (%d)", res.Code, observer.SORInvalidFile)
	}
	if res.Storage != 0 {
		t.Errorf("Storage = %d, want 0", res.Storage)
	}
}

func TestOpenStorageWithoutMount(t *testing.T) {
	// A Module loaded with no mount at all (nil) can still load, matching
	// transport_wazero.go's WasmPlugin default of giving a guest no
	// filesystem until it is explicitly granted one.
	wasmBytes := loadFixture(t)
	ctx := context.Background()

	mod, err := observer.LoadModule(ctx, wasmBytes, nil, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer mod.Close()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{
		FilePath: "/target.bin",
		Data:     []byte("F4OBSV01"),
	})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORInvalidFile {
		t.Fatalf("Code = %d, want SORInvalidFile (%d): open() on an unmounted guest must fail, not fall back to Data", res.Code, observer.SORInvalidFile)
	}
}

// TestGetItem exercises Module.GetItem (f4#1563, part 3) against
// testdata/stub's f4observer_get_item, proving StorageItemInfo's marshaling
// (including its two wchar_t[] fields and the FILETIME-shaped Creation/
// ModificationTime pairs) round-trips correctly, independent of the isoimg
// end-to-end test in isoimg_e2e_test.go.
func TestGetItem(t *testing.T) {
	wasmBytes := loadFixture(t)
	ctx := context.Background()

	magic := []byte("F4OBSV01")
	ra := newMemReaderAt(magic)
	defer func() { _ = ra.Close() }()
	mount := observer.NewSingleFileFS(ctx, "target.bin", ra)

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{FilePath: "/target.bin"})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORSuccess {
		t.Fatalf("Code = %d, want SORSuccess (%d)", res.Code, observer.SORSuccess)
	}

	item, err := mod.GetItem(res.Storage, 0)
	if err != nil {
		t.Fatalf("GetItem(0): %v", err)
	}
	if item.Code != observer.GetItemOK {
		t.Fatalf("Code = %d, want GetItemOK (%d)", item.Code, observer.GetItemOK)
	}
	if item.Info.Path != "stub-item.txt" {
		t.Errorf("Info.Path = %q, want %q", item.Info.Path, "stub-item.txt")
	}
	if item.Info.Size != 42 {
		t.Errorf("Info.Size = %d, want 42", item.Info.Size)
	}
	if item.Info.PackedSize != 42 {
		t.Errorf("Info.PackedSize = %d, want 42", item.Info.PackedSize)
	}
	if item.Info.NumHardlinks != 1 {
		t.Errorf("Info.NumHardlinks = %d, want 1", item.Info.NumHardlinks)
	}
	if want := (observer.FileTime{Low: 0x11111111, High: 0x22222222}); item.Info.CreationTime != want {
		t.Errorf("Info.CreationTime = %+v, want %+v", item.Info.CreationTime, want)
	}
	if want := (observer.FileTime{Low: 0x33333333, High: 0x44444444}); item.Info.ModificationTime != want {
		t.Errorf("Info.ModificationTime = %+v, want %+v", item.Info.ModificationTime, want)
	}

	if next, err := mod.GetItem(res.Storage, 1); err != nil {
		t.Fatalf("GetItem(1): %v", err)
	} else if next.Code != observer.GetItemNoMoreItems {
		t.Errorf("Code = %d, want GetItemNoMoreItems (%d)", next.Code, observer.GetItemNoMoreItems)
	}

	if err := mod.CloseStorage(res.Storage); err != nil {
		t.Fatalf("CloseStorage: %v", err)
	}
}
