package id3editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/id3-go"
	v1 "github.com/unxed/id3-go/v1"
	v2 "github.com/unxed/id3-go/v2"
	"github.com/unxed/vtui"
)

type ID3EditorPlugin struct {
	api                  vfs.HostAPI
	registration         vfs.Registration
	settingsRegistration vfs.Registration
}

func (p *ID3EditorPlugin) Init(api vfs.HostAPI) error {
	p.api = api
	if settingsHost, ok := api.(vfs.SettingsContributionHost); ok {
		registration, err := settingsHost.RegisterSettingsProvider(id3SettingsProvider())
		if err != nil {
			p.api = nil
			return fmt.Errorf("ID3 editor: register settings: %w", err)
		}
		p.settingsRegistration = registration
	}
	if contributions, ok := api.(vfs.ContributionHost); ok {
		registration, err := contributions.RegisterPluginCommand(vfs.PluginCommand{
			ID:             "id3editor.edit",
			Location:       vfs.PluginCommandPanel,
			Label:          "ID3 Tag &Editor",
			LabelKey:       "ID3Editor.Menu",
			Description:    "Edit ID3 metadata of the selected MP3 file",
			DescriptionKey: "ID3Editor.Command.Edit.Desc",
			SearchKeys:     []string{"ID3Editor.Title", "ID3Editor.FieldTitle", "ID3Editor.FieldArtist"},
			Enabled:        canHandleEdit,
			Run:            p.handleEdit,
		})
		if err != nil {
			p.api = nil
			return fmt.Errorf("ID3 editor: register command: %w", err)
		}
		p.registration = registration
		return nil
	}
	api.RegisterPluginMenuItem(vtui.Msg("ID3Editor.Menu"), func(app vfs.App) {
		p.handleEdit(app)
	})
	return nil
}

func (p *ID3EditorPlugin) Close() error {
	if p.settingsRegistration != nil {
		p.settingsRegistration.Unregister()
		p.settingsRegistration = nil
	}
	if p.registration != nil {
		p.registration.Unregister()
		p.registration = nil
	}
	p.api = nil
	return nil
}

func (p *ID3EditorPlugin) GetName() string {
	return "ID3 Tag Editor"
}

// isSupportedID3Extension reports whether name's extension is one the ID3
// Tag Editor knows how to read. handleEdit's own guard below and the
// Enabled predicate registered in Init (f4#1356) both call this, so "which
// files this plugin supports" has exactly one definition instead of two
// that could silently drift apart.
func isSupportedID3Extension(name string) bool {
	return strings.ToLower(filepath.Ext(name)) == ".mp3"
}

// canHandleEdit reports whether handleEdit would actually open the editor
// for app's current active panel and selection, without any of its side
// effects (no message dialogs). It backs the plugin command's Enabled
// predicate (f4#1356): a menu item/command-palette entry with Enabled
// returning false is dimmed instead of popping an error dialog when the
// command plainly cannot apply — non-local panel, nothing selected, or a
// selection that is not an MP3 file.
func canHandleEdit(app vfs.App) bool {
	activeVFS := app.GetActivePanelVFS()
	if activeVFS == nil {
		return false
	}
	if _, isLocal := activeVFS.(*vfs.OSVFS); !isLocal {
		return false
	}
	names := app.GetSelectedNames()
	if len(names) == 0 {
		return false
	}
	return isSupportedID3Extension(names[0])
}

func (p *ID3EditorPlugin) handleEdit(app vfs.App) {
	activeVFS := app.GetActivePanelVFS()
	if activeVFS == nil {
		vtui.ShowMessage(" Error ", vtui.Msg("ID3Editor.LocalOnly"), []string{"&Ok"})
		return
	}

	if _, isLocal := activeVFS.(*vfs.OSVFS); !isLocal {
		vtui.ShowMessage(" Error ", vtui.Msg("ID3Editor.LocalOnly"), []string{"&Ok"})
		return
	}

	names := app.GetSelectedNames()
	if len(names) == 0 {
		// Neutral hint ("select a file first"), not an alarm — a
		// non-whitelist title keeps this on the info palette. See #379.
		vtui.ShowMessage(" ID3 Editor ", vtui.Msg("ID3Editor.SelectFile"), []string{"&Ok"})
		return
	}

	name := names[0]
	if !isSupportedID3Extension(name) {
		// Same rationale as the SelectFile hint above.
		vtui.ShowMessage(" ID3 Editor ", vtui.Msg("ID3Editor.OnlyMP3"), []string{"&Ok"})
		return
	}

	fullPath, err := activeVFS.Abs(name)
	if err != nil {
		vtui.ShowMessage(" Error ", fmt.Sprintf(vtui.Msg("ID3Editor.OpenError"), err), []string{"&Ok"})
		return
	}

	p.showEditorDialog(app, fullPath)
}

func (p *ID3EditorPlugin) showEditorDialog(app vfs.App, fullPath string) {
	original, statErr := os.Stat(fullPath)
	file, err := id3.Open(fullPath)
	if err != nil {
		app.Message(" Error ", fmt.Sprintf(vtui.Msg("ID3Editor.OpenError"), err), []string{"&Ok"})
		return
	}

	width, height := 66, 17
	dlg := vtui.NewCenteredDialog(width, height, vtui.Msg("ID3Editor.Title"))
	dlg.ShowClose = true

	cleanStr := func(s string) string {
		return strings.TrimSpace(strings.TrimRight(s, "\x00"))
	}

	titleText := cleanStr(file.Title())
	artistText := cleanStr(file.Artist())
	albumText := cleanStr(file.Album())
	yearText := cleanStr(file.Year())
	genreText := cleanStr(file.Genre())

	commentText := ""
	if comments := file.Comments(); len(comments) > 0 {
		commentText = cleanStr(comments[0])
	}

	editTitle := vtui.NewEdit(0, 0, 44, titleText)
	editArtist := vtui.NewEdit(0, 0, 44, artistText)
	editAlbum := vtui.NewEdit(0, 0, 44, albumText)
	editYear := vtui.NewEdit(0, 0, 10, yearText)
	editGenre := vtui.NewEdit(0, 0, 20, genreText)
	editComment := vtui.NewEdit(0, 0, 44, commentText)

	padLabel := func(s string) string {
		for len([]rune(s)) < 10 {
			s += " "
		}
		return s
	}

	lblTitle := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldTitle")), editTitle)
	lblArtist := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldArtist")), editArtist)
	lblAlbum := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldAlbum")), editAlbum)
	lblYear := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldYear")), editYear)
	lblGenre := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldGenre")), editGenre)
	lblComment := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldComment")), editComment)

	btnSave := vtui.NewButton(0, 0, vtui.Msg("vtui.Save"))
	btnSave.IsDefault = true
	btnCancel := vtui.NewButton(0, 0, vtui.Msg("vtui.Cancel"))

	dlg.AddItem(lblTitle)
	dlg.AddItem(editTitle)
	dlg.AddItem(lblArtist)
	dlg.AddItem(editArtist)
	dlg.AddItem(lblAlbum)
	dlg.AddItem(editAlbum)
	dlg.AddItem(lblYear)
	dlg.AddItem(editYear)
	dlg.AddItem(lblGenre)
	dlg.AddItem(editGenre)
	dlg.AddItem(lblComment)
	dlg.AddItem(editComment)
	dlg.AddItem(btnSave)
	dlg.AddItem(btnCancel)

	versionText := file.Version()
	lblVersion := vtui.NewLabel(0, 0, padLabel(vtui.Msg("ID3Editor.FieldVersion")), nil)
	valVersion := vtui.NewLabel(0, 0, versionText, nil)
	dlg.AddItem(lblVersion)
	dlg.AddItem(valVersion)
	valVersion.SetText(versionText)

	vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, height-4)

	addFormRow := func(lbl vtui.UIElement, edit vtui.UIElement, topMargin int) {
		row := vtui.NewHBoxLayout(0, 0, width-4, 1)
		row.Add(lbl, vtui.Margins{Right: 1}, vtui.AlignLeft)
		row.Add(edit, vtui.Margins{}, vtui.AlignFill)
		vbox.Add(row, vtui.Margins{Top: topMargin}, vtui.AlignFill)
	}

	addFormRow(lblTitle, editTitle, 0)
	addFormRow(lblArtist, editArtist, 1)
	addFormRow(lblAlbum, editAlbum, 1)

	rowYearGenre := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowYearGenre.Add(lblYear, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowYearGenre.Add(editYear, vtui.Margins{Right: 2}, vtui.AlignLeft)
	rowYearGenre.Add(lblGenre, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowYearGenre.Add(editGenre, vtui.Margins{}, vtui.AlignFill)
	vbox.Add(rowYearGenre, vtui.Margins{Top: 1}, vtui.AlignFill)

	addFormRow(lblComment, editComment, 1)

	rowVersion := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowVersion.Add(lblVersion, vtui.Margins{Right: 1}, vtui.AlignLeft)
	rowVersion.Add(valVersion, vtui.Margins{}, vtui.AlignLeft)
	vbox.Add(rowVersion, vtui.Margins{Top: 1}, vtui.AlignFill)

	rowBtns := vtui.NewHBoxLayout(0, 0, width-4, 1)
	rowBtns.HorizontalAlign = vtui.AlignCenter
	rowBtns.Spacing = 2
	rowBtns.Add(btnSave, vtui.Margins{}, vtui.AlignTop)
	rowBtns.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
	vbox.Add(rowBtns, vtui.Margins{Top: 1}, vtui.AlignFill)

	vbox.Apply()

	closed := false
	safeClose := func() {
		if !closed {
			_ = file.Close()
			closed = true
		}
	}

	btnCancel.OnClick = func() {
		safeClose()
		dlg.Close()
	}

	btnSave.OnClick = func() {
		file.SetTitle(editTitle.GetText())
		file.SetArtist(editArtist.GetText())
		file.SetAlbum(editAlbum.GetText())
		file.SetYear(editYear.GetText())
		file.SetGenre(editGenre.GetText())
		setComment(file.Tagger, editComment.GetText())

		err := file.Close()
		closed = true
		if err == nil && statErr == nil && loadID3EditorSettings().PreserveModTime {
			err = os.Chtimes(fullPath, original.ModTime(), original.ModTime())
		}
		if err != nil {
			app.Message(" Error ", fmt.Sprintf(vtui.Msg("ID3Editor.SaveError"), err), []string{"&Ok"})
			return
		}

		dlg.Close()
		app.RefreshAll()
	}

	dlg.OnResult = func(code int) {
		safeClose()
	}

	vtui.FrameManager.Push(dlg)
}

func setComment(tagger id3.Tagger, comment string) {
	switch t := tagger.(type) {
	case *v1.Tag:
		t.SetComment(comment)
	case *v2.Tag:
		if strings.HasPrefix(t.Version(), "2.2.") {
			t.DeleteFrames("COM")
			ft := v2.V22FrameTypeMap["COM"]
			f := v2.NewUnsynchTextFrame(ft, "", comment)
			_ = f.SetEncoding("UTF-8")
			t.AddFrames(f)
		} else {
			t.DeleteFrames("COMM")
			ft := v2.V23FrameTypeMap["COMM"]
			f := v2.NewUnsynchTextFrame(ft, "", comment)
			_ = f.SetEncoding("UTF-8")
			t.AddFrames(f)
		}
	}
}
