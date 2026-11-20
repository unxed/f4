package panel

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const promptGitCacheTTL = 5 * time.Second

type promptGitCacheEntry struct {
	branch string
	until  time.Time
}

var promptGitCache = struct {
	sync.Mutex
	entries map[string]promptGitCacheEntry
}{entries: make(map[string]promptGitCacheEntry)}

// promptGitBranch returns the branch for path without running Git on every
// prompt redraw. A short timeout keeps a slow or unavailable Git executable
// from blocking panel rendering; non-worktrees simply have no branch label.
func promptGitBranch(path string) string {
	now := time.Now()
	promptGitCache.Lock()
	for cachedPath, cached := range promptGitCache.entries {
		if !now.Before(cached.until) {
			delete(promptGitCache.entries, cachedPath)
		}
	}
	if cached, ok := promptGitCache.entries[path]; ok && now.Before(cached.until) {
		promptGitCache.Unlock()
		return cached.branch
	}
	promptGitCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", path, "branch", "--show-current").Output()
	branch := ""
	if err == nil {
		branch = strings.TrimSpace(string(output))
	}

	promptGitCache.Lock()
	promptGitCache.entries[path] = promptGitCacheEntry{branch: branch, until: time.Now().Add(promptGitCacheTTL)}
	promptGitCache.Unlock()
	return branch
}
