package fishplus

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// ErrNoSymlink is returned when the remote host cannot create symbolic links.
var ErrNoSymlink = errors.New("fishplus: the remote host cannot create symlinks")

// MkDir creates a directory and any missing parent of it, so a caller does
// not pay a round trip per level.
func (c *Client) MkDir(ctx context.Context, p string) error {
	resp, err := c.sess.ExecPath(ctx, "mkdir", p)
	if err != nil {
		return err
	}
	return resp.Err("mkdir " + p)
}

// Remove deletes a file or a symlink. A path that is not there is not an
// error, which matches what the panel expects when two operations race.
func (c *Client) Remove(ctx context.Context, p string) error {
	resp, err := c.sess.ExecPath(ctx, "rm", p)
	if err != nil {
		return err
	}
	return resp.Err("rm " + p)
}

// RemoveDir deletes an empty directory.
func (c *Client) RemoveDir(ctx context.Context, p string) error {
	resp, err := c.sess.ExecPath(ctx, "rmdir", p)
	if err != nil {
		return err
	}
	return resp.Err("rmdir " + p)
}

// RemoveAll deletes a directory with everything below it. The remote host
// does the walking, which is the whole point of a shell based file system:
// the classic alternative is one round trip per entry. The helper refuses to
// aim this at the root directory.
func (c *Client) RemoveAll(ctx context.Context, p string) error {
	resp, err := c.sess.ExecPath(ctx, "rmtree", p)
	if err != nil {
		return err
	}
	return resp.Err("rmtree " + p)
}

// Rename moves a path, overwriting the destination the way mv does.
func (c *Client) Rename(ctx context.Context, from, to string) error {
	resp, err := c.sess.ExecPaths(ctx, "mv", []string{from, to})
	if err != nil {
		return err
	}
	return resp.Err("mv " + from)
}

// Copy copies a path, overwriting the destination the way cp does.
func (c *Client) Copy(ctx context.Context, from, to string) error {
	resp, err := c.sess.ExecPaths(ctx, "cp", []string{from, to})
	if err != nil {
		return err
	}
	return resp.Err("cp " + from)
}

// Symlink creates a symbolic link at linkPath pointing at target.
//
// The target is stored verbatim and is never resolved here: a relative
// target, a target that does not exist yet, and a target containing ".." are
// all ordinary and all meaningful, so only linkPath is subject to the path
// guard the remote host applies to every mutation.
//
// A linkPath that already exists is refused rather than replaced. That is not
// only about not losing data: ln -s TARGET DIR silently creates the link
// inside DIR instead of failing, so a name collision would otherwise put a
// link somewhere nobody asked for.
func (c *Client) Symlink(ctx context.Context, linkPath, target string) error {
	if !c.CanSymlink() {
		return ErrNoSymlink
	}
	resp, err := c.sess.ExecPaths(ctx, "mklink", []string{linkPath, target})
	if err != nil {
		return err
	}
	return resp.Err("mklink " + linkPath)
}

// CanSymlink reports whether the remote host can create symbolic links.
//
// The banner lists ln among its tools only from the helper revision that
// implements mklink, so its presence answers both questions at once: the
// tool is there and the helper knows the command.
func (c *Client) CanSymlink() bool { return c.sess.Features().Has("ln") }

// Chmod sets the permission, setuid, setgid and sticky bits; the file type
// bits of a raw st_mode are ignored, so an Entry.Mode can be passed as is.
func (c *Client) Chmod(ctx context.Context, p string, mode uint32) error {
	octal := strconv.FormatUint(uint64(mode&07777), 8)
	resp, err := c.sess.ExecPath(ctx, "chmod", p, octal)
	if err != nil {
		return err
	}
	return resp.Err("chmod " + p)
}

// Chown changes ownership. A negative uid or gid leaves that half alone, and
// a request that would change neither costs no round trip at all.
func (c *Client) Chown(ctx context.Context, p string, uid, gid int) error {
	if uid < 0 && gid < 0 {
		return nil
	}
	resp, err := c.sess.ExecPath(ctx, "chown", p, ownerArg(uid), ownerArg(gid))
	if err != nil {
		return err
	}
	return resp.Err("chown " + p)
}

func ownerArg(id int) string {
	if id < 0 {
		return "-"
	}
	return strconv.Itoa(id)
}

// Chtimes sets the modification and access times. A zero time leaves that
// timestamp alone; so does a time before the epoch, which no remote touch
// can be relied upon to express.
func (c *Client) Chtimes(ctx context.Context, p string, mtime, atime time.Time) error {
	m, a := epochArg(mtime), epochArg(atime)
	if m == "-" && a == "-" {
		return nil
	}
	resp, err := c.sess.ExecPath(ctx, "utime", p, m, a)
	if err != nil {
		return err
	}
	return resp.Err("utime " + p)
}

func epochArg(t time.Time) string {
	if t.IsZero() || t.Unix() < 0 {
		return "-"
	}
	return strconv.FormatInt(t.Unix(), 10)
}
