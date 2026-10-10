package vtvibe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dialogs on disk (unxed/f4#1842, docs/VTVIBE.md § 19a, stage H3, first
// step): the dialog used to live only in memory and was gone with f4. With a
// store path set, the session writes itself to disk after every change that
// matters (a new message, a name, a reset, the patch mode) and reads itself
// back when f4 starts again. Detaching and picking up a dialog from another
// f4 is the next step.

const storeVersion = 1

type savedDialog struct {
	Version   int               `json:"version"`
	Title     string            `json:"title,omitempty"`
	PatchMode bool              `json:"patch_mode,omitempty"`
	Turns     []Turn            `json:"turns"`
	Context   map[string][]byte `json:"context,omitempty"` // ctx/ files by path
	Draft     string            `json:"draft,omitempty"`
	Orders    []Order           `json:"orders,omitempty"`
	Mode      Mode              `json:"mode,omitempty"`
	// GitHubToken is the dialog's own token; the file is 0600.
	GitHubToken string `json:"github_token,omitempty"`
	// Spent is what the dialog has spent, by model.
	Spent map[string]Usage `json:"spent,omitempty"`
	// Applied lists the patches of the model the user applied.
	Applied []AppliedPatch `json:"applied,omitempty"`
}

// SetStorePath makes path the dialog's file: a dialog saved there earlier is
// restored now, and every later change is written back. An empty path stops
// saving.
func (s *Session) SetStorePath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storePath = ""
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) // #nosec G304 G703 -- the host's own file in the f4 config directory
	switch {
	case err == nil:
		var d savedDialog
		if err := json.Unmarshal(data, &d); err != nil {
			s.storePath = path
			return fmt.Errorf("vtvibe: %s is damaged, starting a new dialog: %w", path, err)
		}
		s.restoreLocked(d)
	case !os.IsNotExist(err):
		return err
	}
	s.storePath = path
	return nil
}

// restoreLocked replaces the dialog with d. Caller holds s.mu.
func (s *Session) restoreLocked(d savedDialog) {
	s.treeMu.Lock()
	s.reset()
	s.turns = nil
	for _, t := range d.Turns {
		s.appendTurn(t)
	}
	for p, data := range d.Context {
		clean := "/" + strings.TrimLeft(filepathToSlash(p), "/")
		if strings.HasPrefix(clean, ctxDir+"/") && !strings.Contains(clean, "/../") {
			_ = s.tree.writeFile(clean, data)
		}
	}
	if d.Draft != "" {
		_ = s.tree.writeFile(draftFile, []byte(d.Draft))
	}
	s.treeMu.Unlock()
	s.title = d.Title
	s.apMode = d.PatchMode
	s.orders = d.Orders
	s.mode = d.Mode
	s.githubToken = d.GitHubToken
	s.spent = d.Spent
	s.applied = d.Applied
	s.writeSessionFile()
}

func filepathToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// saveLocked writes the dialog to its store path, if it has one. A failed
// write is kept for StoreError and retried with the next change. Caller holds
// s.mu.
func (s *Session) saveLocked() {
	if s.storePath == "" {
		return
	}
	d := savedDialog{Version: storeVersion, Title: s.title, PatchMode: s.apMode, Turns: s.turns, Orders: s.orders, Mode: s.mode, GitHubToken: s.githubToken, Spent: s.spent, Applied: s.applied}
	for _, p := range s.tree.walkFiles(ctxDir) {
		if data, ok := s.tree.readFile(p); ok {
			if d.Context == nil {
				d.Context = map[string][]byte{}
			}
			d.Context[p] = data
		}
	}
	if draft, ok := s.tree.readFile(draftFile); ok && string(draft) != draftTemplate {
		d.Draft = string(draft)
	}
	s.storeErr = writeJSONAtomically(s.storePath, d)
}

// StoreError is the last failure to save the dialog, nil when it is saved.
func (s *Session) StoreError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storeErr
}

// Archive copies the saved dialog into dir under a name made of the time and
// the dialog's title, and returns that path; "" when there is nothing saved.
func (s *Session) Archive(dir string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storePath == "" || len(s.turns) <= 1 {
		return "", nil
	}
	s.saveLocked()
	data, err := os.ReadFile(s.storePath) // #nosec G304 G703 -- the host's own dialog file
	if err != nil {
		return "", err
	}
	name := time.Now().Format("2006-01-02_150405")
	if slug := archiveSlug(s.title); slug != "" {
		name += "_" + slug
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Two dialogs put aside within one second under one name must not
	// overwrite each other.
	target := filepath.Join(dir, name+".json")
	for n := 2; ; n++ {
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 G703 -- the archive directory and a slug cleaned of path characters
		if os.IsExist(err) {
			target = filepath.Join(dir, fmt.Sprintf("%s-%d.json", name, n))
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return target, werr
	}
}

func archiveSlug(title string) string {
	var sb strings.Builder
	for _, r := range title {
		switch {
		case r == ' ' || r == '-' || r == '_':
			sb.WriteRune('-')
		case strings.ContainsRune(`/\:*?"<>|`, r) || r < ' ':
		default:
			sb.WriteRune(r)
		}
		if sb.Len() >= 40 {
			break
		}
	}
	return strings.Trim(sb.String(), "-")
}

func writeJSONAtomically(path string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dialog-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil { // #nosec G703 -- path is the host's own dialog file
		_ = os.Remove(name)
		return err
	}
	return nil
}

// ArchivedDialog describes one dialog put aside by ai:new.
type ArchivedDialog struct {
	Path     string
	Title    string
	Messages int
	Saved    time.Time
}

// ListArchive returns the dialogs in dir, newest first. Files that are not
// readable dialogs are skipped.
func ListArchive(dir string) ([]ArchivedDialog, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ArchivedDialog
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(p) // #nosec G304 G703 -- the host's own archive directory
		if err != nil {
			continue
		}
		var d savedDialog
		if json.Unmarshal(data, &d) != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		messages := 0
		for _, t := range d.Turns {
			if t.Role == "user" || t.Role == "assistant" {
				messages++
			}
		}
		out = append(out, ArchivedDialog{Path: p, Title: d.Title, Messages: messages, Saved: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Saved.After(out[j].Saved) })
	return out, nil
}

// OpenArchived makes the archived dialog at path the current one: the
// current dialog goes to archiveDir first (when it has anything in it), the
// chosen one is restored and leaves the archive, so it is not kept twice.
func (s *Session) OpenArchived(path, archiveDir string) error {
	data, err := os.ReadFile(path) // #nosec G304 G703 -- a file ListArchive returned
	if err != nil {
		return err
	}
	var d savedDialog
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("vtvibe: %s is damaged: %w", path, err)
	}
	if _, err := s.Archive(archiveDir); err != nil {
		return err
	}
	s.mu.Lock()
	storePath := s.storePath
	s.storePath = ""
	s.restoreLocked(d)
	s.storePath = storePath
	s.saveLocked()
	err = s.storeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return os.Remove(path) // #nosec G703 -- a file ListArchive returned
}
