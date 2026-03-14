package main

import "github.com/unxed/vtui"

// colorMap links farcolors.ini keys to vtui.Palette indices.
var colorMap = map[string]int{
	"Menu.Text":                  vtui.ColMenuText,
	"Menu.Text.Selected":         vtui.ColMenuSelectedText,
	"Menu.Highlight":             vtui.ColMenuHighlight,
	"Menu.Highlight.Selected":    vtui.ColMenuSelectedHighlight,
	"Menu.Box":                   vtui.ColMenuBox,
	"Menu.Title":                 vtui.ColMenuTitle,
	"Panel.Text":                 vtui.ColPanelText,
	"Panel.Text.Selected":        vtui.ColPanelSelectedText,
	"Panel.Text.Highlight":       vtui.ColPanelHighlightText,
	"Panel.Text.Info":            vtui.ColPanelInfoText,
	"Panel.Cursor":               vtui.ColPanelCursor,
	"Panel.Cursor.Selected":      vtui.ColPanelSelectedCursor,
	"Panel.Title":                vtui.ColPanelTitle,
	"Panel.Title.Selected":       vtui.ColPanelSelectedTitle,
	"Panel.Title.Column":         vtui.ColPanelColumnTitle,
	"Dialog.Text":                vtui.ColDialogText,
	"Dialog.Box":                 vtui.ColDialogBox,
	"Dialog.Box.Title":           vtui.ColDialogBoxTitle,
	"Dialog.Edit":                vtui.ColDialogEdit,
	"Dialog.Button":              vtui.ColDialogButton,
	"Dialog.Button.Selected":     vtui.ColDialogSelectedButton,
	"CommandLine.UserScreen":     vtui.ColCommandLineUserScreen,
	"Dialog.Edit.Unchanged":      vtui.ColDialogEditUnchanged,
	"Panel.Box":                  vtui.ColPanelBox,
	"Dialog.Edit.Selected":       vtui.ColDialogEditSelected,
	"Panel.Scrollbar":            vtui.ColPanelScrollbar,
}

// InitColors parses the farcolors section and applies it to the vtui.Palette
func InitColors(ini *IniFile) {
	for key, idx := range colorMap {
		expr := ini.GetString("farcolors", key, "")
		if expr != "" {
			vtui.Palette[idx] = ParseFarColor(expr, vtui.Palette[idx])
		}
	}
}