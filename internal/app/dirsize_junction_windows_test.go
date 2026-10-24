//go:build windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/vfs"
)

func TestViewOnADirectoryDoesNotFollowJunction(t *testing.T) {
	pf, fsp, entries := dirSizeFixture(t)
	dir := filepath.Join(fsp.Vfs.GetPath(), "b")
	payload := make([]byte, 1<<20)
	if err := os.WriteFile(filepath.Join(dir, "target.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.(*vfs.OSVFS).Junction(context.Background(), dir, filepath.Join(dir, "loop")); err != nil {
		t.Fatalf("create recursive junction: %v", err)
	}

	fsp.SetCursorIndex(2) // b
	actionViewFile(pf)
	pumpSizeTasksUntil(t, func() bool { return entries["b"].SizeCalculated })

	got := entries["b"].Size
	if got < int64(len(payload)) || got >= 2*int64(len(payload)) {
		t.Errorf("F3 size = %d, want the directory's file but not a second traversal through its junction", got)
	}
}

func TestViewOnSelectedJunctionFollowsOnlySelectedTarget(t *testing.T) {
	pf, fsp, _ := dirSizeFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1<<20)
	if err := os.WriteFile(filepath.Join(target, "target.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fsp.Vfs.(*vfs.OSVFS).Junction(context.Background(), target, filepath.Join(target, "nested-loop")); err != nil {
		t.Fatalf("create nested recursive junction: %v", err)
	}
	selected := filepath.Join(fsp.Vfs.GetPath(), "selected")
	if err := fsp.Vfs.(*vfs.OSVFS).Junction(context.Background(), target, selected); err != nil {
		t.Fatalf("create selected junction: %v", err)
	}
	item, err := vfs.Lstat(context.Background(), fsp.Vfs, selected)
	if err != nil {
		t.Fatalf("stat selected junction: %v", err)
	}
	fsp.Entries = append(fsp.Entries, &panel.FileEntry{VFSItem: item})
	fsp.SetCursorIndex(len(fsp.Entries) - 1)

	actionViewFile(pf)
	pumpSizeTasksUntil(t, func() bool { return fsp.Entries[len(fsp.Entries)-1].SizeCalculated })

	got := fsp.Entries[len(fsp.Entries)-1].Size
	if got < int64(len(payload)) || got >= 2*int64(len(payload)) {
		t.Errorf("F3 size = %d, want selected junction target contents without following its nested junction", got)
	}
}
