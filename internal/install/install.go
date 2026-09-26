// Package install implements `f4 --install`: copying the running executable
// into a directory on the user's PATH (or offering to put it there), without
// needing an external package manager. See f4#252.
//
// Scope: Unix shells only (bash, zsh, fish). Windows has no single
// well-known profile file this maps onto -- cmd.exe has none at all, and
// PowerShell's $PROFILE is a different enough mechanism that it is left for
// a follow-up rather than forced into this package. cli.go's RunCLI reports
// that plainly instead of half-installing anything there.
package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PreferredDir is the install location --install tries first: XDG's
// user-specific binary directory, which most modern distributions already
// add to PATH by default (e.g. via /etc/profile.d or ~/.profile snippets
// shipped by the distro), making it the more likely of the two to need no
// profile edit at all.
func PreferredDir(home string) string {
	return filepath.Join(home, ".local", "bin")
}

// FallbackDir is the older, still-common convention --install falls back to
// when ~/.local/bin neither exists nor can be created (e.g. a read-only
// ~/.local on some restricted setups).
func FallbackDir(home string) string {
	return filepath.Join(home, "bin")
}

// EnsureDir reports whether dir already exists as a directory, creating it
// (and any missing parents) otherwise. It is the seam ChooseInstallDir calls
// through, so a test can substitute a stub that fails without touching a
// real filesystem.
func EnsureDir(dir string) (existed bool, err error) {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("%s exists and is not a directory", dir)
		}
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return false, nil
}

// ChooseInstallDir picks PreferredDir(home), falling back to FallbackDir(home)
// only when the preferred directory neither exists nor can be created.
// ensureDir is EnsureDir in production; tests pass a stub so this never needs
// a real home directory.
func ChooseInstallDir(home string, ensureDir func(dir string) (existed bool, err error)) (dir string, usedFallback bool, err error) {
	preferred := PreferredDir(home)
	if _, err := ensureDir(preferred); err == nil {
		return preferred, false, nil
	}
	fallback := FallbackDir(home)
	if _, err := ensureDir(fallback); err != nil {
		return "", false, fmt.Errorf("could not create %s or %s: %w", preferred, fallback, err)
	}
	return fallback, true, nil
}

// HasDirOnPath reports whether dir already appears among the
// os.PathListSeparator-joined entries of a $PATH-style string, comparing
// cleaned paths so a trailing slash or a redundant "." segment does not read
// as a false negative.
func HasDirOnPath(pathEnv, dir string) bool {
	target := filepath.Clean(dir)
	for _, entry := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if filepath.Clean(entry) == target {
			return true
		}
	}
	return false
}

// ShellProfile names a shell --install recognized and the profile file a
// PATH line for it belongs in.
type ShellProfile struct {
	Shell string // "bash", "zsh", "fish"
	Path  string
}

// DetectShellProfile maps a $SHELL value to its profile file. ok is false for
// an empty or unrecognized shell, in which case RunCLI prints the PATH line
// instead of offering to add it anywhere.
func DetectShellProfile(home, shellEnv string) (profile ShellProfile, ok bool) {
	switch filepath.Base(strings.TrimSpace(shellEnv)) {
	case "bash":
		return ShellProfile{Shell: "bash", Path: filepath.Join(home, ".bashrc")}, true
	case "zsh":
		return ShellProfile{Shell: "zsh", Path: filepath.Join(home, ".zshrc")}, true
	case "fish":
		return ShellProfile{Shell: "fish", Path: filepath.Join(home, ".config", "fish", "config.fish")}, true
	default:
		return ShellProfile{}, false
	}
}

// PathLine is the exact text RunCLI offers to add to a profile (or prints,
// when the shell is not recognized -- callers pass a zero ShellProfile for
// that case, which reads as the portable/POSIX "export" form). dir is shown
// as "$HOME/..." when it sits under home, so the line still works after the
// user renames or migrates their home directory.
func PathLine(profile ShellProfile, home, dir string) string {
	display := dir
	if home != "" {
		prefix := strings.TrimRight(home, string(filepath.Separator)) + string(filepath.Separator)
		if strings.HasPrefix(dir, prefix) {
			display = "$HOME" + string(filepath.Separator) + dir[len(prefix):]
		}
	}
	if profile.Shell == "fish" {
		return fmt.Sprintf("set -gx PATH %s $PATH", display)
	}
	return fmt.Sprintf("export PATH=\"%s:$PATH\"", display)
}

// CopyExecutable copies src to dst, making sure dst ends up executable even
// if src's permission bits, unusually, do not say so. It writes to a
// temporary file next to dst and renames it into place, so re-installing
// over an already-installed (and possibly currently running) copy never
// leaves a half-written binary and never fails with "text file busy".
func CopyExecutable(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	perm := info.Mode().Perm() | 0o111

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	// OpenFile's perm is filtered by umask, so pin the executable bits down
	// explicitly rather than trust what got created.
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, dst, err)
	}
	return nil
}

// AppendProfileLine appends line to the profile file if it is not already
// present verbatim, creating the file (and, for fish, its ~/.config/fish
// directory) if neither exists yet. Re-running --install after the line was
// already added is therefore a no-op here rather than a duplicate line.
func AppendProfileLine(path, line string) (added bool, err error) {
	existing, err := os.ReadFile(path) // #nosec G304 -- path is a well-known shell profile this run's own home directory names, not user-controlled input.
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if strings.Contains(string(existing), line) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) // #nosec G302,G304 -- a shell profile is meant to be readable, and path is well-known, not user-controlled.
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	data := line + "\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		data = "\n" + data
	}
	if _, err := f.WriteString(data); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}
