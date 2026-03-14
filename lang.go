package main

import "fmt"

// Lng is a simple map-based localization storage.
// In the future, this can load from JSON/TOML or embed.FS.
var Lng = map[string]string{
	"Panel.Column.Name": "Name",
	"Panel.Column.Size": "Size",
	"Panel.UpDir":       "UP-DIR",
	"Panels.Prompt":     "> ",
	"Desktop.Welcome":   " f4 project | Ctrl+Q to exit ",

	// KeyBar Normal
	"KeyBar.F1":  "Help",
	"KeyBar.F2":  "Menu",
	"KeyBar.F3":  "View",
	"KeyBar.F4":  "Edit",
	"KeyBar.F5":  "Copy",
	"KeyBar.F6":  "RenMov",
	"KeyBar.F7":  "MkDir",
	"KeyBar.F8":  "Delete",
	"KeyBar.F9":  "ConfMenu",
	"KeyBar.F10": "Quit",
	"KeyBar.F11": "Plugin",
	"KeyBar.F12": "Screen",

	// KeyBar Alt
	"KeyBar.AltF1": "Left",
	"KeyBar.AltF2": "Right",
}

// Msg retrieves a localized string by key.
func Msg(key string) string {
	if val, ok := Lng[key]; ok {
		return val
	}
	return fmt.Sprintf("{%s}", key) // Return key in braces if not found
}
