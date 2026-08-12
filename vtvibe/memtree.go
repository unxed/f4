package vtvibe

import (
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxFileBytes caps a single file kept in the session. The whole context lives
// in RAM, so a stray "copy the 2 GB dump into the dialog" must fail loudly
// instead of taking the process down.
const maxFileBytes = 8 << 20

// maxTreeBytes caps the whole session tree for the same reason.
const maxTreeBytes = 64 << 20

// memNode is one file or directory of the session tree.
type memNode struct {
	name  string
	isDir bool
	data  []byte
	mtime time.Time
}

// memTree is a tiny in-memory filesystem addressed by clean, slash separated
// absolute paths. "/" is the root and always exists.
type memTree struct {
	mu    sync.RWMutex
	nodes map[string]*memNode
}

func newMemTree() *memTree {
	t := &memTree{nodes: make(map[string]*memNode)}
	t.nodes["/"] = &memNode{name: "/", isDir: true, mtime: time.Now()}
	return t
}

// CleanPath normalizes any path the panel hands us into the tree's own form.
func CleanPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	if p == "." {
		return "/"
	}
	return p
}

func (t *memTree) stat(p string) (memNode, bool) {
	p = CleanPath(p)
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, ok := t.nodes[p]
	if !ok {
		return memNode{}, false
	}
	return *n, true
}

func (t *memTree) list(dir string) []memNode {
	dir = CleanPath(dir)
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	t.mu.RLock()
	var out []memNode
	for p, n := range t.nodes {
		if p == dir || !strings.HasPrefix(p, prefix) {
			continue
		}
		if strings.Contains(strings.TrimPrefix(p, prefix), "/") {
			continue // not a direct child
		}
		out = append(out, *n)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].isDir != out[j].isDir {
			return out[i].isDir
		}
		return out[i].name < out[j].name
	})
	return out
}

// walkFiles returns every file below dir, sorted by path, so callers that
// serialize the tree produce byte-identical output for identical input.
func (t *memTree) walkFiles(dir string) []string {
	dir = CleanPath(dir)
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	t.mu.RLock()
	var out []string
	for p, n := range t.nodes {
		if n.isDir || !strings.HasPrefix(p, prefix) {
			continue
		}
		out = append(out, p)
	}
	t.mu.RUnlock()
	sort.Strings(out)
	return out
}

func (t *memTree) mkdirAll(p string) error {
	p = CleanPath(p)
	if p == "/" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	cur := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		cur += "/" + part
		if n, ok := t.nodes[cur]; ok {
			if !n.isDir {
				return os.ErrExist
			}
			continue
		}
		t.nodes[cur] = &memNode{name: part, isDir: true, mtime: time.Now()}
	}
	return nil
}

func (t *memTree) totalBytes() int {
	total := 0
	for _, n := range t.nodes {
		total += len(n.data)
	}
	return total
}

func (t *memTree) writeFile(p string, data []byte) error {
	p = CleanPath(p)
	if p == "/" {
		return os.ErrInvalid
	}
	if len(data) > maxFileBytes {
		return errTooLarge
	}
	if err := t.mkdirAll(path.Dir(p)); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if n, ok := t.nodes[p]; ok && n.isDir {
		return os.ErrExist
	}
	if old, ok := t.nodes[p]; ok {
		if t.totalBytes()-len(old.data)+len(data) > maxTreeBytes {
			return errTooLarge
		}
	} else if t.totalBytes()+len(data) > maxTreeBytes {
		return errTooLarge
	}
	t.nodes[p] = &memNode{name: path.Base(p), data: data, mtime: time.Now()}
	return nil
}

func (t *memTree) readFile(p string) ([]byte, bool) {
	n, ok := t.stat(p)
	if !ok || n.isDir {
		return nil, false
	}
	return n.data, true
}

func (t *memTree) remove(p string) error {
	p = CleanPath(p)
	if p == "/" {
		return os.ErrPermission
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.nodes[p]; !ok {
		return os.ErrNotExist
	}
	delete(t.nodes, p)
	prefix := p + "/"
	for k := range t.nodes {
		if strings.HasPrefix(k, prefix) {
			delete(t.nodes, k)
		}
	}
	return nil
}

func (t *memTree) rename(from, to string) error {
	from, to = CleanPath(from), CleanPath(to)
	if from == "/" || to == "/" {
		return os.ErrPermission
	}
	if err := t.mkdirAll(path.Dir(to)); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	src, ok := t.nodes[from]
	if !ok {
		return os.ErrNotExist
	}
	if _, exists := t.nodes[to]; exists {
		return os.ErrExist
	}
	moved := map[string]*memNode{}
	prefix := from + "/"
	for k, v := range t.nodes {
		if strings.HasPrefix(k, prefix) {
			moved[to+strings.TrimPrefix(k, from)] = v
			delete(t.nodes, k)
		}
	}
	delete(t.nodes, from)
	src.name = path.Base(to)
	t.nodes[to] = src
	for k, v := range moved {
		t.nodes[k] = v
	}
	return nil
}
