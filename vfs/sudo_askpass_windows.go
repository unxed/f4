//go:build windows

package vfs

import (
	"github.com/unxed/vtui"
	"os"
)

func RunSudoAskpass() {
	// Not used on Windows
	os.Exit(1)
}

func (c *SudoClient) runAskpassServer(path string) {
	// Not used on Windows
}

// RunOnUI runs fn on the UI goroutine: the "retry as administrator"
// question before UAC (f4#1768).
func (c *SudoClient) RunOnUI(fn func()) {
	if vtui.FrameManager != nil {
		vtui.FrameManager.PostTask(fn)
	}
}
