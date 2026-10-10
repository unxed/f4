package vtvibe

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

// Shortening the model's own answers (unxed/f4#1842, docs/VTVIBE.md
// § 19a.3, stage H4, and § 19a.9 item 10). When the main dialog no longer
// fits the model's context, f4 sends it again with the model's earlier
// answers shortened — code blocks reduced to a mark, long text cut — and
// the user's messages in full: what the user wrote is never shortened. The
// dialog itself is kept whole; only that request is shortened.

// keepFullAnswers is how many of the latest answers stay whole.
const keepFullAnswers = 2

// maxShortAnswer bounds an older answer once shortened, in runes.
const maxShortAnswer = 1200

var fencedCode = regexp.MustCompile("(?s)```[^\\n]*\\n(.*?)```")

// shortenAnswer is an older answer of the model, made short. notes maps the
// ID of an ap patch the user applied to what replaces its code: the files it
// changed and the commit they went into (appliedNotes).
func shortenAnswer(text string, notes map[string]string) string {
	for id, note := range notes {
		if !strings.Contains(text, id) {
			continue
		}
		text = fencedCode.ReplaceAllStringFunc(text, func(block string) string {
			if strings.Contains(block, id) {
				return note
			}
			return block
		})
		// A patch given without fences: its lines all start with the ID.
		var kept []string
		noted := strings.Contains(text, note)
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, id+" ") || strings.TrimSpace(line) == id {
				if !noted {
					kept = append(kept, note)
					noted = true
				}
				continue
			}
			kept = append(kept, line)
		}
		text = strings.Join(kept, "\n")
	}
	text = fencedCode.ReplaceAllStringFunc(text, func(block string) string {
		lines := strings.Count(fencedCode.FindStringSubmatch(block)[1], "\n")
		return fmt.Sprintf("[code block of %d lines left out]", lines)
	})
	if r := []rune(text); len(r) > maxShortAnswer {
		text = string(r[:maxShortAnswer]) + " … [shortened]"
	}
	return text
}

// historyMessages turns the dialog into messages; compact shortens the
// model's answers except the latest keepFullAnswers, never the user's.
func historyMessages(history []Turn, compact bool, notes map[string]string) []Message {
	answers := 0
	for _, t := range history {
		if t.Role == "assistant" {
			answers++
		}
	}
	msgs := make([]Message, 0, len(history))
	seen := 0
	for _, t := range history {
		if t.Text == "RCtrl+A to hide" {
			continue
		}
		text := t.Text
		if t.Role == "assistant" {
			seen++
			if compact && seen <= answers-keepFullAnswers {
				text = shortenAnswer(text, notes)
			}
		}
		msgs = append(msgs, Message{Role: t.Role, Content: text})
	}
	return msgs
}

// AppliedPatch is an ap patch from the model's answer that the user applied
// (§ 19a.3): when the dialog is shortened its code gives way to the files it
// changed and the commit they went into.
type AppliedPatch struct {
	ID    string    `json:"id"`
	Root  string    `json:"root"`
	Files []string  `json:"files"`
	At    time.Time `json:"at"`
}

// NoteApplied records that the user applied patch p; it is kept with the
// dialog.
func (s *Session) NoteApplied(p AppliedPatch) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied = append(s.applied, p)
	s.saveLocked()
}

// commitOf finds the latest commit, made at since or later, that changed
// files in the repository at root; ok is false when there is none or root
// is not a repository. A variable, so tests need no git.
var commitOf = func(ctx context.Context, root string, files []string, since time.Time) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := append([]string{"-C", root, "log", "-1", "--format=%h", "--since=" + since.Format(time.RFC3339), "--"}, files...)
	out, err := exec.CommandContext(ctx, "git", args...).Output() // #nosec G204 -- fixed git subcommand; the paths are arguments after "--"
	hash := strings.TrimSpace(string(out))
	return hash, err == nil && hash != ""
}

// appliedNotes is the replacement for the code of every applied patch.
func (s *Session) appliedNotes(ctx context.Context) map[string]string {
	s.mu.Lock()
	applied := append([]AppliedPatch(nil), s.applied...)
	s.mu.Unlock()
	if len(applied) == 0 {
		return nil
	}
	notes := make(map[string]string, len(applied))
	for _, p := range applied {
		files := strings.Join(p.Files, ", ")
		if hash, ok := commitOf(ctx, p.Root, p.Files, p.At); ok {
			notes[p.ID] = fmt.Sprintf("[ap patch %s: applied to %s, committed as %s]", p.ID, files, hash)
		} else {
			notes[p.ID] = fmt.Sprintf("[ap patch %s: applied to %s, not committed yet]", p.ID, files)
		}
	}
	return notes
}

// recentUserTurns is how many of the user's latest messages decide which
// attached files are still relevant.
const recentUserTurns = 3

// relevantFiles decides which attached files still go in full: those the
// question or the user's latest messages mention, by path or by name. keep
// is nil when every file is mentioned, or there are none; leftOut names the
// others.
func (s *Session) relevantFiles(question string, history []Turn) (keep func(rel string) bool, leftOut []string) {
	texts := []string{strings.ToLower(question)}
	for i := len(history) - 1; i >= 0 && len(texts) <= recentUserTurns; i-- {
		if history[i].Role == "user" {
			texts = append(texts, strings.ToLower(history[i].Text))
		}
	}
	mentioned := func(rel string) bool {
		for _, name := range []string{strings.ToLower(rel), strings.ToLower(path.Base(rel))} {
			for _, t := range texts {
				if strings.Contains(t, name) {
					return true
				}
			}
		}
		return false
	}
	s.treeMu.RLock()
	files := s.tree.walkFiles(ctxDir)
	s.treeMu.RUnlock()
	for _, full := range files {
		if rel := strings.TrimPrefix(full, ctxDir+"/"); !mentioned(rel) {
			leftOut = append(leftOut, rel)
		}
	}
	if len(leftOut) == 0 {
		return nil, nil
	}
	return mentioned, leftOut
}

// keepImages is images without the pictures keep leaves out.
func keepImages(images []Image, keep func(rel string) bool) []Image {
	var out []Image
	for _, img := range images {
		if keep(img.Name) {
			out = append(out, img)
		}
	}
	return out
}

// compactNote tells the user, in the dialog, that a request went shortened.
const compactNote = "f4: the dialog no longer fit the model's context, so the model's earlier answers were sent shortened (your messages were sent in full; the dialog itself is kept whole)."

// compactNoteFor is compactNote, naming the attached files that were left
// out as well, if any.
func compactNoteFor(leftOut []string) string {
	if len(leftOut) == 0 {
		return compactNote
	}
	return compactNote + " Attached files nobody mentioned lately were left out of that request too: " +
		strings.Join(leftOut, ", ") + " — mention one to have it sent again."
}

// errStillTooLong explains a dialog that does not fit even shortened.
func errStillTooLong(err error) error {
	return fmt.Errorf("%w — the dialog does not fit the model's context even with the model's earlier answers shortened and unmentioned files left out; your own messages are never shortened, so start a new dialog with ai:new (this one stays in ai:dialogs)", err)
}
