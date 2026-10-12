package vtvibe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Streaming answers (unxed/f4#1842, docs/VTVIBE.md § 19a, stage H2): the
// chat shows the answer while the model writes it instead of after the whole
// round trip. ChatStream hands every piece to onDelta and returns the full
// text at the end, exactly as Chat would.

type streamRequest struct {
	Model         string    `json:"model"`
	Messages      []Message `json:"messages"`
	Stream        bool      `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content json.RawMessage `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// errStreamRefused means the service answered the streaming request with an
// error status before sending anything: Chat is tried instead, which also
// retries 429 and 5xx and covers servers that do not stream at all.
var errStreamRefused = errors.New("vtvibe: streaming refused")

// ChatStream is Chat that shows its work: onDelta gets each piece of text as
// it arrives (from the request's goroutine).
func (c Config) ChatStream(ctx context.Context, msgs []Message, onDelta func(string)) (string, Usage, error) {
	if onDelta == nil {
		return c.Chat(ctx, msgs)
	}
	if c.Kind == KindAnthropic {
		return c.chatAnthropicStream(ctx, msgs, onDelta)
	}
	if err := c.checkReady(); err != nil {
		return "", Usage{}, err
	}
	text, usage, err := c.chatOpenAIStream(ctx, msgs, onDelta)
	if errors.Is(err, errStreamRefused) {
		return c.Chat(ctx, msgs)
	}
	return text, usage, err
}

func (c Config) chatOpenAIStream(ctx context.Context, msgs []Message, onDelta func(string)) (string, Usage, error) {
	req := streamRequest{Model: c.Model, Messages: msgs, Stream: true}
	req.StreamOptions.IncludeUsage = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", Usage{}, ctx.Err()
		}
		return "", Usage{}, errStreamRefused
	}
	defer func() { _ = resp.Body.Close() }() // Read-only body; nothing to report.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", Usage{}, errStreamRefused
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		// A server that ignores "stream" sends the ordinary JSON answer.
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			return "", Usage{}, err
		}
		return parseChatResponse(data, onDelta)
	}

	var text strings.Builder
	var usage Usage
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // keep-alives and comments some proxies insert
		}
		if chunk.Error != nil && chunk.Error.text() != "" {
			return "", usage, errors.New(chunk.Error.text())
		}
		if chunk.Usage != nil {
			usage = Usage{In: chunk.Usage.PromptTokens, Out: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if piece := decodeContent(choice.Delta.Content); piece != "" {
				text.WriteString(piece)
				onDelta(piece)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return "", usage, ctx.Err()
		}
		return "", usage, fmt.Errorf("the answer stream broke off: %w", err)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", usage, errors.New("the model returned an empty answer")
	}
	return text.String(), usage, nil
}

// parseChatResponse reads a non-streamed answer and hands it over whole.
func parseChatResponse(raw []byte, onDelta func(string)) (string, Usage, error) {
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", Usage{}, fmt.Errorf("cannot parse the reply: %w", err)
	}
	if parsed.Error != nil && parsed.Error.text() != "" {
		return "", Usage{}, errors.New(parsed.Error.text())
	}
	if len(parsed.Choices) == 0 {
		return "", Usage{}, errors.New("the model returned no answer")
	}
	text := decodeContent(parsed.Choices[0].Message.Content)
	usage := Usage{In: parsed.Usage.PromptTokens, Out: parsed.Usage.CompletionTokens}
	if strings.TrimSpace(text) == "" {
		return "", usage, errors.New("the model returned an empty answer")
	}
	onDelta(text)
	return text, usage, nil
}

// chatAnthropicStream streams through the SDK and keeps the whole message
// with Accumulate, so the usage and a refusal are read as Chat reads them.
func (c Config) chatAnthropicStream(ctx context.Context, msgs []Message, onDelta func(string)) (string, Usage, error) {
	if err := c.checkReady(); err != nil {
		return "", Usage{}, err
	}
	client := c.anthropicClient()
	stream := client.Beta.Messages.NewStreaming(ctx, c.anthropicParams(msgs))
	defer func() { _ = stream.Close() }() // The stream is drained or abandoned on error.
	reply := anthropic.BetaMessage{}
	for stream.Next() {
		event := stream.Current()
		if err := reply.Accumulate(event); err != nil {
			return "", Usage{}, err
		}
		if delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok && text.Text != "" {
				onDelta(text.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return "", Usage{}, err
	}
	return anthropicReplyText(reply)
}
