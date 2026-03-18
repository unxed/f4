package main

import (
	"testing"
	"github.com/unxed/vtui"
)

func TestFileEntry_GetCellText(t *testing.T) {
	// Mock entries
	file := &fileEntry{VFSItem: vtui.VFSItem{Name: "test.txt", Size: 1024, IsDir: false}}
	dir := &fileEntry{VFSItem: vtui.VFSItem{Name: "work", IsDir: true}}

	// 1. Column 0 (Name)
	if file.GetCellText(0) != "test.txt" {
		t.Errorf("File name mismatch: %s", file.GetCellText(0))
	}
	if dir.GetCellText(0) != "/work" {
		t.Errorf("Dir name mismatch: %s", dir.GetCellText(0))
	}

	// 2. Column 1 (Size)
	if file.GetCellText(1) != "1024" {
		t.Errorf("File size mismatch: %s", file.GetCellText(1))
	}
	if dir.GetCellText(1) != "UP-DIR" {
		t.Errorf("Dir size placeholder mismatch: %s", dir.GetCellText(1))
	}
}