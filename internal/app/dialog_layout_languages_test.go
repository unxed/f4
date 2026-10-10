package app

// Layout checks for two dialogs whose subjects are still here: the file
// association editor and the find-file options. The file-operation buttons
// they used to share a file with went to internal/fileops with theirs.

import (
	"github.com/unxed/f4/internal/panel"
	"github.com/unxed/f4/internal/paneltest"
	"github.com/unxed/f4/internal/theme"
	"testing"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestFindFileSelectedFoldersCheckbox(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	theme.SetDefaultF4Palette()
	packs := i18n.LoadAllLanguagePacks()
	for _, tc := range []struct {
		name    string
		entries []*panel.FileEntry
		want    bool
	}{
		{name: "cursor folder", entries: []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "folder", IsDir: true}}}},
		{name: "marked file", entries: []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "file.txt"}, Selected: true}}},
		{name: "marked folder", entries: []*panel.FileEntry{{VFSItem: vfs.VFSItem{Name: "folder", IsDir: true}, Selected: true}}, want: true},
		{name: "mixed marks", entries: []*panel.FileEntry{
			{VFSItem: vfs.VFSItem{Name: "file.txt"}, Selected: true},
			{VFSItem: vfs.VFSItem{Name: "folder", IsDir: true}, Selected: true},
		}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vtui.AssertLayoutInLanguages(t, packs, func() vtui.Container {
				screen := vtui.NewSilentScreenBuf()
				screen.AllocBuf(80, 25)
				vtui.FrameManager.Init(screen)
				pf := panel.NewPanelsFrame()
				defer pf.Close()
				pf.ResizeConsole(80, 25)
				pf.GetActivePanel().Entries = tc.entries
				actionFindFile(pf)
				dlg := vtui.FrameManager.GetTopFrame().(vtui.Container)
				var selected *vtui.Checkbox
				var separator *vtui.Separator
				var edits []*vtui.Edit
				var checkboxes []*vtui.Checkbox
				var buttons []*vtui.Button
				for _, child := range dlg.GetChildren() {
					switch value := child.(type) {
					case *vtui.Separator:
						separator = value
					case *vtui.Edit:
						edits = append(edits, value)
					case *vtui.Checkbox:
						checkboxes = append(checkboxes, value)
					case *vtui.Button:
						buttons = append(buttons, value)
					}
					if checkbox, ok := child.(*vtui.Checkbox); ok && checkbox.GetText() == i18n.Msg("FindFile.SelectedFolders") {
						selected = checkbox
					}
				}
				if (selected != nil) != tc.want {
					t.Errorf("checkbox present=%v, want %v", selected != nil, tc.want)
				}
				if selected != nil && selected.State != 1 {
					t.Error("selected folders must be checked by default")
				}
				if separator == nil || len(edits) != 2 {
					t.Error("file and text sections must be separated")
					return dlg
				}
				_, maskY, _, _ := edits[0].GetPosition()
				_, textY, _, _ := edits[1].GetPosition()
				_, separatorY, _, _ := separator.GetPosition()
				fileOptionsBottom := maskY
				optionsBottom := maskY
				for _, checkbox := range checkboxes {
					_, y, _, _ := checkbox.GetPosition()
					optionsBottom = max(optionsBottom, y)
					fileOption := checkbox == selected || checkbox.GetText() == i18n.Msg("FindFile.Folders") || checkbox.GetText() == i18n.Msg("FindFile.Symlinks")
					if fileOption {
						fileOptionsBottom = max(fileOptionsBottom, y)
						if y <= maskY || y >= separatorY {
							t.Errorf("file option %q outside file section", checkbox.GetText())
						}
					} else if y <= textY || textY <= separatorY {
						t.Errorf("text option %q outside text section", checkbox.GetText())
					}
				}
				if separatorY != fileOptionsBottom+1 {
					t.Error("separator must immediately follow file search options")
				}
				for _, button := range buttons {
					_, y, _, _ := button.GetPosition()
					if y != optionsBottom+2 {
						t.Error("expected exactly one blank row before Find and Cancel")
					}
				}
				return dlg
			})
		})
	}
}

func TestFindFileInputsImmediatelyFollowLabels(t *testing.T) {
	t.Cleanup(paneltest.SwapFrameManager(t))
	screen := vtui.NewSilentScreenBuf()
	screen.AllocBuf(80, 25)
	vtui.FrameManager.Init(screen)
	theme.SetDefaultF4Palette()
	pf := panel.NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(80, 25)
	actionFindFile(pf)
	dlg, ok := vtui.FrameManager.GetTopFrame().(vtui.Container)
	if !ok {
		t.Fatal("Find File dialog missing")
	}
	var label *vtui.Text
	pairs := 0
	for _, child := range dlg.GetChildren() {
		switch value := child.(type) {
		case *vtui.Text:
			label = value
		case *vtui.Edit:
			if label == nil {
				t.Fatal("input missing its label")
			}
			_, _, _, labelBottom := label.GetPosition()
			_, inputTop, _, _ := value.GetPosition()
			if inputTop != labelBottom+1 {
				t.Errorf("input starts at row %d, want %d immediately after label", inputTop, labelBottom+1)
			}
			pairs++
			label = nil
		}
	}
	if pairs != 2 {
		t.Errorf("checked %d label/input pairs, want 2", pairs)
	}
}

func TestLayout_FileAssociationEditor_AllLanguages(t *testing.T) {
	vtui.SetDefaultPalette()

	packs := i18n.LoadAllLanguagePacks()
	if len(packs) == 0 {
		t.Skip("no language packs bundled")
	}
	filtered := packs[:0]
	for _, pack := range packs {
		if pack.Name != "bn" && pack.Name != "hi" {
			filtered = append(filtered, pack)
		}
	}
	packs = filtered

	// The association editor is the other dialog shown in the report. Build
	// its New association form for every translation so the final button row
	// remains inside the frame as captions change.
	vtui.AssertLayoutInLanguages(t, packs, func() vtui.Container {
		screen := vtui.NewSilentScreenBuf()
		screen.AllocBuf(120, 60)
		vtui.FrameManager.Init(screen)
		(&panel.AssocEditorState{}).EditAt(0, true)
		if top := vtui.FrameManager.GetTopFrame(); top != nil {
			if dlg, ok := top.(vtui.Container); ok {
				return dlg
			}
		}
		return nil
	})
}

func TestLayout_FindFileOptionsColumns_AllLanguages(t *testing.T) {
	vtui.SetDefaultPalette()

	packs := i18n.LoadAllLanguagePacks()
	if len(packs) == 0 {
		t.Skip("no language packs bundled")
	}

	// The Find File dialog lays its six option checkboxes out as three
	// two-column rows. The right column must start at the same X in
	// every row (#903), and the captions must still fit the dialog.
	build := func() vtui.Container {
		const width, height = 78, 20
		dlg := vtui.NewDialog(0, 0, width-1, height-1, i18n.Msg("FindFile.Title"))

		chkCase := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.CaseSensitive"), false)
		chkWhole := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.WholeWords"), false)
		chkRegexp := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Regexp"), false)
		chkNotContaining := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.NotContaining"), false)
		chkFolders := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Folders"), false)
		chkSymlinks := vtui.NewCheckbox(0, 0, i18n.Msg("FindFile.Symlinks"), false)
		for _, cb := range []*vtui.Checkbox{chkCase, chkWhole, chkRegexp, chkNotContaining, chkFolders, chkSymlinks} {
			dlg.AddItem(cb)
		}

		vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)
		leftColumn := checkboxColumnWidth(chkCase, chkRegexp, chkFolders)
		optionsRow := func(left, right *vtui.Checkbox) *vtui.HBoxLayout {
			row := vtui.NewHBoxLayout(0, 0, width-4, 1)
			row.Spacing = 8
			row.Add(left, vtui.Margins{Right: leftColumn - elementWidth(left)}, vtui.AlignTop)
			row.Add(right, vtui.Margins{}, vtui.AlignTop)
			return row
		}
		vbox.Add(optionsRow(chkCase, chkWhole), vtui.Margins{}, vtui.AlignFill)
		vbox.Add(optionsRow(chkRegexp, chkNotContaining), vtui.Margins{}, vtui.AlignFill)
		vbox.Add(optionsRow(chkFolders, chkSymlinks), vtui.Margins{}, vtui.AlignFill)
		vbox.Apply()

		rightX := func(cb *vtui.Checkbox) int { x1, _, _, _ := cb.GetPosition(); return x1 }
		want := rightX(chkWhole)
		for _, cb := range []*vtui.Checkbox{chkNotContaining, chkSymlinks} {
			if got := rightX(cb); got != want {
				t.Errorf("right column misaligned: %q starts at %d, want %d", cb.GetText(), got, want)
			}
		}
		return dlg
	}
	vtui.AssertLayoutInLanguages(t, packs, build)
}
