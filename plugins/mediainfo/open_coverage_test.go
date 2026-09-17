package mediainfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
)

type reportPathVFS struct {
	*vfs.OSVFS
	statErr   error
	allExists bool
}

func (f *reportPathVFS) Stat(ctx context.Context, path string) (vfs.VFSItem, error) {
	if f.statErr != nil {
		return vfs.VFSItem{}, f.statErr
	}
	if f.allExists {
		return vfs.VFSItem{Name: filepath.Base(path)}, nil
	}
	return f.OSVFS.Stat(ctx, path)
}

func TestResolveMediaPathRejectsInvalidInputs(t *testing.T) {
	fs := vfs.NewOSVFS(t.TempDir())
	for _, raw := range []string{"", "   ", `"unterminated`, `""`} {
		if _, err := resolveMediaPath(fs, raw); err == nil {
			t.Errorf("resolveMediaPath(%q) accepted invalid input", raw)
		}
	}
	if _, err := resolveMediaPath(nil, "movie.mp4"); err == nil {
		t.Fatal("resolveMediaPath accepted a nil VFS")
	}
}

func TestResolveMediaPathUnescapesSingleQuotes(t *testing.T) {
	root := t.TempDir()
	fs := vfs.NewOSVFS(root)
	got, err := resolveMediaPath(fs, `'folder/it''s movie.mp4'`)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "folder", "it's movie.mp4")
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestExpandOSPathEnvironmentCoversPercentForms(t *testing.T) {
	t.Setenv("F4_MEDIA_PERCENT", "value")
	for input, want := range map[string]string{
		"prefix%%suffix":         "prefix%suffix",
		"%F4_MEDIA_PERCENT%/x":   "value/x",
		"%F4_MEDIA_UNKNOWN%/x":   "/x",
		"unmatched%F4_MEDIA_X":   "unmatched%F4_MEDIA_X",
		"$F4_MEDIA_PERCENT/path": "value/path",
	} {
		if got := expandOSPathEnvironment(input); got != want {
			t.Errorf("expandOSPathEnvironment(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAvailableReportPathHandlesStatErrorsAndExhaustion(t *testing.T) {
	root := t.TempDir()
	errStat := errors.New("stat failed")
	fs := &reportPathVFS{OSVFS: vfs.NewOSVFS(root), statErr: errStat}
	if _, err := availableReportPath(context.Background(), fs, filepath.Join(root, "movie.mp4")); !errors.Is(err, errStat) {
		t.Fatalf("availableReportPath error = %v, want %v", err, errStat)
	}

	all := &reportPathVFS{OSVFS: vfs.NewOSVFS(root), allExists: true}
	if _, err := availableReportPath(context.Background(), all, filepath.Join(root, "movie")); err == nil || !strings.Contains(err.Error(), "free MediaInfo report name") {
		t.Fatalf("exhausted report names returned %v", err)
	}

	if got, err := availableReportPath(context.Background(), vfs.NewOSVFS(root), filepath.Join(root, ".MediaInfo.txt")); err != nil || got != filepath.Join(root, ".MediaInfo.MediaInfo.txt") {
		t.Fatalf("extension edge: %q, %v", got, err)
	}
}

func TestAvailableReportPathUsesNotExistAsFree(t *testing.T) {
	root := t.TempDir()
	fs := vfs.NewOSVFS(root)
	path, err := availableReportPath(context.Background(), fs, filepath.Join(root, "movie.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate %q already exists: %v", path, err)
	}
}
