package chroma

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/unxed/f4/internal/theme"
	"github.com/unxed/f4/vfs"
	"github.com/unxed/vtui"
)

// Plugin is the internal plugin wrapper for the Chroma syntax highlighter.
type Plugin struct{}

func (p *Plugin) Init(api vfs.HostAPI) error {
	api.RegisterHighlighter(&ChromaProvider{})
	return nil
}

func (p *Plugin) Close() error    { return nil }
func (p *Plugin) GetName() string { return "Internal Syntax Highlighter (Chroma)" }

// syntaxSlots links Chroma token types to f4 theme color slots (f4#1470).
// The actual RGB value for each slot lives in vtui.Palette and is resolved
// at call time (see GetSyntaxAttr/SyntaxMap), so it tracks whichever f4
// theme is active (internal/theme) instead of being a fixed palette baked
// into this plugin — exactly like the rest of f4's themed UI elements.
var syntaxSlots = map[chroma.TokenType]int{
	chroma.Comment:        theme.ColEditorSyntaxComment,
	chroma.Keyword:        theme.ColEditorSyntaxKeyword,
	chroma.String:         theme.ColEditorSyntaxString,
	chroma.Number:         theme.ColEditorSyntaxNumber,
	chroma.Operator:       theme.ColEditorSyntaxOperator,
	chroma.NameFunction:   theme.ColEditorSyntaxFunction,
	chroma.NameVariable:   theme.ColEditorSyntaxVariable,
	chroma.GenericHeading: theme.ColEditorSyntaxHeading,
}

// SyntaxMap returns the current token-type -> RGB color mapping, read live
// from the active f4 theme's palette (vtui.Palette). It replaces the old
// hardcoded package-level map of the same name: callers that want "the
// current syntax colors" get exactly that, including right after a theme
// switch, without needing to restart f4.
func SyntaxMap() map[chroma.TokenType]uint32 {
	m := make(map[chroma.TokenType]uint32, len(syntaxSlots))
	for t, idx := range syntaxSlots {
		m[t] = vtui.GetRGBFore(vtui.Palette[idx])
	}
	return m
}

// GetSyntaxAttr returns vtui attributes for a specific token type, using
// whichever color the active f4 theme currently assigns to that token's
// slot (see syntaxSlots).
func GetSyntaxAttr(t chroma.TokenType, baseAttr uint64) uint64 {
	for t != chroma.None {
		if idx, ok := syntaxSlots[t]; ok {
			return vtui.SetRGBFore(baseAttr, vtui.GetRGBFore(vtui.Palette[idx]))
		}
		p := t.Parent()
		if p == t {
			break
		}
		t = p
	}
	return baseAttr
}

// ChromaProvider implements vtui.HighlighterProvider using the chroma library.
type ChromaProvider struct{}

func (p *ChromaProvider) Name() string { return "Chroma" }

func (p *ChromaProvider) Match(filename string, content string) bool {
	return lexers.Match(filename) != nil || lexers.Analyse(content) != nil
}

func (p *ChromaProvider) Create(filename string, content string) vtui.Highlighter {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	return &ChromaHighlighter{lexer: lexer}
}

// ChromaHighlighter implements vtui.Highlighter.
type ChromaHighlighter struct {
	lexer chroma.Lexer
}

func (c *ChromaHighlighter) Highlight(line string, prevState any, baseAttr uint64) ([]uint64, any) {
	iterator, err := c.lexer.Tokenise(nil, line)
	if err != nil {
		return nil, nil
	}

	var attrs []uint64
	for _, token := range iterator.Tokens() {
		attr := GetSyntaxAttr(token.Type, baseAttr)
		runes := []rune(token.Value)
		for range runes {
			attrs = append(attrs, attr)
		}
	}
	return attrs, nil
}
