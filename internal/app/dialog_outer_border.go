package app

import (
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/vtui"
)

// RenderDialogOuterBorder optionally draws an extra frame one screen cell
// outside the border of a modal dialog or the user menu, matching the
// far2l/Far3 "outer border" look requested in f4#1399 (the reporter finds
// f4's current single-frame look, without that extra ring, too close to Dos
// Navigator's). It is purely cosmetic and off by default.
//
// It runs from vtui's FrameManager.AfterFrameShow, right after the frame
// painted its own border -- the same extension point dialog.RenderHelpFrame
// already uses to add a hint on the Help window's border (see #378) -- so
// the extra ring stays in that frame's place in the paint stack: a frame
// above still covers it, and nothing outside vtui needs to change.
func RenderDialogOuterBorder(scr *vtui.ScreenBuf, frame vtui.Frame) {
	if !config.App.DialogOuterBorder || !isDialogOuterBorderFrame(frame) {
		return
	}
	x1, y1, x2, y2 := frame.GetPosition()
	if x2 <= x1 || y2 <= y1 {
		return
	}
	// Sample the frame's own top-left corner so the extra ring reuses
	// whatever attribute the frame just drew there -- the normal dialog box
	// colour, a themed variant, or (for IsWarning dialogs) the warning box
	// colour -- instead of hard-coding a palette index.
	attr := scr.GetCell(x1, y1).Attributes
	vtui.NewPainter(scr).DrawBox(x1-1, y1-1, x2+1, y2+1, attr, vtui.SingleBox)
}

// isDialogOuterBorderFrame reports whether frame is one of the two kinds
// f4#1399 asked for: a modal dialog window, or the user menu. It deliberately
// excludes ordinary popup/context menus and non-modal windows (editor,
// viewer, panels, ...), which never get the extra ring.
//
// GetType and IsModal are vtui.Frame interface methods, so they resolve
// correctly through any number of embedding layers (several f4 dialogs wrap
// *vtui.Window in their own struct, e.g. dialog.FileDialog) without needing
// a concrete type assertion for every wrapper. The user menu is the one
// exception: it is a *vtui.VMenu under the hood, whose GetType() reports
// TypeMenu the same as any other menu, so it needs its own concrete check
// against panel.UserMenuFrame, the wrapper f4 pushes it as.
func isDialogOuterBorderFrame(frame vtui.Frame) bool {
	if frame.GetType() == vtui.TypeDialog {
		return true
	}
	_, isUserMenu := frame.(*panel.UserMenuFrame)
	return isUserMenu
}
