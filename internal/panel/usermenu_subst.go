package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// User-menu command substitution. This is a minimal subset of
// far2l's fnparce.cpp (~870 lines of C++) covering what's actually used
// in user_menu.ini files seen in practice. Tokens not in this list are
// passed through verbatim so the shell sees them unmodified; we can
// extend the set incrementally based on real-world configs.
//
// Supported tokens (active panel by default, '!#' switches to passive):
//
//	!!              literal '!'
//	!.!             file name under cursor (with extension)
//	!`!             extension of the cursor file, including the dot
//	!\ or !\!       current panel directory
//	!&              space-joined basenames of marked files (or cursor file)
//	!@!             path of a temp file containing one marked basename per line
//	!?title?init!   prompt the user for input; init is the prefilled value
//	!#              switch subsequent tokens to the passive panel
//	!^              switch subsequent tokens back to the active panel
//	!~!             file name under cursor without extension
//	!/ or !/!       current directory path without trailing slash
//	!:              current drive letter or scheme with colon
//
// !\ and !/ close with '!' only to delimit an immediately following token
// (e.g. "!/!.!" is the directory then the file name); on their own the
// closing '!' is optional, matching far2l's fnparce.cpp.
//
// Shell variable references such as $VAR and ${VAR} are intentionally passed
// through unchanged. The command is expanded by the target shell; resolving
// them here would make the result depend on f4's environment and would turn
// an unset variable into an empty command fragment.

// PanelSnapshot is a minimal snapshot of one panel's state, used as input
// to SubstFileName. Snapshot is taken before the menu opens so a slow
// substitution (e.g. !?...!) can't race against panel updates.
type PanelSnapshot struct {
	CurDir      string
	CurrentFile string   // basename, "" if no file under cursor or it's ".."
	Marked      []string // basenames; falls back to []string{CurrentFile} if nothing was explicitly marked
}

// SubstContext bundles everything SubstFileName needs.
type SubstContext struct {
	Active  PanelSnapshot
	Passive PanelSnapshot

	// AskUser is called for !?title?init! tokens. The callback should
	// return the entered text and ok=true on Enter, or "", false on Esc /
	// cancel. If nil, !?...! tokens expand to their default ("init").
	AskUser func(title, init string) (text string, ok bool)

	// MarkedListTempDir overrides the temp directory for !@! files
	// (mainly for tests). Defaults to os.TempDir().
	MarkedListTempDir string

	// Label expands a menu item's label rather than its command: !@!
	// stays as written, so showing a menu writes no list file.
	Label bool
}

// SubstResult holds the outcome of a substitution.
type SubstResult struct {
	Command   string
	TempFiles []string // !@! files created; caller removes them after command runs
	Cancelled bool     // user dismissed a !?...! prompt
	ListFiles bool     // true if any !@!/!&-like aggregate token was used
}

func SubstFileName(cmd string, ctx *SubstContext) SubstResult {
	if ctx == nil {
		return SubstResult{Command: cmd}
	}

	var (
		out       strings.Builder
		temp      []string
		cancelled bool
		listFiles bool
	)

	usePassive := false
	src := cmd
	i := 0
	for i < len(src) {
		if src[i] != '!' {
			out.WriteByte(src[i])
			i++
			continue
		}
		// At a '!' — try every supported token in priority order.
		rest := src[i:]

		// !! → literal '!'
		if strings.HasPrefix(rest, "!!") {
			out.WriteByte('!')
			i += 2
			continue
		}
		// !# / !^ — flip the "which panel is the source"
		if strings.HasPrefix(rest, "!#") {
			usePassive = true
			i += 2
			continue
		}
		if strings.HasPrefix(rest, "!^") {
			usePassive = false
			i += 2
			continue
		}
		// !?title?init!
		if strings.HasPrefix(rest, "!?") {
			body, consumed, ok := parsePromptToken(rest)
			if ok {
				title, init, _ := splitPromptBody(body)
				value := init
				if ctx.AskUser != nil {
					answered, accepted := ctx.AskUser(title, init)
					if !accepted {
						return SubstResult{Cancelled: true}
					}
					value = answered
				}
				out.WriteString(value)
				i += consumed
				continue
			}
		}

		panel := &ctx.Active
		if usePassive {
			panel = &ctx.Passive
		}

		// !.!  →  current file (basename)
		if strings.HasPrefix(rest, "!.!") {
			out.WriteString(panel.CurrentFile)
			i += 3
			continue
		}
		// !`!  →  extension including the dot
		if strings.HasPrefix(rest, "!`!") {
			out.WriteString(filepath.Ext(panel.CurrentFile))
			i += 3
			continue
		}
		// !~!  →  file name without extension
		if strings.HasPrefix(rest, "!~!") {
			ext := filepath.Ext(panel.CurrentFile)
			out.WriteString(strings.TrimSuffix(panel.CurrentFile, ext))
			i += 3
			continue
		}
		// !\!  →  current directory
		if strings.HasPrefix(rest, "!\\!") {
			out.WriteString(panel.CurDir)
			i += 3
			continue
		}
		// !/!  →  current directory path without trailing slash
		if strings.HasPrefix(rest, "!/!") {
			out.WriteString(trimTrailingSlash(panel.CurDir))
			i += 3
			continue
		}
		// !\  / !/  (bare, no closing '!') → same as above. far2l's fnparce.cpp
		// treats the closing '!' as optional: it only matters to delimit an
		// immediately following token (e.g. "!/!.!" is the path token then the
		// filename token), so a standalone "!/" with nothing else after it is
		// already a complete, valid token (f4 #1395).
		if strings.HasPrefix(rest, "!\\") {
			out.WriteString(panel.CurDir)
			i += 2
			continue
		}
		if strings.HasPrefix(rest, "!/") {
			out.WriteString(trimTrailingSlash(panel.CurDir))
			i += 2
			continue
		}
		// !:  →  current drive letter or scheme with colon (e.g. C:)
		if strings.HasPrefix(rest, "!:") {
			vol := ""
			if len(panel.CurDir) >= 2 && panel.CurDir[1] == ':' {
				c := panel.CurDir[0]
				if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
					vol = string(panel.CurDir[:2])
				}
			}
			if vol == "" {
				vol = filepath.VolumeName(panel.CurDir)
				if vol == "" {
					if strings.HasPrefix(panel.CurDir, "/") {
						vol = "/"
					}
				}
			}
			out.WriteString(vol)
			i += 2
			continue
		}
		// !&  →  space-joined marked basenames. far2l does not quote;
		// authors are expected to do their own quoting (e.g. "!.!") or
		// use !@! for filenames with spaces.
		if strings.HasPrefix(rest, "!&") {
			files := marked(panel)
			out.WriteString(strings.Join(files, " "))
			listFiles = true
			i += 2
			continue
		}
		// !@!  →  path of a temp file with marked basenames (one per line)
		if strings.HasPrefix(rest, "!@!") {
			if ctx.Label {
				out.WriteString("!@!")
				i += 3
				continue
			}
			path, err := writeMarkedListFile(marked(panel), ctx.MarkedListTempDir)
			if err == nil {
				out.WriteString(path)
				temp = append(temp, path)
				listFiles = true
			}
			i += 3
			continue
		}

		// Not a recognized token: pass the '!' through and move on.
		out.WriteByte('!')
		i++
	}

	return SubstResult{
		// zoin-bot: Preserve shell variables for the command interpreter instead
		// of expanding them in f4 before the command is launched.
		Command:   out.String(),
		TempFiles: temp,
		Cancelled: cancelled,
		ListFiles: listFiles,
	}
}

// SubstMenuLabel expands the metasymbols in a menu item's label for the
// menu to show, as Far shows "!\\" in a label as the folder of the panel
// (f4#1395). Nothing is asked: a prompt shows its initial value.
func SubstMenuLabel(label string, ctx *SubstContext) string {
	if ctx == nil || !strings.Contains(label, "!") {
		return label
	}
	labelCtx := *ctx
	labelCtx.AskUser = nil
	labelCtx.Label = true
	return SubstFileName(label, &labelCtx).Command
}

// parsePromptToken returns the body between !? and the closing !, plus
// the total bytes consumed (including the opening !? and closing !).
// Returns ok=false if the token is malformed (no closing !).
func parsePromptToken(s string) (body string, consumed int, ok bool) {
	if !strings.HasPrefix(s, "!?") {
		return "", 0, false
	}
	end := strings.IndexByte(s[2:], '!')
	if end < 0 {
		return "", 0, false
	}
	return s[2 : 2+end], 2 + end + 1, true
}

// splitPromptBody parses "title?init" out of the body. If no '?' is
// present, the whole body is treated as title and init is empty. far2l
// has additional history-hint syntax ("title?$history?init") that we
// don't (yet) implement.
func splitPromptBody(body string) (title, init string, hasInit bool) {
	q := strings.IndexByte(body, '?')
	if q < 0 {
		return body, "", false
	}
	return body[:q], body[q+1:], true
}

// trimTrailingSlash drops one trailing path separator, so "!/"/"!/!" never
// hand the shell a directory with a doubled slash when it's joined with more
// path text.
func trimTrailingSlash(dir string) string {
	if len(dir) > 1 && (strings.HasSuffix(dir, "/") || strings.HasSuffix(dir, "\\")) {
		return dir[:len(dir)-1]
	}
	return dir
}

func marked(p *PanelSnapshot) []string {
	if len(p.Marked) > 0 {
		return p.Marked
	}
	if p.CurrentFile != "" {
		return []string{p.CurrentFile}
	}
	return nil
}

func writeMarkedListFile(files []string, tmpDir string) (string, error) {
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	f, err := os.CreateTemp(tmpDir, "f4-menu-list-*.lst")
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, name := range files {
		if _, err := fmt.Fprintln(f, name); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}
