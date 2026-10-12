package panel

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unxed/f4/internal/terminal"
	"github.com/unxed/f4/vfs"
)

// endedPty is a remote shell that ends as soon as it is read, like the one
// an SFTP-only host closes after printing its notice.
type endedPty struct{}

func (endedPty) Read([]byte) (int, error)    { return 0, io.EOF }
func (endedPty) Write(b []byte) (int, error) { return len(b), nil }
func (endedPty) Close() error                { return nil }
func (endedPty) SetSize(int, int)            {}
func (endedPty) Wait() error                 { return nil }
func (endedPty) Run(string, ...string) error { return nil }
func (endedPty) IsBusy() bool                { return false }

type shellVFS struct {
	*vfs.OSVFS
	opens atomic.Int32
	fail  bool
}

func (v *shellVFS) OpenPty(cols, rows int) (any, error) {
	v.opens.Add(1)
	if v.fail {
		return nil, errors.New("channel refused")
	}
	return terminal.PtyBackend(endedPty{}), nil
}

func openRemoteShellOften(pf *PanelsFrame, calls int) {
	for i := 0; i < calls; i++ {
		_ = pf.GetActivePTY()
		time.Sleep(2 * time.Millisecond)
	}
}

// f4#1766: a host that allows no shell was dialled again by every call.
func TestRemoteShellThatEndsAtOnceIsNotOpenedAgainAtEveryCall(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	host := &shellVFS{OSVFS: vfs.NewOSVFS(t.TempDir())}
	pf.GetActivePanel().Vfs = host

	openRemoteShellOften(pf, 50)
	if got := host.opens.Load(); got != 1 {
		t.Fatalf("shell opened %d times in 50 calls, want once", got)
	}
}

func TestRemoteShellThatFailsToOpenIsNotRetriedAtEveryCall(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	host := &shellVFS{OSVFS: vfs.NewOSVFS(t.TempDir()), fail: true}
	pf.GetActivePanel().Vfs = host

	openRemoteShellOften(pf, 50)
	if got := host.opens.Load(); got != 1 {
		t.Fatalf("failed shell retried %d times in 50 calls, want once", got)
	}
}

func TestRemoteShellIsOpenedAgainOnceTheHoldIsOver(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	host := &shellVFS{OSVFS: vfs.NewOSVFS(t.TempDir()), fail: true}
	pf.GetActivePanel().Vfs = host

	_ = pf.GetActivePTY()
	pf.PtyMutex.Lock()
	pf.remotePtyRetryAt[host] = time.Now().Add(-time.Second)
	pf.PtyMutex.Unlock()
	_ = pf.GetActivePTY()
	if got := host.opens.Load(); got != 2 {
		t.Fatalf("shell opened %d times, want a second try after the hold", got)
	}
}
