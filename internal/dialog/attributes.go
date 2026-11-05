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
	// setATime writes the access time the Accessed field holds (f4#1404).
	setATime bool
	atime    time.Time
	// setBTime writes the creation time the Created field holds, where the
	// platform can set one (macOS; vfs.OSVFS.SupportsSetBTime).
	setBTime bool
	btime    time.Time
	// mode holds the new mode bits, keepMode the bits each object keeps.
	mode     uint32
	keepMode uint32
}

// applyUnixAttributesToOne is the single-item primitive: turn edit into the
// object's new VFSItem and write it. It is the one place edit is applied, so
// that setUnixAttributesForTargets (top-level selection) and
// walkUnixAttributesRecursive (f4#1502: recursive Set) do exactly the same
// thing to every object they reach, including symlinks -- Lchown for the
// owner, Chmod (which follows the link) for the mode, exactly as a single
// selected symlink is already handled today via OSVFS.SetAttributes.
func applyUnixAttributesToOne(ctx context.Context, v vfs.VFS, path string, item vfs.VFSItem, edit unixAttributesEdit) error {
	if edit.setUid {
		item.Uid = edit.uid
	}
	if edit.setGid {
		item.Gid = edit.gid
	}
	if edit.setMTime {
		item.MTime = edit.mtime
	}
	if edit.setATime {
		item.ATime = edit.atime
	}
	if edit.setBTime {
		item.BTime, item.SetBTime = edit.btime, true
	}
	item.UnixMode = (item.UnixMode & edit.keepMode) | (edit.mode &^ edit.keepMode)
	// The mode the dialog computed is written even when it is 000 (f4#1981),
	// where the object's own mode was known to compute it from.
	item.SetUnixMode = item.HasMetadata(vfs.MetadataPermissions)
	if err := v.SetAttributes(ctx, path, item); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// setUnixAttributesForTargets applies edit to every selected object and,
// when recursive is set, to everything a selected real directory contains
// (f4#1502 -- "Смена владельца/прав не работает рекурсивно"). applied counts
// every object actually written, selected or descendant, for the caller's
// completion summary. It stops at the first error, same as before this
// object had no recursion at all: a multi-object Set has always stopped
// there, and a recursive one keeps that rather than inventing a
// continue-past-errors policy for just this one path.
func setUnixAttributesForTargets(ctx context.Context, v vfs.VFS, targets []AttributesTarget, edit unixAttributesEdit, recursive bool) (applied int, err error) {
	for _, target := range targets {
		if err := applyUnixAttributesToOne(ctx, v, target.Path, target.Item, edit); err != nil {
			return applied, err
		}
		applied++
		// A symlink is a leaf here even when it resolves to a directory --
		// the same convention f4's own recursive tree walks already use:
		// OSVFS.Remove's os.RemoveAll only ever removes the link itself,
		// and vfs.ScanOptions{FollowSymlinkDirs: false} (QuickView) counts
		// a symlink once instead of walking its target. Recursing through
		// it here would let a Set on one selected folder reach arbitrary
		// files outside that folder, and could loop forever on a symlink
		// that points back into its own tree.
		if recursive && target.Item.IsDir && !target.Item.IsSymlink {
			n, walkErr := walkUnixAttributesRecursive(ctx, v, target.Path, edit, 0)
			applied += n
			if walkErr != nil {
				return applied, walkErr
			}
		}
	}
	return applied, nil
}

// walkUnixAttributesRecursive applies edit to every entry ReadDir finds
// under dirPath, recursing into real subdirectories only (see the symlink
// note on setUnixAttributesForTargets). Each entry is applied through
// applyUnixAttributesToOne, the very primitive the non-recursive Set already
// uses, so a permission error partway through the tree gets exactly the
// same sudo-elevation fallback that OSVFS.SetAttributes already gives a
// single object (the mechanism #1255/#1261 fixed) -- no separate privilege
// path for the recursive case.
func walkUnixAttributesRecursive(ctx context.Context, v vfs.VFS, dirPath string, edit unixAttributesEdit, depth int) (applied int, err error) {
	if depth > 1000 {
		return 0, fmt.Errorf("%s: maximum recursion depth exceeded (circular structure?)", dirPath)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var items []vfs.VFSItem
	if err := v.ReadDir(ctx, dirPath, func(chunk []vfs.VFSItem) {
		items = append(items, chunk...)
	}); err != nil {
		return 0, fmt.Errorf("%s: %w", dirPath, err)
	}
	for _, item := range items {
		if item.Name == "" || item.Name == ".." {
			continue
		}
		if err := ctx.Err(); err != nil {
			return applied, err
		}
		childPath := v.Join(dirPath, item.Name)
		if err := applyUnixAttributesToOne(ctx, v, childPath, item, edit); err != nil {
			return applied, err
		}
		applied++
		if item.IsDir && !item.IsSymlink {
			n, err := walkUnixAttributesRecursive(ctx, v, childPath, edit, depth+1)
			applied += n
			if err != nil {
				return applied, err
			}
		}
	}
	return applied, nil
}

// targetsIncludeRealDir reports whether the recursive checkbox has anything
// to do: it is offered only when a real directory (not a symlink, even one
// that resolves to a directory -- see the symlink note above) is among the
// selected objects.
func targetsIncludeRealDir(targets []AttributesTarget) bool {
	for _, target := range targets {
		if target.Item.IsDir && !target.Item.IsSymlink {
			return true
		}
	}
	return false
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
		confirmDanglingLink(v, path, newTarget, newTarget == target, func() {
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

// linkTargetExists reports whether target, as the link at path sees it
// (relative to the link's folder), exists.
func linkTargetExists(ctx context.Context, v vfs.VFS, path, target string) bool {
	full := target
	if !v.IsAbs(target) {
		full = v.Join(v.Dir(path), target)
	}
	_, err := v.Stat(ctx, full)
	return err == nil
}

// confirmDanglingLink runs proceed, asking first when a symbolic link is
// about to point at nothing: such a link is legal, but saving one silently
// looked like the link had been turned into something strange (DkmS1953,
// f4#1828). skip says there is nothing to ask — the target is unchanged, or
// the link is a junction, whose creation refuses a missing target itself.
func confirmDanglingLink(v vfs.VFS, path, target string, skip bool, proceed func()) {
	if skip || target == "" || linkTargetExists(context.Background(), v, path, target) {
		proceed()
		return
	}
	dlg := vtui.ShowMessage(i18n.Msg("Warning.Title"),
		fmt.Sprintf(i18n.Msg("SymlinkEdit.TargetMissing"), vtui.TruncateMiddle(target, 60)),
		[]string{i18n.Msg("SymlinkEdit.Save"), i18n.Msg("SymlinkEdit.Cancel")})
	dlg.OnResult = func(code int) {
		if code == 0 {
			proceed()
		}
	}
}

// ReplaceSymlinkTarget changes the link itself, never the object it points at.
// The new link is created only after the old one has been removed because the
// optional VFS API does not promise replace semantics. If creation fails, put
// the original link back before returning the error so a failed edit cannot
// silently delete the user's link.
func ReplaceSymlinkTarget(ctx context.Context, v vfs.VFS, path, newTarget string) error {
	return replaceLinkTarget(ctx, v, path, newTarget, false)
}

// ReplaceJunctionTarget is ReplaceSymlinkTarget for a directory junction: the
// link is recreated as a junction.
func ReplaceJunctionTarget(ctx context.Context, v vfs.VFS, path, newTarget string) error {
	return replaceLinkTarget(ctx, v, path, newTarget, true)
}

// replaceLinkTarget is ReplaceSymlinkTarget for either kind of link. A
// directory junction is recreated as a junction (f4#1828): turning it into a
// symbolic link would change its kind and, on Windows, needs a privilege that
// creating a junction does not.
func replaceLinkTarget(ctx context.Context, v vfs.VFS, path, newTarget string, junction bool) error {
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
	create := symVFS.Symlink
	if junction {
		juncVFS, ok := v.(vfs.JunctionVFS)
		if !ok {
			return errors.New("VFS does not support directory junctions")
		}
		create = juncVFS.Junction
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
	createErr := create(ctx, newTarget, path)
	if createErr == nil {
		return nil
	}
	if restoreErr := create(ctx, oldTarget, path); restoreErr != nil {
		return fmt.Errorf("create symlink %q: %w; restore original target %q: %v", path, createErr, oldTarget, restoreErr)
	}
	return fmt.Errorf("create symlink %q: %w (original target restored)", path, createErr)
}

// Windows file attributes the dialog edits through SetFileAttributes besides
// Read only, Hidden, System and Archive (f4#1931).
const (
	winAttrTemporary         = 0x00000100
	winAttrOffline           = 0x00001000
	winAttrNotContentIndexed = 0x00002000
	winAttrPinned            = 0x00080000
	winAttrUnpinned          = 0x00100000
)

// isCloudFileItem reports whether the object belongs to a cloud files
// provider (OneDrive and the like): it carries the cloud reparse tag
// (IO_REPARSE_TAG_CLOUD, 0x9000?01A) or one of the attributes only such files
// have. Pinned and Unpinned mean nothing, and are refused by Windows, on any
// other file.
func isCloudFileItem(item vfs.VFSItem) bool {
	const cloudAttrs = winAttrPinned | winAttrUnpinned | 0x00040000 | 0x00400000
	return item.WinAttrs&cloudAttrs != 0 || item.ReparseTag&0xFFFF0FFF == 0x9000001A
}

// cloudAttributesAvailable reports whether Pinned and Unpinned can be set for
// the whole selection: every object has to be a cloud file.
func cloudAttributesAvailable(targets []AttributesTarget) bool {
	for _, target := range targets {
		if !isCloudFileItem(target.Item) {
			return false
		}
	}
	return len(targets) > 0
}

// applyFlagState records the state of a three-state box in the edit: a mixed
// box, or one that cannot be used, leaves the flag as each object has it.
func (e *windowsAttributesEdit) applyFlagState(bit uint32, state int, unavailable bool) {
	switch {
	case unavailable || state == 2:
		e.keepWinAttrs |= bit
	case state == 1:
		e.winAttrs |= bit
	}
}

// excludeEachOther makes checking either box clear the other one.
func excludeEachOther(a, b *vtui.Checkbox) {
	a.OnChange = func(state int) {
		if state == 1 {
			b.State = 0
		}
	}
	b.OnChange = func(state int) {
		if state == 1 {
			a.State = 0
		}
	}
}

// windowsAttributesEdit is the Windows dialog's counterpart of
// unixAttributesEdit: only what the user changed, the rest stays per object.
type windowsAttributesEdit struct {
	setMTime bool
	mtime    time.Time
	// setATime writes the access time the Accessed field holds (f4#1404).
	setATime bool
	atime    time.Time
	// setBTime writes the creation time the Created field holds, where the
	// platform can set one (vfs.OSVFS.SupportsSetBTime; f4#1404).
	setBTime bool
	btime    time.Time
	// winAttrs holds the new editable flags, keepWinAttrs the flags each
	// object keeps. Most advanced flags are display-only; Not Content Indexed,
	// Temporary, Offline, Pinned and Unpinned are editable through
	// SetFileAttributes.
	winAttrs       uint32
	keepWinAttrs   uint32
	setCompression bool
	compressed     bool
	setSparse      bool
	sparse         bool
	setEncryption  bool
	encrypted      bool
	// setUnixMode is set where Read only is carried by the mode as well
	// (native Windows semantics); the mode then follows the Read only box.
	setUnixMode bool
	unixMode    uint32
}

func setWindowsAttributesForTargets(ctx context.Context, v vfs.VFS, targets []AttributesTarget, edit windowsAttributesEdit) error {
	const editableWinAttrs = uint32(1 | 2 | 4 | 32 | winAttrTemporary | winAttrOffline |
		winAttrNotContentIndexed | winAttrPinned | winAttrUnpinned)
	for _, target := range targets {
		// EFS encryption and NTFS compression are mutually exclusive. When
		// the user switches from encrypted to compressed, decrypt first;
		// when switching the other way, the compression call must come first.
		if edit.setEncryption && !edit.encrypted {
			if err := setWindowsFileEncryption(v, target.Path, false); err != nil {
				return fmt.Errorf("%s: %w", target.Path, err)
			}
		}
		if edit.setSparse {
			if err := setWindowsFileSparse(v, target.Path, edit.sparse); err != nil {
				return fmt.Errorf("%s: %w", target.Path, err)
			}
		}
		if edit.setCompression {
			if err := setWindowsFileCompression(v, target.Path, edit.compressed); err != nil {
				return fmt.Errorf("%s: %w", target.Path, err)
			}
		}
		if edit.setEncryption && edit.encrypted {
			if err := setWindowsFileEncryption(v, target.Path, edit.encrypted); err != nil {
				return fmt.Errorf("%s: %w", target.Path, err)
			}
		}
		item := target.Item
		if edit.setMTime {
			item.MTime = edit.mtime
		}
		if edit.setATime {
			item.ATime = edit.atime
		}
		if edit.setBTime {
			item.BTime, item.SetBTime = edit.btime, true
		}
		if edit.setUnixMode {
			item.UnixMode = edit.unixMode
		}
		// Keep directory/reparse/compression and other provider-specific flags
		// from each target instead of copying those of the first selected one.
		if edit.setCompression {
			item.WinAttrs &^= 0x00000800
			if edit.compressed {
				item.WinAttrs |= 0x00000800
			}
		}
		if edit.setSparse {
			item.WinAttrs &^= 0x00000200
			if edit.sparse {
				item.WinAttrs |= 0x00000200
			}
		}
		if edit.setEncryption {
			item.WinAttrs &^= 0x00004000
			if edit.encrypted {
				item.WinAttrs |= 0x00004000
			}
		}
		editable := editableWinAttrs &^ edit.keepWinAttrs
		item.WinAttrs = (item.WinAttrs &^ editable) | (edit.winAttrs & editable)
		// Pinned and Unpinned exclude each other; the dialog keeps the pair
		// consistent, this refuses a combination that would reach Windows.
		if item.WinAttrs&(winAttrPinned|winAttrUnpinned) == winAttrPinned|winAttrUnpinned {
			return fmt.Errorf("%s: %s", target.Path, i18n.Msg("Attributes.PinnedExclusive"))
		}
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

func parseAttributesTime(text string, utc bool) (time.Time, error) {
	if utc {
		return time.Parse(attributesTimeFormat, text)
	}
	return time.ParseInLocation(attributesTimeFormat, text, time.Local)
}

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

// sharedAttributeTimeText is sharedAttributeText's counterpart for a
// time.Time field that is only sometimes known (Created/Accessed/Changed):
// ok is false when any object in the selection lacks the field at all, so
// the caller omits the row entirely rather than showing a wrong zero time
// for part of the selection. When every object has it, the text is the
// common value, or "(multiple values)" the way owner/group show one.
func sharedAttributeTimeText(targets []AttributesTarget, value func(vfs.VFSItem) time.Time, known func(vfs.VFSItem) bool) (text string, ok bool) {
	for _, target := range targets {
		if !known(target.Item) {
			return "", false
		}
	}
	first := value(targets[0].Item)
	for _, target := range targets[1:] {
		if !value(target.Item).Equal(first) {
			return i18n.Msg("Attributes.MultipleValues"), true
		}
	}
	return first.Format(attributesTimeFormat), true
}

// attributesReadOnlyTimeRow builds a label+value row for a time field the
// dialog only displays (Created/Accessed/Changed): no Edit, no Set-time
// validation, matching the reporter's explicit read-only ask (f4#1404).
// leftMargin mirrors whatever margin the dialog's own M-Time/Last-write row
// uses, so the new rows line up with it (the Unix and Windows-shaped dialogs
// use different values).
func attributesReadOnlyTimeRow(dlg *vtui.Window, mainVBox *vtui.VBoxLayout, width, leftMargin int, label, text string) *vtui.HBoxLayout {
	lbl := vtui.NewText(0, 0, PadLabel(label), vtui.Palette[vtui.ColDialogText])
	val := vtui.NewText(0, 0, text, vtui.Palette[vtui.ColDialogText])
	row := vtui.NewHBoxLayout(0, 0, width, 1)
	row.Add(lbl, vtui.Margins{Left: leftMargin, Right: 1}, vtui.AlignLeft)
	row.Add(val, vtui.Margins{}, vtui.AlignLeft)
	dlg.AddItem(lbl)
	dlg.AddItem(val)
	mainVBox.Add(row, vtui.Margins{Top: 0}, vtui.AlignFill)
	return row
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

// windowsAdvancedFlags are Windows file attributes listed separately from the four
// common editable checkboxes in the dialog.
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
	{0x00000040, "Device"},
	{0x00008000, "Integrity stream"},
	{0x00010000, "Virtual"},
	{0x00020000, "No scrub data"},
	{0x00040000, "Recall on open"},
	{0x00080000, "Pinned"},
	{0x00100000, "Unpinned"},
	{0x00200000, "Strictly sequential"},
	{0x00400000, "Recall on data access"},
}

// advancedFlagsListRows is the height of the list of every known flag in the
// Advanced NTFS Flags box. The list holds sixteen lines, so it scrolls, and
// vtui draws a scroll bar only for a list taller than two rows: at two rows
// the display-only flags (Reparse Point, Virtual, Recall ..., Integrity stream,
// No scrub data) could not be reached or even noticed (f4#1931, part 8).
const advancedFlagsListRows = 4

// windowsAdvancedFlagsLines lists every known Windows file attribute, including
// unset ones, so users can distinguish "not set" from "not supported". A
// mixed selection uses "?", matching the three-state checkboxes above.
func windowsAdvancedFlagsLines(targets []AttributesTarget) []string {
	winAttrs := func(item vfs.VFSItem) uint32 { return item.WinAttrs }
	lines := make([]string, 0, len(windowsAdvancedFlags))
	for _, flag := range windowsAdvancedFlags {
		state := "no"
		switch mixedAttributeState(targets, flag.bit, winAttrs) {
		case 1:
			state = "yes"
		case 2:
			state = "?"
		}
		lines = append(lines, flag.name+": "+state)
	}
	return lines
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

	// A listing carries no birth time on Linux; ask for the one of each shown
	// object now, when the filesystem keeps it (f4#1817).
	if _, local := v.(*vfs.OSVFS); local {
		targets = append([]AttributesTarget(nil), targets...)
		for i := range targets {
			if targets[i].Item.HasMetadata(vfs.MetadataBTime) {
				continue
			}
			if born, ok := vfs.ReadBirthTime(targets[i].Path); ok {
				targets[i].Item.BTime = born
				targets[i].Item.KnownMetadata |= vfs.MetadataBTime
			}
		}
		item = targets[0].Item
	}

	// Read-only Created/Accessed/Changed rows (f4#1404 follow-up): computed
	// up front so the dialog's height can grow by exactly the rows that will
	// actually be shown. A row is omitted entirely, not shown with a zero
	// time, when any object in the selection lacks that field.
	createdText, showCreated := sharedAttributeTimeText(targets,
		func(item vfs.VFSItem) time.Time { return item.BTime },
		func(item vfs.VFSItem) bool { return item.HasMetadata(vfs.MetadataBTime) })
	accessedText, showAccessed := sharedAttributeTimeText(targets,
		func(item vfs.VFSItem) time.Time { return item.ATime },
		func(item vfs.VFSItem) bool { return item.HasMetadata(vfs.MetadataATime) })
	changedText, showChanged := sharedAttributeTimeText(targets,
		func(item vfs.VFSItem) time.Time { return item.CTime },
		func(item vfs.VFSItem) bool { return item.HasMetadata(vfs.MetadataCTime) })
	for _, shown := range []bool{showCreated, showAccessed, showChanged} {
		if shown {
			height++
		}
	}

	// The recursive checkbox (f4#1502) only makes sense, and is only shown,
	// when a real directory is actually selected -- a selection of plain
	// files has nothing under it to walk.
	showRecursive := targetsIncludeRealDir(targets)
	if showRecursive {
		height++
	}
	height++ // UTC input checkbox

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

	// Recursive checkbox (f4#1502). Opt-in and off by default, the way
	// Sync.Subdirs/Compare.Recursive ("&Subfolders") are in this codebase's
	// other apply-to-a-tree dialogs -- a plain checkbox next to the other
	// options rather than its own group box.
	var cbRecursive *vtui.Checkbox
	if showRecursive {
		cbRecursive = vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Recursive"), false)
		dlg.AddItem(cbRecursive)
		mainVBox.Add(cbRecursive, vtui.Margins{Top: 0}, vtui.AlignLeft)
	}

	// Created, Modified, Metadata (changed) and Accessed rows, in that order
	// (f4#1404, f4#1817). Changed is read-only: no OS lets a program set the
	// status-change time. Created is editable only where the platform can set a
	// birth time (macOS) and for a real local file system; elsewhere it is shown
	// and cannot be changed. Accessed is editable, below.
	var rowCreated, rowAccessed, rowChanged *vtui.HBoxLayout
	var editCreated *vtui.Edit
	initialCreated := ""
	if showCreated {
		if osv, ok := v.(*vfs.OSVFS); ok && osv.SupportsSetBTime() {
			if !multiple {
				initialCreated = createdText
			}
			editCreated = vtui.NewEdit(0, 0, 20, initialCreated)
			lblCreated := vtui.NewText(0, 0, PadLabel(i18n.Msg("Attributes.Created")), vtui.Palette[vtui.ColDialogText])
			rowCreated = vtui.NewHBoxLayout(0, 0, 66, 1)
			rowCreated.Add(lblCreated, vtui.Margins{Left: 2, Right: 1}, vtui.AlignLeft)
			rowCreated.Add(editCreated, vtui.Margins{}, vtui.AlignLeft)
			dlg.AddItem(lblCreated)
			dlg.AddItem(editCreated)
			mainVBox.Add(rowCreated, vtui.Margins{Top: 0}, vtui.AlignFill)
		} else {
			rowCreated = attributesReadOnlyTimeRow(dlg, mainVBox, 66, 2, i18n.Msg("Attributes.Created"), createdText)
		}
	}
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
	cbUTC := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.UTC"), false)
	dlg.AddItem(cbUTC)
	mainVBox.Add(cbUTC, vtui.Margins{Top: 0}, vtui.AlignLeft)

	if showChanged {
		rowChanged = attributesReadOnlyTimeRow(dlg, mainVBox, 66, 2, i18n.Msg("Attributes.Changed"), changedText)
	}

	// Accessed is editable (f4#1404): utimensat takes it together with the
	// modification time, which OSVFS.SetAttributes already passes on. Like
	// M-Time it stays blank for a multiple selection, and a field left
	// untouched changes nothing.
	var editAccessed *vtui.Edit
	initialAccessed := ""
	if showAccessed {
		if !multiple {
			initialAccessed = accessedText
		}
		editAccessed = vtui.NewEdit(0, 0, 20, initialAccessed)
		lblAccessed := vtui.NewText(0, 0, PadLabel(i18n.Msg("Attributes.Accessed")), vtui.Palette[vtui.ColDialogText])
		rowAccessed = vtui.NewHBoxLayout(0, 0, 66, 1)
		rowAccessed.Add(lblAccessed, vtui.Margins{Left: 2, Right: 1}, vtui.AlignLeft)
		rowAccessed.Add(editAccessed, vtui.Margins{}, vtui.AlignLeft)
		dlg.AddItem(lblAccessed)
		dlg.AddItem(editAccessed)
		mainVBox.Add(rowAccessed, vtui.Margins{Top: 0}, vtui.AlignFill)
	}
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
	cbUTC.Apply()
	if rowCreated != nil {
		rowCreated.Apply()
	}
	if rowAccessed != nil {
		rowAccessed.Apply()
	}
	if rowChanged != nil {
		rowChanged.Apply()
	}
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
			t, err := parseAttributesTime(text, cbUTC.State == 1)
			if err != nil {
				// f4 #1404: a garbled date used to be silently dropped —
				// nothing applied, nothing said why.
				vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
				return
			}
			edit.mtime, edit.setMTime = t, true
		}
		if editAccessed != nil {
			if text := editAccessed.GetText(); text != initialAccessed {
				t, err := parseAttributesTime(text, cbUTC.State == 1)
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
					return
				}
				edit.atime, edit.setATime = t, true
			}
		}
		if editCreated != nil {
			if text := editCreated.GetText(); text != initialCreated {
				t, err := parseAttributesTime(text, cbUTC.State == 1)
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
					return
				}
				edit.btime, edit.setBTime = t, true
			}
		}
		edit.mode, edit.keepMode = unixModeEdit(editOctal.GetText(), allChecks)
		recursive := cbRecursive != nil && cbRecursive.State == 1
		confirmDanglingLink(v, path, newTarget, !targetEdited, func() {
			vtui.RunAsync(func(ctx *vtui.TaskContext) {
				if targetEdited {
					if err := ReplaceSymlinkTarget(ctx.Context, v, path, newTarget); err != nil {
						ctx.RunOnUI(func() {
							vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
						})
						return
					}
				}
				applied, err := setUnixAttributesForTargets(ctx.Context, v, targets, edit, recursive)
				ctx.RunOnUI(func() {
					if err != nil {
						vtui.ShowMessage(" Error ", err.Error(), []string{"&Ok"})
						return
					}
					dlg.Close()
					if refresh != nil {
						refresh()
					}
					// A recursive Set can silently touch a tree the user cannot
					// see the size of from the dialog alone; a one-line count
					// is this ticket's "smaller first cut" instead of a full
					// progress dialog (f4#1502).
					if recursive {
						vtui.ShowMessage(i18n.Msg("Info.Title"), fmt.Sprintf(i18n.Msg("Attributes.RecursiveApplied"), applied), []string{"&Ok"})
					}
				})
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

func allTargetsAreFiles(targets []AttributesTarget) bool {
	for _, target := range targets {
		if target.Item.IsDir || target.Item.IsSymlink {
			return false
		}
	}
	return true
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
	// The rows of the dialog, top to bottom: the frame, a blank row, the file
	// name, the Flags box (4), the Advanced NTFS Flags box (4 rows of check
	// boxes and the 4-row list between its borders, 10), a blank row, Last
	// write, a blank row, the buttons, a blank row and the frame: 23. Created
	// and Accessed, shown on Windows for any file that has them, make 25, the
	// whole of an 80x25 terminal; a link or hard links below add more, and a
	// dialog taller than the screen scrolls (f4#1931). The boxes follow each
	// other without a blank row for the sake of those two rows.
	width, height := 60, 23
	showCompression := runtime.GOOS == "windows" && fileops.IsLocalOSVFS(v) && !hostmode.Posix()
	showEncryption := showCompression
	showSparse := showCompression && allTargetsAreFiles(targets)

	// Read-only Created/Accessed rows (f4#1404 follow-up). No "Changed" row
	// here: Windows has no ctime-shaped "metadata changed" timestamp to show,
	// unlike Unix. Computed up front so height grows by exactly the rows
	// that will actually be shown; a row is omitted, not shown with a zero
	// time, when any object in the selection lacks the field (e.g. Wine
	// posix mode, whose Ctim is a real Linux ctime with no creation-time
	// meaning at all, never sets MetadataBTime — see os_vfs_windows.go).
	createdText, showCreated := sharedAttributeTimeText(targets,
		func(item vfs.VFSItem) time.Time { return item.BTime },
		func(item vfs.VFSItem) bool { return item.HasMetadata(vfs.MetadataBTime) })
	accessedText, showAccessed := sharedAttributeTimeText(targets,
		func(item vfs.VFSItem) time.Time { return item.ATime },
		func(item vfs.VFSItem) bool { return item.HasMetadata(vfs.MetadataATime) })
	for _, shown := range []bool{showCreated, showAccessed} {
		if shown {
			height++
		}
	}
	height++ // UTC input checkbox

	// f4#1828: the target of a single link is editable here as it is in the
	// Unix-shaped dialog. A link whose target cannot be read gets no field,
	// rather than one that would replace it with an empty string.
	linkTarget := ""
	linkIsJunction := false
	if !multiple && (item.IsSymlink || vfs.LinkKindOf(&item) == vfs.LinkJunction) {
		if t, err := vfs.Readlink(context.Background(), v, path); err == nil && t != "" {
			linkTarget = t
			linkIsJunction = vfs.LinkKindOf(&item) == vfs.LinkJunction
			height += 4 // the kind of the link and its target, each after a blank row
		}
	}

	// f4#1861: a file with several hard links lists all its names in a
	// drop-down list, as far3 does; the count alone would not tell where the
	// others are. One row however many names there are, so the dialog keeps
	// its size (DkmS1953).
	linkNames := attributesHardLinkNames(v, path, item, multiple)
	if len(linkNames) > 0 {
		height += 2 // a blank row and the list
	}

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

	var editLinkTarget *vtui.Edit
	var rowLinkTarget *vtui.HBoxLayout
	if linkTarget != "" {
		// Which kind of link it is matters, and Ctrl+A did not say
		// (DkmS1953, f4#1828).
		kind := i18n.Msg("Attributes.LinkKindSymlink")
		if linkIsJunction {
			kind = i18n.Msg("Attributes.LinkKindJunction")
		}
		lblKind := vtui.NewText(0, 0, kind, vtui.Palette[vtui.ColDialogText])
		dlg.AddItem(lblKind)
		mainVBox.Add(lblKind, vtui.Margins{Top: 1}, vtui.AlignLeft)
		editLinkTarget = vtui.NewEdit(0, 0, 40, linkTarget)
		lblLinkTarget := vtui.NewLabel(0, 0, i18n.Msg("Attributes.Target"), editLinkTarget)
		rowLinkTarget = vtui.NewHBoxLayout(0, 0, 54, 1)
		rowLinkTarget.Add(lblLinkTarget, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowLinkTarget.Add(editLinkTarget, vtui.Margins{}, vtui.AlignFill)
		dlg.AddItem(lblLinkTarget)
		dlg.AddItem(editLinkTarget)
		mainVBox.Add(rowLinkTarget, vtui.Margins{Top: 1}, vtui.AlignFill)
	}

	if len(linkNames) > 0 {
		// Not DropdownOnly: the field takes a selection, so a name can be
		// copied with Ctrl+Ins. What is typed there is never saved.
		comboLinks := vtui.NewComboBox(0, 0, 30, linkNames)
		comboLinks.Menu.SetSelectPos(0)
		comboLinks.Edit.SetText(linkNames[0])
		// A name chosen from the list is selected in the field, ready for
		// Ctrl+Ins (DkmS1953, f4#1861). It is not put on the clipboard by
		// itself: that would replace what is there without being asked.
		comboLinks.Menu.OnAction = func(idx int) {
			if idx >= 0 && idx < len(linkNames) {
				comboLinks.Edit.SetText(linkNames[idx])
				comboLinks.Edit.SelectAll()
			}
		}
		lblLinks := vtui.NewLabel(0, 0, fmt.Sprintf(i18n.Msg("Attributes.HardLinks"), len(linkNames)), comboLinks)
		rowLinks := vtui.NewHBoxLayout(0, 0, 54, 1)
		rowLinks.Add(lblLinks, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowLinks.Add(comboLinks, vtui.Margins{}, vtui.AlignFill)
		dlg.AddItem(lblLinks)
		dlg.AddItem(comboLinks)
		mainVBox.Add(rowLinks, vtui.Margins{Top: 1}, vtui.AlignFill)
	}

	// The ordinary flags sit in two columns and the advanced ones in two more,
	// so the many editable NTFS flags fit a 25-line terminal (f4#1931). Neither
	// box keeps a spare row: the rows it saves go to the list of flags.
	gbAttr := vtui.NewGroupBox(0, 0, 54, 3, " "+i18n.Msg("Attributes.Flags")+" ")
	dlg.AddItem(gbAttr)
	mainVBox.Add(gbAttr, vtui.Margins{}, vtui.AlignFill)

	gbAdv := vtui.NewGroupBox(0, 0, 54, 9, " "+i18n.Msg("Attributes.AdvancedFlags")+" ")
	dlg.AddItem(gbAdv)
	mainVBox.Add(gbAdv, vtui.Margins{}, vtui.AlignFill)

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
	cbUTC := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.UTC"), false)
	dlg.AddItem(cbUTC)
	mainVBox.Add(cbUTC, vtui.Margins{Top: 0}, vtui.AlignLeft)

	var rowCreated, rowAccessed *vtui.HBoxLayout
	// Created is editable where the platform can set a creation time (native
	// Windows), and only for a real local file system; elsewhere it is shown
	// and cannot be changed. Like Accessed it is blank for a multiple
	// selection, and a field left untouched changes nothing.
	var editCreated *vtui.Edit
	initialCreated := ""
	if showCreated {
		if osv, ok := v.(*vfs.OSVFS); ok && osv.SupportsSetBTime() {
			if !multiple {
				initialCreated = createdText
			}
			editCreated = vtui.NewEdit(0, 0, 20, initialCreated)
			lblCreated := vtui.NewText(0, 0, PadLabel(i18n.Msg("Attributes.Created")), vtui.Palette[vtui.ColDialogText])
			rowCreated = vtui.NewHBoxLayout(0, 0, 54, 1)
			rowCreated.Add(lblCreated, vtui.Margins{Right: 1}, vtui.AlignLeft)
			rowCreated.Add(editCreated, vtui.Margins{}, vtui.AlignLeft)
			dlg.AddItem(lblCreated)
			dlg.AddItem(editCreated)
			mainVBox.Add(rowCreated, vtui.Margins{Top: 0}, vtui.AlignFill)
		} else {
			rowCreated = attributesReadOnlyTimeRow(dlg, mainVBox, 54, 0, i18n.Msg("Attributes.Created"), createdText)
		}
	}
	// Accessed is editable (f4#1404), like Last write; blank for a multiple
	// selection, and a field left untouched changes nothing.
	var editAccessed *vtui.Edit
	initialAccessed := ""
	if showAccessed {
		if !multiple {
			initialAccessed = accessedText
		}
		editAccessed = vtui.NewEdit(0, 0, 20, initialAccessed)
		lblAccessed := vtui.NewText(0, 0, PadLabel(i18n.Msg("Attributes.Accessed")), vtui.Palette[vtui.ColDialogText])
		rowAccessed = vtui.NewHBoxLayout(0, 0, 54, 1)
		rowAccessed.Add(lblAccessed, vtui.Margins{Right: 1}, vtui.AlignLeft)
		rowAccessed.Add(editAccessed, vtui.Margins{}, vtui.AlignLeft)
		dlg.AddItem(lblAccessed)
		dlg.AddItem(editAccessed)
		mainVBox.Add(rowAccessed, vtui.Margins{Top: 0}, vtui.AlignFill)
	}

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
	cbUTC.Apply()
	if rowLinkTarget != nil {
		rowLinkTarget.Apply()
	}
	if rowCreated != nil {
		rowCreated.Apply()
	}
	if rowAccessed != nil {
		rowAccessed.Apply()
	}
	rowBtns.Apply()

	// Apply second pass for GroupBox
	const flagsColumnWidth = 33
	gbVBox := vtui.NewVBoxLayout(gbAttr.X1+2, gbAttr.Y1+1, flagsColumnWidth, 2)
	gbVBoxRight := vtui.NewVBoxLayout(gbAttr.X1+2+flagsColumnWidth, gbAttr.Y1+1, gbAttr.X2-gbAttr.X1-4-flagsColumnWidth, 2)
	var compressionCheckbox, encryptionCheckbox, sparseCheckbox *vtui.Checkbox
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
	gbVBoxRight.Add(chkSY, vtui.Margins{}, vtui.AlignLeft)
	gbVBoxRight.Add(chkAR, vtui.Margins{}, vtui.AlignLeft)
	gbVBox.Apply()
	gbVBoxRight.Apply()

	chkNotContentIndexed := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.NotContentIndexed"), multiple)
	chkNotContentIndexed.State = mixedAttributeState(targets, 0x00002000, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
	gbAdv.AddItem(chkNotContentIndexed)
	// Left column: Not Content Indexed and the NTFS controls; right column:
	// Temporary, Offline, Pinned and Unpinned. Four rows of boxes, then the
	// scrollable list of every known flag. The list has four rows: a scroll
	// bar needs more than two (vtui draws none for a list of one or two rows,
	// which hid the display-only flags below the first two, f4#1931).
	gbAdvVBox := vtui.NewVBoxLayout(gbAdv.X1+2, gbAdv.Y1+1, flagsColumnWidth, 4)
	gbAdvVBoxRight := vtui.NewVBoxLayout(gbAdv.X1+2+flagsColumnWidth, gbAdv.Y1+1, gbAdv.X2-gbAdv.X1-3-flagsColumnWidth, 4)
	gbAdvVBoxList := vtui.NewVBoxLayout(gbAdv.X1+2, gbAdv.Y1+5, gbAdv.X2-gbAdv.X1-3, advancedFlagsListRows)
	gbAdvVBox.Add(chkNotContentIndexed, vtui.Margins{}, vtui.AlignLeft)
	flagsList := vtui.NewListBox(0, 0, 50, advancedFlagsListRows, windowsAdvancedFlagsLines(targets))
	flagsList.ShowScrollBar = true
	if flagsList.ScrollBar != nil {
		flagsList.ScrollBar.ColorIdx = vtui.ColDialogBox
	}
	if showCompression {
		chkCompressed := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Compressed"), mixedAttributeState(targets, 0x00000800, func(item vfs.VFSItem) uint32 { return item.WinAttrs }) == 1)
		chkCompressed.State = mixedAttributeState(targets, 0x00000800, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
		gbAdv.AddItem(chkCompressed)
		gbAdvVBox.Add(chkCompressed, vtui.Margins{}, vtui.AlignLeft)
		compressionCheckbox = chkCompressed
	}
	if showEncryption {
		state := mixedAttributeState(targets, 0x00004000, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
		chkEncrypted := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Encrypted"), state == 1)
		chkEncrypted.State = state
		gbAdv.AddItem(chkEncrypted)
		gbAdvVBox.Add(chkEncrypted, vtui.Margins{}, vtui.AlignLeft)
		encryptionCheckbox = chkEncrypted
	}
	if showSparse {
		state := mixedAttributeState(targets, 0x00000200, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
		chkSparse := vtui.NewCheckbox(0, 0, i18n.Msg("Attributes.Sparse"), state == 1)
		chkSparse.State = state
		gbAdv.AddItem(chkSparse)
		gbAdvVBox.Add(chkSparse, vtui.Margins{}, vtui.AlignLeft)
		sparseCheckbox = chkSparse
	}
	type extraFlagBox struct {
		box         *vtui.Checkbox
		bit         uint32
		unavailable bool
	}
	cloud := cloudAttributesAvailable(targets)
	var extraFlags []extraFlagBox
	for _, f := range []struct {
		key   string
		bit   uint32
		cloud bool
	}{
		{"Attributes.Temporary", winAttrTemporary, false},
		{"Attributes.Offline", winAttrOffline, false},
		{"Attributes.Pinned", winAttrPinned, true},
		{"Attributes.Unpinned", winAttrUnpinned, true},
	} {
		chk := vtui.NewCheckbox(0, 0, i18n.Msg(f.key), multiple)
		chk.State = mixedAttributeState(targets, f.bit, func(item vfs.VFSItem) uint32 { return item.WinAttrs })
		unavailable := f.cloud && !cloud
		chk.SetDisabled(unavailable)
		gbAdv.AddItem(chk)
		gbAdvVBoxRight.Add(chk, vtui.Margins{}, vtui.AlignLeft)
		extraFlags = append(extraFlags, extraFlagBox{box: chk, bit: f.bit, unavailable: unavailable})
	}
	excludeEachOther(extraFlags[2].box, extraFlags[3].box)
	gbAdv.AddItem(flagsList)
	gbAdvVBoxList.Add(flagsList, vtui.Margins{}, vtui.AlignFill)
	gbAdvVBox.Apply()
	gbAdvVBoxRight.Apply()
	gbAdvVBoxList.Apply()

	btnSet.OnClick = func() {
		var edit windowsAttributesEdit
		if compressionCheckbox != nil {
			state := compressionCheckbox.State
			if state != mixedAttributeState(targets, 0x00000800, func(item vfs.VFSItem) uint32 { return item.WinAttrs }) {
				edit.setCompression = true
				edit.compressed = state == 1
			}
		}
		if encryptionCheckbox != nil {
			state := encryptionCheckbox.State
			if state != mixedAttributeState(targets, 0x00004000, func(item vfs.VFSItem) uint32 { return item.WinAttrs }) {
				edit.setEncryption = true
				edit.encrypted = state == 1
			}
		}
		if sparseCheckbox != nil {
			state := sparseCheckbox.State
			if state != mixedAttributeState(targets, 0x00000200, func(item vfs.VFSItem) uint32 { return item.WinAttrs }) {
				edit.setSparse = true
				edit.sparse = state == 1
			}
		}
		if text := editMTime.GetText(); text != initialMTime {
			nt, err := parseAttributesTime(text, cbUTC.State == 1)
			if err != nil {
				// f4 #1404: a garbled date used to be silently dropped —
				// nothing applied, nothing said why.
				vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
				return
			}
			edit.mtime, edit.setMTime = nt, true
		}
		if editAccessed != nil {
			if text := editAccessed.GetText(); text != initialAccessed {
				nt, err := parseAttributesTime(text, cbUTC.State == 1)
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
					return
				}
				edit.atime, edit.setATime = nt, true
			}
		}
		if editCreated != nil {
			if text := editCreated.GetText(); text != initialCreated {
				nt, err := parseAttributesTime(text, cbUTC.State == 1)
				if err != nil {
					vtui.ShowMessage(" Error ", fmt.Sprintf(i18n.Msg("Attributes.MTimeInvalidError"), i18n.Msg("Attributes.MTimeFormatHint")), []string{"&Ok"})
					return
				}
				edit.btime, edit.setBTime = nt, true
			}
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
		edit.applyFlagState(winAttrNotContentIndexed, chkNotContentIndexed.State, false)
		for _, f := range extraFlags {
			edit.applyFlagState(f.bit, f.box.State, f.unavailable)
		}

		newLinkTarget := ""
		if editLinkTarget != nil {
			newLinkTarget = strings.TrimSpace(editLinkTarget.GetText())
		}
		changeLink := newLinkTarget != "" && newLinkTarget != linkTarget
		confirmDanglingLink(v, path, newLinkTarget, !changeLink || linkIsJunction, func() {
			vtui.RunAsync(func(ctx *vtui.TaskContext) {
				var err error
				if changeLink {
					err = replaceLinkTarget(ctx.Context, v, path, newLinkTarget, linkIsJunction)
				} else if editLinkTarget != nil && newLinkTarget == "" {
					err = errors.New("link target cannot be empty")
				}
				if err == nil {
					err = setWindowsAttributesForTargets(ctx.Context, v, targets, edit)
				}
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
		})
	}
	btnCancel.OnClick = func() { dlg.Close() }
	vtui.FrameManager.Push(dlg)
}

// attributesHardLinkNames is every name of the file when it has more than
// one and the file system can tell them (vfs.OSVFS on Windows).
func attributesHardLinkNames(v vfs.VFS, path string, item vfs.VFSItem, multiple bool) []string {
	if multiple || item.IsDir {
		return nil
	}
	lister, ok := v.(interface {
		HardLinkNames(ctx context.Context, path string) ([]string, error)
	})
	if !ok {
		return nil
	}
	names, err := lister.HardLinkNames(context.Background(), path)
	if err != nil || len(names) < 2 {
		return nil
	}
	return names
}
