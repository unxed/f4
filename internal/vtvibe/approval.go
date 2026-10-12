package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Permission modes (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage H9, item
// 8). Claude Code, OpenCode and Cursor CLI can ask before each command and
// keep a list of what is allowed without asking; vtvibe asked once for a
// whole task or bot. In the asking mode every tool that changes something —
// the shell, file writes and edits, MCP tools — first goes through an
// Approver, unless a rule of the allow list covers the call. Reading and
// searching never ask.

// ErrDenied is what a tool returns to the model when the user said no.
var ErrDenied = errors.New("the user did not allow this; do not try it again another way — say what you needed it for")

// Approval is the user's answer.
type Approval int

const (
	// Deny refuses this call.
	Deny Approval = iota
	// AllowOnce allows this call only.
	AllowOnce
	// AllowAlways allows it and keeps a rule for calls like it.
	AllowAlways
)

// Approver asks the user about one call; it blocks until they answer or ctx
// ends. summary is what the call does, in a line or a few.
type Approver func(ctx context.Context, tool, summary string) (Approval, error)

// AllowList is the set of rules that let calls through without asking. A
// rule is a tool name or glob (edit_file, mcp__fs__*), or for the shell a
// command prefix such as "go test" or "git status".
type AllowList struct {
	Rules func() []string // asked each time, so an edited list counts at once
	Add   func(rule string)
}

// readOnlyTools never ask.
var readOnlyTools = map[string]bool{"read_file": true, "grep": true, "find_files": true, "view_image": true}

// shellMeta marks a command that does more than run one program: an allow
// rule for "go test" must not let "go test; rm -rf ~" through.
const shellMeta = ";&|`$<>\n\r(){}"

// Allowed reports whether a rule covers the call.
func (a AllowList) Allowed(tool, command string) bool {
	if a.Rules == nil {
		return false
	}
	for _, rule := range a.Rules() {
		rule = strings.TrimSpace(rule)
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue
		}
		if tool == "shell" {
			if strings.ContainsAny(command, shellMeta) {
				return false
			}
			cmd := strings.Join(strings.Fields(command), " ")
			if cmd == rule || strings.HasPrefix(cmd, rule+" ") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(rule, tool); ok {
			return true
		}
	}
	return false
}

// ruleFor is the rule "always allow" keeps: for the shell the program and
// its first word when that is a subcommand (git status, go test), else the
// program alone; for other tools the tool's name.
func ruleFor(tool, command string) string {
	if tool != "shell" {
		return tool
	}
	f := strings.Fields(command)
	switch {
	case len(f) == 0:
		return ""
	case len(f) > 1 && !strings.HasPrefix(f[1], "-") && !strings.ContainsAny(f[1], `/\.:=`):
		return f[0] + " " + f[1]
	}
	return f[0]
}

// callSummary is what the user is asked about.
func callSummary(tool string, raw json.RawMessage) (summary, command string) {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	str := func(k string) string { s, _ := args[k].(string); return s }
	switch tool {
	case "shell":
		command = str("command")
		return command, command
	case "write_file":
		return fmt.Sprintf("write %s (%d bytes)", str("path"), len(str("content"))), ""
	case "edit_file":
		return fmt.Sprintf("edit %s: replace %q", str("path"), cutRunes(str("old_text"), 120)), ""
	}
	return fmt.Sprintf("%s %s", tool, cutRunes(string(raw), 300)), ""
}

// WithApproval makes every tool among tools that changes something ask
// approve first, unless allow covers the call.
func WithApproval(tools []Tool, approve Approver, allow AllowList) []Tool {
	out := make([]Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		if readOnlyTools[t.Name] || approve == nil {
			continue
		}
		run, name := t.Run, t.Name
		out[i].Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
			summary, command := callSummary(name, raw)
			if !allow.Allowed(name, command) {
				answer, err := approve(ctx, name, summary)
				if err != nil {
					return "", err
				}
				switch answer {
				case Deny:
					return "", ErrDenied
				case AllowAlways:
					if rule := ruleFor(name, command); rule != "" && allow.Add != nil {
						allow.Add(rule)
					}
				}
			}
			return run(ctx, raw)
		}
	}
	return out
}
