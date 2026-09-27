// trampolines.cpp adds the flat, pointer-taking exports plugins/observer's
// host requires of every module it drives (f4observer_open_storage,
// f4observer_close_storage; see ../../../doc.go's "module_cbs indirection"
// section), forwarding straight to isoimg.cpp's own OpenStorage/CloseStorage
// -- the same functions isoimg.cpp's own LoadSubModule assigns into
// ModuleLoadParameters.ApiFuncs (module_cbs) for a real Windows host to call
// through that table. A wasm host has no public way to call through a
// guest's own indirect-call table, so f4 needs these named exports instead;
// isoimg.cpp is not modified to add them; only OpenStorage's and
// CloseStorage's already-external C++ linkage is used to call them from
// here.
//
// Each trampoline takes StorageOpenParams/HANDLE by pointer, not by
// isoimg.cpp's own by-value StorageOpenParams parameter, so the exported
// wasm function signature is exactly the three raw i32 pointers
// plugins/observer/runtime.go's OpenStorage already calls
// (paramsPtr, storageOutPtr, infoOutPtr) -- independent of whatever ABI
// clang happens to choose for passing a >8-byte struct by value, which this
// file never has to rely on. GetItem/ExtractItem/PrepareFiles trampolines
// are reserved names (doc.go) and not added yet; this part only drives
// LoadSubModule and OpenStorage.
//
// f4's own code (same license as the rest of this repository, see LICENSE).

#include "windows.h"
#include "ModuleDef.h"

// Declared, not defined, here: these are isoimg.cpp's own OpenStorage and
// CloseStorage (isoimg.cpp in the upstream tree fetched at build time by
// scripts/build_isoimg_test_wasm.sh), given C++ linkage exactly like
// isoimg.cpp itself declares them so the two translation units agree on one
// mangled symbol each.
extern int OpenStorage(StorageOpenParams params, HANDLE *storage,
                        StorageGeneralInfo *info);
extern void CloseStorage(HANDLE storage);

extern "C" __attribute__((export_name("f4observer_open_storage"))) int
f4observer_open_storage(StorageOpenParams *params, HANDLE *storage,
                         StorageGeneralInfo *info) {
    return OpenStorage(*params, storage, info);
}

extern "C" __attribute__((export_name("f4observer_close_storage"))) void
f4observer_close_storage(HANDLE storage) {
    CloseStorage(storage);
}
