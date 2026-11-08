package history

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func saveLegacyCommandPaths(t *testing.T, hp *F4HistoryProvider, byCommand map[string]string) {
	t.Helper()
	var encoded []string
	for command, path := range byCommand {
		data, err := json.Marshal(commandHistoryPathRecord{Command: command, Path: path})
		if err != nil {
			t.Fatal(err)
		}
		encoded = append(encoded, string(data))
	}
	hp.SaveHistory(CommandHistoryPathsID, encoded)
}

// Alt+F8, the completion menu and adding a command each loaded the history
// their own way, and only Alt+F8 knew the old per-command directories.
func TestCommandRecordsFoldTheLegacyDirectoriesByName(t *testing.T) {
	hp := NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	hp.SaveRichHistory("cmdline", []HistoryRecord{
		{Name: "make", Dir: "/new"},
		{Name: "./old.sh"},
		{Name: "ls"},
	})
	saveLegacyCommandPaths(t, hp, map[string]string{"make": "/legacy", "./old.sh": "/proj"})

	got := CommandRecords(hp, nil)
	if len(got) != 3 {
		t.Fatalf("records = %+v", got)
	}
	if got[0].Directory() != "/new" {
		t.Fatalf("a record's own directory must win: %q", got[0].Directory())
	}
	if got[1].Directory() != "/proj" {
		t.Fatalf("./old.sh must take its legacy directory, got %q", got[1].Directory())
	}
	if got[2].Directory() != "" {
		t.Fatalf("ls has no directory anywhere, got %q", got[2].Directory())
	}

	// The legacy directory is what lists an old command as run here.
	items := completionTexts(CommandCompletions(got, "./", "/proj", 0))
	if len(items) == 0 || items[0] != "./old.sh" {
		t.Fatalf("completions in /proj = %q, want ./old.sh first", items)
	}
}

func TestCommandRecordsFallBackToNames(t *testing.T) {
	hp := NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	got := CommandRecords(hp, []string{"a", "b"})
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("records = %+v, want the names", got)
	}
	if got := CommandRecords(nil, []string{"a"}); len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("no provider: records = %+v", got)
	}
	if got := CommandRecords(hp, nil); got != nil {
		t.Fatalf("nothing anywhere: records = %+v", got)
	}
}

func TestProviderGenerationCountsSaves(t *testing.T) {
	hp := NewProviderAtPath(filepath.Join(t.TempDir(), "history.json"))
	g0 := hp.Generation()
	hp.SaveHistory("cmdline", []string{"a"})
	g1 := hp.Generation()
	hp.SaveRichHistory("cmdline", []HistoryRecord{{Name: "b"}})
	g2 := hp.Generation()
	_ = hp.LoadRichHistory("cmdline")
	if g0 >= g1 || g1 >= g2 || hp.Generation() != g2 {
		t.Fatalf("generations %d %d %d %d", g0, g1, g2, hp.Generation())
	}
}
