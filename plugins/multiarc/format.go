package multiarc

import (
	"context"
	"strings"
	"time"
)

// entry is one member of an archive, as a backend's list reports it. Path
// is forward-slash separated and relative to the archive root, with no
// leading slash; a trailing slash on the raw listing line is what marks it
// as IsDir, already stripped by the time it reaches here. Size/MTime are
// best-effort: several backends are deliberately chosen for how robustly
// they parse rather than for how much they report (see backend_tar.go),
// and leave SizeKnown false and MTime zero rather than guess.
type entry struct {
	Path      string
	IsDir     bool
	Size      int64
	SizeKnown bool
	MTime     time.Time
}

// backend is one CLI archiver wrapped just enough to list and extract.
// far2l's multiarc calls this a "format module"; f4's lite build needs far
// fewer of them, so there is no separate registration mechanism, just the
// small switch in detectFormat.
type backend interface {
	// id names the backend for the panel title and for error messages, e.g.
	// "tar", "zip", "7z", "gzip".
	id() string
	// list returns every entry the archive at localPath contains.
	list(ctx context.Context, localPath string) ([]entry, error)
	// extractAll extracts the whole archive into destDir, which the caller
	// has already created.
	extractAll(ctx context.Context, localPath, destDir string) error
	// extractOne extracts a single member, named exactly as list reported
	// it, into destDir, recreating the member's own parent directories
	// under destDir the way every wrapped tool already does on its own.
	extractOne(ctx context.Context, localPath, destDir, member string) error
}

// hasAnySuffix reports whether lower (already lower-cased by the caller)
// ends with any of suffixes.
func hasAnySuffix(lower string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// detectFormat maps an archive's name to the backend that would read it,
// and reports ok=false when either the name matches nothing multiarc
// knows, or it does but no tool for it is on PATH -- which is what lets a
// missing 7z simply mean ".7z files are not supported" instead of failing
// the whole plugin (f4#1178). The .tar.* cases are listed before the bare
// ".gz" one on purpose: a switch takes its first matching case, so
// "backup.tar.gz" is claimed by tar and never falls through to the
// single-file gzip backend.
func detectFormat(name string) (backend, string, bool) {
	lower := strings.ToLower(name)
	switch {
	case hasAnySuffix(lower,
		".tar", ".tar.gz", ".tgz", ".taz",
		".tar.bz2", ".tbz2", ".tbz", ".tb2",
		".tar.xz", ".txz",
		".tar.zst", ".tzst",
		".tar.lz", ".tlz"):
		if tarAvailable() {
			return tarBackend{}, "tar", true
		}
		return nil, "", false
	case hasAnySuffix(lower, ".zip", ".jar"):
		if zipAvailable() {
			return zipBackend{}, "zip", true
		}
		return nil, "", false
	case hasAnySuffix(lower, ".7z"):
		if bin, ok := sevenZipTool(); ok {
			return sevenZipBackend{bin: bin}, "7z", true
		}
		return nil, "", false
	case hasAnySuffix(lower, ".gz"):
		if bin, ok := gzipTool(); ok {
			return gzipBackend{bin: bin}, "gzip", true
		}
		return nil, "", false
	}
	return nil, "", false
}

// parseBareNameListing turns a names-only listing (one path per line, a
// trailing "/" marking a directory) into entries. tar -tf and unzip -Z1
// both speak this format, and it is chosen over each tool's richer verbose
// listing on purpose: GNU tar's "-tv" and BSD tar's own verbose format
// disagree on column layout (owner/group as one field vs separate numeric
// ones, date format, whether the size precedes or follows them), and a
// filename containing a space makes any fixed-column parser guess wrong
// silently. The bare listing carries no size or timestamp, but one path
// exactly as the archive holds it, unambiguous on every tar and unzip this
// plugin will meet.
func parseBareNameListing(out []byte) []entry {
	lines := strings.Split(string(out), "\n")
	entries := make([]entry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		p := strings.TrimPrefix(line, "./")
		p = strings.ReplaceAll(p, "\\", "/")
		isDir := strings.HasSuffix(p, "/")
		clean := strings.TrimSuffix(p, "/")
		clean = strings.Trim(clean, "/")
		if clean == "" || clean == "." {
			continue
		}
		entries = append(entries, entry{Path: clean, IsDir: isDir})
	}
	return entries
}
