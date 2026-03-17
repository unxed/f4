package main

import "github.com/unxed/vtui"

// HostAPI defines the functions f4 exposes to plugins.
type HostAPI interface {
	GetVersion() string
	Log(msg string)
	Message(msg string)
}

// coreAPI implements HostAPI.
type coreAPI struct{}

func (c *coreAPI) GetVersion() string {
	return "f4 v0.1.0-alpha"
}

func (c *coreAPI) Log(msg string) {
	vtui.DebugLog("PLUGIN: %s", msg)
}

func (c *coreAPI) Message(msg string) {
	vtui.DebugLog("PLUGIN MESSAGE BOX: %s", msg)
	// TODO: Actually show a vtui.Dialog
}
