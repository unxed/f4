package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/unxed/vtui"
)

const (
	ColPanelText = vtui.LastPaletteColor + iota
	ColPanelSelectedText
	ColPanelHighlightText
	ColPanelInfoText
	ColPanelCursor
	ColPanelSelectedCursor
	ColPanelInactiveCursor
	ColPanelInactiveSelectedCursor
	ColPanelTitle
	ColPanelSelectedTitle
	ColPanelColumnTitle
	ColPanelTotalInfo
	ColPanelSelectedInfo
	ColPanelWorkspaceTabs
	ColPanelWorkspaceTabsActive
	ColPanelWorkspaceTabsAccent
	ColPanelWorkspaceTabsAttention

	ColCommandLineUserScreen
	ColPanelBox
	ColPanelScrollbar
	ColPanelMinimalScrollbar
	ColPanelDir
	ColPanelFastFindNoMatch

	ColCommandLinePrompt
	ColCommandLineInactivePrompt
	ColCommandLineText
	ColCommandLineSelectedText

	ColViewerText
	ColViewerSelectedText
	ColViewerStatus
	ColViewerArrows
	ColViewerScrollbar

	ColEditorText
	ColEditorCrosshair
	ColEditorStatus
	ColEditorScrollbar

	LastF4PaletteColor
)

// SetDefaultF4Palette ensures the palette is large enough and sets f4-specific default colors.
func SetDefaultF4Palette() {
	resetColorSources()
	// Initialize ThemePalette to match the "Tango/Ubuntu" gray theme from far2l
	vtui.ThemePalette[0] = 0x2E3436 // Default Background (Dark Gray)
	vtui.ThemePalette[7] = 0xD3D7CF // Default Text (Light Gray)
	vtui.ThemePalette[8] = 0x555753 // Bold/Intensity Background

	if len(vtui.Palette) < LastF4PaletteColor {
		newPal := make([]uint64, LastF4PaletteColor)
		copy(newPal, vtui.Palette)
		vtui.Palette = newPal
	}

	black := uint32(0x000000)
	//white := uint32(0xFFFFFF)
	cyan := uint32(0x00A0A0)
	blue := uint32(0x0000A0)
	yellow := uint32(0xFFFF00)
	//lightGray := uint32(0xC0C0C0)

	// Panels (LightCyan on Blue)
	vtui.Palette[ColPanelText] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelSelectedText] = vtui.SetRGBBoth(0, yellow, blue)
	vtui.Palette[ColPanelCursor] = vtui.SetRGBBoth(0, black, cyan)
	vtui.Palette[ColPanelSelectedCursor] = vtui.SetRGBBoth(0, yellow, cyan)
	vtui.Palette[ColPanelInactiveCursor] = vtui.SetRGBBoth(0, 0xD3D7CF, 0x555753)
	vtui.Palette[ColPanelInactiveSelectedCursor] = vtui.SetRGBBoth(0, yellow, 0x555753)
	vtui.Palette[ColPanelBox] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelTitle] = vtui.SetRGBBoth(0, 0x00FFFF, blue)
	vtui.Palette[ColPanelColumnTitle] = vtui.SetRGBBoth(0, yellow, blue)

	vtui.Palette[ColPanelHighlightText] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelInfoText] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelSelectedTitle] = vtui.SetRGBBoth(0, 0x00FFFF, 0x3030C0)
	vtui.Palette[ColPanelTotalInfo] = vtui.Palette[ColPanelText]
	vtui.Palette[ColPanelDir] = vtui.SetRGBBoth(0, 0xFFFFFF, blue)
	vtui.Palette[ColPanelFastFindNoMatch] = vtui.SetRGBBoth(0, 0xD75F5F, blue)
	vtui.Palette[ColPanelSelectedInfo] = vtui.Palette[ColPanelSelectedText]
	vtui.Palette[ColPanelWorkspaceTabs] = vtui.SetRGBBoth(0, 0x00FFFF, 0x000058)
	vtui.Palette[ColPanelWorkspaceTabsActive] = vtui.SetRGBBoth(0, 0xFFFFFF, blue)
	vtui.Palette[ColPanelWorkspaceTabsAccent] = vtui.SetRGBBoth(0, yellow, 0)
	vtui.Palette[ColPanelWorkspaceTabsAttention] = vtui.SetRGBBoth(0, 0xFF8700, 0)
	vtui.Palette[ColPanelScrollbar] = vtui.Palette[ColPanelBox]
	vtui.Palette[ColPanelMinimalScrollbar] = vtui.SetRGBBoth(0, 0xFFFFFF, blue)

	// Command line / User screen (Using terminal default background, Index 0)
	vtui.Palette[ColCommandLineUserScreen] = vtui.SetIndexBoth(0, 7, 0)
	vtui.Palette[ColCommandLinePrompt] = vtui.SetIndexBoth(0, 11, 0)        // Light Cyan on Black
	vtui.Palette[ColCommandLineInactivePrompt] = vtui.SetIndexBoth(0, 8, 0) // Gray on Black
	vtui.Palette[ColCommandLineText] = vtui.SetIndexBoth(0, 15, 0)          // White on Black
	vtui.Palette[ColCommandLineSelectedText] = vtui.SetIndexBoth(0, 0, 11)  // Black on Light Cyan

	// Editor selection: inverse of UserScreen
	vtui.Palette[vtui.ColDialogEditSelected] = vtui.SetIndexBoth(0, 0, 7)
	// KeyBar (Match far2l: LightGray on DarkGray for numbers, DarkGray on Teal for labels)
	vtui.Palette[vtui.ColKeyBarNum] = vtui.SetRGBBoth(0, 0xD3D7CF, 0x2E3436)
	vtui.Palette[vtui.ColKeyBarText] = vtui.SetRGBBoth(0, 0x2E3436, 0x06989A)

	// Viewer (Match far2l: LightGray on Black)
	vtui.Palette[ColViewerText] = vtui.SetIndexBoth(0, 7, 0)
	vtui.Palette[ColViewerSelectedText] = vtui.SetIndexBoth(0, 0, 11)
	vtui.Palette[ColViewerStatus] = vtui.SetIndexBoth(0, 0, 7)
	vtui.Palette[ColViewerArrows] = vtui.SetIndexBoth(0, 14, 0)
	vtui.Palette[ColViewerScrollbar] = vtui.SetIndexBoth(0, 8, 0)

	vtui.Palette[ColEditorText] = vtui.SetIndexBoth(0, 7, 0)
	vtui.Palette[ColEditorCrosshair] = vtui.SetRGBBoth(0, 0xD3D7CF, 0x222222)
	vtui.Palette[ColEditorStatus] = vtui.Palette[ColViewerStatus]
	vtui.Palette[ColEditorScrollbar] = vtui.Palette[ColPanelScrollbar]
}

type ColorSlot struct {
	Canonical    string
	Index        int
	Group        string
	ConstantName string
	Aliases      []string
}

var ColorGroups = []string{
	"Panel",
	"Lists and tables",
	"Dialog",
	"Warning message",
	"Menu",
	"Horizontal menu",
	"Key bar",
	"Command line",
	"Viewer",
	"Editor",
	"Help",
}

var ColorSlots = []ColorSlot{
	// Menu Group
	{Canonical: "Menu.Text", Index: vtui.ColMenuText, Group: "Menu", ConstantName: "ColMenuText"},
	{Canonical: "Menu.Text.Selected", Index: vtui.ColMenuSelectedText, Group: "Menu", ConstantName: "ColMenuSelectedText"},
	{Canonical: "Menu.Highlight", Index: vtui.ColMenuHighlight, Group: "Menu", ConstantName: "ColMenuHighlight"},
	{Canonical: "Menu.Highlight.Selected", Index: vtui.ColMenuSelectedHighlight, Group: "Menu", ConstantName: "ColMenuSelectedHighlight"},
	{Canonical: "Menu.Box", Index: vtui.ColMenuBox, Group: "Menu", ConstantName: "ColMenuBox"},
	{Canonical: "Menu.Title", Index: vtui.ColMenuTitle, Group: "Menu", ConstantName: "ColMenuTitle"},

	// Horizontal menu Group
	{Canonical: "HMenu.Text", Index: vtui.ColMenuBarItem, Group: "Horizontal menu", ConstantName: "ColMenuBarItem", Aliases: []string{"MenuBar.Text"}},
	{Canonical: "HMenu.Text.Selected", Index: vtui.ColMenuBarSelected, Group: "Horizontal menu", ConstantName: "ColMenuBarSelected", Aliases: []string{"MenuBar.Text.Selected"}},
	{Canonical: "HMenu.Highlight", Index: vtui.ColMenuBarHighlight, Group: "Horizontal menu", ConstantName: "ColMenuBarHighlight", Aliases: []string{"MenuBar.Highlight"}},
	{Canonical: "HMenu.Highlight.Selected", Index: vtui.ColMenuBarSelectedHighlight, Group: "Horizontal menu", ConstantName: "ColMenuBarSelectedHighlight", Aliases: []string{"MenuBar.Highlight.Selected"}},

	// Panel Group
	{Canonical: "Panel.Text", Index: ColPanelText, Group: "Panel", ConstantName: "ColPanelText"},
	{Canonical: "Panel.Text.Selected", Index: ColPanelSelectedText, Group: "Panel", ConstantName: "ColPanelSelectedText"},
	{Canonical: "Panel.Text.Highlight", Index: ColPanelHighlightText, Group: "Panel", ConstantName: "ColPanelHighlightText"},
	{Canonical: "Panel.Text.Info", Index: ColPanelInfoText, Group: "Panel", ConstantName: "ColPanelInfoText"},
	{Canonical: "Panel.Cursor", Index: ColPanelCursor, Group: "Panel", ConstantName: "ColPanelCursor"},
	{Canonical: "Panel.Cursor.Selected", Index: ColPanelSelectedCursor, Group: "Panel", ConstantName: "ColPanelSelectedCursor"},
	{Canonical: "Panel.Cursor.Inactive", Index: ColPanelInactiveCursor, Group: "Panel", ConstantName: "ColPanelInactiveCursor"},
	{Canonical: "Panel.Cursor.Inactive.Selected", Index: ColPanelInactiveSelectedCursor, Group: "Panel", ConstantName: "ColPanelInactiveSelectedCursor"},
	{Canonical: "Panel.Title", Index: ColPanelTitle, Group: "Panel", ConstantName: "ColPanelTitle"},
	{Canonical: "Panel.Title.Selected", Index: ColPanelSelectedTitle, Group: "Panel", ConstantName: "ColPanelSelectedTitle"},
	{Canonical: "Panel.Title.Column", Index: ColPanelColumnTitle, Group: "Panel", ConstantName: "ColPanelColumnTitle"},
	{Canonical: "Panel.Box", Index: ColPanelBox, Group: "Panel", ConstantName: "ColPanelBox"},
	{Canonical: "Panel.Scrollbar", Index: ColPanelScrollbar, Group: "Panel", ConstantName: "ColPanelScrollbar"},
	{Canonical: "Panel.Scrollbar.Minimal", Index: ColPanelMinimalScrollbar, Group: "Panel", ConstantName: "ColPanelMinimalScrollbar"},
	{Canonical: "Panel.Dir", Index: ColPanelDir, Group: "Panel", ConstantName: "ColPanelDir"},
	{Canonical: "Panel.FastFindNoMatch", Index: ColPanelFastFindNoMatch, Group: "Panel", ConstantName: "ColPanelFastFindNoMatch"},
	{Canonical: "Panel.Info.Total", Index: ColPanelTotalInfo, Group: "Panel", ConstantName: "ColPanelTotalInfo", Aliases: []string{"Panel.TotalInfo"}},
	{Canonical: "Panel.Info.Selected", Index: ColPanelSelectedInfo, Group: "Panel", ConstantName: "ColPanelSelectedInfo", Aliases: []string{"Panel.SelectedInfo"}},
	{Canonical: "Panel.Tabs", Index: ColPanelWorkspaceTabs, Group: "Panel", ConstantName: "ColPanelWorkspaceTabs"},
	{Canonical: "Panel.Tabs.Active", Index: ColPanelWorkspaceTabsActive, Group: "Panel", ConstantName: "ColPanelWorkspaceTabsActive"},
	{Canonical: "Panel.Tabs.Accent", Index: ColPanelWorkspaceTabsAccent, Group: "Panel", ConstantName: "ColPanelWorkspaceTabsAccent"},
	{Canonical: "Panel.Tabs.Attention", Index: ColPanelWorkspaceTabsAttention, Group: "Panel", ConstantName: "ColPanelWorkspaceTabsAttention"},

	// Lists and tables Group: the generic widgets that belong to neither a
	// panel nor a dialog, such as the find results list.
	//
	// Table.Separator is the column separator drawn between table columns. It
	// used to be called Table.Box and also carried the tree lines of a tree
	// view, which is why one key appeared to control unrelated elements; the
	// tree lines now sit in a vtui slot of their own.
	{Canonical: "Table.Separator", Index: vtui.ColTableBox, Group: "Lists and tables", ConstantName: "ColTableBox", Aliases: []string{"Table.Box"}},
	// Scrollbar is the fallback for the scrollbars that have no semantic slot
	// of their own: generic lists and menus. Panel, Viewer, Editor, Help and
	// combo dropdowns all carry their own key and never read this one.
	{Canonical: "Scrollbar", Index: vtui.ColScrollBar, Group: "Lists and tables", ConstantName: "ColScrollBar"},

	// Dialog Group
	{Canonical: "Dialog.Text", Index: vtui.ColDialogText, Group: "Dialog", ConstantName: "ColDialogText"},
	{Canonical: "Dialog.Text.Highlight", Index: vtui.ColDialogHighlightText, Group: "Dialog", ConstantName: "ColDialogHighlightText", Aliases: []string{"Dialog.Highlight"}},
	{Canonical: "Dialog.Box", Index: vtui.ColDialogBox, Group: "Dialog", ConstantName: "ColDialogBox"},
	{Canonical: "Dialog.Box.Title", Index: vtui.ColDialogBoxTitle, Group: "Dialog", ConstantName: "ColDialogBoxTitle"},
	{Canonical: "Dialog.Box.Title.Highlight", Index: vtui.ColDialogHighlightBoxTitle, Group: "Dialog", ConstantName: "ColDialogHighlightBoxTitle"},
	{Canonical: "Dialog.Edit", Index: vtui.ColDialogEdit, Group: "Dialog", ConstantName: "ColDialogEdit"},
	{Canonical: "Dialog.Button", Index: vtui.ColDialogButton, Group: "Dialog", ConstantName: "ColDialogButton"},
	{Canonical: "Dialog.Button.Selected", Index: vtui.ColDialogSelectedButton, Group: "Dialog", ConstantName: "ColDialogSelectedButton"},
	{Canonical: "Dialog.Button.Highlight", Index: vtui.ColDialogHighlightButton, Group: "Dialog", ConstantName: "ColDialogHighlightButton"},
	{Canonical: "Dialog.Button.Highlight.Selected", Index: vtui.ColDialogHighlightSelectedButton, Group: "Dialog", ConstantName: "ColDialogHighlightSelectedButton"},
	{Canonical: "Dialog.Edit.Unchanged", Index: vtui.ColDialogEditUnchanged, Group: "Dialog", ConstantName: "ColDialogEditUnchanged"},
	{Canonical: "Dialog.Edit.Selected", Index: vtui.ColDialogEditSelected, Group: "Dialog", ConstantName: "ColDialogEditSelected"},
	{Canonical: "Dialog.Combo.Text", Index: vtui.ColDialogComboText, Group: "Dialog", ConstantName: "ColDialogComboText"},
	{Canonical: "Dialog.Combo.Text.Selected", Index: vtui.ColDialogComboSelectedText, Group: "Dialog", ConstantName: "ColDialogComboSelectedText"},
	{Canonical: "Dialog.Combo.Highlight", Index: vtui.ColDialogComboHighlight, Group: "Dialog", ConstantName: "ColDialogComboHighlight"},
	{Canonical: "Dialog.Combo.Highlight.Selected", Index: vtui.ColDialogComboSelectedHighlight, Group: "Dialog", ConstantName: "ColDialogComboSelectedHighlight"},
	{Canonical: "Dialog.Combo.Box", Index: vtui.ColDialogComboBox, Group: "Dialog", ConstantName: "ColDialogComboBox"},
	{Canonical: "Dialog.Combo.Title", Index: vtui.ColDialogComboTitle, Group: "Dialog", ConstantName: "ColDialogComboTitle"},
	{Canonical: "Dialog.Combo.Scrollbar", Index: vtui.ColDialogComboScrollbar, Group: "Dialog", ConstantName: "ColDialogComboScrollbar"},

	// Warning message Group
	{Canonical: "WarnDialog.Text", Index: vtui.ColWarnText, Group: "Warning message", ConstantName: "ColWarnText"},
	{Canonical: "WarnDialog.Text.Highlight", Index: vtui.ColWarnHighlightText, Group: "Warning message", ConstantName: "ColWarnHighlightText", Aliases: []string{"WarnDialog.Highlight"}},
	{Canonical: "WarnDialog.Box", Index: vtui.ColWarnBox, Group: "Warning message", ConstantName: "ColWarnBox"},
	{Canonical: "WarnDialog.Box.Title", Index: vtui.ColWarnBoxTitle, Group: "Warning message", ConstantName: "ColWarnBoxTitle"},
	{Canonical: "WarnDialog.Box.Title.Highlight", Index: vtui.ColWarnHighlightBoxTitle, Group: "Warning message", ConstantName: "ColWarnHighlightBoxTitle"},
	{Canonical: "WarnDialog.Edit", Index: vtui.ColWarnEdit, Group: "Warning message", ConstantName: "ColWarnEdit"},
	{Canonical: "WarnDialog.Button", Index: vtui.ColWarnButton, Group: "Warning message", ConstantName: "ColWarnButton"},
	{Canonical: "WarnDialog.Button.Selected", Index: vtui.ColWarnSelectedButton, Group: "Warning message", ConstantName: "ColWarnSelectedButton"},
	{Canonical: "WarnDialog.Button.Highlight", Index: vtui.ColWarnHighlightButton, Group: "Warning message", ConstantName: "ColWarnHighlightButton"},
	{Canonical: "WarnDialog.Button.Highlight.Selected", Index: vtui.ColWarnHighlightSelectedButton, Group: "Warning message", ConstantName: "ColWarnHighlightSelectedButton"},
	{Canonical: "WarnDialog.Edit.Unchanged", Index: vtui.ColWarnEdit, Group: "Warning message", ConstantName: "ColWarnEdit"},
	{Canonical: "WarnDialog.Edit.Selected", Index: vtui.ColWarnEdit, Group: "Warning message", ConstantName: "ColWarnEdit"},

	// Key bar Group
	{Canonical: "Keybar.Num", Index: vtui.ColKeyBarNum, Group: "Key bar", ConstantName: "ColKeyBarNum", Aliases: []string{"KeyBar.Numbers"}},
	{Canonical: "Keybar.Text", Index: vtui.ColKeyBarText, Group: "Key bar", ConstantName: "ColKeyBarText", Aliases: []string{"KeyBar.Labels"}},

	// Command line Group
	{Canonical: "CommandLine", Index: ColCommandLineText, Group: "Command line", ConstantName: "ColCommandLineText", Aliases: []string{"CommandLine.Text"}},
	{Canonical: "CommandLine.Prefix", Index: ColCommandLinePrompt, Group: "Command line", ConstantName: "ColCommandLinePrompt", Aliases: []string{"CommandLine.Prompt"}},
	{Canonical: "CommandLine.Selected", Index: ColCommandLineSelectedText, Group: "Command line", ConstantName: "ColCommandLineSelectedText", Aliases: []string{"CommandLine.SelectedText", "CommandLine.Text.Selected"}},
	{Canonical: "CommandLine.Prompt.Inactive", Index: ColCommandLineInactivePrompt, Group: "Command line", ConstantName: "ColCommandLineInactivePrompt"},
	{Canonical: "CommandLine.UserScreen", Index: ColCommandLineUserScreen, Group: "Command line", ConstantName: "ColCommandLineUserScreen"},

	// Viewer Group
	{Canonical: "Viewer.Text", Index: ColViewerText, Group: "Viewer", ConstantName: "ColViewerText"},
	{Canonical: "Viewer.Text.Selected", Index: ColViewerSelectedText, Group: "Viewer", ConstantName: "ColViewerSelectedText"},
	{Canonical: "Viewer.Status", Index: ColViewerStatus, Group: "Viewer", ConstantName: "ColViewerStatus"},
	{Canonical: "Viewer.Arrows", Index: ColViewerArrows, Group: "Viewer", ConstantName: "ColViewerArrows"},
	{Canonical: "Viewer.Scrollbar", Index: ColViewerScrollbar, Group: "Viewer", ConstantName: "ColViewerScrollbar"},

	// Editor Group
	{Canonical: "Editor.Text", Index: ColEditorText, Group: "Editor", ConstantName: "ColEditorText"},
	{Canonical: "Editor.Scrollbar", Index: ColEditorScrollbar, Group: "Editor", ConstantName: "ColEditorScrollbar"},
	{Canonical: "Editor.Status", Index: ColEditorStatus, Group: "Editor", ConstantName: "ColEditorStatus"},

	// Help Group
	{Canonical: "Help.Text", Index: vtui.ColHelpText, Group: "Help", ConstantName: "ColHelpText"},
	{Canonical: "Help.Text.Highlight", Index: vtui.ColHelpBold, Group: "Help", ConstantName: "ColHelpBold", Aliases: []string{"Help.Bold"}},
	{Canonical: "Help.Topic", Index: vtui.ColHelpLink, Group: "Help", ConstantName: "ColHelpLink", Aliases: []string{"Help.Link"}},
	{Canonical: "Help.Topic.Selected", Index: vtui.ColHelpSelectedLink, Group: "Help", ConstantName: "ColHelpSelectedLink", Aliases: []string{"Help.SelectedLink"}},
	{Canonical: "Help.Box", Index: vtui.ColHelpBox, Group: "Help", ConstantName: "ColHelpBox"},
	{Canonical: "Help.Box.Title", Index: vtui.ColHelpBoxTitle, Group: "Help", ConstantName: "ColHelpBoxTitle"},
	{Canonical: "Help.Scrollbar", Index: vtui.ColHelpScrollbar, Group: "Help", ConstantName: "ColHelpScrollbar"},
}

// colorMap links farcolors.ini keys to vtui.Palette indices dynamically.
var colorMap = make(map[string]int)

// colorSourceExpressions keeps the last authored expression for each
// canonical color key. Several far2l keys intentionally share one runtime
// palette slot, and contrast correction may adjust an RGB value slightly.
var colorSourceExpressions = make(map[string]string)
var colorSourcePalette = make(map[string]uint64)

func init() {
	for _, slot := range ColorSlots {
		for _, alias := range slot.Aliases {
			colorMap[alias] = slot.Index
		}
		colorMap[slot.Canonical] = slot.Index
	}
}

func resetColorSources() {
	colorSourceExpressions = make(map[string]string)
	colorSourcePalette = make(map[string]uint64)
}

// InitColors parses the farcolors section and applies it to the vtui.Palette.
// It is the whole chain in one call: use it when a single ini is the only
// source. Layering several inis goes through ApplyColorIni + FinishColors, so
// that the contrast pass runs once at the end rather than after every layer.
func InitColors(ini *IniFile) {
	ApplyColorIni(ini)
	FinishColors()
}

// ApplyColorIni overlays one farcolors section onto the current palette.
// Aliases are applied first so that a canonical key in the same file wins.
func ApplyColorIni(ini *IniFile) {
	for _, slot := range ColorSlots {
		var sourceExpr string
		for _, alias := range slot.Aliases {
			expr := ini.GetString("farcolors", alias, "")
			if expr != "" {
				vtui.Palette[slot.Index] = ParseFarColor(expr, vtui.Palette[slot.Index])
				sourceExpr = expr
			}
		}
		expr := ini.GetString("farcolors", slot.Canonical, "")
		if expr != "" {
			vtui.Palette[slot.Index] = ParseFarColor(expr, vtui.Palette[slot.Index])
			sourceExpr = expr
		}
		if sourceExpr != "" {
			colorSourceExpressions[slot.Canonical] = sourceExpr
		}
	}
}

// FinishColors performs the steps that must happen once, after every layer of
// the palette is in place.
func FinishColors() {
	// Terminal history uses indexed background color 0 for default and blank cells.
	// Keep it in sync with the configurable user-screen background.
	vtui.ThemePalette[0] = vtui.GetRGBBack(vtui.Palette[ColCommandLineUserScreen])

	AdjustContrastLevels()
	for _, slot := range ColorSlots {
		if _, ok := colorSourceExpressions[slot.Canonical]; ok {
			colorSourcePalette[slot.Canonical] = vtui.Palette[slot.Index]
		}
	}
}

// FormatFarColor serializes a vtui palette color attribute to a farcolors.ini string.
func FormatFarColor(attr uint64) string {
	var fg, bg uint32
	if attr&vtui.IsFgRGB != 0 {
		fg = vtui.GetRGBFore(attr)
	} else {
		fg = vtui.ThemePalette[vtui.GetIndexFore(attr)]
	}

	if attr&vtui.IsBgRGB != 0 {
		bg = vtui.GetRGBBack(attr)
	} else {
		bg = vtui.ThemePalette[vtui.GetIndexBack(attr)]
	}

	return fmt.Sprintf("foreground:#%06x | background:#%06x", fg, bg)
}

// ExportColors writes the current palette to a farcolors.ini file.
func ExportColors(path string) error {
	var sb strings.Builder
	sb.WriteString("[style]\nName = Custom\n")
	if base := strings.TrimSpace(AppConfig.ColorStyle); base != "" && !strings.EqualFold(base, customColorStyleName) {
		fmt.Fprintf(&sb, "Base = %s\n", base)
	}
	sb.WriteString("\n[farcolors]\n")

	for _, group := range ColorGroups {
		fmt.Fprintf(&sb, "\n# %s\n", group)
		if group == "Lists and tables" {
			sb.WriteString("# Table.Separator colors the column separators of a table, not the outer frame.\n")
			sb.WriteString("# Scrollbar is the shared fallback for generic lists and menus; panels, the\n")
			sb.WriteString("# viewer, the editor, help and combo dropdowns have scrollbar keys of their own.\n")
		}
		var slots []ColorSlot
		for _, slot := range ColorSlots {
			if slot.Group == group {
				slots = append(slots, slot)
			}
		}
		sort.Slice(slots, func(i, j int) bool {
			return slots[i].Canonical < slots[j].Canonical
		})
		for _, slot := range slots {
			attr := vtui.Palette[slot.Index]
			value := FormatFarColor(attr)
			if source, ok := colorSourceExpressions[slot.Canonical]; ok && colorSourcePalette[slot.Canonical] == attr {
				value = source
			}
			fmt.Fprintf(&sb, "%s = %s\n", slot.Canonical, value)
		}
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}
func GetColorRGBBoth(attr uint64) (fg uint32, bg uint32) {
	if attr&vtui.IsFgRGB != 0 {
		fg = vtui.GetRGBFore(attr)
	} else {
		fg = vtui.ThemePalette[vtui.GetIndexFore(attr)]
	}

	if attr&vtui.IsBgRGB != 0 {
		bg = vtui.GetRGBBack(attr)
	} else {
		bg = vtui.ThemePalette[vtui.GetIndexBack(attr)]
	}
	return fg, bg
}

func configureWorkspaceTabColors() {
	vtui.FrameManager.ConfigureWorkspaceTabColors(
		vtui.Palette[ColPanelWorkspaceTabs],
		vtui.Palette[ColPanelWorkspaceTabsActive],
		vtui.Palette[ColPanelWorkspaceTabsAccent],
		vtui.Palette[ColPanelWorkspaceTabsAttention],
	)
}

// GetLuminance returns the WCAG relative luminance of a packed 0xRRGGBB value.
func GetLuminance(rgb uint32) float64 {
	return relativeLuminance(toRGBF(rgb))
}

// CorrectContrast returns the foreground far2l would use for this pair.
// Packing to 8 bits and back is lossless, so a pair the algorithm leaves alone
// comes back bit-identical and callers can simply compare against the input.
func CorrectContrast(fg, bg uint32) uint32 {
	newFg, _ := ComputeContrast(toRGBF(fg), toRGBF(bg))
	return toRGB24(newFg)
}

func AdjustContrastLevels() {
	if !AppConfig.EnforceColorCorrection {
		return
	}
	vtui.DebugLog("COLORS: Adjusting contrast levels for palette")
	// Several canonical names map onto one palette slot, so correct each slot
	// once: a second pass would feed an already-corrected foreground back in.
	done := make(map[int]bool, len(ColorSlots))
	for _, slot := range ColorSlots {
		if strings.HasSuffix(slot.Canonical, ".Box") || done[slot.Index] {
			continue
		}
		done[slot.Index] = true
		attr := vtui.Palette[slot.Index]
		fg, bg := GetColorRGBBoth(attr)
		nfg := CorrectContrast(fg, bg)
		if nfg != fg {
			vtui.Palette[slot.Index] = vtui.SetRGBFore(attr, nfg)
		}
	}
}
