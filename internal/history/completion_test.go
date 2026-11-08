package history

import (
	"reflect"
	"testing"

	"github.com/unxed/vtui"
)

func completionTexts(items []vtui.AutoCompleteItem) []string {
	var texts []string
	for _, it := range items {
		if it.Separator {
			texts = append(texts, "---")
			continue
		}
		texts = append(texts, it.Text)
	}
	return texts
}

// Typing "./" offered every command that merely contained "./" somewhere.
// far2l matches the start of the entry only.
func TestCommandCompletionsMatchThePrefixOnly(t *testing.T) {
	records := RecordsFromNames([]string{
		"cd ../x", "./build.sh", "git add ./y", "./run.sh --fast", "python ../a.py",
	})
	got := completionTexts(CommandCompletions(records, "./", ""))
	want := []string{"./build.sh", "./run.sh --fast"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("./ -> %q, want %q", got, want)
	}

	got = completionTexts(CommandCompletions(records, "./b", ""))
	if !reflect.DeepEqual(got, []string{"./build.sh"}) {
		t.Fatalf("./b -> %q, want only ./build.sh", got)
	}
}

func TestCommandCompletionsCaseAndExactEntry(t *testing.T) {
	records := RecordsFromNames([]string{"make", "Make test", "make install"})
	got := completionTexts(CommandCompletions(records, "make", ""))
	// "make" itself is what is already typed; far2l leaves it out.
	want := []string{"Make test", "make install"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("make -> %q, want %q", got, want)
	}
}

// Commands run in the current directory come first, then a separator, then
// the rest; each group keeps the newest first.
func TestCommandCompletionsCurrentDirectoryFirst(t *testing.T) {
	records := []HistoryRecord{
		{Name: "./a.sh", Dir: "/other"},
		{Name: "./b.sh", Dir: "/proj"},
		{Name: "./c.sh", Extra: "/proj"}, // a record from before Dir existed
		{Name: "./d.sh"},
	}
	got := completionTexts(CommandCompletions(records, "./", "/proj"))
	want := []string{"./b.sh", "./c.sh", "---", "./a.sh", "./d.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Nothing from the current directory: no leading separator.
	got = completionTexts(CommandCompletions(records, "./", "/none"))
	want = []string{"./a.sh", "./b.sh", "./c.sh", "./d.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Typos are forgiven at the start of an entry, never in the middle, and an
// exact prefix outranks a typo however recent the typo's entry is.
func TestCommandCompletionsForgiveTyposAtTheStart(t *testing.T) {
	records := RecordsFromNames([]string{"./biuld-old.sh", "./build.sh", "echo ./build"})
	got := completionTexts(CommandCompletions(records, "./build", ""))
	want := []string{"./build.sh", "./biuld-old.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("./build -> %q, want %q", got, want)
	}

	// Two swapped letters are one typo.
	got = completionTexts(CommandCompletions(records, "./bulid", ""))
	if len(got) == 0 || got[0] != "./build.sh" {
		t.Fatalf("./bulid -> %q, want ./build.sh first", got)
	}

	if got := CommandCompletions(records, "uild.sh", ""); len(got) != 0 {
		t.Fatalf("a match in the middle of an entry must not count: %q", completionTexts(got))
	}
	// Up to four runes allow no typo at all.
	short := RecordsFromNames([]string{"./run.sh", "ls -la"})
	for _, text := range []string{"lx", "./b", "./rx"} {
		if got := CommandCompletions(short, text, ""); len(got) != 0 {
			t.Fatalf("%q must match nothing, got %q", text, completionTexts(got))
		}
	}
}

func TestCommandCompletionsHighlightTheMatchedPrefix(t *testing.T) {
	items := CommandCompletions(RecordsFromNames([]string{"./build.sh"}), "./b", "")
	if len(items) != 1 || items[0].MatchStart != 0 || items[0].MatchEnd != 2 {
		t.Fatalf("items = %+v, want the first three runes highlighted", items)
	}
}

func TestCommandCompletionsSkipPluginEntriesAndDuplicates(t *testing.T) {
	records := []HistoryRecord{
		{Name: "./x"},
		{Name: "./x", Dir: "/proj"},
		{Name: "./plug", PluginType: "sftp", PluginRef: "ref"},
	}
	got := completionTexts(CommandCompletions(records, "./", "/proj"))
	if !reflect.DeepEqual(got, []string{"./x"}) {
		t.Fatalf("got %q, want a single ./x", got)
	}
}
