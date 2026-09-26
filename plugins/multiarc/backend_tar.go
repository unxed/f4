package multiarc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// tarBackend wraps the system tar binary. GNU tar and BSD tar (libarchive)
// both auto-detect gzip/bzip2/xz/zstd/lzip compression from the archive's
// own content when reading, so one backend covers every .tar.* variant in
// detectFormat without being told which compressor produced it.
type tarBackend struct{}

func (tarBackend) id() string { return "tar" }

func tarAvailable() bool { return toolAvailable("tar") }

func (tarBackend) list(ctx context.Context, localPath string) ([]entry, error) {
	out, errOut, err := runTool(ctx, "tar", "-tf", localPath)
	if err != nil {
		return nil, fmt.Errorf("multiarc: tar -tf %s: %w (%s)", localPath, err, strings.TrimSpace(string(errOut)))
	}
	return parseBareNameListing(out), nil
}

func (tarBackend) extractAll(ctx context.Context, localPath, destDir string) error {
	_, errOut, err := runTool(ctx, "tar", "-xf", localPath, "-C", destDir)
	if err != nil {
		return fmt.Errorf("multiarc: tar -xf %s -C %s: %w (%s)", localPath, destDir, err, strings.TrimSpace(string(errOut)))
	}
	return nil
}

func (tarBackend) extractOne(ctx context.Context, localPath, destDir, member string) error {
	if member == "" {
		return errors.New("multiarc: extractOne needs a member path")
	}
	_, errOut, err := runTool(ctx, "tar", "-xf", localPath, "-C", destDir, "--", member)
	if err != nil {
		return fmt.Errorf("multiarc: tar -xf %s -C %s -- %s: %w (%s)", localPath, destDir, member, err, strings.TrimSpace(string(errOut)))
	}
	return nil
}
