package vfs

import (
	"context"
	"testing"
)

// One refused password ends elevation for the rest of that operation, however
// long it runs, and only for it: the next operation asks again (f4#1998).
func TestRefusedPasswordEndsElevationForItsOperationOnly(t *testing.T) {
	old := globalSudoClient
	t.Cleanup(func() { globalSudoClient = old })
	globalSudoClient = &SudoClient{}

	op := WithElevationScope(context.Background())
	if !ElevationAllowed(op) {
		t.Fatal("a new operation must be allowed to ask")
	}
	before := globalSudoClient.declines.Load()
	globalSudoClient.declines.Add(1) // the user cancelled the password dialog
	noteElevationRefusal(op, before)

	if ElevationAllowed(op) {
		t.Error("the operation asked again after the password was refused")
	}
	if !ElevationAllowed(WithElevationScope(context.Background())) {
		t.Error("the next operation must ask once more")
	}
	if !ElevationAllowed(context.Background()) {
		t.Error("a call outside any operation must not be silenced")
	}

	// A refusal that happened before the call started is not this call's.
	other := WithElevationScope(context.Background())
	noteElevationRefusal(other, globalSudoClient.declines.Load())
	if !ElevationAllowed(other) {
		t.Error("an older refusal silenced an operation that was never asked")
	}
}

// A scope is kept, not replaced, when ctx already has one: several scans
// started by one command stay one operation.
func TestElevationScopeIsKeptByNestedCalls(t *testing.T) {
	op := WithElevationScope(context.Background())
	if elevationScopeOf(WithElevationScope(op)) != elevationScopeOf(op) {
		t.Fatal("a nested WithElevationScope started a new operation")
	}
}

// Separate top-level size counts are separate operations: refusing elevation
// in one must not suppress the prompt for the next one (f4#1998).
func TestSeparateSizeCountsHaveIndependentElevationScopes(t *testing.T) {
	old := globalSudoClient
	globalSudoClient = &SudoClient{}
	t.Cleanup(func() { globalSudoClient = old })

	v := &scopeRecordingVFS{VFS: NewOSVFS("/")}
	if _, err := CalculateStatsWithOptions(context.Background(), v, "/", []string{"root"}, ScanOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	first := v.scopes[0]
	before := globalSudoClient.declines.Load()
	globalSudoClient.declines.Add(1) // the user refused the first operation's prompt
	noteElevationRefusal(context.WithValue(context.Background(), elevationScopeKeyType{}, first), before)
	if ElevationAllowed(context.WithValue(context.Background(), elevationScopeKeyType{}, first)) {
		t.Fatal("the first operation should remember the refusal")
	}

	v.scopes = nil
	if _, err := CalculateStatsWithOptions(context.Background(), v, "/", []string{"root"}, ScanOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	for i, scope := range v.scopes {
		if scope == first {
			t.Fatalf("second operation reused the first operation's elevation scope at call %d", i)
		}
		if !scopeAllowed(scope) {
			t.Fatalf("second operation was silenced at call %d", i)
		}
	}
}

func scopeAllowed(scope *elevationScope) bool {
	return ElevationAllowed(context.WithValue(context.Background(), elevationScopeKeyType{}, scope))
}

type scopeRecordingVFS struct {
	VFS
	scopes []*elevationScope
}

func (v *scopeRecordingVFS) Stat(ctx context.Context, path string) (VFSItem, error) {
	v.scopes = append(v.scopes, elevationScopeOf(ctx))
	// Base, not the whole path: Join gives "\\root" on Windows.
	name := v.Base(path)
	return VFSItem{Name: name, IsDir: name == "root" || name == "a"}, nil
}

func (v *scopeRecordingVFS) ReadDir(ctx context.Context, path string, onChunk func([]VFSItem)) error {
	v.scopes = append(v.scopes, elevationScopeOf(ctx))
	if v.Base(path) == "root" {
		onChunk([]VFSItem{{Name: "a", IsDir: true}, {Name: "b", Size: 1}})
	}
	return nil
}

// Every folder a size count reads belongs to one operation.
func TestSizeCountIsOneElevationOperation(t *testing.T) {
	v := &scopeRecordingVFS{VFS: NewOSVFS("/")}
	if _, err := CalculateStatsWithOptions(context.Background(), v, "/", []string{"root"}, ScanOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(v.scopes) < 2 {
		t.Fatalf("the scan made %d calls, want several", len(v.scopes))
	}
	for i, s := range v.scopes {
		if s == nil || s != v.scopes[0] {
			t.Fatalf("call %d ran in scope %p, want the scan's one %p", i, s, v.scopes[0])
		}
	}
}
