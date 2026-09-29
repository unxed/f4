package update

import (
	"slices"
	"strings"
	"testing"
)

// The release publishes two Linux flavors under one GOOS/GOARCH pair:
// f4-linux-<arch>.tar.gz (static, no FFI) and f4-linux-musl-<arch>.tar.gz
// (musl, full FFI). Asset selection is the only thing keeping a musl install
// from quietly updating itself into the flavor it did not ask for, so the
// preference order and -- more importantly -- the directions the fallback
// must NOT go are pinned here.

func linuxAssets() []Asset {
	return []Asset{
		{Name: "f4-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/generic-amd64"},
		{Name: "f4-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example/musl-amd64"},
		{Name: "f4-linux-arm64.tar.gz", BrowserDownloadURL: "https://example/generic-arm64"},
		{Name: "f4-linux-musl-arm64.tar.gz", BrowserDownloadURL: "https://example/musl-arm64"},
		{Name: "f4-darwin-arm64.tar.gz", BrowserDownloadURL: "https://example/darwin"},
	}
}

func TestUpdateAssetSuffixes_PicksMatchingFlavor(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		goarch  string
		libc    string
		assets  []Asset
		wantURL string
	}{
		{
			name:    "musl build prefers the musl asset",
			goos:    "linux",
			goarch:  "amd64",
			libc:    "musl",
			assets:  linuxAssets(),
			wantURL: "https://example/musl-amd64",
		},
		{
			name:    "musl build on arm64 stays on arm64",
			goos:    "linux",
			goarch:  "arm64",
			libc:    "musl",
			assets:  linuxAssets(),
			wantURL: "https://example/musl-arm64",
		},
		{
			// The generic Linux artifacts are static and do start on a musl
			// system, so an older release without musl assets should still
			// update rather than refuse. FFI is lost; f4 keeps running.
			name:   "musl build falls back to the static asset",
			goos:   "linux",
			goarch: "amd64",
			libc:   "musl",
			assets: []Asset{
				{Name: "f4-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/generic-amd64"},
			},
			wantURL: "https://example/generic-amd64",
		},
		{
			// The dangerous direction: a glibc build must never take the
			// musl artifact, which would not start at all on its system.
			name:    "glibc build ignores the musl asset",
			goos:    "linux",
			goarch:  "amd64",
			libc:    "",
			assets:  linuxAssets(),
			wantURL: "https://example/generic-amd64",
		},
		{
			name:   "glibc build refuses rather than take a musl-only release",
			goos:   "linux",
			goarch: "amd64",
			libc:   "",
			assets: []Asset{
				{Name: "f4-linux-musl-amd64.tar.gz", BrowserDownloadURL: "https://example/musl-amd64"},
			},
			wantURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, _, kind := pickAsset(tt.assets, assetSuffixes(tt.goos, tt.goarch, tt.libc))
			if url != tt.wantURL {
				t.Errorf("picked %q, want %q", url, tt.wantURL)
			}
			if tt.wantURL != "" && kind != "targz" {
				t.Errorf("archive kind = %q, want targz", kind)
			}
		})
	}
}

// A musl asset name ends with "-musl-<arch>.tar.gz", so it cannot match the
// generic "-linux-<arch>.tar.gz" suffix. That is what makes the glibc case
// above safe, and it would break silently if either name scheme changed, so
// assert the property directly rather than only through the table.
func TestUpdateAssetSuffixes_MuslNameDoesNotMatchGenericSuffix(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		muslName := "f4-linux-musl-" + arch + ".tar.gz"
		for _, suffix := range assetSuffixes("linux", arch, "") {
			if strings.HasSuffix(muslName, suffix) {
				t.Errorf("glibc suffix %q matches musl asset %q", suffix, muslName)
			}
		}
	}
}

func TestUpdateAssetSuffixes_NonLinuxUnchanged(t *testing.T) {
	// A libc value must not leak into platforms that have no such split.
	if got := assetSuffixes("darwin", "arm64", "musl"); len(got) != 1 || got[0] != "-darwin-arm64.tar.gz" {
		t.Errorf("darwin suffixes = %v, want [-darwin-arm64.tar.gz]", got)
	}
	got := assetSuffixes("windows", "amd64", "")
	want := []string{"-windows-amd64.7z", "-windows-amd64.zip"}
	if len(got) != len(want) {
		t.Fatalf("windows suffixes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("windows suffixes = %v, want %v", got, want)
		}
	}
}

// The lite edition is published under the same GOOS/GOARCH as the regular
// one (f4-lite-linux-amd64.tar.gz next to f4-linux-amd64.tar.gz), and both
// names end with "-linux-amd64.tar.gz". Whichever the release listed first
// used to win, so either edition could update itself into the other.
func TestUpdateAssetSuffixes_EditionsDoNotCross(t *testing.T) {
	assets := []Asset{
		{Name: "f4-lite-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/lite-amd64"},
		{Name: "f4-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/generic-amd64"},
		{Name: "f4-lite-linux-arm.tar.gz", BrowserDownloadURL: "https://example/lite-arm"},
		{Name: "f4-linux-arm.tar.gz", BrowserDownloadURL: "https://example/generic-arm"},
	}
	tests := []struct {
		name    string
		lite    bool
		goarch  string
		assets  []Asset
		wantURL string
	}{
		{name: "regular build skips the lite asset listed first", goarch: "amd64", assets: assets, wantURL: "https://example/generic-amd64"},
		{name: "regular build on arm skips the lite asset", goarch: "arm", assets: assets, wantURL: "https://example/generic-arm"},
		{name: "lite build takes the lite asset", lite: true, goarch: "amd64", assets: assets, wantURL: "https://example/lite-amd64"},
		{name: "lite build on arm takes the lite asset", lite: true, goarch: "arm", assets: assets, wantURL: "https://example/lite-arm"},
		{
			name:   "regular build refuses a lite-only release",
			goarch: "amd64",
			assets: []Asset{{Name: "f4-lite-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/lite-amd64"}},
		},
		{
			name:   "lite build refuses a release without a lite asset",
			lite:   true,
			goarch: "amd64",
			assets: []Asset{{Name: "f4-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/generic-amd64"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, _, kind := pickAsset(tt.assets, editionAssetSuffixes(tt.lite, "linux", tt.goarch, ""))
			if url != tt.wantURL {
				t.Errorf("picked %q, want %q", url, tt.wantURL)
			}
			if tt.wantURL != "" && kind != "targz" {
				t.Errorf("archive kind = %q, want targz", kind)
			}
		})
	}
}

// #1656: a lite Windows .zip is what f4 up to v0.1.3-alpha takes for the
// regular edition's update, so lite is published, and looked for, as a
// .tar.gz on Windows too. A lite build cannot unpack .7z either
// (internal/unpack's formats_lite.go).
func TestUpdateAssetSuffixes_LiteWindowsIsTarGz(t *testing.T) {
	got := editionAssetSuffixes(true, "windows", "amd64", "")
	if len(got) != 1 || got[0] != "-lite-windows-amd64.tar.gz" {
		t.Errorf("lite windows suffixes = %v, want [-lite-windows-amd64.tar.gz]", got)
	}
}

// nightly20260929Assets is the asset list of the nightly of 2026-09-29
// (1c2e8c8) in the order the GitHub API returns it, by name. Since f4#1178
// the plugins are published beside f4, and their names end with the same
// "-<os>-<arch>.tar.gz".
func nightly20260929Assets() []Asset {
	names := []string{
		"android-plugin-darwin-amd64.tar.gz",
		"android-plugin-linux-amd64.tar.gz",
		"android-plugin-linux-arm64.tar.gz",
		"android-plugin-windows-amd64.tar.gz",
		"cloudfox-plugin-darwin-amd64.tar.gz",
		"cloudfox-plugin-linux-amd64.tar.gz",
		"cloudfox-plugin-linux-arm64.tar.gz",
		"cloudfox-plugin-windows-amd64.tar.gz",
		"f4-darwin-amd64.app.zip",
		"f4-darwin-amd64.tar.gz",
		"f4-darwin-arm64.app.zip",
		"f4-darwin-arm64.tar.gz",
		"f4-legacy-windows-386.zip",
		"f4-linux-386.tar.gz",
		"f4-linux-amd64.tar.gz",
		"f4-linux-arm64.tar.gz",
		"f4-linux-musl-amd64.tar.gz",
		"f4-lite-linux-amd64.tar.gz",
		"f4-lite-windows-amd64.zip",
		"f4-termux-arm64.tar.gz",
		"f4-windows-amd64.zip",
		"f4-windows7-amd64.zip",
		"ios-plugin-darwin-amd64.tar.gz",
		"ios-plugin-linux-amd64.tar.gz",
		"ios-plugin-linux-arm64.tar.gz",
		"ios-plugin-windows-amd64.tar.gz",
	}
	assets := make([]Asset, len(names))
	for i, name := range names {
		assets[i] = Asset{Name: name, BrowserDownloadURL: "https://example/" + name}
	}
	return assets
}

// #1656: android-plugin-linux-amd64.tar.gz sorts before f4-linux-amd64.tar.gz
// and ends with the same suffix, so it was what every Linux and macOS updater
// downloaded. The update then "succeeded" without touching f4.
func TestPickAssetTakesF4NotAPluginArchive(t *testing.T) {
	tests := []struct {
		name   string
		lite   bool
		goos   string
		goarch string
		libc   string
		want   string
	}{
		{name: "linux amd64", goos: "linux", goarch: "amd64", want: "f4-linux-amd64.tar.gz"},
		{name: "linux arm64", goos: "linux", goarch: "arm64", want: "f4-linux-arm64.tar.gz"},
		{name: "linux musl", goos: "linux", goarch: "amd64", libc: "musl", want: "f4-linux-musl-amd64.tar.gz"},
		{name: "darwin amd64", goos: "darwin", goarch: "amd64", want: "f4-darwin-amd64.tar.gz"},
		{name: "lite linux", lite: true, goos: "linux", goarch: "amd64", want: "f4-lite-linux-amd64.tar.gz"},
		{name: "termux", goos: "android", goarch: "arm64", want: "f4-termux-arm64.tar.gz"},
		{name: "windows", goos: "windows", goarch: "amd64", want: "f4-windows-amd64.zip"},
		// The legacy build's own archive is named apart, and still found.
		{name: "legacy windows 386", goos: "windows", goarch: "386", want: "f4-legacy-windows-386.zip"},
		// The Windows 7/8/8.1 build asks for its own archive (releaseOS).
		{name: "windows 7", goos: "windows7", goarch: "amd64", want: "f4-windows7-amd64.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, _, _ := pickAsset(nightly20260929Assets(), editionAssetSuffixes(tt.lite, tt.goos, tt.goarch, tt.libc))
			if want := "https://example/" + tt.want; url != want {
				t.Errorf("picked %q, want %q", url, want)
			}
		})
	}
}

// A release whose only matching archive is a plugin's has nothing for f4.
func TestPickAssetRefusesAPluginOnlyRelease(t *testing.T) {
	assets := []Asset{{Name: "cloudfox-plugin-linux-amd64.tar.gz", BrowserDownloadURL: "https://example/cloudfox"}}
	if url, _, _ := pickAsset(assets, assetSuffixes("linux", "amd64", "")); url != "" {
		t.Errorf("picked %q from a release without f4", url)
	}
}

// The regular Windows build does not start on Windows 7/8/8.1, and the
// Windows 7 build is the only one that does: each asks for its own archive,
// and neither name ends with the other's suffix (#1656).
func TestWindows7BuildAsksForItsOwnArchive(t *testing.T) {
	if releaseOS("windows") != "windows" || releaseOS("linux") != "linux" {
		t.Fatalf("releaseOS renames a platform outside the win7 build")
	}
	if got := assetSuffixes("windows7", "amd64", ""); !slices.Equal(got, []string{"-windows7-amd64.7z", "-windows7-amd64.zip"}) {
		t.Errorf("windows7 suffixes = %v", got)
	}
	for _, suffix := range assetSuffixes("windows", "amd64", "") {
		if takesAsset("f4-windows7-amd64.zip", suffix) {
			t.Errorf("the regular Windows build takes the Windows 7 archive for %q", suffix)
		}
	}
	for _, suffix := range assetSuffixes("windows7", "amd64", "") {
		if takesAsset("f4-windows-amd64.zip", suffix) || takesAsset("f4-windows-amd64.7z", suffix) {
			t.Errorf("the Windows 7 build takes the regular archive for %q", suffix)
		}
	}
}
