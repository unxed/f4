// Package unpack extracts a downloaded archive over a directory on disk.
//
// Three places in f4 do that with an archive it did not create: the updater
// unpacks a release, the plugin catalogue unpacks a plugin, and the colorer
// downloader unpacks a scheme bundle. SanitizePath is the guard all three need
// — an archive member named "../../etc/passwd" must land nowhere — and a guard
// that lives in one of the three is a guard the fourth caller will not find.
package unpack

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

func writeFileSafe(targetPath string, r io.Reader, mode os.FileMode) error {
	primaryOldPath := targetPath + ".old"
	oldPath := primaryOldPath

	err := os.Remove(oldPath)
	if err != nil && os.IsPermission(err) && vfs.GetSudoClient().IsAvailable() {
		_ = vfs.GetSudoClient().Remove(oldPath)
	}

	if _, err := os.Stat(oldPath); err == nil {
		for i := 1; i < 1000; i++ {
			cand := fmt.Sprintf("%s.%d", primaryOldPath, i)
			err := os.Remove(cand)
			if err != nil && os.IsPermission(err) && vfs.GetSudoClient().IsAvailable() {
				_ = vfs.GetSudoClient().Remove(cand)
			}
			if _, err := os.Stat(cand); os.IsNotExist(err) {
				oldPath = cand
				break
			}
		}
	}

	if _, err := os.Stat(targetPath); err == nil {
		errRename := os.Rename(targetPath, oldPath)
		if errRename != nil && os.IsPermission(errRename) && vfs.GetSudoClient().IsAvailable() {
			_ = vfs.GetSudoClient().Rename(targetPath, oldPath)
		}
	}

	dir := filepath.Dir(targetPath)
	// #nosec G301 -- updater targets may be shared/system installations whose directories must remain searchable by other users.
	errMkdir := os.MkdirAll(dir, 0755)
	if errMkdir != nil && os.IsPermission(errMkdir) && vfs.GetSudoClient().IsAvailable() {
		_ = vfs.GetSudoClient().MkDir(dir, 0755)
	}

	var f io.WriteCloser
	f, err = os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil && os.IsPermission(err) && vfs.GetSudoClient().IsAvailable() {
		vtui.DebugLog("UPDATER: Permission denied for %q, attempting elevated write...", targetPath)
		// CreateFile, not Open: on Windows no descriptor crosses from the
		// elevated dispatcher, the contents go by requests (f4#1768).
		f, err = vfs.GetSudoClient().CreateFile(targetPath, uint32(mode))
	}

	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	// The elevated writer sends what is left at Close, so its error counts.
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	errRemove := os.Remove(primaryOldPath)
	if errRemove != nil && os.IsPermission(errRemove) && vfs.GetSudoClient().IsAvailable() {
		_ = vfs.GetSudoClient().Remove(primaryOldPath)
	}

	return nil
}

func SanitizePath(name, destDir string) (string, error) {
	// Archive paths are always slash-separated
	if strings.ContainsRune(name, '\\') || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid path in archive: %s", name)
	}
	cleanName := path.Clean(name)
	if path.IsAbs(cleanName) || strings.HasPrefix(cleanName, "../") || cleanName == ".." {
		return "", fmt.Errorf("invalid path in archive: %s", name)
	}
	return filepath.Join(destDir, filepath.FromSlash(cleanName)), nil
}

type archiveEntry struct {
	name  string
	isDir bool
	mode  os.FileMode
	// mtime is the member's own modification time; zero when the archive has
	// none. The extracted file takes it (applyArchiveTimes).
	mtime time.Time
	open  func() (io.ReadCloser, error)
}

// Extract unpacks an update archive. New f4 release archives carry their
// contents below a single f4/ directory so extracting one by hand does not
// scatter files into the current directory. Older releases were flat; keep
// accepting them, and strip the wrapper only when every archive member is
// below f4/.
func Extract(data []byte, archiveKind, destDir string, workers int) error {
	names, err := archiveNames(data, archiveKind)
	if err != nil {
		return err
	}
	prefix := f4ArchivePrefix(names)
	switch archiveKind {
	case "7z":
		return sevenZip(data, destDir, prefix)
	case "targz":
		return tarGz(data, destDir, prefix)
	default:
		return zipParallel(data, destDir, workers, prefix)
	}
}

func archiveNames(data []byte, archiveKind string) ([]string, error) {
	switch archiveKind {
	case "7z":
		entries, err := sevenZipEntries(data)
		if err != nil {
			return nil, err
		}
		return entryNames(entries), nil
	case "targz":
		r := bytes.NewReader(data)
		gzr, err := newGzipReader(r)
		if err != nil {
			return nil, err
		}
		defer func() { _ = gzr.Close() }()

		tr := tar.NewReader(gzr)
		var names []string
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				return names, nil
			}
			if err != nil {
				return nil, err
			}
			names = append(names, hdr.Name)
		}
	default:
		entries, err := zipEntries(data)
		if err != nil {
			return nil, err
		}
		return entryNames(entries), nil
	}
}

func entryNames(entries []archiveEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.name
	}
	return names
}

func f4ArchivePrefix(names []string) string {
	const prefix = "f4"
	nested := false
	for _, name := range names {
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." || name == prefix {
			continue
		}
		if strings.HasPrefix(name, prefix+"/") {
			nested = true
			continue
		}
		return ""
	}
	if nested {
		return prefix
	}
	return ""
}

func extractEntry(e archiveEntry, destDir string) error {
	return extractEntryWithPrefix(e, destDir, "")
}

func extractEntryWithPrefix(e archiveEntry, destDir, prefix string) error {
	if prefix != "" {
		if e.name == prefix || e.name == prefix+"/" {
			return nil
		}
		name, ok := strings.CutPrefix(e.name, prefix+"/")
		if !ok {
			return nil
		}
		e.name = name
	}

	targetPath, err := SanitizePath(e.name, destDir)
	if err != nil {
		return nil // Skip malicious/invalid paths
	}

	if e.isDir {
		// #nosec G301 -- release archive directories may belong to a shared installation and must remain searchable by other users.
		errMkdir := os.MkdirAll(targetPath, 0755)
		if errMkdir != nil && os.IsPermission(errMkdir) && vfs.GetSudoClient().IsAvailable() {
			_ = vfs.GetSudoClient().MkDir(targetPath, 0755)
		}
		return nil
	}

	rc, err := e.open()
	if err != nil {
		return err
	}

	mode := e.mode
	if mode == 0 {
		mode = 0644
	}
	err = writeFileSafe(targetPath, rc, mode)
	_ = rc.Close()
	if err == nil {
		applyArchiveTimes(targetPath, e.mtime)
	}
	return err
}

func TarGz(data []byte, destDir string) error {
	return tarGz(data, destDir, "")
}

func tarGz(data []byte, destDir, prefix string) error {
	r := bytes.NewReader(data)
	gzr, err := newGzipReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// tar.Header.Mode is a signed container, but only its low permission
		// bits are meaningful to the extracted file.
		// #nosec G115 -- masking to 0777 bounds the os.FileMode conversion.
		mode := os.FileMode(hdr.Mode & 0o777)
		if err := extractEntryWithPrefix(archiveEntry{
			name:  hdr.Name,
			isDir: hdr.Typeflag == tar.TypeDir,
			mode:  mode,
			mtime: hdr.ModTime,
			open:  func() (io.ReadCloser, error) { return io.NopCloser(tr), nil },
		}, destDir, prefix); err != nil {
			return err
		}
	}
}

func Zip(data []byte, destDir string) error {
	return zipWithPrefix(data, destDir, "")
}

func zipWithPrefix(data []byte, destDir, prefix string) error {
	entries, err := zipEntries(data)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := extractEntryWithPrefix(e, destDir, prefix); err != nil {
			return err
		}
	}
	return nil
}

// ZipParallel: Zip with entries in parallel; falls back when <2 entries.
func ZipParallel(data []byte, destDir string, workers int) error {
	return zipParallel(data, destDir, workers, "")
}

func zipParallel(data []byte, destDir string, workers int, prefix string) error {
	entries, err := zipEntries(data)
	if err != nil {
		return err
	}
	if workers < 2 || len(entries) < 2 {
		return zipWithPrefix(data, destDir, prefix)
	}
	if workers > len(entries) {
		workers = len(entries)
	}

	errCh := make(chan error, 1)
	done := make(chan struct{})
	jobs := make(chan archiveEntry)
	go func() {
		defer close(jobs)
		for _, e := range entries {
			select {
			case jobs <- e:
			case <-done:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				if err := extractEntryWithPrefix(e, destDir, prefix); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}
