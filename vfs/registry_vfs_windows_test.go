//go:build windows

package vfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

// newRegistryVFSTestKey creates a throwaway HKEY_CURRENT_USER key for a test
// and registers its cleanup, matching TestRegistryVFSReadsStringAndDefaultValues.
func newRegistryVFSTestKey(t *testing.T) (registry.Key, string, string) {
	t.Helper()
	keyPath := fmt.Sprintf(`Software\f4-registry-vfs-%d-%d`, time.Now().UnixNano(), os.Getpid())
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
	})
	keyURI := NewRegistryVFS().Join(registryVFSRoot, "HKEY_CURRENT_USER", "Software", strings.TrimPrefix(keyPath, `Software\`))
	return key, keyPath, keyURI
}

// utf16NulString encodes s as the raw little-endian UTF-16 bytes a REG_SZ or
// REG_MULTI_SZ value stores, including the terminating NUL word.
func utf16NulString(s string) []byte {
	words := utf16.Encode([]rune(s))
	words = append(words, 0)
	data := make([]byte, len(words)*2)
	for i, w := range words {
		binary.LittleEndian.PutUint16(data[i*2:], w)
	}
	return data
}

// createEditor opens path for editing through v.Create, writes text and
// closes it, returning the error Close reported (the point where the actual
// registry write happens).
func createEditor(t *testing.T, ctx context.Context, v *RegistryVFS, path string, text string) error {
	t.Helper()
	w, err := v.Create(ctx, path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.Write([]byte(text)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return w.Close()
}

func TestRegistryVFSPathRoundTrip(t *testing.T) {
	v := NewRegistryVFS()
	p := v.Join(registryVFSRoot, "HKEY_CURRENT_USER", "Software", "name/with slash")
	if want := "registry://HKEY_CURRENT_USER/Software/name%2Fwith%20slash"; p != want {
		t.Fatalf("Join = %q, want %q", p, want)
	}
	if got := v.Base(p); got != "name/with slash" {
		t.Fatalf("Base = %q, want original registry name", got)
	}
	if got := v.Dir(p); got != "registry://HKEY_CURRENT_USER/Software" {
		t.Fatalf("Dir = %q, want parent key", got)
	}
}

func TestRegistryVFSListsHivesAndIsReadOnly(t *testing.T) {
	ctx := context.Background()
	v := NewRegistryVFS()
	var items []VFSItem
	if err := v.ReadDir(ctx, registryVFSRoot, func(chunk []VFSItem) { items = append(items, chunk...) }); err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	if len(items) != len(registryHives) {
		t.Fatalf("root item count = %d, want %d: %#v", len(items), len(registryHives), items)
	}
	for _, hive := range registryHives {
		found := false
		for _, item := range items {
			if item.Name == hive.name && item.IsDir {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("root hive %q is missing", hive.name)
		}
	}
	if got := v.GetCapabilities(); !got.HasWrite || !got.HasRandomAccess {
		t.Fatalf("capabilities = %#v, want write-capable random access", got)
	}
	if err := v.MkDir(ctx, registryVFSRoot+"HKEY_CURRENT_USER/new"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("MkDir error = %v, want permission denied", err)
	}
	if err := v.Remove(ctx, registryVFSRoot+"HKEY_CURRENT_USER/Software"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Remove error = %v, want permission denied", err)
	}
	if err := v.Rename(ctx, registryVFSRoot+"HKEY_CURRENT_USER/Software", registryVFSRoot+"HKEY_CURRENT_USER/Renamed"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Rename error = %v, want permission denied", err)
	}
	// Create still refuses a value that does not exist: part 2 edits an
	// existing value's data and never creates a new one.
	if _, err := v.Create(ctx, registryVFSRoot+"HKEY_CURRENT_USER/new-value-that-does-not-exist"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Create error = %v, want not-exist for a missing value", err)
	}
	// Create still refuses a path that names an existing key, never a value.
	if _, err := v.Create(ctx, registryVFSRoot+"HKEY_CURRENT_USER/Software"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Create error = %v, want permission denied for a key path", err)
	}
}

func TestRegistryVFSReadsStringAndDefaultValues(t *testing.T) {
	ctx := context.Background()
	keyPath := fmt.Sprintf(`Software\f4-registry-vfs-%d`, time.Now().UnixNano())
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if err := key.SetStringValue("Greeting", "hello"); err != nil {
		key.Close()
		t.Fatalf("SetStringValue: %v", err)
	}
	if err := key.SetStringValue("", "default"); err != nil {
		key.Close()
		t.Fatalf("SetStringValue default: %v", err)
	}
	if err := key.Close(); err != nil {
		t.Fatalf("close test key: %v", err)
	}
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, keyPath) })

	v := NewRegistryVFS()
	keyURI := v.Join(registryVFSRoot, "HKEY_CURRENT_USER", "Software", strings.TrimPrefix(keyPath, `Software\`))
	var items []VFSItem
	if err := v.ReadDir(ctx, keyURI, func(chunk []VFSItem) { items = append(items, chunk...) }); err != nil {
		t.Fatalf("ReadDir test key: %v", err)
	}
	for _, name := range []string{"Greeting", registryDefaultValueName} {
		found := false
		for _, item := range items {
			if item.Name == name && !item.IsDir && item.SizeKnown {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("value %q is missing from %#v", name, items)
		}
	}

	valueURI := v.Join(keyURI, "Greeting")
	stat, err := v.Stat(ctx, valueURI)
	if err != nil {
		t.Fatalf("Stat value: %v", err)
	}
	if stat.IsDir || stat.Mode != "REG_SZ" || stat.Revision == "" {
		t.Fatalf("Stat value = %#v, want REG_SZ file with revision", stat)
	}
	reader, err := v.Open(ctx, valueURI)
	if err != nil {
		t.Fatalf("Open value: %v", err)
	}
	data := make([]byte, reader.Size())
	if _, err := reader.ReadAt(ctx, data, 0); err != nil && !errors.Is(err, io.EOF) {
		reader.Close()
		t.Fatalf("ReadAt value: %v", err)
	}
	reader.Close()
	text := string(data)
	if !strings.Contains(text, "Type: REG_SZ") || !strings.Contains(text, `Value: "hello"`) {
		t.Fatalf("value text = %q, want type and data", text)
	}

	defaultURI := v.Join(keyURI, registryDefaultValueName)
	defaultReader, err := v.Open(ctx, defaultURI)
	if err != nil {
		t.Fatalf("Open default value: %v", err)
	}
	defaultData := make([]byte, defaultReader.Size())
	_, _ = defaultReader.ReadAt(ctx, defaultData, 0)
	_ = defaultReader.Close()
	if !strings.Contains(string(defaultData), `Value: "default"`) {
		t.Fatalf("default value text = %q", defaultData)
	}
}

func TestRegistryVFSEditsStringValue(t *testing.T) {
	ctx := context.Background()
	key, keyPath, keyURI := newRegistryVFSTestKey(t)
	if err := key.SetStringValue("Greeting", "hello"); err != nil {
		key.Close()
		t.Fatalf("SetStringValue: %v", err)
	}
	key.Close()

	v := NewRegistryVFS()
	valueURI := v.Join(keyURI, "Greeting")
	newText := "Name: Greeting\nType: REG_SZ\nValue: \"goodbye\"\n"
	if err := createEditor(t, ctx, v, valueURI, newText); err != nil {
		t.Fatalf("edit Greeting: %v", err)
	}

	readKey, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("reopen test key: %v", err)
	}
	defer readKey.Close()
	got, _, err := readKey.GetStringValue("Greeting")
	if err != nil {
		t.Fatalf("GetStringValue: %v", err)
	}
	if got != "goodbye" {
		t.Fatalf("Greeting = %q, want %q", got, "goodbye")
	}
}

func TestRegistryVFSEditsDWordAndQwordValues(t *testing.T) {
	ctx := context.Background()
	key, keyPath, keyURI := newRegistryVFSTestKey(t)
	if err := key.SetDWordValue("Count", 1); err != nil {
		key.Close()
		t.Fatalf("SetDWordValue: %v", err)
	}
	if err := key.SetQWordValue("Big", 1); err != nil {
		key.Close()
		t.Fatalf("SetQWordValue: %v", err)
	}
	key.Close()

	v := NewRegistryVFS()
	if err := createEditor(t, ctx, v, v.Join(keyURI, "Count"), "Name: Count\nType: REG_DWORD\nValue: 42 (0x0000002A)\n"); err != nil {
		t.Fatalf("edit Count: %v", err)
	}
	if err := createEditor(t, ctx, v, v.Join(keyURI, "Big"), "Name: Big\nType: REG_QWORD\nValue: 9999999999 (0x00000002540BE3FF)\n"); err != nil {
		t.Fatalf("edit Big: %v", err)
	}

	readKey, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("reopen test key: %v", err)
	}
	defer readKey.Close()
	if count, _, err := readKey.GetIntegerValue("Count"); err != nil || count != 42 {
		t.Fatalf("Count = %v, %v, want 42, nil", count, err)
	}
	if big, _, err := readKey.GetIntegerValue("Big"); err != nil || big != 9999999999 {
		t.Fatalf("Big = %v, %v, want 9999999999, nil", big, err)
	}
}

func TestRegistryVFSEditsMultiStringValue(t *testing.T) {
	ctx := context.Background()
	key, keyPath, keyURI := newRegistryVFSTestKey(t)
	if err := key.SetStringsValue("List", []string{"a"}); err != nil {
		key.Close()
		t.Fatalf("SetStringsValue: %v", err)
	}
	key.Close()

	v := NewRegistryVFS()
	newText := "Name: List\nType: REG_MULTI_SZ\nValue[0]: \"one\"\nValue[1]: \"two\"\n"
	if err := createEditor(t, ctx, v, v.Join(keyURI, "List"), newText); err != nil {
		t.Fatalf("edit List: %v", err)
	}

	readKey, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("reopen test key: %v", err)
	}
	defer readKey.Close()
	got, _, err := readKey.GetStringsValue("List")
	if err != nil {
		t.Fatalf("GetStringsValue: %v", err)
	}
	if want := []string{"one", "two"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("List = %#v, want %#v", got, want)
	}
}

func TestRegistryVFSEditsBinaryValue(t *testing.T) {
	ctx := context.Background()
	key, keyPath, keyURI := newRegistryVFSTestKey(t)
	if err := key.SetBinaryValue("Blob", []byte{0x01, 0x02}); err != nil {
		key.Close()
		t.Fatalf("SetBinaryValue: %v", err)
	}
	key.Close()

	v := NewRegistryVFS()
	newText := "Name: Blob\nType: REG_BINARY\nData: deadbeef\n"
	if err := createEditor(t, ctx, v, v.Join(keyURI, "Blob"), newText); err != nil {
		t.Fatalf("edit Blob: %v", err)
	}

	readKey, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("reopen test key: %v", err)
	}
	defer readKey.Close()
	got, _, err := readKey.GetBinaryValue("Blob")
	if err != nil {
		t.Fatalf("GetBinaryValue: %v", err)
	}
	if want := []byte{0xde, 0xad, 0xbe, 0xef}; !bytes.Equal(got, want) {
		t.Fatalf("Blob = %x, want %x", got, want)
	}
}

func TestRegistryVFSEditRejectsMalformedText(t *testing.T) {
	ctx := context.Background()
	key, keyPath, keyURI := newRegistryVFSTestKey(t)
	if err := key.SetStringValue("Greeting", "hello"); err != nil {
		key.Close()
		t.Fatalf("SetStringValue: %v", err)
	}
	key.Close()

	v := NewRegistryVFS()
	// A Value line with no closing quote fails strconv.Unquote.
	err := createEditor(t, ctx, v, v.Join(keyURI, "Greeting"), "Name: Greeting\nType: REG_SZ\nValue: \"unterminated\n")
	if err == nil {
		t.Fatal("edit with malformed Value line: want error, got nil")
	}

	readKey, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("reopen test key: %v", err)
	}
	defer readKey.Close()
	got, _, err := readKey.GetStringValue("Greeting")
	if err != nil || got != "hello" {
		t.Fatalf("Greeting = %q, %v, want unchanged %q, nil", got, err, "hello")
	}
}

func TestRegistryVFSEditRejectsUnsupportedType(t *testing.T) {
	// golang.org/x/sys/windows/registry exposes no generic setter for
	// REG_DWORD_BIG_ENDIAN (or REG_NONE, REG_LINK, and the resource-list
	// types), so writeRegistryValue must refuse them before touching the
	// registry at all -- a zero Key value here would panic if it tried.
	err := writeRegistryValue(0, "Legacy", []byte("Name: Legacy\nType: REG_DWORD_BIG_ENDIAN\nValue: 1 (0x00000001)\n"))
	if err == nil {
		t.Fatal("writeRegistryValue REG_DWORD_BIG_ENDIAN: want error, got nil")
	}
	if !strings.Contains(err.Error(), "REG_DWORD_BIG_ENDIAN") {
		t.Fatalf("writeRegistryValue error = %v, want it to name the unsupported type", err)
	}
}

func TestRegistryValueTypeNameRoundTrip(t *testing.T) {
	types := []uint32{
		registry.NONE, registry.SZ, registry.EXPAND_SZ, registry.BINARY,
		registry.DWORD, registry.DWORD_BIG_ENDIAN, registry.LINK,
		registry.MULTI_SZ, registry.RESOURCE_LIST,
		registry.FULL_RESOURCE_DESCRIPTOR, registry.RESOURCE_REQUIREMENTS_LIST,
		registry.QWORD, 12345,
	}
	for _, want := range types {
		name := registryValueTypeName(want)
		got, ok := registryValueTypeByName(name)
		if !ok || got != want {
			t.Errorf("registryValueTypeByName(%q) = %v, %v, want %v, true", name, got, ok, want)
		}
	}
}

// TestParseRegistryStringValueDataLine covers editing through a "Data:"
// line instead of a quoted "Value:" line. formatRegistryValue itself only
// falls back to "Data:" for SZ/EXPAND_SZ when the raw bytes have an odd
// length (not valid UTF-16 at all, so not round-trippable either way), but
// a "Data:" line with well-formed UTF-16 hex is still an accepted, more
// direct way to write a string value.
func TestParseRegistryStringValueDataLine(t *testing.T) {
	data := utf16NulString("fallback")
	text := []byte("Name: Greeting\nType: REG_SZ\nData: " + hex.EncodeToString(data) + "\n")
	_, lines, err := parseRegistryValueHeader(text)
	if err != nil {
		t.Fatalf("parseRegistryValueHeader: %v", err)
	}
	got, err := parseRegistryStringValue(lines)
	if err != nil {
		t.Fatalf("parseRegistryStringValue: %v", err)
	}
	if got != "fallback" {
		t.Fatalf("parseRegistryStringValue = %q, want %q", got, "fallback")
	}
}

func TestParseRegistryMultiStringValueEmpty(t *testing.T) {
	text := formatRegistryValue("List", registry.MULTI_SZ, nil)
	_, lines, err := parseRegistryValueHeader(text)
	if err != nil {
		t.Fatalf("parseRegistryValueHeader: %v", err)
	}
	got, err := parseRegistryMultiStringValue(lines)
	if err != nil {
		t.Fatalf("parseRegistryMultiStringValue: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("parseRegistryMultiStringValue = %#v, want empty", got)
	}
}
