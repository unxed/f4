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
	// Kind is the wire protocol: "" for OpenAI chat-completions, KindAnthropic
	// for the Anthropic Messages API.
	Kind    string
	BaseURL string
	Model   string
	APIKey  string
	System  string
	// ToolEnv is added to the environment of the commands the agent's tools
	// run, as NAME=value (the GitHub token, github.go); never sent anywhere.
	ToolEnv []string
}

// Message is one chat-completions message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images go with a user message as pictures (image.go).
	Images []Image `json:"-"`
}

// Usage carries the token counts the endpoint reports back, when it does.
type Usage struct {
	In  int `json:"in"`
	Out int `json:"out"`
}

var (
	errTooLarge = errors.New("vtvibe: file is too large for the AI panel")
	// ErrNoKey is returned when no API key could be resolved. The host turns
	// it into a localized hint instead of a raw error string.
	ErrNoKey = errors.New("vtvibe: no API key configured")
	// ErrNoModel is returned when no model is set and the service has no
	// default one, as with a custom address (f4#1842: a request with an
	// empty model came back as MODEL_NOT_FOUND).
	ErrNoModel = errors.New("vtvibe: no model chosen")
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
	// Code is a number for some services and a string for others; it is
	// what is left to show when the message is empty (bothub:
	// {"code":"MODEL_NOT_FOUND","message":""}).
	Code json.RawMessage `json:"code"`
}

// text is the message, or the code when the service sent no message.
func (e *apiError) text() string {
	if e.Message != "" {
		return e.Message
	}
	code := strings.Trim(strings.TrimSpace(string(e.Code)), `"`)
	if code == "null" {
		return ""
	}
	return code
}

// HTTPStatus is the HTTP status of a request that failed with one, or 0.
func HTTPStatus(err error) int {
	var se *statusError
	if errors.As(err, &se) {
		return se.code
	}
	return 0
}

// checkReady is the common check before a request: a key where the service
// needs one, and a model.
func (c Config) checkReady() error {
	if c.APIKey == "" && !isLocal(c.BaseURL) {
		return ErrNoKey
	}
	if strings.TrimSpace(c.Model) == "" {
		return ErrNoModel
	}
	return nil
}

// Chat sends the whole conversation and returns the reply text.
func (c Config) Chat(ctx context.Context, msgs []Message) (string, Usage, error) {
	if c.Kind == KindAnthropic {
		return c.chatAnthropic(ctx, msgs)
	}
	if err := c.checkReady(); err != nil {
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
	body, err := json.Marshal(req)
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
	return text, usage, nil
}

// Models lists what the key can actually reach. Model names go stale faster
// than documentation does, so the user needs a way to ask.
func (c Config) Models(ctx context.Context) ([]string, error) {
	infos, err := c.ModelsWithInfo(ctx)
	out := make([]string, 0, len(infos))
	for _, m := range infos {
		out = append(out, m.ID)
	}
	return out, err
}

// ModelInfo is one model a key can reach.
type ModelInfo struct {
	ID string
	// Free is true when the service says the model costs nothing: OpenRouter
	// marks such models with a ":free" suffix and a zero price (f4#1842).
	Free bool
	// PriceIn and PriceOut are the price of one input and one output token
	// when the service publishes it (Priced).
	PriceIn, PriceOut float64
	Priced            bool
}

// ModelsWithInfo is Models with what the service tells about each model.
func (c Config) ModelsWithInfo(ctx context.Context) ([]ModelInfo, error) {
	if c.Kind == KindAnthropic {
		ids, err := c.modelsAnthropic(ctx)
		out := make([]ModelInfo, 0, len(ids))
		for _, id := range ids {
			out = append(out, ModelInfo{ID: id})
		}
		return out, err
	}
	if c.APIKey == "" && !isLocal(c.BaseURL) {
		return nil, ErrNoKey
	}
	raw, err := c.get(ctx, c.endpoint("/models"))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing *struct {
				Prompt     json.RawMessage `json:"prompt"`
				Completion json.RawMessage `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil && parsed.Error.text() != "" {
		return nil, errors.New(parsed.Error.text())
	}
	out := make([]ModelInfo, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		info := ModelInfo{ID: strings.TrimPrefix(m.ID, "models/")}
		info.Free = strings.HasSuffix(info.ID, ":free") ||
			(m.Pricing != nil && zeroPrice(m.Pricing.Prompt) && zeroPrice(m.Pricing.Completion))
		if m.Pricing != nil {
			in, okIn := price(m.Pricing.Prompt)
			out, okOut := price(m.Pricing.Completion)
			info.PriceIn, info.PriceOut, info.Priced = in, out, okIn && okOut
		}
		out = append(out, info)
	}
	return out, nil
}

// zeroPrice reads a price that may come as a string ("0") or a number (0).
func zeroPrice(raw json.RawMessage) bool {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || text == "null" {
		return false
	}
	f, err := strconv.ParseFloat(text, 64)
	return err == nil && f == 0
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
		_ = resp.Body.Close() // Response-body cleanup is best effort.
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
		msg = parsed.Error.text()
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
