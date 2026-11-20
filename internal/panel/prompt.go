package panel

import (
	"strings"
	"time"

	"github.com/unxed/f4/internal/cmdline"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/vtui"
)

// promptFormatEnabled reports whether this prompt comes from the configured
// format string rather than from BuildPrompt's built-in layout.
//
// A panel showing a virtual filesystem is excluded. Its prompt names the
// provider instead of the user and the host, and there is no local home
// directory to abbreviate against, so $u, $n and $p would all describe a
// machine the panel is not on.
func promptFormatEnabled(vfsTitle string) bool {
	return config.App.UsePromptFormat && vfsTitle == ""
}

// promptFormatSpans renders the configured format for the current panel.
func promptFormatSpans(path, home, username, host string, maxPromptLen int) []cmdline.PromptSpan {
	format := config.App.PromptFormat
	if format == "" {
		format = cmdline.DefaultPromptFormat
	}
	gitBranch := ""
	if strings.Contains(strings.ToLower(format), "$z") {
		gitBranch = promptGitBranch(path)
	}
	spans := cmdline.ExpandPrompt(format, cmdline.PromptContext{
		Path:       path,
		Home:       home,
		User:       username,
		Host:       host,
		GitBranch:  gitBranch,
		Admin:      cmdline.PromptUserIsAdmin(),
		AdminLabel: cmdline.PromptAdminLabel(),
		Now:        time.Now(),
	})
	return cmdline.FitPrompt(spans, maxPromptLen)
}

// promptSpansToCharInfo paints the spans: the user and host names, and the path,
// have colours of their own in the theme (CommandLine.User, CommandLine.Path);
// everything else is on baseAttr (CommandLine.Prefix).
func promptSpansToCharInfo(spans []cmdline.PromptSpan, baseAttr uint64) []vtui.CharInfo {
	var out []vtui.CharInfo
	for _, s := range spans {
		attr := baseAttr
		switch s.Kind {
		case cmdline.PromptIdentity:
			attr = vtui.Palette[theme.ColCommandLineUser]
		case cmdline.PromptPath:
			attr = vtui.Palette[theme.ColCommandLinePath]
		}
		out = append(out, vtui.StringToCharInfo(s.Text, attr)...)
	}
	return out
}

// promptSpansInactive is the same prompt in the one colour the search-first
// mode uses while the command line does not have focus.
func promptSpansInactive(spans []cmdline.PromptSpan) []vtui.CharInfo {
	return vtui.StringToCharInfo(cmdline.PromptText(spans), vtui.Palette[theme.ColCommandLineInactivePrompt])
}
