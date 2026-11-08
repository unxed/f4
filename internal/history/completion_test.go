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
	got := completionTexts(CommandCompletions(records, "./", "", 0))
	want := []string{"./build.sh", "./run.sh --fast"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("./ -> %q, want %q", got, want)
	}

	got = completionTexts(CommandCompletions(records, "./b", "", 0))
	if !reflect.DeepEqual(got, []string{"./build.sh"}) {
		t.Fatalf("./b -> %q, want only ./build.sh", got)
	}
}

func TestCommandCompletionsCaseAndExactEntry(t *testing.T) {
	records := RecordsFromNames([]string{"make", "Make test", "make install"})
	got := completionTexts(CommandCompletions(records, "make", "", 0))
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
	got := completionTexts(CommandCompletions(records, "./", "/proj", 0))
	want := []string{"./b.sh", "./c.sh", "---", "./a.sh", "./d.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Nothing from the current directory: no leading separator.
	got = completionTexts(CommandCompletions(records, "./", "/none", 0))
	want = []string{"./a.sh", "./b.sh", "./c.sh", "./d.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Typos are forgiven at the start of an entry, never in the middle, and an
// exact prefix outranks a typo however recent the typo's entry is.
func TestCommandCompletionsForgiveTyposAtTheStart(t *testing.T) {
	records := RecordsFromNames([]string{"./biuld-old.sh", "./build.sh", "echo ./build"})
	got := completionTexts(CommandCompletions(records, "./build", "", 0))
	want := []string{"./build.sh", "./biuld-old.sh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("./build -> %q, want %q", got, want)
	}

	// Two swapped letters are one typo.
	got = completionTexts(CommandCompletions(records, "./bulid", "", 0))
	if len(got) == 0 || got[0] != "./build.sh" {
		t.Fatalf("./bulid -> %q, want ./build.sh first", got)
	}

	if got := CommandCompletions(records, "uild.sh", "", 0); len(got) != 0 {
		t.Fatalf("a match in the middle of an entry must not count: %q", completionTexts(got))
	}
	// Up to four runes allow no typo at all.
	short := RecordsFromNames([]string{"./run.sh", "ls -la"})
	for _, text := range []string{"lx", "./b", "./rx"} {
		if got := CommandCompletions(short, text, "", 0); len(got) != 0 {
			t.Fatalf("%q must match nothing, got %q", text, completionTexts(got))
		}
	}
}

func TestCommandCompletionsHighlightTheMatchedPrefix(t *testing.T) {
	items := CommandCompletions(RecordsFromNames([]string{"./build.sh"}), "./b", "", 0)
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
	got := completionTexts(CommandCompletions(records, "./", "/proj", 0))
	if !reflect.DeepEqual(got, []string{"./x"}) {
		t.Fatalf("got %q, want a single ./x", got)
	}
}

// Deleting runes off the end of the typed text counted against the typo
// budget, so a shorter entry was offered as a "completion" of a longer line.
func TestCommandCompletionsNeverOfferAShorterEntry(t *testing.T) {
	records := RecordsFromNames([]string{"./build.sh", "./build.sh -v --all"})
	got := completionTexts(CommandCompletions(records, "./build.sh -v", "", 0))
	if !reflect.DeepEqual(got, []string{"./build.sh -v --all"}) {
		t.Fatalf("./build.sh -v -> %q, want only the longer entry", got)
	}
}

// The typed text itself in another case is nothing to complete to.
func TestCommandCompletionsSkipTheTypedTextInAnotherCase(t *testing.T) {
	records := RecordsFromNames([]string{"ls", "LS", "ls -la"})
	got := completionTexts(CommandCompletions(records, "LS", "", 0))
	if !reflect.DeepEqual(got, []string{"ls -la"}) {
		t.Fatalf("LS -> %q, want only ls -la", got)
	}
}

// The menu caps each separator-delimited group on its own, so the two
// history groups must share one cap or history fills twice its share.
func TestCommandCompletionsShareOneLimitAcrossGroups(t *testing.T) {
	records := []HistoryRecord{
		{Name: "./h1", Dir: "/proj"}, {Name: "./h2", Dir: "/proj"},
		{Name: "./e1"}, {Name: "./e2"}, {Name: "./e3"},
	}
	got := completionTexts(CommandCompletions(records, "./", "/proj", 3))
	if want := []string{"./h1", "./h2", "---", "./e1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("limit 3 -> %q, want %q", got, want)
	}
	got = completionTexts(CommandCompletions(records, "./", "/proj", 2))
	if want := []string{"./h1", "./h2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("limit 2 -> %q, want %q", got, want)
	}
}

// Every keystroke used to fold and allocate for each record. Entries that do
// not match must now cost no allocation at all, however many there are.
func TestCommandCompletionsAllocateOncePerKeystroke(t *testing.T) {
	names := make([]string, 2000)
	for i := range names {
		names[i] = "git commit --amend --no-edit"
	}
	records := RecordsFromNames(names)
	allocs := testing.AllocsPerRun(20, func() {
		_ = CommandCompletions(records, "./build", "", 0)
	})
	if allocs > 8 {
		t.Fatalf("CommandCompletions allocates %.0f times over 2000 records, want a constant few", allocs)
	}
}
