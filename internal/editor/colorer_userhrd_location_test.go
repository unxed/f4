package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A <location link> inside a user's own hrd-sets file resolves relative to
// that file, not to catalog.xml, which lives in an entirely different
// directory the user's file has no reason to know about (#277, reported by
// montoner0).
func TestMaterializeUserHRDPathRewritesRelativeLink(t *testing.T) {
	userDir := t.TempDir()
	configsDir := t.TempDir()

	leaf := "<hrd xmlns=\"http://colorer.sf.net/2003/hrd\" class=\"rgb\" name=\"leaf\" description=\"leaf\"></hrd>"
	if err := os.MkdirAll(filepath.Join(userDir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "sub", "black.hrd"), []byte(leaf), 0600); err != nil {
		t.Fatal(err)
	}

	top := "<hrd-sets>\n" +
		"    <hrd class=\"rgb\" name=\"Mine\" description=\"Mine\">\n" +
		"        <location link=\"sub/black.hrd\"/>\n" +
		"    </hrd>\n" +
		"</hrd-sets>\n"
	topPath := filepath.Join(userDir, "mystyles.xml")
	if err := os.WriteFile(topPath, []byte(top), 0600); err != nil {
		t.Fatal(err)
	}

	got := materializeUserHRDPath(configsDir, topPath)
	if got == topPath {
		t.Fatalf("materializeUserHRDPath did not rewrite %q", topPath)
	}

	wantTop := filepath.Join(configsDir, "base", colorerUserHRDCacheDirName, "top-mystyles.xml")
	if got != wantTop {
		t.Fatalf("materializeUserHRDPath(_, %q) = %q, want %q", topPath, got, wantTop)
	}

	rewritten, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), "sub/black.hrd") {
		t.Errorf("rewritten file still names the original relative link: %s", rewritten)
	}
	wantLink := colorerUserHRDCacheDirName + "/link1.hrd"
	if !strings.Contains(string(rewritten), wantLink) {
		t.Errorf("rewritten file does not point at %q:\n%s", wantLink, rewritten)
	}

	copied, err := os.ReadFile(filepath.Join(configsDir, "base", colorerUserHRDCacheDirName, "link1.hrd"))
	if err != nil {
		t.Fatalf("the linked file was not copied into the cache: %v", err)
	}
	if string(copied) != leaf {
		t.Errorf("copied file content = %q, want %q", copied, leaf)
	}
}

// A link naming an XML entity such as &hrd; only catalog.xml's own DOCTYPE
// defines, so it already means "resolve me against the catalog" and must
// keep doing exactly that.
func TestMaterializeUserHRDPathLeavesEntityLinkAlone(t *testing.T) {
	userDir := t.TempDir()
	configsDir := t.TempDir()

	top := "<hrd-sets>\n" +
		"    <hrd class=\"rgb\" name=\"Mine\" description=\"Mine\">\n" +
		"        <location link=\"&hrd;/rgb/dark.hrd\"/>\n" +
		"    </hrd>\n" +
		"</hrd-sets>\n"
	topPath := filepath.Join(userDir, "mystyles.xml")
	if err := os.WriteFile(topPath, []byte(top), 0600); err != nil {
		t.Fatal(err)
	}

	if got := materializeUserHRDPath(configsDir, topPath); got != topPath {
		t.Errorf("materializeUserHRDPath(_, %q) = %q, want it left unchanged", topPath, got)
	}
}

// A folder of standalone .hrd files, each naming itself in its own root
// element, has no <location> indirection to fix.
func TestMaterializeUserHRDPathLeavesFolderAlone(t *testing.T) {
	userDir := t.TempDir()
	configsDir := t.TempDir()

	if got := materializeUserHRDPath(configsDir, userDir); got != userDir {
		t.Errorf("materializeUserHRDPath(_, %q) = %q, want it left unchanged", userDir, got)
	}
}

// A link Colorer itself would fail to resolve -- its target does not exist
// -- is left exactly as written, so the failure is Colorer's own to report,
// not silently swallowed here.
func TestMaterializeUserHRDPathLeavesMissingTargetAlone(t *testing.T) {
	userDir := t.TempDir()
	configsDir := t.TempDir()

	top := "<hrd-sets>\n" +
		"    <hrd class=\"rgb\" name=\"Mine\" description=\"Mine\">\n" +
		"        <location link=\"missing.hrd\"/>\n" +
		"    </hrd>\n" +
		"</hrd-sets>\n"
	topPath := filepath.Join(userDir, "mystyles.xml")
	if err := os.WriteFile(topPath, []byte(top), 0600); err != nil {
		t.Fatal(err)
	}

	if got := materializeUserHRDPath(configsDir, topPath); got != topPath {
		t.Errorf("materializeUserHRDPath(_, %q) = %q, want it left unchanged", topPath, got)
	}
}
