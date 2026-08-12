package vtvibe

import (
	"testing"
)

func TestArtifactName(t *testing.T) {
	tests := []struct {
		info string
		want string
	}{
		{"go", ""},
		{"go:main.go", "main.go"},
		{"python: src/app.py ", "app.py"},
		{"bash: /etc/passwd", "passwd"},
		{"yaml:../secret.yml", "secret.yml"},
	}

	for _, tt := range tests {
		if got := artifactName(tt.info); got != tt.want {
			t.Errorf("artifactName(%q) = %q, want %q", tt.info, got, tt.want)
		}
	}
}

func TestIsLocal(t *testing.T) {
	if !isLocal("http://localhost:11434/v1") {
		t.Error("localhost should be local")
	}
	if !isLocal("http://127.0.0.1:8080") {
		t.Error("127.0.0.1 should be local")
	}
	if isLocal("https://api.openai.com/v1") {
		t.Error("openai is not local")
	}
}
