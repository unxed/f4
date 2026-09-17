package cloudfox

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/unxed/f4/vfs"
)

type coverageCloudBackend struct {
	*fakeBackend
	copied       [][2]string
	renamed      [][2]string
	removed      []string
	madeDirs     []string
	attributes   []string
	canonical    map[string]string
	panelInfo    vfs.PanelInfoSnapshot
	refreshError error
	reader       vfs.ReadAtCloser
}

func (b *coverageCloudBackend) MkDir(_ context.Context, location string) error {
	b.madeDirs = append(b.madeDirs, location)
	return nil
}

func (b *coverageCloudBackend) Remove(_ context.Context, location string) error {
	b.removed = append(b.removed, location)
	return nil
}

func (b *coverageCloudBackend) Rename(_ context.Context, oldLocation, newLocation string) error {
	b.renamed = append(b.renamed, [2]string{oldLocation, newLocation})
	return nil
}

func (b *coverageCloudBackend) Copy(_ context.Context, oldLocation, newLocation string) error {
	b.copied = append(b.copied, [2]string{oldLocation, newLocation})
	return nil
}

func (b *coverageCloudBackend) SetAttributes(_ context.Context, location string, _ vfs.VFSItem) error {
	b.attributes = append(b.attributes, location)
	return nil
}

func (b *coverageCloudBackend) Open(ctx context.Context, location string) (vfs.ReadAtCloser, error) {
	if b.reader != nil {
		return b.reader, nil
	}
	return b.fakeBackend.Open(ctx, location)
}

func (b *coverageCloudBackend) CanonicalLocation(location string) string {
	return b.canonical[location]
}

func (*coverageCloudBackend) TransferName(string) string { return "remote-name.txt" }

func (*coverageCloudBackend) IntraSessionTransferName(string) string { return "native-name" }

func (*coverageCloudBackend) PanelInfoKey(req vfs.PanelInfoRequest) string {
	return "provider:" + req.Path + ":" + req.SelectedName
}

func (b *coverageCloudBackend) CachedPanelInfo(vfs.PanelInfoRequest) (vfs.PanelInfoSnapshot, bool) {
	return b.panelInfo, true
}

func (b *coverageCloudBackend) RefreshPanelInfo(ctx context.Context, _ vfs.PanelInfoRequest) (vfs.PanelInfoSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return vfs.PanelInfoSnapshot{}, err
	}
	if b.refreshError != nil {
		return vfs.PanelInfoSnapshot{}, b.refreshError
	}
	return b.panelInfo, nil
}

type coverageLocalReader struct{ *contextCheckingReader }

func (*coverageLocalReader) LocalPath() (string, bool) { return "/cache/cloud-object", true }

func TestCloudVFSCoverageHelpersAndHandles(t *testing.T) {
	if got := visualPathParts(`one\\two/../three/.`); len(got) != 2 || got[0] != "one" || got[1] != "three" {
		t.Fatalf("visualPathParts = %#v", got)
	}
	if got := applyVisualPathParts([]string{"base"}, `../child`); len(got) != 1 || got[0] != "child" {
		t.Fatalf("applyVisualPathParts = %#v", got)
	}
	if got := applyVisualPathParts(nil, `../../child`); len(got) != 1 || got[0] != "child" {
		t.Fatalf("root-clamped visual path = %#v", got)
	}
	if got := aliasKey("parent", "name"); got != "parent\x00name" {
		t.Fatalf("aliasKey = %q", got)
	}
	for _, raw := range []string{"", ".", "..", "a/b", "a\\b", "bad\x00name", "line\nname"} {
		if got := safeCloudPanelName(raw, "/opaque/"+raw); got == "" || got == "." || got == ".." {
			t.Fatalf("unsafe panel name %q -> %q", raw, got)
		}
	}
	firstHash, secondHash := stableLocationHash("/same"), stableLocationHash("/same")
	if len(firstHash) != 16 || firstHash != secondHash {
		t.Fatal("stable location hash is not deterministic")
	}
	if err := validateShareURL("https://example.test/share"); err != nil {
		t.Fatal(err)
	}
	if err := validateShareURL("not a URL"); err == nil {
		t.Fatal("invalid share URL was accepted")
	}
	if err := validateHTTPSShareURL("http://example.test/share"); err == nil {
		t.Fatal("HTTP share URL was accepted by HTTPS validator")
	}

	backend := &coverageCloudBackend{
		fakeBackend: &fakeBackend{},
		canonical:   map[string]string{"/root/created": "/objects/created"},
		panelInfo:   vfs.PanelInfoSnapshot{Authoritative: true},
	}
	cloud := testCloudVFS(t, backend)
	defer func() { _ = cloud.Close() }()
	root := cloud.GetPath()
	if !cloud.IsAtRoot() || !cloud.RemoteTransfer() || cloud.GetTitle() != "Test" {
		t.Fatalf("root/title contract failed: root=%v transfer=%v title=%q", cloud.IsAtRoot(), cloud.RemoteTransfer(), cloud.GetTitle())
	}
	if got := cloud.PanelTitle("not-a-cloud-path"); got != root {
		t.Fatalf("fallback PanelTitle = %q, want %q", got, root)
	}
	if got := cloud.PanelInfoKey(vfs.PanelInfoRequest{Path: root, SelectedName: "item"}); got != "provider:"+root+":item" {
		t.Fatalf("provider PanelInfoKey = %q", got)
	}
	if got, fresh := cloud.CachedPanelInfo(vfs.PanelInfoRequest{}); !fresh || !got.Authoritative {
		t.Fatalf("CachedPanelInfo = %#v, %v", got, fresh)
	}
	if got, err := cloud.RefreshPanelInfo(context.Background(), vfs.PanelInfoRequest{}); err != nil || !got.Authoritative {
		t.Fatalf("RefreshPanelInfo = %#v, %v", got, err)
	}
	if got := cloud.GetCapabilities(); got != (vfs.VFSCapabilities{}) {
		t.Fatalf("GetCapabilities = %#v", got)
	}
	if _, err := cloud.Search(context.Background(), "name", "needle"); err == nil {
		t.Fatal("unsupported Search returned nil error")
	}
	if parent, ok := cloud.ParentVFS().(*ManagerVFS); !ok || parent != nil {
		t.Fatalf("ParentVFS = %#v, want typed nil manager", cloud.ParentVFS())
	}

	if err := cloud.MkDir(context.Background(), cloud.Join(root, "created")); err != nil {
		t.Fatal(err)
	}
	if err := cloud.Rename(context.Background(), cloud.Join(root, "created"), cloud.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	if err := cloud.Copy(context.Background(), cloud.Join(root, "renamed"), cloud.Join(root, "copy")); err != nil {
		t.Fatal(err)
	}
	if err := cloud.SetAttributes(context.Background(), cloud.Join(root, "copy"), vfs.VFSItem{Mode: "0755"}); err != nil {
		t.Fatal(err)
	}
	if err := cloud.Remove(context.Background(), cloud.Join(root, "copy")); err != nil {
		t.Fatal(err)
	}
	if len(backend.madeDirs) != 1 || len(backend.renamed) != 1 || len(backend.copied) != 1 || len(backend.attributes) != 1 || len(backend.removed) != 1 {
		t.Fatalf("recorded operations: mkdir=%v rename=%v copy=%v attrs=%v remove=%v", backend.madeDirs, backend.renamed, backend.copied, backend.attributes, backend.removed)
	}
	if got := cloud.currentLocation(); got != "/root" {
		t.Fatalf("current location changed by file operations: %q", got)
	}

	backend.reader = nil
	reader, err := cloud.Open(context.Background(), cloud.Join(root, "read.txt"))
	if err != os.ErrPermission {
		t.Fatalf("Open error = %v, want permission error", err)
	}
	if reader != nil {
		t.Fatal("failed Open returned a reader")
	}
	backend.fakeBackend = &fakeBackend{}
	backend.reader = &coverageLocalReader{contextCheckingReader: &contextCheckingReader{data: []byte("hello")}}
	reader, err = cloud.Open(context.Background(), cloud.Join(root, "read.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if local, ok := reader.(interface{ LocalPath() (string, bool) }); !ok || func() bool { p, yes := local.LocalPath(); return yes && p == "/cache/cloud-object" }() == false {
		t.Fatal("Open reader did not preserve LocalPath")
	}
	if n, err := reader.Read(context.Background(), make([]byte, 5)); err != nil || n != 5 {
		t.Fatalf("Read = %d, %v", n, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := cloud.Create(context.Background(), cloud.Join(root, "created"))
	if err != nil {
		t.Fatal(err)
	}
	if managed, ok := writer.(vfs.ManagedTransferWriter); ok && managed.TransferProgressManaged() {
		t.Fatal("fake writer unexpectedly manages transfer progress")
	}
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	cloud.connection.Provider = ProviderYandexDisk
	if cloud.ManagedTransferWrites() {
		// Expected for staged providers; restore below before testing the URI path.
	} else {
		t.Fatal("Yandex writes are not managed")
	}
	cloud.connection.Provider = ProviderGoogleDrive
	if !cloud.IsAbs(root) || !cloud.IsAbs(cloud.canonicalURI("/root/file")) || cloud.IsAbs("other:/file") {
		t.Fatal("IsAbs contract failed")
	}
	if got := cloud.Base(root); got != "Test" || cloud.Dir(root) != root {
		t.Fatalf("root Base/Dir = %q/%q", got, cloud.Dir(root))
	}
	if got, err := cloud.Abs("."); err != nil || got != root {
		t.Fatalf("Abs(.) = %q, %v", got, err)
	}
	if err := cloud.SetPathOptimistic(root); err != nil {
		t.Fatal(err)
	}
}

func TestCloudVFSOptionalCapabilitiesAndFallbacks(t *testing.T) {
	plain := testCloudVFS(t, &fakeBackend{})
	if got := plain.PanelInfoKey(vfs.PanelInfoRequest{Path: "root"}); got != "cloudfox:"+testConnectionID+":root" {
		t.Fatalf("fallback PanelInfoKey = %q", got)
	}
	if got, fresh := plain.CachedPanelInfo(vfs.PanelInfoRequest{}); !fresh || !got.Authoritative {
		t.Fatalf("fallback CachedPanelInfo = %#v, %v", got, fresh)
	}
	if got, err := plain.RefreshPanelInfo(context.Background(), vfs.PanelInfoRequest{}); err != nil || !got.Authoritative {
		t.Fatalf("fallback RefreshPanelInfo = %#v, %v", got, err)
	}
	if got := plain.TransferName(plain.GetPath(), nil); got != "Test" {
		t.Fatalf("fallback TransferName = %q", got)
	}
	if err := plain.Copy(context.Background(), plain.GetPath(), plain.Join(plain.GetPath(), "copy")); err == nil {
		t.Fatal("copy without BackendCopier succeeded")
	}
	if got := wrapCloudVFS(plain); got != plain {
		t.Fatal("plain backend unexpectedly wrapped with trash support")
	}
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}
	if got := plain.GetPath(); got != "Test:"+string(os.PathSeparator) {
		t.Fatalf("closed GetPath = %q", got)
	}

	trash := testCloudVFS(t, &fakeTrashBackend{fakeBackend: &fakeBackend{}})
	wrapped, ok := wrapCloudVFS(trash).(*trashCloudVFS)
	if !ok {
		t.Fatal("trash backend was not wrapped")
	}
	if err := wrapped.MoveToTrash(context.Background(), wrapped.GetPath()); err != nil {
		t.Fatal(err)
	}
	if cloned, ok := wrapped.Clone().(*trashCloudVFS); !ok || cloned == wrapped {
		t.Fatalf("trash Clone = %T %p", wrapped.Clone(), wrapped.Clone())
	} else {
		_ = cloned.Close()
	}
	if err := wrapped.Close(); err != nil {
		t.Fatal(err)
	}

	backend := &coverageCloudBackend{
		fakeBackend: &fakeBackend{}, canonical: map[string]string{},
		panelInfo: vfs.PanelInfoSnapshot{Authoritative: true},
	}
	cloud := testCloudVFS(t, backend)
	clone := cloud.Clone()
	if got := cloud.TransferName(cloud.GetPath(), clone); got != "native-name" {
		t.Fatalf("same-session TransferName = %q", got)
	}
	if got := cloud.TransferName(cloud.GetPath(), nil); got != "remote-name.txt" {
		t.Fatalf("provider TransferName = %q", got)
	}
	_ = clone.Close()
	_ = cloud.Close()

	noMap := &coverageCloudBackend{
		fakeBackend: &fakeBackend{}, canonical: map[string]string{},
		panelInfo: vfs.PanelInfoSnapshot{Authoritative: true}, refreshError: errors.New("refresh failed"),
	}
	cloud = testCloudVFS(t, noMap)
	if _, err := cloud.RefreshPanelInfo(context.Background(), vfs.PanelInfoRequest{}); err == nil {
		t.Fatal("provider refresh error was swallowed")
	}
	_ = cloud.Close()
}

func TestNewCloudVFSEmptySessionAndHandleBranches(t *testing.T) {
	pool := newSessionPool()
	ready := make(chan struct{})
	close(ready)
	if _, err := newCloudVFS(Connection{Name: "empty"}, nil, &pooledSession{pool: pool, ready: ready}, "/root"); err == nil {
		t.Fatal("newCloudVFS accepted an unavailable backend")
	}
	var finishes int
	read := &cloudReadHandle{ReadAtCloser: &contextCheckingReader{data: []byte("x")}, finish: func() { finishes++ }}
	if local, ok := read.LocalPath(); ok || local != "" {
		t.Fatalf("reader without LocalPath = %q, %v", local, ok)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	_ = read.Close()
	if finishes != 1 {
		t.Fatalf("reader finish count = %d", finishes)
	}
	var abortFinishes int
	write := &cloudWriteHandle{WriteCloser: &nopWriteCloser{Writer: io.Discard}, finish: func() { abortFinishes++ }}
	if err := write.Abort(); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("unsupported Abort = %v", err)
	}
	_ = write.Abort()
	if abortFinishes != 1 {
		t.Fatalf("abort finish count = %d", abortFinishes)
	}
}
