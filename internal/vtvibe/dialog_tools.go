package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The model's own handles on the dialog (unxed/f4#1842, docs/VTVIBE.md
// § 19a, step B3): the name of the model is always in the context, and the
// model may switch the dialog to another model or rename the dialog when the
// user or the instruction asks for it. Each can be switched off in Settings;
// the host then leaves the tool out.

// ModelNotice is the line every system prompt carries about the model.
func ModelNotice(model string) string {
	if strings.TrimSpace(model) == "" {
		model = "the provider's default model"
	}
	return fmt.Sprintf("You are running on the model %q.", model)
}

// DialogControls are the host's callbacks; a nil one leaves its tool out.
type DialogControls struct {
	// SetModel switches the dialog to another model from the next request on.
	SetModel func(model string) error
	// Rename gives the dialog a new name.
	Rename func(title string) error
}

// DialogTools returns the tools for the controls that are enabled.
func DialogTools(c DialogControls) []Tool {
	var tools []Tool
	if c.SetModel != nil {
		tools = append(tools, stringArgTool("set_model",
			"Switch this dialog to another model, from the next request on. Use it only when the user or the instruction asks for a different model.",
			"model", "The model identifier, as the provider names it.", c.SetModel,
			"the dialog switches to %q from the next request"))
	}
	if c.Rename != nil {
		tools = append(tools, stringArgTool("rename_dialog",
			"Give this dialog a short name that says what it is about.",
			"title", "The new name of the dialog.", c.Rename, "the dialog is now called %q"))
	}
	return tools
}

func stringArgTool(name, description, arg, argDescription string, apply func(string) error, done string) Tool {
	return Tool{
		Name:        name,
		Description: description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{arg: map[string]any{"type": "string", "description": argDescription}},
			"required":   []string{arg},
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var args map[string]any
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			value, _ := args[arg].(string)
			value = strings.TrimSpace(value)
			if value == "" || strings.ContainsAny(value, "\r\n") {
				return "", errors.New(arg + " must be one non-empty line")
			}
			if err := apply(value); err != nil {
				return "", err
			}
			return fmt.Sprintf(done, value), nil
		},
	}
}
