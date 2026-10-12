//go:build !lite

package unpack

import (
	"bytes"
	"io"

	gzip "github.com/klauspost/pgzip"
	"github.com/unxed/sevenzip"
	"github.com/unxed/zip"
)

// The regular build reads the archives it downloads with the same libraries
// the archive plugin uses. formats_lite.go swaps them for the standard
// library, which covers everything f4's own release and plugin archives hold.

func newGzipReader(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}

func zipEntries(data []byte) ([]archiveEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	entries := make([]archiveEntry, len(zr.File))
	for i, f := range zr.File {
		entries[i] = archiveEntry{
			name:  f.Name,
			isDir: f.FileInfo().IsDir(),
			mode:  f.Mode(),
			mtime: f.FileInfo().ModTime(),
			open:  f.Open,
		}
	}
	return entries, nil
}

func sevenZipEntries(data []byte) ([]archiveEntry, error) {
	szr, err := sevenzip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	entries := make([]archiveEntry, len(szr.File))
	for i, f := range szr.File {
		entries[i] = archiveEntry{
			name:  f.Name,
			isDir: f.FileInfo().IsDir(),
			mode:  f.Mode(),
			mtime: f.FileInfo().ModTime(),
			open:  f.Open,
		}
	}
	return entries, nil
}

func SevenZip(data []byte, destDir string) error {
	return sevenZip(data, destDir, "")
}

func sevenZip(data []byte, destDir, prefix string) error {
	entries, err := sevenZipEntries(data)
	if err != nil {
		return err
	}
	for _, f := range entries {
		if err := extractEntryWithPrefix(f, destDir, prefix); err != nil {
			return err
		}
	}
	return nil
}
