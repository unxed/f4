package netfox

import (
	"context"
	"testing"

	"github.com/jlaffaye/ftp"
	"github.com/unxed/f4/vfs"
)

func TestFTPSessionRetainIgnoresClosedSessionCoverageBatch41(t *testing.T) {
	session := &ftpSession{refs: 1, closed: true}
	session.retain()
	if session.refs != 1 {
		t.Fatalf("closed session refs = %d, want unchanged 1", session.refs)
	}
}

func TestFTPSessionReleaseWaitsForOtherViewsCoverageBatch41(t *testing.T) {
	session := &ftpSession{refs: 2}
	if err := session.release(); err != nil {
		t.Fatal(err)
	}
	if session.refs != 1 || session.closed {
		t.Fatalf("release with another view = refs %d closed %v", session.refs, session.closed)
	}
}

func TestFTPSessionReleaseWithoutConnectionMarksClosedCoverageBatch41(t *testing.T) {
	session := &ftpSession{refs: 1}
	if err := session.release(); err != nil {
		t.Fatal(err)
	}
	if !session.closed || session.refs != 0 {
		t.Fatalf("final release = refs %d closed %v", session.refs, session.closed)
	}
}

func TestFTPVFSSessionKeyAndOperationConnectionSelectionCoverageBatch41(t *testing.T) {
	conn := &ftp.ServerConn{}
	sessionConn := &ftp.ServerConn{}
	v := &FTPVFS{conn: conn, session: &ftpSession{conn: sessionConn}}
	if v.SessionKey() != conn || v.operationConn() != sessionConn {
		t.Fatal("session key or operation connection selected the wrong connection")
	}
	if (&FTPVFS{session: &ftpSession{}}).SessionKey() == nil {
		t.Fatal("session fallback key is nil")
	}
}

func TestFTPVFSPathLockedHandlesAbsoluteAndRelativeCoverageBatch41(t *testing.T) {
	v := &FTPVFS{cwd: "/home/user"}
	if got := v.pathLocked("../tmp"); got != "/home/tmp" {
		t.Fatalf("relative path = %q", got)
	}
	if got := v.pathLocked("/var/log"); got != "/var/log" {
		t.Fatalf("absolute path = %q", got)
	}
}

func TestFTPVFSRootDetectionCoversEmptyAndDotCoverageBatch41(t *testing.T) {
	for _, cwd := range []string{"", ".", "/"} {
		if !(&FTPVFS{cwd: cwd}).IsAtRoot() {
			t.Errorf("IsAtRoot(%q) = false", cwd)
		}
	}
	if (&FTPVFS{cwd: "/home"}).IsAtRoot() {
		t.Fatal("non-root FTP path reported as root")
	}
}

func TestFTPVFSAbsCleansAbsoluteAndJoinsRelativeCoverageBatch41(t *testing.T) {
	v := &FTPVFS{cwd: "/home/user"}
	if got, err := v.Abs("/tmp/../var"); err != nil || got != "/var" {
		t.Fatalf("absolute Abs = (%q, %v)", got, err)
	}
	if got, err := v.Abs("../tmp"); err != nil || got != "/home/tmp" {
		t.Fatalf("relative Abs = (%q, %v)", got, err)
	}
}

func TestFTPVFSSetAttributesReportsUnsupportedCoverageBatch41(t *testing.T) {
	if err := (&FTPVFS{}).SetAttributes(context.Background(), "/file", vfs.VFSItem{}); err == nil {
		t.Fatal("SetAttributes unexpectedly succeeded")
	}
}

func TestFTPVFSGetCapabilitiesAndSearchStubCoverageBatch41(t *testing.T) {
	caps := (&FTPVFS{}).GetCapabilities()
	if !caps.HasUnixPermissions || caps.HasWrite || caps.HasRandomAccess {
		t.Fatalf("FTP capabilities = %+v", caps)
	}
	ch, err := (&FTPVFS{}).Search(context.Background(), "/", "needle")
	if ch != nil || err != nil {
		t.Fatalf("Search = (%v, %v), want (nil, nil)", ch, err)
	}
}

func TestFTPVFSCloneWithoutSessionCreatesIndependentViewCoverageBatch41(t *testing.T) {
	v := &FTPVFS{cwd: "/captured"}
	clone, ok := v.Clone().(*FTPVFS)
	if !ok || clone == v || clone.GetPath() != v.GetPath() {
		t.Fatalf("clone without session = %#v, want independent view with path %q", clone, v.GetPath())
	}
}
