package netfox

import (
	"encoding/json"

	"github.com/unxed/f4/vfs"
)

// netfoxHistoryRef is the opaque, plugin-defined reference format shared by
// every NetFox session VFS kind (Fish, SFTP, FTP) for vfs.HistoryPathProvider
// (f4#262). Title is the same credential-free, comparable session identity
// GetTitle() already exposes and internal/app/history_bridge.go already uses
// to recognize an already-open viewer/editor session; it never contains a
// password. It is stored verbatim in folder history, including across
// restarts, so a stale reference for a session that is no longer open simply
// fails to match anything the next time it is tried.
type netfoxHistoryRef struct {
	Title string `json:"title"`
	Path  string `json:"path"`
}

// netfoxHistoryEntry implements the read half of vfs.HistoryPathProvider for
// any live NetFox session VFS. It declines (ok=false) for a VFS that has no
// title or no current remote path, which in practice means the root
// connection-list screen: that type does not embed this helper at all (see
// NetFoxVFS.HistoryEntry), so this path only matters if a future session
// kind somehow ends up without a title.
func netfoxHistoryEntry(v vfs.VFS) (display, ref string, ok bool) {
	titled, hasTitle := v.(vfs.TitleProvider)
	if !hasTitle {
		return "", "", false
	}
	title := titled.GetTitle()
	remotePath := v.GetPath()
	if title == "" || remotePath == "" {
		return "", "", false
	}
	data, err := json.Marshal(netfoxHistoryRef{Title: title, Path: remotePath})
	if err != nil {
		return "", "", false
	}
	return title + ":" + remotePath, string(data), true
}

// netfoxNavigateHistoryEntry implements the write half of
// vfs.HistoryPathProvider: it moves v to the remote path named by ref only
// when v is itself the exact session ref was recorded from, matched by
// title rather than by opening anything new (f4#262). Changing the path uses
// the connection v already holds — an OptimisticPathSetter when the VFS kind
// offers one (no round trip needed), otherwise a plain SetPath, which talks
// to the already-open connection and is not a reconnect.
func netfoxNavigateHistoryEntry(v vfs.VFS, ref string) bool {
	titled, hasTitle := v.(vfs.TitleProvider)
	if !hasTitle {
		return false
	}
	var parsed netfoxHistoryRef
	if err := json.Unmarshal([]byte(ref), &parsed); err != nil {
		return false
	}
	if parsed.Title == "" || parsed.Path == "" || titled.GetTitle() != parsed.Title {
		return false
	}
	if setter, ok := v.(vfs.OptimisticPathSetter); ok {
		return setter.SetPathOptimistic(parsed.Path) == nil
	}
	return v.SetPath(parsed.Path) == nil
}
