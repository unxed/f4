package vtvibe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project instructions (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage H9,
// item 5). Claude Code, OpenCode and Cursor CLI read the instructions a
// project keeps for agents — AGENTS.md, CLAUDE.md — on their own. Workers
// and the bot now do too: the files in the working folder and in the
// folders above it, up to the root of its repository, go into their system
// prompt, the nearest last so it has the final word.

// projectInstructionFiles are the names looked for, in this order.
var projectInstructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// maxProjectInstructions bounds one file's share of the prompt, in runes.
const maxProjectInstructions = 64 << 10

// ProjectInstructions returns the instructions for agents that apply in
// dir, ready to append to a system prompt; "" when there are none.
func ProjectInstructions(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	// From dir up to the repository root (a folder with .git) or the
	// file system root; a folder outside any repository reads only itself.
	var chain []string
	for d := abs; ; {
		chain = append(chain, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			chain = chain[:1]
			break
		}
		d = parent
	}
	var parts []string
	for i := len(chain) - 1; i >= 0; i-- {
		for _, name := range projectInstructionFiles {
			path := filepath.Join(chain[i], name)
			data, err := os.ReadFile(path) // #nosec G304 G703 -- fixed names in the folders the user's agent works in
			if err != nil || strings.TrimSpace(string(data)) == "" {
				continue
			}
			text := strings.TrimSpace(cutRunes(string(data), maxProjectInstructions))
			parts = append(parts, fmt.Sprintf("=== %s ===\n%s", path, text))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n\nThe project's instructions for agents (follow them where they apply; the later file wins where they differ):\n\n" +
		strings.Join(parts, "\n\n")
}
