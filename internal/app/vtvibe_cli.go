package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/vtvibe"
)

// f4 --ai "question" (unxed/f4#1842, docs/VTVIBE.md § 19a.9, item 12): one
// question to the model of Settings → AI without starting the UI, the answer
// on stdout — for scripts, like claude -p or opencode run. Each run is a new
// dialog of its own; the panel's dialog is not touched.

// aiCLIArgs is what the command line says about a headless run.
type aiCLIArgs struct {
	question string
	bot      string
	yes      bool
	whole    bool
	model    string
	files    []string
}

var errAICLIUsage = errors.New(`usage: f4 --ai "question" [--ai-file PATH]... [--ai-model NAME]
       f4 --ai-bot FILE|URL --ai-yes [--ai-whole] [--ai-model NAME]`)

// parseAICLIArgs finds --ai or --ai-bot among args; found is false when
// neither is there.
func parseAICLIArgs(args []string) (parsed aiCLIArgs, found bool, err error) {
	asked := false
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		switch name {
		case "--ai-yes", "--ai-whole":
			found = true
			if hasValue {
				return parsed, true, errAICLIUsage
			}
			if name == "--ai-yes" {
				parsed.yes = true
			} else {
				parsed.whole = true
			}
			continue
		case "--ai", "--ai-bot", "--ai-file", "--ai-model":
		default:
			continue
		}
		found = true
		if !hasValue {
			if i+1 >= len(args) {
				return parsed, true, errAICLIUsage
			}
			i++
			value = args[i]
		}
		switch name {
		case "--ai":
			parsed.question, asked = value, true
		case "--ai-bot":
			parsed.bot = value
		case "--ai-file":
			parsed.files = append(parsed.files, value)
		case "--ai-model":
			parsed.model = value
		}
	}
	if !found {
		return parsed, false, nil
	}
	bot := strings.TrimSpace(parsed.bot) != ""
	if bot == asked || (asked && strings.TrimSpace(parsed.question) == "") || (bot && len(parsed.files) > 0) ||
		(!bot && (parsed.yes || parsed.whole)) {
		return parsed, true, errAICLIUsage
	}
	return parsed, true, nil
}

// runAICLI answers the question or runs the bot's round; handled is false
// when the command line has neither --ai nor --ai-bot. Piped stdin is attached to the dialog as stdin.txt, so
// `git diff | f4 --ai "review this"` works.
func runAICLI(args []string, stdin io.Reader, stdout, stderr io.Writer, readConfig func() (vtvibe.Config, string, vtvibe.Provider)) (code int, handled bool) {
	parsed, found, err := parseAICLIArgs(args)
	if !found {
		return 0, false
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2, true
	}
	cfg, _, _ := readConfig()
	if parsed.model != "" {
		cfg.Model = parsed.model
	}
	if parsed.bot != "" {
		return runAIBotCLI(parsed, cfg, stdout, stderr), true
	}
	session := vtvibe.NewSession()
	for _, path := range parsed.files {
		data, err := os.ReadFile(path) // #nosec G304 -- the user names the file to send on the command line
		if err == nil {
			err = vtvibeWriteContextFile(session, filepath.Base(path), data)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "f4 --ai: %s: %v\n", path, err)
			return 2, true
		}
	}
	if stdin != nil {
		data, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "f4 --ai: stdin: %v\n", err)
			return 2, true
		}
		if len(data) > 0 {
			if err := vtvibeWriteContextFile(session, "stdin.txt", data); err != nil {
				_, _ = fmt.Fprintf(stderr, "f4 --ai: stdin: %v\n", err)
				return 2, true
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	answer, err := session.AskOnce(ctx, cfg, parsed.question)
	if err != nil {
		if errors.Is(err, vtvibe.ErrNoKey) {
			_, _ = fmt.Fprintln(stderr, "f4 --ai: no API key: set the key variable of the provider or the key in Settings → AI")
		} else {
			_, _ = fmt.Fprintf(stderr, "f4 --ai: %v\n", err)
		}
		return 1, true
	}
	if !strings.HasSuffix(answer, "\n") {
		answer += "\n"
	}
	if _, err := io.WriteString(stdout, answer); err != nil {
		return 1, true
	}
	return 0, true
}

// pipedStdin is stdin when something is piped into f4, nil at a terminal.
func pipedStdin() io.Reader {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return os.Stdin
}

// runAIBotCLI runs one round of the bot's instruction in the current folder
// and prints its report (f4 --ai-bot). Nobody is there to approve the
// commands, so --ai-yes must say they may run; the tools' calls go to
// stderr as they happen.
func runAIBotCLI(parsed aiCLIArgs, cfg vtvibe.Config, stdout, stderr io.Writer) int {
	if !parsed.yes {
		_, _ = fmt.Fprintln(stderr, "f4 --ai-bot: the bot runs commands and changes files without asking; add --ai-yes to allow that")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "f4 --ai-bot: %v\n", err)
		return 2
	}
	cfg.ToolEnv = vtvibe.GitHubEnv(aiSettingsGitHubToken())
	bot := &vtvibe.Bot{}
	bot.SetStepped(!parsed.whole)
	bot.SetToolWrapper(func(tools []vtvibe.Tool) []vtvibe.Tool {
		for i, t := range tools {
			run, name := t.Run, t.Name
			tools[i].Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
				_, _ = fmt.Fprintf(stderr, "f4 --ai-bot: %s %s\n", name, aiCLIArgsLine(raw))
				return run(ctx, raw)
			}
		}
		return tools
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// The MCP servers of ai/mcp.json run for this round, in its folder, as
	// for a round started from the panel.
	mcp := &aiMCPRun{dir: dir}
	round, err := bot.RunOnce(ctx, parsed.bot, dir, cfg, mcp.tools())
	if problems := mcp.close(); problems != "" {
		_, _ = fmt.Fprintf(stderr, "f4 --ai-bot: %s\n", problems)
	}
	if err == nil {
		err = round.Err
	}
	if round.Report != "" {
		report := round.Report
		if !strings.HasSuffix(report, "\n") {
			report += "\n"
		}
		_, _ = io.WriteString(stdout, report)
	}
	if err != nil {
		if errors.Is(err, vtvibe.ErrNoKey) {
			_, _ = fmt.Fprintln(stderr, "f4 --ai-bot: no API key: set the key variable of the provider or the key in Settings → AI")
		} else {
			_, _ = fmt.Fprintf(stderr, "f4 --ai-bot: %v\n", err)
		}
		return 1
	}
	return 0
}

// aiCLIArgsLine shortens a tool call's arguments to one line for stderr.
func aiCLIArgsLine(raw json.RawMessage) string {
	line := strings.Join(strings.Fields(string(raw)), " ")
	if r := []rune(line); len(r) > 200 {
		line = string(r[:200]) + "…"
	}
	return line
}
