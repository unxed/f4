package dialog

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"os/user"
	"strconv"

	"github.com/unxed/f4/internal/fileops"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/f4/vfs/hostmode"
	"github.com/unxed/vtui"
)

type AttributesTarget struct {
	Path string
	Item vfs.VFSItem
}

func ShowAttributesDialog(refresh func(), v vfs.VFS, path string, item vfs.VFSItem) {
	if litem, err := vfs.Lstat(context.Background(), v, path); err == nil && litem.IsSymlink {
		item = litem
	}
	ShowAttributesDialogForTargets(refresh, v, []AttributesTarget{{Path: path, Item: item}})
}

func ShowAttributesDialogForTargets(refresh func(), v vfs.VFS, targets []AttributesTarget) {
	if v == nil || len(targets) == 0 {
		return
	}
	caps := v.GetCapabilities()
	if !caps.HasUnixPermissions {
		ShowAttributesWindowsForTargets(refresh, v, targets)
	} else {
		ShowAttributesUnixForTargets(refresh, v, targets)
	}
}

// unixAttributesEdit is what Set writes. A field the user left untouched is
// not in it, and every object keeps its own value there. With a multiple
// selection the dialog has no single value to write back: writing the first
// object's owner, group or time to the rest is exactly what it must not do.
// For one object an untouched time field would still round the modification
// time down to the whole seconds the field shows.
type unixAttributesEdit struct {
	setUid   bool
	uid      int
	setGid   bool
	gid      int
	setMTime bool
	mtime    time.Time
	// mode holds the new mode bits, keepMode the bits each object keeps.
	mode     uint32
	keepMode uint32
}

func setUnixAttributesForTargets(ctx context.Context, v vfs.VFS, targets []AttributesTarget, edit unixAttributesEdit) error {
	for _, target := range targets {
		item := target.Item
		if edit.setUid {
			item.Uid = edit.uid
		}
		if edit.setGid {
			item.Gid = edit.gid
		}
		if edit.setMTime {
			item.MTime = edit.mtime
		}
		item.UnixMode = (item.UnixMode & edit.keepMode) | (edit.mode &^ edit.keepMode)
		if err := v.SetAttributes(ctx, target.Path, item); err != nil {
			return fmt.Errorf("%s: %w", target.Path, err)
		}
	}
	return nil
}

func ShowSymlinkTargetDialog(refresh func(), v vfs.VFS, path, target string) {
	const width, height = 72, 9
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("SymlinkEdit.Title"))
	dlg.ShowClose = true

	fileText := vtui.NewText(0, 0,
		fmt.Sprintf(i18n.Msg("SymlinkEdit.File"), vtui.TruncateMiddle(v.Base(path), width-8)),
		vtui.Palette[vtui.ColDialogText])
	editTarget := vtui.NewEdit(0, 0, width-10, target)
	lblTarget := vtui.NewLabel(0, 0, i18n.Msg("SymlinkEdit.Target"), editTarget)
	btnSave := vtui.NewButton(0, 0, i18n.Msg("SymlinkEdit.Save"))
	btnSave.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("SymlinkEdit.Cancel"))

	dlg.AddItem(fileText)
	dlg.AddItem(lblTarget)
	dlg.AddItem(editTarget)
	dlg.AddItem(btnSave)
	dlg.AddItem(btnCancel)

	btnSave.OnClick = func() {
		newTarget := editTarget.GetText()
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			if err := ReplaceSymlinkTarget(ctx.Context, v, path, newTarget); err != nil {
				ctx.RunOnUI(func() {
					vtui.ShowMessage(i18n.Msg("SymlinkEdit.ErrorTitle"), err.Error(), []string{"&Ok"})
				})
				return
			}
			ctx.RunOnUI(func() {
				dlg.Close()
				if refresh != nil {
					refresh()
				}
			})
		})
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-3)
	vbox.Add(fileText, vtui.Margins{}, vtui.AlignLeft)
	rowTarget := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowTarget.Add(lblTarget, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowTarget.Add(editTarget, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowTarget, vtui.Margins{Top: 1}, vtui.AlignFill)
	rowButtons := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowButtons.HorizontalAlign = vtui.AlignCenter
	rowButtons.Spacing = 2
	rowButtons.Add(btnSave, vtui.Margins{}, vtui.AlignTop)
	rowButtons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowButtons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()
	dlg.SetFocusedItem(editTarget)
	vtui.FrameManager.Push(dlg)
}

// ReplaceSymlinkTarget changes the link itself, never the object it points at.
// The new link is created only after the old one has been removed because the
// optional VFS API does not promise replace semantics. If creation fails, put
// the original link back before returning the error so a failed edit cannot
// silently delete the user's link.
func ReplaceSymlinkTarget(ctx context.Context, v vfs.VFS, path, newTarget string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if newTarget == "" {
		return errors.New("symlink target cannot be empty")
	}
	symVFS, ok := v.(vfs.SymlinkVFS)
	if !ok {
		return errors.New("VFS does not support symbolic links")
	}
	oldTarget, err := symVFS.Readlink(ctx, path)
	if err != nil {
		return fmt.Errorf("read symlink %q: %w", path, err)
	}
	if oldTarget == newTarget {
		return nil
	}
	if err := v.Remove(ctx, path); err != nil {
		return fmt.Errorf("remove symlink %q: %w", path, err)
	}
	createErr := symVFS.Symlink(ctx, newTarget, path)
	if createErr == nil {
		return nil
	}
	if restoreErr := symVFS.Symlink(ctx, oldTarget, path); restoreErr != nil {
		return fmt.Errorf("create symlink %q: %w; restore original target %q: %v", path, createErr, oldTarget, restoreErr)
	}
	return fmt.Errorf("create symlink %q: %w (original target restored)", path, createErr)
}

// windowsAttributesEdit is the Windows dialog's counterpart of
// unixAttributesEdit: only what the user changed, the rest stays per object.
type windowsAttributesEdit struct {
	setMTime bool
	mtime    time.Time
	// winAttrs holds the new ordinary flags, keepWinAttrs the ordinary flags
	// each object keeps.
	winAttrs     uint32
	keepWinAttrs uint32
	// setUnixMode is set where Read only is carried by the mode as well
	// (native Windows semantics); the mode then follows the Read only box.
	setUnixMode bool
	unixMode    uint32
}

func setWindowsAttributesForTargets(ctx context.Context, v vfs.VFS, targets []AttributesTarget, edit windowsAttributesEdit) error {
	const editableWinAttrs = uint32(1 | 2 | 4 | 32)
	for _, target := range targets {
		item := target.Item
		if edit.setMTime {
			item.MTime = edit.mtime
		}
		if edit.setUnixMode {
			item.UnixMode = edit.unixMode
		}
		// The dialog edits only the four ordinary Windows flags. Keep
		// directory/reparse/compression and other provider-specific flags from
		// each target instead of copying those of the first selected object.
		editable := editableWinAttrs &^ edit.keepWinAttrs
		item.WinAttrs = (item.WinAttrs &^ editable) | (edit.winAttrs & editable)
		if err := v.SetAttributes(ctx, target.Path, item); err != nil {
			return fmt.Errorf("%s: %w", target.Path, err)
		}
	}
	return nil
}

// attributesTimeFormat is the format of the dialogs' time fields, as a Go
// reference-time layout (used for both parsing and rendering the current
// value). It is unfit for showing the user as "the expected format" on a
// parse error: Go's reference date "02.01.2006 15:04:05" reads as a
// specific past date/time to anyone who does not know Go's layout
// convention, not as a pattern to follow (f4#1404) — the error message
// below uses the localized i18n.Msg("Attributes.MTimeFormatHint") instead.
const attributesTimeFormat = "02.01.2006 15:04:05"

// attributesSelectionSummary names a multiple selection the way far2l's
// attributes dialog does, "selected 3 items (dirs: 1, files: 2)", so the
// dialog describes what Set will change instead of naming its first object.
func attributesSelectionSummary(targets []AttributesTarget) string {
	var dirs, files, symlinks int
	for _, target := range targets {
		switch {
		case target.Item.IsSymlink:
			symlinks++
		case target.Item.IsDir:
			dirs++
		default:
			files++
		}
	}
	var kinds []string
	for _, kind := range []struct {
		count int
		key   string
	}{
		{dirs, "Attributes.SelectedDirs"},
		{files, "Attributes.SelectedFiles"},
		{symlinks, "Attributes.SelectedSymlinks"},
	} {
		if kind.count > 0 {
			kinds = append(kinds, fmt.Sprintf(i18n.Msg(kind.key), kind.count))
		}
	}
	return fmt.Sprintf(i18n.Msg("Attributes.SelectedCount"), len(targets), strings.Join(kinds, ", "))
}

// sharedAttributeText shows the value all objects have in common, or
// "(multiple values)" when they differ, as far2l does for owner and group.
func sharedAttributeText(targets []AttributesTarget, value func(vfs.VFSItem) int, name func(int) string) string {
	first := value(targets[0].Item)
	for _, target := range targets[1:] {
		if value(target.Item) != first {
			return i18n.Msg("Attributes.MultipleValues")
		}
	}
	return name(first)
}

func unixOwnerName(uid int) string {
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		return u.Username
	}
	return name
}

func unixGroupName(gid int) string {
	name := strconv.Itoa(gid)
	if g, err := user.LookupGroupId(name); err == nil {
		return g.Name
	}
	return name
}

// lookupUnixUid resolves the Owner field: a user name, or a numeric id.
func lookupUnixUid(text string) (int, bool) {
	if u, err := user.Lookup(text); err == nil {
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			return uid, true
		}
	}
	uid, err := strconv.Atoi(text)
	return uid, err == nil
}

// lookupUnixGid resolves the Group field: a group name, or a numeric id.
func lookupUnixGid(text string) (int, bool) {
	if g, err := user.LookupGroup(text); err == nil {
		if gid, err := strconv.Atoi(g.Gid); err == nil {
			return gid, true
		}
	}
	gid, err := strconv.Atoi(text)
	return gid, err == nil
}

// octalMixedDigit stands in the Octal field for a digit whose bits differ
// across a multiple selection; far2l uses the same character there. A digit
// written this way keeps its bits on every object.
const octalMixedDigit = '-'

// modeBits lists the permission bits in checkbox order: read, write and
// execute for user, group and other.
var modeBits = []uint32{0400, 0200, 0100, 0040, 0020, 0010, 0004, 0002, 0001}

// parseOctalModeText reads the Octal field. Text shorter than four positions
// is right-aligned, so "755" means 0755. mode holds the bits of the digits
// written, mixed the bits of the positions written as octalMixedDigit.
func parseOctalModeText(text string) (mode, mixed uint32, ok bool) {
	if len(text) > 4 {
		return 0, 0, false
	}
	padded := strings.Repeat("0", 4-len(text)) + text
	for i := 0; i < 4; i++ {
		shift := 3 * (3 - i)
		switch c := padded[i]; {
		case c >= '0' && c <= '7':
			mode |= uint32(c-'0') << shift
		case c == octalMixedDigit:
			mixed |= 7 << shift
		default:
			return 0, 0, false
		}
	}
	return mode, mixed, true
}

// formatOctalModeText writes the Octal field from the permission checkboxes.
// The set-id and sticky digit has no checkboxes, so it is carried over:
// special holds its bits, specialMixed says the digit is mixed.
func formatOctalModeText(checks []*vtui.Checkbox, special uint32, specialMixed bool) string {
	const octalDigits = "01234567"
	digits := []byte{octalDigits[(special>>9)&7], 0, 0, 0}
	if specialMixed {
		digits[0] = octalMixedDigit
	}
	for triple := 0; triple < 3; triple++ {
		var value byte
		mixed := false
		for i, check := range checks[triple*3 : triple*3+3] {
			switch check.State {
			case 1:
				value |= 4 >> i
			case 2:
				mixed = true
			}
		}
		digits[triple+1] = '0' + value
		if mixed {
			digits[triple+1] = octalMixedDigit
		}
	}
	return string(digits)
}

// unixModeEdit turns the Octal field and the checkboxes into the mode bits
// Set writes and the bits each object keeps. A digit in the field decides its
// three bits. A digit written as octalMixedDigit leaves its bits to the
// checkboxes, where a '?' box keeps its bit per object; the set-id and sticky
// digit has no boxes, so written that way it keeps all three.
func unixModeEdit(octal string, checks []*vtui.Checkbox) (mode, keep uint32) {
	mode, mixed, ok := parseOctalModeText(octal)
	if !ok {
		mode, mixed = 0, 07777
	}
	keep = mixed & 07000
	for i, check := range checks {
		bit := modeBits[i]
		if mixed&bit == 0 {
			continue
		}
		mode &^= bit
		switch check.State {
		case 1:
			mode |= bit
		case 2:
			keep |= bit
		}
	}
	return mode, keep
}

// mixedOctalValidator guards the Octal field of a multiple selection: octal
// digits, and octalMixedDigit for a digit that keeps each object's bits. A
// single object has nothing mixed and keeps vtui.OctalValidator.
type mixedOctalValidator struct{}

func (mixedOctalValidator) Validate(s string) bool {
	_, _, ok := parseOctalModeText(s)
	return ok
}

func (v mixedOctalValidator) IsValidInput(s string) bool {
	return v.Validate(s)
}

func (mixedOctalValidator) Error(owner vtui.Frame) {
	vtui.ShowMessageOn(owner, i18n.Msg("Error.Title"), i18n.Msg("Attributes.OctalMixedError"), []string{i18n.Msg("vtui.Ok")})
}

// windowsAdvancedFlags are the flags the Windows dialog shows but does not edit.
var windowsAdvancedFlags = []struct {
	bit  uint32
	name string
}{
	{0x00000800, "Compressed"},
	{0x00004000, "Encrypted"},
	{0x00000400, "Reparse Point"},
	{0x00000200, "Sparse"},
	{0x00001000, "Offline"},
	{0x00002000, "Not Content Indexed"},
	{0x00000010, "Directory"},
}

// windowsAdvancedFlagsText lists the advanced flags of the selection. A flag
// only some of the objects have is marked "(?)", the way a mixed checkbox
// shows "?".
func windowsAdvancedFlagsText(targets []AttributesTarget) string {
	winAttrs := func(item vfs.VFSItem) uint32 { return item.WinAttrs }
	var names []string
	for _, flag := range windowsAdvancedFlags {
		switch mixedAttributeState(targets, flag.bit, winAttrs) {
		case 1:
			names = append(names, flag.name)
		case 2:
			names = append(names, flag.name+" (?)")
		}
	}
	if len(names) == 0 {
		return "None"
	}
	return strings.Join(names, ", ")
}

func ShowAttributesUnix(refresh func(), v vfs.VFS, path string, item vfs.VFSItem) {
	ShowAttributesUnixForTargets(refresh, v, []AttributesTarget{{Path: path, Item: item}})
}

func ShowAttributesUnixForTargets(refresh func(), v vfs.VFS, targets []AttributesTarget) {
	path := targets[0].Path
	item := targets[0].Item
	multiple := len(targets) > 1
	width, height := 70, 24
	if item.IsSymlink && !multiple {
		height = 26
	}

	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Attributes.Title"))
	dlg.ShowClose = true

	x, y := dlg.X1, dlg.Y1
	unixMode := func(item vfs.VFSItem) uint32 { return item.UnixMode }

	// Основной контейнер
	mainVBox := vtui.NewVBoxLayout(x+3, y+2, width-6, height-4)

	// Header. As in far2l, a multiple selection is named as a whole, never by
	// its first object.
	header := []string{i18n.Msg("Attributes.ChangeFor")}
	if multiple {
		header = append(header, i18n.Msg("Attributes.SelectedObjects"), attributesSelectionSummary(targets))
	} else {
		header = append(header, vtui.TruncateMiddle(v.Base(path), 60))
	}
	for _, line := range header {
		for _, l := range vtui.WrapText(line, 60) {
			t := vtui.NewText(0, 0, l, vtui.Palette[vtui.ColDialogText])
			dlg.AddItem(t)
			mainVBox.Add(t, vtui.Margins{}, vtui.AlignCenter)
		}
	}

	var editTarget *vtui.Edit
	if item.IsSymlink && !multiple {
		targetVal, _ := vfs.Readlink(context.Background(), v, path)
		editTarget = vtui.NewEdit(0, 0, 35, targetVal)
		lblTarget := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.Target")), editTarget)
		rowTarget := vtui.NewHBoxLayout(0, 0, 66, 1)
		rowTarget.Add(lblTarget, vtui.Margins{Left: 2, Right: 1}, vtui.AlignLeft)
		rowTarget.Add(editTarget, vtui.Margins{}, vtui.AlignFill)
		dlg.AddItem(lblTarget)
		dlg.AddItem(editTarget)
		mainVBox.Add(rowTarget, vtui.Margins{Top: 1}, vtui.AlignFill)
	}

	// Ownership Group
	gbOwnership := vtui.NewGroupBox(0, 0, 66, 4, " "+i18n.Msg("Attributes.Ownership")+" ")
	dlg.AddItem(gbOwnership)
	mainVBox.Add(gbOwnership, vtui.Margins{Top: 1}, vtui.AlignFill)

	// Permissions Group
	gbPerms := vtui.NewGroupBox(0, 0, 66, 7, " "+i18n.Msg("Attributes.Permissions")+" ")
	dlg.AddItem(gbPerms)
	mainVBox.Add(gbPerms, vtui.Margins{Top: 0}, vtui.AlignFill)

	// Time Row. far2l leaves the dates of a multiple selection blank; a blank
	// field left blank changes nothing.
	initialMTime := ""
	if !multiple {
		initialMTime = item.MTime.Format(attributesTimeFormat)
	}
	editMTime := vtui.NewEdit(0, 0, 20, initialMTime)
	lblTime := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.MTime")), editMTime)
	rowTime := vtui.NewHBoxLayout(0, 0, 66, 1)
	rowTime.Add(lblTime, vtui.Margins{Left: 2, Right: 1}, vtui.AlignLeft)
	rowTime.Add(editMTime, vtui.Margins{}, vtui.AlignLeft)
	dlg.AddItem(lblTime)
	dlg.AddItem(editMTime)
	mainVBox.Add(rowTime, vtui.Margins{Top: 0}, vtui.AlignFill)

	// Buttons
	btnSet := vtui.NewButton(0, 0, i18n.Msg("Attributes.BtnSet"))
	btnSet.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))
	rowBtns := vtui.NewHBoxLayout(0, 0, 66, 1)
	rowBtns.HorizontalAlign = vtui.AlignCenter
	rowBtns.Spacing = 2
	rowBtns.Add(btnSet, vtui.Margins{}, vtui.AlignTop)
	rowBtns.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	dlg.AddItem(btnSet)
	dlg.AddItem(btnCancel)
	mainVBox.Add(rowBtns, vtui.Margins{Top: 1}, vtui.AlignFill)

	// --- ПЕРВЫЙ ПРОХОД: Позиционируем контейнеры в диалоге ---
	mainVBox.Apply()
	rowTime.Apply()
	rowBtns.Apply()

	// --- ВТОРОЙ ПРОХОД: Наполняем уже спозиционированные GroupBox ---

	// Наполнение Ownership
	initialOwner := sharedAttributeText(targets, func(item vfs.VFSItem) int { return item.Uid }, unixOwnerName)
	initialGroup := sharedAttributeText(targets, func(item vfs.VFSItem) int { return item.Gid }, unixGroupName)
	editOwner := vtui.NewEdit(0, 0, 20, initialOwner)
	editGroup := vtui.NewEdit(0, 0, 20, initialGroup)

	vboxOwner := vtui.NewVBoxLayout(gbOwnership.X1+2, gbOwnership.Y1+1, gbOwnership.X2-gbOwnership.X1-4, 2)

	r1 := vtui.NewHBoxLayout(0, 0, 60, 1)
	l1 := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.Owner")), editOwner)
	r1.Add(l1, vtui.Margins{Right: 1}, vtui.AlignLeft)
	r1.Add(editOwner, vtui.Margins{}, vtui.AlignFill)
	gbOwnership.AddItem(l1)
	gbOwnership.AddItem(editOwner)
	vboxOwner.Add(r1, vtui.Margins{}, vtui.AlignFill)

	r2 := vtui.NewHBoxLayout(0, 0, 60, 1)
	l2 := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.Group")), editGroup)
	r2.Add(l2, vtui.Margins{Right: 1}, vtui.AlignLeft)
	r2.Add(editGroup, vtui.Margins{}, vtui.AlignFill)
	gbOwnership.AddItem(l2)
	gbOwnership.AddItem(editGroup)
	gbOwnership.SetFocus(false)
	vboxOwner.Add(r2, vtui.Margins{Top: 0}, vtui.AlignFill)
	vboxOwner.Apply()
	r1.Apply()
	r2.Apply()

	// Наполнение Permissions
	vboxPerms := vtui.NewVBoxLayout(gbPerms.X1+2, gbPerms.Y1+1, gbPerms.X2-gbPerms.X1-4, 5)
	allChecks := []*vtui.Checkbox{}

	makeRow := func(label string, bitOff uint) {
		row := vtui.NewHBoxLayout(0, 0, 60, 1)
		lbl := vtui.NewText(0, 0, PadLabel(label), vtui.Palette[vtui.ColDialogText])
		r := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Read"), multiple)
		r.State = mixedAttributeState(targets, uint32(0400>>bitOff), unixMode)
		w := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Write"), multiple)
		w.State = mixedAttributeState(targets, uint32(0200>>bitOff), unixMode)
		x_ := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Execute"), multiple)
		x_.State = mixedAttributeState(targets, uint32(0100>>bitOff), unixMode)
		row.Add(lbl, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(r, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(w, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(x_, vtui.Margins{}, vtui.AlignLeft)
		gbPerms.AddItem(lbl)
		gbPerms.AddItem(r)
		gbPerms.AddItem(w)
		gbPerms.AddItem(x_)
		vboxPerms.Add(row, vtui.Margins{}, vtui.AlignFill)
		allChecks = append(allChecks, r, w, x_)
		row.Apply()
	}
	makeRow(i18n.Msg("Attributes.PermUser"), 0)
	makeRow(i18n.Msg("Attributes.PermGroup"), 3)
	makeRow(i18n.Msg("Attributes.PermOther"), 6)

	var editOctal *vtui.Edit
	if multiple {
		specialMixed := false
		for _, bit := range []uint32{04000, 02000, 01000} {
			specialMixed = specialMixed || mixedAttributeState(targets, bit, unixMode) == 2
		}
		editOctal = vtui.NewEdit(0, 0, 6, formatOctalModeText(allChecks, item.UnixMode&07000, specialMixed))
		editOctal.Validator = mixedOctalValidator{}
	} else {
		editOctal = vtui.NewEdit(0, 0, 6, fmt.Sprintf("%04o", item.UnixMode))
		editOctal.Validator = &vtui.OctalValidator{MaxDigits: 4}
	}
	editOctal.ClearSelection()
	rowOct := vtui.NewHBoxLayout(0, 0, 60, 1)
	lblOct := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.Octal")), editOctal)
	rowOct.Add(lblOct, vtui.Margins{Right: 2}, vtui.AlignLeft)
	rowOct.Add(editOctal, vtui.Margins{}, vtui.AlignLeft)
	gbPerms.AddItem(lblOct)
	gbPerms.AddItem(editOctal)
	gbPerms.SetFocus(false)
	vboxPerms.Add(rowOct, vtui.Margins{Top: 1}, vtui.AlignFill)
	vboxPerms.Apply()
	for _, itm := range vboxPerms.Items {
		if h, ok := itm.Element.(*vtui.HBoxLayout); ok {
			h.Apply()
		}
	}

	// Синхронизация чекбоксов и поля Octal. The set-id and sticky digit has
	// no checkboxes, so a checkbox change carries it over rather than
	// resetting it to 0.
	syncing := false
	updateOct := func() {
		if syncing {
			return
		}
		syncing = true
		mode, mixed, ok := parseOctalModeText(editOctal.GetText())
		if !ok {
			mode, mixed = 0, 0
		}
		editOctal.SetText(formatOctalModeText(allChecks, mode&07000, mixed&07000 != 0))
		syncing = false
		vtui.FrameManager.Redraw()
	}
	for _, c := range allChecks {
		c.OnChange = func(int) { updateOct() }
	}
	editOctal.OnTextChange = func(s string) {
		if syncing {
			return
		}
		mode, mixed, ok := parseOctalModeText(s)
		if !ok {
			return
		}
		syncing = true
		for i, c := range allChecks {
			switch {
			case mixed&modeBits[i] != 0 && c.ThreeState:
				c.State = 2
			case mode&modeBits[i] != 0:
				c.State = 1
			default:
				c.State = 0
			}
		}
		syncing = false
		vtui.FrameManager.Redraw()
	}

	targetEdited := item.IsSymlink && editTarget != nil && !multiple

	btnSet.OnClick = func() {
		newTarget := ""
		if targetEdited {
			newTarget = editTarget.GetText()
		}
		var edit unixAttributesEdit
		if text := editOwner.GetText(); text != initialOwner {
			edit.uid, edit.setUid = lookupUnixUid(text)
		}
		if text := editGroup.GetText(); text != initialGroup {
			edit.gid, edit.setGid = lookupUnixGid(text)
		}
		if text := editMTime.GetText(); text != initialMTime {
			t, err := time.ParseInLocation(attributesTimeFormat, text, time.Local)
			if err != nil {
				// f4 #1404: a garbled date used to be silently dropped —
				// nothing applied, nothing said why.
				vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
				return
			}
			edit.mtime, edit.setMTime = t, true
		}
		edit.mode, edit.keepMode = unixModeEdit(editOctal.GetText(), allChecks)
		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			if targetEdited {
				if err := ReplaceSymlinkTarget(ctx.Context, v, path, newTarget); err != nil {
					ctx.RunOnUI(func() {
						vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
					})
					return
				}
			}
			err := setUnixAttributesForTargets(ctx.Context, v, targets, edit)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
				} else {
					dlg.Close()
					if refresh != nil {
						refresh()
					}
				}
			})
		})
	}
	btnCancel.OnClick = func() { dlg.Close() }
	vtui.FrameManager.Push(dlg)
}

func ShowAttributesWindows(refresh func(), v vfs.VFS, path string, item vfs.VFSItem) {
	ShowAttributesWindowsForTargets(refresh, v, []AttributesTarget{{Path: path, Item: item}})
}

func ShowAttributesWindowsForTargets(refresh func(), v vfs.VFS, targets []AttributesTarget) {
	ShowAttributesWindowsWithPropertiesForTargets(refresh, v, targets, DefaultNativePropertiesOpener)
}

func mixedAttributeState(targets []AttributesTarget, bit uint32, value func(vfs.VFSItem) uint32) int {
	if len(targets) == 0 {
		return 0
	}
	wantSet := value(targets[0].Item)&bit != 0
	for _, target := range targets[1:] {
		if (value(target.Item)&bit != 0) != wantSet {
			return 2
		}
	}
	if wantSet {
		return 1
	}
	return 0
}

var DefaultNativePropertiesOpener = showNativePropertiesOS

// ShowAttributesWindowsWithProperties keeps the native shell boundary
// injectable. In particular, UI tests must not invoke ShellExecute: it can
// outlive a test's temporary directory and make Windows display an error
// dialog after the test has already completed.
func ShowAttributesWindowsWithProperties(
	refresh func(),
	v vfs.VFS,
	path string,
	item vfs.VFSItem,
	openProperties func(string) error,
) {
	ShowAttributesWindowsWithPropertiesForTargets(refresh, v, []AttributesTarget{{Path: path, Item: item}}, openProperties)
}

func ShowAttributesWindowsWithPropertiesForTargets(
	refresh func(),
	v vfs.VFS,
	targets []AttributesTarget,
	openProperties func(string) error,
) {
	path := targets[0].Path
	item := targets[0].Item
	multiple := len(targets) > 1
	width, height := 60, 22
	dlg := vtui.NewCenteredDialog(width, height, i18n.Msg("Attributes.Title"))
	dlg.ShowClose = true
	x, y := dlg.X1, dlg.Y1

	mainVBox := vtui.NewVBoxLayout(x+3, y+2, width-6, height-4)

	fileText := fmt.Sprintf(i18n.Msg("Attributes.File"), vtui.TruncateMiddle(v.Base(path), 46))
	if multiple {
		fileText = vtui.TruncateMiddle(attributesSelectionSummary(targets), 54)
	}
	lblFile := vtui.NewText(0, 0, fileText, vtui.Palette[vtui.ColDialogText])
	dlg.AddItem(lblFile)
	mainVBox.Add(lblFile, vtui.Margins{}, vtui.AlignLeft)

	gbAttr := vtui.NewGroupBox(0, 0, 54, 6, " "+i18n.Msg("Attributes.Flags")+" ")
	dlg.AddItem(gbAttr)
	mainVBox.Add(gbAttr, vtui.Margins{Top: 1}, vtui.AlignFill)

	gbAdv := vtui.NewGroupBox(0, 0, 54, 3, " "+i18n.Msg("Attributes.AdvancedFlags")+" ")
	dlg.AddItem(gbAdv)
	mainVBox.Add(gbAdv, vtui.Margins{Top: 1}, vtui.AlignFill)

	initialMTime := ""
	if !multiple {
		initialMTime = item.MTime.Format(attributesTimeFormat)
	}
	editMTime := vtui.NewEdit(0, 0, 20, initialMTime)
	lblTime := vtui.NewLabel(0, 0, PadLabel(i18n.Msg("Attributes.LastWrite")), editMTime)
	rowTime := vtui.NewHBoxLayout(0, 0, 54, 1)
	rowTime.Add(lblTime, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowTime.Add(editMTime, vtui.Margins{}, vtui.AlignLeft)
	dlg.AddItem(lblTime)
	dlg.AddItem(editMTime)
	mainVBox.Add(rowTime, vtui.Margins{Top: 1}, vtui.AlignFill)

	btnSet := vtui.NewButton(0, 0, i18n.Msg("Attributes.BtnSet"))
	btnSet.IsDefault = true
	btnSec := vtui.NewButton(0, 0, i18n.Msg("Attributes.BtnSecurity"))
	btnCancel := vtui.NewButton(0, 0, i18n.Msg("vtui.Cancel"))

	// The native properties sheet is opened for one path. For a multiple
	// selection that would be the first object's sheet, not the selection's.
	var osPath string
	if fileops.IsLocalOSVFS(v) && !multiple {
		if abs, err := v.Abs(path); err == nil {
			if runtime.GOOS == "windows" {
				if (len(abs) >= 2 && abs[1] == ':') || strings.HasPrefix(abs, "\\\\") {
					osPath = abs
				}
			} else {
				if strings.HasPrefix(abs, "/") {
					osPath = abs
				}
			}
		}
	}
	if osPath == "" {
		btnSec.SetDisabled(true)
	}

	btnSec.OnClick = func() {
		if osPath != "" && openProperties != nil {
			if err := openProperties(osPath); err != nil {
				vtui.ShowMessage(" Error ", "Cannot open Windows properties: "+err.Error(), []string{"&Ok"})
			}
		}
	}

	rowBtns := vtui.NewHBoxLayout(0, 0, 54, 1)
	rowBtns.HorizontalAlign = vtui.AlignCenter
	rowBtns.Spacing = 2
	rowBtns.Add(btnSet, vtui.Margins{}, vtui.AlignTop)
	rowBtns.Add(btnSec, vtui.Margins{}, vtui.AlignTop)
	rowBtns.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)

	dlg.AddItem(btnSet)
	dlg.AddItem(btnSec)
	dlg.AddItem(btnCancel)
	mainVBox.Add(rowBtns, vtui.Margins{Top: 1}, vtui.AlignFill)

	// Apply first pass
	mainVBox.Apply()
	rowTime.Apply()
	rowBtns.Apply()

	// Apply second pass for GroupBox
	gbVBox := vtui.NewVBoxLayout(gbAttr.X1+2, gbAttr.Y1+1, gbAttr.X2-gbAttr.X1-4, 4)
	chkRO := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.ReadOnly"), multiple)
	chkHD := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Hidden"), multiple)
	chkSY := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.System"), multiple)
	chkAR := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Archive"), multiple)

	chkRO.State = mixedAttributeState(targets, 1, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
	chkHD.State = mixedAttributeState(targets, 2, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
	chkSY.State = mixedAttributeState(targets, 4, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
	chkAR.State = mixedAttributeState(targets, 32, func(item vfs.VFSItem) uint32 { return item.WinAttrs })

	gbAttr.AddItem(chkRO)
	gbAttr.AddItem(chkHD)
	gbAttr.AddItem(chkSY)
	gbAttr.AddItem(chkAR)
	gbVBox.Add(chkRO, vtui.Margins{}, vtui.AlignLeft)
	gbVBox.Add(chkHD, vtui.Margins{}, vtui.AlignLeft)
	gbVBox.Add(chkSY, vtui.Margins{}, vtui.AlignLeft)
	gbVBox.Add(chkAR, vtui.Margins{}, vtui.AlignLeft)
	gbVBox.Apply()

	lblAdv := vtui.NewText(0, 0, vtui.TruncateMiddle(windowsAdvancedFlagsText(targets), 50), vtui.Palette[vtui.ColDialogText])
	gbAdv.AddItem(lblAdv)
	gbAdvVBox := vtui.NewVBoxLayout(gbAdv.X1+2, gbAdv.Y1+1, gbAdv.X2-gbAdv.X1-4, 1)
	gbAdvVBox.Add(lblAdv, vtui.Margins{}, vtui.AlignLeft)
	gbAdvVBox.Apply()

	btnSet.OnClick = func() {
		var edit windowsAttributesEdit
		if text := editMTime.GetText(); text != initialMTime {
			nt, err := time.ParseInLocation(attributesTimeFormat, text, time.Local)
			if err != nil {
				// f4 #1404: a garbled date used to be silently dropped —
				// nothing applied, nothing said why.
				vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
				return
			}
			edit.mtime, edit.setMTime = nt, true
		}

		// Real POSIX semantics apply on a genuine Unix build (runtime.GOOS
		// != "windows") and equally in Wine posix mode (hostmode.Posix());
		// on both, each object's UnixMode already holds its actual rwx bits,
		// and stomping it with a synthetic 0444/0666 derived from the
		// Windows-only "read-only" checkbox would silently discard real
		// per-owner/group/other permissions. Found while wiring Wine posix
		// mode (WINE.md §14.2) but the bug is not Wine-specific:
		// item.WinAttrs is only ever populated on GOOS=windows
		// (vfs/os_vfs_windows.go), so chkRO defaults to unchecked on a
		// native Linux build too, meaning Set already reset every file's
		// mode to 0666 there before this fix.
		posixSemantics := runtime.GOOS != "windows" || hostmode.Posix()
		switch chkRO.State {
		case 2:
			edit.keepWinAttrs |= 1
		case 1:
			edit.winAttrs |= 1
			if !posixSemantics {
				edit.unixMode, edit.setUnixMode = 0444, true
			}
		default:
			if !posixSemantics {
				edit.unixMode, edit.setUnixMode = 0666, true
			}
		}
		for _, flag := range []struct {
			state int
			bit   uint32
		}{
			{chkHD.State, 2},
			{chkSY.State, 4},
			{chkAR.State, 32},
		} {
			switch flag.state {
			case 2:
				edit.keepWinAttrs |= flag.bit
			case 1:
				edit.winAttrs |= flag.bit
			}
		}

		vtui.RunAsync(func(ctx *vtui.TaskContext) {
			err := setWindowsAttributesForTargets(ctx.Context, v, targets, edit)
			ctx.RunOnUI(func() {
				if err != nil {
					vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
					return
				}
				dlg.Close()
				if refresh != nil {
					refresh()
				}
			})
		})
	}
	btnCancel.OnClick = func() { dlg.Close() }
	vtui.FrameManager.Push(dlg)
}
