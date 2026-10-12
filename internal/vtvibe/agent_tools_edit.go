package vtvibe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Editing and searching tools for workers (unxed/f4#1842, docs/VTVIBE.md
// § 19a.9, stage H9, item 4). The other harnesses change a file by
// replacing a fragment and search the tree with grep and glob tools; a
// worker here could only rewrite whole files and search through the shell,
// which costs tokens and risks spoiling the parts it meant to keep.

const (
	maxSearchResults = 200
	maxFoundFiles    = 500
	maxSearchFile    = 2 << 20
)

// EditFileTool replaces a fragment of a file.
func EditFileTool(dir string) Tool {
	return Tool{
		Name: "edit_file",
		Description: "Replace a fragment of a text file: old_text must occur in the file exactly once (copy it with its indentation), " +
			"unless replace_all is set. Prefer this to write_file for changing part of a file. Relative paths start in the working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string"},
				"old_text":    map[string]any{"type": "string", "description": "The exact text to replace."},
				"new_text":    map[string]any{"type": "string", "description": "The text to put in its place."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence instead of exactly one."},
			},
			"required": []string{"path", "old_text", "new_text"},
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				OldText    string `json:"old_text"`
				NewText    string `json:"new_text"`
				ReplaceAll bool   `json:"replace_all"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			if args.OldText == "" {
				return "", errors.New("old_text is empty; use write_file to create a file")
			}
			path, err := resolvePath(dir, args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(path) // #nosec G304 -- the agent edits the files the user's worker works on
			if err != nil {
				return "", err
			}
			text := string(data)
			n := strings.Count(text, args.OldText)
			switch {
			case n == 0:
				return "", fmt.Errorf("old_text does not occur in %s; read the file and copy the fragment exactly", path)
			case n > 1 && !args.ReplaceAll:
				return "", fmt.Errorf("old_text occurs %d times in %s; give more context to make it unique, or set replace_all", n, path)
			}
			replaced := 1
			if args.ReplaceAll {
				text, replaced = strings.ReplaceAll(text, args.OldText, args.NewText), n
			} else {
				text = strings.Replace(text, args.OldText, args.NewText, 1)
			}
			if err := os.WriteFile(path, []byte(text), info.Mode().Perm()); err != nil { // #nosec G306 G703 -- the file keeps its own mode
				return "", err
			}
			return fmt.Sprintf("replaced %d occurrence(s) in %s", replaced, path), nil
		},
	}
}

// GrepTool searches file contents for a regular expression.
func GrepTool(dir string) Tool {
	return Tool{
		Name: "grep",
		Description: fmt.Sprintf("Search the text files under a folder for a regular expression (Go syntax) and list the matching lines as path:line: text, "+
			"at most %d. Hidden folders and binary or very large files are skipped. Optionally only files whose name matches a glob such as *.go.", maxSearchResults),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"path":    map[string]any{"type": "string", "description": "Folder or file to search; the working directory by default."},
				"glob":    map[string]any{"type": "string", "description": "Only files whose name matches, e.g. *.go."},
			},
			"required": []string{"pattern"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Pattern string `json:"pattern"`
				Path    string `json:"path"`
				Glob    string `json:"glob"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			re, err := regexp.Compile(args.Pattern)
			if err != nil {
				return "", err
			}
			root, err := searchRoot(dir, args.Path)
			if err != nil {
				return "", err
			}
			var out []string
			more := false
			err = walkFiles(ctx, root, args.Glob, func(path string, rel string) bool {
				data, err := os.ReadFile(path) // #nosec G304 -- see EditFileTool
				if err != nil || bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
					return true
				}
				sc := bufio.NewScanner(bytes.NewReader(data))
				sc.Buffer(make([]byte, 64<<10), maxSearchFile)
				for line := 1; sc.Scan(); line++ {
					if re.MatchString(sc.Text()) {
						if len(out) == maxSearchResults {
							more = true
							return false
						}
						out = append(out, fmt.Sprintf("%s:%d: %s", rel, line, cutRunes(sc.Text(), 300)))
					}
				}
				return true
			})
			if err != nil {
				return "", err
			}
			return searchReport(out, more, "no matches"), nil
		},
	}
}

// FindFilesTool lists files whose name matches a glob.
func FindFilesTool(dir string) Tool {
	return Tool{
		Name:        "find_files",
		Description: fmt.Sprintf("List the files under a folder whose name matches a glob such as *_test.go, at most %d. Hidden folders are skipped.", maxFoundFiles),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"glob": map[string]any{"type": "string"},
				"path": map[string]any{"type": "string", "description": "Folder to search; the working directory by default."},
			},
			"required": []string{"glob"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Glob string `json:"glob"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			if _, err := filepath.Match(args.Glob, ""); err != nil || args.Glob == "" {
				return "", fmt.Errorf("bad glob %q", args.Glob)
			}
			root, err := searchRoot(dir, args.Path)
			if err != nil {
				return "", err
			}
			var out []string
			more := false
			err = walkFiles(ctx, root, args.Glob, func(_ string, rel string) bool {
				if len(out) == maxFoundFiles {
					more = true
					return false
				}
				out = append(out, rel)
				return true
			})
			if err != nil {
				return "", err
			}
			return searchReport(out, more, "no files found"), nil
		},
	}
}

func searchRoot(dir, p string) (string, error) {
	if p == "" {
		return dir, nil
	}
	return resolvePath(dir, p)
}

// walkFiles calls visit for every regular file under root (root itself when
// it is a file) whose name matches glob, skipping hidden folders and files
// too large to search; visit returns false to stop.
func walkFiles(ctx context.Context, root, glob string, visit func(path, rel string) bool) error {
	stop := errors.New("stop")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry is skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if glob != "" {
			if ok, _ := filepath.Match(glob, d.Name()); !ok {
				return nil
			}
		}
		if info, err := d.Info(); err != nil || info.Size() > maxSearchFile {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			rel = path
		}
		if !visit(path, filepath.ToSlash(rel)) {
			return stop
		}
		return nil
	})
	if errors.Is(err, stop) {
		return nil
	}
	return err
}

func searchReport(lines []string, more bool, none string) string {
	if len(lines) == 0 {
		return none
	}
	text := strings.Join(lines, "\n")
	if more {
		text += "\n… more results were cut; narrow the search"
	}
	return text
}
