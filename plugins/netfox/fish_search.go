package netfox

import (
	"context"
	"os"
	"path"
	"strings"

	"github.com/unxed/f4/plugins/netfox/fishplus"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// FindFilesStream keeps the panel session free while a synchronous ffind owns
// its protocol lock. Job searches already release that lock between polls.
func (v *FishVFS) FindFilesStream(ctx context.Context, dir string, q vfs.FindQuery, onFound func(vfs.FoundEntry)) error {
	if q.WholeWords || q.NotContaining || q.FindFolders || q.FindSymlinks {
		return vfs.ErrFindOptionsUnsupported
	}
	client := v.client()
	if !client.CanFind() || (q.Text != "" && !client.CanGrep()) {
		return vfs.ErrFindOptionsUnsupported
	}
	features := client.Session().Features()
	job := features.Has("ffindjob") && client.CanRunJobs() && os.Getenv("F4_NO_FFINDJOB") == ""
	if !job {
		v.conn.mu.Lock()
		dial, opts, dialAlt, optsAlt := v.conn.dial, v.conn.opts, v.conn.dialAlt, v.conn.optsAlt
		v.conn.mu.Unlock()
		if dial == nil {
			return vfs.ErrFindOptionsUnsupported
		}
		session, _, _, err := establishWithFallback(ctx, dial, opts, dialAlt, optsAlt)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			vtui.DebugLog("FIND: separate FISH+ session unavailable, using VFS walk: %v", err)
			return vfs.ErrFindOptionsUnsupported
		}
		defer func() { _ = session.Close() }()
		client = fishplus.NewClient(session)
	}
	last := vfs.FindProgress{Path: v.abs(dir)}
	var hits int64
	if q.Progress != nil {
		q.Progress(last)
	}
	opts := fishplus.FindOptions{
		Masks: q.Masks, Text: q.Text, Fixed: !q.Regex,
		IgnoreCase: q.IgnoreCase, Limit: q.Limit,
		OnFound: func(e fishplus.Entry) {
			hits++
			last.Found = max(last.Found, hits)
			last.Path = e.Name
			onFound(foundFishEntry(e))
			if q.Progress != nil {
				q.Progress(last)
			}
		},
	}
	if q.Progress != nil {
		opts.Progress = func(p fishplus.FindProgress) {
			last.Scanned, last.Found, last.Path = p.Scanned, max(hits, p.Found), p.Path
			q.Progress(last)
		}
	}
	_, err := client.Find(ctx, v.abs(dir), opts)
	// Older listing helpers can advertise a backend but lack a usable find.
	// Falling back is safe only before publishing any hits.
	if err != nil && hits == 0 && ctx.Err() == nil {
		vtui.DebugLog("FIND: remote finder unavailable, using VFS walk: %v", err)
		return vfs.ErrFindOptionsUnsupported
	}
	return err
}

func foundFishEntry(e fishplus.Entry) vfs.FoundEntry {
	name := path.Base(e.Name)
	return vfs.FoundEntry{Path: e.Name, Item: vfs.VFSItem{
		Name: name, Size: e.Size, IsDir: e.IsDir(), MTime: e.MTime, ATime: e.ATime,
		IsExecutable: e.IsExecutable(), IsHidden: strings.HasPrefix(name, "."),
		IsSymlink: e.IsSymlink(), UnixMode: e.Mode, Uid: e.Uid, Gid: e.Gid,
	}}
}
