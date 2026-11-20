package cmdline

import (
	"testing"
	"time"
)

func promptCtx() PromptContext {
	return PromptContext{
		Path:       "/home/nz/src/f4",
		Home:       "/home/nz",
		User:       "nz",
		Host:       "nz-en",
		GitBranch:  "feature/prompt",
		AdminLabel: "Root",
		Now:        time.Date(2026, 9, 12, 13, 38, 7, 0, time.UTC),
	}
}

func TestExpandPromptDefaultFormat(t *testing.T) {
	got := PromptText(ExpandPrompt(DefaultPromptFormat, promptCtx()))
	if want := "nz@nz-en:~/src/f4$ "; got != want {
		t.Errorf("ExpandPrompt(default) = %q, want %q", got, want)
	}
}

func TestExpandPromptCodes(t *testing.T) {
	ctx := promptCtx()
	cases := []struct {
		format string
		want   string
	}{
		{"$p$# ", "~/src/f4$ "},
		{"$r", "/home/nz/src/f4"},
		{"$u", "nz"},
		{"$n", "nz-en"},
		{"$z", "feature/prompt"},
		{"$Z", "feature/prompt"},
		{"$t", "13:38:07"},
		{"$d", "09/12/26"},
		{"$a$b$c$f$g$l$q$$", "&|()><=$"},
		{"a$sb", "a b"},
		// Case does not matter: far2l upper-cases the code first.
		{"$P$G", "~/src/f4>"},
		// The documented way to cut the seconds off $t.
		{"[$t$h$h$h]", "[13:38]"},
		// $@ eats its two brackets whether or not it prints anything.
		{"$@[]$p", "~/src/f4"},
		// A code nobody knows expands to nothing, and so does a trailing $.
		{"a$ky", "ay"},
		{"ab$", "ab"},
	}

	for _, tc := range cases {
		if got := PromptText(ExpandPrompt(tc.format, ctx)); got != tc.want {
			t.Errorf("ExpandPrompt(%q) = %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestExpandPromptRootMarkers(t *testing.T) {
	ctx := promptCtx()
	ctx.Admin = true

	if got := PromptText(ExpandPrompt("$#", ctx)); got != "#" {
		t.Errorf("$# for root = %q, want %q", got, "#")
	}
	if got := PromptText(ExpandPrompt("$@[]$s$p", ctx)); got != "[Root] ~/src/f4" {
		t.Errorf("$@[] for root = %q", got)
	}
	ctx.Admin = false
	if got := PromptText(ExpandPrompt("$@[]$p", ctx)); got != "~/src/f4" {
		t.Errorf("$@[] for a normal user must print nothing, got %q", got)
	}
}

func TestExpandPromptFolderStack(t *testing.T) {
	ctx := promptCtx()
	ctx.StackDepth = 3
	if got := PromptText(ExpandPrompt("$+$p", ctx)); got != "+++~/src/f4" {
		t.Errorf("$+ = %q", got)
	}
}

func TestExpandPromptPathWithoutHome(t *testing.T) {
	ctx := promptCtx()
	ctx.Home = ""
	if got := PromptText(ExpandPrompt("$p", ctx)); got != "/home/nz/src/f4" {
		t.Errorf("without a home directory $p must not abbreviate: %q", got)
	}
}

// The user and the host are what the caller colours, and the path is what it
// shortens, so each has to arrive in a span of its own.
func TestExpandPromptMarksSpans(t *testing.T) {
	spans := ExpandPrompt("$u@$n:$p$# ", promptCtx())

	kinds := map[PromptSpanKind]string{}
	for _, s := range spans {
		kinds[s.Kind] += s.Text
	}
	if kinds[PromptIdentity] != "nznz-en" {
		t.Errorf("identity spans = %q", kinds[PromptIdentity])
	}
	if kinds[PromptPath] != "~/src/f4" {
		t.Errorf("path span = %q", kinds[PromptPath])
	}
	if kinds[PromptLiteral] != "@:$ " {
		t.Errorf("literal spans = %q", kinds[PromptLiteral])
	}
}

func TestFitPromptShortensThePathFirst(t *testing.T) {
	ctx := promptCtx()
	ctx.Path = "/home/nz/very/long/path/that/will/not/fit/anywhere/at/all"
	spans := FitPrompt(ExpandPrompt(DefaultPromptFormat, ctx), 30)

	if got := PromptWidth(spans); got > 30 {
		t.Errorf("prompt width = %d, want at most 30: %q", got, PromptText(spans))
	}
	text := PromptText(spans)
	if len(text) < 2 || text[len(text)-2:] != "$ " {
		t.Errorf("the tail of the prompt must survive: %q", text)
	}
	if got := PromptText(spans)[:len("nz@nz-en:")]; got != "nz@nz-en:" {
		t.Errorf("only the path should have been shortened: %q", PromptText(spans))
	}
}

// Everything else goes only once the path is down to MinPromptPathWidth, and
// it goes from the left: the end of the prompt is where the cursor sits.
func TestFitPromptTrimsTheHeadWhenThePathIsNotEnough(t *testing.T) {
	ctx := promptCtx()
	ctx.User = "a-very-long-user-name"
	ctx.Host = "a-very-long-host-name"
	spans := FitPrompt(ExpandPrompt(DefaultPromptFormat, ctx), 20)

	if got := PromptWidth(spans); got > 20 {
		t.Errorf("prompt width = %d, want at most 20: %q", got, PromptText(spans))
	}
	text := PromptText(spans)
	if len(text) < 2 || text[len(text)-2:] != "$ " {
		t.Errorf("the tail of the prompt must survive: %q", text)
	}
}

func TestFitPromptLeavesAShortPromptAlone(t *testing.T) {
	spans := ExpandPrompt(DefaultPromptFormat, promptCtx())
	if got := PromptText(FitPrompt(spans, 100)); got != PromptText(spans) {
		t.Errorf("a prompt that fits must not be touched: %q", got)
	}
}
