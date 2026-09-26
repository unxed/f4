package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	colorer "github.com/unxed/colorer4go"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/testutil"
	"github.com/unxed/vtui"
)

type debugLogCapture struct {
	mu    sync.Mutex
	lines []string
}

// captureDebugLog routes vtui.DebugLog into the test for its duration.
func captureDebugLog(t *testing.T) *debugLogCapture {
	t.Helper()
	t.Setenv("VTUI_DEBUG", "test")
	c := &debugLogCapture{}
	restore := vtui.SetTestLogger(func(format string, a ...any) {
		c.mu.Lock()
		c.lines = append(c.lines, fmt.Sprintf(format, a...))
		c.mu.Unlock()
	})
	t.Cleanup(restore)
	return c
}

// has reports whether one logged line contains every one of parts.
func (c *debugLogCapture) has(parts ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
next:
	for _, line := range c.lines {
		for _, p := range parts {
			if !strings.Contains(line, p) {
				continue next
			}
		}
		return true
	}
	return false
}

func (c *debugLogCapture) dump(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range c.lines {
		t.Logf("  %s", line)
	}
}

// minimalColorerConfigs is a catalog Colorer loads without complaint and
// that knows no file types and no colour styles — enough to get a working
// session, and to make any SetHRD call fail.
func minimalColorerConfigs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	catalog := `<?xml version="1.0" encoding="UTF-8"?>
<catalog xmlns="http://colorer.github.io/schema/v1/catalog">
  <hrc-sets/>
  <hrd-sets/>
</catalog>
`
	if err := os.WriteFile(filepath.Join(dir, "base", "catalog.xml"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func pooledColorerSession() *colorer.Session {
	colorerPoolMu.Lock()
	defer colorerPoolMu.Unlock()
	return colorerIdle
}

func TestColorerDiagnosticsLevel(t *testing.T) {
	cases := []struct {
		env     string
		want    colorer.Level
		enabled bool
	}{
		{"", colorer.LevelWarn, true},
		{"off", 0, false},
		{"OFF", 0, false},
		{"error", colorer.LevelError, true},
		{"warning", colorer.LevelWarn, true},
		{"warn", colorer.LevelWarn, true},
		{" info ", colorer.LevelInfo, true},
		{"debug", colorer.LevelDebug, true},
		{"trace", colorer.LevelTrace, true},
		{"nonsense", colorer.LevelWarn, true},
	}
	for _, c := range cases {
		t.Setenv("COLORER_VERBOSE", c.env)
		got, enabled := colorerDiagnosticsLevel()
		if enabled != c.enabled || (enabled && got != c.want) {
			t.Errorf("COLORER_VERBOSE=%q: got %v, %v; want %v, %v", c.env, got, enabled, c.want, c.enabled)
		}
	}
}

// The case issue #306 is about: before this, a session that could not load
// its catalog failed with "wasm error: unreachable" and a list of numbered
// wasm functions, and nothing in debug.log said which file was the problem.
func TestColorer_FailedCatalogIsExplainedInTheDebugLog(t *testing.T) {
	t.Setenv("COLORER_VERBOSE", "")
	logs := captureDebugLog(t)
	configs := t.TempDir() // no base/catalog.xml at all

	session, err := acquireColorerSession(ColorerSource{ConfigsDir: configs})
	if err == nil {
		session.Close()
		t.Fatal("a session started without a catalog")
	}
	var fe *colorer.FatalError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v (%T), want a *colorer.FatalError", err, err)
	}
	if !strings.Contains(fe.Reason, "exception thrown at colorer/") {
		t.Errorf("Reason = %q, want Colorer's throw site", fe.Reason)
	}
	if !logs.has("COLORER: [error]", "/base/catalog.xml") {
		logs.dump(t)
		t.Error("debug.log has no Colorer error naming the catalog")
	}
}

func TestColorer_DiagnosticsCanBeSwitchedOff(t *testing.T) {
	t.Setenv("COLORER_VERBOSE", "off")
	logs := captureDebugLog(t)

	if session, err := acquireColorerSession(ColorerSource{ConfigsDir: t.TempDir()}); err == nil {
		session.Close()
		t.Fatal("a session started without a catalog")
	}
	if logs.has("COLORER: [") {
		logs.dump(t)
		t.Error("Colorer diagnostics were logged with COLORER_VERBOSE=off")
	}
}

func TestColorer_FailedSessionIsNotPooled(t *testing.T) {
	logs := captureDebugLog(t)
	ResetColorerSessions()
	t.Cleanup(ResetColorerSessions)
	configs := minimalColorerConfigs(t)

	healthy, err := acquireColorerSession(ColorerSource{ConfigsDir: configs})
	if err != nil {
		t.Fatalf("acquiring a session on the minimal catalog: %v", err)
	}
	releaseColorerSession(healthy, ColorerSource{ConfigsDir: configs})
	if pooledColorerSession() != healthy {
		t.Fatal("a healthy session was not pooled; the test cannot tell pooling apart from failure")
	}

	failing, err := acquireColorerSession(ColorerSource{ConfigsDir: configs})
	if err != nil {
		t.Fatalf("reacquiring the pooled session: %v", err)
	}
	if err := failing.SetHRD("rgb", "no-such-style"); err == nil {
		t.Fatal("SetHRD accepted a colour style the catalog does not have")
	}
	releaseColorerSession(failing, ColorerSource{ConfigsDir: configs})

	if pooledColorerSession() != nil {
		t.Error("a session whose call failed went back into the pool")
	}
	if !logs.has("COLORER: Closing a failed session", "colorer_set_hrd") {
		logs.dump(t)
		t.Error("closing the failed session was not logged with its cause")
	}
}

func TestColorer_RegionDefineReportsAFailedColourStyle(t *testing.T) {
	logs := captureDebugLog(t)
	ResetColorerSessions()
	t.Cleanup(ResetColorerSessions)
	configs := minimalColorerConfigs(t)

	if rd := colorerGetRegionDefineFor("def:Text", ColorerSource{ConfigsDir: configs}, "no-such-style"); rd != nil {
		t.Errorf("got a region define %+v from a style that does not exist", rd)
	}
	if !logs.has(`COLORER: Cannot read region "def:Text", colour style "no-such-style" failed`, "exception thrown at colorer/") {
		logs.dump(t)
		t.Error("the failed colour style was not logged with Colorer's throw site")
	}
	if pooledColorerSession() != nil {
		t.Error("the session the colour style broke was pooled")
	}
}

// withColorerSource points CurrentColorerSource (what newColorerHighlighter
// reads) at the given catalog and scheme for the test's duration.
func withColorerSource(t *testing.T, configsDir, scheme string) {
	t.Helper()
	prevCatalog := config.App.EditorColorerCatalog
	prevScheme := config.App.EditorColorerScheme
	prevUserHrc := config.App.EditorColorerUserHrc
	prevUserHrd := config.App.EditorColorerUserHrd
	config.App.EditorColorerCatalog = configsDir
	config.App.EditorColorerScheme = scheme
	config.App.EditorColorerUserHrc = ""
	config.App.EditorColorerUserHrd = ""
	t.Cleanup(func() {
		config.App.EditorColorerCatalog = prevCatalog
		config.App.EditorColorerScheme = prevScheme
		config.App.EditorColorerUserHrc = prevUserHrc
		config.App.EditorColorerUserHrd = prevUserHrd
	})
}

// The other half of issue #306: the owner's own trailing note said a scheme
// failure reaching only debug.log, with the editor silently falling back to
// another highlighter, was easy to miss. newColorerHighlighter must also
// raise a toast, in addition to (not instead of) the debug.log entry.
func TestColorer_SchemeFailureAlsoShowsAToast(t *testing.T) {
	logs := captureDebugLog(t)
	ResetColorerSessions()
	t.Cleanup(ResetColorerSessions)

	// minimalColorerConfigs has no hrd-sets at all, so any colour style name
	// fails SetHRD once the session itself has started cleanly.
	withColorerSource(t, minimalColorerConfigs(t), "")

	t.Cleanup(testutil.SwapFrameManager(t))
	vtui.FrameManager.Init(vtui.NewSilentScreenBuf())
	t.Cleanup(colorerSetups.wait)

	previousDuration := colorerSchemeFailureToastDuration
	colorerSchemeFailureToastDuration = 2 * time.Second
	t.Cleanup(func() { colorerSchemeFailureToastDuration = previousDuration })

	stub := &stubHighlighter{}
	ch := newColorerHighlighter(nil, "broken.txt", "", stub)
	t.Cleanup(func() { _ = ch.Close() })

	// The goroutine's own failure handling has already run its toast.Show
	// and useFallback calls by the time this returns; both only queued
	// tasks on the frame manager, so draining it is still needed below.
	colorerSetups.wait()
	testutil.DrainUITasks()

	toastMsg := vtui.FrameManager.GetActiveToast()
	if toastMsg == "" {
		t.Fatal("a Colorer scheme failure did not raise a toast")
	}
	if !strings.Contains(toastMsg, "broken.txt") {
		t.Errorf("toast %q does not name the file the scheme failed for", toastMsg)
	}
	if !logs.has("COLORER: Colour style", "broken.txt") {
		logs.dump(t)
		t.Error("the same failure the toast reported is missing from debug.log")
	}
}
