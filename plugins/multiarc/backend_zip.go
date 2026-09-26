package multiarc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// zipBackend wraps Info-ZIP's unzip. "-Z1" asks for its zipinfo mode's
// names-only listing (one path per line, directories carrying their own
// trailing "/" entry the way zip already stores them), which is the same
// bare format tarBackend prefers and for the same reason: it needs no
// column parsing that a space in a filename could throw off.
type zipBackend struct{}

func (zipBackend) id() string { return "zip" }

func zipAvailable() bool { return toolAvailable("unzip") }

func (zipBackend) list(ctx context.Context, localPath string) ([]entry, error) {
	out, errOut, err := runTool(ctx, "unzip", "-Z1", localPath)
	if err != nil {
		return nil, fmt.Errorf("multiarc: unzip -Z1 %s: %w (%s)", localPath, err, strings.TrimSpace(string(errOut)))
	}
	return parseBareNameListing(out), nil
}

func (zipBackend) extractAll(ctx context.Context, localPath, destDir string) error {
	_, errOut, err := runTool(ctx, "unzip", "-o", "-q", localPath, "-d", destDir)
	if err != nil {
		return fmt.Errorf("multiarc: unzip -o -q %s -d %s: %w (%s)", localPath, destDir, err, strings.TrimSpace(string(errOut)))
	}
	return nil
}

func (zipBackend) extractOne(ctx context.Context, localPath, destDir, member string) error {
	if member == "" {
		return errors.New("multiarc: extractOne needs a member path")
	}
	_, errOut, err := runTool(ctx, "unzip", "-o", "-q", localPath, member, "-d", destDir)
	if err != nil {
		return fmt.Errorf("multiarc: unzip -o -q %s %s -d %s: %w (%s)", localPath, member, destDir, err, strings.TrimSpace(string(errOut)))
	}
	return nil
}
