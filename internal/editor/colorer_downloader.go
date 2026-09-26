//go:build !lite

package editor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/internal/unpack"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
	"github.com/unxed/zip"
)

func SchemasExist() bool {
	path := filepath.Join(ColorerConfigsDir(), "base", "catalog.xml")
	_, err := os.Stat(path)
	return err == nil
}

// ColorerDownloadURL is where the colorer schemes come from. It is a variable
// so a test can point it at a local server; the address itself is upstream's
// release archive and is not configurable at runtime.
var ColorerDownloadURL = "https://github.com/elfmz/far2l/archive/refs/tags/v_2.8.0.zip"

const MaxColorerDownload = 256 << 20

func DownloadColorerSchemas(app vfs.App, onComplete func(success bool)) {
	url := ColorerDownloadURL
	destDir := ColorerConfigsDir()

	app.RunProgressTask(" Downloading Colorer Schemas ", "Connecting to GitHub...", false, func(ctx context.Context, updateProgress func(msg string, percent int)) error {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "f4-colorer-downloader")

		resp, err := netproxy.HTTPClient(0).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return fmt.Errorf("download failed with status %d", resp.StatusCode)
		}

		contentLength := resp.ContentLength
		if contentLength > MaxColorerDownload {
			return fmt.Errorf("colorer schemas download exceeds %d bytes", MaxColorerDownload)
		}
		var buf bytes.Buffer
		tmpBuf := make([]byte, 32*1024)
		var downloaded int64

		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, readErr := resp.Body.Read(tmpBuf)
			if n > 0 {
				if downloaded+int64(n) > MaxColorerDownload {
					return fmt.Errorf("colorer schemas download exceeds %d bytes", MaxColorerDownload)
				}
				buf.Write(tmpBuf[:n])
				downloaded += int64(n)
				pct := -1
				if contentLength > 0 {
					pct = int((downloaded * 100) / contentLength)
				}
				updateProgress("Downloading schemas...", pct)
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				return readErr
			}
		}

		updateProgress("Extracting schemas...", -1)
		return InstallColorerSchemas(buf.Bytes(), destDir, ctx)
	}, func(err error) {
		if err != nil {
			vtui.ShowMessage(" Error ", fmt.Sprintf("Failed to download Colorer schemas:\n%v\n\nFalling back to Chroma.", err), []string{"&Ok"})
			onComplete(false)
		} else {
			// The cached scheme list predates the download — invalidate it.
			ResetColorerSchemesCache()
			vtui.ShowMessage(" Success ", "Colorer schemas downloaded and installed successfully!", []string{"&Ok"})
			onComplete(true)
		}
	})
}

// InstallColorerSchemas validates and extracts a downloaded schema archive in
// a fresh sibling directory, then swaps it into place. The old installation
// is never removed before the new one is complete.
func InstallColorerSchemas(data []byte, destDir string, ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	prefix := "far2l-v_2.8.0/colorer/configs/"
	seen := make(map[string]struct{})
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}
		if _, err := unpack.SanitizePath(relPath, destDir); err != nil {
			return fmt.Errorf("invalid Colorer archive member %q: %w", f.Name, err)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is not allowed in Colorer archive member %q", f.Name)
		}
		key := path.Clean(filepath.ToSlash(relPath))
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate Colorer archive member %q", f.Name)
		}
		seen[key] = struct{}{}
	}

	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(destDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink Colorer catalog %q", destDir)
	}
	stage, err := os.MkdirTemp(parent, ".f4-colorer-stage-*")
	if err != nil {
		return err
	}
	stageLive := true
	defer func() {
		if stageLive {
			_ = os.RemoveAll(stage)
		}
	}()

	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}
		targetPath, err := unpack.SanitizePath(relPath, stage)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeInErr := rc.Close()
		if copyErr == nil {
			copyErr = closeInErr
		}
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeOutErr := out.Close()
		if copyErr == nil {
			copyErr = closeOutErr
		}
		if copyErr != nil {
			return copyErr
		}
	}

	backup := ""
	if _, err := os.Lstat(destDir); err == nil {
		backup, err = os.MkdirTemp(parent, ".f4-colorer-backup-*")
		if err != nil {
			return err
		}
		_ = os.Remove(backup)
		if err := os.Rename(destDir, backup); err != nil {
			_ = os.RemoveAll(backup)
			return err
		}
	}
	if err := os.Rename(stage, destDir); err != nil {
		if backup != "" {
			_ = os.Rename(backup, destDir)
		}
		return err
	}
	stageLive = false
	if backup != "" {
		// Cleanup failure cannot invalidate the newly installed catalog; leave
		// the backup for recovery and report the successful install.
		_ = os.RemoveAll(backup)
	}
	return nil
}
