//go:build lite

package unpack

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
)

// The lite build reads the archives it downloads with the standard library
// instead of the archive plugin's libraries (github.com/unxed/zip,
// github.com/klauspost/pgzip, github.com/unxed/sevenzip), which it does not
// link at all (f4#1178). f4's own release and plugin archives are plain
// deflate ZIPs and gzip tarballs, which the standard library reads fine.

// ErrSevenZipUnsupported is what SevenZip returns in a lite build. The lite
// updater never asks for a .7z release asset (see internal/update).
var ErrSevenZipUnsupported = errors.New("7z archives are not supported by the lite build")

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

func SevenZip(data []byte, destDir string) error {
	return ErrSevenZipUnsupported
}

func sevenZipEntries(data []byte) ([]archiveEntry, error) {
	return nil, ErrSevenZipUnsupported
}

func sevenZip(data []byte, destDir, prefix string) error {
	return ErrSevenZipUnsupported
}
