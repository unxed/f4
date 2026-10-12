package settings

import (
	"path/filepath"
	"sort"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/keymap"
	"github.com/unxed/f4/internal/vtvibe"

	"context"
	"fmt"
	"os"
	"strings"

	"github.com/unxed/f4/internal/action"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/f4/sdk/f4settings"
)

type aiSettingsProvider struct{}

func (aiSettingsProvider) Catalog() f4settings.Catalog {
	loaded := ini.Load(filepath.Join(config.GetF4ConfigDir(), "vtvibe.ini"))
	provider := vtvibe.ResolveProvider(loaded.GetString("general", "provider", ""), loaded.GetString("general", "base_url", ""))
	source := "vtvibe.ini"
	for _, name := range provider.KeyEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			source = name
			break
		}
	}
	choices := make([]f4settings.Choice, 0, len(vtvibe.Providers))
	for _, p := range vtvibe.Providers {
		choices = append(choices, f4settings.Choice{Value: p.ID, Label: f4settings.Text{English: p.Name, Literal: true}})
	}
	service := f4settings.Scalar("ai.provider", "ai", "Service", "Provider", "Service the AI panel talks to. Each preset fills in the address and reads its own key variable: GEMINI_API_KEY or GOOGLE_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY, XAI_API_KEY, MISTRAL_API_KEY, DEEPSEEK_API_KEY, GROQ_API_KEY, OPENROUTER_API_KEY. A local server needs no key.", f4settings.ChoiceKind)
	service.Choices = choices
	key := f4settings.Scalar("ai.key", "ai", "Credentials", "Saved API key", "Used only when the key variable of the chosen provider is empty. Effective source: %s (if a key is available). The key is stored in the local vtvibe.ini file.", f4settings.Secret)
	key.Description.Args = []any{source}
	return f4settings.Catalog{ID: "ai", Categories: Categories, Background: true, Fields: []f4settings.Field{
		service,
		f4settings.Scalar("ai.base_url", "ai", "Service", "Address", "Chat-completions address for the Local server and Custom address providers, for example http://127.0.0.1:1234/v1 for LM Studio. The other providers use their own address.", f4settings.String),
		key,
		f4settings.Scalar("ai.model", "ai", "Model", "Model", "Model identifier sent with subsequent AI requests. Empty means the default model of the chosen provider.", f4settings.String),
		aiAllowField("ai.allow_model_switch", "Let the model switch models", "The model may switch the dialog to another model when you or its instruction ask for it."),
		aiAllowField("ai.allow_rename", "Let the model rename the dialog", "The model may give the dialog a name that says what it is about."),
		aiNonstopField(),
		aiAskEachField(),
		f4settings.Scalar("ai.github_token", "ai", "Credentials", "GitHub token", "Given to the commands the bot and the workers run as GH_TOKEN and GITHUB_TOKEN, unless the dialog has its own (ai:token). Stored in the local vtvibe.ini file.", f4settings.Secret),
	}}
}
func aiAllowField(id, label, description string) f4settings.Field {
	f := f4settings.Scalar(id, "ai", "Model", label, description, f4settings.Boolean)
	f.Default = "true"
	return f
}

// aiNonstopField is the working mode of new dialogs (f4#1842, stage H6).
func aiNonstopField() f4settings.Field {
	f := f4settings.Scalar("ai.nonstop", "ai", "Model", "Work without stopping", "New dialogs start in the non-stop mode: the model goes on by itself until every order is done instead of answering once and waiting. The ai:mode command chooses the mode of one dialog.", f4settings.Boolean)
	f.Default = "false"
	return f
}

// aiAskEachField is the permission mode of workers and the bot (f4#1842,
// stage H9).
func aiAskEachField() f4settings.Field {
	f := f4settings.Scalar("ai.ask_each", "ai", "Model", "Ask before each command", "Workers and the bot ask before each shell command, file change or MCP tool, unless the allow list (ai:allow) covers it. Off: one confirmation for the whole task or bot.", f4settings.Boolean)
	f.Default = "false"
	return f
}

func (p aiSettingsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	path := filepath.Join(config.GetF4ConfigDir(), "vtvibe.ini")
	loaded := ini.Load(path)
	provider := vtvibe.ResolveProvider(loaded.GetString("general", "provider", ""), loaded.GetString("general", "base_url", ""))
	d := f4settings.NewDraft(map[string]string{
		"ai.provider": provider.ID,
		"ai.base_url": loaded.GetString("general", "base_url", ""),
		"ai.key":      loaded.GetString("general", "key", ""),
		"ai.model":    loaded.GetString("general", "model", ""),
		// The model's own controls (f4#1842) are on unless switched off.
		"ai.allow_model_switch": loaded.GetString("general", "allow_model_switch", "true"),
		"ai.allow_rename":       loaded.GetString("general", "allow_rename", "true"),
		"ai.nonstop":            loaded.GetString("general", "nonstop", "false"),
		"ai.ask_each":           loaded.GetString("general", "ask_each", "false"),
		"ai.github_token":       loaded.GetString("general", "github_token", ""),
	}, nil)
	d.ValidateFunc = func(d *f4settings.Draft) map[string]error {
		errs := map[string]error{}
		for _, id := range d.Changed() {
			if err := settingsSingleLine(d.Values[id]); err != nil {
				errs[id] = err
			}
		}
		return errs
	}
	d.CommitFunc = func(ctx context.Context, d *f4settings.Draft) f4settings.Result {
		errs := d.Validate()
		if len(errs) > 0 {
			return f4settings.Result{Errors: errs}
		}
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return f4settings.Result{Errors: map[string]error{"ai": err}}
		}
		current := ini.Load(path)
		patch := map[string]string{}
		for _, id := range d.Changed() {
			key := strings.TrimPrefix(id, "ai.")
			fallback := ""
			if strings.HasPrefix(key, "allow_") {
				fallback = "true"
			}
			if key == "nonstop" || key == "ask_each" {
				fallback = "false"
			}
			v := current.GetString("general", key, fallback)
			if key == "provider" {
				v = vtvibe.ResolveProvider(v, current.GetString("general", "base_url", "")).ID
			}
			if v != d.Baseline[id] && v != d.Values[id] {
				return f4settings.Result{Errors: map[string]error{id: settingsError("value changed outside Settings Center")}}
			}
			patch[key] = d.Values[id]
		}
		if err := ctx.Err(); err != nil {
			return f4settings.Result{Errors: map[string]error{"ai": err}}
		}
		if err := config.WriteUserFileAtomically(path, config.UpdateIniValues(data, "general", patch), 0600); err != nil {
			return f4settings.Result{Errors: map[string]error{"ai": err}}
		}
		return f4settings.Result{Applied: d.Changed()}
	}
	return d, nil
}

type hotkeySettingsProvider struct{}

func (hotkeySettingsProvider) Catalog() f4settings.Catalog {
	fields := []f4settings.Field{
		recordField("binding.Action", "Action", "Stable action identifier to invoke. Existing action IDs and plugin actions remain supported.", f4settings.ChoiceKind),
		recordField("binding.Key", "Chord", "Far-style chord spelling, such as CtrlShiftF. An empty chord leaves the action unassigned.", f4settings.Chord),
		recordField("binding.Area", "Area", "Input context in which this binding is considered. Common is the fallback for other areas.", f4settings.ChoiceKind),
		recordField("binding.Condition", "Condition", "Optional registered condition checked when dispatching the action. Empty means unconditional.", f4settings.String),
	}
	fields[0].ChoicePresentation = "dropdown"
	fields[2].ChoicePresentation = "dropdown"
	for _, a := range action.AllSorted() {
		fields[0].Choices = append(fields[0].Choices, f4settings.Choice{Value: a.Name, Label: f4settings.Text{English: action.PlainLabel(a.DisplayLabel()) + " (" + a.Name + ")", Literal: true}})
	}
	fields[2].Choices = f4settings.Choices("Shell:Panels", "Terminal:Terminal", "Editor:Editor", "Viewer:Viewer", "Dialog:Dialog", "Menu:Menu", "Disks:Drive chooser", "Common:Common")
	col := recordCollection("bindings", "hotkeys", "Key bindings", "The Hotkey Configurator lists configurable, native and plugin shortcuts. Use Assign or Unbind; native shortcuts are read-only. Changes are saved with Apply or OK.", "binding.Action", fields)
	col.Ordered = false
	return f4settings.Catalog{ID: "hotkeys", Categories: Categories, Collections: []f4settings.Collection{col}}
}
func (hotkeySettingsProvider) Begin(context.Context) (*f4settings.Draft, error) {
	hm := keymap.GlobalHotkeysMgr
	var rows []f4settings.Record
	if hm != nil {
		for i, r := range settingsHotkeyRows(hm) {
			if r.Editable {
				rows = append(rows, f4settings.Record{ID: fmt.Sprintf("binding:%d", i), Values: map[string]string{"binding.Action": r.Action, "binding.Key": r.RawKey, "binding.Area": r.Area, "binding.Condition": r.Condition}})
			}
		}
	}
	d := f4settings.NewDraft(nil, map[string][]f4settings.Record{"bindings": rows})
	build := func(d *f4settings.Draft) (*keymap.HotkeyManager, error) {
		if hm == nil {
			return nil, settingsError("keyboard manager is unavailable")
		}
		next := hm.CloneForEdit()
		old := map[string]string{}
		wanted := map[string]string{}
		flatten := func(rows []f4settings.Record, dest map[string]string) error {
			for _, r := range rows {
				v := r.Values
				key := v["binding.Key"]
				if key == "" {
					continue
				}
				area := v["binding.Area"]
				action := v["binding.Action"]
				cond := v["binding.Condition"]
				if strings.ContainsAny(key+area+action+cond, "\r\n=") {
					return settingsError("binding contains an invalid separator")
				}
				id := area + "\x00" + key
				if _, ok := dest[id]; ok {
					return settingsError("duplicate chord %s in %s", key, area)
				}
				if cond != "" {
					action += ":" + cond
				}
				dest[id] = action
			}
			return nil
		}
		if err := flatten(d.BaselineRecords["bindings"], old); err != nil {
			return nil, err
		}
		if err := flatten(d.Records["bindings"], wanted); err != nil {
			return nil, err
		}
		keys := map[string]bool{}
		for k := range old {
			keys[k] = true
		}
		for k := range wanted {
			keys[k] = true
		}
		for id := range keys {
			if old[id] == wanted[id] {
				continue
			}
			area, key, _ := strings.Cut(id, "\x00")
			current, configured := next.Bindings[area][key]
			if !configured {
				current = next.Defaults[area][key]
			}
			if current != old[id] && current != wanted[id] {
				return nil, settingsError("binding %s changed outside Settings Center", key)
			}
			if next.Bindings[area] == nil {
				next.Bindings[area] = map[string]string{}
			}
			if wanted[id] == "" {
				if next.Defaults[area][key] != "" {
					next.Bindings[area][key] = "None"
				} else {
					delete(next.Bindings[area], key)
				}
			} else {
				next.Bindings[area][key] = wanted[id]
			}
		}
		return next, nil
	}
	d.ValidateFunc = func(d *f4settings.Draft) map[string]error {
		if !d.Dirty("bindings") {
			return nil
		}
		_, err := build(d)
		if err != nil {
			return map[string]error{"bindings": err}
		}
		return nil
	}
	d.CommitFunc = func(ctx context.Context, d *f4settings.Draft) f4settings.Result {
		next, err := build(d)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = next.SaveError()
		}
		if err != nil {
			return f4settings.Result{Errors: map[string]error{"bindings": err}}
		}
		hm.ReplaceBindingsFrom(next)
		return f4settings.Result{Applied: []string{"bindings"}}
	}
	return d, nil
}

// settingsHotkeyRows contains configurable bindings and unassigned actions.
func settingsHotkeyRows(hm *keymap.HotkeyManager) []settingsHotkeyRow {
	var rows []settingsHotkeyRow
	assigned := map[string]bool{}
	for area, bindings := range hm.GetActiveBindings() {
		for key, binding := range bindings {
			name, condition, _ := strings.Cut(binding, ":")
			rows = append(rows, settingsHotkeyRow{name, key, area, condition, true})
			assigned[strings.ToLower(name)] = true
		}
	}
	// Keep explicit removals in the draft so default chords remain disabled.
	for area, bindings := range hm.Bindings {
		for key, binding := range bindings {
			if binding == "None" || binding == "" {
				rows = append(rows, settingsHotkeyRow{"None", key, area, "", true})
			}
		}
	}

	for _, a := range action.AllSorted() {
		if !assigned[strings.ToLower(a.Name)] {
			rows = append(rows, settingsHotkeyRow{Action: a.Name, Area: a.Area, Editable: true})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Action != b.Action {
			return a.Action < b.Action
		}
		if a.Area != b.Area {
			return a.Area < b.Area
		}
		return a.RawKey < b.RawKey
	})
	return rows
}

type settingsHotkeyRow struct {
	Action, RawKey, Area, Condition string
	Editable                        bool
}
