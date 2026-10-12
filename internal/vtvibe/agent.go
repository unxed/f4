package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The agent loop (unxed/f4#1842, docs/VTVIBE.md § 19a, step B1). Chat sends
// one request and returns text; an agent may answer with tool calls instead,
// which are run here and handed back until the model answers with text. The
// protocol is the chat-completions one Config already speaks: "tools" in the
// request, "tool_calls" in the reply, a "tool" message per result.

// Tool is something the model may call. Run gets the raw JSON arguments the
// model produced and returns the text the model sees as the result; an error
// is reported to the model as the result too, so it can correct itself.
type Tool struct {
	Name        string
	Description string
	// Parameters is the JSON Schema of the arguments object.
	Parameters map[string]any
	Run        func(ctx context.Context, args json.RawMessage) (string, error)
}

// AgentStep describes one tool call, for the caller's log.
type AgentStep struct {
	Tool   string
	Args   string
	Result string
	Err    error
}

// AgentOptions bounds and observes a run.
type AgentOptions struct {
	// MaxSteps caps the number of model requests; 0 means DefaultAgentSteps.
	MaxSteps int
	// MaxToolOutput caps one tool result handed back to the model, in bytes;
	// 0 means DefaultToolOutput. The tail is kept: errors and summaries end
	// command output.
	MaxToolOutput int
	OnStep        func(AgentStep)
}

const (
	DefaultAgentSteps = 50
	DefaultToolOutput = 32 << 10
)

// ErrAgentSteps is returned when the model keeps calling tools past MaxSteps.
var ErrAgentSteps = errors.New("vtvibe: the agent did not finish within its step limit")

type agentMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content"`
	ToolCalls  []agentToolUse `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	// Images go with a user message as pictures (view_image).
	Images []Image `json:"-"`
}

// MarshalJSON writes the message; with pictures the content is a list of
// parts, as for Message.
func (m agentMessage) MarshalJSON() ([]byte, error) {
	type plain agentMessage
	if len(m.Images) == 0 {
		return json.Marshal(plain(m))
	}
	text := ""
	if m.Content != nil {
		text = *m.Content
	}
	parts, err := json.Marshal(Message{Role: m.Role, Content: text, Images: m.Images})
	if err != nil {
		return nil, err
	}
	var withParts struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(parts, &withParts); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []agentToolUse  `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}{m.Role, withParts.Content, m.ToolCalls, m.ToolCallID})
}

type agentToolUse struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type agentToolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type agentRequest struct {
	Model    string          `json:"model"`
	Messages []agentMessage  `json:"messages"`
	Tools    []agentToolSpec `json:"tools,omitempty"`
}

type agentResponse struct {
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []agentToolUse  `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

func textPtr(s string) *string { return &s }

// RunAgent runs msgs through the model, executing the tool calls it makes,
// until it answers with text. It returns that text and the summed usage.
func (c Config) RunAgent(ctx context.Context, msgs []Message, tools []Tool, opts AgentOptions) (string, Usage, error) {
	if err := c.checkReady(); err != nil {
		return "", Usage{}, err
	}
	maxSteps := opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultAgentSteps
	}
	if c.Kind == KindAnthropic {
		return c.runAgentAnthropic(ctx, msgs, tools, opts, maxSteps)
	}
	byName := make(map[string]Tool, len(tools))
	specs := make([]agentToolSpec, 0, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
		var spec agentToolSpec
		spec.Type = "function"
		spec.Function.Name = t.Name
		spec.Function.Description = t.Description
		spec.Function.Parameters = t.Parameters
		if spec.Function.Parameters == nil {
			spec.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		specs = append(specs, spec)
	}
	history := make([]agentMessage, 0, len(msgs)+8)
	for _, m := range msgs {
		history = append(history, agentMessage{Role: m.Role, Content: textPtr(m.Content)})
	}

	var usage Usage
	for step := 0; step < maxSteps; step++ {
		body, err := json.Marshal(agentRequest{Model: c.Model, Messages: history, Tools: specs})
		if err != nil {
			return "", usage, err
		}
		raw, err := c.post(ctx, c.endpoint("/chat/completions"), body)
		if n := len(history); err != nil && n > 0 && len(history[n-1].Images) > 0 && !contextExhausted(err) && ctx.Err() == nil {
			// The model may not take pictures: once more without them.
			history[n-1].Content, history[n-1].Images = textPtr(imagesRefusedText(history[n-1].Images)), nil
			if body, err = json.Marshal(agentRequest{Model: c.Model, Messages: history, Tools: specs}); err == nil {
				raw, err = c.post(ctx, c.endpoint("/chat/completions"), body)
			}
		}
		if err != nil {
			return "", usage, err
		}
		var parsed agentResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", usage, fmt.Errorf("cannot parse the reply: %w", err)
		}
		if parsed.Error != nil && parsed.Error.text() != "" {
			return "", usage, errors.New(parsed.Error.text())
		}
		usage.In += parsed.Usage.PromptTokens
		usage.Out += parsed.Usage.CompletionTokens
		if len(parsed.Choices) == 0 {
			return "", usage, errors.New("the model returned no answer")
		}
		reply := parsed.Choices[0].Message
		text := decodeContent(reply.Content)
		if len(reply.ToolCalls) == 0 {
			if strings.TrimSpace(text) == "" {
				return "", usage, errors.New("the model returned an empty answer")
			}
			return text, usage, nil
		}
		history = append(history, agentMessage{Role: "assistant", Content: textPtr(text), ToolCalls: reply.ToolCalls})
		toolCtx, sink := withImageSink(ctx)
		for _, call := range reply.ToolCalls {
			result, runErr := c.runTool(toolCtx, byName, call)
			if opts.OnStep != nil {
				opts.OnStep(AgentStep{Tool: call.Function.Name, Args: call.Function.Arguments, Result: result, Err: runErr})
			}
			if runErr != nil {
				result = "error: " + runErr.Error() + "\n" + result
			}
			history = append(history, agentMessage{Role: "tool", ToolCallID: call.ID,
				Content: textPtr(tailBytes(result, opts.MaxToolOutput))})
		}
		if imgs := sink.take(); len(imgs) > 0 {
			history = append(history, agentMessage{Role: "user", Content: textPtr(imagesShownText(imgs)), Images: imgs})
		}
		if err := ctx.Err(); err != nil {
			return "", usage, err
		}
	}
	return "", usage, ErrAgentSteps
}

func (c Config) runTool(ctx context.Context, byName map[string]Tool, call agentToolUse) (string, error) {
	tool, ok := byName[call.Function.Name]
	if !ok || tool.Run == nil {
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	args := json.RawMessage(call.Function.Arguments)
	if strings.TrimSpace(call.Function.Arguments) == "" {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return "", fmt.Errorf("arguments of %s are not valid JSON", call.Function.Name)
	}
	return tool.Run(ctx, args)
}

// tailBytes keeps the last limit bytes of s, cut on a line start when one is
// near, and says how much was dropped.
func tailBytes(s string, limit int) string {
	if limit <= 0 {
		limit = DefaultToolOutput
	}
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	if nl := strings.IndexByte(s[cut:], '\n'); nl >= 0 && nl < 256 {
		cut += nl + 1
	}
	return fmt.Sprintf("[%d bytes of earlier output omitted]\n", cut) + strings.ToValidUTF8(s[cut:], "")
}
