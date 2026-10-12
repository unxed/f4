package vtvibe

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Loading a web page (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage H9,
// item 9). Claude Code and OpenCode give their agents a web fetch tool; a
// worker here had only curl through the shell, which pours raw HTML into
// the context. fetch_url returns the page as text: scripts, styles and tags
// go, the text and the link targets stay.

const (
	maxFetchBytes = 2 << 20
	maxFetchText  = 100_000
	fetchTimeout  = time.Minute
)

var (
	htmlDrop    = regexp.MustCompile(`(?is)<(script|style|noscript|svg|head)\b.*?</(script|style|noscript|svg|head)\s*>`)
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlLink    = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a\s*>`)
	htmlBreak   = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr|/h[1-6]|/pre|/blockquote|/section|/article|hr)\b[^>]*>`)
	htmlItem    = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	htmlTag     = regexp.MustCompile(`(?s)<[^>]+>`)
	blankRuns   = regexp.MustCompile(`\n[ \t]*\n(?:[ \t]*\n)+`)
	spaceRuns   = regexp.MustCompile(`[ \t\r\f\v]+`)
)

// htmlToText keeps what a reader of the page sees, and where its links go.
func htmlToText(page string) string {
	page = htmlComment.ReplaceAllString(page, "")
	page = htmlDrop.ReplaceAllString(page, "")
	page = htmlLink.ReplaceAllStringFunc(page, func(a string) string {
		m := htmlLink.FindStringSubmatch(a)
		text := strings.TrimSpace(htmlTag.ReplaceAllString(m[2], ""))
		if text == "" || strings.HasPrefix(m[1], "#") || strings.HasPrefix(strings.ToLower(m[1]), "javascript:") {
			return text
		}
		return text + " (" + m[1] + ")"
	})
	page = htmlBreak.ReplaceAllString(page, "\n")
	page = htmlItem.ReplaceAllString(page, "\n- ")
	page = htmlTag.ReplaceAllString(page, "")
	page = strings.ReplaceAll(html.UnescapeString(page), "\u00a0", " ")
	lines := strings.Split(page, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRuns.ReplaceAllString(l, " "))
	}
	page = strings.Join(lines, "\n")
	return strings.TrimSpace(blankRuns.ReplaceAllString(page, "\n\n"))
}

// FetchURLTool loads an http(s) page and returns it as text.
func FetchURLTool() Tool {
	return Tool{
		Name:        "fetch_url",
		Description: "Load a web page or a text file over http(s) and return it as text (HTML is turned into plain text with link targets kept). For pages, not downloads; at most 100000 characters.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"url": map[string]any{"type": "string"}},
			"required":   []string{"url"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			u := strings.TrimSpace(args.URL)
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return "", fmt.Errorf("only http and https addresses can be loaded, not %q", u)
			}
			ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return "", err
			}
			req.Header.Set("User-Agent", "f4-vtvibe")
			resp, err := httpClient.Do(req)
			if err != nil {
				return "", err
			}
			defer func() { _ = resp.Body.Close() }() // read-only body
			data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
			if err != nil {
				return "", err
			}
			if resp.StatusCode >= 400 {
				return "", fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
			}
			ctype := resp.Header.Get("Content-Type")
			text := string(data)
			if strings.Contains(strings.ToLower(ctype), "html") || strings.HasPrefix(strings.TrimSpace(strings.ToLower(text)), "<!doctype html") {
				text = htmlToText(text)
			} else if !strings.HasPrefix(ctype, "text/") && !strings.Contains(ctype, "json") && !strings.Contains(ctype, "xml") && ctype != "" {
				return "", fmt.Errorf("%s is %s, not a page; download it with the shell if it is needed", u, ctype)
			}
			if r := []rune(text); len(r) > maxFetchText {
				text = string(r[:maxFetchText]) + "\n… (cut)"
			}
			return fmt.Sprintf("%s (%s)\n\n%s", u, ctype, text), nil
		},
	}
}
