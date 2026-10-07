//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/unxed/vtui"
)

func fileClipboardBody(paths []string, cut bool) (string, string) {
	body := vtui.FormatURIList(paths)
	if cut {
		return "cut\n" + body, "x-special/gnome-copied-files"
	}
	return body, "text/uri-list"
}

func setFileClipboard(ctx context.Context, _ string, paths []string, cut bool) error {
	if len(paths) == 0 {
		return errFileClipboardUnavailable
	}
	body, mime := fileClipboardBody(paths, cut)
	var name string
	var args []string
	switch {
	case runtime.GOOS == "linux" && os.Getenv("WAYLAND_DISPLAY") != "":
		name, args = "wl-copy", []string{"--type", mime}
	case runtime.GOOS == "linux" && os.Getenv("DISPLAY") != "":
		name, args = "xclip", []string{"-selection", "clipboard", "-t", mime, "-i"}
	default:
		return errFileClipboardUnavailable
	}
	if _, err := exec.LookPath(name); err != nil {
		return errFileClipboardUnavailable
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(body)
	return cmd.Run()
}

func readFileClipboard(ctx context.Context) ([]string, bool, error) {
	var candidates []struct {
		name  string
		args  []string
		gnome bool
	}
	switch {
	case runtime.GOOS == "linux" && os.Getenv("WAYLAND_DISPLAY") != "":
		candidates = []struct {
			name  string
			args  []string
			gnome bool
		}{
			{"wl-paste", []string{"--no-newline", "--type", "x-special/gnome-copied-files"}, true},
			{"wl-paste", []string{"--no-newline", "--type", "text/uri-list"}, false},
		}
	case runtime.GOOS == "linux" && os.Getenv("DISPLAY") != "":
		candidates = []struct {
			name  string
			args  []string
			gnome bool
		}{
			{"xclip", []string{"-selection", "clipboard", "-t", "x-special/gnome-copied-files", "-o"}, true},
			{"xclip", []string{"-selection", "clipboard", "-t", "text/uri-list", "-o"}, false},
		}
	default:
		return nil, false, errFileClipboardUnavailable
	}
	for _, candidate := range candidates {
		if _, err := exec.LookPath(candidate.name); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, candidate.name, candidate.args...).Output()
		if err != nil {
			continue
		}
		data := string(out)
		cut := false
		if candidate.gnome {
			lines := strings.SplitN(strings.ReplaceAll(data, "\r\n", "\n"), "\n", 2)
			if len(lines) < 2 {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(lines[0])) {
			case "cut":
				cut = true
			case "copy":
			default:
				continue
			}
			data = lines[1]
		}
		payload := vtui.ParseURIList(data)
		if len(payload.Paths) > 0 {
			return payload.Paths, cut, nil
		}
	}
	return nil, false, errFileClipboardUnavailable
}
