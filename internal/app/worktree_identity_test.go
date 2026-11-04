package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorktreeIdentity(t *testing.T) {
	checkout := t.TempDir()
	gitdir := t.TempDir()
	_ = os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+gitdir), 0600)
	_ = os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/zoin_branch/one\n"), 0600)
	if worktreeBranchAt(checkout) != "zoin_branch/one" {
		t.Fatal("branch resolution")
	}
	_ = os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/zoin_branch/two\n"), 0600)
	if !strings.HasSuffix(worktreeBranchAt(checkout), "two") {
		t.Fatal("branch was cached")
	}
	if worktreeBranchAt(t.TempDir()) != "" {
		t.Fatal("main checkout fallback")
	}
}

// The label is drawn on every frame; .git and HEAD are read once per
// worktreeBranchTTL, and a branch switch shows once it has passed.
func TestWorktreeBranchCache(t *testing.T) {
	checkout := t.TempDir()
	gitdir := t.TempDir()
	_ = os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: "+gitdir), 0600)
	head := filepath.Join(gitdir, "HEAD")
	_ = os.WriteFile(head, []byte("ref: refs/heads/one\n"), 0600)

	c := worktreeBranchCache{dir: checkout}
	t0 := time.Now()
	if got := c.get(t0); got != "one" {
		t.Fatalf("first frame: %q, want one", got)
	}
	_ = os.WriteFile(head, []byte("ref: refs/heads/two\n"), 0600)
	if got := c.get(t0.Add(worktreeBranchTTL / 2)); got != "one" {
		t.Errorf("within the TTL: %q, want the cached one", got)
	}
	if got := c.get(t0.Add(worktreeBranchTTL)); got != "two" {
		t.Errorf("after the TTL: %q, want two", got)
	}
}
