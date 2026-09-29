package update

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// A release is read by every f4 installed anywhere, not only by the build it
// is published with. Each of them picks its archive with the rule its updater
// was built with, and a rule that has shipped cannot be changed any more: an
// asset name one of them misreads reaches all of its users at once, and a fix
// only reaches them if they can still update. #1656 was two such names, the
// plugin archives and the lite Windows archive, both sorting before f4's own.
//
// AuditRelease replays every rule still installed over a release's asset
// names before the release is published (tools/releasecheck, run by the
// nightly and release jobs), so that a layout one of them misreads is never
// published.

// flavor is one kind of installed f4: what its updater asks a release for.
type flavor struct {
	goos, goarch, libc string
	lite               bool
}

func (f flavor) String() string {
	s := f.goos + "/" + f.goarch
	if f.libc != "" {
		s += " (" + f.libc + ")"
	}
	if f.lite {
		s += " lite"
	}
	return s
}

// generation is the asset choice of the f4 builds released between two
// changes to it.
type generation struct {
	who string
	// suffixes are what the generation asks for, most preferred first, or nil
	// for a flavor it has no archive for: one it was never built as, or one
	// deliberately left behind (see below).
	suffixes func(flavor) []string
	// skipLite: it passes over a lite archive when asking for a regular one.
	skipLite bool
	// prefixed: it takes only names beginning with "f4-" (#1656).
	prefixed bool
}

// generations are the rules installed f4 builds pick with, oldest first. Add
// one whenever pickAsset or editionAssetSuffixes changes what it picks: the
// builds released before the change keep the old rule for good. The last one
// is the current code.
//
// Only the current code knows the Windows 7/8/8.1 build ("windows7"). Every
// earlier one of those builds (published from 2026-08-20) asks for the
// regular windows/amd64 archive, exactly as a regular build does, so no
// asset name can serve them their own; they are the regular windows/amd64
// flavor here, and they install a build that does not start on their system
// until reinstalled by hand.
var generations = []generation{
	{
		// No .7z, no Termux name, no musl, no lite edition.
		who: "f4 v0.1.2-alpha and v0.1.3-alpha",
		suffixes: func(f flavor) []string {
			if f.lite || f.libc != "" || f.goos == "android" || f.goos == "windows7" {
				return nil
			}
			if f.goos == "windows" {
				return []string{fmt.Sprintf("-windows-%s.zip", f.goarch)}
			}
			return []string{fmt.Sprintf("-%s-%s.tar.gz", f.goos, f.goarch)}
		},
	},
	{
		who: "f4 v0.2.0-beta to v0.3.0-beta and nightlies before 2026-09-27",
		suffixes: func(f flavor) []string {
			if f.lite || f.goos == "windows7" {
				return nil
			}
			return assetSuffixes(f.goos, f.goarch, f.libc)
		},
	},
	{
		// The lite edition appeared here. Its Windows updater asks for
		// "-lite-windows-<arch>.zip", which is exactly the name that turned
		// regular builds of the two generations above into lite ones; it was
		// dropped for "-lite-windows-<arch>.tar.gz", and these few lite
		// builds say "no suitable build found" until reinstalled by hand.
		who: "f4 nightlies from 2026-09-27 to the #1656 fix",
		suffixes: func(f flavor) []string {
			if (f.lite && f.goos == "windows") || f.goos == "windows7" {
				return nil
			}
			return editionAssetSuffixes(f.lite, f.goos, f.goarch, f.libc)
		},
		skipLite: true,
	},
	{
		who:      "current f4",
		suffixes: func(f flavor) []string { return editionAssetSuffixes(f.lite, f.goos, f.goarch, f.libc) },
		skipLite: true,
		prefixed: true,
	},
}

// installedFlavors are the platforms f4 has been released for, each of which
// has builds installed that will ask the next release for their archive.
// Taking a platform out of the release means taking it out of here as well,
// on purpose: those users stop receiving updates.
var installedFlavors = []flavor{
	{goos: "linux", goarch: "386"},
	{goos: "linux", goarch: "amd64"},
	{goos: "linux", goarch: "arm"},
	{goos: "linux", goarch: "arm64"},
	{goos: "linux", goarch: "loong64"},
	{goos: "linux", goarch: "mips"},
	{goos: "linux", goarch: "mipsle"},
	{goos: "linux", goarch: "mips64"},
	{goos: "linux", goarch: "mips64le"},
	{goos: "linux", goarch: "ppc64"},
	{goos: "linux", goarch: "ppc64le"},
	{goos: "linux", goarch: "riscv64"},
	// The musl builds published from 2026-08-22 until the universal build
	// replaced them: they fall back to the regular Linux archive.
	{goos: "linux", goarch: "amd64", libc: "musl"},
	{goos: "linux", goarch: "arm64", libc: "musl"},
	{goos: "linux", goarch: "amd64", lite: true},
	{goos: "linux", goarch: "arm", lite: true},
	{goos: "linux", goarch: "mipsle", lite: true},
	{goos: "windows", goarch: "amd64"},
	{goos: "windows", goarch: "arm64"},
	// The legacy (ReactOS/XP) build; f4-legacy-windows-386.zip is its own.
	{goos: "windows", goarch: "386"},
	{goos: "windows", goarch: "amd64", lite: true},
	// The Windows 7/8/8.1 build, f4-windows7-amd64.zip (see releaseOS).
	{goos: "windows7", goarch: "amd64"},
	{goos: "darwin", goarch: "amd64"},
	{goos: "darwin", goarch: "arm64"},
	{goos: "freebsd", goarch: "amd64"},
	{goos: "freebsd", goarch: "arm64"},
	{goos: "openbsd", goarch: "amd64"},
	{goos: "openbsd", goarch: "arm64"},
	{goos: "netbsd", goarch: "amd64"},
	{goos: "netbsd", goarch: "arm64"},
	{goos: "dragonfly", goarch: "amd64"},
	{goos: "illumos", goarch: "amd64"},
	{goos: "solaris", goarch: "amd64"},
	{goos: "android", goarch: "arm"},
	{goos: "android", goarch: "arm64"},
}

// ReleaseArchive is an archive of f4 some installed build would take from a
// release.
type ReleaseArchive struct {
	Name string
	// Kind is how the updater unpacks it: "targz", "zip" or "7z".
	Kind string
	// Executable is the file it has to replace.
	Executable string
}

// ReleaseAudit is what AuditRelease found.
type ReleaseAudit struct {
	// Problems are the builds that would install the wrong archive, or none.
	Problems []string
	// Archives are the archives the builds would install, each once.
	Archives []ReleaseArchive
}

// AuditRelease replays every generation of installed f4 over a release's
// asset names, in the order the GitHub API lists them: by name. A build must
// take one of the archives the current code accepts for its platform and
// edition; taking anything else, or nothing where the current code finds an
// archive, is a problem.
func AuditRelease(names []string) ReleaseAudit {
	names = slices.Sorted(slices.Values(names))
	flavors := slices.Clone(installedFlavors)
	for _, name := range names {
		if f, ok := publishedFlavor(name); ok && !slices.Contains(flavors, f) {
			flavors = append(flavors, f)
		}
	}

	var audit ReleaseAudit
	seen := map[string]bool{}
	current := generations[len(generations)-1]
	for _, f := range flavors {
		accepted := acceptedArchives(names, current.suffixes(f))
		if len(accepted) == 0 {
			audit.Problems = append(audit.Problems, fmt.Sprintf(
				"no archive of f4 for %s: installed builds for it would stop updating", f))
			continue
		}
		for _, g := range generations {
			suffixes := g.suffixes(f)
			if suffixes == nil {
				continue
			}
			name, kind := g.pick(names, suffixes)
			if !slices.Contains(accepted, name) {
				got := name
				if got == "" {
					got = "nothing"
				}
				audit.Problems = append(audit.Problems, fmt.Sprintf(
					"%s on %s would install %s instead of %s",
					g.who, f, got, strings.Join(accepted, " or ")))
				continue
			}
			if !seen[name] {
				seen[name] = true
				audit.Archives = append(audit.Archives, ReleaseArchive{Name: name, Kind: kind, Executable: archiveExecutable(name, f)})
			}
		}
	}
	return audit
}

// pick is the generation's choice among names, which are sorted.
func (g generation) pick(names, suffixes []string) (name, kind string) {
	for _, suffix := range suffixes {
		for _, n := range names {
			if g.prefixed && !strings.HasPrefix(n, releaseAssetPrefix) {
				continue
			}
			if g.skipLite && strings.HasSuffix(n, "-lite"+suffix) {
				continue
			}
			if strings.HasSuffix(n, suffix) {
				return n, archiveKindForSuffix(suffix)
			}
		}
	}
	return "", ""
}

// acceptedArchives are the names the current code takes for any of suffixes:
// a Windows build may take the .7z or the .zip, a musl build its own archive
// or the regular one.
func acceptedArchives(names, suffixes []string) []string {
	var accepted []string
	for _, suffix := range suffixes {
		for _, n := range names {
			if takesAsset(n, suffix) && !slices.Contains(accepted, n) {
				accepted = append(accepted, n)
			}
		}
	}
	return accepted
}

// publishedFlavor reads the platform from the name of an archive of f4, so a
// platform added to the release is checked without being listed above.
// Names that are not "f4-[lite-]<os>-[musl-]<arch>.<tar.gz|zip|7z>" -- the
// macOS .app.zip, the Termux .deb -- say nothing. <os> is the name releaseOS
// gives: a GOOS, "termux" for Android, or "windows7".
func publishedFlavor(name string) (flavor, bool) {
	rest, ok := strings.CutPrefix(name, releaseAssetPrefix)
	if !ok {
		return flavor{}, false
	}
	trimmed := false
	for _, ext := range []string{".tar.gz", ".zip", ".7z"} {
		if r, ok := strings.CutSuffix(rest, ext); ok {
			rest, trimmed = r, true
			break
		}
	}
	if !trimmed {
		return flavor{}, false
	}
	var f flavor
	if r, ok := strings.CutPrefix(rest, "lite-"); ok {
		rest, f.lite = r, true
	}
	i := strings.LastIndexByte(rest, '-')
	if i < 0 {
		return flavor{}, false
	}
	f.goos, f.goarch = rest[:i], rest[i+1:]
	if r, ok := strings.CutSuffix(f.goos, "-musl"); ok {
		f.goos, f.libc = r, "musl"
	}
	switch f.goos {
	case "termux":
		f.goos = "android"
	case "legacy-windows":
		f.goos = "windows"
	}
	if !knownAssetOS[f.goos] || !knownGOARCH[f.goarch] {
		return flavor{}, false
	}
	return f, true
}

var (
	knownAssetOS = map[string]bool{
		"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true,
		"illumos": true, "ios": true, "linux": true, "netbsd": true, "openbsd": true,
		"plan9": true, "solaris": true, "windows": true, "windows7": true,
	}
	knownGOARCH = map[string]bool{
		"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true,
		"mips": true, "mipsle": true, "mips64": true, "mips64le": true,
		"ppc64": true, "ppc64le": true, "riscv64": true, "s390x": true,
	}
)

// archiveExecutable is the file an archive has to replace: f4, f4.exe, and
// for the legacy Windows build the f4-legacy.exe its archive holds.
func archiveExecutable(name string, f flavor) string {
	switch {
	case strings.HasPrefix(name, releaseAssetPrefix+"legacy-"):
		return "f4-legacy.exe"
	case f.goos == "windows" || f.goos == "windows7":
		return "f4.exe"
	}
	return "f4"
}

// CheckReleaseArchive unpacks a release archive the way the updater does,
// over a stand-in for the executable it names, and fails unless the
// executable was replaced.
func CheckReleaseArchive(data []byte, a ReleaseArchive) error {
	dir, err := os.MkdirTemp("", "f4-release-check-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	exe := filepath.Join(dir, a.Executable)
	if err := os.WriteFile(exe, []byte("installed f4"), 0o755); err != nil { // #nosec G306 -- the stand-in is an executable the archive must replace.
		return err
	}
	return installOver(exe, data, a.Kind)
}
