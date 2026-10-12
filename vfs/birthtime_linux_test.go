//go:build linux

package vfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The creation time comes from statx where the filesystem keeps one; where it
// does not the answer is "unknown", never a made-up date (f4#1817).
func TestReadBirthTimeIsRealOrUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "born.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadBirthTime(path)
	if !ok {
		t.Skip("this filesystem keeps no birth time")
	}
	if d := time.Since(got); d < -2*time.Second || d > time.Minute {
		t.Errorf("birth time %v of a file made just now is %v away", got, d)
	}
	if _, ok := ReadBirthTime(filepath.Join(filepath.Dir(path), "missing")); ok {
		t.Error("a missing file has a birth time")
	}
}
