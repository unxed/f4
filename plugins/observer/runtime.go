package observer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// defaultMemoryLimitPages caps a module instance at 16 MiB (a wasm page is
// 64 KiB), the same WithMemoryLimitPages guard transport_wazero.go's plugin
// host and colorer4go both rely on to keep a runaway or hostile module from
// growing without bound. It is a first, conservative number for this part's
// tiny test module; real modules will need this tuned per module.
const defaultMemoryLimitPages = 256

var (
	cacheOnce        sync.Once
	compilationCache wazero.CompilationCache
)

// sharedCompilationCache returns a process-wide cache of compiled wasm code,
// the same pattern colorer4go's sharedCompilationCache uses: compiling a
// module is the most expensive part of loading it, so the machine code is
// kept on disk and reused across later loads and later runs of the process.
func sharedCompilationCache() wazero.CompilationCache {
	cacheOnce.Do(func() {
		if dir, err := os.UserCacheDir(); err == nil {
			if cache, cErr := wazero.NewCompilationCacheWithDir(filepath.Join(dir, "f4-observer")); cErr == nil {
				compilationCache = cache
				return
			}
		}
		compilationCache = wazero.NewCompilationCache()
	})
	return compilationCache
}

// compileMu serializes compilation against the shared cache. Every runtime
// created with sharedCompilationCache shares one wazero engine and its table
// of compiled modules; colorer4go's compileColorer documents a wazero
// v1.12.0 race (a module read from the file cache is visible to a second
// compiler before its entry preambles are ready) that this avoids the same
// way: compiling one module at a time.
var compileMu sync.Mutex

// Module runs one instance of an Observer module compiled to a WASI
// reactor. Instances are not safe for concurrent use -- like a real
// Observer module instance is not -- and are scoped to at most one open
// storage at a time, matching the plan in f4#1563 to give every opened
// storage its own module instance.
type Module struct {
	ctx    context.Context
	cancel context.CancelFunc

	runtime wazero.Runtime
	mod     api.Module
	host    *hostState

	loadFn   api.Function
	unloadFn api.Function
	openFn   api.Function
	closeFn  api.Function
	mallocFn api.Function
	freeFn   api.Function

	// getItemFn is deliberately not in LoadModule's required map: a module
	// this package can drive for LoadSubModule/OpenStorage alone (as parts
	// 1-2 did) need not implement every trampoline from day one. It is
	// resolved opportunistically instead, and GetItem reports a clear error
	// if a module lacks it.
	getItemFn api.Function

	fatal *FatalError
}

// LoadModule compiles wasmBytes and instantiates it as a WASI reactor.
//
// mount, when non-nil, is attached to the guest's root ("/"); pass a
// SingleFileFS to give the module read access to exactly the file it is
// being probed against, backed by that file's parent VFS rather than a real
// host path. A nil mount gives the guest no filesystem access at all, which
// is enough to exercise LoadSubModule alone.
//
// progress is called for every observer.progress import the module makes;
// see ProgressFunc.
//
// ctx governs the lifetime of the whole instance: canceling it interrupts
// any call in progress (wazero.RuntimeConfig.WithCloseOnContextDone) and
// Close cancels it if the caller has not already done so.
func LoadModule(ctx context.Context, wasmBytes []byte, mount fs.FS, progress ProgressFunc) (*Module, error) {
	runCtx, cancel := context.WithCancel(ctx)

	m := &Module{
		ctx:    runCtx,
		cancel: cancel,
		host:   &hostState{progress: progress},
	}

	m.runtime = wazero.NewRuntimeWithConfig(runCtx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(defaultMemoryLimitPages).
		WithCompilationCache(sharedCompilationCache()))

	if _, err := wasi_snapshot_preview1.Instantiate(runCtx, m.runtime); err != nil {
		m.shutdown()
		return nil, fmt.Errorf("observer: instantiating wasi_snapshot_preview1: %w", err)
	}

	if err := newObserverHostModule(runCtx, m.runtime, m.host); err != nil {
		m.shutdown()
		return nil, fmt.Errorf("observer: instantiating the observer host module: %w", err)
	}

	compileMu.Lock()
	compiled, err := m.runtime.CompileModule(runCtx, wasmBytes)
	compileMu.Unlock()
	if err != nil {
		m.shutdown()
		return nil, fmt.Errorf("observer: compiling module: %w", err)
	}

	config := wazero.NewModuleConfig().
		WithStdout(io.Discard).
		WithStderr(&stderrWriter{host: m.host})
	if mount != nil {
		config = config.WithFSConfig(wazero.NewFSConfig().WithFSMount(mount, ""))
	}

	mod, err := m.runtime.InstantiateModule(runCtx, compiled, config)
	if err != nil {
		m.shutdown()
		return nil, fmt.Errorf("observer: instantiating module: %w", err)
	}
	m.mod = mod

	// Reactor modules run their global constructors in _initialize rather
	// than a _start entrypoint (which reactors do not have); see
	// colorer4go's NewSession for the same call.
	if initFn := mod.ExportedFunction("_initialize"); initFn != nil {
		if _, err := m.call(initFn, "_initialize"); err != nil {
			m.shutdown()
			return nil, err
		}
	}

	required := map[string]*api.Function{
		ExportLoadSubModule:   &m.loadFn,
		ExportUnloadSubModule: &m.unloadFn,
		ExportOpenStorage:     &m.openFn,
		ExportCloseStorage:    &m.closeFn,
		ExportMalloc:          &m.mallocFn,
		ExportFree:            &m.freeFn,
	}
	var missing []string
	for name, slot := range required {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			missing = append(missing, name)
			continue
		}
		*slot = fn
	}
	if len(missing) > 0 {
		m.shutdown()
		return nil, fmt.Errorf("observer: module does not export %v", missing)
	}

	m.getItemFn = mod.ExportedFunction(ExportGetItem)

	return m, nil
}

// call runs one exported function and turns a failed call into a
// *FatalError, the same policy as colorer4go's callFn/Session.call: once a
// call has failed the Module refuses every later one, returning that same
// error.
func (m *Module) call(fn api.Function, op string, params ...uint64) ([]uint64, error) {
	if m.fatal != nil {
		return nil, m.fatal
	}
	m.host.beginCall()
	res, err := fn.Call(m.ctx, params...)
	if err != nil {
		fe := &FatalError{Op: op, Reason: m.host.lastStderr, Err: err}
		m.fatal = fe
		return nil, fe
	}
	return res, nil
}

// Err returns the *FatalError that made the Module unusable, or nil while it
// is still usable.
func (m *Module) Err() error {
	if m.fatal == nil {
		return nil
	}
	return m.fatal
}

// alloc reserves size bytes in the guest's own linear memory via its
// exported malloc, the way colorer4go's NewSession uses colorer_alloc.
func (m *Module) alloc(size uint32) (uint32, error) {
	if size == 0 {
		return 0, nil
	}
	res, err := m.call(m.mallocFn, ExportMalloc, uint64(size))
	if err != nil {
		return 0, err
	}
	// #nosec G115 -- res[0] is malloc's i32 return value, already
	// zero-extended into the uint64 result slot by wazero.
	ptr := uint32(res[0])
	if ptr == 0 {
		return 0, fmt.Errorf("observer: module malloc(%d) returned null", size)
	}
	return ptr, nil
}

// free releases a pointer obtained from alloc. It is best-effort: a Module
// already dead from a prior FatalError has nothing left worth freeing.
func (m *Module) free(ptr uint32) {
	if ptr == 0 || m.fatal != nil {
		return
	}
	_, _ = m.call(m.freeFn, ExportFree, uint64(ptr))
}

func (m *Module) writeMemory(ptr uint32, b []byte) error {
	if !m.mod.Memory().Write(ptr, b) {
		return fmt.Errorf("observer: write of %d bytes at 0x%x is out of bounds", len(b), ptr)
	}
	return nil
}

func (m *Module) readMemory(ptr uint32, size uint32) ([]byte, error) {
	b, ok := m.mod.Memory().Read(ptr, size)
	if !ok {
		return nil, fmt.Errorf("observer: read of %d bytes at 0x%x is out of bounds", size, ptr)
	}
	// Read returns a view over live guest memory; copy it out so it survives
	// the next call, which may reuse or free that same region.
	return append([]byte(nil), b...), nil
}

// allocWChars allocates and writes a wchar_t string, returning 0 (a null
// pointer, matching the ABI's own "not given" convention) for an empty
// string.
func (m *Module) allocWChars(s string) (uint32, error) {
	if s == "" {
		return 0, nil
	}
	b := encodeWChars(s)
	// #nosec G115 -- b is written into the guest's own wasm32 linear memory,
	// whose whole address space is under 2^32 bytes, so its length always
	// fits in uint32.
	ptr, err := m.alloc(uint32(len(b)))
	if err != nil {
		return 0, err
	}
	if err := m.writeMemory(ptr, b); err != nil {
		m.free(ptr)
		return 0, err
	}
	return ptr, nil
}

func (m *Module) allocBytes(b []byte) (uint32, error) {
	if len(b) == 0 {
		return 0, nil
	}
	// #nosec G115 -- b is written into the guest's own wasm32 linear memory,
	// whose whole address space is under 2^32 bytes, so its length always
	// fits in uint32.
	ptr, err := m.alloc(uint32(len(b)))
	if err != nil {
		return 0, err
	}
	if err := m.writeMemory(ptr, b); err != nil {
		m.free(ptr)
		return 0, err
	}
	return ptr, nil
}

// LoadSubModule calls the guest's exported LoadSubModule with a
// ModuleLoadParameters built from settings, and decodes the OUT fields the
// module wrote back: ModuleId, ModuleVersion, ApiVersion and the raw
// module_cbs table (see doc.go for why f4 never calls through the latter).
//
// A zero return from LoadSubModule is treated as the module refusing to
// load; ModuleDef.h does not name this convention explicitly, but every
// other result code in the header uses 0 for failure and a nonzero low bit
// for success (SOR_SUCCESS, GET_ITEM_OK), and LoadSubModuleFunc's own
// signature (returning int, taking only an OUT struct) has no other way to
// fail than through its return value.
func (m *Module) LoadSubModule(settings string) (ModuleInfo, error) {
	settingsPtr, err := m.allocWChars(settings)
	if err != nil {
		return ModuleInfo{}, err
	}
	defer m.free(settingsPtr)

	paramsPtr, err := m.alloc(moduleLoadParametersSize)
	if err != nil {
		return ModuleInfo{}, err
	}
	defer m.free(paramsPtr)

	buf := make([]byte, moduleLoadParametersSize)
	encodeModuleLoadParameters(buf, settingsPtr)
	if err := m.writeMemory(paramsPtr, buf); err != nil {
		return ModuleInfo{}, err
	}

	res, err := m.call(m.loadFn, ExportLoadSubModule, uint64(paramsPtr))
	if err != nil {
		return ModuleInfo{}, err
	}
	// #nosec G115 -- res[0] is LoadSubModule's i32 BOOL return value, already
	// zero-extended into the uint64 result slot by wazero; the inner
	// uint32(...) narrowing is exact, and the outer int32(...) reinterprets
	// its bits as the signed BOOL the ABI specifies.
	if int32(uint32(res[0])) == 0 {
		return ModuleInfo{}, errors.New("observer: LoadSubModule refused to load")
	}

	out, err := m.readMemory(paramsPtr, moduleLoadParametersSize)
	if err != nil {
		return ModuleInfo{}, err
	}
	return decodeModuleLoadParameters(out), nil
}

// OpenResult is the decoded return of the module's f4observer_open_storage
// trampoline: the SOR_* code, and, only when Code == SORSuccess, the opaque
// storage handle and the StorageGeneralInfo the module filled in.
type OpenResult struct {
	Code    int32
	Storage uint32
	Info    StorageGeneralInfo
}

// OpenStorage calls the guest's f4observer_open_storage trampoline (see
// doc.go) with a StorageOpenParams built from params. FilePath, if set, must
// be a guest-visible path -- the name the Module's mount (see LoadModule)
// exposes the probed file at, prefixed with "/" -- not a host path.
func (m *Module) OpenStorage(params StorageOpenParams) (OpenResult, error) {
	filePathPtr, err := m.allocWChars(params.FilePath)
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(filePathPtr)

	passwordPtr, err := m.allocBytes(nullTerminate(params.Password))
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(passwordPtr)

	dataPtr, err := m.allocBytes(params.Data)
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(dataPtr)

	paramsPtr, err := m.alloc(storageOpenParamsSize)
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(paramsPtr)

	buf := make([]byte, storageOpenParamsSize)
	// #nosec G115 -- params.Data was just allocated into the guest's own
	// wasm32 linear memory above, whose whole address space is under 2^32
	// bytes, so its length always fits in uint32.
	encodeStorageOpenParams(buf, filePathPtr, passwordPtr, dataPtr, uint32(len(params.Data)))
	if err := m.writeMemory(paramsPtr, buf); err != nil {
		return OpenResult{}, err
	}

	storageOutPtr, err := m.alloc(4)
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(storageOutPtr)

	infoOutPtr, err := m.alloc(storageGeneralInfoSize)
	if err != nil {
		return OpenResult{}, err
	}
	defer m.free(infoOutPtr)

	res, err := m.call(m.openFn, ExportOpenStorage, uint64(paramsPtr), uint64(storageOutPtr), uint64(infoOutPtr))
	if err != nil {
		return OpenResult{}, err
	}

	result := OpenResult{Code: int32(uint32(res[0]))}
	if result.Code != SORSuccess {
		return result, nil
	}

	handleBytes, err := m.readMemory(storageOutPtr, 4)
	if err != nil {
		return OpenResult{}, err
	}
	result.Storage = binary.LittleEndian.Uint32(handleBytes)

	infoBytes, err := m.readMemory(infoOutPtr, storageGeneralInfoSize)
	if err != nil {
		return OpenResult{}, err
	}
	result.Info = decodeStorageGeneralInfo(infoBytes)

	return result, nil
}

// GetItemResult is the decoded return of the module's f4observer_get_item
// trampoline: the GET_ITEM_* code, and, only when Code == GetItemOK, the
// StorageItemInfo the module filled in.
type GetItemResult struct {
	Code int32
	Info StorageItemInfo
}

// GetItem calls the guest's f4observer_get_item trampoline (see doc.go) for
// itemIndex within a storage handle returned by a prior successful
// OpenStorage. itemIndex walks 0, 1, 2, ... until Code is GetItemNoMoreItems
// (ModuleDef.h names no other way to learn how many items a storage has).
//
// GetItem returns an error, not a GetItemResult with Code ==
// observer.GetItemError, if the module has no f4observer_get_item trampoline
// at all -- a module this package can otherwise drive is not required to
// implement every trampoline (see the getItemFn field's doc comment).
func (m *Module) GetItem(storage uint32, itemIndex int32) (GetItemResult, error) {
	if m.getItemFn == nil {
		return GetItemResult{}, fmt.Errorf("observer: module does not export %s", ExportGetItem)
	}

	infoOutPtr, err := m.alloc(storageItemInfoSize)
	if err != nil {
		return GetItemResult{}, err
	}
	defer m.free(infoOutPtr)

	// #nosec G115 -- itemIndex is the ABI's own signed int item_index,
	// reinterpreted bit-for-bit into the uint64 call slot, not narrowed.
	res, err := m.call(m.getItemFn, ExportGetItem, uint64(storage), uint64(uint32(itemIndex)), uint64(infoOutPtr))
	if err != nil {
		return GetItemResult{}, err
	}

	result := GetItemResult{Code: int32(uint32(res[0]))}
	if result.Code != GetItemOK {
		return result, nil
	}

	infoBytes, err := m.readMemory(infoOutPtr, storageItemInfoSize)
	if err != nil {
		return GetItemResult{}, err
	}
	result.Info = decodeStorageItemInfo(infoBytes)

	return result, nil
}

// CallNoArgInt32 calls a guest export that takes no arguments and returns a
// single i32. It is not part of the Observer ABI itself: it exists so tests
// and diagnostics can read a module's own introspection accessors, such as
// testdata/stub's f4observer_last_data_size and f4observer_close_count.
func (m *Module) CallNoArgInt32(name string) (int32, error) {
	fn := m.mod.ExportedFunction(name)
	if fn == nil {
		return 0, fmt.Errorf("observer: %s is not exported", name)
	}
	res, err := m.call(fn, name)
	if err != nil {
		return 0, err
	}
	return int32(uint32(res[0])), nil
}

// CloseStorage calls the guest's f4observer_close_storage trampoline with a
// handle returned by a prior successful OpenStorage.
func (m *Module) CloseStorage(storage uint32) error {
	_, err := m.call(m.closeFn, ExportCloseStorage, uint64(storage))
	return err
}

// Close calls the guest's UnloadSubModule (best-effort, skipped once the
// Module is already dead) and tears down the wazero runtime.
func (m *Module) Close() error {
	if m.unloadFn != nil && m.fatal == nil {
		_, _ = m.call(m.unloadFn, ExportUnloadSubModule)
	}
	m.shutdown()
	return nil
}

func (m *Module) shutdown() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.runtime != nil {
		_ = m.runtime.Close(context.Background())
		m.runtime = nil
	}
}

func nullTerminate(s string) []byte {
	if s == "" {
		return nil
	}
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}
