package main

import (
	"strings"
	"testing"

	"github.com/unxed/vtui"
)

func TestLoadAllLanguagePacks(t *testing.T) {
	packs := LoadAllLanguagePacks()
	if len(packs) < 2 {
		t.Fatalf("expected at least the bundled en and ru packs, got %d", len(packs))
	}

	seen := make(map[string]bool)
	for _, p := range packs {
		if p.Name == "" {
			t.Error("language pack without a name")
		}
		if len(p.Strings) == 0 {
			t.Errorf("language pack %q carries no strings", p.Name)
		}
		if seen[p.Name] {
			t.Errorf("duplicate language pack %q", p.Name)
		}
		seen[p.Name] = true
	}

	if !seen["en"] || !seen["ru"] {
		t.Errorf("expected the en and ru packs to be present, got %v", seen)
	}
}

func TestEmbeddedCurrentLanguageContainsNewCommandStrings(t *testing.T) {
	ru := loadEmbeddedLanguageMap("ru")
	if got := ru["Action.Workspace.Close"]; !strings.Contains(got, "Закрыть") {
		t.Fatalf("embedded Russian workspace label = %q", got)
	}
	if got := ru["Archive.Command.Extract"]; !strings.Contains(got, "Извлечь") {
		t.Fatalf("embedded Russian plugin label = %q", got)
	}
	if got := loadEmbeddedLanguageMap("does-not-exist"); got != nil {
		t.Fatalf("unknown embedded language = %#v, want nil", got)
	}
}

func TestInitLangUsesEmbeddedCurrentLanguageAndResetsFallback(t *testing.T) {
	previous := AppConfig
	t.Cleanup(func() {
		AppConfig = previous
		InitLang()
	})

	AppConfig.Language = "ru"
	AppConfig.FallbackLanguage = ""
	InitLang()
	if got := Msg("Action.Workspace.Close"); !strings.Contains(got, "Закрыть") {
		t.Fatalf("Russian current-language label = %q", got)
	}

	AppConfig.Language = "de"
	InitLang()
	if got := Msg("Action.Workspace.Close"); plainLabel(got) != "Close workspace" {
		t.Fatalf("missing German translation inherited a stale language: %q", got)
	}
}

func TestInitLangPreservesRuntimePluginStrings(t *testing.T) {
	previousConfig := AppConfig
	previousStrings := vtui.SnapshotStrings()
	languageState.Lock()
	previousCore := make(map[string]string, len(languageState.core))
	for key, value := range languageState.core {
		previousCore[key] = value
	}
	languageState.Unlock()
	t.Cleanup(func() {
		AppConfig = previousConfig
		languageState.Lock()
		languageState.core = previousCore
		vtui.ReplaceStrings(previousStrings)
		languageState.Unlock()
	})

	const key = "Test.Plugin.RuntimeTranslation"
	vtui.AddStrings(map[string]string{key: "runtime plugin text"})
	AppConfig.Language = "ru"
	AppConfig.FallbackLanguage = ""
	InitLang()
	if got := Msg(key); got != "runtime plugin text" {
		t.Fatalf("runtime plugin string after language switch = %q", got)
	}
}

func TestLayout_ButtonRow_AllLanguages(t *testing.T) {
	vtui.SetDefaultPalette()

	packs := LoadAllLanguagePacks()
	if len(packs) == 0 {
		t.Skip("no language packs bundled")
	}
	// A button row built from localized captions must keep clear of the dialog
	// border in every language, not only in the one currently loaded.
	vtui.AssertLayoutInLanguages(t, packs, func() vtui.Container {
		const width, height = 60, 10
		dlg := vtui.NewDialog(0, 0, width-1, height-1, Msg("VisRen.Title"))

		btnRename := vtui.NewButton(0, 0, Msg("VisRen.Rename"))
		btnCancel := vtui.NewButton(0, 0, Msg("VisRen.Cancel"))

		row := vtui.NewHBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, 1)
		row.Spacing = 2
		row.Add(btnRename, vtui.Margins{}, vtui.AlignTop)
		row.Add(btnCancel, vtui.Margins{}, vtui.AlignTop)
		row.Apply()

		dlg.AddItem(btnRename)
		dlg.AddItem(btnCancel)
		return dlg
	})
}

func TestLayout_FileOpConflictButtons_AllLanguages(t *testing.T) {
	vtui.SetDefaultPalette()

	packs := LoadAllLanguagePacks()
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

	// The overwrite dialog has six actions. The row helper must wrap long
	// translations before they can cross the dialog border.
	vtui.AssertLayoutInLanguages(t, packs, func() vtui.Container {
		const width = 76
		buttons := []*vtui.Button{
			vtui.NewButton(0, 0, Msg("FileOp.Overwrite")),
			vtui.NewButton(0, 0, Msg("FileOp.Skip")),
			vtui.NewButton(0, 0, Msg("FileOp.Rename")),
			vtui.NewButton(0, 0, Msg("FileOp.Append")),
			vtui.NewButton(0, 0, Msg("FileOp.Resume")),
			vtui.NewButton(0, 0, Msg("vtui.Cancel")),
		}
		rows := dialogButtonRows(buttons, width-4, 1)
		dlg := vtui.NewDialog(0, 0, width-1, 2+2*len(rows), Msg("Warning.Title"))
		for _, button := range buttons {
			dlg.AddItem(button)
		}
		vbox := vtui.NewVBoxLayout(dlg.X1+2, dlg.Y1+2, width-4, 2*len(rows)-1)
		for i, row := range rows {
			margin := vtui.Margins{}
			if i < len(rows)-1 {
				margin.Bottom = 1
			}
			vbox.Add(row, margin, vtui.AlignFill)
		}
		vbox.Apply()
		return dlg
	})
}

func TestLayout_FileAssociationEditor_AllLanguages(t *testing.T) {
	vtui.SetDefaultPalette()

	packs := LoadAllLanguagePacks()
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
		(&assocEditorState{}).editAt(0, true)
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

	packs := LoadAllLanguagePacks()
	if len(packs) == 0 {
		t.Skip("no language packs bundled")
	}

	// The Find File dialog lays its six option checkboxes out as three
	// two-column rows. The right column must start at the same X in
	// every row (#903), and the captions must still fit the dialog.
	build := func() vtui.Container {
		const width, height = 78, 20
		dlg := vtui.NewDialog(0, 0, width-1, height-1, Msg("FindFile.Title"))

		chkCase := vtui.NewCheckbox(0, 0, Msg("FindFile.CaseSensitive"), false)
		chkWhole := vtui.NewCheckbox(0, 0, Msg("FindFile.WholeWords"), false)
		chkRegexp := vtui.NewCheckbox(0, 0, Msg("FindFile.Regexp"), false)
		chkNotContaining := vtui.NewCheckbox(0, 0, Msg("FindFile.NotContaining"), false)
		chkFolders := vtui.NewCheckbox(0, 0, Msg("FindFile.Folders"), false)
		chkSymlinks := vtui.NewCheckbox(0, 0, Msg("FindFile.Symlinks"), false)
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
