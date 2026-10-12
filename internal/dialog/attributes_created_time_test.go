package dialog

import (
	"strings"
	"testing"
	"time"

	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// dialogTexts collects every *vtui.Text caption in the dialog, the way the
// existing multiple-selection tests already do.
func dialogTexts(t *testing.T, dlg vtui.UIElement) []string {
	t.Helper()
	var texts []string
	walkUI(dlg, func(el vtui.UIElement) bool {
		if c, ok := el.(*vtui.Text); ok {
			texts = append(texts, c.GetText())
		}
		return true
	})
	return texts
}

// containsPrefix reports whether any text starts with label (PadLabel pads
// labels with trailing spaces, so an exact match would be brittle).
func containsPrefix(texts []string, label string) bool {
	for _, text := range texts {
		if strings.HasPrefix(text, label) {
			return true
		}
	}
	return false
}

func containsText(texts []string, s string) bool {
	for _, text := range texts {
		if text == s {
			return true
		}
	}
	return false
}

// f4#1404: the reporter's explicit ask was a read-only creation date, "особенно
// часто нужна дата создания. В режиме readonly." When the platform reports a
// birth time (VFSItem.BTime + MetadataBTime, see vfs/os_vfs_posix_atimespec.go
// and vfs/os_vfs_windows.go), the Unix-shaped dialog must show it, together
// with Accessed and Changed (both already free: ATime/CTime were already
// populated for every OSVFS item before this change).
func TestAttributesDialog_UnixShowsCreatedAccessedChanged(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	created := time.Date(2020, 1, 2, 3, 4, 5, 0, time.Local)
	accessed := time.Date(2023, 6, 7, 8, 9, 10, 0, time.Local)
	changed := time.Date(2024, 11, 12, 13, 14, 15, 0, time.Local)
	item := vfs.VFSItem{
		Name: "a.txt", UnixMode: 0644, MTime: time.Now(),
		BTime: created, ATime: accessed, CTime: changed,
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataBTime | vfs.MetadataATime | vfs.MetadataCTime,
	}

	ShowAttributesUnix(nil, mockVFS, "a.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)
	texts := dialogTexts(t, dlg.(vtui.UIElement))

	for _, want := range []struct {
		label string
		value string
	}{
		{i18n.Msg("Attributes.Created"), created.Format(attributesTimeFormat)},
		{i18n.Msg("Attributes.Changed"), changed.Format(attributesTimeFormat)},
	} {
		if !containsPrefix(texts, want.label) {
			t.Errorf("dialog lacks a %q label; texts:\n%s", want.label, strings.Join(texts, "\n"))
		}
		if !containsText(texts, want.value) {
			t.Errorf("dialog lacks the value %q; texts:\n%s", want.value, strings.Join(texts, "\n"))
		}
	}
	// Accessed is the one editable one of the three (f4#1404): a label and an
	// edit field holding the value.
	if !containsPrefix(texts, i18n.Msg("Attributes.Accessed")) {
		t.Errorf("dialog lacks an Accessed label; texts:\n%s", strings.Join(texts, "\n"))
	}
	var accessedEdit bool
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if e, ok := el.(*vtui.Edit); ok && e.GetText() == accessed.Format(attributesTimeFormat) {
			accessedEdit = true
		}
		return true
	})
	if !accessedEdit {
		t.Errorf("no edit field holds the Accessed time %q", accessed.Format(attributesTimeFormat))
	}

	// Created and Changed are read-only display: no Edit widgets for them;
	// the edit fields are owner, group, octal, M-Time and Accessed = 5.
	var edits int
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		if _, ok := el.(*vtui.Edit); ok {
			edits++
		}
		return true
	})
	if edits != 5 {
		t.Errorf("edit field count = %d, want 5 (owner, group, octal, M-Time, Accessed); Created/Changed must not be editable", edits)
	}
}

// A platform/filesystem that can't report a field (e.g. no birth time on
// Linux's classic stat(2), or a VFS backend that never learned it) must make
// the dialog omit that row entirely — never show a wrong zero/epoch date in
// its place.
func TestAttributesDialog_UnixOmitsUnavailableCreatedTime(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	accessed := time.Date(2023, 6, 7, 8, 9, 10, 0, time.Local)
	changed := time.Date(2024, 11, 12, 13, 14, 15, 0, time.Local)
	item := vfs.VFSItem{
		Name: "a.txt", UnixMode: 0644, MTime: time.Now(),
		ATime: accessed, CTime: changed,
		// Explicit metadata, BTime bit deliberately absent: "known unknown".
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataATime | vfs.MetadataCTime,
	}

	ShowAttributesUnix(nil, mockVFS, "a.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)
	texts := dialogTexts(t, dlg.(vtui.UIElement))

	if containsPrefix(texts, i18n.Msg("Attributes.Created")) {
		t.Errorf("dialog shows a Created row for an item with no known birth time:\n%s", strings.Join(texts, "\n"))
	}
	if !containsPrefix(texts, i18n.Msg("Attributes.Accessed")) {
		t.Error("dialog should still show Accessed: it is known for this item")
	}
	if !containsPrefix(texts, i18n.Msg("Attributes.Changed")) {
		t.Error("dialog should still show Changed: it is known for this item")
	}
}

// A multiple selection with differing creation times must say so, the same
// way owner/group already do, instead of showing one object's date as if it
// were the whole selection's.
func TestAttributesDialog_UnixMultipleSelectionCreatedTimeDiffers(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	targets := []AttributesTarget{
		{Path: "first.txt", Item: vfs.VFSItem{
			Name: "first.txt", UnixMode: 0644,
			BTime:         time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local),
			KnownMetadata: vfs.MetadataExplicit | vfs.MetadataBTime,
		}},
		{Path: "second.txt", Item: vfs.VFSItem{
			Name: "second.txt", UnixMode: 0644,
			BTime:         time.Date(2021, 2, 2, 0, 0, 0, 0, time.Local),
			KnownMetadata: vfs.MetadataExplicit | vfs.MetadataBTime,
		}},
	}

	ShowAttributesUnixForTargets(nil, mockVFS, targets)
	dlg := fm.GetTopFrame().(vtui.Container)
	texts := dialogTexts(t, dlg.(vtui.UIElement))

	if !containsPrefix(texts, i18n.Msg("Attributes.Created")) {
		t.Fatalf("dialog lacks a Created row when both objects have a known (if different) birth time:\n%s", strings.Join(texts, "\n"))
	}
	if !containsText(texts, i18n.Msg("Attributes.MultipleValues")) {
		t.Errorf("Created row should read %q for differing birth times:\n%s", i18n.Msg("Attributes.MultipleValues"), strings.Join(texts, "\n"))
	}
}

// The Windows-shaped dialog gets Created and Accessed too, but never a
// "Changed" row: NTFS/Win32FileAttributeData has no ctime-shaped "metadata
// changed" timestamp comparable to Unix's, so there is nothing honest to
// label that way there.
func TestAttributesDialog_WindowsShowsCreatedAndAccessedNoChanged(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	created := time.Date(2019, 3, 4, 5, 6, 7, 0, time.Local)
	accessed := time.Date(2022, 8, 9, 10, 11, 12, 0, time.Local)
	item := vfs.VFSItem{
		Name: "a.txt", MTime: time.Now(),
		BTime: created, ATime: accessed,
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataBTime | vfs.MetadataATime,
	}

	ShowAttributesWindows(nil, mockVFS, "a.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)
	texts := dialogTexts(t, dlg.(vtui.UIElement))

	if !containsPrefix(texts, i18n.Msg("Attributes.Created")) {
		t.Errorf("Windows dialog lacks a Created row:\n%s", strings.Join(texts, "\n"))
	}
	if !containsText(texts, created.Format(attributesTimeFormat)) {
		t.Errorf("Windows dialog lacks the Created value:\n%s", strings.Join(texts, "\n"))
	}
	if !containsPrefix(texts, i18n.Msg("Attributes.Accessed")) {
		t.Errorf("Windows dialog lacks an Accessed row:\n%s", strings.Join(texts, "\n"))
	}
	if containsPrefix(texts, i18n.Msg("Attributes.Changed")) {
		t.Errorf("Windows dialog must not show a Changed row:\n%s", strings.Join(texts, "\n"))
	}
}

// A selection with every optional time row showing must still lay out
// without overlap/overflow, the same guarantee TestAttributesDialog_
// MultipleSelectionLayout already gives the base dialog.
func TestAttributesDialog_TimeRowsLayout(t *testing.T) {
	created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.Local)
	accessed := time.Date(2021, 1, 1, 0, 0, 0, 0, time.Local)
	changed := time.Date(2022, 1, 1, 0, 0, 0, 0, time.Local)
	knownAll := vfs.MetadataExplicit | vfs.MetadataBTime | vfs.MetadataATime | vfs.MetadataCTime
	targets := []AttributesTarget{
		{Path: "first.txt", Item: vfs.VFSItem{
			Name: "first.txt", UnixMode: 0644, MTime: time.Now(),
			BTime: created, ATime: accessed, CTime: changed, KnownMetadata: knownAll,
		}},
	}
	for _, show := range []struct {
		name string
		open func(vfs.VFS)
	}{
		{"unix", func(v vfs.VFS) { ShowAttributesUnixForTargets(nil, v, targets) }},
		{"windows", func(v vfs.VFS) {
			ShowAttributesWindowsWithPropertiesForTargets(nil, v, targets, func(string) error { return nil })
		}},
	} {
		t.Run(show.name, func(t *testing.T) {
			scr := vtui.NewSilentScreenBuf()
			scr.AllocBuf(80, 30)
			vtui.FrameManager.Init(scr)
			show.open(vfs.NewOSVFS(t.TempDir()))
			top := vtui.FrameManager.GetTopFrame()
			dlg, ok := top.(vtui.Container)
			if !ok {
				t.Fatal("attributes dialog not found")
			}
			vtui.AssertLayout(t, dlg)
			top.SetExitCode(-1)
			vtui.FrameManager.Pop()
		})
	}
}

// f4#1817: the rows go Created, Modified, Metadata (changed), Accessed.
func TestAttributesDialog_UnixTimeRowsOrder(t *testing.T) {
	fm := vtui.FrameManager
	fm.Init(vtui.NewSilentScreenBuf())

	mockVFS := &mockMetadataVFS{VFS: vfs.NewOSVFS(t.TempDir())}
	mtime := time.Date(2021, 3, 4, 5, 6, 7, 0, time.Local)
	item := vfs.VFSItem{
		Name: "a.txt", UnixMode: 0644, MTime: mtime,
		BTime:         time.Date(2020, 1, 2, 3, 4, 5, 0, time.Local),
		ATime:         time.Date(2023, 6, 7, 8, 9, 10, 0, time.Local),
		CTime:         time.Date(2024, 11, 12, 13, 14, 15, 0, time.Local),
		KnownMetadata: vfs.MetadataExplicit | vfs.MetadataBTime | vfs.MetadataATime | vfs.MetadataCTime,
	}

	ShowAttributesUnix(nil, mockVFS, "a.txt", item)
	dlg := fm.GetTopFrame().(vtui.Container)
	rows := map[string]int{}
	walkUI(dlg.(vtui.UIElement), func(el vtui.UIElement) bool {
		switch c := el.(type) {
		case *vtui.Text:
			for _, key := range []string{"Attributes.Created", "Attributes.Changed", "Attributes.Accessed"} {
				if strings.HasPrefix(c.GetText(), i18n.Msg(key)) {
					rows[key] = c.Y1
				}
			}
		case *vtui.Edit:
			if c.GetText() == mtime.Format(attributesTimeFormat) {
				rows["Attributes.MTime"] = c.Y1
			}
		}
		return true
	})
	order := []string{"Attributes.Created", "Attributes.MTime", "Attributes.Changed", "Attributes.Accessed"}
	previous := ""
	for _, key := range order {
		if _, ok := rows[key]; !ok {
			t.Fatalf("no row for %s: %v", key, rows)
		}
		if previous != "" && rows[previous] >= rows[key] {
			t.Errorf("%s (line %d) is not below %s (line %d)", key, rows[key], previous, rows[previous])
		}
		previous = key
	}
	fm.GetTopFrame().SetExitCode(-1)
	fm.Pop()
}
