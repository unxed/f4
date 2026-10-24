package panel

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/dialog"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/sysinfo"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

type driveMenuKind uint8

const (
	driveMenuKindUnknown driveMenuKind = iota
	driveMenuKindFixed
	driveMenuKindRemovable
	driveMenuKindCD
	driveMenuKindDVD
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
	{config.DriveMenuShowPlugins, "Drive.ShowTools"},
	{config.DriveMenuSortPluginsByHotkey, "Drive.SortToolsByHotkey"},
	{config.DriveMenuShowRemovable, "Drive.ShowRemovable"},
	{config.DriveMenuShowCD, "Drive.ShowCD"},
	{config.DriveMenuShowRemote, "Drive.ShowRemote"},
	{config.DriveMenuDetectVirtual, "Drive.DetectVirtual"},
	{config.DriveMenuShowBookmarks, "Drive.ShowBookmarks"},
	{config.DriveMenuHideLinks, "Drive.ShowLinks"},
}

func driveMenuOptionEnabled(options, flag uint32) bool { return options&flag != 0 }

func driveMenuNameWithoutMarker(name string) string {
	return strings.TrimSpace(strings.ReplaceAll(name, "&", ""))
}

// driveMenuAssignableText renders a row whose accelerator is user-configured
// through F4. Unlike platform drives, these rows intentionally have no
// default letter; PluginMenuItemText keeps the shortcut in a stable column
// and preserves the same one-character accelerator behavior as F11.
// driveMenuBoxChrome is what a row of the drive menu takes beside its text:
// the two borders, the blank before the text and the one blank after the
// longest row (f4#1148: "the right border one character after the last
// character").
const driveMenuBoxChrome = 4

// driveMenuBoxWidth is the width of the drive menu box: the widest row of the
// Drive, Tools and Links sections, with the key a row shows at its right edge,
// plus driveMenuBoxChrome. The rows in skip
// (the bookmarks) do not count: their paths are cut to the box afterwards. A
// section rule or a caption (the title, the key hint at the bottom) is never
// cut, so the box is at least as wide as each of them needs.
func driveMenuBoxWidth(items []vtui.MenuItem, skip map[int]int, captions ...string) int {
	w := 0
	need := func(n int) {
		if n > w {
			w = n
		}
	}
	for _, caption := range captions {
		need(vtui.StringWidth(caption) + 4) // Painter.DrawTitle keeps a title whole in a box 4 wider
	}
	for i, it := range items {
		if _, ok := skip[i]; ok {
			continue
		}
		clean, _, _ := vtui.ParseAmpersandString(it.Text)
		if it.Separator {
			need(vtui.StringWidth(clean) + 6) // the rule draws its caption with a blank on each side
			continue
		}
		if it.Shortcut != "" {
			// A link's global hot key at the right edge, two blanks after
			// the name (f4#2171).
			need(vtui.StringWidth(clean) + driveMenuShortcutGap + vtui.StringWidth(it.Shortcut) + driveMenuBoxChrome)
			continue
		}
		need(vtui.StringWidth(clean) + driveMenuBoxChrome)
	}
	return w
}

// driveMenuShortcutGap is the room between a row's text and the key shown at
// its right edge.
const driveMenuShortcutGap = 2

// driveMenuBookmarkText is the row of the folder bookmark in slot: its hotkey
// digit and the path, cut on the left to width columns.
func driveMenuBookmarkText(slot int, path string, width int) string {
	return fmt.Sprintf("&%d %s", slot, dialog.EscapeAmpersand(dialog.TruncPathLeft(path, width)))
}

func driveMenuAssignableText(actionName, label string) string {
	shortcut := PluginActionConfiguredKey(actionName)
	if shortcut == "" {
		// The hotkey column stays reserved, so the names of the Tools do not
		// jump left and right with a letter being assigned (f4#1148).
		clean, _, _ := vtui.ParseAmpersandString(label)
		return "  " + clean
	}
	return PluginMenuItemText(label, shortcut, 1)
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
		home, _ := hostmode.UserHomeDir()
		return home
	case len(clean) >= 2 && clean[1] == ':':
		// GetDiskFreeSpaceEx and GetVolumeInformation both want a root.
		return clean[:2] + string(os.PathSeparator)
	default:
		return ""
	}
}

// driveMenuInfoPathFor is driveMenuInfoPath extended for entries that carry
// their own path instead of one the menu has to derive from Name: a live
// mount-point row (f4#415) sets DriveEntry.InfoPath to its mount point, so
// its free space and filesystem type come from the same sysinfo.FS lookup
// the built-in "/ Root" and "~ Home" rows already use.
func driveMenuInfoPathFor(drv sysinfo.DriveEntry) string {
	if drv.InfoPath != "" {
		return drv.InfoPath
	}
	return driveMenuInfoPath(drv.Name)
}

// isPhysicalDisksDrive reports whether a platform drive is the Physical Disks
// provider, listed among the tools of the drive menu (f4#1148).
func isPhysicalDisksDrive(name string) bool {
	return strings.Contains(strings.ToLower(name), "physical disk")
}

func driveMenuKindFor(name, path string) driveMenuKind {
	if isPhysicalDisksDrive(name) {
		return driveMenuKindPhysical
	}
	if path == "" {
		return driveMenuKindUnknown
	}
	return driveMenuPlatformKind(path)
}

// fileDeviceDVD is FILE_DEVICE_DVD, the device type an optical drive that
// reads DVDs reports for IOCTL_STORAGE_GET_MEDIA_TYPES_EX; a CD-only drive
// reports FILE_DEVICE_CD_ROM.
const fileDeviceDVD = 0x33

// driveMenuOpticalKind tells a DVD drive from a CD drive by the device type
// the drive reports, so that the menu says DVD-ROM rather than CD-ROM for it
// (mitek1977-coder, f4#1148). A drive that does not answer stays a CD.
func driveMenuOpticalKind(deviceType uint32, known bool) driveMenuKind {
	if known && deviceType == fileDeviceDVD {
		return driveMenuKindDVD
	}
	return driveMenuKindCD
}

func driveMenuKindLabel(kind driveMenuKind) string {
	switch kind {
	case driveMenuKindFixed:
		return i18n.Msg("Drive.TypeFixed")
	case driveMenuKindRemovable:
		return i18n.Msg("Drive.TypeRemovable")
	case driveMenuKindCD:
		return i18n.Msg("Drive.TypeCD")
	case driveMenuKindDVD:
		return i18n.Msg("Drive.TypeDVD")
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
// driveMenuRowKind and driveMenuRowFS are driveMenuKindFor and sysinfo.FS;
// variables so a test can give a drive a kind and a file system.
var (
	driveMenuRowKind = driveMenuKindFor
	driveMenuRowFS   = sysinfo.FS
	// driveMenuPlatformDrives is sysinfo.GetPlatformDrives, the Drive section
	// of the menu; a variable for the same reason.
	driveMenuPlatformDrives = sysinfo.GetPlatformDrives
)

func driveMenuPlatformRowFor(drv sysinfo.DriveEntry, options uint32) DriveMenuPlatformRow {
	row := DriveMenuPlatformRow{Base: driveMenuBaseName(drv.Name)}
	path := driveMenuInfoPathFor(drv)
	kind := driveMenuRowKind(drv.Name, path)

	if driveMenuOptionEnabled(options, config.DriveMenuShowType) {
		row.Kind = driveMenuKindLabel(kind)
	}

	info, infoOK := sysinfo.FSInfo{}, false
	if path != "" {
		info, infoOK = driveMenuRowFS(path)
	}
	if infoOK {
		parameters := driveMenuKindShowsParameters(kind, options)
		if parameters && driveMenuOptionEnabled(options, config.DriveMenuShowLabel) && info.Label != "" {
			row.Label = info.Label
		}
		if parameters && driveMenuOptionEnabled(options, config.DriveMenuShowFilesystem) && info.Type != "" {
			row.Filesystem = info.Type
		}
		if parameters && driveMenuOptionEnabled(options, config.DriveMenuShowSize) {
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

// driveMenuPadColumn aligns one column of a drive row. The padding is worked
// out from the text as it reads on screen, and only then is the text escaped
// for the menu: a volume label such as "R&D" would otherwise be taken for a
// hotkey marker, lose its ampersand and underline the next letter (f4#1148).
func driveMenuPadColumn(column driveMenuPlatformColumn, width int, last bool) string {
	text := dialog.EscapeAmpersand(column.text)
	if last {
		if column.rightAlign {
			return strings.Repeat(" ", width-vtui.StringWidth(column.text)) + text
		}
		return text
	}
	padding := width - vtui.StringWidth(column.text)
	if column.rightAlign {
		return strings.Repeat(" ", padding) + text
	}
	return text + strings.Repeat(" ", padding)
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
	anyDetails := false
	for _, columns := range rowColumns {
		if driveMenuPlatformRowHasDetails(columns) {
			anyDetails = true
			break
		}
	}
	for i, row := range rows {
		columns := rowColumns[i]
		if !anyDetails || len(columns) == 0 {
			texts[i] = row.Base
			continue
		}
		// Keep every configured column on every drive row. In particular, a
		// removable/CD/remote drive with hidden parameters still needs the same
		// separators as neighboring rows for the columns to read as solid rules.
		last := len(columns) - 1
		parts := make([]string, 1, last+2)
		parts[0] = row.Base
		for column := 0; column <= last; column++ {
			parts = append(parts, driveMenuPadColumn(columns[column], columnWidths[column], column == last))
		}
		texts[i] = strings.Join(parts, " │ ")
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

// driveMenuKindShowsParameters reports whether the drive menu shows the label,
// the file system and the size of a drive of this kind. "Show removable / CD /
// remote drive parameters" hide only those: the drive itself always stays in
// the list (mitek1977-coder, f4#1148).
func driveMenuKindShowsParameters(kind driveMenuKind, options uint32) bool {
	switch kind {
	case driveMenuKindRemovable:
		return driveMenuOptionEnabled(options, config.DriveMenuShowRemovable)
	case driveMenuKindCD, driveMenuKindDVD:
		return driveMenuOptionEnabled(options, config.DriveMenuShowCD)
	case driveMenuKindRemote:
		return driveMenuOptionEnabled(options, config.DriveMenuShowRemote)
	default:
		return true
	}
}

// openDriveMenuTools is what F9 does in the drive menu: the settings window of
// the drive chooser with its pages, Drive options, Tools, Bookmarks and Links,
// without a window of the tools in between (f4#1148).
func (pf *PanelsFrame) openDriveMenuTools(panelIdx int, menu *vtui.VMenu) {
	pf.openDriveMenuOptions(panelIdx, menu)
}

// driveMenuBelowSettings rebuilds the drive menu the drive chooser's settings
// window was opened from (see RefreshDriveMenuBelow); nil when no such window
// was opened from a drive menu.
var driveMenuBelowSettings func(settings vtui.Frame)

// RefreshDriveMenuBelow is called by the drive chooser's settings window after
// Apply or OK: the drive menu under it is rebuilt from the settings just
// written, while the window stays open on top (mitek1977-coder, f4#1148). It
// does nothing unless that menu is right under the window.
func RefreshDriveMenuBelow(settings vtui.Frame) {
	if driveMenuBelowSettings != nil {
		driveMenuBelowSettings(settings)
	}
}

// driveMenuWatchEvery is how often an open drive menu looks for drives that
// came or went: a USB stick plugged in or pulled out (mitek1977-coder,
// f4#1148). 0 turns the watch off.
var driveMenuWatchEvery = 2 * time.Second

// driveListSignature is what the drive menu compares to see that the drives
// changed: their names and the paths it reads their parameters from.
func driveListSignature(drives []sysinfo.DriveEntry) string {
	var b strings.Builder
	for _, d := range drives {
		b.WriteString(d.Name)
		b.WriteByte(0)
		b.WriteString(d.InfoPath)
		b.WriteByte(0)
	}
	return b.String()
}

// watchDriveList looks at the drives every driveMenuWatchEvery while menu is
// open and rebuilds it when they change (refreshDriveMenuIfChanged). The list
// is read off the UI goroutine, since a slow drive may keep it waiting; the
// menu is rebuilt on it.
func (pf *PanelsFrame) watchDriveList(panelIdx int, menu *vtui.VMenu, signature string) {
	fm, every, list := vtui.FrameManager, driveMenuWatchEvery, driveMenuPlatformDrives
	if fm == nil || every <= 0 {
		return
	}
	var stopped atomic.Bool
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for range ticker.C {
			// The task that ends the watch runs on the frame manager; one
			// that was shut down runs no more tasks, so it would never come
			// and the ticker would read the drives forever.
			if stopped.Load() || fm.IsShutdown() {
				return
			}
			current := driveListSignature(list())
			fm.PostTask(func() {
				if !pf.refreshDriveMenuIfChanged(panelIdx, menu, signature, current) {
					stopped.Store(true)
				}
			})
		}
	}()
}

// refreshDriveMenuIfChanged rebuilds menu when the drives changed from
// signature to current, keeping the cursor on the row it was on and any
// window opened over the menu (the settings of the drive chooser) on top of
// it. It reports whether menu should still be watched: false once it is
// closed or replaced by the rebuilt one, which has a watch of its own.
func (pf *PanelsFrame) refreshDriveMenuIfChanged(panelIdx int, menu *vtui.VMenu, signature, current string) bool {
	fm := vtui.FrameManager
	if fm == nil || menu.IsDone() {
		return false
	}
	frames := fm.GetActiveFrames(fm.ActiveIdx)
	at := -1
	for i, f := range frames {
		if dm, ok := f.(*DriveMenuFrame); ok && dm.VMenu == menu {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	if current == signature {
		return true
	}
	row := ""
	if menu.SelectPos >= 0 && menu.SelectPos < len(menu.Items) {
		row = menu.Items[menu.SelectPos].Text
	}
	above := append([]vtui.Frame(nil), frames[at+1:]...)
	for i := len(above) - 1; i >= 0; i-- {
		fm.RemoveFrame(above[i])
	}
	fm.RemoveFrame(frames[at])
	pf.showDriveMenuAt(panelIdx, menu.SelectPos)
	if rebuilt, ok := fm.GetTopFrame().(*DriveMenuFrame); ok {
		for i, it := range rebuilt.Items {
			if row != "" && it.Text == row {
				rebuilt.SetSelectPos(i)
				break
			}
		}
	}
	for _, f := range above {
		fm.Push(f)
	}
	return false
}

func (pf *PanelsFrame) openDriveMenuOptions(panelIdx int, menu *vtui.VMenu) {
	// The menu is built once, from the settings of the moment: rebuild it
	// when the window opened below applies new ones. The window is taken off
	// the stack for that, not closed, and put back on top of the new menu.
	current := menu
	driveMenuBelowSettings = func(settings vtui.Frame) {
		fm := vtui.FrameManager
		if fm == nil {
			return
		}
		frames := fm.GetActiveFrames(fm.ActiveIdx)
		n := len(frames)
		if n < 2 || frames[n-1] != settings {
			return
		}
		// Not the menu F9 was pressed in: watchDriveList may have rebuilt
		// it since, when a drive came or went.
		under, ok := frames[n-2].(*DriveMenuFrame)
		if !ok || under.IsDone() {
			return
		}
		current = under.VMenu
		pos := current.SelectPos
		fm.RemoveFrame(settings)
		fm.RemoveFrame(under)
		pf.showDriveMenuAt(panelIdx, pos)
		if rebuilt, ok := fm.GetTopFrame().(*DriveMenuFrame); ok {
			current = rebuilt.VMenu
		}
		fm.Push(settings)
	}
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
