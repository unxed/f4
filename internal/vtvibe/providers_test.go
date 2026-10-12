package vtvibe

import "testing"

func TestResolveProviderKeepsOldIniMeaning(t *testing.T) {
	if p := ResolveProvider("", ""); p.ID != ProviderGemini {
		t.Fatalf("empty ini = %q, want gemini", p.ID)
	}
	if p := ResolveProvider("", Providers[0].BaseURL+"/"); p.ID != ProviderGemini {
		t.Fatalf("Gemini's own address = %q, want gemini", p.ID)
	}
	p := ResolveProvider("", "http://localhost:9999/v1")
	if p.ID != ProviderCustom || p.Endpoint("http://localhost:9999/v1") != "http://localhost:9999/v1" {
		t.Fatalf("old custom base_url resolved to %q at %q", p.ID, p.Endpoint("http://localhost:9999/v1"))
	}
	if p := ResolveProvider("XAI", ""); p.ID != "xai" || p.Endpoint("http://ignored") != "https://api.x.ai/v1" {
		t.Fatalf("named preset = %q at %q", p.ID, p.Endpoint("http://ignored"))
	}
	if p := ResolveProvider("nonsense", ""); p.ID != ProviderGemini {
		t.Fatalf("unknown preset = %q, want gemini", p.ID)
	}
}

func TestEffectiveModelFollowsTheProvider(t *testing.T) {
	openai := ProviderByID("openai")
	if got := openai.EffectiveModel(""); got != openai.Model {
		t.Fatalf("empty model = %q", got)
	}
	// Switching from Gemini must not keep asking OpenAI for a Gemini model.
	if got := openai.EffectiveModel(DefaultModel); got != openai.Model {
		t.Fatalf("leftover Gemini default = %q", got)
	}
	if got := openai.EffectiveModel("my-model"); got != "my-model" {
		t.Fatalf("chosen model = %q", got)
	}
	// A local server has no default to fall back to: keep what was chosen.
	if got := ProviderByID("local").EffectiveModel(DefaultModel); got != DefaultModel {
		t.Fatalf("local server replaced the chosen model with %q", got)
	}
}

func TestNeedsKey(t *testing.T) {
	local := ProviderByID("local")
	if local.NeedsKey(local.BaseURL) {
		t.Fatal("a local server asks for a key")
	}
	if !ProviderByID("openrouter").NeedsKey("https://openrouter.ai/api/v1") {
		t.Fatal("OpenRouter does not ask for a key")
	}
	if ProviderByID(ProviderCustom).NeedsKey("http://127.0.0.1:8080/v1") {
		t.Fatal("a custom address on this machine asks for a key")
	}
}
