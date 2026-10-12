package vtvibe

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// f4#1842, stage H9 item 9: a web page as text.

func TestFetchURLTurnsAPageIntoText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>T</title><style>p{}</style></head><body>
<script>alert(1)</script><h1>Release&nbsp;notes</h1><p>Fixed <b>two</b> bugs.</p><!-- hidden -->
<ul><li>one</li><li>two</li></ul><a href="https://example.org/x">details</a> <a href="#top">top</a></body></html>`)
		case "/data.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"a":1}`)
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(w, "\x00\x01")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tool := FetchURLTool()
	out, err := runTool(t, tool, map[string]any{"url": srv.URL + "/page"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Release notes", "Fixed two bugs.", "- one", "- two", "details (https://example.org/x)", "top"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in %q", want, out)
		}
	}
	for _, bad := range []string{"alert", "<b>", "hidden", "p{}", "(#top)"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q left in %q", bad, out)
		}
	}
	if out, err := runTool(t, tool, map[string]any{"url": srv.URL + "/data.json"}); err != nil || !strings.Contains(out, `{"a":1}`) {
		t.Fatalf("json: %q, %v", out, err)
	}
	if _, err := runTool(t, tool, map[string]any{"url": srv.URL + "/bin"}); err == nil {
		t.Fatal("a binary download was returned as a page")
	}
	if _, err := runTool(t, tool, map[string]any{"url": srv.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing page: %v", err)
	}
	if _, err := runTool(t, tool, map[string]any{"url": "file:///etc/passwd"}); err == nil {
		t.Fatal("a file: address was loaded")
	}
}
