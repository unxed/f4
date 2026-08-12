package vtvibe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Config is everything vtvibe needs to reach a model. One shape covers Google
// AI Studio, OpenRouter, llama.cpp, LM Studio, Ollama and the rest: they all
// speak the OpenAI chat-completions dialect.
type Config struct {
	BaseURL string
	Model   string
	APIKey  string
	System  string
}

// Message is one chat-completions message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage carries the token counts the endpoint reports back, when it does.
type Usage struct {
	In  int
	Out int
}

var (
	errTooLarge = errors.New("vtvibe: file is too large for the AI panel")
	// ErrNoKey is returned when no API key could be resolved. The host turns
	// it into a localized hint instead of a raw error string.
	ErrNoKey = errors.New("vtvibe: no API key configured")
	// ErrBusy is returned while a request for this session is still running.
	ErrBusy = errors.New("vtvibe: a request is already running")
)

func (c Config) endpoint(suffix string) string {
	return strings.TrimRight(c.BaseURL, "/") + suffix
}

// httpClient has no overall deadline on purpose: long generations are normal.
// Cancellation is the caller's context, which the progress dialog owns.
var httpClient = &http.Client{
	Transport: &http.Transport{
		ResponseHeaderTimeout: 120 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []any     `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Chat sends the whole conversation and returns the reply text.
func (c Config) Chat(ctx context.Context, msgs []Message) (string, Usage, error) {
	if c.APIKey == "" && !isLocal(c.BaseURL) {
		return "", Usage{}, ErrNoKey
	}
	body, err := json.Marshal(chatRequest{Model: c.Model, Messages: msgs})
	if err != nil {
		return "", Usage{}, err
	}

	req := chatRequest{Model: c.Model, Messages: msgs}
	if strings.Contains(strings.ToLower(c.Model), "gemini") {
		// Embed native Gemini search tools directly into OpenAI-compatible payload
		req.Tools = []any{
			// err 400 invalid parameter
			/*
				map[string]any{"google_search": map[string]any{}},
				map[string]any{"url_context": map[string]any{}},
			*/
		}
	}
	body, err = json.Marshal(req)
	if err != nil {
		return "", Usage{}, err
	}

	raw, err := c.post(ctx, c.endpoint("/chat/completions"), body)
	if err != nil {
		return "", Usage{}, err
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", Usage{}, fmt.Errorf("cannot parse the reply: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", Usage{}, errors.New(parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", Usage{}, errors.New("the model returned no answer")
	}
	text := decodeContent(parsed.Choices[0].Message.Content)
	usage := Usage{In: parsed.Usage.PromptTokens, Out: parsed.Usage.CompletionTokens}
	if strings.TrimSpace(text) == "" {
		return "", usage, errors.New("the model returned an empty answer")
	}
	return text, usage, nil
}

// Models lists what the key can actually reach. Model names go stale faster
// than documentation does, so the user needs a way to ask.
func (c Config) Models(ctx context.Context) ([]string, error) {
	if c.APIKey == "" && !isLocal(c.BaseURL) {
		return nil, ErrNoKey
	}
	raw, err := c.get(ctx, c.endpoint("/models"))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, errors.New(parsed.Error.Message)
	}
	out := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		out = append(out, strings.TrimPrefix(m.ID, "models/"))
	}
	return out, nil
}

func (c Config) post(ctx context.Context, url string, body []byte) ([]byte, error) {
	return c.do(ctx, http.MethodPost, url, body)
}

func (c Config) get(ctx context.Context, url string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, url, nil)
}

// do performs the request, retrying only 429 and 5xx: free tiers hand out 429
// as a matter of routine and a single retry usually saves the round trip.
func (c Config) do(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt, lastErr)):
			}
		}
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		httpErr := &statusError{code: resp.StatusCode, retryAfter: resp.Header.Get("Retry-After"), body: data}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = httpErr
			continue
		}
		return nil, httpErr
	}
	return nil, lastErr
}

type statusError struct {
	code       int
	retryAfter string
	body       []byte
}

func (e *statusError) Error() string {
	msg := ""
	var parsed struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(e.body, &parsed) == nil && parsed.Error != nil {
		msg = parsed.Error.Message
	}
	if msg == "" {
		msg = strings.TrimSpace(string(e.body))
	}
	if len(msg) > 400 {
		msg = msg[:400] + "..."
	}
	if msg == "" {
		return fmt.Sprintf("HTTP %d", e.code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.code, msg)
}

func backoff(attempt int, last error) time.Duration {
	var se *statusError
	if errors.As(last, &se) && se.retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(se.retryAfter)); err == nil && secs > 0 {
			if secs > 30 {
				secs = 30
			}
			return time.Duration(secs) * time.Second
		}
	}
	return time.Duration(1<<uint(attempt-1)) * time.Second
}

// decodeContent accepts both shapes seen in the wild: a plain string and an
// array of typed parts.
func decodeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Text)
		}
		return sb.String()
	}
	return ""
}

// isLocal reports whether the endpoint is on this machine, where a key is
// normally not needed (llama.cpp, LM Studio, Ollama).
func isLocal(baseURL string) bool {
	lower := strings.ToLower(baseURL)
	return strings.Contains(lower, "://127.0.0.1") ||
		strings.Contains(lower, "://localhost") ||
		strings.Contains(lower, "://[::1]") ||
		strings.Contains(lower, "://0.0.0.0")
}
