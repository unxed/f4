package mongofs

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/unxed/f4/vfs"
)

// uriProvider opens mongo:///<path> as the MongoDB panel at that folder, so a
// bookmark, a folder-history entry or a restored session (f4#1669) can bring
// the panel back. Which server it talks to is not in the URI: it is the same
// one the drive menu entry uses.
type uriProvider struct {
	// open makes the connect function of one panel, so that a password typed
	// for it is remembered by that panel only.
	open func() func(context.Context) (*conn, error)
}

func (uriProvider) Scheme() string { return "mongo" }

func (p uriProvider) OpenURI(ctx context.Context, _ vfs.VFS, raw string) (vfs.VFS, error) {
	if len(raw) < len(uriPrefix) || !strings.EqualFold(raw[:len(uriPrefix)], uriPrefix) {
		return nil, fmt.Errorf("MongoDB: not a mongo:// address: %s", raw)
	}
	plain := path.Clean("/" + strings.TrimPrefix(raw[len(uriPrefix):], "/"))
	v := newMongoVFS(p.open())
	item, err := v.Stat(ctx, plain)
	if err != nil {
		_ = v.Close()
		return nil, err
	}
	if !item.IsDir {
		_ = v.Close()
		return nil, fmt.Errorf("%s: %w", plain, errNotADirectory)
	}
	v.mu.Lock()
	v.cwd = plain
	v.mu.Unlock()
	return v, nil
}
