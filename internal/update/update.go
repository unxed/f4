package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/internal/unpack"
	"github.com/unxed/vtui"
)

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	UpdatedAt          string `json:"updated_at"`
}

type Release struct {
	TagName     string  `json:"tag_name"`
	PublishedAt string  `json:"published_at"`
	Body        string  `json:"body"`
	Assets      []Asset `json:"assets"`
}

var (
	APIURL = "https://api.github.com/repos/unxed/f4/releases"

	// Executable is where the running binary lives, corrected for the build
	// modes that move it out from under the process. A variable so a test can
	// point an install at a scratch directory.
	Executable = executable

	// CurrentOS and CurrentArch name the platform an asset must match. Both
	// are variables so a test can ask for a platform it is not running on.
	CurrentOS   = runtime.GOOS
	CurrentArch = runtime.GOARCH

	// Empty except in builds that target a specific C library; see
	// libc_musl.go and assetSuffixes.
	currentLibc = buildLibc

	// zoin-bot: fail a stalled release download instead of leaving the
	// progress screen without an answer after the network disappears.
	DownloadIdleTimeout = 30 * time.Second
)

// Settings are the four update fields of the user's configuration. They are
// passed in rather than read from a global, so this package stays independent
// of where the configuration lives.
type Settings struct {
	Channel     int    // 0 = Stable, 1 = Nightly
	Interval    int    // 0 = Never, 1 = Every start, 2 = Daily, 3 = Weekly
	LastCheck   int64  // Unix timestamp of the last check
	LastVersion string // the update key of the build already installed

	// SwitchedChannel says the user has just named Channel in place of the
	// other one -- `f4 --update stable` on a nightly setup, or a new channel
	// picked in the update dialog before Check. It is not stored: after an
	// install, LastVersion records the switch on its own. See #1218.
	SwitchedChannel bool
}

// Build describes the running binary to the update check.
type Build struct {
	// Version is what the binary reports, either a release tag or a commit.
	Version string
	// IsRelease says whether Version is a release tag. A development build's
	// version cannot be compared to one, so it is dated by TimeText instead.
	IsRelease bool
	// TimeText is the build timestamp in RFC3339 or "2006-01-02 15:04", and
	// empty when the build carries none.
	TimeText string
}

type readResult struct {
	n   int
	err error
}

// readChunk bounds the time spent waiting for the next download chunk.
// Closing the response body in the caller releases a reader that is still
// blocked when the timeout or task cancellation wins the select.
func readChunk(ctx context.Context, r io.Reader, buf []byte) (int, error) {
	if DownloadIdleTimeout <= 0 {
		return r.Read(buf)
	}

	result := make(chan readResult, 1)
	go func() {
		n, err := r.Read(buf)
		result <- readResult{n: n, err: err}
	}()

	timer := time.NewTimer(DownloadIdleTimeout)
	defer timer.Stop()
	select {
	case res := <-result:
		return res.n, res.err
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
		return 0, fmt.Errorf("update download stalled for %s", DownloadIdleTimeout)
	}
}

// Update channels, in the order the settings combo box lists them.
const (
	ChannelStable  = 0
	ChannelNightly = 1
)

// Candidate is the build a channel offers to install.
type Candidate struct {
	DownloadURL    string
	ArchiveKind    string
	DisplayVersion string
	// UpdateKey marks a build as already installed: the tag on stable, the
	// asset upload time on nightly, where the tag is always "nightly" and
	// tells builds apart not at all.
	UpdateKey   string
	NeedsUpdate bool
	// OlderThanRunning marks a stable release offered to someone who has
	// moved from nightly to stable while running a build newer than that
	// release: installing it is a step back, and the prompt says so.
	OlderThanRunning bool
}

// Check asks GitHub what cfg.Channel offers and whether that is newer than the
// running build. Shared by the dialog and by --update.
func Check(ctx context.Context, cfg Settings, b Build) (Candidate, error) {
	url := APIURL + "/latest"
	if cfg.Channel == ChannelNightly {
		url = APIURL + "/tags/nightly"
	}

	vtui.DebugLog("UPDATER: Checking for updates at %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return Candidate{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "f4-updater")

	// Everything f4 fetches goes through the configured proxy, if any.
	resp, err := netproxy.HTTPClient(0).Do(req)
	if err != nil {
		return Candidate{}, fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		proxyAuthHeader := resp.Header.Get("Proxy-Authenticate")
		vtui.DebugLog("UPDATER ERROR: HTTP %d from %s (Proxy: %s, Proxy-Authenticate: %q)",
			resp.StatusCode, url, netproxy.Global().Describe(), proxyAuthHeader)
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return Candidate{}, fmt.Errorf("GitHub API returned status 407 (Proxy Authentication Required).\nProxy: %s, Proxy-Authenticate: %s", netproxy.Global().Describe(), proxyAuthHeader)
		}
		if msg := rateLimitMessage(resp); msg != "" {
			return Candidate{}, fmt.Errorf("%s", msg)
		}
		return Candidate{}, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Candidate{}, fmt.Errorf("failed to parse API response: %w", err)
	}

	downloadURL, assetUpdated, archiveKind := pickAsset(release.Assets, editionAssetSuffixes(liteEdition, CurrentOS, CurrentArch, currentLibc))
	if downloadURL == "" {
		return Candidate{}, fmt.Errorf("no suitable build found for your OS/Arch")
	}

	cand := Candidate{
		DownloadURL:    downloadURL,
		ArchiveKind:    archiveKind,
		DisplayVersion: release.TagName,
		UpdateKey:      release.TagName,
	}

	if cfg.Channel == ChannelNightly {
		cand.UpdateKey = assetUpdated
		cand.DisplayVersion = nightlyDisplayVersion(release, assetUpdated)
		cand.NeedsUpdate = cfg.LastVersion != cand.UpdateKey || runningOlderThanNightly(release, b)
		return cand, nil
	}

	cand.NeedsUpdate = stableReleaseNeedsUpdate(release, b, cfg.LastVersion)
	if !cand.NeedsUpdate && release.TagName != b.Version && leavingNightly(cfg, b) {
		// Dates say the running build is newer, and for someone who never
		// left stable that rightly means "nothing to install". Someone who
		// has chosen stable over nightly, though, asked for the release
		// itself, so it is offered even though it is older. See #1218.
		cand.NeedsUpdate = true
		cand.OlderThanRunning = true
	}
	return cand, nil
}

// leavingNightly says a stable check comes from a user who has moved from the
// nightly channel to stable: either the switch was just made, or the build
// last installed by the updater is a nightly one -- its update key is an asset
// upload time, while a stable key is a release tag. A release build has
// nothing to leave.
func leavingNightly(cfg Settings, b Build) bool {
	if b.IsRelease {
		return false
	}
	if cfg.SwitchedChannel {
		return true
	}
	_, err := time.Parse(time.RFC3339, cfg.LastVersion)
	return err == nil
}

// runningOlderThanNightly compares the running build with the commit the
// nightly release was built from. LastVersion alone only says that an install
// of this nightly once finished; when the binary that runs is still an older
// one, "already up to date" would be false. See #1218.
func runningOlderThanNightly(release Release, b Build) bool {
	if b.IsRelease {
		return false
	}
	_, builtOn := commitInfoFromReleaseBody(release.Body)
	nightly, running := parseBuildTime(builtOn), parseBuildTime(b.TimeText)
	return !nightly.IsZero() && !running.IsZero() && nightly.After(running)
}

// nightlyDisplayVersion names a nightly build the way F1 > Help Index names it
// after installing.
//
// The asset only knows when its upload finished, which trails the commit by the
// whole build matrix; the nightly workflow records the commit and the build
// time in the release body, so prefer those. See #343.
func nightlyDisplayVersion(release Release, assetUpdated string) string {
	if commit, builtOn := commitInfoFromReleaseBody(release.Body); commit != "" {
		if builtOn != "" {
			return "Nightly (" + commit + " [" + FormatBuildTime(builtOn) + "])"
		}
		return "Nightly (" + commit + ")"
	}

	displayTime := assetUpdated
	if t, err := time.Parse(time.RFC3339, assetUpdated); err == nil {
		displayTime = t.Local().Format("2006-01-02 15:04")
	} else if len(displayTime) >= 16 {
		displayTime = strings.Replace(displayTime[:16], "T", " ", 1)
	}
	return "Nightly (" + displayTime + ")"
}

// FormatBuildTime converts the UTC timestamp Go embeds in release binaries to
// the user's local time. Nightly release metadata uses the same commit
// timestamp, so the updater and F1's Help Index show one value instead of one
// UTC value and one local value.
func FormatBuildTime(value string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		var (
			parsed time.Time
			err    error
		)
		if layout == time.RFC3339 {
			parsed, err = time.Parse(layout, value)
		} else {
			parsed, err = time.ParseInLocation(layout, value, time.UTC)
		}
		if err == nil {
			return parsed.Local().Format("2006-01-02 15:04")
		}
	}
	return value
}

// rateLimitMessage explains a 403 that came from the request limit:
// GitHub allows 60 anonymous requests an hour per address, so a shared network
// can spend them without the user touching anything. An empty string means the
// 403 had another cause.
func rateLimitMessage(resp *http.Response) string {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		return ""
	}
	msg := "GitHub limits anonymous requests to 60 per hour per address,\nand this address has used them all up."
	if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && sec > 0 {
		msg += "\nTry again after " + time.Unix(sec, 0).Local().Format("15:04") + "."
	}
	return msg
}

func stableReleaseNeedsUpdate(release Release, b Build, lastVersion string) bool {
	if release.TagName == b.Version || release.TagName == lastVersion {
		return false
	}

	// A release tag is an exact version marker. A manually built binary may
	// instead expose only a commit hash, which cannot be compared lexically to
	// a semver release tag. In that case compare the commit/build timestamp to
	// the release publication time, so a newer local checkout is not told to
	// install an older release.
	if buildTime := parseBuildTime(b.TimeText); !b.IsRelease && !buildTime.IsZero() {
		published, err := time.Parse(time.RFC3339, release.PublishedAt)
		if err == nil {
			return published.After(buildTime)
		}
	}

	// If metadata is missing or malformed, preserve the safe historical
	// behavior and offer the release rather than silently skipping it.
	return true
}

func parseBuildTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

// commitInfoFromReleaseBody pulls the commit hash and build time the
// nightly workflow records in the release body:
//
//	**Commit:** `<hash>`
//	**Built on:** `<time>`
//
// or, as the nightly workflow writes it today, on one plain line:
//
//	... Commit: <hash>. Built on: <time>
//
// Returns empty strings if either field isn't found, so the caller can fall
// back to the asset timestamp.
func commitInfoFromReleaseBody(body string) (commit, builtOn string) {
	commit = extractBacktickedField(body, "**Commit:**")
	builtOn = extractBacktickedField(body, "**Built on:**")
	if commit == "" {
		if m := plainCommitField.FindStringSubmatch(body); m != nil {
			commit = m[1]
		}
	}
	if builtOn == "" {
		if m := plainBuiltOnField.FindStringSubmatch(body); m != nil {
			builtOn = m[1]
		}
	}
	return commit, builtOn
}

var (
	plainCommitField  = regexp.MustCompile(`\bCommit:\s*([0-9a-fA-F]{7,40})\b`)
	plainBuiltOnField = regexp.MustCompile(`\bBuilt on:\s*(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?Z?)`)
)

func extractBacktickedField(body, label string) string {
	i := strings.Index(body, label)
	if i == -1 {
		return ""
	}
	rest := body[i+len(label):]
	start := strings.Index(rest, "`")
	if start == -1 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, "`")
	if end == -1 {
		return ""
	}
	return rest[:end]
}

// Download reads a release archive into memory, reporting progress
// in percent. Shared by the update dialog and by --update.
func Download(ctx context.Context, url string, progress func(percent int)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "f4-updater")

	resp, err := netproxy.HTTPClient(0).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		proxyAuthHeader := resp.Header.Get("Proxy-Authenticate")
		vtui.DebugLog("UPDATER ERROR: Download failed HTTP %d from %s (Proxy: %s, Proxy-Authenticate: %q)",
			resp.StatusCode, url, netproxy.Global().Describe(), proxyAuthHeader)
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, fmt.Errorf("download failed with status 407 (Proxy Authentication Required).\nProxy: %s, Proxy-Authenticate: %s", netproxy.Global().Describe(), proxyAuthHeader)
		}
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	contentLength := resp.ContentLength
	var archiveData bytes.Buffer
	buf := make([]byte, 32*1024)
	var downloaded int64

	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n, readErr := readChunk(ctx, resp.Body, buf)
		if n > 0 {
			archiveData.Write(buf[:n])
			downloaded += int64(n)
			pct := 0
			if contentLength > 0 {
				pct = int((downloaded * 100) / contentLength)
			}
			progress(pct)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}

	return archiveData.Bytes(), nil
}

// TargetDir is the directory an update unpacks over. Callers ask before
// downloading as well: an unreadable path is cheaper to learn about now than
// after the megabytes.
func TargetDir() (string, error) {
	exePath, err := targetExecutable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}

// targetExecutable is the file an update has to replace: the running binary
// with its symlinks resolved, because the archive is unpacked where the file
// really is.
func targetExecutable() (string, error) {
	exePath, err := Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve symlinks for executable: %w", err)
	}
	return exePath, nil
}

// Install unpacks the archive over the running binary's directory,
// escalating when permissions deny it, and fails when the binary itself was
// not among what it unpacked.
func Install(data []byte, archiveKind string) error {
	exePath, err := targetExecutable()
	if err != nil {
		return err
	}
	return installOver(exePath, data, archiveKind)
}

// installOver is Install for the executable at exePath. The release check
// (CheckReleaseArchive) runs it over a stand-in, so a release is unpacked
// by the same code before anyone installs it.
func installOver(exePath string, data []byte, archiveKind string) error {
	exeDir := filepath.Dir(exePath)
	before, err := statExecutable(exePath)
	if err != nil {
		return fmt.Errorf("failed to stat executable: %w", err)
	}

	if dirNeedsElevation(exeDir) {
		vtui.DebugLog("UPDATER: %q is not writable, requesting UAC elevation", exeDir)
		err = runElevated(data, archiveKind)
	} else {
		err = extract(data, archiveKind, exeDir)
		if err != nil && isPermissionError(err) {
			vtui.DebugLog("UPDATER: extraction needs elevation, retrying through UAC: %v", err)
			err = runElevated(data, archiveKind)
		}
	}
	if err != nil {
		return err
	}
	return checkReplaced(exePath, before)
}

// statExecutable reads the file's identity through an open handle. On Windows
// os.Stat leaves the file ID to be read when os.SameFile asks for it, and by
// then the path names the new file; a handle's Stat reads it at once.
func statExecutable(path string) (os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		// Unreadable but present: the size and time still tell.
		return os.Stat(path)
	}
	defer func() { _ = f.Close() }()
	return f.Stat()
}

// checkReplaced fails an install that left the running binary as it was.
// Extraction writes every file anew, so an archive of f4 always replaces the
// binary; an archive that did not was the wrong one. #1656: a plugin's
// archive, unpacked next to f4, was reported as the update installed, and
// the old binary then heard "already up to date" from every later check.
func checkReplaced(path string, before os.FileInfo) error {
	after, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the update left no executable at %s: %w", path, err)
	}
	if os.SameFile(before, after) && after.ModTime().Equal(before.ModTime()) && after.Size() == before.Size() {
		return fmt.Errorf("the update archive did not replace %s; the running build is still the installed one", path)
	}
	return nil
}

func extract(data []byte, archiveKind, destDir string) error {
	switch archiveKind {
	case "7z":
		return unpack.SevenZip(data, destDir)
	case "targz":
		return unpack.TarGz(data, destDir)
	default:
		// 4 workers: benchmark-optimal.
		return unpack.ZipParallel(data, destDir, min(runtime.GOMAXPROCS(0), 4))
	}
}

// assetSuffixes returns the release asset suffixes to look for, most
// preferred first.
//
// Linux is published in two flavors that share a GOOS and a GOARCH: the
// generic artifacts are fully static (they start anywhere, including on musl
// systems, but carry no FFI, so no GPU, Wayland or Ebiten backend), while the
// musl artifacts link against musl's libc and keep FFI. Only the running
// binary knows which one it is, hence libc.
//
// A musl build therefore asks for the musl asset first and falls back to the
// generic one, which is a real downgrade -- FFI disappears -- but a working
// f4 rather than a failed update. The fallback is safe in that direction only:
// the generic artifact is static, so it runs on Alpine. The reverse is not
// true, and does not happen, because a musl asset name ends in
// "-musl-<arch>.tar.gz" and so never matches a glibc build's suffix.
func assetSuffixes(goos, goarch, libc string) []string {
	if goos == "windows" {
		// Windows: priority order .7z, then .zip.
		return []string{
			fmt.Sprintf("-%s-%s.7z", goos, goarch),
			fmt.Sprintf("-%s-%s.zip", goos, goarch),
		}
	}

	// Android is published as the Termux build and named after it.
	if goos == "android" {
		goos = "termux"
	}
	generic := fmt.Sprintf("-%s-%s.tar.gz", goos, goarch)
	if goos == "linux" && libc != "" {
		return []string{
			fmt.Sprintf("-%s-%s-%s.tar.gz", goos, libc, goarch),
			generic,
		}
	}
	return []string{generic}
}

// editionAssetSuffixes is assetSuffixes for the edition this binary is.
// A lite build updates to the lite asset only: f4-lite-linux-amd64.tar.gz
// ends with "-linux-amd64.tar.gz" as well, and the regular asset the plain
// suffix would also match is a different program. There is no musl lite
// flavor.
//
// Lite is a .tar.gz on Windows too (#1656). A lite .zip ends with
// "-windows-amd64.zip", and f4 up to v0.1.3-alpha takes the first .zip by
// name with that suffix -- f4-lite-windows-amd64.zip, before
// f4-windows-amd64.zip -- so the regular edition updated itself into lite.
// No regular Windows updater asks for a .tar.gz.
func editionAssetSuffixes(lite bool, goos, goarch, libc string) []string {
	if !lite {
		return assetSuffixes(goos, goarch, libc)
	}
	return []string{fmt.Sprintf("-lite-%s-%s.tar.gz", goos, goarch)}
}

// releaseAssetPrefix begins the name of every archive of f4 itself. A release
// carries other archives beside them: the plugins f4#1178 publishes
// (android-plugin-linux-amd64.tar.gz, cloudfox-plugin-..., ios-plugin-...)
// end with the same "-<os>-<arch>.tar.gz".
const releaseAssetPrefix = "f4-"

// pickAsset returns the first release asset of f4 itself whose name ends
// with one of the suffixes, trying suffixes in order. Returns an empty url
// when nothing matches.
//
// Only names that begin with releaseAssetPrefix count. GitHub lists assets by
// name, so android-plugin-linux-amd64.tar.gz comes before
// f4-linux-amd64.tar.gz, and a suffix alone picked the plugin: the update
// unpacked it next to f4, recorded the nightly as installed and left the old
// binary running (#1656). f4-legacy-windows-386.zip still counts: it is the
// legacy build's own archive.
//
// It skips a lite asset when matching a suffix that is not itself a lite
// one: "-linux-amd64.tar.gz" matches f4-lite-linux-amd64.tar.gz too,
// and a regular build must not update itself into the lite edition.
func pickAsset(assets []Asset, suffixes []string) (url, updatedAt, kind string) {
	for _, suffix := range suffixes {
		for _, a := range assets {
			if takesAsset(a.Name, suffix) {
				return a.BrowserDownloadURL, a.UpdatedAt, archiveKindForSuffix(suffix)
			}
		}
	}
	return "", "", ""
}

// takesAsset says whether pickAsset takes the asset name for suffix.
func takesAsset(name, suffix string) bool {
	return strings.HasPrefix(name, releaseAssetPrefix) && strings.HasSuffix(name, suffix) && !strings.HasSuffix(name, "-lite"+suffix)
}

func archiveKindForSuffix(suffix string) string {
	switch {
	case strings.HasSuffix(suffix, ".7z"):
		return "7z"
	case strings.HasSuffix(suffix, ".tar.gz"):
		return "targz"
	default:
		return "zip"
	}
}
