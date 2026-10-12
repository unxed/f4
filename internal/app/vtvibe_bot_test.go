package app

import (
	"testing"
	"time"

	"github.com/unxed/f4/internal/vtvibe"
)

func TestParseBotArgs(t *testing.T) {
	for _, tc := range []struct {
		in     string
		source string
		pause  time.Duration
		whole  bool
	}{
		{"https://example.org/LUNOBOT.md 30m", "https://example.org/LUNOBOT.md", 30 * time.Minute, false},
		{"/home/me/bot.md", "/home/me/bot.md", vtvibe.DefaultBotPause, false},
		{"/home/me/my bot.md 1h", "/home/me/my bot.md", time.Hour, false},
		{"/home/me/my bot.md", "/home/me/my bot.md", vtvibe.DefaultBotPause, false},
		{"/home/me/bot.md 1h whole", "/home/me/bot.md", time.Hour, true},
		{"/home/me/bot.md WHOLE", "/home/me/bot.md", vtvibe.DefaultBotPause, true},
		{"whole", "whole", vtvibe.DefaultBotPause, false},
	} {
		source, pause, whole := parseBotArgs(tc.in)
		if source != tc.source || pause != tc.pause || whole != tc.whole {
			t.Errorf("parseBotArgs(%q) = %q, %s, %v", tc.in, source, pause, whole)
		}
	}
}
