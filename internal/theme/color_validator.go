package theme

import "github.com/unxed/vtui"

// ValidateColorScheme checks the currently active palette for perceptual
// color problems (f4#363): insufficient WCAG contrast within a slot's own
// text/background pair, and harsh, highly-saturated clashes either within
// one slot or between two surfaces that sit right next to each other on
// screen. The concrete regression this guards against is hotkey
// highlighting rendered as light-yellow-on-light-gray, as it once was in
// far2l's default dark scheme.
//
// It is a thin adapter over vtui's generic color validator: colorSlotPairs
// turns each meaningful ColorSlot into a vtui.ColorPair, and
// vtui.ValidateColorsWithRules does the actual checking.
func ValidateColorScheme() []error {
	return ValidateColorSchemeWithRules(vtui.DefaultColorRules)
}

// ValidateColorSchemeWithRules is ValidateColorScheme with a custom rule
// set; see vtui.ColorRules for what each threshold controls.
func ValidateColorSchemeWithRules(rules vtui.ColorRules) []error {
	errs := vtui.ValidateColorsWithRules(colorSlotPairs(), rules)

	// Two adjacent backgrounds are not read like text on a background —
	// nobody expects a 4.5:1 contrast ratio between a dialog and the panel
	// behind it — so only the harsh-clash axis applies to this pair.
	adjacencyRules := rules
	adjacencyRules.MinContrastRatio = 0
	errs = append(errs, vtui.ValidateColorsWithRules(adjacentSurfacePairs(), adjacencyRules)...)

	return errs
}

// colorSlotPairs builds one vtui.ColorPair per meaningful ColorSlot, pairing
// each slot's own foreground and background as GetColorRGBBoth resolves
// them. It reuses AdjustContrastLevels' own notion of "meaningful pair":
// the caret and the two optional "inherit" backgrounds have no real
// foreground/background pair of their own, frame lines pair a real
// foreground with the box fill rather than with what visually sits behind
// the line, and Colorer's syntax slots pair a real foreground with filler
// that FormatFarColor/ExportColors need but nothing ever renders (see
// isFrameLineSlot/isSyntaxColorSlot). Several canonical names can map onto
// the same palette index; each index is validated only once.
func colorSlotPairs() []vtui.ColorPair {
	pairs := make([]vtui.ColorPair, 0, len(ColorSlots))
	done := make(map[int]bool, len(ColorSlots))
	for _, slot := range ColorSlots {
		if slot.Index == ColTerminalCursor ||
			slot.Index == vtui.ColDialogIndicatorBackground ||
			slot.Index == ColDialogSettingsBackground {
			continue
		}
		if isFrameLineSlot(slot) || isSyntaxColorSlot(slot) || done[slot.Index] {
			continue
		}
		done[slot.Index] = true
		fg, bg := GetColorRGBBoth(vtui.Palette[slot.Index])
		pairs = append(pairs, vtui.ColorPair{Name: slot.Canonical, FG: fg, BG: bg})
	}
	return pairs
}

// adjacentSurfacePairs checks colors that are not connected by a text
// relationship but still sit right next to each other on screen: a dialog
// floats over a panel, so a harsh clash between their box fills is just as
// jarring as one inside the dialog itself (f4#363: "не только внутри
// диалогов, но, например, контраст диалог-панели").
func adjacentSurfacePairs() []vtui.ColorPair {
	_, dialogBG := GetColorRGBBoth(vtui.Palette[vtui.ColDialogBox])
	_, panelBG := GetColorRGBBoth(vtui.Palette[ColPanelBox])
	return []vtui.ColorPair{
		{Name: "Dialog.Box / Panel.Box (adjacent surfaces)", FG: dialogBG, BG: panelBG},
	}
}
