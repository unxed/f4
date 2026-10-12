package vtvibe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// Claude through the Anthropic Messages API with the official Go SDK
// (unxed/f4#1842, docs/VTVIBE.md § 6.3.1). Every other preset speaks the
// OpenAI chat-completions dialect; Claude is reached natively instead of
// through a compatibility shim.

// KindAnthropic marks a Config whose BaseURL speaks the Messages API.
const KindAnthropic = "anthropic"

// anthropicMaxTokens is the answer cap of one non-streaming request: large
// enough for whole files, small enough to stay under the HTTP timeouts.
const anthropicMaxTokens = 16000

// anthropicFallbackModels are the models for which a request asks the API to
// re-serve a refused answer with a fallback model ("fallbacks": "default").
var anthropicFallbackModels = map[string]bool{
	"claude-fable-5-1":  true,
	"claude-opus-5-5":   true,
	"claude-opus-5":     true,
	"claude-sonnet-5-5": true,
}

func (c Config) anthropicClient() anthropic.Client {
	opts := []option.RequestOption{option.WithAPIKey(c.APIKey), option.WithHTTPClient(httpClient)}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	return anthropic.NewClient(opts...)
}

// anthropicParams turns the chat-completions shaped history into a Messages
// API request: system messages become the system prompt, the rest alternate
// between user and assistant.
func (c Config) anthropicParams(msgs []Message) anthropic.BetaMessageNewParams {
	params := anthropic.BetaMessageNewParams{Model: anthropic.Model(c.Model), MaxTokens: anthropicMaxTokens}
	for _, m := range msgs {
		switch m.Role {
		case "system":
			params.System = append(params.System, anthropic.BetaTextBlockParam{Text: m.Content})
		case "assistant", "model":
			params.Messages = append(params.Messages, anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)},
			})
		default:
			blocks := append([]anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)}, anthropicImageBlocks(m.Images)...)
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(blocks...))
		}
	}
	if anthropicFallbackModels[c.Model] {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}
	return params
}

func (c Config) chatAnthropic(ctx context.Context, msgs []Message) (string, Usage, error) {
	if err := c.checkReady(); err != nil {
		return "", Usage{}, err
	}
	client := c.anthropicClient()
	reply, err := client.Beta.Messages.New(ctx, c.anthropicParams(msgs))
	if err != nil {
		return "", Usage{}, err
	}
	return anthropicReplyText(*reply)
}

// anthropicReplyText is the text of a whole reply, or why there is none.
func anthropicReplyText(reply anthropic.BetaMessage) (string, Usage, error) {
	usage := Usage{In: int(reply.Usage.InputTokens), Out: int(reply.Usage.OutputTokens)}
	if reply.StopReason == anthropic.BetaStopReasonRefusal {
		why := strings.TrimSpace(reply.StopDetails.Explanation)
		if why == "" {
			why = string(reply.StopDetails.Category)
		}
		return "", usage, fmt.Errorf("the model declined to answer: %s", why)
	}
	var sb strings.Builder
	for _, block := range reply.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			sb.WriteString(text.Text)
		}
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "", usage, errors.New("the model returned an empty answer")
	}
	return sb.String(), usage, nil
}

func (c Config) modelsAnthropic(ctx context.Context) ([]string, error) {
	if c.APIKey == "" && !isLocal(c.BaseURL) {
		return nil, ErrNoKey
	}
	client := c.anthropicClient()
	pager := client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	var out []string
	for pager.Next() {
		out = append(out, pager.Current().ID)
	}
	return out, pager.Err()
}

// runAgentAnthropic is RunAgent over the Messages API: tools are offered as
// client tools, every tool_use block of a reply is run and answered with a
// tool_result block in one user message, and the reply itself goes back into
// the history unchanged (ToParam keeps its thinking blocks, which the next
// request must carry as they were).
func (c Config) runAgentAnthropic(ctx context.Context, msgs []Message, tools []Tool, opts AgentOptions, maxSteps int) (string, Usage, error) {
	client := c.anthropicClient()
	params := c.anthropicParams(msgs)
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
		schema := anthropic.BetaToolInputSchemaParam{}
		if t.Parameters != nil {
			schema.Properties = t.Parameters["properties"]
			if req, ok := t.Parameters["required"].([]string); ok {
				schema.Required = req
			}
		}
		spec := anthropic.BetaToolUnionParamOfTool(schema, t.Name)
		if t.Description != "" {
			spec.OfTool.Description = anthropic.String(t.Description)
		}
		params.Tools = append(params.Tools, spec)
	}

	var usage Usage
	var withoutImages []anthropic.BetaContentBlockParamUnion
	for step := 0; step < maxSteps; step++ {
		reply, err := client.Beta.Messages.New(ctx, params)
		if err != nil && withoutImages != nil && !contextExhausted(err) && ctx.Err() == nil {
			params.Messages[len(params.Messages)-1] = anthropic.NewBetaUserMessage(withoutImages...)
			reply, err = client.Beta.Messages.New(ctx, params)
		}
		withoutImages = nil
		if err != nil {
			return "", usage, err
		}
		usage.In += int(reply.Usage.InputTokens)
		usage.Out += int(reply.Usage.OutputTokens)
		if reply.StopReason == anthropic.BetaStopReasonRefusal {
			why := strings.TrimSpace(reply.StopDetails.Explanation)
			if why == "" {
				why = string(reply.StopDetails.Category)
			}
			return "", usage, fmt.Errorf("the model declined to go on: %s", why)
		}
		var text strings.Builder
		var results []anthropic.BetaContentBlockParamUnion
		toolCtx, sink := withImageSink(ctx)
		for _, block := range reply.Content {
			switch b := block.AsAny().(type) {
			case anthropic.BetaTextBlock:
				text.WriteString(b.Text)
			case anthropic.BetaToolUseBlock:
				raw, err := json.Marshal(b.Input)
				if err != nil {
					raw = []byte("{}")
				}
				call := agentToolUse{ID: b.ID, Type: "function"}
				call.Function.Name = b.Name
				call.Function.Arguments = string(raw)
				result, runErr := c.runTool(toolCtx, byName, call)
				if opts.OnStep != nil {
					opts.OnStep(AgentStep{Tool: b.Name, Args: string(raw), Result: result, Err: runErr})
				}
				if runErr != nil {
					result = "error: " + runErr.Error() + "\n" + result
				}
				results = append(results, anthropic.NewBetaToolResultBlock(b.ID, tailBytes(result, opts.MaxToolOutput), runErr != nil))
			}
		}
		// pause_turn: a long server-side turn stopped midway; sending the
		// reply back as it is lets the model continue it.
		if len(results) == 0 && reply.StopReason != anthropic.BetaStopReasonPauseTurn {
			if strings.TrimSpace(text.String()) == "" {
				return "", usage, errors.New("the model returned an empty answer")
			}
			return text.String(), usage, nil
		}
		params.Messages = append(params.Messages, reply.ToParam())
		if len(results) > 0 {
			imgs := sink.take()
			blocks := results
			if len(imgs) > 0 {
				blocks = append(append([]anthropic.BetaContentBlockParamUnion(nil), results...), anthropic.NewBetaTextBlock(imagesShownText(imgs)))
				blocks = append(blocks, anthropicImageBlocks(imgs)...)
			}
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(blocks...))
			if len(imgs) > 0 {
				// The model may not take pictures: the request is checked
				// now, and on failure the round goes on without them.
				withoutImages = append(append([]anthropic.BetaContentBlockParamUnion(nil), results...), anthropic.NewBetaTextBlock(imagesRefusedText(imgs)))
			}
		}
		if err := ctx.Err(); err != nil {
			return "", usage, err
		}
	}
	return "", usage, ErrAgentSteps
}

func anthropicImageBlocks(imgs []Image) []anthropic.BetaContentBlockParamUnion {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(imgs))
	for _, img := range imgs {
		blocks = append(blocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
			Data:      base64.StdEncoding.EncodeToString(img.Data),
			MediaType: anthropic.BetaBase64ImageSourceMediaType(img.MIME),
		}))
	}
	return blocks
}
