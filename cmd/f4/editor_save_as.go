package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// saveAsEOL is the line-break choice of the Save As dialog (#899), in the
// order Far's Shift+F2 dialog lists them.
type saveAsEOL int

const (
	saveAsEOLKeep saveAsEOL = iota
	saveAsEOLDos
	saveAsEOLUnix
	saveAsEOLMac
)

// saveAsEOLBytes is the line break each choice writes; empty means "do not
// change".
func saveAsEOLBytes(eol saveAsEOL) []byte {
	switch eol {
	case saveAsEOLDos:
		return []byte("\r\n")
	case saveAsEOLUnix:
		return []byte("\n")
	case saveAsEOLMac:
		return []byte("\r")
	default:
		return nil
	}
}

// convertLineEndings rewrites every CR LF, lone CR and lone LF as eol. It
// returns the input itself when nothing would change, so callers can skip
// replacing the buffer for a file that already uses the requested breaks.
func convertLineEndings(data []byte, eol []byte) []byte {
	if len(eol) == 0 || len(data) == 0 {
		return data
	}
	var out []byte
	start := 0
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c != '\r' && c != '\n' {
			continue
		}
		brk := 1
		if c == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			brk = 2
		}
		if out == nil {
			if bytes.Equal(data[i:i+brk], eol) {
				i += brk - 1
				continue
			}
			out = make([]byte, 0, len(data)+len(data)/16)
		}
		out = append(out, data[start:i]...)
		out = append(out, eol...)
		i += brk - 1
		start = i + 1
	}
	if out == nil {
		return data
	}
	return append(out, data[start:]...)
}

// codepageWritesBOM reports whether the encoder for cpID starts its output
// with a byte order mark. UTF-8 is handled separately through ev.utf8BOM.
func codepageWritesBOM(cpID int) bool {
	switch vfs.NormalizeCodepageID(cpID) {
	case 1200, 1201, 12000, 12001:
		return true
	}
	return false
}

// isUnicodeCodepage reports whether the BOM checkbox applies to cpID.
func isUnicodeCodepage(cpID int) bool {
	return vfs.NormalizeCodepageID(cpID) == 65001 || codepageWritesBOM(cpID)
}

// stripEncodedBOM removes the BOM that the UTF-16/UTF-32 encoders always
// emit; it is the "Add signature (BOM)" checkbox turned off for those
// codepages. Their decoders accept input without a BOM, so the reopen after
// the save still reads the file back correctly.
func stripEncodedBOM(encoded []byte, cpID int) []byte {
	switch vfs.NormalizeCodepageID(cpID) {
	case 1200:
		if len(encoded) >= 2 && encoded[0] == 0xFF && encoded[1] == 0xFE {
			return encoded[2:]
		}
	case 1201:
		if len(encoded) >= 2 && encoded[0] == 0xFE && encoded[1] == 0xFF {
			return encoded[2:]
		}
	case 12000:
		if len(encoded) >= 4 && encoded[0] == 0xFF && encoded[1] == 0xFE && encoded[2] == 0 && encoded[3] == 0 {
			return encoded[4:]
		}
	case 12001:
		if len(encoded) >= 4 && encoded[0] == 0 && encoded[1] == 0 && encoded[2] == 0xFE && encoded[3] == 0xFF {
			return encoded[4:]
		}
	}
	return encoded
}

// saveAsCodepages lists the codepages the dialog offers and the index of
// currentID in that list.
func saveAsCodepages(currentID int) ([]vfs.Codepage, []string, int) {
	currentID = vfs.NormalizeCodepageID(currentID)
	cps := make([]vfs.Codepage, 0, len(vfs.AvailableCodepages))
	labels := make([]string, 0, len(vfs.AvailableCodepages))
	current := -1
	for _, cp := range vfs.AvailableCodepages {
		if cp.ID == currentID && current < 0 {
			current = len(cps)
		}
		cps = append(cps, cp)
		labels = append(labels, strings.TrimSpace(vfs.CodepageMenuLabel(cp)))
	}
	if current < 0 {
		current = 0
	}
	return cps, labels, current
}

// resolveSaveAsPath turns what the user typed into a path on the editor's
// VFS: a relative name lands next to the file being edited.
func (ev *EditorView) resolveSaveAsPath(input string) string {
	input = strings.TrimSpace(input)
	if input == "" || ev.vfs == nil {
		return ""
	}
	if ev.vfs.IsAbs(input) {
		return input
	}
	dir := ""
	if ev.filePath != "" {
		dir = ev.vfs.Dir(ev.filePath)
	}
	if dir == "" {
		if abs, err := ev.vfs.Abs(input); err == nil && abs != "" {
			return abs
		}
		return input
	}
	return ev.vfs.Join(dir, input)
}

// showSaveAsDialog is the editor's Shift+F2: Far's "Save file as" dialog
// with the target name, the codepage (plus its BOM) and the line breaks to
// write (#899).
func (ev *EditorView) showSaveAsDialog() {
	if ev.vfs == nil || ev.saving {
		return
	}
	dlgW, dlgH := 76, 16
	dlg := vtui.NewCenteredDialog(dlgW, dlgH, Msg("SaveAs.Title"))
	dlg.ShowClose = true

	editPath := vtui.NewEdit(0, 0, dlgW-4, ev.filePath)
	attachHistory(editPath, newEditHistoryID)
	editPath.SelectAll()
	lblPath := vtui.NewLabel(0, 0, Msg("SaveAs.Path"), editPath)
	dlg.SetFocusedItem(editPath)

	cps, cpLabels, cpIdx := saveAsCodepages(ev.Codepage)
	comboCP := vtui.NewComboBox(0, 0, 38, cpLabels)
	comboCP.DropdownOnly = true
	comboCP.Menu.SetSelectPos(cpIdx)
	comboCP.Edit.SetText(cpLabels[cpIdx])
	lblCP := vtui.NewLabel(0, 0, Msg("SaveAs.Codepage"), comboCP)

	chkBOM := vtui.NewCheckbox(0, 0, Msg("SaveAs.BOM"), false)
	selectedCP := func() int {
		pos := comboCP.Menu.SelectPos
		if pos < 0 || pos >= len(cps) {
			pos = cpIdx
		}
		return cps[pos].ID
	}
	syncBOM := func() {
		cp := selectedCP()
		chkBOM.SetDisabled(!isUnicodeCodepage(cp))
		switch {
		case !isUnicodeCodepage(cp):
			chkBOM.State = 0
		case vfs.NormalizeCodepageID(cp) == 65001:
			// The file's own marker for UTF-8; a UTF-16/32 file that is saved
			// back as UTF-8 does not get one it never had.
			if ev.utf8BOM {
				chkBOM.State = 1
			} else {
				chkBOM.State = 0
			}
		default:
			chkBOM.State = 1
		}
	}
	comboCP.Menu.OnAction = func(idx int) {
		if idx >= 0 && idx < len(cpLabels) {
			comboCP.Edit.SetText(cpLabels[idx])
		}
		syncBOM()
		vtui.FrameManager.Redraw()
	}
	syncBOM()

	lblEOL := vtui.NewText(0, 0, Msg("SaveAs.LineBreaks"), vtui.Palette[vtui.ColDialogText])
	eolGroup := vtui.NewRadioGroup(0, 0, 2, []string{
		Msg("SaveAs.EOLKeep"),
		Msg("SaveAs.EOLDos"),
		Msg("SaveAs.EOLUnix"),
		Msg("SaveAs.EOLMac"),
	})
	eolGroup.Selected = int(saveAsEOLKeep)

	btnSave := vtui.NewButton(0, 0, Msg("SaveAs.BtnSave"))
	btnSave.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, Msg("vtui.Cancel"))

	for _, item := range []vtui.UIElement{lblPath, editPath, lblCP, comboCP, chkBOM, lblEOL, eolGroup, btnSave, btnCancel} {
		dlg.AddItem(item)
	}

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, dlgW-4, dlgH-4)
	vbox.Add(lblPath, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(editPath, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblCP, vtui.Margins{Top: 1}, vtui.AlignLeft)
	rowCP := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	rowCP.Spacing = 2
	rowCP.Add(comboCP, vtui.Margins{}, vtui.AlignTop)
	rowCP.Add(chkBOM, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowCP, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(lblEOL, vtui.Margins{Top: 1}, vtui.AlignLeft)
	vbox.Add(eolGroup, vtui.Margins{}, vtui.AlignLeft)
	buttons := vtui.NewHBoxLayout(0, 0, dlgW-4, 1)
	buttons.HorizontalAlign = vtui.AlignCenter
	buttons.Spacing = 2
	buttons.Add(btnSave, vtui.Margins{}, vtui.AlignTop)
	buttons.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(buttons, vtui.Margins{Top: 1}, vtui.AlignFill)
	vbox.Apply()

	btnSave.OnClick = func() {
		target := ev.resolveSaveAsPath(editPath.GetText())
		if target == "" {
			vtui.ShowMessage(Msg("SaveAs.Title"), Msg("SaveAs.EmptyPath"), []string{Msg("vtui.Ok")})
			return
		}
		commitHistory(editPath, editPath.GetText())
		cp := selectedCP()
		bom := chkBOM.State == 1 && isUnicodeCodepage(cp)
		eol := saveAsEOL(eolGroup.Selected)
		dlg.Close()
		ev.saveAs(target, cp, bom, eol)
	}
	btnCancel.OnClick = func() { dlg.Close() }

	vtui.FrameManager.Push(dlg)
}

// saveAs writes the buffer to target. A new path is created without
// replacing anything that appears there meanwhile; a path that already
// exists is replaced only after the user confirms it.
func (ev *EditorView) saveAs(target string, cpID int, bom bool, eol saveAsEOL) {
	if ev.vfs == nil || ev.saving || target == "" {
		return
	}
	if target == ev.filePath {
		ev.applySaveAs(target, cpID, bom, eol, ev.createNewTarget)
		return
	}
	filesystem := ev.vfs
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		_, statErr := filesystem.Stat(ctx.Context, target)
		ctx.RunOnUI(func() {
			if ev.vfs != filesystem || ev.saving {
				return
			}
			switch {
			case statErr == nil:
				msg := fmt.Sprintf(Msg("SaveAs.OverwriteQuestion"), target)
				confirm := vtui.ShowMessageOn(ev, Msg("SaveAs.Title"), msg, []string{Msg("FileOp.Overwrite"), Msg("vtui.Cancel")})
				confirm.OnResult = func(code int) {
					if code == 0 {
						ev.applySaveAs(target, cpID, bom, eol, false)
					}
				}
			case errors.Is(statErr, os.ErrNotExist):
				ev.applySaveAs(target, cpID, bom, eol, true)
			default:
				vtui.ShowMessage(" Error ", fmt.Sprintf("Cannot check the target file:\n%v", statErr), []string{"&Ok"})
			}
		})
	})
}

// applySaveAs retargets the editor and saves. Line breaks are converted in
// the buffer itself, as one undoable edit, before the write: the file that
// is reopened after a save must be the text the editor shows, and the line
// index it keeps across the save is built from that text.
func (ev *EditorView) applySaveAs(target string, cpID int, bom bool, eol saveAsEOL, createNew bool) {
	if ev.vfs == nil || ev.saving {
		return
	}
	pathChanged := target != ev.filePath
	cpChanged := vfs.NormalizeCodepageID(cpID) != vfs.NormalizeCodepageID(ev.Codepage)
	utf8BOM := vfs.NormalizeCodepageID(cpID) == 65001 && bom
	omitBOM := codepageWritesBOM(cpID) && !bom
	bomChanged := utf8BOM != ev.utf8BOM || omitBOM != ev.omitUnicodeBOM
	eolBytes := saveAsEOLBytes(eol)

	commit := func() {
		if ev.saving {
			return
		}
		previousPath := ev.filePath
		ev.filePath = target
		if pathChanged {
			ev.DisplayTitle = ""
			ev.createNewTarget = createNew
		}
		ev.Codepage = vfs.NormalizeCodepageID(cpID)
		ev.codepageRaw = nil
		ev.utf8BOM = utf8BOM
		ev.omitUnicodeBOM = omitBOM
		if pathChanged || cpChanged || bomChanged || eolBytes != nil {
			ev.modified = true
		}
		fullWrite := pathChanged || cpChanged || bomChanged || eolBytes != nil
		ev.saveToFile(func() {
			if cpChanged || bomChanged || eolBytes != nil {
				// Undo states are pieces of the file's bytes. The file now
				// holds different bytes for the same text, so every earlier
				// state would load garbage; the history has to start over.
				ev.undoStack = nil
				ev.redoStack = nil
				ev.lastOp = opNone
			}
			if pathChanged {
				rememberViewerEditorHistory(ev.vfs, ev.filePath, historyModeEdit)
			}
			if pathChanged || cpChanged {
				override := ev.Codepage
				if override == 65001 {
					override = 0
				}
				saveCodepageOverride(ev.vfs, ev.filePath, override)
			}
			vtui.DebugLog("EDITOR: Saved %s as %s", previousPath, ev.filePath)
		}, fullWrite)
	}

	if eolBytes == nil {
		commit()
		return
	}

	session := ev.editSession
	vtui.RunAsync(func(ctx *vtui.TaskContext) {
		defer ev.guardMapping("converting line breaks")()
		data, err := ev.searchBuffer(ctx, session)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			ctx.RunOnUI(func() {
				vtui.ShowMessage(" Error ", "Failed to read file buffer.", []string{"&Ok"})
			})
			return
		}
		converted := convertLineEndings(data, eolBytes)
		changed := !bytes.Equal(converted, data)
		text := string(converted)
		ctx.RunOnUI(func() {
			if ev.editSession != session || ev.saving {
				return
			}
			if changed {
				line, pos := ev.CursorLine, ev.CursorPos
				scrollTop := ev.ScrollTopRow
				ev.saveUndo(opOther)
				ev.SetText(text)
				ev.clearCaches()
				if line >= ev.li.LineCount() {
					line = ev.li.LineCount() - 1
				}
				if line < 0 {
					line = 0
				}
				ev.CursorLine = line
				if pos > ev.getLineLength(line) {
					pos = ev.getLineLength(line)
				}
				ev.CursorPos = pos
				ev.ScrollTopRow = scrollTop
				ev.updateDesiredVisualCol()
				ev.ensureCursorVisible()
				vtui.FrameManager.Redraw()
			}
			commit()
		})
	})
}
