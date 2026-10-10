package vfs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOSVFSStreamingProgressAndCancellation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/deep/one.txt", "b/two.txt", "c/three.txt", "d/four.txt"} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	v := NewOSVFS(root)
	var checkpoints []int64
	var hits []FoundEntry
	q := FindQuery{Masks: []string{"*.txt"}, Progress: func(p FindProgress) {
		if p.DirectoryTotalKnown {
			if p.TotalDirs != 4 {
				t.Errorf("total=%d", p.TotalDirs)
			}
			if len(checkpoints) == 0 || checkpoints[len(checkpoints)-1] != p.CompletedDirs {
				checkpoints = append(checkpoints, p.CompletedDirs)
			}
		}
	}}
	err := v.FindFilesStream(context.Background(), root, q, func(e FoundEntry) { hits = append(hits, e) })
	if err != nil || len(hits) != 4 || fmt.Sprint(checkpoints) != "[0 1 2 3 4]" {
		t.Fatalf("hits=%v progress=%v err=%v", hits, checkpoints, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	count := 0
	err = v.FindFilesStream(ctx, root, FindQuery{Masks: []string{"*"}}, func(FoundEntry) { count++; cancel() })
	if count != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancel count=%d err=%v", count, err)
	}
}

func TestOSVFSStreamingRootWithoutChildren(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "one.txt")
	if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	v := NewOSVFS(root)
	var final FindProgress
	err := v.FindFilesStream(context.Background(), root, FindQuery{Progress: func(p FindProgress) { final = p }}, func(FoundEntry) {})
	if err != nil || !final.DirectoryTotalKnown || final.TotalDirs != 0 || final.Found != 1 {
		t.Fatalf("progress=%+v err=%v", final, err)
	}
}
