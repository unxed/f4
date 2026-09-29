// Package observer is the host side of running Observer
// (github.com/lazyhamster/Observer) archive-format modules inside f4,
// compiled to WebAssembly and run in-process on top of wazero, the same way
// internal/plughost/transport_wazero.go runs a wasm plugin and colorer4go
// (github.com/unxed/colorer4go) runs a wasm C++ library.
//
// # Scope so far (f4#1563)
//
// Parts 1-4 built infrastructure only: ABI marshaling and a loader that can
// drive LoadSubModule/OpenStorage/GetItem/ExtractItem against a real module,
// with no notion of a directory tree beyond one GetItem call at a time, no
// vfs.VFSProvider, and nothing reachable from Enter on a panel. Part 5 adds
// exactly one, deliberately narrow slice on top of that: Provider
// (provider.go) is a real vfs.VFSProvider, registered from both build tags
// (internal/plughost/manager.go), that drives a real isoimg.wasm against a
// real ISO9660 image, builds its whole directory tree from a GetItem walk
// (ObserverVFS in vfs.go), and lets a panel browse it and read files out of
// it -- with no observer.ini, no module selection beyond that one hardcoded
// module, and no cancellation beyond what ctx already gives every VFS
// call. Those remain later, separate parts; see status/1563.md in the
// accounting repository for what is next. Part 7 (password.go) already
// closed one item off that list: OpenStorage returning
// SOR_PASSWORD_REQUIRED now drives the same interactive password retry loop
// plugins/archive/password.go gives ArchiveVFS, instead of a bare error.
// Part 8 added PanelEnterAllowed (provider.go), ObserverEnterExcludeMask's
// own escape hatch. Part 9 (config.go) closed the remaining "no
// observer.ini, no module selection" gap named two paragraphs up: Provider
// now tries an ordered []moduleEntry read from
// observer.ini/observer_user.ini (falling back to the same single
// isoimg/"*.iso" row when neither file exists), which answers both
// "configure Observer the way Far does" and "pick between several modules
// for one format" with the same mechanism -- file order in [Modules]
// already is the priority, upstream's own host never needed a second one.
// PlugRing distribution and real modules beyond isoimg remain unstarted.
//
// What parts 1-4 already provide, and part 5 builds on unchanged, is:
//
//   - Go types and constants for the Observer module ABI (API v6, see
//     src/common/ModuleDef.h in lazyhamster/Observer), laid out the way a
//     wasm32 build of a module sees them: 4-byte pointers, 4-byte size_t,
//     and 4-byte wchar_t (wasi-sdk compiles wchar_t as a 32-bit int, unlike
//     the 16-bit wchar_t of the real Windows ABI the ModuleDef.h header
//     targets).
//   - A wazero host module ("observer") with the progress callback import.
//   - A loader that can instantiate an arbitrary WASI-reactor .wasm module
//     and drive LoadSubModule/OpenStorage/CloseStorage/GetItem/ExtractItem
//     against it, proving the struct marshaling and the host imports all
//     work -- part 1 proved this against a small test-only module
//     (testdata/stub); part 2 against a real, unmodified upstream module
//     (isoimg, built by scripts/build_isoimg_test_wasm.sh, see
//     plugins/observer/testdata/isoimg/compat/); part 3 added GetItem; part
//     4 added ExtractItem, all exercised against both.
//   - Read access to the probed file for the guest, through a WASI
//     filesystem mount backed by an io.ReaderAt-like view of the parent
//     VFS, not a real path on the host disk. That is what lets a module
//     opened on a nested archive member read straight through to wherever
//     the bytes actually live. A second, real read-write directory mount
//     (LoadModule's WithExtractDir) gives ExtractItem somewhere to write
//     extracted files, for now a plain host temp directory -- the same
//     "extract to a temp file first" approach plugins/multiarc already uses.
//
// # The module_cbs indirection
//
// Real Observer modules export only LoadSubModule and UnloadSubModule.
// LoadSubModule fills in a ModuleLoadParameters.ApiFuncs table of five
// function pointers (OpenStorage, CloseStorage, GetItem, ExtractItem,
// PrepareFiles) that the Windows host is meant to call directly. In wasm a C
// function pointer is an index into the module's own internal indirect-call
// table, which wazero has no public API to invoke from the host side without
// the module also exporting a callable trampoline for it.
//
// So a module targeting f4 additionally exports flat, pointer-taking
// trampolines under the fixed names in ExportOpenStorage, ExportCloseStorage
// and so on (see the Export* constants below), each simply forwarding to
// whatever module_cbs entry the module itself populated. ApiFuncs is still
// read back and kept on ModuleInfo after LoadSubModule returns, purely so a
// caller (or a test) can confirm the module actually populated its table --
// f4 never calls through those values itself.
//
// Only ExportOpenStorage and ExportCloseStorage are in LoadModule's required
// map: a module this package can drive at all must have those two, but
// ExportGetItem/ExportExtractItem are resolved opportunistically
// (Module.GetItem/Module.ExtractItem error clearly if a module lacks the
// corresponding trampoline) so a module need not implement every trampoline
// from day one. ExportPrepareFiles remains a reserved name, not yet driven
// by anything in this package.
//
// ExtractItem's ExtractProcessCallbacks.FileProgress is a genuine function
// pointer, not a struct field the host can just fill in the way it fills in
// everything else: see ExportProgressTrampoline's own doc comment for how a
// module hands the host something it actually can put there.
//
// # No random access to an item's own content
//
// The container file a module is opened on gets full random access already
// (see "Read access to the probed file" above): the WASI mount serves
// fd_pread/fd_seek straight off the parent VFS's own ReadAt, at whatever
// offset the module's own CreateFile/ReadFile/SetFilePointer(Ex) calls ask
// for, no different from a real file on disk.
//
// An individual *item* inside that container is a different matter. API v6
// (ModuleDef.h, unchanged since 2016) has exactly one way to get an item's
// bytes out: ExtractFunc, which always writes the whole item to a
// caller-chosen DestPath on a real filesystem -- there is no
// OpenItemStream/ReadItem/Seek in the ABI, and no flag in
// ExtractOperationParams that asks for one. So an Observer-backed item can
// never be opened for random-access reads the way vfs.ReadAtCloser
// generally allows: Module.ExtractItem's only option, and this package's
// only option in turn, is what LoadModule's WithExtractDir already does --
// extract the whole item to a real file first (the same "extract to a temp
// file" fallback plugins/archive already uses for solid 7z/RAR), then hand
// out a ReadAtCloser over *that* file. This is an ABI limitation, not
// something a smarter host implementation could work around; it also means
// f4#1563's own item 5 (partial-decompression zran-style ReadAt for nested
// archives) has no Observer-side equivalent to build -- only plugins/archive
// zip/deflate members can ever get that treatment.
//
// # Nested archive composition (f4#1653)
//
// The input side of ObserverVFS already uses the parent's ReadAt-backed WASI
// mount, so an Observer container can participate in the generic
// reader-backed archive composition path in plugins/archive. That path avoids
// copying an archive intermediary before the next provider opens it. The
// output side has the ABI limit described above: opening an Observer item
// still calls ExtractItem and materializes the complete item in the temporary
// extraction directory. This is intentional and documented; it is not a
// claim that Observer items have random-access streams.
package observer
