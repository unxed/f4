package panel

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func TestBuiltInPromptShowsGitBranch(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())

	repo := t.TempDir()
	if output, err := exec.Command("git", "-C", repo, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", repo, "symbolic-ref", "HEAD", "refs/heads/feature/prompt").CombinedOutput(); err != nil {
		t.Fatalf("set test branch: %v: %s", err, output)
	}

	oldUsePromptFormat := config.App.UsePromptFormat
	config.App.UsePromptFormat = false
	t.Cleanup(func() { config.App.UsePromptFormat = oldUsePromptFormat })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 25)
	pf.Panels[0].(*FileSystemPanel).Vfs = vfs.NewOSVFS(repo)
	pf.ActiveIdx = 0

	var prompt strings.Builder
	for _, cell := range pf.BuildPrompt() {
		if cell.Char != vtui.WideCharFiller {
			prompt.WriteRune(testutil.Rune(cell.Char))
		}
	}
	if got := prompt.String(); !strings.Contains(got, "{feature/prompt}") {
		t.Fatalf("built-in prompt = %q, want Git branch in braces", got)
	}
}

func TestBuiltInPromptOmitsGitBranchOutsideWorktree(t *testing.T) {
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	oldUsePromptFormat := config.App.UsePromptFormat
	config.App.UsePromptFormat = false
	t.Cleanup(func() { config.App.UsePromptFormat = oldUsePromptFormat })

	pf := NewPanelsFrame()
	defer pf.Close()
	pf.ResizeConsole(120, 25)
	pf.Panels[0].(*FileSystemPanel).Vfs = vfs.NewOSVFS(t.TempDir())
	pf.ActiveIdx = 0

	var prompt strings.Builder
	for _, cell := range pf.BuildPrompt() {
		if cell.Char != vtui.WideCharFiller {
			prompt.WriteRune(testutil.Rune(cell.Char))
		}
	}
	if got := prompt.String(); strings.Contains(got, "{") || strings.Contains(got, "}") {
		t.Fatalf("built-in prompt outside Git worktree = %q, want no branch label", got)
	}
}
