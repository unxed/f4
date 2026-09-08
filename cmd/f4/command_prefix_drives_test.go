package main

import (
	"strings"
	"testing"

	"github.com/unxed/f4/vfs"
)

func TestRegisterDriveCommandPrefixOpensRegisteredDrive(t *testing.T) {
	pf := setupMockPanelsFrame(t)
	defer pf.Close()

	const driveName = "TestPrefixDrive976"
	oldDrives := driveRegistrySnapshot()
	t.Cleanup(func() {
		pluginRegistryMu.Lock()
		DriveRegistry = oldDrives
		pluginRegistryMu.Unlock()
	})

	factoryCalls := 0
	drivePath := t.TempDir()
	(&coreAPI{}).RegisterDrive(driveName, func() vfs.VFS {
		factoryCalls++
		return vfs.NewOSVFS(drivePath)
	})

	if !dispatchCommandPrefix(pf, strings.ToLower(driveName)+":") {
		t.Fatal("registered drive prefix was not consumed")
	}
	if factoryCalls != 1 {
		t.Fatalf("drive factory calls = %d, want 1", factoryCalls)
	}
	active, ok := pf.Active().(*FileSystemPanel)
	if !ok || active.vfs == nil || active.vfs.GetPath() != drivePath {
		t.Fatalf("active panel VFS = %#v, want %q", active, drivePath)
	}
}

func TestRegisterDriveCommandPrefixLeavesAICommandAlone(t *testing.T) {
	commandPrefixRegistry.RLock()
	before := commandPrefixRegistry.byID["drive:ai"]
	commandPrefixRegistry.RUnlock()

	registerDriveCommandPrefix("AI")

	commandPrefixRegistry.RLock()
	after := commandPrefixRegistry.byID["drive:ai"]
	commandPrefixRegistry.RUnlock()
	if after != before {
		t.Fatal("AI drive prefix registration changed the existing ai: command")
	}
}

func TestBuiltInTemporaryPanelPrefix(t *testing.T) {
	registerBuiltInCommandPrefixes()
	prefixFrame := setupMockPanelsFrame(t)
	defer prefixFrame.Close()

	if !dispatchCommandPrefix(prefixFrame, "TMP:") {
		t.Fatal("tmp: prefix was not consumed")
	}
	if _, ok := prefixFrame.Active().(*FileSystemPanel).vfs.(*TempPanelVFS); !ok {
		t.Fatalf("active panel VFS = %T, want *TempPanelVFS", prefixFrame.Active().(*FileSystemPanel).vfs)
	}
}

func TestOpenDriveRevealsHiddenPanels(t *testing.T) {
	prefixFrame := setupMockPanelsFrame(t)
	defer prefixFrame.Close()
	prefixFrame.showPanels = false
	prefixFrame.showLeftPanel = false
	prefixFrame.showRightPanel = false
	prefixFrame.shellMode = ShellModeSimpleCaptured

	if !prefixFrame.OpenDrive("tmp") {
		t.Fatal("tmp drive was not opened")
	}
	if !prefixFrame.showPanels {
		t.Fatal("opening a drive from a hidden terminal did not reveal panels")
	}
}
