//go:build windows

package panel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestTreeShowsDirectoryJunctionAsLeaf(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	if err := vfs.NewOSVFS(root).Junction(context.Background(), target, link); err != nil {
		t.Fatalf("create junction: %v", err)
	}

	tp := newBareTreePanel(root)
	tp.expandAt(0)
	idx := tp.findChild(0, "junction")
	if idx < 0 {
		t.Fatal("directory junction is missing from the tree")
	}
	item := tp.items[idx]
	if !item.vfsItem.IsDir || vfs.LinkKindOf(&item.vfsItem) != vfs.LinkJunction {
		t.Fatalf("junction metadata = %+v", item.vfsItem)
	}
	if item.expandable {
		t.Fatal("directory junction must be a leaf to prevent cycles")
	}
}
