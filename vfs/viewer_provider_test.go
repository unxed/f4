package vfs

import (
	"context"
	"errors"
	"testing"

	"github.com/unxed/vtui"
)

type testViewerProvider struct {
	name     string
	priority int
	canView  bool
	frame    vtui.Frame
	openErr  error
	opened   int
}

func (provider *testViewerProvider) Name() string               { return provider.name }
func (provider *testViewerProvider) Priority() int              { return provider.priority }
func (provider *testViewerProvider) CanView(ViewerRequest) bool { return provider.canView }
func (provider *testViewerProvider) OpenViewer(context.Context, ViewerRequest) (vtui.Frame, error) {
	provider.opened++
	return provider.frame, provider.openErr
}

func TestViewerProvidersForPriorityAndUnregister(t *testing.T) {
	low := &testViewerProvider{name: "viewer-registry-low-test", priority: 1, canView: true}
	high := &testViewerProvider{name: "viewer-registry-high-test", priority: 2, canView: true}
	declined := &testViewerProvider{name: "viewer-registry-declined-test", priority: 3}
	registrations := make([]Registration, 0, 3)
	for _, provider := range []*testViewerProvider{low, high, declined} {
		registration, err := RegisterViewerProvider(provider)
		if err != nil {
			t.Fatalf("RegisterViewerProvider(%q): %v", provider.name, err)
		}
		registrations = append(registrations, registration)
	}
	t.Cleanup(func() {
		for _, registration := range registrations {
			registration.Unregister()
		}
	})

	got := ViewerProvidersFor(ViewerRequest{})
	if len(got) < 2 || got[0] != high || got[1] != low {
		t.Fatalf("ViewerProvidersFor() = %#v, want high-priority then low-priority matching providers", got)
	}
}

func TestViewerProviderRegistrationRejectsNilEmptyAndDuplicate(t *testing.T) {
	if _, err := RegisterViewerProvider(nil); err == nil {
		t.Fatal("RegisterViewerProvider(nil) succeeded")
	}
	if _, err := RegisterViewerProvider(&testViewerProvider{name: "  "}); err == nil {
		t.Fatal("RegisterViewerProvider(empty name) succeeded")
	}
	first := &testViewerProvider{name: "viewer-registry-duplicate-test"}
	registration, err := RegisterViewerProvider(first)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)
	if _, err := RegisterViewerProvider(&testViewerProvider{name: " VIEWER-REGISTRY-DUPLICATE-TEST "}); err == nil {
		t.Fatal("case-insensitive duplicate registration succeeded")
	}
}

func TestOpenViewerFromProvidersFallsBackAfterDeclines(t *testing.T) {
	declines := &testViewerProvider{
		name: "viewer-decline-test", priority: 2, canView: true, openErr: ErrViewerUnsupported,
	}
	broken := &testViewerProvider{
		name: "viewer-broken-test", priority: 1, canView: true, openErr: ErrViewerUnsupported,
	}
	registrations := make([]Registration, 0, 2)
	for _, provider := range []*testViewerProvider{declines, broken} {
		registration, err := RegisterViewerProvider(provider)
		if err != nil {
			t.Fatal(err)
		}
		registrations = append(registrations, registration)
	}
	t.Cleanup(func() {
		for _, registration := range registrations {
			registration.Unregister()
		}
	})

	frame, handled, err := OpenViewerFromProviders(context.Background(), ViewerRequest{})
	if err != nil || handled || frame != nil {
		t.Fatalf("OpenViewerFromProviders() = (%v, %v, %v), want (nil, false, nil)", frame, handled, err)
	}
	if declines.opened != 1 || broken.opened != 1 {
		t.Fatalf("providers opened %d and %d times, want each once", declines.opened, broken.opened)
	}
}

func TestOpenViewerFromProvidersReturnsNonUnsupportedError(t *testing.T) {
	wantErr := errors.New("decode failed")
	provider := &testViewerProvider{name: "viewer-error-test", canView: true, openErr: wantErr}
	registration, err := RegisterViewerProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	frame, handled, err := OpenViewerFromProviders(context.Background(), ViewerRequest{})
	if frame != nil || !handled || !errors.Is(err, wantErr) {
		t.Fatalf("OpenViewerFromProviders() = (%v, %v, %v), want (nil, true, wrapped decode error)", frame, handled, err)
	}
}

func TestOpenViewerFromProvidersReturnsOpenedFrame(t *testing.T) {
	wantFrame := vtui.NewMarkdownView("test", "")
	provider := &testViewerProvider{name: "viewer-open-test", canView: true, frame: wantFrame}
	registration, err := RegisterViewerProvider(provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Unregister)

	frame, handled, err := OpenViewerFromProviders(context.Background(), ViewerRequest{})
	if err != nil || !handled || frame != wantFrame {
		t.Fatalf("OpenViewerFromProviders() = (%v, %v, %v), want (test frame, true, nil)", frame, handled, err)
	}
}

func TestNewViewerPanelSnapshotCopiesPanelSlices(t *testing.T) {
	items := []ViewerPanelItem{{URI: "archive://one", Item: VFSItem{Name: "one"}}}
	selected := []string{"archive://one"}
	snapshot := NewViewerPanelSnapshot(items, "archive://one", selected)

	items[0].URI = "changed"
	selected[0] = "changed"
	if snapshot.Items[0].URI != "archive://one" || snapshot.SelectedURI[0] != "archive://one" {
		t.Fatalf("snapshot changed with caller slices: %#v", snapshot)
	}
}

type viewerHostTestVFS struct {
	VFS
	opened string
}

func (filesystem *viewerHostTestVFS) Open(_ context.Context, path string) (ReadAtCloser, error) {
	filesystem.opened = path
	return nil, nil
}

func TestViewerHostVFSPreservesHostURI(t *testing.T) {
	filesystem := &viewerHostTestVFS{}
	host := NewViewerHostVFS(filesystem)
	if _, err := host.OpenFile(context.Background(), "archive://drive/image.png"); err != nil {
		t.Fatal(err)
	}
	if filesystem.opened != "archive://drive/image.png" {
		t.Fatalf("OpenFile path = %q", filesystem.opened)
	}
}
