package panel

import (
	"testing"
	"time"
)

// f4#1411: entering /root waits for the sudo password; walking on to /var
// must not wait behind it.

func TestACancelledLocalLoadDoesNotHoldTheNextOne(t *testing.T) {
	fp := &FileSystemPanel{}
	release := make(chan struct{})
	started := make(chan struct{})
	fp.enqueueDirectoryLoad(func() {
		close(started)
		<-release // the sudo prompt nobody has answered yet
	}, true)
	<-started
	done := make(chan struct{})
	fp.enqueueDirectoryLoad(func() { close(done) }, true)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the next directory waited for the hanging one")
	}
	close(release)
	fp.WaitForIdle()
}

func TestARemoteLoadIsStillWaitedFor(t *testing.T) {
	fp := &FileSystemPanel{}
	release := make(chan struct{})
	started := make(chan struct{})
	fp.enqueueDirectoryLoad(func() {
		close(started)
		<-release
	}, false)
	<-started
	done := make(chan struct{})
	fp.enqueueDirectoryLoad(func() { close(done) }, true)
	select {
	case <-done:
		t.Fatal("a load ran beside a remote one that holds the connection")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued load never ran")
	}
	fp.WaitForIdle()
}
