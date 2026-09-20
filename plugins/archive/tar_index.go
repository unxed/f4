package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/tarindexcache"
)

// The tar index is built once, by scanning the whole archive, and kept in the
// user's cache so the next open is instant. Nothing tied it to the archive it
// was built from: a tar that had been replaced or rewritten under the same
// name was listed from its old index, sometimes as an empty archive (#1187).
//
// The cache file's name now carries a fingerprint of the archive, so an archive
// that changed simply finds no index and gets a new one. The fingerprint is
// cheap on purpose, since a full hash is another read of the whole archive,
// which is what the cache exists to avoid: the size, and the first and the last
// tarIndexEdge bytes.
const tarIndexEdge = 64 << 10

// tarIndexPath returns the cache file to keep the index of the tar archive at
// localPath in, or "" when the library should choose (not a tar, an index
// sidecar the user put next to it, or the archive cannot be read). Indexes the
// same archive left under an older fingerprint are removed.
func tarIndexPath(localPath string) string {
	if !looksLikeTar(localPath) {
		return ""
	}
	// A sidecar index next to the archive is the user's (ratarmount's own
	// convention); leave it to the library, which prefers it.
	for _, ext := range []string{".index.sqlite", ".index.arcidx", ".arcidx"} {
		if _, err := os.Stat(localPath + ext); err == nil {
			return ""
		}
	}
	fingerprint, err := tarFingerprint(localPath)
	if err != nil {
		return ""
	}
	dir := tarindexcache.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	// The library named its files "<name>-<hash of the path>.index.sqlite";
	// the fingerprint goes in after the same hash, so the older indexes of this
	// very archive, under either scheme, are recognisable by their prefix.
	prefix := tarindexcache.Prefix(localPath)
	current := prefix + "-" + fingerprint + ".index.sqlite"
	if !config.App.ArchiveTarIndexCache {
		// The user does not want a saved index trusted: every open builds its
		// own. The one of the last open is removed here rather than after the
		// archive is closed, which nothing here gets to see (#1187).
		removeOldTarIndexes(dir, prefix, "")
		return filepath.Join(dir, current)
	}
	removeOldTarIndexes(dir, prefix, current)
	return filepath.Join(dir, current)
}

func looksLikeTar(path string) bool {
	name := strings.ToLower(path)
	for _, suffix := range []string{".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst", ".tar.zstd"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// tarFingerprint hashes the archive's size and its first and last bytes. It is
// short: the cache file's name has to stay within a file system's limit.
func tarFingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := st.Size()
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00", size)
	head := io.NewSectionReader(f, 0, min(size, tarIndexEdge))
	if _, err := io.Copy(h, head); err != nil {
		return "", err
	}
	tailStart := max(size-tarIndexEdge, 0)
	if _, err := io.Copy(h, io.NewSectionReader(f, tailStart, size-tailStart)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:8]), nil
}

// removeOldTarIndexes deletes the index files (and SQLite's side files) of the
// same archive other than current; with no current one, all of them. Failures are ignored: a file another
// process holds open cannot be removed on Windows, and it is only clutter.
func removeOldTarIndexes(dir, prefix, current string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || (current != "" && strings.HasPrefix(name, current)) {
			continue
		}
		// The prefix ends where the name must: either the library's old
		// "<prefix>.index.sqlite" or "<prefix>-<fingerprint>.index.sqlite".
		rest := name[len(prefix):]
		if !strings.HasPrefix(rest, ".index.sqlite") && !strings.HasPrefix(rest, "-") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
}
