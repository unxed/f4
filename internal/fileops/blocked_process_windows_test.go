//go:build windows

package fileops

import "testing"

func TestDecodeRMName(t *testing.T) {
	got := decodeRMName([]uint16{'E', 'd', 'i', 't', 'o', 'r', 0, 'x'})
	if got != "Editor" {
		t.Fatalf("decodeRMName() = %q, want Editor", got)
	}
}
