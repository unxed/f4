package vtvibe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

// view_image (unxed/f4#1842, docs/VTVIBE.md § 19a.9, item 11): a worker or
// the bot looks at a picture in its folder — a screenshot, a diagram. A tool
// result is text, so the picture goes to the model next to it: in the user
// message that follows the results for the chat-completions endpoints, as an
// image block after the tool results for Anthropic. A model that does not
// take pictures gets the round again with a note instead.

type imageSinkKey struct{}

// imageSink collects the pictures the tools of one model reply attach.
type imageSink struct {
	mu   sync.Mutex
	imgs []Image
}

func withImageSink(ctx context.Context) (context.Context, *imageSink) {
	sink := &imageSink{}
	return context.WithValue(ctx, imageSinkKey{}, sink), sink
}

func (s *imageSink) take() []Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	imgs := s.imgs
	s.imgs = nil
	return imgs
}

// attachImage hands img to the model with the tool results; false when the
// run has nowhere to put it.
func attachImage(ctx context.Context, img Image) bool {
	sink, ok := ctx.Value(imageSinkKey{}).(*imageSink)
	if !ok {
		return false
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.imgs) >= maxImages {
		return false
	}
	sink.imgs = append(sink.imgs, img)
	return true
}

// ViewImageTool lets the model see a PNG, JPEG, GIF or WebP file in dir.
func ViewImageTool(dir string) Tool {
	return Tool{
		Name:        "view_image",
		Description: "Look at a picture file (PNG, JPEG, GIF or WebP, up to 5 MB): it is shown to you after the tool results. Relative paths start in the working directory.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path"},
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			path, err := resolvePath(dir, args.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if info.Size() > maxImageBytes {
				return "", fmt.Errorf("%s has %d bytes, more than %d", path, info.Size(), maxImageBytes)
			}
			data, err := os.ReadFile(path) // #nosec G304 -- the model reads files of its working folder; that is the tool
			if err != nil {
				return "", err
			}
			mime := imageMIME(path, data)
			if mime == "" {
				return "", fmt.Errorf("%s is not a PNG, JPEG, GIF or WebP picture", path)
			}
			if !attachImage(ctx, Image{Name: args.Path, MIME: mime, Data: data}) {
				return "", fmt.Errorf("no more than %d pictures can be shown at once", maxImages)
			}
			return fmt.Sprintf("%s (%s, %d bytes) is shown to you after the tool results.", args.Path, mime, len(data)), nil
		},
	}
}

// imagesShownText introduces the pictures after the tool results.
func imagesShownText(imgs []Image) string {
	names := make([]string, len(imgs))
	for i, img := range imgs {
		names[i] = img.Name
	}
	return "[f4] The pictures view_image was asked for: " + strings.Join(names, ", ") + "."
}

// imagesRefusedText replaces them when the model does not take pictures.
func imagesRefusedText(imgs []Image) string {
	names := make([]string, len(imgs))
	for i, img := range imgs {
		names[i] = img.Name
	}
	return "[f4] This model did not accept pictures, so " + strings.Join(names, ", ") +
		" could not be shown; do without them and do not call view_image again."
}
