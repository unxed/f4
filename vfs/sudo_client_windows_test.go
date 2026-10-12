//go:build windows

package vfs

import (
	"errors"
	"testing"
)

// f4#1768: Windows elevates through UAC, so the client is available there;
// an Open that wants a descriptor is refused before anything is asked,
// since no descriptor can cross from the elevated dispatcher.
func TestSudoClient_WindowsElevatesWithoutDescriptors(t *testing.T) {
	oldConfirm := confirmElevation
	t.Cleanup(func() { confirmElevation = oldConfirm })
	confirmElevation = func() bool {
		t.Error("the user was asked to elevate for an Open that cannot work")
		return false
	}
	client := &SudoClient{}
	if !client.IsAvailable() {
		t.Fatal("the UAC client must be available on Windows")
	}
	if _, err := client.Open(`C:\Windows\System32\config\SAM`, 0, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Open: %v, want ErrUnsupported", err)
	}
}
