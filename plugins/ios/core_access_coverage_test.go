//go:build darwin || linux || windows

package iosfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unxed/f4/plugins/ios/internal/corefileservice"
)

type coverageCoreConnection struct {
	lists   map[string][]string
	listErr map[string]error
	pullErr error
	closed  atomic.Int32
}

func (c *coverageCoreConnection) ListDirectory(path string) ([]string, error) {
	if err := c.listErr[path]; err != nil {
		return nil, err
	}
	return c.lists[path], nil
}

func (c *coverageCoreConnection) PullFile(string, io.Writer) error { return c.pullErr }
func (c *coverageCoreConnection) Close() error {
	c.closed.Add(1)
	return nil
}

func TestCoreAccessSmallContracts(t *testing.T) {
	for _, tc := range []struct {
		domain coreDomain
		want   corefileservice.Domain
	}{
		{coreDomainAppData, corefileservice.DomainAppDataContainer},
		{coreDomainAppGroup, corefileservice.DomainAppGroupDataContainer},
		{coreDomainCrashReports, corefileservice.DomainSystemCrashLogs},
	} {
		if got, err := nativeFileServiceDomain(tc.domain); err != nil || got != tc.want {
			t.Errorf("nativeFileServiceDomain(%d) = %v, %v", tc.domain, got, err)
		}
	}
	if _, err := nativeFileServiceDomain(99); err == nil {
		t.Fatal("unknown CoreDevice domain was accepted")
	}

	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, errors.New("broken pipe"), errors.New("ordinary failure")} {
		want := err.Error() != "ordinary failure"
		if got := coreTransportFailure(err); got != want {
			t.Errorf("coreTransportFailure(%v) = %v, want %v", err, got, want)
		}
	}
	for _, message := range []string{"connection reset", "connection refused", "connection closed", "closed network connection", "stream closed", "transport is closing", "unexpected EOF"} {
		if !coreTransportFailure(errors.New(message)) {
			t.Errorf("coreTransportFailure(%q) = false", message)
		}
	}
	if coreTransportFailure(nil) {
		t.Fatal("nil transport error was classified as a failure")
	}
	if got := joinError(nil, errors.New("next")); got == nil || got.Error() != "next" {
		t.Fatalf("joinError(nil, next) = %v", got)
	}
	if got := joinError(errors.New("base"), nil); got == nil || got.Error() != "base" {
		t.Fatalf("joinError(base, nil) = %v", got)
	}
}

func TestNativeCoreFileServiceListsAndPulls(t *testing.T) {
	connection := &coverageCoreConnection{
		lists: map[string][]string{
			".":       {"folder", "file", ".hidden", "bad/name"},
			"folder":  {},
			".hidden": {},
		},
		listErr: map[string]error{
			"file":    os.ErrNotExist,
			".hidden": os.ErrPermission,
			"missing": os.ErrNotExist,
		},
	}
	service := &nativeCoreFileService{connection: connection}
	entries, err := service.List(context.Background(), "/")
	if err != nil {
		t.Fatalf("List = %v", err)
	}
	if len(entries) != 3 || !entries[0].IsDir || entries[1].Name != "file" || !entries[2].Hidden {
		t.Fatalf("List entries = %+v", entries)
	}

	var out bytes.Buffer
	if err := service.Pull(context.Background(), "/file", &out); err != nil {
		t.Fatalf("Pull = %v", err)
	}
	if _, err := service.List(context.Background(), "/missing"); err == nil {
		t.Fatal("failed directory probe was accepted")
	}
	if got := coreServicePath("/Documents/file"); got != "Documents/file" {
		t.Fatalf("coreServicePath = %q", got)
	}
}

func TestNativeCoreFileServiceErrorsPoisonAndCloseOnce(t *testing.T) {
	connection := &coverageCoreConnection{pullErr: io.EOF}
	var aborted atomic.Int32
	service := &nativeCoreFileService{connection: connection, abort: func() { aborted.Add(1) }}
	if err := service.Pull(context.Background(), "file", io.Discard); !errors.Is(err, ErrCoreDeviceConnection) {
		t.Fatalf("transport Pull error = %v", err)
	}
	if aborted.Load() != 1 || !service.broken.Load() {
		t.Fatalf("aborted=%d broken=%v", aborted.Load(), service.broken.Load())
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if err := service.Close(); err != nil || connection.closed.Load() != 1 {
		t.Fatalf("second Close = %v, calls=%d", err, connection.closed.Load())
	}

	access := newCoreAccess().(*nativeCoreAccess)
	if err := access.Close(); err != nil {
		t.Fatal(err)
	}
	if err := access.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCoreListErrorPrefersCallerCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	bounded, boundedCancel := context.WithCancel(context.Background())
	boundedCancel()
	if !errors.Is(coreListError(parent, context.Background(), errors.New("ignored")), context.Canceled) {
		t.Fatal("caller cancellation was not returned")
	}
	if err := coreListError(context.Background(), bounded, errors.New("ignored")); !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("bounded cancellation error = %v", err)
	}
}
