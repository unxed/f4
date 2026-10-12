package vtvibe

import "strings"

// Provider is a preset for one chat-completions service: where it lives, the
// model to use when none is chosen, and the environment variables that carry
// its key. Every preset speaks the OpenAI chat-completions dialect that
// Config already targets; picking one only fills in the address and the key
// lookup, so a service is reached without hand-editing vtvibe.ini
// (unxed/f4#1842).
type Provider struct {
	ID string
	// Kind is the protocol, see Config.Kind.
	Kind    string
	Name    string
	BaseURL string
	Model   string
	KeyEnv  []string
	// KeyURL is the page where a key for this service is issued; empty when
	// the service needs no key or the address is the user's own.
	KeyURL string
	// OwnURL means the base_url from vtvibe.ini is used instead of BaseURL
	// when it is set: a local server can listen on any port, and a custom
	// service has no preset address at all.
	OwnURL bool
}

const (
	ProviderGemini = "gemini"
	ProviderCustom = "custom"
)

// Providers lists the presets in the order the settings show them.
var Providers = []Provider{
	{ID: ProviderGemini, Name: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Model: DefaultModel, KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, KeyURL: "https://aistudio.google.com/apikey"},
	{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1",
		Model: "gpt-5.5", KeyEnv: []string{"OPENAI_API_KEY"}, KeyURL: "https://platform.openai.com/api-keys"},
	{ID: "anthropic", Name: "Anthropic Claude", Kind: KindAnthropic, BaseURL: "https://api.anthropic.com",
		Model: "claude-opus-5-5", KeyEnv: []string{"ANTHROPIC_API_KEY"}, KeyURL: "https://console.anthropic.com/settings/keys"},
	{ID: "xai", Name: "xAI Grok", BaseURL: "https://api.x.ai/v1",
		Model: "grok-4.6", KeyEnv: []string{"XAI_API_KEY"}, KeyURL: "https://console.x.ai"},
	{ID: "mistral", Name: "Mistral", BaseURL: "https://api.mistral.ai/v1",
		Model: "mistral-large-latest", KeyEnv: []string{"MISTRAL_API_KEY"}, KeyURL: "https://console.mistral.ai/api-keys"},
	{ID: "deepseek", Name: "DeepSeek", BaseURL: "https://api.deepseek.com",
		Model: "deepseek-flash", KeyEnv: []string{"DEEPSEEK_API_KEY"}, KeyURL: "https://platform.deepseek.com/api_keys"},
	// Groq serves open-weight models fast and has a free tier.
	{ID: "groq", Name: "Groq", BaseURL: "https://api.groq.com/openai/v1",
		Model: "llama-3.3-70b-versatile", KeyEnv: []string{"GROQ_API_KEY"}, KeyURL: "https://console.groq.com/keys"},
	{ID: "openrouter", Name: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1",
		Model: "openrouter/auto", KeyEnv: []string{"OPENROUTER_API_KEY"}, KeyURL: "https://openrouter.ai/keys"},
	{ID: "local", Name: "Local server (Ollama, LM Studio, llama.cpp)", BaseURL: "http://127.0.0.1:11434/v1", OwnURL: true},
	// Custom keeps what vtvibe.ini meant before presets existed: its own
	// base_url, and the key from whichever of the three variables is set.
	{ID: ProviderCustom, Name: "Custom address", OwnURL: true,
		KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY"}},
}

// ProviderByID returns the preset with id, or Gemini for an unknown one.
func ProviderByID(id string) Provider {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range Providers {
		if p.ID == id {
			return p
		}
	}
	return Providers[0]
}

// ResolveProvider picks the preset for a vtvibe.ini that names provider and
// base_url. An ini written before presets existed has no provider: its
// base_url, when it is not Gemini's, made it a custom service.
func ResolveProvider(provider, baseURL string) Provider {
	if strings.TrimSpace(provider) != "" {
		return ProviderByID(provider)
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || baseURL == Providers[0].BaseURL {
		return Providers[0]
	}
	return ProviderByID(ProviderCustom)
}

// Endpoint is the address requests go to: the ini's base_url where the
// preset takes the user's own, the preset's address otherwise.
func (p Provider) Endpoint(iniBaseURL string) string {
	if p.OwnURL && strings.TrimSpace(iniBaseURL) != "" {
		return strings.TrimSpace(iniBaseURL)
	}
	return p.BaseURL
}

// EffectiveModel is the model to request. An empty model, or the default of
// another preset left behind after switching providers, means this preset's
// own default.
func (p Provider) EffectiveModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return p.Model
	}
	for _, other := range Providers {
		if other.ID != p.ID && other.Model != "" && model == other.Model && p.Model != "" {
			return p.Model
		}
	}
	return model
}

// NeedsKey reports whether requests to this preset need an API key at all.
func (p Provider) NeedsKey(endpoint string) bool {
	if p.ID == "local" {
		return false
	}
	return !strings.Contains(endpoint, "127.0.0.1") && !strings.Contains(endpoint, "localhost")
}
