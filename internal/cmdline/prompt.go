package cmdline

// The command line prompt, and the format string that describes it.
//
// far2l builds its prompt from a cmd.exe-shaped format string (Options ->
// Command line settings -> "Set command line prompt format"), so people
// arriving from there already have one written down. The codes below are the
// ones far2l documents in its help under CommandPrompt, and the expansion
// follows far2l/src/cmdline.cpp GetPrompt: codes are case-insensitive, and a
// code nobody knows expands to nothing rather than to itself.
//
// Four of far2l's codes are not here. $e, $v, $_ and $m far2l does not
// implement either. $+ is accepted and reads the folder stack depth the
// caller passes, which is zero until f4 grows a pushd stack.
//
// Unlike far2l this does not expand environment variables in the format
// string before the codes. There $HOSTNAME works but $s is whatever the
// environment says $s is, and a format string that behaves differently
// depending on what happens to be exported is not worth the one variable it
// buys: $u and $n already name the user and the host.

import (
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/unxed/vtui"
)

// DefaultPromptFormat reproduces the prompt f4 draws without a format string,
// except that root gets '#' the way every shell shows it.
const DefaultPromptFormat = "$u@$n:$p$# "

// PromptSpanKind says what a run of prompt text was made of, so the caller
// can colour it and, when the prompt does not fit, know what to shorten.
type PromptSpanKind int

const (
	// PromptLiteral is text from the format string and the punctuation codes.
	PromptLiteral PromptSpanKind = iota
	// PromptIdentity is $u and $n -- the part a shell prompt colours.
	PromptIdentity
	// PromptPath is $p and $r, the only part worth shortening.
	PromptPath
)

// PromptSpan is a run of prompt text that shares one kind.
type PromptSpan struct {
	Text string
	Kind PromptSpanKind
}

// PromptContext is everything the codes can ask about. It is passed in rather
// than looked up so that expansion stays a pure function of its input.
type PromptContext struct {
	Path       string // the directory the panel is showing
	Home       string // "" leaves $p unabbreviated
	User       string
	Host       string
	GitBranch  string // current branch, empty outside a Git worktree
	Admin      bool
	AdminLabel string // what $@xx puts between its two brackets
	StackDepth int    // folder stack depth, for $+
	Now        time.Time
}

// punctuation is the part of the table that is a plain substitution. These
// exist because a format string is also a shell command line in cmd.exe,
// where '&', '|' and '>' cannot be written directly.
var punctuation = map[rune]rune{
	'a': '&',
	'b': '|',
	'c': '(',
	'f': ')',
	'g': '>',
	'l': '<',
	'q': '=',
	's': ' ',
	'$': '$',
}

// ExpandPrompt renders a format string into coloured spans.
func ExpandPrompt(format string, ctx PromptContext) []PromptSpan {
	var out []PromptSpan
	add := func(text string, kind PromptSpanKind) {
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Kind == kind {
			out[n-1].Text += text
			return
		}
		out = append(out, PromptSpan{Text: text, Kind: kind})
	}

	runes := []rune(format)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			add(string(runes[i]), PromptLiteral)
			continue
		}
		i++
		if i >= len(runes) {
			// A format string ending in a lone '$' names no code.
			break
		}
		code := runes[i]
		if code != '$' {
			code = unicode.ToLower(code)
		}
		if chr, ok := punctuation[code]; ok {
			add(string(chr), PromptLiteral)
			continue
		}
		switch code {
		case 'p':
			add(abbreviateHome(ctx.Path, ctx.Home), PromptPath)
		case 'r':
			add(ctx.Path, PromptPath)
		case 'u':
			add(ctx.User, PromptIdentity)
		case 'n':
			add(ctx.Host, PromptIdentity)
		case 'z':
			if ctx.GitBranch != "" {
				add("{"+ctx.GitBranch+"}", PromptLiteral)
			}
		case '#':
			if ctx.Admin {
				add("#", PromptLiteral)
			} else {
				add("$", PromptLiteral)
			}
		case 'd':
			add(ctx.Now.Format("01/02/06"), PromptLiteral)
		case 't':
			add(ctx.Now.Format("15:04:05"), PromptLiteral)
		case 'h':
			// $H is a backspace: it erases what the prompt has so far,
			// which is how "$t$h$h$h" gets HH:MM out of HH:MM:SS.
			out = dropLastRune(out)
		case '+':
			add(strings.Repeat("+", ctx.StackDepth), PromptLiteral)
		case '@':
			// $@xx: the two characters after it bracket the label, and
			// the whole thing disappears when this is not a root session.
			var lb, rb string
			if i+1 < len(runes) {
				i++
				lb = string(runes[i])
			}
			if i+1 < len(runes) {
				i++
				rb = string(runes[i])
			}
			if ctx.Admin {
				add(lb+ctx.AdminLabel+rb, PromptLiteral)
			}
		}
	}
	return out
}

// abbreviateHome writes the home directory as '~', the way $p does and $r
// does not.
func abbreviateHome(path, home string) string {
	if home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}

func dropLastRune(spans []PromptSpan) []PromptSpan {
	for i := len(spans) - 1; i >= 0; i-- {
		if spans[i].Text == "" {
			continue
		}
		runes := []rune(spans[i].Text)
		spans[i].Text = string(runes[:len(runes)-1])
		if spans[i].Text == "" {
			return spans[:i]
		}
		return spans[:i+1]
	}
	return spans
}

// PromptText is the plain string the spans spell out.
func PromptText(spans []PromptSpan) string {
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// PromptWidth is how many cells the spans occupy.
func PromptWidth(spans []PromptSpan) int {
	total := 0
	for _, s := range spans {
		total += runewidth.StringWidth(s.Text)
	}
	return total
}

// MinPromptPathWidth is how short the path may get before the prompt starts
// losing its other parts instead. It matches what the built-in prompt allows.
const MinPromptPathWidth = 10

// FitPrompt shortens a prompt to maxWidth. The path goes first, because a
// path is what a prompt has too much of; a prompt that is still too long
// after the path has shrunk as far as it may loses its head, so that the part
// the cursor sits next to -- the '$', the '>' -- always stays on screen.
func FitPrompt(spans []PromptSpan, maxWidth int) []PromptSpan {
	if maxWidth <= 0 {
		return nil
	}
	excess := PromptWidth(spans) - maxWidth
	if excess <= 0 {
		return spans
	}

	fitted := make([]PromptSpan, len(spans))
	copy(fitted, spans)
	for i := len(fitted) - 1; i >= 0 && excess > 0; i-- {
		if fitted[i].Kind != PromptPath {
			continue
		}
		width := runewidth.StringWidth(fitted[i].Text)
		target := width - excess
		if target < MinPromptPathWidth {
			target = MinPromptPathWidth
		}
		if target >= width {
			continue
		}
		fitted[i].Text = vtui.TruncateMiddle(fitted[i].Text, target)
		excess -= width - runewidth.StringWidth(fitted[i].Text)
	}
	if excess <= 0 {
		return fitted
	}
	return trimPromptHead(fitted, excess)
}

func trimPromptHead(spans []PromptSpan, excess int) []PromptSpan {
	for i := 0; i < len(spans) && excess > 0; i++ {
		runes := []rune(spans[i].Text)
		for len(runes) > 0 && excess > 0 {
			excess -= runewidth.RuneWidth(runes[0])
			runes = runes[1:]
		}
		spans[i].Text = string(runes)
	}
	out := spans[:0]
	for _, s := range spans {
		if s.Text != "" {
			out = append(out, s)
		}
	}
	return out
}
