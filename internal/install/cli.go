package install

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// UserHomeDir and Getenv are os.UserHomeDir and os.Getenv, held in variables
// so a test can point RunCLI at a scratch "home" and a synthetic
// $SHELL/$PATH without touching the real environment -- the same seam style
// internal/update uses for Executable, CurrentOS and CurrentArch.
var (
	UserHomeDir = os.UserHomeDir
	Getenv      = os.Getenv
)

// Confirm asks the user a yes/no question on stdin, defaulting to "no" on
// EOF, an empty answer, or anything but "y"/"yes". A variable so RunCLI's
// test can answer without a real terminal attached.
var Confirm = confirmStdin

func confirmStdin(prompt string) bool {
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// Options are the parsed `--install` command-line flags.
type Options struct {
	// AutoConfirm is --install --yes: append the PATH line to the shell
	// profile outright, without asking first. Without it, RunCLI only ever
	// prints the line and Confirm()s before touching the user's dotfile --
	// appending to someone's profile is a bigger action than copying a
	// binary, so it does not happen on --install alone.
	AutoConfirm bool
}

// RunCLI serves `f4 --install [--yes]`: copies exePath (the running
// executable, resolved by the caller the same way --update resolves its own
// target) into ~/.local/bin, falling back to ~/bin, and offers to add that
// directory to PATH via the current shell's profile if it is not there
// already. Returns the process exit code.
//
// Unix shells only (bash, zsh, fish); see the package doc comment for why
// Windows is out of scope for this slice.
func RunCLI(exePath string, opts Options) int {
	if runtime.GOOS == "windows" {
		fmt.Println("f4: --install covers Unix shells (bash, zsh, fish) and does not run on Windows yet.")
		fmt.Println("Use --update to fetch new builds, or copy f4.exe onto PATH by hand; a PowerShell $PROFILE equivalent is a reasonable follow-up.")
		return 1
	}

	home, err := UserHomeDir()
	if err != nil {
		fmt.Printf("f4: could not determine your home directory: %v\n", err)
		return 1
	}

	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	dir, usedFallback, err := ChooseInstallDir(home, EnsureDir)
	if err != nil {
		fmt.Printf("f4: %v\n", err)
		return 1
	}
	if usedFallback {
		fmt.Printf("%s is not available; installing into %s instead.\n", PreferredDir(home), dir)
	}

	dst := filepath.Join(dir, filepath.Base(exePath))
	if err := CopyExecutable(exePath, dst); err != nil {
		fmt.Printf("f4: install failed: %v\n", err)
		return 1
	}
	fmt.Printf("Installed %s\n", dst)

	if HasDirOnPath(Getenv("PATH"), dir) {
		fmt.Println("That directory is already on PATH; nothing else to do.")
		return 0
	}

	profile, ok := DetectShellProfile(home, Getenv("SHELL"))
	line := PathLine(profile, home, dir)

	if !ok {
		fmt.Printf("\n%s is not on PATH yet, and f4 could not recognize your shell from $SHELL.\n", dir)
		fmt.Printf("Add this line to your shell's profile file yourself:\n\n    %s\n\n", line)
		return 0
	}

	existing, err := os.ReadFile(profile.Path) // #nosec G304 -- profile.Path is this run's own detected shell profile, not user-controlled input.
	if err != nil && !os.IsNotExist(err) {
		fmt.Printf("f4: could not read %s: %v\n", profile.Path, err)
		fmt.Printf("Add this line yourself:\n\n    %s\n\n", line)
		return 1
	}
	if strings.Contains(string(existing), line) {
		fmt.Printf("%s already has this line; restart your shell (or run `source %s`) to use it.\n", profile.Path, profile.Path)
		return 0
	}

	if !opts.AutoConfirm {
		fmt.Printf("\n%s is not on PATH yet for %s.\n", dir, profile.Shell)
		fmt.Printf("Add this line to %s?\n\n    %s\n\n", profile.Path, line)
		if !Confirm("Add it now? [y/N] ") {
			fmt.Println("Not changed. Add it yourself, or re-run with --install --yes.")
			return 0
		}
	}

	if _, err := AppendProfileLine(profile.Path, line); err != nil {
		fmt.Printf("f4: could not update %s: %v\n", profile.Path, err)
		fmt.Printf("Add this line yourself:\n\n    %s\n\n", line)
		return 1
	}
	fmt.Printf("Added to %s. Restart your shell (or run `source %s`) to use it.\n", profile.Path, profile.Path)
	return 0
}
