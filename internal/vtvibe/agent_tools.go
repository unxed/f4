package vtvibe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Built-in tools of the agent loop. They act with the user's own rights, so
// a host offers them only where the user asked the model to act on its own
// (the bot mode of docs/VTVIBE.md § 19a, which starts only after an explicit
// confirmation).

const (
	// DefaultShellTimeout bounds one command when the model names none.
	DefaultShellTimeout = 10 * time.Minute
	maxShellTimeout     = time.Hour
	maxReadFile         = 512 << 10
)

// resolvePath makes a model-given path absolute against dir.
func resolvePath(dir, p string) (string, error) {
	if p == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// ShellTool runs a command in the system shell, starting in dir, with env
// (NAME=value) added to its environment.
func ShellTool(dir string, env ...string) Tool {
	return Tool{
		Name:        "shell",
		Description: "Run a command in the system shell (sh -c on Unix, cmd /C on Windows) and return its combined output and exit code. The command starts in the working directory unless it changes directory itself.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":         map[string]any{"type": "string", "description": "The command line to run."},
				"timeout_seconds": map[string]any{"type": "integer", "description": "Kill the command after this many seconds (default 600, at most 3600)."},
			},
			"required": []string{"command"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Command        string `json:"command"`
				TimeoutSeconds int    `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			if args.Command == "" {
				return "", errors.New("command is empty")
			}
			timeout := DefaultShellTimeout
			if args.TimeoutSeconds > 0 {
				timeout = min(time.Duration(args.TimeoutSeconds)*time.Second, maxShellTimeout)
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(ctx, "cmd", "/C", args.Command) // #nosec G204 -- the agent's shell tool runs model commands by design, only in the bot mode the user confirmed
			} else {
				cmd = exec.CommandContext(ctx, "sh", "-c", args.Command) // #nosec G204 -- see above
			}
			cmd.Dir = dir
			if len(env) > 0 {
				cmd.Env = append(os.Environ(), env...)
			}
			cmd.WaitDelay = 5 * time.Second
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err := cmd.Run()
			code := 0
			var exitErr *exec.ExitError
			switch {
			case err == nil:
			case errors.As(err, &exitErr):
				code = exitErr.ExitCode()
			case ctx.Err() != nil:
				return out.String(), fmt.Errorf("command stopped after %s", timeout)
			default:
				return out.String(), err
			}
			return fmt.Sprintf("%s\n[exit code %d]", out.String(), code), nil
		},
	}
}

// ReadFileTool returns the text of a file; relative paths are taken from dir.
func ReadFileTool(dir string) Tool {
	return Tool{
		Name:        "read_file",
		Description: "Read a text file. Relative paths start in the working directory.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path"},
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			path, err := resolvePath(dir, args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if info.Size() > maxReadFile {
				return "", fmt.Errorf("%s has %d bytes, more than %d; read a part of it with the shell tool", path, info.Size(), maxReadFile)
			}
			data, err := os.ReadFile(path) // #nosec G304 -- the agent reads the files the user's bot works on
			if err != nil {
				return "", err
			}
			return string(data), nil
		},
	}
}

// WriteFileTool replaces (or creates) a file with the given text.
func WriteFileTool(dir string) Tool {
	return Tool{
		Name:        "write_file",
		Description: "Write a text file, replacing it if it exists and creating missing directories. Relative paths start in the working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			path, err := resolvePath(dir, args.Path)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o600); err != nil { // #nosec G306 -- see above
				return "", err
			}
			return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), path), nil
		},
	}
}

// WorkTools are the tools a bot and a worker get in dir: shell, read_file,
// write_file, edit_file, grep, find_files, view_image and fetch_url; env (NAME=value)
// is added to the environment of the shell's commands.
func WorkTools(dir string, env ...string) []Tool {
	return []Tool{ShellTool(dir, env...), ReadFileTool(dir), WriteFileTool(dir), EditFileTool(dir), GrepTool(dir), FindFilesTool(dir), ViewImageTool(dir), FetchURLTool()}
}
