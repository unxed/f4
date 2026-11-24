package vfs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/unxed/vtui"
)

// ErrViewerUnsupported tells the host that a provider declined a file after
// selection. The next matching provider is tried; if all providers decline,
// the caller can open f4's ordinary viewer.
var ErrViewerUnsupported = errors.New("viewer provider: unsupported")

// ViewerRequest identifies the selected file. VFS is borrowed from the host:
// providers may read through it but must not change its current path or close it.
// Item is the panel's immutable metadata snapshot for the selection.
type ViewerRequest struct {
	VFS     VFS
	HostVFS ViewerHostVFS
	Path    string
	Item    VFSItem
	Panel   ViewerPanelSnapshot
}

// ViewerHostVFS lets a viewer provider read another file addressed by the
// host's URI namespace. The returned handle supports random access, reports
// its size, and is closed by the provider. Passing the request context to
// OpenFile and ReadAt lets the host cancel work when it cancels that context.
type ViewerHostVFS interface {
	OpenFile(context.Context, string) (ReadAtCloser, error)
}

type viewerHostVFS struct{ source VFS }

func (host viewerHostVFS) OpenFile(ctx context.Context, uri string) (ReadAtCloser, error) {
	return host.source.Open(ctx, uri)
}

// NewViewerHostVFS exposes the source panel's VFS through a read-only viewer
// capability. The provider can open panel URIs without receiving mutation
// methods or changing the VFS's current directory.
func NewViewerHostVFS(source VFS) ViewerHostVFS {
	if source == nil {
		return nil
	}
	return viewerHostVFS{source: source}
}

// ViewerPanelItem is one entry in the source panel's captured display order.
// URI identifies the entry in the host's VFS namespace; Item is its metadata
// snapshot at the time the viewer was opened.
type ViewerPanelItem struct {
	URI  string
	Item VFSItem
}

// ViewerPanelSnapshot is an immutable snapshot of the panel that opened the
// viewer. Providers must not retain or mutate its slices. URIs, rather than
// row indexes, identify entries so later panel navigation cannot change the
// meaning of the captured cursor or selection.
type ViewerPanelSnapshot struct {
	Items       []ViewerPanelItem
	CursorURI   string
	SelectedURI []string
}

// NewViewerPanelSnapshot copies the supplied slices so the caller can safely
// continue using its panel-owned buffers after opening a viewer.
func NewViewerPanelSnapshot(items []ViewerPanelItem, cursorURI string, selectedURI []string) ViewerPanelSnapshot {
	return ViewerPanelSnapshot{
		Items:       append([]ViewerPanelItem(nil), items...),
		CursorURI:   cursorURI,
		SelectedURI: append([]string(nil), selectedURI...),
	}
}

// ViewerProvider contributes a full-screen viewer for files it recognises.
// Name is a stable, case-insensitive registry identifier; higher priorities
// are tried first. CanView must be local, bounded and free of VFS I/O. OpenViewer
// runs on the UI goroutine and should construct its frame quickly. Returning
// ErrViewerUnsupported declines this file and allows the next provider or the
// ordinary f4 viewer to handle it; any other error is returned to the caller.
type ViewerProvider interface {
	Name() string
	Priority() int
	CanView(ViewerRequest) bool
	OpenViewer(context.Context, ViewerRequest) (vtui.Frame, error)
}

type viewerProviderEntry struct {
	id       uint64
	key      string
	provider ViewerProvider
}

var viewerProviders = struct {
	sync.RWMutex
	nextID uint64
	items  []viewerProviderEntry
}{}

type viewerProviderRegistration struct {
	once sync.Once
	id   uint64
}

func (registration *viewerProviderRegistration) Unregister() {
	if registration == nil {
		return
	}
	registration.once.Do(func() {
		viewerProviders.Lock()
		defer viewerProviders.Unlock()
		for i, entry := range viewerProviders.items {
			if entry.id == registration.id {
				viewerProviders.items = append(viewerProviders.items[:i], viewerProviders.items[i+1:]...)
				return
			}
		}
	})
}

// RegisterViewerProvider installs one provider. Names are trimmed and
// compared case-insensitively to prevent duplicate handlers after reload.
func RegisterViewerProvider(provider ViewerProvider) (Registration, error) {
	if isNilViewerProvider(provider) {
		return nil, fmt.Errorf("register viewer provider: nil provider")
	}
	name := provider.Name()
	key := viewerProviderKey(name)
	if key == "" {
		return nil, fmt.Errorf("register viewer provider: empty name")
	}

	viewerProviders.Lock()
	defer viewerProviders.Unlock()
	for _, entry := range viewerProviders.items {
		if entry.key == key {
			return nil, fmt.Errorf("register viewer provider: name %q is already registered", name)
		}
	}
	viewerProviders.nextID++
	id := viewerProviders.nextID
	viewerProviders.items = append(viewerProviders.items, viewerProviderEntry{id: id, key: key, provider: provider})
	return &viewerProviderRegistration{id: id}, nil
}

// ViewerProvidersFor returns a stable-priority snapshot of providers that
// cheaply claim req. Registry locks are released before plugin code is called.
func ViewerProvidersFor(req ViewerRequest) []ViewerProvider {
	viewerProviders.RLock()
	entries := append([]viewerProviderEntry(nil), viewerProviders.items...)
	viewerProviders.RUnlock()

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].provider.Priority() > entries[j].provider.Priority()
	})

	providers := make([]ViewerProvider, 0, len(entries))
	for _, entry := range entries {
		if entry.provider.CanView(req) {
			providers = append(providers, entry.provider)
		}
	}
	return providers
}

// OpenViewerFromProviders tries matching providers in priority order. A
// provider that returns ErrViewerUnsupported declines the file; other errors
// stop dispatch so the caller can report them rather than silently hiding a
// broken plugin. handled is false only when every provider declined or none
// matched, which is the signal to use f4's normal viewer.
func OpenViewerFromProviders(ctx context.Context, req ViewerRequest) (frame vtui.Frame, handled bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, provider := range ViewerProvidersFor(req) {
		frame, err = provider.OpenViewer(ctx, req)
		if errors.Is(err, ErrViewerUnsupported) {
			continue
		}
		if err != nil {
			return nil, true, fmt.Errorf("open viewer with %q: %w", provider.Name(), err)
		}
		if frame == nil {
			return nil, true, fmt.Errorf("open viewer with %q: provider returned a nil frame", provider.Name())
		}
		return frame, true, nil
	}
	return nil, false, nil
}

func viewerProviderKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func isNilViewerProvider(provider ViewerProvider) bool {
	if provider == nil {
		return true
	}
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
