package i18n

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/unxed/f4/internal/ini"
)

// Settings is a primary application surface: do not silently fall back to
// English in one of its supported languages, including option explanations.
func TestSettingsTranslationsComplete(t *testing.T) {
	load := func(path string) map[string]string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return LoadLangMapFromINI(ini.Parse(strings.NewReader(string(data))))
	}
	source := load("lang/en.lng")
	files, err := filepath.Glob("lang/*.lng")
	if err != nil || len(files) < 2 {
		t.Fatalf("language files: %v, %v", files, err)
	}
	formats := regexp.MustCompile(`%[A-Z][A-Za-z]+|%[sdwvq]`)
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			translated := load(file)
			for key, value := range source {
				if !strings.HasPrefix(key, "SettingsCenter.") {
					continue
				}
				text := translated[key]
				if strings.TrimSpace(text) == "" {
					t.Errorf("missing %s", key)
					continue
				}
				if !reflect.DeepEqual(formats.FindAllString(value, -1), formats.FindAllString(text, -1)) {
					t.Errorf("%s: format substitutions differ", key)
				}
				if strings.Count(value, "\n") != strings.Count(text, "\n") {
					t.Errorf("%s: line breaks differ", key)
				}
				for _, literal := range []string{
					"us-east-1", "colorer/configs", "Macros/scripts", "vtvibe.ini",
					"indent_style", "indent_size", "tab_width",
				} {
					if strings.Contains(value, literal) && !strings.Contains(text, literal) {
						t.Errorf("%s: missing literal %q", key, literal)
					}
				}
			}
		})
	}
}
