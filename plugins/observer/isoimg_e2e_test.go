package observer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/plugins/observer"
)

// isoimgWasmPath and isoimgIsoPath are built by scripts/build_isoimg_test_
// wasm.sh (wasi-sdk, fetching github.com/lazyhamster/Observer's isoimg
// module at a pinned commit) and scripts/build_isoimg_test_iso.sh
// (genisoimage) in CI before `go test` runs; see .github/workflows/quick.yml
// and plugins/observer/testdata/isoimg/compat/. Neither is checked into the
// repository (see .gitignore), so a local `go test` without those steps
// skips this file's test instead of failing.
const (
	isoimgWasmPath = "testdata/isoimg_test.wasm"
	isoimgIsoPath  = "testdata/isoimg_test.iso"
)

// helloTxtContent must match scripts/build_isoimg_test_iso.sh's own HELLO.TXT
// content exactly.
const helloTxtContent = "hello from the f4#1563 isoimg end-to-end test\n"

func loadOptionalFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("%s is missing; build it with scripts/build_isoimg_test_wasm.sh and scripts/build_isoimg_test_iso.sh before running this test -- CI does this in quick.yml/build.yml", path)
		}
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

// TestIsoimgOpenStorageEndToEnd is f4#1563's first real, non-stub end-to-end
// run: it drives an actual, unmodified upstream Observer module (isoimg,
// compiled by scripts/build_isoimg_test_wasm.sh against f4's own compat
// shim, see plugins/observer/testdata/isoimg/compat/) through
// plugins/observer's real wazero-based runtime.go, against a small, plain
// ISO9660 image built by genisoimage -- not the from-scratch
// testdata/stub/observer_stub.c fixture TestLoadSubModule and friends use to
// exercise the ABI in isolation.
//
// It proves LoadSubModule, OpenStorage and (f4#1563 part 3) GetItem all work
// against real isoimg code: struct marshaling into wasm32 linear memory, the
// WASI filesystem mount serving the probed file's actual bytes to isoimg's
// own CreateFile/ReadFile/SetFilePointer(Ex) calls (via
// plugins/observer/testdata/isoimg/compat/windows.h), and the
// f4observer_open_storage/f4observer_close_storage/f4observer_get_item
// trampolines (compat/trampolines.cpp) forwarding to isoimg.cpp's real
// OpenStorage/CloseStorage/GetStorageItem -- the same functions a real
// Windows Observer host would reach through module_cbs (see ../../doc.go's
// "module_cbs indirection").
//
// ExtractItem/PrepareFiles are not driven here: they remain reserved,
// exactly as doc.go describes.
func TestIsoimgOpenStorageEndToEnd(t *testing.T) {
	wasmBytes := loadOptionalFixture(t, isoimgWasmPath)
	isoBytes := loadOptionalFixture(t, isoimgIsoPath)

	ctx := context.Background()
	ra := newMemReaderAt(isoBytes)
	defer func() { _ = ra.Close() }() // SingleFileFS never closes it; see fsbridge.go.
	mount := observer.NewSingleFileFS(ctx, "target.iso", ra)

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{FilePath: "/target.iso"})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORSuccess {
		t.Fatalf("Code = %d, want SORSuccess (%d): %s did not recognize the genisoimage-built test ISO", res.Code, observer.SORSuccess, isoimgWasmPath)
	}
	if res.Storage == 0 {
		t.Error("Storage handle is 0, want a nonzero opaque handle")
	}
	if res.Info.Format != "ISO" {
		t.Errorf("Info.Format = %q, want %q", res.Info.Format, "ISO")
	}
	// isoimg trims trailing spaces off the 32-byte, space-padded
	// VolumeIdentifier field (see iso_ext.h's TrimRight, used from
	// isoimg.cpp's OpenStorage) before widening it into Info.Comment; the
	// volume label itself came from build_isoimg_test_iso.sh's -V flag.
	if got := strings.TrimRight(res.Info.Comment, " "); got != "F4TESTVOL" {
		t.Errorf("Info.Comment = %q, want %q", got, "F4TESTVOL")
	}

	// f4#1563 part 3: walk the real directory tree isoimg built for this
	// storage handle via GetItem, proving GetItem's StorageItemInfo
	// marshaling against a real module too, not just testdata/stub's fixed
	// fake item (see TestGetItem in observer_test.go). genisoimage's plain
	// ISO9660 output is expected to include HELLO.TXT under some 8.3-style
	// name (possibly with a trailing ";1" ISO9660 version suffix, which
	// isoimg's non-Joliet path does not strip); do not depend on the exact
	// spelling or on "." /".." being present or absent.
	helloIndex, err := findHelloItem(mod, res.Storage)
	if err != nil {
		t.Fatal(err)
	}
	item, err := mod.GetItem(res.Storage, helloIndex)
	if err != nil {
		t.Fatalf("GetItem(%d): %v", helloIndex, err)
	}
	if item.Info.Size != int64(len(helloTxtContent)) {
		t.Errorf("GetItem(%d): Info.Size = %d for %q, want %d", helloIndex, item.Info.Size, item.Info.Path, len(helloTxtContent))
	}

	if err := mod.CloseStorage(res.Storage); err != nil {
		t.Fatalf("CloseStorage: %v", err)
	}
}

// findHelloItem walks storage's directory tree via GetItem looking for the
// entry containing "HELLO.TXT" (see TestIsoimgOpenStorageEndToEnd's own
// comment on why a substring match, not an exact one). Shared by every
// isoimg end-to-end test that needs HELLO.TXT's item index.
func findHelloItem(mod *observer.Module, storage uint32) (int32, error) {
	for index := int32(0); index < 64; index++ {
		item, err := mod.GetItem(storage, index)
		if err != nil {
			return 0, fmt.Errorf("GetItem(%d): %w", index, err)
		}
		if item.Code == observer.GetItemNoMoreItems {
			break
		}
		if item.Code != observer.GetItemOK {
			return 0, fmt.Errorf("GetItem(%d): Code = %d, want GetItemOK or GetItemNoMoreItems", index, item.Code)
		}
		if strings.Contains(strings.ToUpper(item.Info.Path), "HELLO.TXT") {
			return index, nil
		}
	}
	return 0, errors.New("no directory entry containing HELLO.TXT found via GetItem")
}

// TestIsoimgExtractItemEndToEnd is f4#1563 part 4's end-to-end run: it
// extracts HELLO.TXT out of the real genisoimage-built test ISO through
// isoimg's real ExtractItem (compat/trampolines.cpp's
// f4observer_extract_item, forwarding to isoimg.cpp's own ExtractItem/
// ExtractFile), into a real, writable host directory mounted at
// observer.ExtractGuestDir via observer.WithExtractDir, and checks the
// extracted file's content byte-for-byte -- not just that ExtractItem
// returned success.
//
// It also proves the ExportProgressTrampoline mechanism (abi.go, doc.go)
// works end-to-end: isoimg.cpp's ExtractFile calls
// params.Callbacks.FileProgress once per block it writes
// (iso_ext.cpp), a real function pointer LoadModule resolved from the
// module's own f4observer_progress_trampoline export, and this test checks
// the resulting observer.progress calls actually reached the host's
// ProgressFunc.
func TestIsoimgExtractItemEndToEnd(t *testing.T) {
	wasmBytes := loadOptionalFixture(t, isoimgWasmPath)
	isoBytes := loadOptionalFixture(t, isoimgIsoPath)

	ctx := context.Background()
	ra := newMemReaderAt(isoBytes)
	defer func() { _ = ra.Close() }()
	mount := observer.NewSingleFileFS(ctx, "target.iso", ra)

	extractDir := t.TempDir()

	var progressCalls int
	progress := func(_ uint32, bytesDone int64) int32 {
		progressCalls++
		if bytesDone <= 0 {
			t.Errorf("progress bytesDone = %d, want > 0", bytesDone)
		}
		return 1
	}

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, progress, observer.WithExtractDir(extractDir))
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{FilePath: "/target.iso"})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORSuccess {
		t.Fatalf("Code = %d, want SORSuccess (%d)", res.Code, observer.SORSuccess)
	}

	helloIndex, err := findHelloItem(mod, res.Storage)
	if err != nil {
		t.Fatal(err)
	}

	code, err := mod.ExtractItem(res.Storage, observer.ExtractItemParams{
		ItemIndex: helloIndex,
		DestName:  "HELLO.TXT",
	})
	if err != nil {
		t.Fatalf("ExtractItem: %v", err)
	}
	if code != observer.SERSuccess {
		t.Fatalf("ExtractItem: code = %d, want SERSuccess (%d)", code, observer.SERSuccess)
	}

	got, err := os.ReadFile(filepath.Join(extractDir, "HELLO.TXT"))
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(got) != helloTxtContent {
		t.Errorf("extracted content = %q, want %q", got, helloTxtContent)
	}

	if progressCalls == 0 {
		t.Error("progress was never called during ExtractItem, want at least one call")
	}

	if err := mod.CloseStorage(res.Storage); err != nil {
		t.Fatalf("CloseStorage: %v", err)
	}
}

// TestIsoimgOpenStorageRejectsGarbage confirms isoimg's real GetImage still
// rejects a file that is not an ISO image at all -- SOR_INVALID_FILE, not a
// trap -- through the same trampoline path TestIsoimgOpenStorageEndToEnd
// exercises for the success case.
func TestIsoimgOpenStorageRejectsGarbage(t *testing.T) {
	wasmBytes := loadOptionalFixture(t, isoimgWasmPath)

	ctx := context.Background()
	content := make([]byte, 128*1024) // GetImage scans up to 1 MiB before giving up; keep this test fast.
	ra := newMemReaderAt(content)
	mount := observer.NewSingleFileFS(ctx, "target.iso", ra)

	mod, err := observer.LoadModule(ctx, wasmBytes, mount, nil)
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	defer func() { _ = mod.Close() }()

	if _, err := mod.LoadSubModule(""); err != nil {
		t.Fatalf("LoadSubModule: %v", err)
	}

	res, err := mod.OpenStorage(observer.StorageOpenParams{FilePath: "/target.iso"})
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if res.Code != observer.SORInvalidFile {
		t.Fatalf("Code = %d, want SORInvalidFile (%d)", res.Code, observer.SORInvalidFile)
	}
}
