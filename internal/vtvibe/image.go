package vtvibe

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
)

// Pictures in the context folder (unxed/f4#1842, docs/VTVIBE.md § 19a.9,
// item 11). A PNG, JPEG, GIF or WebP file the user attached goes to the model
// as a picture next to the question, not as a hash line in the file pack, so a
// model that sees images can read a screenshot. A model that does not accept
// pictures gets the question again without them and is told so.

// Image is one picture sent with a message.
type Image struct {
	Name string
	MIME string
	Data []byte
}

const (
	// maxImages bounds the pictures sent with one message.
	maxImages = 8
	// maxImageBytes is the largest picture sent; the Anthropic API takes up
	// to 5 MB per picture and the OpenAI-compatible ones about the same.
	maxImageBytes = 5 << 20
)

var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp",
}

// imageMIME returns the picture type of a file named rel, or "" when it is
// not a picture the models take: both the name and the bytes must say so.
func imageMIME(rel string, data []byte) string {
	want := imageTypes[strings.ToLower(path.Ext(rel))]
	if want == "" || http.DetectContentType(data) != want {
		return ""
	}
	return want
}

// imageNote is what the file pack says in place of a picture's bytes.
func imageNote(img Image, sent bool) string {
	if sent {
		return fmt.Sprintf("<picture, %s, %d bytes: attached to the user's message>", img.MIME, len(img.Data))
	}
	return fmt.Sprintf("<picture, %s, %d bytes: not sent, only %d pictures of up to %d MB go with a message>",
		img.MIME, len(img.Data), maxImages, maxImageBytes>>20)
}

// Images returns the pictures of the context folder that go with the next
// message, in the order of the file pack.
func (s *Session) Images() []Image {
	s.treeMu.RLock()
	defer s.treeMu.RUnlock()
	var out []Image
	for _, full := range s.tree.walkFiles(ctxDir) {
		rel := strings.TrimPrefix(full, ctxDir+"/")
		if looksSecret(rel) {
			continue
		}
		data, ok := s.tree.readFile(full)
		if !ok {
			continue
		}
		if img, ok := sendableImage(rel, data, len(out)); ok {
			out = append(out, img)
		}
	}
	return out
}

// sendableImage says whether the file rel is a picture that goes with the
// message when sent pictures come before it.
func sendableImage(rel string, data []byte, sent int) (Image, bool) {
	mime := imageMIME(rel, data)
	if mime == "" {
		return Image{}, false
	}
	img := Image{Name: rel, MIME: mime, Data: data}
	return img, sent < maxImages && len(data) <= maxImageBytes
}

func (img Image) dataURL() string {
	return "data:" + img.MIME + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
}

// MarshalJSON writes the chat-completions message; with pictures the content
// is the list of parts the OpenAI-compatible endpoints take.
func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.Images) == 0 {
		return json.Marshal(struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{m.Role, m.Content})
	}
	parts := []map[string]any{{"type": "text", "text": m.Content}}
	for _, img := range m.Images {
		parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": img.dataURL()}})
	}
	return json.Marshal(struct {
		Role    string           `json:"role"`
		Content []map[string]any `json:"content"`
	}{m.Role, parts})
}

// imagesRefusedPrompt tells the model the pictures did not reach it.
func imagesRefusedPrompt(images []Image) string {
	names := make([]string, len(images))
	for i, img := range images {
		names[i] = img.Name
	}
	return "The user attached pictures (" + strings.Join(names, ", ") + "), but this model did not accept them, " +
		"so they were not sent. Tell the user you cannot see them and that a model which reads pictures can."
}
