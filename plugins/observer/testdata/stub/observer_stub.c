// observer_stub.c is f4's own, from-scratch test fixture for
// plugins/observer (f4#1563, parts 1 and 3 of N). It is not a port of any
// real Observer module, is licensed the same as the rest of f4 (see
// LICENSE, not Observer's LGPL/GPL), and knows no archive format: it exists
// only to answer the Observer API v6 ABI slice plugins/observer drives
// (LoadSubModule/UnloadSubModule and the f4observer_open_storage /
// f4observer_close_storage / f4observer_get_item trampolines, see
// ../../doc.go) in a fixed, checkable way, so plugins/observer's tests can
// prove that
//   - struct marshaling into and out of wasm32 linear memory is correct
//     (LoadSubModule's ModuleLoadParameters round trip, the Data/DataSize
//     fields of StorageOpenParams, and GetItem's StorageItemInfo);
//   - the WASI filesystem mount plugins/observer builds over an
//     io.ReaderAt-backed parent VFS (fsbridge.go) really does let a module
//     read the probed file's actual bytes, not just trust whatever the host
//     claims about them in the Data head; and
//   - the "observer.progress" host import round-trips a value back to the
//     module.
//
// It always calls SOR_INVALID_FILE (0) for a file that does not start with
// kMagic below, and SOR_SUCCESS (1) for one that does -- decided by
// actually opening FilePath through the mounted WASI filesystem and reading
// its first bytes, not from the inline Data head.
//
// Built only in CI by scripts/build_observer_test_wasm.sh (wasi-sdk); see
// that script for the exact compiler flags this file depends on
// (-mexec-model=reactor and the --export list for LoadSubModule,
// UnloadSubModule, the f4observer_* trampolines, the four
// f4observer_last_*/close_count accessors below, and libc's malloc/free).

#include <fcntl.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <unistd.h>

#define API_VERSION 6

// Kept in sync with the magic plugins/observer's tests write to the front of
// the "recognized" probe file (observer_test.go).
static const unsigned char kMagic[8] = {'F', '4', 'O', 'B', 'S', 'V', '0', '1'};

__attribute__((import_module("observer"), import_name("progress")))
extern int32_t observer_progress(int32_t signal_context, int64_t bytes_done);

#pragma pack(push, 1)

typedef struct {
	uint32_t StructSize;
	uint32_t FilePath; // const wchar_t* (wasi-sdk wchar_t: 4 bytes/unit)
	uint32_t Password; // const char*
	uint32_t Data;     // const void*
	uint32_t DataSize;
} StorageOpenParams;

typedef struct {
	uint32_t Format[32];
	uint32_t Compression[64];
	uint32_t Comment[64];
	uint32_t CreatedLow;
	uint32_t CreatedHigh;
} StorageGeneralInfo;

typedef struct {
	uint32_t Data1;
	uint16_t Data2;
	uint16_t Data3;
	uint8_t Data4[8];
} ModuleGUID;

typedef struct {
	uint32_t OpenStorage;
	uint32_t CloseStorage;
	uint32_t GetItem;
	uint32_t ExtractItem;
	uint32_t PrepareFiles;
} ModuleCbs;

typedef struct {
	uint32_t StructSize;
	uint32_t Settings; // const wchar_t*
	ModuleGUID ModuleId;
	uint32_t ModuleVersion;
	uint32_t ApiVersion;
	ModuleCbs ApiFuncs;
} ModuleLoadParameters;

// Mirrors ModuleDef.h's StorageItemInfo the same way the structs above
// mirror their own real counterparts; see abi.go's storageItemInfo* offset
// constants for the byte layout this must match.
typedef struct {
	int64_t Size;
	int64_t PackedSize;
	uint32_t Attributes;
	uint32_t CreationTimeLow;
	uint32_t CreationTimeHigh;
	uint32_t ModificationTimeLow;
	uint32_t ModificationTimeHigh;
	uint16_t NumHardlinks;
	uint32_t Owner[64];
	uint32_t Path[1024];
} StorageItemInfo;

#pragma pack(pop)

// Test-introspection state, read back by observer_test.go through the
// f4observer_last_*/close_count accessors below.
static int32_t g_close_count = 0;
static uint32_t g_last_data_size = 0;
static int32_t g_last_data_first_byte = -1;
static int32_t g_last_progress_result = -1;

// wide_to_narrow copies a NUL-terminated wchar_t string to a narrow buffer,
// one byte per code point. It only has to be correct for the ASCII paths
// this fixture is ever given -- a real module's own compatibility shim is
// what will eventually handle the general case.
static void wide_to_narrow(uint32_t wptr, char *out, int max) {
	const uint32_t *w = (const uint32_t *)(uintptr_t)wptr;
	int i = 0;
	for (; i < max - 1; i++) {
		uint32_t c = w[i];
		if (c == 0) {
			break;
		}
		out[i] = (char)c;
	}
	out[i] = 0;
}

__attribute__((export_name("LoadSubModule")))
int32_t LoadSubModule(uint32_t params_ptr) {
	ModuleLoadParameters *p = (ModuleLoadParameters *)(uintptr_t)params_ptr;

	// Fixed, arbitrary marker so the host test can confirm the whole
	// struct, including the nested GUID and module_cbs table, round-trips
	// through wasm32 linear memory correctly. The values themselves carry
	// no meaning; observer_test.go asserts these exact constants.
	p->ModuleId.Data1 = 0xF4013701u;
	p->ModuleId.Data2 = 0x5453u;
	p->ModuleId.Data3 = 0x4255u;
	for (int i = 0; i < 8; i++) {
		p->ModuleId.Data4[i] = (uint8_t)(0xA0 + i);
	}
	p->ModuleVersion = (1u << 16); // MAKEMODULEVERSION(1, 0)
	p->ApiVersion = API_VERSION;
	p->ApiFuncs.OpenStorage = 0xAAAA0001u;
	p->ApiFuncs.CloseStorage = 0xAAAA0002u;
	p->ApiFuncs.GetItem = 0xAAAA0003u;
	p->ApiFuncs.ExtractItem = 0xAAAA0004u;
	p->ApiFuncs.PrepareFiles = 0xAAAA0005u;
	return 1;
}

__attribute__((export_name("UnloadSubModule")))
void UnloadSubModule(void) {}

__attribute__((export_name("f4observer_open_storage")))
int32_t f4observer_open_storage(uint32_t params_ptr, uint32_t storage_out_ptr, uint32_t info_out_ptr) {
	const StorageOpenParams *params = (const StorageOpenParams *)(uintptr_t)params_ptr;

	g_last_data_size = params->DataSize;
	g_last_data_first_byte = -1;
	if (params->Data != 0 && params->DataSize > 0) {
		g_last_data_first_byte = *(const uint8_t *)(uintptr_t)params->Data;
	}

	char path[512];
	path[0] = 0;
	if (params->FilePath != 0) {
		wide_to_narrow(params->FilePath, path, sizeof(path));
	}

	int matched = 0;
	if (path[0] != 0) {
		int fd = open(path, O_RDONLY);
		if (fd >= 0) {
			unsigned char head[sizeof(kMagic)];
			ssize_t n = read(fd, head, sizeof(head));
			if (n == (ssize_t)sizeof(kMagic) && memcmp(head, kMagic, sizeof(kMagic)) == 0) {
				matched = 1;
			}
			close(fd);
		}
	}

	if (!matched) {
		return 0; // SOR_INVALID_FILE
	}

	g_last_progress_result = observer_progress(0, (int64_t)params->DataSize);

	if (storage_out_ptr != 0) {
		*(uint32_t *)(uintptr_t)storage_out_ptr = 0xF4000001u;
	}
	if (info_out_ptr != 0) {
		StorageGeneralInfo *info = (StorageGeneralInfo *)(uintptr_t)info_out_ptr;
		memset(info, 0, sizeof(*info));
		static const char kFormat[] = "F4OBSTUB";
		static const char kCompression[] = "store";
		for (size_t i = 0; i < sizeof(kFormat); i++) {
			info->Format[i] = (uint32_t)(unsigned char)kFormat[i];
		}
		for (size_t i = 0; i < sizeof(kCompression); i++) {
			info->Compression[i] = (uint32_t)(unsigned char)kCompression[i];
		}
	}
	return 1; // SOR_SUCCESS
}

__attribute__((export_name("f4observer_close_storage")))
void f4observer_close_storage(uint32_t storage) {
	(void)storage;
	g_close_count++;
}

// GET_ITEM_* result codes, mirrored from ModuleDef.h (see abi.go's own
// GetItemOK/GetItemNoMoreItems constants).
#define GET_ITEM_OK 1
#define GET_ITEM_NOMOREITEMS 2

// f4observer_get_item answers exactly one fixed, fake item at index 0 --
// enough for observer_test.go to prove GetItem's StorageItemInfo marshaling
// round-trips (including the nested FILETIME-shaped fields and both
// wchar_t[] fields), the same way f4observer_open_storage above proves
// StorageOpenParams's. index 0's own OpenStorage-returned storage handle is
// not checked against storage: this fixture, like the real ABI itself,
// treats storage as an opaque token the module chose, not something the
// caller can construct meaning from.
__attribute__((export_name("f4observer_get_item")))
int32_t f4observer_get_item(uint32_t storage, int32_t index, uint32_t info_ptr) {
	(void)storage;
	if (index != 0) {
		return GET_ITEM_NOMOREITEMS;
	}

	StorageItemInfo *info = (StorageItemInfo *)(uintptr_t)info_ptr;
	memset(info, 0, sizeof(*info));
	info->Size = 42;
	info->PackedSize = 42;
	info->Attributes = 0;
	info->CreationTimeLow = 0x11111111u;
	info->CreationTimeHigh = 0x22222222u;
	info->ModificationTimeLow = 0x33333333u;
	info->ModificationTimeHigh = 0x44444444u;
	info->NumHardlinks = 1;

	static const char kPath[] = "stub-item.txt";
	for (size_t i = 0; i < sizeof(kPath); i++) {
		info->Path[i] = (uint32_t)(unsigned char)kPath[i];
	}

	return GET_ITEM_OK;
}

__attribute__((export_name("f4observer_close_count")))
int32_t f4observer_close_count(void) {
	return g_close_count;
}

__attribute__((export_name("f4observer_last_data_size")))
int32_t f4observer_last_data_size(void) {
	return (int32_t)g_last_data_size;
}

__attribute__((export_name("f4observer_last_data_first_byte")))
int32_t f4observer_last_data_first_byte(void) {
	return g_last_data_first_byte;
}

__attribute__((export_name("f4observer_last_progress_result")))
int32_t f4observer_last_progress_result(void) {
	return g_last_progress_result;
}
