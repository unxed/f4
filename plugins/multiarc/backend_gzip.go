package multiarc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// gzipBackend handles a lone .gz file: not a tar.gz (tarBackend already
// claims those in detectFormat), just one compressed file, the way a
// downloaded log or a kernel config on a router often arrives. gzip does
// not hold a directory of members, so list always answers with exactly
// one entry, named by stripping ".gz" from the archive's own name -- gzip
// itself can carry the original name in its header, but not every gzip
// on PATH exposes it the same way, and the archive's name is right there
// for free.
type gzipBackend struct{ bin string }

func (gzipBackend) id() string { return "gzip" }

func gzipTool() (string, bool) {
	for _, name := range []string{"gzip", "gunzip"} {
		if toolAvailable(name) {
			return name, true
		}
	}
	return "", false
}

func (b gzipBackend) tool() (string, bool) {
	if b.bin != "" {
		return b.bin, true
	}
	return gzipTool()
}

func (gzipBackend) innerName(localPath string) string {
	name := strings.TrimSuffix(path.Base(localPath), ".gz")
	if name == "" {
		name = "data"
	}
	return name
}

func (b gzipBackend) list(_ context.Context, localPath string) ([]entry, error) {
	return []entry{{Path: b.innerName(localPath)}}, nil
}

// decompressTo runs "<tool> -dc localPath" and writes its stdout to
// destPath. gzip's own -d writes next to the source and refuses to
// overwrite, which is the wrong shape for extracting into a scratch
// directory, so this always goes through -c instead.
func (b gzipBackend) decompressTo(ctx context.Context, localPath, destPath string) error {
	bin, ok := b.tool()
	if !ok {
		return errors.New("multiarc: no gzip/gunzip on PATH")
	}
	out, errOut, err := runTool(ctx, bin, "-dc", localPath)
	if err != nil {
		return fmt.Errorf("multiarc: %s -dc %s: %w (%s)", bin, localPath, err, strings.TrimSpace(string(errOut)))
	}
	return os.WriteFile(destPath, out, 0o644)
}

func (b gzipBackend) extractAll(ctx context.Context, localPath, destDir string) error {
	return b.decompressTo(ctx, localPath, path.Join(destDir, b.innerName(localPath)))
}

func (b gzipBackend) extractOne(ctx context.Context, localPath, destDir, member string) error {
	if member != b.innerName(localPath) {
		return fmt.Errorf("multiarc: gzip archive has no member %q", member)
	}
	return b.decompressTo(ctx, localPath, path.Join(destDir, member))
}
