package multiarc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestProviderCanOpen(t *testing.T) {
	withFakeTools(t, func(name string) (string, error) {
		if name == "tar" {
			return "/usr/bin/tar", nil
		}
		return "", errNotFoundStub
	}, nil)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "backup.tar.gz")
	if err := os.WriteFile(archivePath, []byte("not a real archive, CanOpen never reads it"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parent := vfs.NewOSVFS(dir)
	p := &Provider{}

	if !p.CanOpen(context.Background(), parent, "backup.tar.gz") {
		t.Error("expected CanOpen to accept a local .tar.gz when tar is on PATH")
	}
	if p.CanOpen(context.Background(), parent, "backup.7z") {
		t.Error("CanOpen should refuse .7z when no 7z/7za/7zr is on PATH")
	}
	if p.CanOpen(context.Background(), parent, "backup.zip") {
		t.Error("CanOpen should refuse .zip when unzip is not on PATH and the file does not exist")
	}
}

func TestProviderCanOpenRejectsNonLocalParent(t *testing.T) {
	withFakeTools(t, func(string) (string, error) { return "/usr/bin/tar", nil }, nil)
	p := &Provider{}
	if p.CanOpen(context.Background(), nil, "backup.tar.gz") {
		t.Error("CanOpen should refuse when the parent is not an OSVFS (nil here)")
	}
}

func TestProviderOpen(t *testing.T) {
	withFakeTools(t, func(name string) (string, error) {
		if name == "tar" {
			return "/usr/bin/tar", nil
		}
		return "", errNotFoundStub
	}, nil)

	dir := t.TempDir()
	archivePath := filepath.Join(dir, "backup.tar.gz")
	if err := os.WriteFile(archivePath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parent := vfs.NewOSVFS(dir)
	p := &Provider{}

	v, err := p.Open(context.Background(), parent, "backup.tar.gz")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mv, ok := v.(*MultiArcVFS)
	if !ok {
		t.Fatalf("Open returned %T, want *MultiArcVFS", v)
	}
	if mv.localPath != archivePath {
		t.Errorf("localPath = %q, want %q", mv.localPath, archivePath)
	}
	if mv.backendID != "tar" {
		t.Errorf("backendID = %q, want %q", mv.backendID, "tar")
	}
	if !p.PanelEnterAllowed(context.Background(), parent, "backup.tar.gz") {
		t.Error("PanelEnterAllowed should default to true")
	}
}

func TestProviderOpenNoToolAvailable(t *testing.T) {
	withFakeTools(t, func(string) (string, error) { return "", errNotFoundStub }, nil)
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "backup.7z")
	if err := os.WriteFile(archivePath, []byte("stub"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	parent := vfs.NewOSVFS(dir)
	p := &Provider{}
	if _, err := p.Open(context.Background(), parent, "backup.7z"); err == nil {
		t.Fatal("expected Open to fail when no 7z binary is on PATH")
	}
}
