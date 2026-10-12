package vtvibe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// f4#1842, item 11: pictures in the context folder go to the model as pictures.

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 32)...)

// rawChatServer answers every request with reply, or with a 400 when refuse
// says the request is one the model does not take.
func rawChatServer(t *testing.T, reply string, refuse func(body string) bool) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		if refuse != nil && refuse(string(data)) {
			http.Error(w, `{"error":{"message":"this model does not support image input"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
}

func sessionWithFiles(t *testing.T, files map[string][]byte) *Session {
	t.Helper()
	s := NewSession()
	for name, data := range files {
		if err := s.tree.writeFile("/ctx/"+name, data); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestPicturesGoWithTheQuestion(t *testing.T) {
	srv, bodies := rawChatServer(t, chatReply("a red square"), nil)
	s := sessionWithFiles(t, map[string][]byte{
		"shot.png":  pngBytes,
		"fake.png":  []byte("not a picture at all"),
		"notes.txt": []byte("plain text"),
	})
	if err := s.Ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "what is on the screenshot?"); err != nil {
		t.Fatal(err)
	}
	got := bodies()
	if len(got) != 1 {
		t.Fatalf("requests %d", len(got))
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got[0]), &req); err != nil {
		t.Fatal(err)
	}
	var system string
	if err := json.Unmarshal(req.Messages[0].Content, &system); err != nil {
		t.Fatalf("the system message is not text: %s", req.Messages[0].Content)
	}
	if !strings.Contains(system, "<picture, image/png, 40 bytes: attached to the user's message>") ||
		!strings.Contains(system, "not a picture at all") {
		t.Fatalf("the pack does not describe the files right:\n%s", system)
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &parts); err != nil {
		t.Fatalf("the question is not a list of parts: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	if len(parts) != 2 || parts[0].Text != "what is on the screenshot?" || parts[1].Type != "image_url" || parts[1].ImageURL.URL != want {
		t.Fatalf("parts %+v", parts)
	}
}

func TestAModelWithoutPicturesIsAskedAgainWithoutThem(t *testing.T) {
	srv, bodies := rawChatServer(t, chatReply("I cannot see pictures"), func(body string) bool {
		return strings.Contains(body, "image_url")
	})
	s := sessionWithFiles(t, map[string][]byte{"shot.png": pngBytes})
	reply, err := s.ask(context.Background(), Config{BaseURL: srv.URL, Model: "m", APIKey: "k"}, "look", true, "")
	if err != nil || reply != "I cannot see pictures" {
		t.Fatalf("reply %q, err %v", reply, err)
	}
	got := bodies()
	last := got[len(got)-1]
	if !strings.Contains(got[0], "image_url") || strings.Contains(last, "image_url") ||
		!strings.Contains(last, "did not accept them") || !strings.Contains(last, "shot.png") {
		t.Fatalf("requests:\n%s", strings.Join(got, "\n"))
	}
}

func TestPicturesAreBounded(t *testing.T) {
	files := map[string][]byte{"big.png": append(append([]byte(nil), pngBytes...), make([]byte, maxImageBytes)...)}
	for i := 0; i < maxImages+1; i++ {
		files[string(rune('a'+i))+".png"] = pngBytes
	}
	s := sessionWithFiles(t, files)
	images := s.Images()
	if len(images) != maxImages || images[0].Name != "a.png" {
		t.Fatalf("%d pictures, first %q", len(images), images[0].Name)
	}
	pack := s.Pack()
	if strings.Count(pack, "attached to the user's message") != maxImages || strings.Count(pack, "not sent") != 2 {
		t.Fatalf("pack:\n%s", pack)
	}
}

func TestAnthropicGetsPictureBlocks(t *testing.T) {
	cfg := Config{Kind: KindAnthropic, Model: "claude-opus-5-5"}
	params := cfg.anthropicParams([]Message{{Role: "user", Content: "look", Images: []Image{{Name: "shot.png", MIME: "image/png", Data: pngBytes}}}})
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	blocks := got.Messages[0].Content
	if len(blocks) != 2 || blocks[0].Text != "look" || blocks[1].Type != "image" || blocks[1].Source.Type != "base64" ||
		blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatalf("blocks %s", data)
	}
}

func TestMessageWithoutPicturesKeepsTextContent(t *testing.T) {
	data, err := json.Marshal(Message{Role: "user", Content: "hi"})
	if err != nil || string(data) != `{"role":"user","content":"hi"}` {
		t.Fatalf("%s, %v", data, err)
	}
}
