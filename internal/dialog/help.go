package dialog

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	embedded "github.com/unxed/f4"
	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/internal/ini"
	"github.com/unxed/vtui"
)

//go:embed help/en.hlf
var DefaultHelpData string

// README.md lives in the repository root (it must render on GitHub), out of
// reach of this directory's embed directive; the root "embedded" package bridges it.
var ReadmeData = embedded.ReadmeMD

type MemoryHelpVFS struct {
	files map[string]string
}

// NewMemoryHelpVFS wraps the in-memory help files the root assembled. The map
// is taken as given: the caller owns it and this does not copy it.
func NewMemoryHelpVFS(files map[string]string) *MemoryHelpVFS {
	return &MemoryHelpVFS{files: files}
}

func (m *MemoryHelpVFS) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	content, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

var mdLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

func convertMarkdownLinks(line string) string {
	return mdLinkRegex.ReplaceAllString(line, "~$1~$2@")
}

func ParseMarkdownToHelpTopic(name string, mdContent string) *vtui.HelpTopic {
	topic := &vtui.HelpTopic{
		Name: name,
	}
	lines := strings.Split(mdContent, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			headerText := strings.TrimSpace(trimmed[level:])
			line = "#" + headerText + "#"
			if level == 1 && topic.StickyRows == 0 {
				topic.StickyRows = 1
				topic.Lines = append([]string{headerText}, topic.Lines...)
				continue
			}
			line = convertMarkdownLinks(line)
			topic.Lines = append(topic.Lines, line)
			continue
		}

		wrapped := vtui.WrapText(line, GeneratedHelpLineWidth)
		for _, wLine := range wrapped {
			wLine = convertMarkdownLinks(wLine)
			topic.Lines = append(topic.Lines, wLine)
		}
	}
	return topic
}

// HelpActionStrings holds the .lng strings of the configured help
// language. Generated key topics use it so their language matches the
// static .hlf content even when it differs from the UI language.
var HelpActionStrings map[string]string

const GeneratedHelpLineWidth = 70

func AppendGeneratedHelpAction(topic *vtui.HelpTopic, keys, desc string) {
	prefix := fmt.Sprintf("  %-14s - ", keys)
	// Keep the action description as one source line. HelpView wraps it to the
	// actual window width, so maximizing Help can reflow the line instead of
	// retaining breaks made for the old 70-column layout (f4 #378).
	topic.Lines = append(topic.Lines, prefix+desc)
}

// HelpMsg resolves an i18n key preferring the help language strings,
// falling back to the UI language.
func HelpMsg(key string) string {
	if HelpActionStrings != nil {
		if s, ok := HelpActionStrings[key]; ok {
			return s
		}
	}
	return i18n.Msg(key)
}

// LoadHelpLangStrings loads the .lng map for a help language code.
// Returns nil for English (embedded strings already cover it via i18n.Msg)
// or when no language file is found.
func LoadHelpLangStrings(code string) map[string]string {
	if code == "" || code == "en" || code == "eng" {
		return nil
	}
	if !i18n.SafeLanguageCode(code) {
		return nil
	}
	for _, dir := range i18n.SearchDirs(config.LocalLangDir()) {
		cand := filepath.Join(dir, code+".lng")
		// #nosec G703 -- i18n.SafeLanguageCode rejects separators and ".." before code is used as a path component.
		if _, err := os.Stat(cand); err == nil {
			return i18n.LoadLangMapFromINI(ini.Load(cand))
		}
	}
	// No pack on disk: the one embedded in this binary says the same thing.
	// Generated help used to go untranslated in an installation that carries no
	// lang/ directory, which is every single-binary one.
	if embedded := i18n.LoadEmbeddedLanguageMap(code); len(embedded) > 0 {
		return embedded
	}
	return nil
}

var visRenHelpSections = []string{
	"VisRenQuickStart", "VisRenMasks", "VisRenTransforms", "VisRenMetadata",
	"VisRenSearch", "VisRenPreview", "VisRenEditor", "VisRenRename",
	"VisRenSafety", "VisRenExamples",
}

// FlattenVisRenHelp keeps the linked detail topics for quick navigation while
// also appending their complete contents to the main topic. This lets readers
// browse all of VisRen's help continuously with PgDn.
func FlattenVisRenHelp(Engine *vtui.HelpEngine) {
	if Engine == nil {
		return
	}
	index := Engine.GetTopic("VisRen")
	if index == nil {
		return
	}
	for _, name := range visRenHelpSections {
		section := Engine.GetTopic(name)
		if section == nil || len(section.Lines) == 0 {
			continue
		}
		title := strings.TrimSpace(section.Lines[0])
		index.Lines = append(index.Lines, "", "#"+title+"#")
		start := section.StickyRows
		if start < 0 || start > len(section.Lines) {
			start = 0
		}
		index.Lines = append(index.Lines, section.Lines[start:]...)
	}
}
