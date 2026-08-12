package main

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unxed/vtui"
)

func TestEnvironmentManagerHelpIsAvailableInEnglishAndRussian(t *testing.T) {
	for _, language := range []string{"en", "ru"} {
		t.Run(language, func(t *testing.T) {
			path := "help/" + language + ".hlf"
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.Valid(data) {
				t.Fatalf("%s is not valid UTF-8", path)
			}
			normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
			if strings.Count(normalized, "@EnvironmentManager\n") != 1 {
				t.Fatalf("%s must define EnvironmentManager exactly once", path)
			}

			engine := vtui.NewHelpEngine(&memoryHelpVFS{files: map[string]string{"envman.hlf": normalized}})
			if err := engine.LoadFile("envman.hlf"); err != nil {
				t.Fatal(err)
			}
			topic := engine.GetTopic("EnvironmentManager")
			if topic == nil {
				t.Fatal("EnvironmentManager topic is missing")
			}
			joined := strings.Join(topic.Lines, "\n")
			if !strings.Contains(joined, "envman:") || !strings.Contains(joined, "%NAME%") {
				t.Fatalf("EnvironmentManager topic does not document its command and expansion syntax: %q", joined)
			}
		})
	}
}
