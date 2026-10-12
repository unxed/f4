package vtvibe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// f4#1842, stage H3: the dialog outlives the f4 that held it.
func TestDialogSurvivesARestart(t *testing.T) {
	srv, _ := fakeAgentServer(t, finalReply)
	path := filepath.Join(t.TempDir(), "ai", "dialog.json")

	first := NewSession()
	if err := first.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	if err := first.tree.writeFile(ctxDir+"/notes.txt", []byte("attached file")); err != nil {
		t.Fatal(err)
	}
	if err := first.tree.writeFile(draftFile, []byte("half-written question")); err != nil {
		t.Fatal(err)
	}
	first.SetPatchMode(true)
	if err := first.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "hello"); err != nil {
		t.Fatal(err)
	}
	first.SetTitle("Release notes")
	if err := first.StoreError(); err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits: Go reports 0666 there.
	if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("dialog file: %v, %v", info, err)
	}

	second := NewSession()
	if err := second.SetStorePath(path); err != nil {
		t.Fatal(err)
	}
	turns := second.Turns()
	if len(turns) != len(first.Turns()) || turns[len(turns)-1].Text != "done: pong" || turns[len(turns)-2].Text != "hello" {
		t.Fatalf("turns not restored: %#v", turns)
	}
	if second.Title() != "Release notes" || !second.PatchMode() {
		t.Fatalf("title %q, patch mode %v", second.Title(), second.PatchMode())
	}
	if data, ok := second.tree.readFile(ctxDir + "/notes.txt"); !ok || string(data) != "attached file" {
		t.Fatalf("context file not restored: %q", data)
	}
	if second.Draft() != "half-written question" {
		t.Fatalf("draft = %q", second.Draft())
	}
	if _, ok := second.tree.readFile(chatDir + "/0003-model.md"); !ok {
		t.Fatal("chat files not rebuilt for F3")
	}
}

func TestArchiveKeepsTheOldDialogAndResetStartsANewOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dialog.json")
	s := NewSession()
	_ = s.SetStorePath(path)
	if got, err := s.Archive(filepath.Join(dir, "dialogs")); err != nil || got != "" {
		t.Fatalf("an empty dialog was archived: %q, %v", got, err)
	}
	s.Note("assistant", "something worth keeping")
	s.SetTitle("My: dialog?")
	archived, err := s.Archive(filepath.Join(dir, "dialogs"))
	if err != nil || !strings.HasSuffix(archived, "_My-dialog.json") {
		t.Fatalf("archive = %q, %v", archived, err)
	}
	data, err := os.ReadFile(archived)
	if err != nil || !strings.Contains(string(data), "something worth keeping") {
		t.Fatalf("archive content: %v", err)
	}
	s.Reset(true)
	again := NewSession()
	_ = again.SetStorePath(path)
	if len(again.Turns()) != 1 || again.Title() != "" {
		t.Fatalf("after ai:new the saved dialog still holds %d turns, title %q", len(again.Turns()), again.Title())
	}
}

func TestDamagedDialogFileStartsAFreshDialog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dialog.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSession()
	if err := s.SetStorePath(path); err == nil {
		t.Fatal("a damaged file was accepted silently")
	}
	s.Note("assistant", "new")
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"new"`) {
		t.Fatalf("the fresh dialog is not saved over the damaged file: %s", data)
	}
}

func TestArchivedDialogsAreListedAndOpened(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "dialogs")
	s := NewSession()
	_ = s.SetStorePath(filepath.Join(dir, "dialog.json"))
	s.Note("assistant", "first dialog")
	s.SetTitle("First")
	if _, err := s.Archive(archive); err != nil {
		t.Fatal(err)
	}
	s.Reset(true)
	s.Note("assistant", "second dialog")

	list, err := ListArchive(archive)
	if err != nil || len(list) != 1 || list[0].Title != "First" || list[0].Messages != 1 {
		t.Fatalf("archive list = %#v, %v", list, err)
	}
	if err := s.OpenArchived(list[0].Path, archive); err != nil {
		t.Fatal(err)
	}
	turns := s.Turns()
	if s.Title() != "First" || turns[len(turns)-1].Text != "first dialog" {
		t.Fatalf("opened dialog: title %q, last %q", s.Title(), turns[len(turns)-1].Text)
	}
	after, _ := ListArchive(archive)
	if len(after) != 1 || after[0].Title != "" {
		t.Fatalf("the second dialog should be archived and the first should leave the archive: %#v", after)
	}
	if missing, err := ListArchive(filepath.Join(dir, "none")); err != nil || missing != nil {
		t.Fatalf("missing archive dir = %v, %v", missing, err)
	}
}

func TestArchiveNeverOverwritesAnEarlierDialog(t *testing.T) {
	dir := t.TempDir()
	s := NewSession()
	_ = s.SetStorePath(filepath.Join(dir, "dialog.json"))
	s.Note("assistant", "one")
	a, _ := s.Archive(filepath.Join(dir, "dialogs"))
	b, err := s.Archive(filepath.Join(dir, "dialogs"))
	if err != nil || a == b {
		t.Fatalf("two archives in one second share %q (%v)", a, err)
	}
}
