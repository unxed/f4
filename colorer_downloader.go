package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/vtui"
	"github.com/unxed/zip"
)

func SchemasExist() bool {
	path := filepath.Join(ColorerConfigsDir(), "base", "catalog.xml")
	_, err := os.Stat(path)
	return err == nil
}

var colorerDownloadURL = "https://github.com/elfmz/far2l/archive/refs/tags/v_2.8.0.zip"

func DownloadColorerSchemas(pf *PanelsFrame, onComplete func(success bool)) {
	url := colorerDownloadURL
	destDir := ColorerConfigsDir()

	pf.RunProgressTask(" Downloading Colorer Schemas ", "Connecting to GitHub...", false, func(ctx context.Context, update func(msg string, percent int)) error {
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
		var buf bytes.Buffer
		tmpBuf := make([]byte, 32*1024)
		var downloaded int64

		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n, readErr := resp.Body.Read(tmpBuf)
			if n > 0 {
				buf.Write(tmpBuf[:n])
				downloaded += int64(n)
				pct := -1
				if contentLength > 0 {
					pct = int((downloaded * 100) / contentLength)
				}
				update("Downloading schemas...", pct)
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				return readErr
			}
		}

		update("Extracting schemas...", -1)
		os.RemoveAll(destDir)
		os.MkdirAll(destDir, 0755)

		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			return err
		}

		prefix := "far2l-v_2.8.0/colorer/configs/"
		for _, f := range zr.File {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !strings.HasPrefix(f.Name, prefix) {
				continue
			}
			relPath := strings.TrimPrefix(f.Name, prefix)
			if relPath == "" {
				continue
			}

			targetPath := filepath.Join(destDir, filepath.FromSlash(relPath))
			if f.FileInfo().IsDir() {
				os.MkdirAll(targetPath, 0755)
				continue
			}

			rc, err := f.Open()
			if err != nil {
				return err
			}

			os.MkdirAll(filepath.Dir(targetPath), 0755)
			out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode())
			if err != nil {
				rc.Close()
				return err
			}

			_, err = io.Copy(out, rc)
			rc.Close()
			out.Close()
			if err != nil {
				return err
			}
		}

		return nil
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
