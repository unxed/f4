package vtvibe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsWithInfoMarksFreeModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[
{"id":"paid/model","pricing":{"prompt":"0.000002","completion":"0.000008"}},
{"id":"vendor/model:free","pricing":{"prompt":"0","completion":"0"}},
{"id":"numeric/zero","pricing":{"prompt":0,"completion":0}},
{"id":"half/free","pricing":{"prompt":"0","completion":"0.1"}},
{"id":"models/no-pricing"}]}`)
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}
	got, err := cfg.ModelsWithInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"paid/model": false, "vendor/model:free": true, "numeric/zero": true, "half/free": false, "no-pricing": false}
	if len(got) != len(want) {
		t.Fatalf("models = %#v", got)
	}
	for _, m := range got {
		if free, ok := want[m.ID]; !ok || free != m.Free {
			t.Errorf("%s: free = %v", m.ID, m.Free)
		}
	}
	ids, err := cfg.Models(context.Background())
	if err != nil || len(ids) != 5 || ids[4] != "no-pricing" {
		t.Fatalf("Models = %v, %v", ids, err)
	}
}

func TestNewPresets(t *testing.T) {
	for id, env := range map[string]string{"mistral": "MISTRAL_API_KEY", "deepseek": "DEEPSEEK_API_KEY", "groq": "GROQ_API_KEY"} {
		p := ProviderByID(id)
		if p.ID != id || p.BaseURL == "" || p.Model == "" || p.KeyEnv[0] != env || !p.NeedsKey(p.BaseURL) {
			t.Errorf("preset %s = %#v", id, p)
		}
	}
}
