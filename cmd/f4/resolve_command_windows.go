//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const appPathsKeyPath = `Software\Microsoft\Windows\CurrentVersion\App Paths`

// resolveWindowsCommand rewrites the first token of cmd to the full path
// registered in the "App Paths" registry key, but only when the program is
// NOT resolvable by the regular search (current dir / PATH / PATHEXT) that
// cmd.exe itself uses. Commands found normally are passed through untouched,
// so the registry is queried only as a fallback.
func resolveWindowsCommand(cmd string) string {
	start, end, name, ok := findCmdToken(cmd)
	if !ok || name == "" {
		return cmd
	}

	// Already a path (relative or absolute): cmd.exe handles it itself.
	if strings.ContainsAny(name, `\/`) {
		return cmd
	}

	// Resolve exactly as cmd.exe would (PATH + PATHEXT). If found, cmd will
	// run it without any help from us.
	if _, err := exec.LookPath(name); err == nil {
		return cmd
	}

	// Fallback: cmd.exe ignores App Paths, so try it ourselves. Wrap in
	// double quotes verbatim (no escaping) so the path reaches cmd unchanged.
	if full := appPathLookup(name); full != "" {
		return cmd[:start] + `"` + full + `"` + cmd[end:]
	}
	return cmd
}

// findCmdToken returns the byte range of the first command-line token and its
// unquoted value. A leading double or single quote is honoured.
func findCmdToken(cmd string) (start, end int, name string, ok bool) {
	start = -1
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == ' ' || cmd[i] == '\t' {
			continue
		}
		start = i
		break
	}
	if start < 0 {
		return -1, -1, "", false
	}

	if cmd[start] == '"' {
		quote := cmd[start]
		close := -1
		for i := start + 1; i < len(cmd); i++ {
			if cmd[i] == quote {
				close = i
				break
			}
		}
		if close < 0 {
			return -1, -1, "", false
		}
		return start, close + 1, cmd[start+1 : close], true
	}

	end = start
	for end < len(cmd) && cmd[end] != ' ' && cmd[end] != '\t' {
		end++
	}
	return start, end, cmd[start:end], true
}

// appPathLookup searches the App Paths registry (HKCU first, then HKLM) for
// name, trying each PATHEXT variant. It returns the (default) value of the
// matching subkey, i.e. the full path to the executable.
func appPathLookup(name string) string {
	roots := []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE}

	exts := appPathExts()
	variants := []string{name}
	for _, ext := range exts {
		if !strings.HasSuffix(strings.ToLower(name), strings.ToLower(ext)) {
			variants = append(variants, name+ext)
		}
	}

	for _, variant := range variants {
		subKey := filepath.Join(appPathsKeyPath, variant)
		for _, root := range roots {
			k, err := registry.OpenKey(root, subKey, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			value, _, err := k.GetStringValue("")
			k.Close()
			if err == nil && value != "" {
				return value
			}
		}
	}
	return ""
}

// isBatchCommand reports whether the first token of cmd is a .bat or .cmd file.
func isBatchCommand(cmd string) bool {
	_, _, token, ok := findCmdToken(cmd)
	if !ok {
		return false
	}
	token = strings.ToUpper(token)
	return strings.HasSuffix(token, ".BAT") || strings.HasSuffix(token, ".CMD")
}

func appPathExts() []string {
	var out []string
	for _, ext := range strings.Split(os.Getenv("PATHEXT"), ";") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out = append(out, ext)
	}
	if len(out) == 0 {
		out = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	return out
}
