package vtvibe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// MCP servers (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage H9, item 7).
// Claude Code, OpenCode and Cursor CLI give their agents the tools of MCP
// servers; workers here get them too. This is a client of the stdio
// transport only: the server is a subprocess speaking newline-delimited
// JSON-RPC on its standard streams. It speaks both eras of the protocol:
// it first probes with server/discover (revision 2026-07-28, stateless,
// every request carries its _meta) and falls back to initialize (the
// earlier, session-based revisions) when the server answers with an error
// or not at all, as the specification asks a dual-era client to do.

const (
	mcpModernVersion = "2026-07-28"
	mcpLegacyVersion = "2025-11-25"
	mcpProbeTimeout  = 5 * time.Second
	mcpStartTimeout  = 30 * time.Second
	mcpCallTimeout   = 10 * time.Minute
)

// MCPServer is one entry of the configuration, in the format Claude Code
// and others use: {"mcpServers": {"name": {"command", "args", "env"}}}.
type MCPServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// LoadMCPConfig reads the servers from path; none when the file is missing.
// Servers of another transport than stdio are left out.
func LoadMCPConfig(path string) (map[string]MCPServer, error) {
	data, err := os.ReadFile(path) // #nosec G304 G703 -- f4's own configuration file
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Servers map[string]MCPServer `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]MCPServer{}
	for name, s := range cfg.Servers {
		if (s.Type == "" || s.Type == "stdio") && s.Command != "" {
			out[name] = s
		}
	}
	return out, nil
}

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// MCPClient talks to one server.
type MCPClient struct {
	Name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan []byte
	mu     sync.Mutex // one request at a time
	nextID int
	modern bool
}

// StartMCP launches server name in dir and opens the conversation.
func StartMCP(ctx context.Context, name string, s MCPServer, dir string) (*MCPClient, error) {
	cmd := exec.Command(s.Command, s.Args...) // #nosec G204 -- a server the user configured in ai/mcp.json
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range s.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("MCP server %s: %w", name, err)
	}
	c := &MCPClient{Name: name, cmd: cmd, stdin: stdin, lines: make(chan []byte, 16)}
	go func() {
		r := bufio.NewReaderSize(stdout, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				c.lines <- line
			}
			if err != nil {
				close(c.lines)
				return
			}
		}
	}()
	if err := c.open(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("MCP server %s: %w", name, err)
	}
	return c, nil
}

func (c *MCPClient) open(ctx context.Context) error {
	probe, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	var discovered struct {
		SupportedVersions []string `json:"supportedVersions"`
	}
	err := c.request(probe, "server/discover", map[string]any{"_meta": c.meta()}, &discovered)
	cancel()
	if err == nil {
		for _, v := range discovered.SupportedVersions {
			if v == mcpModernVersion {
				c.modern = true
				return nil
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	start, cancel := context.WithTimeout(ctx, mcpStartTimeout)
	defer cancel()
	err = c.request(start, "initialize", map[string]any{
		"protocolVersion": mcpLegacyVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "f4", "version": "1"},
	}, nil)
	if err != nil {
		return err
	}
	return c.send(mcpMessage{JSONRPC: "2.0", Method: "notifications/initialized"})
}

func (c *MCPClient) meta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion":    mcpModernVersion,
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "f4", "version": "1"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	}
}

func (c *MCPClient) send(m mcpMessage) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// request sends method and waits for its response, answering the server's
// own requests on the way (a legacy server may ping).
func (c *MCPClient) request(ctx context.Context, method string, params map[string]any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.modern {
		params["_meta"] = c.meta()
	}
	c.nextID++
	id := json.RawMessage(fmt.Sprintf("%d", c.nextID))
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := c.send(mcpMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-c.lines:
			if !ok {
				return errors.New("the server closed its output")
			}
			var m mcpMessage
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			if m.Method != "" {
				if len(m.ID) > 0 {
					c.answer(m)
				}
				continue // a notification
			}
			if string(m.ID) != string(id) {
				continue
			}
			if m.Error != nil {
				return fmt.Errorf("%s: %s (%d)", method, m.Error.Message, m.Error.Code)
			}
			if result != nil {
				return json.Unmarshal(m.Result, result)
			}
			return nil
		}
	}
}

func (c *MCPClient) answer(m mcpMessage) {
	if m.Method == "ping" {
		_ = c.send(mcpMessage{JSONRPC: "2.0", ID: m.ID, Result: json.RawMessage(`{}`)})
		return
	}
	reply := mcpMessage{JSONRPC: "2.0", ID: m.ID}
	reply.Error = &struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{-32601, "f4 does not support " + m.Method}
	_ = c.send(reply)
}

// MCPToolInfo is one tool a server offers.
type MCPToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ListTools returns the server's tools, all pages.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolInfo, error) {
	var all []MCPToolInfo
	cursor := ""
	for page := 0; page < 50; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Tools      []MCPToolInfo `json:"tools"`
			NextCursor string        `json:"nextCursor"`
		}
		if err := c.request(ctx, "tools/list", params, &res); err != nil {
			return all, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// Call runs tool with args and returns its text content.
func (c *MCPClient) Call(ctx context.Context, tool string, args json.RawMessage) (string, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		arguments = args
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	ctx, cancel := context.WithTimeout(ctx, mcpCallTimeout)
	defer cancel()
	if err := c.request(ctx, "tools/call", map[string]any{"name": tool, "arguments": arguments}, &res); err != nil {
		return "", err
	}
	var parts []string
	for _, item := range res.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		} else {
			parts = append(parts, "["+item.Type+" content]")
		}
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// Close ends the server.
func (c *MCPClient) Close() {
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() { _ = c.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
}

var mcpNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// mcpToolName is the name a server's tool goes by among the agent's tools:
// mcp__server__tool, as Claude Code names them, at most 64 characters.
func mcpToolName(server, tool string) string {
	name := "mcp__" + mcpNameChars.ReplaceAllString(server, "_") + "__" + mcpNameChars.ReplaceAllString(tool, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// MCPTools makes the tools of clients into agent tools.
func MCPTools(ctx context.Context, clients []*MCPClient) ([]Tool, []error) {
	var tools []Tool
	var errs []error
	sort.Slice(clients, func(i, j int) bool { return clients[i].Name < clients[j].Name })
	for _, c := range clients {
		list, err := c.ListTools(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("MCP server %s: %w", c.Name, err))
		}
		for _, info := range list {
			client, name := c, info.Name
			schema := info.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			tools = append(tools, Tool{
				Name:        mcpToolName(c.Name, info.Name),
				Description: fmt.Sprintf("[MCP server %s] %s", c.Name, info.Description),
				Parameters:  schema,
				Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
					return client.Call(ctx, name, raw)
				},
			})
		}
	}
	return tools, errs
}
