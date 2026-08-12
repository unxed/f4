package main

import (
	"time"

	"github.com/unxed/f4/fusefs"
)

func cancelOperationsForShutdown() {
	cancelAllForegroundApplyCommands()
	if GlobalQueueManager != nil {
		GlobalQueueManager.CancelAll()
	}
	if GlobalBackgroundJobs != nil {
		GlobalBackgroundJobs.CancelAll()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		queued, background := 0, 0
		if GlobalQueueManager != nil {
			queued = GlobalQueueManager.ActiveTasksCount()
		}
		if GlobalBackgroundJobs != nil {
			background = GlobalBackgroundJobs.ActiveCount()
		}
		if queued == 0 && background == 0 && activeForegroundApplyCommandCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cleanupAllApplyCommandResources()
	// FUSE mounts created from the panels belong to this process, and the
	// kernel connection dies with it: leaving them up would strand a mount
	// point that hangs every program walking into it. Mounts started with
	// --daemon live in a process of their own and are not in this list.
	fusefs.UnmountAll()
}
