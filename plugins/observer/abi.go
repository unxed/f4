package observer

// This file ports the Observer module ABI (API v6) from
// src/common/ModuleDef.h in github.com/lazyhamster/Observer to the byte
// layout a wasm32 module built with wasi-sdk actually uses:
//
//   - every field is packed with no padding, exactly as the header's own
//     #pragma pack(push, 1) requires;
//   - a pointer (including a function pointer) is 4 bytes;
//   - size_t and DWORD are 4 bytes;
//   - wchar_t is 4 bytes, because that is what wasi-sdk's clang gives it on
//     wasm32 -- the original header targets the Windows ABI, where wchar_t
//     is a UTF-16 code unit, 2 bytes. This package writes and reads
//     wchar_t strings as one Unicode code point per 4-byte unit; a
//     compatibility shim inside a ported module is what will eventually
//     have to bridge that to whatever the module's own C++ source expects.
//   - __int64 is 8 bytes.
//
// Only the structs actually driven by runtime.go in this part
// (StorageOpenParams, StorageGeneralInfo, ModuleLoadParameters and its
// nested GUID/ModuleCbs) have read/write helpers below. StorageItemInfo,
// ExtractOperationParams and ExtractProcessCallbacks are laid out and sized
// here because item 1 of f4#1563's part-1 plan asks for the whole table, but
// GetItem/ExtractItem/PrepareFiles marshaling is reserved for a later part.

import "encoding/binary"

// Open storage return results (SOR_*).
const (
	SORInvalidFile      int32 = 0
	SORSuccess          int32 = 1
	SORPasswordRequired int32 = 2
)

// Item retrieval results (GET_ITEM_*).
const (
	GetItemError       int32 = 0
	GetItemOK          int32 = 1
	GetItemNoMoreItems int32 = 2
)

// Extract results (SER_*).
const (
	SERSuccess          int32 = 0
	SERErrorWrite       int32 = 1
	SERErrorRead        int32 = 2
	SERErrorSystem      int32 = 3
	SERUserAbort        int32 = 4
	SERPasswordRequired int32 = 5
)

// ActualAPIVersion mirrors ACTUAL_API_VERSION from ModuleDef.h.
const ActualAPIVersion uint32 = 6

// MakeModuleVersion mirrors the MAKEMODULEVERSION(mj, mn) macro.
func MakeModuleVersion(major, minor uint16) uint32 {
	return uint32(major)<<16 | uint32(minor)
}

// Exported guest function names. A module targeting f4 exports
// LoadSubModule and UnloadSubModule exactly as the real Observer ABI names
// them (LoadSubModuleFunc/UnloadSubModuleFunc in ModuleDef.h), plus the flat
// trampolines below in place of calling through module_cbs -- see doc.go for
// why. GetItem/ExtractItem/PrepareFiles are reserved names: this part
// resolves and requires only the first four.
const (
	ExportLoadSubModule   = "LoadSubModule"
	ExportUnloadSubModule = "UnloadSubModule"
	ExportOpenStorage     = "f4observer_open_storage"
	ExportCloseStorage    = "f4observer_close_storage"
	ExportGetItem         = "f4observer_get_item"
	ExportExtractItem     = "f4observer_extract_item"
	ExportPrepareFiles    = "f4observer_prepare_files"

	// ExportMalloc and ExportFree are the allocator the host uses to place
	// argument structs and strings in the guest's own linear memory before
	// a call, and to release them afterwards. A wasi-sdk reactor build
	// exports libc's malloc/free under these names when asked to
	// (-Wl,--export=malloc,--export=free); see
	// scripts/build_observer_test_wasm.sh.
	ExportMalloc = "malloc"
	ExportFree   = "free"
)

// wcharSize is sizeof(wchar_t) in a wasi-sdk wasm32 build.
const wcharSize = 4

// --- GUID (16 bytes) ---------------------------------------------------

// GUID mirrors the Windows GUID struct referenced by ModuleLoadParameters.
type GUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

const guidSize = 16

func decodeGUID(b []byte) GUID {
	var g GUID
	g.Data1 = binary.LittleEndian.Uint32(b[0:4])
	g.Data2 = binary.LittleEndian.Uint16(b[4:6])
	g.Data3 = binary.LittleEndian.Uint16(b[6:8])
	copy(g.Data4[:], b[8:16])
	return g
}

// --- FILETIME (8 bytes) -------------------------------------------------

// FileTime mirrors the Windows FILETIME struct.
type FileTime struct {
	Low  uint32
	High uint32
}

const fileTimeSize = 8

func decodeFileTime(b []byte) FileTime {
	return FileTime{
		Low:  binary.LittleEndian.Uint32(b[0:4]),
		High: binary.LittleEndian.Uint32(b[4:8]),
	}
}

// --- StorageGeneralInfo (648 bytes) -------------------------------------

const (
	storageFormatNameMaxLen = 32
	storageParamMaxLen      = 64

	storageGeneralInfoFormatOff      = 0
	storageGeneralInfoCompressionOff = storageGeneralInfoFormatOff + storageFormatNameMaxLen*wcharSize
	storageGeneralInfoCommentOff     = storageGeneralInfoCompressionOff + storageParamMaxLen*wcharSize
	storageGeneralInfoCreatedOff     = storageGeneralInfoCommentOff + storageParamMaxLen*wcharSize
	storageGeneralInfoSize           = storageGeneralInfoCreatedOff + fileTimeSize
)

// StorageGeneralInfo mirrors ModuleDef.h's StorageGeneralInfo, decoded into
// Go strings and a FileTime rather than kept as raw wasm bytes.
type StorageGeneralInfo struct {
	Format      string
	Compression string
	Comment     string
	Created     FileTime
}

func decodeStorageGeneralInfo(b []byte) StorageGeneralInfo {
	return StorageGeneralInfo{
		Format:      decodeWCharField(b[storageGeneralInfoFormatOff:storageGeneralInfoCompressionOff]),
		Compression: decodeWCharField(b[storageGeneralInfoCompressionOff:storageGeneralInfoCommentOff]),
		Comment:     decodeWCharField(b[storageGeneralInfoCommentOff:storageGeneralInfoCreatedOff]),
		Created:     decodeFileTime(b[storageGeneralInfoCreatedOff:storageGeneralInfoSize]),
	}
}

// --- StorageOpenParams (20 bytes) ---------------------------------------

const (
	storageOpenParamsStructSizeOff = 0
	storageOpenParamsFilePathOff   = 4
	storageOpenParamsPasswordOff   = 8
	storageOpenParamsDataOff       = 12
	storageOpenParamsDataSizeOff   = 16
	storageOpenParamsSize          = 20
)

// StorageOpenParams is the host-side, already-decoded shape of what
// ModuleDef.h calls StorageOpenParams. FilePath is the guest-visible path
// f4 mounted the probed file at (see fsbridge.go), not a host path.
type StorageOpenParams struct {
	FilePath string
	Password string
	Data     []byte
}

func encodeStorageOpenParams(b []byte, filePathPtr, passwordPtr, dataPtr, dataSize uint32) {
	binary.LittleEndian.PutUint32(b[storageOpenParamsStructSizeOff:], storageOpenParamsSize)
	binary.LittleEndian.PutUint32(b[storageOpenParamsFilePathOff:], filePathPtr)
	binary.LittleEndian.PutUint32(b[storageOpenParamsPasswordOff:], passwordPtr)
	binary.LittleEndian.PutUint32(b[storageOpenParamsDataOff:], dataPtr)
	binary.LittleEndian.PutUint32(b[storageOpenParamsDataSizeOff:], dataSize)
}

// --- StorageItemInfo (4390 bytes) ---------------------------------------
//
// Laid out in part 1 (f4#1563) for completeness; GetItem marshaling itself
// (decodeStorageItemInfo, Module.GetItem in runtime.go) lands in part 3.

const (
	storageItemNameMaxLen = 64
	storageItemPathMaxLen = 1024

	storageItemInfoSizeOff             = 0
	storageItemInfoPackedSizeOff       = storageItemInfoSizeOff + 8
	storageItemInfoAttributesOff       = storageItemInfoPackedSizeOff + 8
	storageItemInfoCreationTimeOff     = storageItemInfoAttributesOff + 4
	storageItemInfoModificationTimeOff = storageItemInfoCreationTimeOff + fileTimeSize
	storageItemInfoNumHardlinksOff     = storageItemInfoModificationTimeOff + fileTimeSize
	storageItemInfoOwnerOff            = storageItemInfoNumHardlinksOff + 2
	storageItemInfoPathOff             = storageItemInfoOwnerOff + storageItemNameMaxLen*wcharSize
	storageItemInfoSize                = storageItemInfoPathOff + storageItemPathMaxLen*wcharSize
)

// StorageItemInfo mirrors ModuleDef.h's StorageItemInfo, decoded into Go
// strings and FileTime values the way decodeStorageGeneralInfo already does
// for StorageGeneralInfo.
type StorageItemInfo struct {
	Size             int64
	PackedSize       int64
	Attributes       uint32
	CreationTime     FileTime
	ModificationTime FileTime
	NumHardlinks     uint16
	Owner            string
	Path             string
}

func decodeStorageItemInfo(b []byte) StorageItemInfo {
	return StorageItemInfo{
		Size:             int64(binary.LittleEndian.Uint64(b[storageItemInfoSizeOff:])), //nolint:gosec // G115: reinterprets bits, not a narrowing conversion.
		PackedSize:       int64(binary.LittleEndian.Uint64(b[storageItemInfoPackedSizeOff:])),
		Attributes:       binary.LittleEndian.Uint32(b[storageItemInfoAttributesOff:]),
		CreationTime:     decodeFileTime(b[storageItemInfoCreationTimeOff : storageItemInfoCreationTimeOff+fileTimeSize]),
		ModificationTime: decodeFileTime(b[storageItemInfoModificationTimeOff : storageItemInfoModificationTimeOff+fileTimeSize]),
		NumHardlinks:     binary.LittleEndian.Uint16(b[storageItemInfoNumHardlinksOff:]),
		Owner:            decodeWCharField(b[storageItemInfoOwnerOff : storageItemInfoOwnerOff+storageItemNameMaxLen*wcharSize]),
		Path:             decodeWCharField(b[storageItemInfoPathOff : storageItemInfoPathOff+storageItemPathMaxLen*wcharSize]),
	}
}

// --- ExtractProcessCallbacks (8 bytes) ----------------------------------

const (
	extractProcessCallbacksSignalContextOff = 0 //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractProcessCallbacksFileProgressOff  = 4 //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractProcessCallbacksSize             = 8 //nolint:unused // reserved size; consumed once ExtractItem marshaling lands in a later part of f4#1563.
)

// --- ExtractOperationParams (24 bytes) ----------------------------------

const (
	extractOperationParamsItemIndexOff = 0                                                                //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractOperationParamsFlagsOff     = 4                                                                //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractOperationParamsDestPathOff  = 8                                                                //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractOperationParamsPasswordOff  = 12                                                               //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractOperationParamsCallbacksOff = 16                                                               //nolint:unused // reserved offset; consumed once ExtractItem marshaling lands in a later part of f4#1563.
	extractOperationParamsSize         = extractOperationParamsCallbacksOff + extractProcessCallbacksSize //nolint:unused // reserved size; consumed once ExtractItem marshaling lands in a later part of f4#1563.
)

// ExtractOperationParams mirrors ModuleDef.h's ExtractOperationParams. Not
// yet produced or consumed anywhere in this package; kept here as the
// documented byte layout a later ExtractItem implementation must use.
type ExtractOperationParams struct {
	ItemIndex     int32
	Flags         int32
	DestPath      string
	Password      string
	SignalContext uint32
}

// --- module_cbs (20 bytes) ------------------------------------------------

const (
	moduleCbsOpenStorageOff  = 0
	moduleCbsCloseStorageOff = 4
	moduleCbsGetItemOff      = 8
	moduleCbsExtractItemOff  = 12
	moduleCbsPrepareFilesOff = 16
	moduleCbsSize            = 20
)

// ModuleCbs holds the five raw function-table indices a module wrote into
// its own module_cbs when LoadSubModule ran. f4 never calls through these;
// see doc.go. They are kept only so a caller can confirm the module
// populated its table.
type ModuleCbs struct {
	OpenStorage  uint32
	CloseStorage uint32
	GetItem      uint32
	ExtractItem  uint32
	PrepareFiles uint32
}

func decodeModuleCbs(b []byte) ModuleCbs {
	return ModuleCbs{
		OpenStorage:  binary.LittleEndian.Uint32(b[moduleCbsOpenStorageOff:]),
		CloseStorage: binary.LittleEndian.Uint32(b[moduleCbsCloseStorageOff:]),
		GetItem:      binary.LittleEndian.Uint32(b[moduleCbsGetItemOff:]),
		ExtractItem:  binary.LittleEndian.Uint32(b[moduleCbsExtractItemOff:]),
		PrepareFiles: binary.LittleEndian.Uint32(b[moduleCbsPrepareFilesOff:]),
	}
}

// --- ModuleLoadParameters (52 bytes) ------------------------------------

const (
	moduleLoadParametersStructSizeOff    = 0
	moduleLoadParametersSettingsOff      = 4
	moduleLoadParametersModuleIdOff      = 8
	moduleLoadParametersModuleVersionOff = moduleLoadParametersModuleIdOff + guidSize
	moduleLoadParametersApiVersionOff    = moduleLoadParametersModuleVersionOff + 4
	moduleLoadParametersApiFuncsOff      = moduleLoadParametersApiVersionOff + 4
	moduleLoadParametersSize             = moduleLoadParametersApiFuncsOff + moduleCbsSize
)

// ModuleInfo is the decoded OUT half of ModuleDef.h's ModuleLoadParameters,
// as read back after a successful LoadSubModule call.
type ModuleInfo struct {
	ModuleId      GUID
	ModuleVersion uint32
	ApiVersion    uint32
	ApiFuncs      ModuleCbs
}

func encodeModuleLoadParameters(b []byte, settingsPtr uint32) {
	// Only StructSize and Settings are IN fields; the rest are OUT and are
	// zeroed here so that a module which does not touch an OUT field (as
	// opposed to a bug in this package) reads back as all-zero, not as
	// whatever happened to be in a reused allocation.
	for i := range b {
		b[i] = 0
	}
	binary.LittleEndian.PutUint32(b[moduleLoadParametersStructSizeOff:], moduleLoadParametersSize)
	binary.LittleEndian.PutUint32(b[moduleLoadParametersSettingsOff:], settingsPtr)
}

func decodeModuleLoadParameters(b []byte) ModuleInfo {
	return ModuleInfo{
		ModuleId:      decodeGUID(b[moduleLoadParametersModuleIdOff : moduleLoadParametersModuleIdOff+guidSize]),
		ModuleVersion: binary.LittleEndian.Uint32(b[moduleLoadParametersModuleVersionOff:]),
		ApiVersion:    binary.LittleEndian.Uint32(b[moduleLoadParametersApiVersionOff:]),
		ApiFuncs:      decodeModuleCbs(b[moduleLoadParametersApiFuncsOff : moduleLoadParametersApiFuncsOff+moduleCbsSize]),
	}
}
