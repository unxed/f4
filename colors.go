package main

import "github.com/unxed/vtui"
const (
	ColPanelText = vtui.LastPaletteColor + iota
	ColPanelSelectedText
	ColPanelHighlightText
	ColPanelInfoText
	ColPanelCursor
	ColPanelSelectedCursor
	ColPanelTitle
	ColPanelSelectedTitle
	ColPanelColumnTitle
	ColPanelTotalInfo
	ColPanelSelectedInfo

	ColCommandLineUserScreen
	ColPanelBox
	ColPanelScrollbar

	ColCommandLinePrompt
	ColCommandLineText

	LastF4PaletteColor
)

// SetDefaultF4Palette ensures the palette is large enough and sets f4-specific default colors.
func SetDefaultF4Palette() {
	if len(vtui.Palette) < LastF4PaletteColor {
		newPal := make([]uint64, LastF4PaletteColor)
		copy(newPal, vtui.Palette)
		vtui.Palette = newPal
	}

	black := uint32(0x000000)
	white := uint32(0xFFFFFF)
	cyan := uint32(0x00A0A0)
	blue := uint32(0x0000A0)
	yellow := uint32(0xFFFF00)
	lightGray := uint32(0xC0C0C0)

	// Panels (LightCyan on Blue)
	vtui.Palette[ColPanelText] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelSelectedText] = vtui.SetRGBBoth(0, yellow, blue)
	vtui.Palette[ColPanelCursor] = vtui.SetRGBBoth(0, black, cyan)
	vtui.Palette[ColPanelSelectedCursor] = vtui.SetRGBBoth(0, yellow, cyan)
	vtui.Palette[ColPanelBox] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelTitle] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelColumnTitle] = vtui.SetRGBBoth(0, yellow, blue)

	vtui.Palette[ColPanelHighlightText] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelInfoText] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelSelectedTitle] = vtui.Palette[ColPanelTitle]
	vtui.Palette[ColPanelTotalInfo] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelSelectedInfo] = vtui.Palette[ColPanelSelectedText]
	vtui.Palette[ColPanelScrollbar] = vtui.Palette[ColPanelBox]

	// Command line / User screen
	vtui.Palette[ColCommandLineUserScreen] = vtui.SetRGBBoth(0, lightGray, black)
	vtui.Palette[ColCommandLinePrompt] = vtui.SetRGBBoth(0, 0x00FFFF, black)
	vtui.Palette[ColCommandLineText] = vtui.SetRGBBoth(0, white, black)
}

// colorMap links farcolors.ini keys to vtui.Palette indices.
var colorMap = map[string]int{
	"Menu.Text":                  vtui.ColMenuText,
	"Menu.Text.Selected":         vtui.ColMenuSelectedText,
	"Menu.Highlight":             vtui.ColMenuHighlight,
	"Menu.Highlight.Selected":    vtui.ColMenuSelectedHighlight,
	"Menu.Box":                   vtui.ColMenuBox,
	"Menu.Title":                 vtui.ColMenuTitle,
	"Panel.Text":                 ColPanelText,
	"Panel.Text.Selected":        ColPanelSelectedText,
	"Panel.Text.Highlight":       ColPanelHighlightText,
	"Panel.Text.Info":            ColPanelInfoText,
	"Panel.Cursor":               ColPanelCursor,
	"Panel.Cursor.Selected":      ColPanelSelectedCursor,
	"Panel.Title":                ColPanelTitle,
	"Panel.Title.Selected":       ColPanelSelectedTitle,
	"Panel.Title.Column":         ColPanelColumnTitle,
	"Panel.Box":                  ColPanelBox,
	"Panel.Scrollbar":            ColPanelScrollbar,
	"Dialog.Text":                vtui.ColDialogText,
	"Dialog.Box":                 vtui.ColDialogBox,
	"Dialog.Box.Title":           vtui.ColDialogBoxTitle,
	"Dialog.Edit":                vtui.ColDialogEdit,
	"Dialog.Button":              vtui.ColDialogButton,
	"Dialog.Button.Selected":     vtui.ColDialogSelectedButton,
	"Dialog.Edit.Unchanged":      vtui.ColDialogEditUnchanged,
	"Dialog.Edit.Selected":       vtui.ColDialogEditSelected,
	"CommandLine.UserScreen":     ColCommandLineUserScreen,
	"CommandLine.Prompt":         ColCommandLinePrompt,
	"CommandLine.Text":           ColCommandLineText,
	"KeyBar.Numbers":             vtui.ColKeyBarNum,
	"KeyBar.Labels":              vtui.ColKeyBarText,
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