package main

import (
	"strings"
	"sync"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

var driveCommandPrefixState = struct {
	sync.Mutex
	ids map[string]struct{}
}{ids: make(map[string]struct{})}

// registerCommandPrefixOnce adds a core-owned prefix without making repeated
// plugin initialization report a duplicate registration. The handler opens
// the corresponding drive in the active panel; its argument is intentionally
// ignored because these prefixes are panel selectors, not path resolvers.
func registerCommandPrefixOnce(id, prefix, driveName string) {
	id = strings.TrimSpace(id)
	prefix = strings.TrimSpace(prefix)
	if id == "" || prefix == "" {
		return
	}

	driveCommandPrefixState.Lock()
	defer driveCommandPrefixState.Unlock()
	if _, exists := driveCommandPrefixState.ids[id]; exists {
		return
	}
	_, err := (&coreAPI{}).RegisterCommandPrefix(id, prefix, func(app vfs.App, _ string) {
		pf, ok := app.(*PanelsFrame)
		if !ok || !pf.OpenDrive(driveName) {
			vtui.DebugLog("PREFIX: cannot open drive %q", driveName)
		}
	})
	if err != nil {
		vtui.DebugLog("PREFIX: cannot register %q as %q: %v", prefix, id, err)
		return
	}
	driveCommandPrefixState.ids[id] = struct{}{}
}

func registerDriveCommandPrefix(name string) {
	name = strings.TrimSpace(name)
	prefix, err := normalizeCommandPrefix(name)
	if err != nil || prefix == "" || prefix == "ai" {
		return
	}
	registerCommandPrefixOnce("drive:"+prefix, prefix, name)
}

func registerBuiltInCommandPrefixes() {
	registerCommandPrefixOnce("builtin.temp-panel", "tmp", "tmp")
	registerPlatformCommandPrefixes()
}

// OpenDrive switches the active panel to a registered plugin or platform
// drive. The two short aliases are core-owned: tmp is the temporary panel and
// reg is the Windows Registry drive. Keeping the lookup here means command
// prefixes and the drive menu use the same factories and lifecycle rules.
func (pf *PanelsFrame) OpenDrive(name string) bool {
	if pf == nil || pf.closed {
		return false
	}
	fsp := pf.getActivePanel()
	if fsp == nil {
		return false
	}

	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "tmp" {
		pf.switchToVFS(fsp, newTempPanelVFS(nil, globalTempPanelStore, 0))
		pf.revealPanelsAfterCommand()
		return true
	}
	if normalized == "reg" {
		name = "Windows Registry"
	}

	var factory func() vfs.VFS
	for _, drive := range getPlatformDrives() {
		if strings.EqualFold(strings.TrimSpace(drive.Name), strings.TrimSpace(name)) {
			factory = drive.Factory
			break
		}
	}
	if factory == nil {
		for _, drive := range driveRegistrySnapshot() {
			if strings.EqualFold(strings.TrimSpace(drive.Name), strings.TrimSpace(name)) {
				factory = drive.Factory
				break
			}
		}
	}
	if factory == nil {
		return false
	}

	opened := factory()
	if opened == nil {
		return false
	}
	pf.switchToVFS(fsp, opened)
	pf.revealPanelsAfterCommand()
	return true
}

func (pf *PanelsFrame) revealPanelsAfterCommand() {
	if !pf.showPanels {
		pf.togglePanelsVisibility()
	}
}
