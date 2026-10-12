package vtvibe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// f4#1842, stage H9 item 7: the tools of MCP servers.

// TestHelperMCPServer is not a test: run as a subprocess with
// F4_FAKE_MCP set, it is a tiny MCP server of the era named there.
func TestHelperMCPServer(t *testing.T) {
	era := os.Getenv("F4_FAKE_MCP")
	if era == "" {
		return
	}
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	reply := func(id json.RawMessage, result any, errMsg string) {
		m := map[string]any{"jsonrpc": "2.0", "id": id}
		if errMsg != "" {
			m["error"] = map[string]any{"code": -32601, "message": errMsg}
		} else {
			m["result"] = result
		}
		data, _ := json.Marshal(m)
		_, _ = fmt.Fprintf(out, "%s\n", data)
		_ = out.Flush()
	}
	initialized := false
	for in.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		_ = json.Unmarshal(in.Bytes(), &m)
		_, hasMeta := m.Params["_meta"]
		switch {
		case m.Method == "server/discover" && era == "modern":
			reply(m.ID, map[string]any{"resultType": "complete", "supportedVersions": []string{mcpModernVersion}, "capabilities": map[string]any{"tools": map[string]any{}}}, "")
		case m.Method == "server/discover" && era == "legacy":
			reply(m.ID, nil, "Method not found")
		case m.Method == "server/discover": // "silent": an old server that ignores what it does not know
		case m.Method == "initialize":
			// A notification and a ping before the answer, as servers may.
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","method":"notifications/message","params":{}}`+"\n")
			_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":"s1","method":"ping"}`+"\n")
			_ = out.Flush()
			reply(m.ID, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "fake"}}, "")
		case m.Method == "notifications/initialized":
			initialized = true
		case m.Method == "tools/list":
			if era == "modern" && !hasMeta || era != "modern" && !initialized {
				reply(m.ID, nil, "not ready")
				continue
			}
			if m.Params["cursor"] == nil {
				reply(m.ID, map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"type": "object"}}}, "nextCursor": "2"}, "")
			} else {
				reply(m.ID, map[string]any{"tools": []any{map[string]any{"name": "fail", "description": "Always fails"}}}, "")
			}
		case m.Method == "tools/call":
			args, _ := m.Params["arguments"].(map[string]any)
			if m.Params["name"] == "fail" {
				reply(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": "it failed"}}, "isError": true}, "")
				continue
			}
			reply(m.ID, map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("%s:%v", era, args["text"])}}}, "")
		}
	}
	os.Exit(0)
}

func fakeMCPServer(era string) MCPServer {
	return MCPServer{Command: os.Args[0], Args: []string{"-test.run=^TestHelperMCPServer$"}, Env: map[string]string{"F4_FAKE_MCP": era}}
}

func TestMCPClientSpeaksBothEras(t *testing.T) {
	for _, era := range []string{"modern", "legacy", "silent"} {
		t.Run(era, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := StartMCP(ctx, "my server", fakeMCPServer(era), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if c.modern != (era == "modern") {
				t.Fatalf("modern = %v", c.modern)
			}
			tools, errs := MCPTools(ctx, []*MCPClient{c})
			if len(errs) != 0 || len(tools) != 2 || tools[0].Name != "mcp__my_server__echo" || tools[1].Name != "mcp__my_server__fail" {
				t.Fatalf("tools %v, errors %v", tools, errs)
			}
			out, err := tools[0].Run(ctx, json.RawMessage(`{"text":"hi"}`))
			if want := map[string]string{"modern": "modern:hi", "legacy": "legacy:hi", "silent": "silent:hi"}[era]; err != nil || out != want {
				t.Fatalf("echo: %q, %v", out, err)
			}
			if _, err := tools[1].Run(ctx, nil); err == nil || !strings.Contains(err.Error(), "it failed") {
				t.Fatalf("a failing tool did not fail: %v", err)
			}
		})
	}
}

func TestLoadMCPConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if servers, err := LoadMCPConfig(path); err != nil || servers != nil {
		t.Fatalf("missing file: %v, %v", servers, err)
	}
	data := `{"mcpServers":{"fs":{"command":"npx","args":["-y","server-fs","."],"env":{"A":"1"}},"web":{"type":"http","url":"https://x"},"empty":{}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadMCPConfig(path)
	if err != nil || len(servers) != 1 || servers["fs"].Command != "npx" || servers["fs"].Env["A"] != "1" {
		t.Fatalf("%v, %v", servers, err)
	}
	if got := mcpToolName("a b/c", strings.Repeat("x", 80)); len(got) != 64 || !strings.HasPrefix(got, "mcp__a_b_c__x") {
		t.Fatalf("name %q", got)
	}
}
