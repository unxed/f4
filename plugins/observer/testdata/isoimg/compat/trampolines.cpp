// trampolines.cpp adds the flat exports plugins/observer's host requires of
// every module it drives: LoadSubModule/UnloadSubModule under their real
// Observer ABI names, and f4observer_open_storage/f4observer_close_storage
// forwarding to isoimg.cpp's own OpenStorage/CloseStorage (see
// ../../../doc.go's "module_cbs indirection" section) -- the same functions
// isoimg.cpp's own LoadSubModule assigns into ModuleLoadParameters.ApiFuncs
// (module_cbs) for a real Windows host to call through that table. A wasm
// host has no public way to call through a guest's own indirect-call table,
// so f4 needs these named exports instead; isoimg.cpp is not modified to add
// them, only its own external C++ linkage is used to call into it from here.
//
// Every wrapper here is given its wasm export name explicitly via
// __attribute__((export_name(...))), rather than relying on its own,
// otherwise arbitrary C symbol name: isoimg.cpp declares LoadSubModule,
// UnloadSubModule, OpenStorage and CloseStorage without extern "C", so their
// *compiled* symbols are C++-mangled, not the literal names above -- a
// linker --export=LoadSubModule (matching by raw symbol name) fails to find
// them at all. Exporting a differently-named C wrapper under the wasm name
// the ABI actually needs sidesteps that entirely, and does not need a
// matching -Wl,--export= flag either: the attribute alone puts the entry in
// the module's export table.
//
// Each OpenStorage/CloseStorage trampoline takes StorageOpenParams/HANDLE by
// pointer, not by isoimg.cpp's own by-value StorageOpenParams parameter, so
// the exported wasm function signature is exactly the three raw i32
// pointers plugins/observer/runtime.go's OpenStorage already calls
// (paramsPtr, storageOutPtr, infoOutPtr) -- independent of whatever ABI
// clang happens to choose for passing a >8-byte struct by value, which this
// file never has to rely on. GetItem forwards to isoimg.cpp's own
// GetStorageItem (the name isoimg.cpp itself gives the function it assigns
// to ApiFuncs.GetItem -- ModuleDef.h's GetItemFunc already takes item_info
// by pointer, so no by-value struct is involved there at all).
// ExtractItem/PrepareFiles trampolines are still reserved names (doc.go),
// not added yet.
//
// f4's own code (same license as the rest of this repository, see LICENSE).

#include "windows.h"
#include "ModuleDef.h"

// Declared, not defined, here: these are isoimg.cpp's own functions
// (isoimg.cpp in the upstream tree fetched at build time by
// scripts/build_isoimg_test_wasm.sh), given C++ linkage exactly like
// isoimg.cpp itself declares them so the two translation units agree on one
// mangled symbol each.
extern int LoadSubModule(ModuleLoadParameters *params);
extern void UnloadSubModule(void);
extern int OpenStorage(StorageOpenParams params, HANDLE *storage,
                        StorageGeneralInfo *info);
extern void CloseStorage(HANDLE storage);
extern int GetStorageItem(HANDLE storage, int item_index,
                           StorageItemInfo *item_info);

extern "C" __attribute__((export_name("LoadSubModule"))) int
f4_export_LoadSubModule(ModuleLoadParameters *params) {
    return LoadSubModule(params);
}

extern "C" __attribute__((export_name("UnloadSubModule"))) void
f4_export_UnloadSubModule(void) {
    UnloadSubModule();
}

extern "C" __attribute__((export_name("f4observer_open_storage"))) int
f4observer_open_storage(StorageOpenParams *params, HANDLE *storage,
                         StorageGeneralInfo *info) {
    return OpenStorage(*params, storage, info);
}

extern "C" __attribute__((export_name("f4observer_close_storage"))) void
f4observer_close_storage(HANDLE storage) {
    CloseStorage(storage);
}

extern "C" __attribute__((export_name("f4observer_get_item"))) int
f4observer_get_item(HANDLE storage, int item_index,
                     StorageItemInfo *item_info) {
    return GetStorageItem(storage, item_index, item_info);
}
