//go:build windows

package vfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

const (
	registryVFSRoot          = "registry://"
	registryDefaultValueName = "(Default)"
)

type registryHive struct {
	name string
	key  registry.Key
}

var registryHives = []registryHive{
	{name: "HKEY_CLASSES_ROOT", key: registry.CLASSES_ROOT},
	{name: "HKEY_CURRENT_USER", key: registry.CURRENT_USER},
	{name: "HKEY_LOCAL_MACHINE", key: registry.LOCAL_MACHINE},
	{name: "HKEY_USERS", key: registry.USERS},
	{name: "HKEY_CURRENT_CONFIG", key: registry.CURRENT_CONFIG},
}

// RegistryVFS exposes the Windows registry as a virtual filesystem. Registry
// keys are directories and registry values are virtual text files. Reads
// always open keys with registry.READ only. Part 2 of f4#242 adds editing an
// existing value's data through the standard EditorView's Save: Create
// re-opens just the parent key with the narrower registry.SET_VALUE access
// for that one write. Keys and values can still not be created, renamed or
// removed — MkDir, Remove and Rename keep refusing every call, and Create
// refuses a path whose value does not already exist. This keeps the
// destructive surface to exactly what f4#242 asked for ("modifying registry
// values"), not to registry structure changes.
type RegistryVFS struct {
	currentPath string
}

func NewRegistryVFS() *RegistryVFS {
	return &RegistryVFS{currentPath: registryVFSRoot}
}

func (v *RegistryVFS) GetPath() string { return v.currentPath }

func (v *RegistryVFS) IsAbs(p string) bool {
	return strings.HasPrefix(p, registryVFSRoot)
}

func (v *RegistryVFS) IsAtRoot() bool {
	return v.currentPath == registryVFSRoot
}

func (v *RegistryVFS) SetPath(p string) error {
	if p == "" || p == "/" {
		p = registryVFSRoot
	}
	segments, err := registryPathSegments(p)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		v.currentPath = registryVFSRoot
		return nil
	}
	key, owned, err := openRegistryKey(segments)
	if err != nil {
		return err
	}
	if owned {
		defer key.Close()
	}
	v.currentPath = registryPath(segments)
	return nil
}

func (v *RegistryVFS) ReadDir(ctx context.Context, p string, onChunk func([]VFSItem)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	segments, err := registryPathSegments(p)
	if err != nil {
		return err
	}

	if len(segments) == 0 {
		items := make([]VFSItem, 0, len(registryHives))
		for _, hive := range registryHives {
			items = append(items, VFSItem{
				Name:        hive.name,
				IsDir:       true,
				NoExtension: true,
			})
		}
		if onChunk != nil {
			onChunk(items)
		}
		return nil
	}

	key, owned, err := openRegistryKey(segments)
	if err != nil {
		return err
	}
	if owned {
		defer key.Close()
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	subkeys, err := key.ReadSubKeyNames(0)
	if err != nil {
		return registryVFSMapError(err)
	}
	values, err := key.ReadValueNames(0)
	if err != nil {
		return registryVFSMapError(err)
	}
	sort.Strings(subkeys)
	sort.Strings(values)

	items := make([]VFSItem, 0, len(subkeys)+len(values))
	for _, name := range subkeys {
		if err := ctx.Err(); err != nil {
			return err
		}
		items = append(items, VFSItem{
			Name:        name,
			IsDir:       true,
			NoExtension: true,
		})
	}
	for _, name := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, err := registryValueItem(key, name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// A value can disappear while the key is being listed.
				continue
			}
			return err
		}
		items = append(items, item)
	}
	if len(items) > 0 && onChunk != nil {
		onChunk(items)
	}
	return nil
}

func (v *RegistryVFS) Stat(ctx context.Context, p string) (VFSItem, error) {
	if err := ctx.Err(); err != nil {
		return VFSItem{}, err
	}
	segments, err := registryPathSegments(p)
	if err != nil {
		return VFSItem{}, err
	}
	if len(segments) == 0 {
		return VFSItem{Name: "Registry", IsDir: true, NoExtension: true}, nil
	}

	key, owned, keyErr := openRegistryKey(segments)
	if keyErr == nil {
		if owned {
			defer key.Close()
		}
		info, err := key.Stat()
		if err != nil {
			return VFSItem{}, registryVFSMapError(err)
		}
		return VFSItem{
			Name:        registryDisplayName(segments[len(segments)-1]),
			IsDir:       true,
			MTime:       info.ModTime(),
			NoExtension: true,
		}, nil
	}
	if !errors.Is(keyErr, os.ErrNotExist) {
		return VFSItem{}, keyErr
	}
	if len(segments) < 2 {
		return VFSItem{}, keyErr
	}

	parent, owned, err := openRegistryKey(segments[:len(segments)-1])
	if err != nil {
		return VFSItem{}, err
	}
	if owned {
		defer parent.Close()
	}
	valueName := registryValueName(segments[len(segments)-1])
	data, valueType, err := readRegistryValue(parent, valueName)
	if err != nil {
		return VFSItem{}, registryVFSMapError(err)
	}
	content := formatRegistryValue(valueName, valueType, data)
	return VFSItem{
		Name:        registryDisplayName(segments[len(segments)-1]),
		Size:        int64(len(content)),
		SizeKnown:   true,
		Mode:        registryValueTypeName(valueType),
		NoExtension: true,
		Revision:    registryValueRevision(valueType, data),
	}, nil
}

func (v *RegistryVFS) Join(elem ...string) string {
	segments := make([]string, 0)
	for _, part := range elem {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, registryVFSRoot) {
			parsed, err := registryPathSegments(part)
			if err != nil {
				return registryVFSRoot
			}
			segments = append(segments, parsed...)
			continue
		}
		switch part {
		case ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, part)
		}
	}
	return registryPath(segments)
}

func (v *RegistryVFS) Abs(p string) (string, error) {
	if p == "" {
		return v.currentPath, nil
	}
	if v.IsAbs(p) {
		segments, err := registryPathSegments(p)
		if err != nil {
			return "", err
		}
		return registryPath(segments), nil
	}
	return v.Join(v.currentPath, p), nil
}

func (v *RegistryVFS) Base(p string) string {
	segments, err := registryPathSegments(p)
	if err != nil || len(segments) == 0 {
		return "Registry"
	}
	return registryDisplayName(segments[len(segments)-1])
}

func (v *RegistryVFS) Dir(p string) string {
	segments, err := registryPathSegments(p)
	if err != nil || len(segments) == 0 {
		return registryVFSRoot
	}
	return registryPath(segments[:len(segments)-1])
}

func (v *RegistryVFS) MkDir(ctx context.Context, _ string) error {
	return registryVFSReadOnlyError(ctx)
}

func (v *RegistryVFS) Remove(ctx context.Context, _ string) error {
	return registryVFSReadOnlyError(ctx)
}

func (v *RegistryVFS) Rename(ctx context.Context, _, _ string) error {
	return registryVFSReadOnlyError(ctx)
}

func (v *RegistryVFS) GetCapabilities() VFSCapabilities {
	return VFSCapabilities{
		HasRandomAccess: true,
		// HasWrite: Create can commit an edited value of a supported type
		// (REG_SZ, REG_EXPAND_SZ, REG_MULTI_SZ, REG_DWORD, REG_QWORD or
		// REG_BINARY) back to the registry. It cannot create a value or key
		// that does not already exist, so this is narrower than most other
		// HasWrite backends; callers still see a precise error for anything
		// outside that scope.
		HasWrite: true,
		// Editing a value updates its data in place; the value's registry
		// identity (its name under its parent key) never changes.
		HasIdentityPreservingWrite: true,
	}
}

func (v *RegistryVFS) Search(context.Context, string, string) (chan int64, error) {
	return nil, nil
}

func (v *RegistryVFS) Open(ctx context.Context, p string) (ReadAtCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	segments, err := registryPathSegments(p)
	if err != nil {
		return nil, err
	}
	if len(segments) < 2 {
		return nil, os.ErrInvalid
	}

	// A key takes precedence over a value with the same name, matching the
	// directory-first presentation in ReadDir.
	if key, owned, keyErr := openRegistryKey(segments); keyErr == nil {
		if owned {
			key.Close()
		}
		return nil, os.ErrInvalid
	} else if !errors.Is(keyErr, os.ErrNotExist) {
		return nil, keyErr
	}

	parent, owned, err := openRegistryKey(segments[:len(segments)-1])
	if err != nil {
		return nil, err
	}
	if owned {
		defer parent.Close()
	}
	data, valueType, err := readRegistryValue(parent, registryValueName(segments[len(segments)-1]))
	if err != nil {
		return nil, registryVFSMapError(err)
	}
	return &registryReader{data: formatRegistryValue(registryValueName(segments[len(segments)-1]), valueType, data)}, nil
}

// Create opens an existing registry value for editing. It never creates a
// new value or a new key: the standard EditorView's Save is the only caller
// this needs to satisfy, and that always targets a value the panel already
// listed. The actual registry write happens in registryValueWriter.Close,
// once the full edited text is known.
func (v *RegistryVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	segments, err := registryPathSegments(p)
	if err != nil {
		return nil, err
	}
	if len(segments) < 2 {
		return nil, registryVFSReadOnlyError(ctx)
	}

	// A key by this name takes precedence, matching Open and ReadDir: Create
	// never overwrites a key with a value.
	if key, owned, keyErr := openRegistryKey(segments); keyErr == nil {
		if owned {
			key.Close()
		}
		return nil, registryVFSReadOnlyError(ctx)
	} else if !errors.Is(keyErr, os.ErrNotExist) {
		return nil, keyErr
	}

	parent, owned, err := openRegistryKey(segments[:len(segments)-1])
	if err != nil {
		return nil, err
	}
	if owned {
		defer parent.Close()
	}
	name := registryValueName(segments[len(segments)-1])
	if _, _, err := readRegistryValue(parent, name); err != nil {
		return nil, registryVFSMapError(err)
	}
	return &registryValueWriter{segments: append([]string(nil), segments...)}, nil
}

func (v *RegistryVFS) SetAttributes(ctx context.Context, _ string, _ VFSItem) error {
	return registryVFSReadOnlyError(ctx)
}

func (v *RegistryVFS) ParentVFS() VFS { return nil }

func (v *RegistryVFS) Clone() VFS {
	return &RegistryVFS{currentPath: v.currentPath}
}

func (v *RegistryVFS) Close() error { return nil }

func registryPathSegments(p string) ([]string, error) {
	if p == "" || p == "/" {
		return nil, nil
	}
	if !strings.HasPrefix(p, registryVFSRoot) {
		return nil, os.ErrInvalid
	}
	rest := strings.TrimPrefix(p, registryVFSRoot)
	if rest == "" {
		return nil, nil
	}
	if strings.HasPrefix(rest, "/") {
		return nil, os.ErrInvalid
	}
	encoded := strings.Split(rest, "/")
	segments := make([]string, 0, len(encoded))
	for _, part := range encoded {
		if part == "" {
			return nil, os.ErrInvalid
		}
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || strings.ContainsRune(decoded, '\x00') {
			return nil, os.ErrInvalid
		}
		segments = append(segments, decoded)
	}
	return segments, nil
}

func registryPath(segments []string) string {
	if len(segments) == 0 {
		return registryVFSRoot
	}
	encoded := make([]string, len(segments))
	for i, segment := range segments {
		encoded[i] = url.PathEscape(segment)
	}
	return registryVFSRoot + strings.Join(encoded, "/")
}

func registryHiveByName(name string) (registryHive, bool) {
	for _, hive := range registryHives {
		if strings.EqualFold(name, hive.name) {
			return hive, true
		}
	}
	return registryHive{}, false
}

// openRegistryKey opens a key with the READ access every listing and value
// read uses. Writing a value re-opens the same path through
// openRegistryKeyAccess with the narrower access that write actually needs,
// rather than widening what every read holds open.
func openRegistryKey(segments []string) (registry.Key, bool, error) {
	return openRegistryKeyAccess(segments, registry.READ)
}

func openRegistryKeyAccess(segments []string, access uint32) (registry.Key, bool, error) {
	if len(segments) == 0 {
		return 0, false, os.ErrInvalid
	}
	hive, ok := registryHiveByName(segments[0])
	if !ok {
		return 0, false, os.ErrNotExist
	}
	if len(segments) == 1 {
		// Predefined hive handles are not opened through OpenKey and carry
		// no access mask of their own to narrow.
		return hive.key, false, nil
	}
	for _, segment := range segments[1:] {
		if strings.ContainsRune(segment, '\\') {
			return 0, false, os.ErrInvalid
		}
	}
	key, err := registry.OpenKey(hive.key, strings.Join(segments[1:], `\`), access)
	if err != nil {
		return 0, false, registryVFSMapError(err)
	}
	return key, true, nil
}

func registryVFSMapError(err error) error {
	if errors.Is(err, registry.ErrNotExist) {
		return os.ErrNotExist
	}
	return err
}

func registryVFSReadOnlyError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.ErrPermission
}

func registryValueName(name string) string {
	if name == registryDefaultValueName {
		return ""
	}
	return name
}

func registryDisplayName(name string) string {
	if name == "" {
		return registryDefaultValueName
	}
	return name
}

func readRegistryValue(key registry.Key, name string) ([]byte, uint32, error) {
	for attempt := 0; attempt < 3; attempt++ {
		n, valueType, err := key.GetValue(name, nil)
		if err != nil && !errors.Is(err, registry.ErrShortBuffer) {
			return nil, valueType, err
		}
		if n < 0 {
			return nil, valueType, os.ErrInvalid
		}
		data := make([]byte, n)
		n, valueType, err = key.GetValue(name, data)
		if errors.Is(err, registry.ErrShortBuffer) {
			continue
		}
		if err != nil {
			return nil, valueType, err
		}
		return data[:n], valueType, nil
	}
	return nil, 0, os.ErrInvalid
}

func registryValueItem(key registry.Key, name string) (VFSItem, error) {
	data, valueType, err := readRegistryValue(key, name)
	if err != nil {
		return VFSItem{}, registryVFSMapError(err)
	}
	return VFSItem{
		Name:        registryDisplayName(name),
		Size:        int64(len(formatRegistryValue(name, valueType, data))),
		SizeKnown:   true,
		Mode:        registryValueTypeName(valueType),
		NoExtension: true,
		Revision:    registryValueRevision(valueType, data),
	}, nil
}

func registryValueRevision(valueType uint32, data []byte) string {
	hash := sha256.New()
	var typeBytes [4]byte
	binary.LittleEndian.PutUint32(typeBytes[:], valueType)
	_, _ = hash.Write(typeBytes[:])
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

func formatRegistryValue(name string, valueType uint32, data []byte) []byte {
	var out strings.Builder
	fmt.Fprintf(&out, "Name: %s\nType: %s\n", registryDisplayName(name), registryValueTypeName(valueType))
	switch valueType {
	case registry.SZ, registry.EXPAND_SZ:
		if value, ok := registryUTF16String(data); ok {
			fmt.Fprintf(&out, "Value: %q\n", value)
			break
		}
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	case registry.MULTI_SZ:
		if values, ok := registryUTF16Strings(data); ok {
			for i, value := range values {
				fmt.Fprintf(&out, "Value[%d]: %q\n", i, value)
			}
			if len(values) == 0 {
				out.WriteString("Value: \"\"\n")
			}
			break
		}
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	case registry.DWORD:
		if len(data) == 4 {
			value := binary.LittleEndian.Uint32(data)
			fmt.Fprintf(&out, "Value: %d (0x%08X)\n", value, value)
			break
		}
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	case registry.DWORD_BIG_ENDIAN:
		if len(data) == 4 {
			value := binary.BigEndian.Uint32(data)
			fmt.Fprintf(&out, "Value: %d (0x%08X)\n", value, value)
			break
		}
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	case registry.QWORD:
		if len(data) == 8 {
			value := binary.LittleEndian.Uint64(data)
			fmt.Fprintf(&out, "Value: %d (0x%016X)\n", value, value)
			break
		}
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	default:
		fmt.Fprintf(&out, "Data: %s\n", hex.EncodeToString(data))
	}
	return []byte(out.String())
}

func registryUTF16String(data []byte) (string, bool) {
	if len(data)%2 != 0 {
		return "", false
	}
	words := make([]uint16, len(data)/2)
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return strings.TrimRight(string(utf16.Decode(words)), "\x00"), true
}

func registryUTF16Strings(data []byte) ([]string, bool) {
	value, ok := registryUTF16String(data)
	if !ok {
		return nil, false
	}
	if value == "" {
		return nil, true
	}
	parts := strings.Split(value, "\x00")
	return parts, true
}

func registryValueTypeName(valueType uint32) string {
	switch valueType {
	case registry.NONE:
		return "REG_NONE"
	case registry.SZ:
		return "REG_SZ"
	case registry.EXPAND_SZ:
		return "REG_EXPAND_SZ"
	case registry.BINARY:
		return "REG_BINARY"
	case registry.DWORD:
		return "REG_DWORD"
	case registry.DWORD_BIG_ENDIAN:
		return "REG_DWORD_BIG_ENDIAN"
	case registry.LINK:
		return "REG_LINK"
	case registry.MULTI_SZ:
		return "REG_MULTI_SZ"
	case registry.RESOURCE_LIST:
		return "REG_RESOURCE_LIST"
	case registry.FULL_RESOURCE_DESCRIPTOR:
		return "REG_FULL_RESOURCE_DESCRIPTOR"
	case registry.RESOURCE_REQUIREMENTS_LIST:
		return "REG_RESOURCE_REQUIREMENTS_LIST"
	case registry.QWORD:
		return "REG_QWORD"
	default:
		return fmt.Sprintf("REG_%d", valueType)
	}
}

// registryValueTypeByName is the inverse of registryValueTypeName. It
// accepts every name that function can produce, including the "REG_<n>"
// fallback for a numeric type with no defined constant, so a value's own
// unmodified "Type:" line always round-trips.
func registryValueTypeByName(name string) (uint32, bool) {
	switch name {
	case "REG_NONE":
		return registry.NONE, true
	case "REG_SZ":
		return registry.SZ, true
	case "REG_EXPAND_SZ":
		return registry.EXPAND_SZ, true
	case "REG_BINARY":
		return registry.BINARY, true
	case "REG_DWORD":
		return registry.DWORD, true
	case "REG_DWORD_BIG_ENDIAN":
		return registry.DWORD_BIG_ENDIAN, true
	case "REG_LINK":
		return registry.LINK, true
	case "REG_MULTI_SZ":
		return registry.MULTI_SZ, true
	case "REG_RESOURCE_LIST":
		return registry.RESOURCE_LIST, true
	case "REG_FULL_RESOURCE_DESCRIPTOR":
		return registry.FULL_RESOURCE_DESCRIPTOR, true
	case "REG_RESOURCE_REQUIREMENTS_LIST":
		return registry.RESOURCE_REQUIREMENTS_LIST, true
	case "REG_QWORD":
		return registry.QWORD, true
	default:
		var n uint32
		if _, err := fmt.Sscanf(name, "REG_%d", &n); err == nil {
			return n, true
		}
		return 0, false
	}
}

type registryReader struct {
	mu     sync.Mutex
	data   []byte
	offset int64
	closed bool
}

func (r *registryReader) Size() int64 { return int64(len(r.data)) }

func (r *registryReader) Read(ctx context.Context, p []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.offset >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += int64(n)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *registryReader) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if off < 0 {
		return 0, os.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *registryReader) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

// registryValueWriter buffers the editor's full save of a value and commits
// it to the registry only once Close has the complete text, mirroring how
// other in-place VFS writers in this codebase (e.g. plugins/netfox's
// netfoxWriter) stage a whole-file save before validating and applying it.
type registryValueWriter struct {
	segments []string // full path, including the value's own segment
	buf      bytes.Buffer
	closed   bool
}

func (w *registryValueWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.buf.Write(p)
}

func (w *registryValueWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	parentSegments := w.segments[:len(w.segments)-1]
	name := registryValueName(w.segments[len(w.segments)-1])
	parent, owned, err := openRegistryKeyAccess(parentSegments, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if owned {
		defer parent.Close()
	}
	return writeRegistryValue(parent, name, w.buf.Bytes())
}

// writeRegistryValue parses the text formatRegistryValue produces (or a
// user's edit of it) and commits it with the one typed setter that matches
// the value's own type. golang.org/x/sys/windows/registry exposes no generic
// "set raw type+bytes" call, only SetStringValue, SetExpandStringValue,
// SetStringsValue, SetDWordValue, SetQWordValue and SetBinaryValue, so only
// REG_SZ, REG_EXPAND_SZ, REG_MULTI_SZ, REG_DWORD, REG_QWORD and REG_BINARY
// can be edited this way; every other type (including REG_DWORD_BIG_ENDIAN,
// which the read side already formats) is rejected with a specific error
// rather than silently attempted.
func writeRegistryValue(key registry.Key, name string, text []byte) error {
	valueType, lines, err := parseRegistryValueHeader(text)
	if err != nil {
		return err
	}
	switch valueType {
	case registry.SZ:
		s, err := parseRegistryStringValue(lines)
		if err != nil {
			return err
		}
		return key.SetStringValue(name, s)
	case registry.EXPAND_SZ:
		s, err := parseRegistryStringValue(lines)
		if err != nil {
			return err
		}
		return key.SetExpandStringValue(name, s)
	case registry.MULTI_SZ:
		values, err := parseRegistryMultiStringValue(lines)
		if err != nil {
			return err
		}
		return key.SetStringsValue(name, values)
	case registry.DWORD:
		n, err := parseRegistryIntValue(lines, 32)
		if err != nil {
			return err
		}
		return key.SetDWordValue(name, uint32(n))
	case registry.QWORD:
		n, err := parseRegistryIntValue(lines, 64)
		if err != nil {
			return err
		}
		return key.SetQWordValue(name, n)
	case registry.BINARY:
		data, err := parseRegistryBinaryValue(lines)
		if err != nil {
			return err
		}
		return key.SetBinaryValue(name, data)
	default:
		return fmt.Errorf("registry: editing %s values is not supported", registryValueTypeName(valueType))
	}
}

// parseRegistryValueHeader finds the "Type:" line formatRegistryValue always
// writes and returns the type it names together with every line of text, so
// each type-specific parser below can scan for the field it needs.
func parseRegistryValueHeader(text []byte) (uint32, []string, error) {
	normalized := strings.ReplaceAll(string(text), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for _, line := range lines {
		rest, ok := strings.CutPrefix(line, "Type: ")
		if !ok {
			continue
		}
		valueType, ok := registryValueTypeByName(strings.TrimSpace(rest))
		if !ok {
			return 0, nil, fmt.Errorf("registry: unrecognized type %q", rest)
		}
		return valueType, lines, nil
	}
	return 0, nil, errors.New("registry: missing Type: line")
}

// parseRegistryStringValue reads back a REG_SZ/REG_EXPAND_SZ value written
// by formatRegistryValue: the quoted "Value:" line it emits for a decodable
// UTF-16 string, or the hex "Data:" fallback it emits otherwise.
func parseRegistryStringValue(lines []string) (string, error) {
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "Value: "); ok {
			s, err := strconv.Unquote(rest)
			if err != nil {
				return "", fmt.Errorf("registry: invalid Value line: %w", err)
			}
			return s, nil
		}
		if rest, ok := strings.CutPrefix(line, "Data: "); ok {
			data, err := hex.DecodeString(strings.TrimSpace(rest))
			if err != nil {
				return "", fmt.Errorf("registry: invalid Data line: %w", err)
			}
			s, ok := registryUTF16String(data)
			if !ok {
				return "", errors.New("registry: Data is not a valid UTF-16 string")
			}
			return s, nil
		}
	}
	return "", errors.New("registry: missing Value/Data line")
}

// parseRegistryMultiStringValue reads back a REG_MULTI_SZ value: the
// "Value[i]:" lines formatRegistryValue emits in order for a non-empty list,
// the single "Value: \"\"" line it emits for an empty one, or the hex
// "Data:" fallback.
func parseRegistryMultiStringValue(lines []string) ([]string, error) {
	type indexedValue struct {
		index int
		value string
	}
	var indexed []indexedValue
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "Value["); ok {
			closeIdx := strings.Index(rest, "]: ")
			if closeIdx < 0 {
				continue
			}
			idx, err := strconv.Atoi(rest[:closeIdx])
			if err != nil {
				continue
			}
			s, err := strconv.Unquote(rest[closeIdx+len("]: "):])
			if err != nil {
				return nil, fmt.Errorf("registry: invalid Value[%d] line: %w", idx, err)
			}
			indexed = append(indexed, indexedValue{idx, s})
			continue
		}
		if rest, ok := strings.CutPrefix(line, "Value: "); ok && len(indexed) == 0 {
			s, err := strconv.Unquote(rest)
			if err != nil {
				return nil, fmt.Errorf("registry: invalid Value line: %w", err)
			}
			if s == "" {
				return []string{}, nil
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "Data: "); ok {
			data, err := hex.DecodeString(strings.TrimSpace(rest))
			if err != nil {
				return nil, fmt.Errorf("registry: invalid Data line: %w", err)
			}
			values, ok := registryUTF16Strings(data)
			if !ok {
				return nil, errors.New("registry: Data is not a valid UTF-16 multi-string")
			}
			return values, nil
		}
	}
	if len(indexed) == 0 {
		return nil, errors.New("registry: missing Value[]/Value/Data line")
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].index < indexed[j].index })
	result := make([]string, len(indexed))
	for i, entry := range indexed {
		result[i] = entry.value
	}
	return result, nil
}

// parseRegistryIntValue reads back a REG_DWORD/REG_QWORD value: the decimal
// count before the " (0x...)" annotation formatRegistryValue emits, or the
// little-endian hex "Data:" fallback for a value of the wrong byte width.
func parseRegistryIntValue(lines []string, bitSize int) (uint64, error) {
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "Value: "); ok {
			decimal := rest
			if sp := strings.IndexByte(rest, ' '); sp >= 0 {
				decimal = rest[:sp]
			}
			n, err := strconv.ParseUint(decimal, 10, bitSize)
			if err != nil {
				return 0, fmt.Errorf("registry: invalid Value line: %w", err)
			}
			return n, nil
		}
		if rest, ok := strings.CutPrefix(line, "Data: "); ok {
			data, err := hex.DecodeString(strings.TrimSpace(rest))
			if err != nil {
				return 0, fmt.Errorf("registry: invalid Data line: %w", err)
			}
			switch bitSize {
			case 32:
				if len(data) != 4 {
					return 0, errors.New("registry: Data must be 4 bytes for a DWORD")
				}
				return uint64(binary.LittleEndian.Uint32(data)), nil
			case 64:
				if len(data) != 8 {
					return 0, errors.New("registry: Data must be 8 bytes for a QWORD")
				}
				return binary.LittleEndian.Uint64(data), nil
			}
		}
	}
	return 0, errors.New("registry: missing Value/Data line")
}

// parseRegistryBinaryValue reads back a REG_BINARY value: the hex "Data:"
// line formatRegistryValue always emits for this type.
func parseRegistryBinaryValue(lines []string) ([]byte, error) {
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "Data: "); ok {
			data, err := hex.DecodeString(strings.TrimSpace(rest))
			if err != nil {
				return nil, fmt.Errorf("registry: invalid Data line: %w", err)
			}
			return data, nil
		}
	}
	return nil, errors.New("registry: missing Data line")
}
