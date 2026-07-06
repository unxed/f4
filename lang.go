package main

import "github.com/unxed/vtui"

// Lng contains f4-specific strings.
// We also override some vtui internal strings here.
var Lng = map[string]string{
	"vtui.Ok":              "&Ok",
	"vtui.Cancel":          "Cancel",
	"Panel.Other":          "&Other panel",
	"Panel.Column.Name":    "Name",
	"Panel.Column.Size":    "Size",
	"Panel.UpDir":          "UP-DIR",
	"Panels.Prompt":        "> ",
	"Edit.NewFileTitle":    " Create New File ",
	"Edit.NewFilePrompt":   "File &name:",
	"MakeFolder.Title":     " Create Folder ",
	"MakeFolder.Prompt":    "Create the &folder:",
	"Delete.Title":         " Delete ",
	"Delete.Confirm":       "Do you want to delete\n%s?",
	"Delete.Btn":           "&Delete",
	"Copy.Title":           " Copy ",
	"Copy.Prompt":          "Copy %d item(s) to:",
	"Copy.Btn":             "&Copy",
	"Move.Title":           " Move ",
	"Move.Prompt":          "Rename or move %d item(s) to:",
	"Move.Btn":             "&Rename",
	"Btn.OverwriteAll":     "Overwrite &All",
	"Btn.SkipAll":          "S&kip All",
	"Btn.Retry":            "&Retry",
	"Btn.Ignore":           "&Ignore",
	"Operation.Error":      "Operation failed:\n%s",
	"Op.ClonePanels":       "&Clone panels to new workspace",
	"Op.SwitchHint":        "Ctrl+Tab to switch windows",
	"Op.DummyTitle":        " Dummy Operation ",
	"Op.DummyText":         "This is a dummy 5-minute operation.\nChoose background mode:",
	"Viewer.Title":         " View ",
	"Viewer.ModeText":      "Text",
	"Viewer.ModeHex":       "Hex",
	"Viewer.SearchTitle":   " Search ",
	"Search.CaseSensitive": "&Case sensitive",
	"Search.Reverse":       "&Reverse search",
	"Select.Title":         " Select ",
	"Deselect.Title":       " Deselect ",
	"Quit.Title":           " Exit ",
	"Quit.Confirm":         "Do you want to leave f4?",
	"Quit.Btn":             "&Leave",
	"Select.Mask":          "&Enter selection mask:",

	// Macros
	"Macro.AssignTitle":  " Assign Macro ",
	"Macro.AssignPrompt": "Press the desired key combination",
	// Top Menu
	"Menu.Left":     "Left",
	"Menu.Files":    "Files",
	"Menu.Commands": "Commands",
	"Menu.Options":  "Options",
	"Menu.Right":    "Right",
	"Menu.Exit":     "Exit",
	// Files Menu specific strings
	"Menu.Files.View":              "View",
	"Menu.Files.Edit":              "Edit",
	"Menu.Files.Copy":              "Copy",
	"Menu.Files.RenMov":            "Rename or move",
	"Menu.Files.MkDir":             "Make folder",
	"Menu.Files.Delete":            "Delete",
	"Menu.Left.Medium":             "Medium",
	"Menu.Left.Detailed":           "Detailed",
	"Menu.Commands.FindFile":       "Find file",
	"Menu.SortName":                "Name",
	"Menu.SortExt":                 "Extension",
	"Menu.SortTime":                "Time",
	"Menu.SortSize":                "Size",
	"Menu.SortUnsorted":            "Unsorted",
	"Menu.PanelSettings":           "Panel settings",
	"Menu.EditorSettings":          "Editor settings",
	"Menu.ConfirmationsSettings":   "Confirmations",
	"Menu.Options.Plugins":         "Manage plugins",
	"EditorSettings.Title":         " Editor settings ",
	"EditorSettings.AutoComplete":  "Enable &Autocomplete",
	"EditorSettings.Mask":          "Active for f&ile masks:",
	"PanelSettings.Title":          " Panel settings ",
	"PanelSettings.ShowHidden":     "Show &hidden and system files",
	"PanelSettings.HighlightDir":   "H&ighlight folders",
	"PanelSettings.SavePaths":      "Save panel &paths on exit",
	"PanelSettings.KeepCursor":     "Don't touch terminal &cursor style",
	"PanelSettings.VimHotkeys":     "Enable &Vim-like hotkeys (j, k, dd, cc, mm)",
	"PanelSettings.SyncPanelLoad":  "S&ynchronous panel loading (disable cache/chunking)",
	"ConfirmationsSettings.Title":  " Confirmations ",
	"ConfirmationsSettings.Copy":   "Confirm on &copy",
	"ConfirmationsSettings.Move":   "Confirm on &move",
	"ConfirmationsSettings.Delete": "Confirm on &delete",
	"ConfirmationsSettings.Exit":   "Confirm on e&xit",
	"FindFile.Title":               " Find File ",
	"FindFile.MaskPrompt":          "A filemask or several filemasks:",
	"FindFile.TextPrompt":          "Containing text:",
	"FindFile.BtnFind":             "&Find",
	"History.FoldersTitle":         " Folders History ",
	"UserMenu.LocalMenuTitle":      "Local menu",
	"UserMenu.MainMenuTitle":       "Main menu",
	"UserMenu.MainMenuFAR":         "FAR",

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
	"KeyBar.AltF1":  "Left",
	"KeyBar.AltF2":  "Right",
	"KeyBar.AltF3":  "Hex",
	"KeyBar.AltF7":  "Find",
	"KeyBar.AltF8":  "History",
	"KeyBar.AltF12": "Folders",
	"KeyBar.CtrlF1": "Left",
	"KeyBar.CtrlF2": "Right",
	"KeyBar.CtrlF3": "Name",
	"KeyBar.CtrlF4": "Ext",
	"KeyBar.CtrlF5": "Time",
	"KeyBar.CtrlF6": "Size",
	"KeyBar.CtrlF7": "Unsort",

	// KeyBar Editor
	"KeyBar.EditorF1":  "Help",
	"KeyBar.EditorF2":  "Save",
	"KeyBar.EditorF3":  "Wrap",
	"KeyBar.EditorF5":  "White",
	"KeyBar.EditorF7":  "Search",
	"KeyBar.EditorF10": "Quit",
	"KeyBar.ViewerF1":  "Help",
	"KeyBar.ViewerF2":  "Wrap",
	"KeyBar.ViewerF3":  "Exit",
	"KeyBar.ViewerF4":  "Hex",
	"KeyBar.ViewerF7":  "Search",
	"KeyBar.ViewerF10": "Quit",
}

// Msg is a proxy for vtui.Msg to keep f4 code clean.
func Msg(key string) string {
	return vtui.Msg(key)
}

func init() {
	InitLang()
}

// InitLang transfers all f4 strings to vtui localization engine.
func InitLang() {
	vtui.AddStrings(Lng)
}
