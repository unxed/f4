package fusefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The registry answers one question that an in-process list cannot: which
// mounts exist right now, including the ones some other f4 made.
//
// Without it, `f4 --umount` could not touch a mount started from the panels,
// the UI could not show a mount started from a shell, and a mount started by
// fstab would be invisible to both. It is a directory of small JSON files —
// one per mount, named by a hash of the mount point — rather than a daemon
// with a socket, because the thing being tracked is already a kernel object:
// the registry is a description of the world, not the world itself, and it
// must not become another thing that can be up or down.

// Record describes one live mount.
type Record struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	MountPoint string    `json:"mountpoint"`
	ReadOnly   bool      `json:"read_only"`
	PID        int       `json:"pid"`
	Started    time.Time `json:"started"`

	// Detached marks a mount that is meant to outlive the process that
	// asked for it. The UI must not sweep these up when f4 exits.
	Detached bool `json:"detached"`
}

// Age is how long the mount has been up.
func (r Record) Age() time.Duration { return time.Since(r.Started) }

// Mode renders the access mode the way mount(8) output does.
func (r Record) Mode() string {
	if r.ReadOnly {
		return "ro"
	}
	return "rw"
}

// RegistryDir is where the records live. It sits next to MountRoot() and
// follows the same rule: per-session state, not documents.
func RegistryDir() string {
	if dir := os.Getenv("F4_FUSE_REGISTRY"); dir != "" {
		return dir
	}
	if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
		return filepath.Join(run, "f4", "mounts")
	}
	user := "user"
	if uid := os.Getuid(); uid >= 0 {
		user = fmt.Sprintf("%d", uid)
	} else if name := os.Getenv("USERNAME"); name != "" {
		user = name
	}
	return filepath.Join(os.TempDir(), "f4-"+user, "mounts")
}

// recordID is a stable name for a mount point, so that re-mounting the same
// place replaces its record instead of accumulating duplicates.
func recordID(mountPoint string) string {
	abs, err := filepath.Abs(mountPoint)
	if err != nil {
		abs = mountPoint
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(filepath.Clean(abs)))
	return fmt.Sprintf("%016x", h.Sum64())
}

// Register writes a record for a mount this process owns. It fills in ID,
// PID and Started; the caller supplies the rest.
func Register(rec Record) (Record, error) {
	if rec.MountPoint == "" {
		return rec, errors.New("cannot register a mount without a mount point")
	}
	if abs, err := filepath.Abs(rec.MountPoint); err == nil {
		rec.MountPoint = filepath.Clean(abs)
	}
	rec.ID = recordID(rec.MountPoint)
	rec.PID = os.Getpid()
	if rec.Started.IsZero() {
		rec.Started = time.Now()
	}

	dir := RegistryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return rec, fmt.Errorf("registry directory: %w", err)
	}
	blob, err := json.Marshal(rec)
	if err != nil {
		return rec, err
	}
	final := filepath.Join(dir, rec.ID+".json")
	tmp, err := os.CreateTemp(dir, rec.ID+".*.tmp")
	if err != nil {
		return rec, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(blob, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return rec, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return rec, err
	}
	// Rename so a reader never sees a half-written record.
	if err := os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return rec, err
	}
	return rec, nil
}

// Deregister removes a record. A missing record is not an error: the whole
// point of the design is that anything may have cleaned up first.
func Deregister(id string) error {
	if id == "" {
		return nil
	}
	err := os.Remove(filepath.Join(RegistryDir(), id+".json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Mounts returns the live records, oldest first, deleting any whose owning
// process is gone.
//
// Liveness is judged by the owner's PID only. Probing the mount point itself
// would catch more cases, but a stat() on a wedged mount can block, and a
// `--list-mounts` that hangs is worse than one that is occasionally
// optimistic. See FUSE.md, "Known gaps".
func Mounts() ([]Record, error) {
	dir := RegistryDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		blob, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(blob, &rec); err != nil {
			os.Remove(path) // corrupt: no reader can do anything with it
			continue
		}
		if !processAlive(rec.PID) {
			os.Remove(path)
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out, nil
}

// FindMount resolves whatever the user typed — a mount point, a relative
// path to one, a record ID, or the original source — to a record.
func FindMount(target string) (Record, bool) {
	recs, err := Mounts()
	if err != nil || len(recs) == 0 {
		return Record{}, false
	}
	clean := target
	if abs, err := filepath.Abs(target); err == nil {
		clean = filepath.Clean(abs)
	}
	for _, r := range recs {
		if r.MountPoint == clean || r.ID == target {
			return r, true
		}
	}
	for _, r := range recs {
		if r.Source == target {
			return r, true
		}
	}
	return Record{}, false
}

// processAlive reports whether a PID still names a running process. The
// probe itself is platform business; see platform_unix.go.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processExists(pid)
}
