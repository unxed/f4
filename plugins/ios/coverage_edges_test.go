package iosfs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/unxed/f4/plugins/ios/internal/afcproto"
	"github.com/unxed/f4/vfs"
)

func TestAFCVirtualRootAndItemContracts(t *testing.T) {
	v := &AFCVFS{}
	v.SetVirtualRoot(nil, CapabilityMedia, CapabilityApplications, CapabilityCrashReports)

	if got, ok := v.rootCapabilityForPath("/[Applications]"); !ok || got != CapabilityApplications {
		t.Fatalf("rootCapabilityForPath = %v, %v", got, ok)
	}
	for _, path := range []string{"/[Applications]/nested", "/missing", "../[Applications]"} {
		if _, ok := v.rootCapabilityForPath(path); ok {
			t.Fatalf("rootCapabilityForPath accepted %q", path)
		}
	}
	items := v.virtualRootItems()
	if len(items) != 2 || items[0].Name != ApplicationsSelector || items[1].Name != CrashReportsSelector {
		t.Fatalf("virtualRootItems = %#v", items)
	}
	if got := v.GetCapabilities(); got != (vfs.VFSCapabilities{HasServerSideMove: true, HasRandomAccess: true}) {
		t.Fatalf("AFC capabilities = %#v", got)
	}
	if _, err := v.Search(context.Background(), "", ""); !errors.Is(err, ErrSearchUnsupported) {
		t.Fatalf("Search error = %v", err)
	}

	now := time.Unix(123, 0)
	cases := []struct {
		name string
		info afcproto.FileInfo
		mode string
		dir  bool
		link bool
		hid  bool
		exec bool
	}{
		{name: ".run", info: afcproto.FileInfo{Name: ".run", Type: afcproto.TypeRegular, Mode: 0755, Size: 4, ModTime: now}, mode: "-rw-r--r--", hid: true, exec: true},
		{name: "dir", info: afcproto.FileInfo{Type: afcproto.TypeDirectory, Mode: 0755}, mode: "drwxr-xr-x", dir: true, exec: true},
		{name: "link", info: afcproto.FileInfo{Type: afcproto.TypeSymlink}, mode: "lrwxrwxrwx", link: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := afcVFSItem(tc.name, tc.info)
			if item.Mode != tc.mode || item.IsDir != tc.dir || item.IsSymlink != tc.link || item.IsHidden != tc.hid || item.IsExecutable != tc.exec || !item.MTime.Equal(tc.info.ModTime) {
				t.Fatalf("afcVFSItem = %#v", item)
			}
		})
	}
}

func TestAFCConnectionLossClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "afc", err: afcproto.ErrConnectionLost, want: true},
		{name: "cancel", err: context.Canceled, want: true},
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "other", err: errors.New("ordinary failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := afcHandleConnectionLost(tc.err); got != tc.want {
				t.Fatalf("afcHandleConnectionLost(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIOSManagerAndCoreVersionEdges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device DeviceInfo
		want   string
	}{
		{name: "unknown paired", device: DeviceInfo{Paired: true}, want: "iOS device [unknown]"},
		{name: "model fallback", device: DeviceInfo{Model: " iPad ", Paired: true, State: "ready"}, want: "iPad"},
		{name: "escaped label", device: DeviceInfo{Name: "A/B\x00", Paired: true, State: "locked"}, want: "A⁄B� [locked]"},
		{name: "unpaired overrides state", device: DeviceInfo{Name: "Phone", Paired: false, State: "ready"}, want: "Phone [unpaired]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeviceDisplayName(tc.device); got != tc.want {
				t.Fatalf("DeviceDisplayName = %q, want %q", got, tc.want)
			}
		})
	}

	if !preferDevice(DeviceInfo{Paired: true, State: "ready"}, DeviceInfo{Paired: true, State: "locked"}) {
		t.Fatal("ready device should replace locked device")
	}
	if !preferDevice(DeviceInfo{Paired: true, State: "locked"}, DeviceInfo{Paired: false, State: "ready"}) {
		t.Fatal("paired device should replace unpaired device")
	}
	if !preferDevice(DeviceInfo{Paired: true, State: "locked", ConnectionType: "USB"}, DeviceInfo{Paired: true, State: "locked", ConnectionType: "network"}) {
		t.Fatal("USB device should replace network device")
	}

	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{raw: "", want: "", ok: false},
		{raw: "ios://Phone", want: "Phone", ok: true},
		{raw: "Phone", want: "Phone", ok: true},
		{raw: "ios://Phone/child", want: "", ok: false},
		{raw: "ios://../Phone", want: "", ok: false},
	} {
		got, ok := directManagerRowName(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("directManagerRowName(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}

	for _, tc := range []struct {
		raw, want string
	}{
		{raw: " 17.4 ", want: "available"},
		{raw: "26.5.2", want: "regressed"},
		{raw: "26.6", want: "available"},
		{raw: "not-a-version", want: "invalid"},
	} {
		switch tc.want {
		case "regressed":
			if !coreDevicePathRegression(tc.raw) || coreDeviceVersionAvailable(tc.raw) {
				t.Errorf("version %q regression/availability mismatch", tc.raw)
			}
		case "available":
			if !coreDeviceVersionAvailable(tc.raw) || coreDevicePathRegression(tc.raw) {
				t.Errorf("version %q availability/regression mismatch", tc.raw)
			}
		case "invalid":
			if coreDevicePathRegression(tc.raw) || coreDeviceVersionAvailable(tc.raw) {
				t.Errorf("invalid version %q was accepted", tc.raw)
			}
		case "":
			if coreDeviceVersionAvailable(tc.raw) || coreDevicePathRegression(tc.raw) {
				t.Errorf("version %q was accepted unexpectedly", tc.raw)
			}
		}
	}
}
