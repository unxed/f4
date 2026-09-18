package terminal

import "strings"

// BuildShellCmdLine assembles the single command line a Windows CreateProcessW
// call takes, for the shell invocation os/exec would otherwise build from
// (shell, flag, command).
//
// It exists because RunOnHostConsole (console_spawn_windows.go) starts the
// child itself instead of going through os/exec, and CreateProcessW takes one
// string where os/exec takes an argv. Keeping the quoting identical to what
// os/exec produces is the whole point: the child is the same shell with the
// same arguments as before, only wired to the console differently.
//
// The escaping below is the same algorithm as syscall.EscapeArg, which is what
// os/exec's makeCmdLine uses on Windows. It is duplicated here rather than
// called because syscall.EscapeArg only exists under the windows build tag,
// and this file is built everywhere so the rule can be unit-tested on the
// machine that runs the tests.
func BuildShellCmdLine(shell, flag, command string) string {
	parts := []string{escapeArg(shell)}
	if flag != "" {
		parts = append(parts, escapeArg(flag))
	}
	parts = append(parts, escapeArg(command))
	return strings.Join(parts, " ")
}

// escapeArg quotes s for the Windows command line parser, matching
// syscall.EscapeArg.
func escapeArg(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '"':
			needsQuote = true
		}
		if needsQuote {
			break
		}
	}
	if !needsQuote {
		return s
	}

	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			// A quote ends a run of backslashes that must each be doubled,
			// and the quote itself is escaped by one more backslash.
			for ; slashes > 0; slashes-- {
				b.WriteString(`\\`)
			}
			b.WriteString(`\"`)
			continue
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	// Backslashes immediately before the closing quote are doubled too, or
	// they would escape it.
	for ; slashes > 0; slashes-- {
		b.WriteString(`\`)
	}
	b.WriteByte('"')
	return b.String()
}
