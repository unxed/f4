package panel

import (
	"fmt"
	"os"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/vtui"
)

type driveMenuKind uint8

const (
	driveMenuKindUnknown driveMenuKind = iota
	driveMenuKindFixed
	driveMenuKindRemovable
	driveMenuKindCD
	driveMenuKindRemote
	driveMenuKindSubstitute
	driveMenuKindPhysical
)

type driveMenuOptionSpec struct {
	flag  uint32
	Label string
}

var DriveMenuOptionSpecs = []driveMenuOptionSpec{
	{config.DriveMenuShowType, "Drive.ShowType"},
	{config.DriveMenuShowLabel, "Drive.ShowLabel"},
	{config.DriveMenuUseShellName, "Drive.UseShellName"},
	{config.DriveMenuShowFilesystem, "Drive.ShowFilesystem"},
	{config.DriveMenuShowSize, "Drive.ShowSize"},
	{config.DriveMenuShowSizeFloat, "Drive.ShowSizeFloat"},
	{config.DriveMenuShowNetworkName, "Drive.ShowNetworkName"},
	{config.DriveMenuShowPlugins, "Drive.ShowPlugins"},
	{config.DriveMenuSortPluginsByHotkey, "Drive.SortPluginsByHotkey"},
	{config.DriveMenuShowRemovable, "Drive.ShowRemovable"},
	{config.DriveMenuShowCD, "Drive.ShowCD"},
	{config.DriveMenuShowRemote, "Drive.ShowRemote"},
	{config.DriveMenuDetectVirtual, "Drive.DetectVirtual"},
	{config.DriveMenuShowBookmarks, "Drive.ShowBookmarks"},
}

func driveMenuOptionEnabled(options, flag uint32) bool { return options&flag != 0 }

func driveMenuNameWithoutMarker(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(name, "&", ""))
}

func driveMenuBaseName(name string) string {
	name = driveMenuNameWithoutMarker(name)
	switch {
	case strings.HasPrefix(name, "/ Root"):
		return "/"
	case strings.HasPrefix(name, "~ Home"):
		return "~"
	case len(name) >= 2 && name[1] == ':':
		return name[:2]
	default:
		return name
	}
}

func driveMenuInfoPath(name string) string {
	clean := driveMenuNameWithoutMarker(name)
	switch {
	case strings.HasPrefix(clean, "/ Root"):
		return "/"
	case strings.HasPrefix(clean, "~ Home"):
		home, _ := os.UserHomeDir()
		return home
	case len(clean) >= 2 && clean[1] == ':':
		// GetDiskFreeSpaceEx and GetVolumeInformation both want a root.
		return clean[:2] + string(os.PathSeparator)
	default:
		return ""
	}
}

func driveMenuKindFor(name, path string) driveMenuKind {
	if strings.Contains(strings.ToLower(name), "physical disk") {
		return driveMenuKindPhysical
	}
	if path == "" {
		return driveMenuKindUnknown
	}
	return driveMenuPlatformKind(path)
}

func driveMenuKindLabel(kind driveMenuKind) string {
	switch kind {
	case driveMenuKindFixed:
		return i18n.Msg("Drive.TypeFixed")
	case driveMenuKindRemovable:
		return i18n.Msg("Drive.TypeRemovable")
	case driveMenuKindCD:
		return i18n.Msg("Drive.TypeCD")
	case driveMenuKindRemote:
		return i18n.Msg("Drive.TypeRemote")
	case driveMenuKindPhysical:
		// The row already says "Physical Disks"; repeating "physical" is
		// noise and is not how Far presents this synthetic entry.
		return ""
	case driveMenuKindSubstitute:
		return i18n.Msg("Drive.TypeSubstitute")
	default:
		return ""
	}
}

func DriveMenuSize(b uint64, decimal bool) string {
	if decimal {
		return formatBytesHuman(b)
	}
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(1024), 0
	for n := b / 1024; n >= 1024 && exp < 6; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%d %ciB", b/div, "KMGTPE"[exp])
}

type DriveMenuPlatformRow struct {
	Base, Kind, Label, Filesystem string
	Total, Free, network          string
}

type driveMenuPlatformColumn struct {
	text       string
	rightAlign bool
}

// driveMenuPlatformRowFor collects the metadata for one built-in drive. sysinfo.FsInfo
// is deliberately used only for built-in local rows: plugin VFSes can be
// remote and may block while resolving their metadata.
func driveMenuPlatformRowFor(drv sysinfo.DriveEntry, options uint32) DriveMenuPlatformRow {
	row := DriveMenuPlatformRow{Base: driveMenuBaseName(drv.Name)}
	path := driveMenuInfoPath(drv.Name)
	kind := driveMenuKindFor(drv.Name, path)

	if driveMenuOptionEnabled(options, config.DriveMenuShowType) {
		row.Kind = driveMenuKindLabel(kind)
	}

	info, infoOK := sysinfo.FSInfo{}, false
	if path != "" {
		info, infoOK = sysinfo.FS(path)
	}
	if infoOK {
		if driveMenuOptionEnabled(options, config.DriveMenuShowLabel) && info.Label != "" {
			row.Label = info.Label
		}
		if driveMenuOptionEnabled(options, config.DriveMenuShowFilesystem) && info.Type != "" {
			row.Filesystem = info.Type
		}
		if driveMenuOptionEnabled(options, config.DriveMenuShowSize) {
			decimal := driveMenuOptionEnabled(options, config.DriveMenuShowSizeFloat)
			row.Total = DriveMenuSize(info.Total, decimal)
			row.Free = DriveMenuSize(info.Free, decimal)
		}
		if driveMenuOptionEnabled(options, config.DriveMenuShowNetworkName) && info.Mount != "" && info.Mount != path {
			row.network = info.Mount
		}
	}
	return row
}

func (row DriveMenuPlatformRow) columns(options uint32) []driveMenuPlatformColumn {
	columns := make([]driveMenuPlatformColumn, 0, 7)
	if driveMenuOptionEnabled(options, config.DriveMenuShowType) {
		columns = append(columns, driveMenuPlatformColumn{text: row.Kind})
	}
	if driveMenuOptionEnabled(options, config.DriveMenuShowLabel) {
		columns = append(columns, driveMenuPlatformColumn{text: row.Label})
	}
	if driveMenuOptionEnabled(options, config.DriveMenuShowFilesystem) {
		columns = append(columns, driveMenuPlatformColumn{text: row.Filesystem})
	}
	if driveMenuOptionEnabled(options, config.DriveMenuShowSize) {
		columns = append(columns,
			driveMenuPlatformColumn{text: row.Total, rightAlign: true},
			driveMenuPlatformColumn{text: row.Free, rightAlign: true})
	}
	if driveMenuOptionEnabled(options, config.DriveMenuShowNetworkName) {
		columns = append(columns, driveMenuPlatformColumn{text: row.network})
	}
	return columns
}

func driveMenuPlatformRowHasDetails(columns []driveMenuPlatformColumn) bool {
	for _, column := range columns {
		if column.text != "" {
			return true
		}
	}
	return false
}

func driveMenuPadColumn(column driveMenuPlatformColumn, width int, last bool) string {
	if last {
		if column.rightAlign {
			return strings.Repeat(" ", width-vtui.StringWidth(column.text)) + column.text
		}
		return column.text
	}
	padding := width - vtui.StringWidth(column.text)
	if column.rightAlign {
		return strings.Repeat(" ", padding) + column.text
	}
	return column.text + strings.Repeat(" ", padding)
}

// driveMenuPlatformRowsText renders the platform rows as Far-style columns.
// Widths are calculated across the complete visible platform list, so a long
// label on one drive no longer makes the following columns jump between rows.
func DriveMenuPlatformRowsText(rows []DriveMenuPlatformRow, options uint32) []string {
	columnWidths := make([]int, 0, 7)
	rowColumns := make([][]driveMenuPlatformColumn, len(rows))
	for i, row := range rows {
		rowColumns[i] = row.columns(options)
		if len(rowColumns[i]) > len(columnWidths) {
			columnWidths = append(columnWidths, make([]int, len(rowColumns[i])-len(columnWidths))...)
		}
		for column, value := range rowColumns[i] {
			if width := vtui.StringWidth(value.text); width > columnWidths[column] {
				columnWidths[column] = width
			}
		}
	}

	texts := make([]string, len(rows))
	for i, row := range rows {
		columns := rowColumns[i]
		if !driveMenuPlatformRowHasDetails(columns) {
			texts[i] = row.Base
			continue
		}
		last := len(columns) - 1
		for column := last; column >= 0; column-- {
			if columns[column].text != "" {
				last = column
				break
			}
		}
		parts := make([]string, 1, last+2)
		parts[0] = row.Base
		for column := 0; column <= last; column++ {
			parts = append(parts, driveMenuPadColumn(columns[column], columnWidths[column], column == last))
		}
		texts[i] = strings.Join(parts, " | ")
	}
	return texts
}

// driveMenuPlatformItemText is kept for callers and small formatting tests;
// the live menu uses driveMenuPlatformRowsText so all rows share widths.
func DriveMenuPlatformItemText(drv sysinfo.DriveEntry, options uint32) string {
	return DriveMenuPlatformRowsText([]DriveMenuPlatformRow{driveMenuPlatformRowFor(drv, options)}, options)[0]
}

func DriveMenuOptionsDialogSize() (int, int) {
	width := vtui.StringWidth(i18n.Msg("Drive.OptionsTitle")) + 6
	for _, spec := range DriveMenuOptionSpecs {
		label, _, _ := vtui.ParseAmpersandString(i18n.Msg(spec.Label))
		// Four columns are the checkbox prefix and four are dialog chrome.
		if candidate := vtui.StringWidth(label) + 8; candidate > width {
			width = candidate
		}
	}
	buttonWidth := vtui.StringWidth(i18n.Msg("vtui.Ok")) + vtui.StringWidth(i18n.Msg("vtui.Cancel")) + 8
	if buttonWidth > width {
		width = buttonWidth
	}
	return width, len(DriveMenuOptionSpecs) + 7
}

func driveMenuPlatformItemVisible(drv sysinfo.DriveEntry, options uint32) bool {
	kind := driveMenuKindFor(drv.Name, driveMenuInfoPath(drv.Name))
	switch kind {
	case driveMenuKindRemovable:
		return driveMenuOptionEnabled(options, config.DriveMenuShowRemovable)
	case driveMenuKindCD:
		return driveMenuOptionEnabled(options, config.DriveMenuShowCD)
	case driveMenuKindRemote:
		return driveMenuOptionEnabled(options, config.DriveMenuShowRemote)
	default:
		return true
	}
}

func (pf *PanelsFrame) openDriveMenuOptions(panelIdx int, menu *vtui.VMenu) {
	// F9 belongs to the drive menu, so it opens the drive-chooser page on
	// its own rather than the whole Settings Center (#1148). The compact
	// dialog below stays as the fallback when settings are not wired.
	if OpenSettingsCategoryOnly != nil && OpenSettingsCategoryOnly("drives") {
		return
	}
	width, height := DriveMenuOptionsDialogSize()
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Drive.OptionsTitle"))
	dlg.ShowClose = true

	options := config.App.DriveMenuOptions
	checks := make([]*vtui.Checkbox, 0, len(DriveMenuOptionSpecs))
	for _, spec := range DriveMenuOptionSpecs {
		check := vtui.NewCheckbox(0, 0, i18n.Msg(spec.Label), false)
		if driveMenuOptionEnabled(options, spec.flag) {
			check.State = 1
		}
		checks = append(checks, check)
		dlg.AddItem(check)
	}

	ok := vtui.NewButton(0, 0, i18n.Msg("vtui.Ok"))
	ok.IsDefault = true
	cancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	dlg.AddItem(ok)
	dlg.AddItem(cancel)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
	for _, check := range checks {
		vbox.Add(check, vtui.Margins{}, vtui.AlignLeft)
	}
	buttons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(ok, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(cancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()
	dlg.SetFocusedItem(checks[0])

	cancel.OnClick = func() { dlg.Close() }
	ok.OnClick = func() {
		var updated uint32
		for i, check := range checks {
			if check.State == 1 {
				updated |= DriveMenuOptionSpecs[i].flag
			}
		}
		config.App.DriveMenuOptions = updated
		config.SaveConfig()
		pos := menu.SelectPos
		dlg.Close()
		menu.Close()
		vtui.FrameManager.PostTask(func() { pf.showDriveMenuAt(panelIdx, pos) })
	}

	vtui.FrameManager.Push(dlg)
}
