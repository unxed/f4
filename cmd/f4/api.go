package main

import "github.com/unxed/vtui"
import "github.com/unxed/f4/vfs"
import "github.com/unxed/vtinput"

// HostAPI defines the functions f4 exposes to plugins.
// coreAPI implements vfs.HostAPI.
type coreAPI struct{}

func (c *coreAPI) GetVersion() string {
	return getShortVersionInfo()
}

func (c *coreAPI) Log(msg string) {
	vtui.DebugLog("PLUGIN.LOG: %s", msg)
}

func (c *coreAPI) Message(msg string) {
	vtui.DebugLog("PLUGIN MESSAGE BOX: %s", msg)
	// Safely push to the main UI thread to avoid race conditions from background plugin loads
	vtui.FrameManager.PostTask(func() {
		vtui.ShowMessage(" Plugin Message ", msg, []string{"&Ok"})
	})
}
func (c *coreAPI) RegisterVFSProvider(p vfs.VFSProvider) {
	vtui.DebugLog("CORE: Registering VFS Provider: %s", p.Name())
	vfs.RegisterProvider(p)
}
func (c *coreAPI) RegisterURIProvider(p vfs.URIProvider) error {
	if err := vfs.RegisterURIProvider(p); err != nil {
		return err
	}
	vtui.DebugLog("CORE: Registered URI Provider: %s", p.Scheme())
	return nil
}
func (c *coreAPI) RegisterHighlighter(p vtui.HighlighterProvider) {
	vtui.DebugLog("CORE: Registering Highlighter: %s", p.Name())
	vtui.RegisterHighlighter(p)
}
func (c *coreAPI) RegisterDrive(name string, factory func() vfs.VFS) {
	RegisterDrive(name, factory)
	registerDriveCommandPrefix(name)
}

func (c *coreAPI) RegisterGlobalHotkey(vk uint16, mods vtinput.ControlKeyState, handler func(app vfs.App)) {
	RegisterGlobalHotkey(vk, mods, handler)
}

func (c *coreAPI) RegisterPluginMenuItem(label string, handler func(app vfs.App)) {
	RegisterPluginMenuItem(label, handler)
}
func (c *coreAPI) RunAction(name string) bool {
	resChan := make(chan bool, 1)
	vtui.FrameManager.PostTask(func() {
		resChan <- RunAction(name)
	})
	return <-resChan
}
