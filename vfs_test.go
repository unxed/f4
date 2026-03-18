package main

import "testing"

func TestOSVFS_PathLogic(t *testing.T) {
	vfs := NewOSVFS(".")

	// Testing path logic (it should be consistent with filepath)
	joined := vfs.Join("dir", "file.txt")
	if vfs.Base(joined) != "file.txt" {
		t.Errorf("VFS Base failed: %s", vfs.Base(joined))
	}

	dir := vfs.Dir(joined)
	if vfs.Base(dir) != "dir" {
		t.Errorf("VFS Dir failed: %s", dir)
	}
}