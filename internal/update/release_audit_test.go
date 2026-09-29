package update

import (
	"archive/zip"
	"bytes"
	"slices"
	"strings"
	"testing"
)

// nightly20260929 is every asset of the nightly of 2026-09-29 (1c2e8c8), the
// release #1656 was reported against.
const nightly20260929 = `android-plugin-darwin-amd64.tar.gz android-plugin-linux-amd64.tar.gz
android-plugin-linux-arm64.tar.gz android-plugin-windows-amd64.tar.gz cloudfox-plugin-darwin-amd64.tar.gz
cloudfox-plugin-linux-amd64.tar.gz cloudfox-plugin-linux-arm64.tar.gz cloudfox-plugin-windows-amd64.tar.gz
f4-darwin-amd64.app.zip f4-darwin-amd64.tar.gz f4-darwin-arm64.app.zip f4-darwin-arm64.tar.gz
f4-dragonfly-amd64.tar.gz f4-freebsd-amd64.tar.gz f4-freebsd-arm64.tar.gz f4-illumos-amd64.tar.gz
f4-legacy-windows-386.zip f4-linux-386.tar.gz f4-linux-amd64.tar.gz f4-linux-arm.tar.gz
f4-linux-arm64.tar.gz f4-linux-loong64.tar.gz f4-linux-mips.tar.gz f4-linux-mips64.tar.gz
f4-linux-mips64le.tar.gz f4-linux-mipsle.tar.gz f4-linux-ppc64.tar.gz f4-linux-ppc64le.tar.gz
f4-linux-riscv64.tar.gz f4-lite-linux-amd64.tar.gz f4-lite-linux-arm.tar.gz f4-lite-linux-mipsle.tar.gz
f4-lite-windows-amd64.zip f4-netbsd-amd64.tar.gz f4-netbsd-arm64.tar.gz f4-openbsd-amd64.tar.gz
f4-openbsd-arm64.tar.gz f4-solaris-amd64.tar.gz f4-termux-arm.deb f4-termux-arm.tar.gz
f4-termux-arm64.deb f4-termux-arm64.tar.gz f4-windows-amd64.zip f4-windows-arm64.zip
f4-windows7-amd64.zip ios-plugin-darwin-amd64.tar.gz ios-plugin-linux-amd64.tar.gz
ios-plugin-linux-arm64.tar.gz ios-plugin-windows-amd64.tar.gz`

// fixedLayout is the same release as the #1656 fixes publish it: plugins as
// .tgz, lite Windows as .tar.gz, regular Windows as .7z as well.
func fixedLayout() []string {
	var names []string
	for _, name := range strings.Fields(nightly20260929) {
		switch {
		case strings.Contains(name, "-plugin-"):
			name = strings.TrimSuffix(name, ".tar.gz") + ".tgz"
		case name == "f4-lite-windows-amd64.zip":
			name = "f4-lite-windows-amd64.tar.gz"
		case name == "f4-windows-amd64.zip" || name == "f4-windows-arm64.zip":
			names = append(names, strings.TrimSuffix(name, ".zip")+".7z")
		}
		names = append(names, name)
	}
	return names
}

func TestAuditReleaseAcceptsTheFixedLayout(t *testing.T) {
	audit := AuditRelease(fixedLayout())
	for _, p := range audit.Problems {
		t.Error(p)
	}
	want := map[string]ReleaseArchive{
		"f4-linux-amd64.tar.gz":        {Name: "f4-linux-amd64.tar.gz", Kind: "targz", Executable: "f4"},
		"f4-windows-amd64.7z":          {Name: "f4-windows-amd64.7z", Kind: "7z", Executable: "f4.exe"},
		"f4-windows-amd64.zip":         {Name: "f4-windows-amd64.zip", Kind: "zip", Executable: "f4.exe"},
		"f4-lite-windows-amd64.tar.gz": {Name: "f4-lite-windows-amd64.tar.gz", Kind: "targz", Executable: "f4.exe"},
		"f4-legacy-windows-386.zip":    {Name: "f4-legacy-windows-386.zip", Kind: "zip", Executable: "f4-legacy.exe"},
		"f4-windows7-amd64.zip":        {Name: "f4-windows7-amd64.zip", Kind: "zip", Executable: "f4.exe"},
		"f4-termux-arm64.tar.gz":       {Name: "f4-termux-arm64.tar.gz", Kind: "targz", Executable: "f4"},
	}
	for _, a := range audit.Archives {
		if w, ok := want[a.Name]; ok {
			if a != w {
				t.Errorf("archive %+v, want %+v", a, w)
			}
			delete(want, a.Name)
		}
		if strings.Contains(a.Name, "plugin") || strings.HasSuffix(a.Name, ".app.zip") || strings.HasSuffix(a.Name, ".deb") {
			t.Errorf("%s is listed as an archive of f4", a.Name)
		}
	}
	for name := range want {
		t.Errorf("%s is not among the archives to check", name)
	}
}

// The release #1656 was reported against, as the check would have seen it.
func TestAuditReleaseRefusesTheNightlyOf20260929(t *testing.T) {
	problems := strings.Join(AuditRelease(strings.Fields(nightly20260929)).Problems, "\n")
	for _, want := range []string{
		"f4 v0.2.0-beta to v0.3.0-beta and nightlies before 2026-09-27 on linux/amd64 would install android-plugin-linux-amd64.tar.gz instead of f4-linux-amd64.tar.gz",
		"f4 nightlies from 2026-09-27 to the #1656 fix on darwin/amd64 would install android-plugin-darwin-amd64.tar.gz",
		"f4 v0.2.0-beta to v0.3.0-beta and nightlies before 2026-09-27 on windows/amd64 would install f4-lite-windows-amd64.zip instead of f4-windows-amd64.zip",
		"f4 v0.1.2-alpha and v0.1.3-alpha on windows/amd64 would install f4-lite-windows-amd64.zip",
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("problems do not include %q:\n%s", want, problems)
		}
	}
}

// Losing an archive is the other way to leave installed builds behind: they
// would say "no suitable build" from then on.
func TestAuditReleaseRefusesAMissingArchive(t *testing.T) {
	names := slices.DeleteFunc(fixedLayout(), func(n string) bool { return n == "f4-linux-amd64.tar.gz" })
	problems := strings.Join(AuditRelease(names).Problems, "\n")
	if !strings.Contains(problems, "no archive of f4 for linux/amd64") {
		t.Errorf("a release without f4-linux-amd64.tar.gz passed:\n%s", problems)
	}

	names = slices.DeleteFunc(fixedLayout(), func(n string) bool { return n == "f4-windows-amd64.zip" })
	problems = strings.Join(AuditRelease(names).Problems, "\n")
	if !strings.Contains(problems, "f4 v0.1.2-alpha and v0.1.3-alpha on windows/amd64 would install nothing") {
		t.Errorf("a release without the Windows .zip passed, though v0.1.x reads only that:\n%s", problems)
	}
}

// A platform published but not listed in installedFlavors is still checked.
func TestAuditReleaseChecksAPlatformItWasNotToldAbout(t *testing.T) {
	names := append(fixedLayout(), "f4-linux-s390x.tar.gz", "android-plugin-linux-s390x.tar.gz")
	problems := strings.Join(AuditRelease(names).Problems, "\n")
	if !strings.Contains(problems, "on linux/s390x would install android-plugin-linux-s390x.tar.gz") {
		t.Errorf("an unlisted platform was not checked:\n%s", problems)
	}
}

func TestPublishedFlavor(t *testing.T) {
	tests := map[string]string{
		"f4-linux-amd64.tar.gz":        "linux/amd64",
		"f4-linux-musl-arm64.tar.gz":   "linux/arm64 (musl)",
		"f4-lite-windows-amd64.tar.gz": "windows/amd64 lite",
		"f4-termux-arm.tar.gz":         "android/arm",
		"f4-legacy-windows-386.zip":    "windows/386",
		"f4-windows-arm64.7z":          "windows/arm64",
		"f4-darwin-arm64.app.zip":      "",
		"f4-termux-arm64.deb":          "",
		"f4-windows7-amd64.zip":        "windows7/amd64",
		"ios-plugin-linux-amd64.tgz":   "",
	}
	for name, want := range tests {
		got := ""
		if f, ok := publishedFlavor(name); ok {
			got = f.String()
		}
		if got != want {
			t.Errorf("publishedFlavor(%q) = %q, want %q", name, got, want)
		}
	}
}

// The release check unpacks what the updaters would install with the
// updater's own code, so an archive that does not hold the executable at its
// root is refused before it is published.
func TestCheckReleaseArchive(t *testing.T) {
	linux := ReleaseArchive{Name: "f4-linux-amd64.tar.gz", Kind: "targz", Executable: "f4"}
	if err := CheckReleaseArchive(targz(t, map[string]string{"f4": "f4", "lang/en.lng": "x"}), linux); err != nil {
		t.Errorf("a good archive was refused: %v", err)
	}
	if err := CheckReleaseArchive(targz(t, map[string]string{"build/f4": "f4"}), linux); err == nil {
		t.Error("an archive with f4 one directory down was accepted")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("f4.exe")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("f4"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	windows := ReleaseArchive{Name: "f4-windows-amd64.zip", Kind: "zip", Executable: "f4.exe"}
	if err := CheckReleaseArchive(buf.Bytes(), windows); err != nil {
		t.Errorf("a good Windows archive was refused: %v", err)
	}
	if err := CheckReleaseArchive(buf.Bytes(), ReleaseArchive{Name: "f4-legacy-windows-386.zip", Kind: "zip", Executable: "f4-legacy.exe"}); err == nil {
		t.Error("an archive without f4-legacy.exe was accepted for the legacy build")
	}
}
