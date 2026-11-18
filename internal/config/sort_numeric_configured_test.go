package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSortNumericConfigured(t *testing.T) {
	tests := []struct {
		name string
		ini  string
		want bool
	}{
		{name: "missing setting"},
		{name: "explicitly disabled", ini: "[Panel]\nSortNumeric = 0\n", want: true},
		{name: "explicitly enabled", ini: "[Panel]\nSortNumeric = 1\n", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.ini")
			if test.ini != "" {
				if err := os.WriteFile(path, []byte(test.ini), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			oldUserPath, oldConfigPaths, oldConfig := GetUserConfigIniPath, GetConfigIniPaths, App
			GetUserConfigIniPath = func() string { return path }
			GetConfigIniPaths = func() []string { return []string{path} }
			t.Cleanup(func() {
				GetUserConfigIniPath, GetConfigIniPaths, App = oldUserPath, oldConfigPaths, oldConfig
				LoadConfig()
			})

			LoadConfig()
			if got := SortNumericConfigured(); got != test.want {
				t.Fatalf("SortNumericConfigured() = %v, want %v", got, test.want)
			}
		})
	}
}
