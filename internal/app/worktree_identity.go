package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/unxed/vtui"
)

// A linked checkout has its own HEAD file. Read that file at runtime so a
// branch switch cannot leave a build-time name or another checkout's identity.
func worktreeBranchAt(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir: ") {
		return ""
	}
	gitdir := strings.TrimPrefix(line, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	root, err := os.OpenRoot(gitdir)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	head, err := root.ReadFile("HEAD")
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(head))
	if !strings.HasPrefix(ref, "ref: refs/heads/") {
		return ""
	}
	return strings.TrimPrefix(ref, "ref: refs/heads/")
}

// worktreeBranchTTL is how long the branch label is drawn before .git and
// HEAD are read again. drawWorktreeIdentity runs on every frame, and every
// frame used to read both files (and, in a release build, fail to open
// .git). A branch switch still shows within the TTL, on the next frame.
const worktreeBranchTTL = 2 * time.Second

// worktreeBranchCache is worktreeBranchAt for the executable's folder, read
// at most once per worktreeBranchTTL. The folder itself is looked up once.
type worktreeBranchCache struct {
	mu     sync.Mutex
	dir    string
	at     time.Time
	branch string
}

var worktreeIdentity worktreeBranchCache

func (c *worktreeBranchCache) get(now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		c.dir = filepath.Dir(exe)
	}
	if c.at.IsZero() || now.Sub(c.at) >= worktreeBranchTTL || now.Before(c.at) {
		c.branch, c.at = worktreeBranchAt(c.dir), now
	}
	return c.branch
}

func drawWorktreeIdentity(scr *vtui.ScreenBuf) {
	branch := worktreeIdentity.get(time.Now())
	if branch == "" {
		return
	}
	fm := vtui.FrameManager
	if fm == nil {
		return
	}
	if top := fm.GetTopFrame(); top != nil && top.IsModal() {
		return
	}
	if fm.MenuBar != nil && fm.MenuBar.Active {
		return
	}
	width := fm.GetScreenSize()
	text := vtui.TruncateString(branch, max(1, width-4), "…")
	// The workspace strip is painted after OnRender and owns its reserved row.
	// Use the application's top header, below that strip when it is visible.
	scr.Write(max(0, (width-vtui.StringWidth(text))/2), fm.WorkspaceTopInset(), vtui.StringToCharInfo(text, vtui.Palette[vtui.ColMenuBarItem]))
}
