package app

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// spreadsheetMenuItem finds the spreadsheet entry in a built menu bar.
func spreadsheetMenuItem(items []vtui.MenuBarItem) (vtui.MenuBarItem, vtui.MenuItem, bool) {
	label := i18n.Msg("Action.App.Spreadsheet")
	for _, bar := range items {
		for _, item := range bar.SubItems {
			if strings.Contains(plainMenuText(item.Text), plainMenuText(label)) {
				return bar, item, true
			}
		}
	}
	return vtui.MenuBarItem{}, vtui.MenuItem{}, false
}

// TestSpreadsheetStaysInTheMenuWhileAPopupIsOpen guards the reason the command
// was invisible in practice.
//
// Menu items are rebuilt on every GetMenuBar call, and once the dropdown is
// open that dropdown is the top frame. An action whose Visible predicate asked
// for panels on top therefore removed itself from the very menu the user was
// reading. The predicate came from Arkanoid, where it is harmless because that
// action is HideFromMenu and the check only ever runs for the palette.
func TestSpreadsheetStaysInTheMenuWhileAPopupIsOpen(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	bar, _, ok := spreadsheetMenuItem(pf.GetMenuBar().Items)
	if !ok {
		t.Fatal("the spreadsheet command is missing from the panels menu")
	}
	if commands := action.PlainLabel(i18n.Msg("Menu.Shell.Commands")); !strings.Contains(bar.Label, commands) {
		t.Errorf("the spreadsheet command sits in %q, expected %q", bar.Label, commands)
	}

	popup := vtui.NewVMenu("dropdown")
	popup.SetPosition(1, 1, 20, 10)
	vtui.FrameManager.Push(popup)
	if _, _, ok := spreadsheetMenuItem(BuildMenuBarItems("Shell")); !ok {
		t.Error("the spreadsheet command disappeared from the menu while a popup was on top")
	}
}

// TestSpreadsheetPathLookupIgnoresPopups keeps the file under the panel cursor
// reachable when the command is launched from a menu or from the palette,
// where the popup rather than the panels frame is on top.
func TestSpreadsheetPathLookupIgnoresPopups(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	vtui.FrameManager.Push(pf)

	if activePanelsFrame() != pf {
		t.Fatal("the panels frame was not found with panels on top")
	}
	popup := vtui.NewVMenu("dropdown")
	popup.SetPosition(1, 1, 20, 10)
	vtui.FrameManager.Push(popup)
	if activePanelsFrame() != pf {
		t.Error("the panels frame was not found while a popup was on top")
	}
}

// TestSheetPathResolution covers where a name typed in a sheet dialog lands.
//
// Nothing resolved these names before, so a relative one reached the writers
// as typed and the file appeared in the directory f4 was started from while
// the status line said it had been saved.
func TestSheetPathResolution(t *testing.T) {
	// Built from TempDir rather than spelled out. A path like /tmp/panel is
	// not absolute on Windows -- filepath.IsAbs wants the volume there -- so
	// a literal POSIX path made the absolute case fail on those jobs while
	// the code under test was doing the right thing with a real C: path.
	panelDir := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "book.xlsx")

	for _, tc := range []struct {
		name string
		dir  string
		path string
		want string
	}{
		{"relative name lands in the panel directory", panelDir, "sheet.f4s", filepath.Join(panelDir, "sheet.f4s")},
		{"surrounding blanks are not part of the name", panelDir, "  sheet.f4s  ", filepath.Join(panelDir, "sheet.f4s")},
		{"a typed absolute path is left alone", panelDir, absolute, absolute},
		{"an empty name stays empty", panelDir, "   ", ""},
	} {
		if got := sheetPathIn(tc.dir, tc.path); got != tc.want {
			t.Errorf("%s: sheetPathIn(%q, %q) = %q, want %q", tc.name, tc.dir, tc.path, got, tc.want)
		}
	}

	// With no panel behind it the destination is the old one, only spelled out.
	got := sheetPathIn("", "sheet.f4s")
	if !filepath.IsAbs(got) || filepath.Base(got) != "sheet.f4s" {
		t.Errorf("sheetPathIn(\"\", \"sheet.f4s\") = %q, want an absolute path ending in sheet.f4s", got)
	}
}

// TestSheetNativeNamesAreSQLiteNames covers the double extension: the offered
// name must end in .sqlite so other tools open the file, and the base name
// helper must treat the pair as one extension.
func TestSheetNativeNamesAreSQLiteNames(t *testing.T) {
	if !strings.HasSuffix(sheetNativeExtension, ".sqlite") {
		t.Fatalf("sheetNativeExtension = %q, want it to end in .sqlite", sheetNativeExtension)
	}
	if offered := i18n.Msg("Sheet.DefaultFileName"); !strings.HasSuffix(strings.ToLower(offered), sheetNativeExtension) {
		t.Errorf("the Save As dialog offers %q, want a %s name", offered, sheetNativeExtension)
	}

	for _, tc := range []struct{ path, want string }{
		{"/tmp/book.f4s.sqlite", "/tmp/book"},
		{"/tmp/book.F4S.SQLITE", "/tmp/book"},
		{"/tmp/book.f4s", "/tmp/book"},
		{"/tmp/book.xlsx", "/tmp/book"},
		{"/tmp/book", "/tmp/book"},
	} {
		if got := sheetBaseName(tc.path); got != tc.want {
			t.Errorf("sheetBaseName(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}
