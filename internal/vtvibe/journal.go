package vtvibe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"sync"
)

// Undoing a worker's changes (unxed/f4#1842, docs/VTVIBE.md § 19a.9, stage
// H9, item 6). Claude Code and Cursor keep checkpoints, OpenCode has /undo;
// a worker here changed the user's files with no way back but by hand.
// Before write_file or edit_file first touches a file, the journal keeps
// what the file was (or that it did not exist); Undo puts every touched
// file back. What the worker did through the shell is not journaled.

// maxJournaledFile is the largest file the journal keeps a copy of.
const maxJournaledFile = 16 << 20

type snapshot struct {
	existed bool
	data    []byte
	mode    fs.FileMode
	// tooLarge marks a file the journal could not keep; Undo reports it.
	tooLarge bool
}

// Journal remembers the files a run changed, as they were before.
type Journal struct {
	mu    sync.Mutex
	files map[string]snapshot
}

// record keeps path as it is now, the first time it is about to change.
func (j *Journal) record(path string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.files == nil {
		j.files = map[string]snapshot{}
	}
	if _, seen := j.files[path]; seen {
		return
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		j.files[path] = snapshot{}
	case err != nil || !info.Mode().IsRegular():
		j.files[path] = snapshot{existed: true, tooLarge: true}
	case info.Size() > maxJournaledFile:
		j.files[path] = snapshot{existed: true, tooLarge: true}
	default:
		data, err := os.ReadFile(path) // #nosec G304 G703 -- the file the worker is about to change
		j.files[path] = snapshot{existed: true, data: data, mode: info.Mode().Perm(), tooLarge: err != nil}
	}
}

// Files lists the files the run changed, sorted.
func (j *Journal) Files() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, 0, len(j.files))
	for p := range j.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Undo puts every changed file back as it was and forgets them: a file the
// run created is removed. It returns the files restored and an error naming
// those that could not be.
func (j *Journal) Undo() ([]string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var done []string
	var errs []error
	for path, s := range j.files {
		var err error
		switch {
		case s.tooLarge:
			err = fmt.Errorf("%s: no copy was kept (too large or unreadable)", path)
		case !s.existed:
			if err = os.Remove(path); errors.Is(err, fs.ErrNotExist) { // #nosec G703 -- a file this run created
				err = nil
			}
		default:
			err = os.WriteFile(path, s.data, s.mode) // #nosec G306 G703 -- the file's own content and mode, as they were
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		done = append(done, path)
	}
	j.files = nil
	sort.Strings(done)
	return done, errors.Join(errs...)
}

// WithJournal makes the file-changing tools among tools record into j
// before they change a file.
func WithJournal(tools []Tool, dir string, j *Journal) []Tool {
	out := make([]Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		if t.Name != "write_file" && t.Name != "edit_file" {
			continue
		}
		run := t.Run
		out[i].Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(raw, &args) == nil {
				if path, err := resolvePath(dir, args.Path); err == nil {
					j.record(path)
				}
			}
			return run(ctx, raw)
		}
	}
	return out
}
