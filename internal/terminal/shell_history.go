package terminal

import (
	"path/filepath"
	"strings"
)

// f4 types its own lines into the shell it hosts (the directory sync
// " cd '...' && true f4_sync", the command wrappers), each with a leading
// space. Whether that space keeps a line out of the shell's history is the
// shell's own setting: stock bash on Debian and Ubuntu has it
// (HISTCONTROL=ignoreboth), stock zsh and bash on macOS do not, and the history
// file then fills with one such line per directory change (f4#1805). So the
// shell f4 starts is told to ignore lines that begin with a space. The user's
// own startup files run after this and still win, which makes it a default,
// not a decision taken for them.

// interactiveShellName returns the base name of the shell being started, or ""
// when the command is not an interactive shell run (a -c command, a script).
func interactiveShellName(name string, args []string) string {
	base := strings.TrimSuffix(filepath.Base(name), ".exe")
	if base != "zsh" && base != "bash" {
		return ""
	}
	for _, a := range args {
		if a == "-c" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsRune(a, 'c')) {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return "" // a script to run
		}
	}
	return base
}

// historyQuietArgs adds the option that makes zsh drop lines starting with a
// space from its history. bash takes the same from the environment
// (historyQuietEnv).
func historyQuietArgs(name string, args []string) []string {
	if interactiveShellName(name, args) != "zsh" {
		return args
	}
	return append([]string{"-o", "HIST_IGNORE_SPACE"}, args...)
}

// historyQuietEnv makes bash ignore lines that start with a space unless
// HISTCONTROL already says so ("ignorespace" or "ignoreboth").
func historyQuietEnv(name string, args []string, env []string) []string {
	if interactiveShellName(name, args) != "bash" {
		return env
	}
	for i, kv := range env {
		if !strings.HasPrefix(kv, "HISTCONTROL=") {
			continue
		}
		value := strings.TrimPrefix(kv, "HISTCONTROL=")
		for _, part := range strings.Split(value, ":") {
			if part == "ignorespace" || part == "ignoreboth" {
				return env
			}
		}
		out := append([]string(nil), env...)
		if value == "" {
			out[i] = "HISTCONTROL=ignorespace"
		} else {
			out[i] = "HISTCONTROL=" + value + ":ignorespace"
		}
		return out
	}
	return append(append([]string(nil), env...), "HISTCONTROL=ignorespace")
}
