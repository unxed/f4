//go:build linux || windows || darwin

package proclist

import (
	"fmt"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/vtui"
)

// procDetailsSection is one part of the F3-style detail view (command line,
// environment, or open files): either the lines it read, or -- on a
// platform/permission combination that could not read it at all -- why not.
// A zero value ("no lines, no error") means "read successfully, and there
// was nothing to show" (an empty environment is exceedingly rare in
// practice, but not an error): showDetails renders that the same way as any
// other empty section, not as a failure.
type procDetailsSection struct {
	lines []string
	err   error
}

// procDetails is what collectProcDetails (details_linux.go/
// details_windows.go/details_darwin.go, one real implementation per
// platform, the same split collect() already uses) gathers for f4#312 part
// 4's F3 details view. FAR3's own F3 (Pcfg.cpp) offers configurable
// modules/handles/WMI-perf sections on top of these three; this plugin
// already refuses those (plugin.go's own package doc), so command line,
// environment and open files are the whole set, and each platform fills in
// whichever of the three it can without an undocumented API -- see each
// file's own comment for what that is on its OS.
type procDetails struct {
	cmdline   procDetailsSection
	environ   procDetailsSection
	openFiles procDetailsSection
}

// detailsDialogWidth/Height are clamped to the screen the same way
// plugins/mediainfo/config_dialog.go clamps its own settings dialog.
const (
	detailsDialogWidth  = 76
	detailsDialogHeight = 22
)

// showDetails is F3: a read-only snapshot, not a live view like the panel
// itself -- FAR3's own F3 does not refresh either, and a process' command
// line and environment cannot change after it starts in the first place
// (only its open files can, but re-reading those on every panel tick just to
// keep one open dialog current is more than this ticket asks for).
func (p *procListPanel) showDetails() {
	s, ok := p.selectedSample()
	if !ok {
		return
	}
	details := collectProcDetails(s.pid)

	lines := make([]string, 0, 64)
	lines = appendDetailsSection(lines, i18n.Msg("ProcList.Details.SectionCmdline"), details.cmdline)
	lines = append(lines, "")
	lines = appendDetailsSection(lines,
		fmt.Sprintf(i18n.Msg("ProcList.Details.SectionEnviron"), len(details.environ.lines)), details.environ)
	lines = append(lines, "")
	lines = appendDetailsSection(lines,
		fmt.Sprintf(i18n.Msg("ProcList.Details.SectionOpenFiles"), len(details.openFiles.lines)), details.openFiles)

	width, height := detailsDialogWidth, detailsDialogHeight
	if vtui.FrameManager != nil {
		if maximum := vtui.FrameManager.GetScreenSize() - 2; maximum > 20 && width > maximum {
			width = maximum
		}
		if maximum := vtui.FrameManager.GetScreenHeight() - 2; maximum > 10 && height > maximum {
			height = maximum
		}
	}

	title := fmt.Sprintf(i18n.Msg("ProcList.Details.Title"), s.name, s.pid)
	dlg := vtui.NewCenteredDialog(width, height, title)
	dlg.ShowClose = true

	list := vtui.NewListBox(0, 0, width-4, height-6, lines)
	list.QuickSearch = true
	dlg.AddItem(list)

	closeBtn := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	closeBtn.IsDefault = true
	dlg.AddItem(closeBtn)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	vbox.Add(list, vtui.Margins{Bottom: 1}, vtui.AlignFill)
	hbox := vtui.NewHBoxLayout(0, 0, width-4, 1)
	hbox.HorizontalAlign = vtui.AlignCenter
	hbox.Add(closeBtn, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(hbox, vtui.Margins{}, vtui.AlignFill)
	vbox.Apply()

	closeBtn.OnClick = func() { dlg.Close() }
	dlg.SetFocusedItem(list)
	vtui.FrameManager.Push(dlg)
}

// appendDetailsSection renders one section's header, then either its lines
// or -- if it could not be read -- a single explanatory line, matching
// ProcList.Kill.Failed and friends (actions.go) in putting the raw Go error
// text (%v) after a translated prefix rather than trying to translate error
// strings the OS itself produced.
func appendDetailsSection(lines []string, header string, section procDetailsSection) []string {
	lines = append(lines, header)
	switch {
	case section.err != nil:
		lines = append(lines, fmt.Sprintf(i18n.Msg("ProcList.Details.Unavailable"), section.err))
	case len(section.lines) == 0:
		lines = append(lines, i18n.Msg("ProcList.Details.Empty"))
	default:
		for _, l := range section.lines {
			lines = append(lines, "  "+l)
		}
	}
	return lines
}
