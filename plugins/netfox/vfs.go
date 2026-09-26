package netfox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/unxed/f4/internal/netproxy"
	"github.com/unxed/f4/vfs"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type NetFoxConfig struct {
	Type     string            `json:"Type"`
	Host     string            `json:"Host"`
	Port     string            `json:"Port"`
	User     string            `json:"User"`
	Pass     string            `json:"Pass"`
	KeyPath  string            `json:"KeyPath,omitempty"`
	Timeout  string            `json:"Timeout,omitempty"`
	Codepage string            `json:"Codepage,omitempty"`
	Options  map[string]string `json:"Options,omitempty"`

	// Proxy overrides f4's app-wide proxy for this site alone. ProxyMode 0
	// is netproxy.ModeGlobal, so connections saved before this existed —
	// and new ones the user never touched — simply follow the app setting.
	ProxyMode int    `json:"ProxyMode,omitempty"`
	ProxyHost string `json:"ProxyHost,omitempty"`
	ProxyPort string `json:"ProxyPort,omitempty"`
	ProxyUser string `json:"ProxyUser,omitempty"`
	ProxyPass string `json:"ProxyPass,omitempty"`
}

// Proxy is the settings this connection dials through: its own when it
// overrides, the app-wide ones otherwise.
func (c NetFoxConfig) Proxy() netproxy.Settings {
	return netproxy.Resolve(netproxy.Settings{
		Mode: c.ProxyMode,
		Host: c.ProxyHost,
		Port: c.ProxyPort,
		User: c.ProxyUser,
		Pass: c.ProxyPass,
	})
}

type NetFoxVFS struct {
	mu   sync.Mutex
	path string
}

func NewNetFoxVFS(dbPath string) *NetFoxVFS {
	_ = os.MkdirAll(filepath.Dir(dbPath), 0700)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		_ = writeNetFoxFile(dbPath, []byte("{}\n"))
	}
	return &NetFoxVFS{path: dbPath}
}

func (v *NetFoxVFS) readConfigsLocked() (map[string]NetFoxConfig, error) {
	data, err := os.ReadFile(v.path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]NetFoxConfig), nil
	}
	if err != nil {
		return nil, fmt.Errorf("netfox: read connections: %w", err)
	}
	var configs map[string]NetFoxConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("netfox: damaged connections file: %w", err)
	}
	if configs == nil {
		return nil, errors.New("netfox: connections file is not a JSON object")
	}

	// Transparently decrypt passwords
	for k, cfg := range configs {
		if cfg.Pass != "" {
			cfg.Pass = deobfuscate(cfg.Pass)
		}
		if cfg.ProxyPass != "" {
			cfg.ProxyPass = deobfuscate(cfg.ProxyPass)
		}
		configs[k] = cfg
	}
	for k, cfg := range configs {
		if cfg.Codepage == "" {
			cfg.Codepage = "65001"
			configs[k] = cfg
		}
	}
	return configs, nil
}

func (v *NetFoxVFS) getConfigs() map[string]NetFoxConfig {
	v.mu.Lock()
	defer v.mu.Unlock()
	configs, err := v.readConfigsLocked()
	if err != nil {
		return make(map[string]NetFoxConfig)
	}
	return configs
}

func saveNetFoxConfigs(configs map[string]NetFoxConfig) ([]byte, error) {
	// Encrypt passwords before saving
	encodedConfigs := make(map[string]NetFoxConfig)
	for k, cfg := range configs {
		if cfg.Pass != "" {
			cfg.Pass = obfuscate(cfg.Pass)
		}
		if cfg.ProxyPass != "" {
			cfg.ProxyPass = obfuscate(cfg.ProxyPass)
		}
		encodedConfigs[k] = cfg
	}

	// Password fields have already been obfuscated above; this is the
	// persistence boundary for the encoded representation.
	data, err := json.MarshalIndent(encodedConfigs, "", "  ") // #nosec G117 -- secrets are obfuscated before serialization.
	if err != nil {
		return nil, fmt.Errorf("netfox: encode connections: %w", err)
	}
	return append(data, '\n'), nil
}

func writeNetFoxFile(path string, data []byte) (returnErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("netfox: create connections directory: %w", err)
	}
	f, err := os.CreateTemp(dir, ".netfox-*.tmp")
	if err != nil {
		return fmt.Errorf("netfox: create temporary connections file: %w", err)
	}
	tmpPath := f.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := f.Close(); returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		if returnErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	for len(data) > 0 {
		written, err := f.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func (v *NetFoxVFS) updateConfigs(mutate func(map[string]NetFoxConfig) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	configs, err := v.readConfigsLocked()
	if err != nil {
		return err
	}
	if err := mutate(configs); err != nil {
		return err
	}
	data, err := saveNetFoxConfigs(configs)
	if err != nil {
		return err
	}
	return writeNetFoxFile(v.path, data)
}

func (v *NetFoxVFS) SaveConfig(name string, cfg NetFoxConfig) error {
	return v.updateConfigs(func(configs map[string]NetFoxConfig) error {
		configs[name] = cfg
		return nil
	})
}

func (v *NetFoxVFS) IsAtRoot() bool         { return true }
func (v *NetFoxVFS) GetPath() string        { return "net://" }
func (v *NetFoxVFS) IsAbs(p string) bool    { return strings.HasPrefix(p, "net://") }
func (v *NetFoxVFS) SetPath(p string) error { return nil }

// HistoryEntry implements vfs.HistoryPathProvider. The root screen is a list
// of saved connections, not a folder, so it never has anything worth
// remembering in folder history (f4#262): without this, the bare "net://"
// GetPath() above fell through to ShouldRecordFolderHistory's "no parent
// VFS, trust it as a local path" rule and was recorded as a useless entry
// point with no navigational value.
func (v *NetFoxVFS) HistoryEntry() (display, ref string, ok bool) { return "", "", false }

// NavigateHistoryEntry implements vfs.HistoryPathProvider. HistoryEntry
// above never produces an entry for the root screen, so this is never
// reached through the normal folder-history flow; it exists only to satisfy
// the interface and always declines.
func (v *NetFoxVFS) NavigateHistoryEntry(ref string) bool { return false }

func (v *NetFoxVFS) ReadDir(ctx context.Context, p string, onChunk func([]vfs.VFSItem)) error {
	configs := v.getConfigs()
	var items []vfs.VFSItem
	items = append(items, vfs.VFSItem{Name: "<Add connection>", IsDir: false})
	for name := range configs {
		items = append(items, vfs.VFSItem{Name: name, IsDir: false})
	}
	if len(items) > 0 {
		onChunk(items)
	}
	return nil
}

func (v *NetFoxVFS) Stat(ctx context.Context, p string) (vfs.VFSItem, error) {
	name := v.Base(p)
	if name == "<Add connection>" {
		return vfs.VFSItem{Name: name, IsDir: false}, nil
	}
	configs := v.getConfigs()
	if _, ok := configs[name]; ok {
		return vfs.VFSItem{Name: name, IsDir: false}, nil
	}
	return vfs.VFSItem{}, os.ErrNotExist
}

func (v *NetFoxVFS) Join(e ...string) string      { return path.Join(e...) }
func (v *NetFoxVFS) Abs(p string) (string, error) { return p, nil }
func (v *NetFoxVFS) Base(p string) string         { return path.Base(p) }
func (v *NetFoxVFS) Dir(p string) string          { return "net://" }

func (v *NetFoxVFS) MkDir(ctx context.Context, p string) error {
	return fmt.Errorf("folders in NetFox are not yet supported")
}

func (v *NetFoxVFS) Remove(ctx context.Context, p string) error {
	name := v.Base(p)
	if name == "<Add connection>" {
		return fmt.Errorf("cannot remove <Add connection>")
	}
	return v.updateConfigs(func(configs map[string]NetFoxConfig) error {
		delete(configs, name)
		return nil
	})
}

func (v *NetFoxVFS) Rename(ctx context.Context, old, new string) error {
	oldName := v.Base(old)
	newName := v.Base(new)
	return v.updateConfigs(func(configs map[string]NetFoxConfig) error {
		if cfg, ok := configs[oldName]; ok {
			configs[newName] = cfg
			delete(configs, oldName)
		}
		return nil
	})
}

func (v *NetFoxVFS) SetAttributes(ctx context.Context, path string, item vfs.VFSItem) error {
	return os.ErrPermission
}

func (v *NetFoxVFS) GetCapabilities() vfs.VFSCapabilities {
	return vfs.VFSCapabilities{HasRandomAccess: true, HasUnixPermissions: false}
}
func (v *NetFoxVFS) Search(ctx context.Context, p, pat string) (chan int64, error) { return nil, nil }

type bufferReadAtCloser struct{ *bytes.Reader }

func (b *bufferReadAtCloser) Close() error { return nil }
func (b *bufferReadAtCloser) Read(ctx context.Context, p []byte) (int, error) {
	return b.Reader.Read(p)
}
func (b *bufferReadAtCloser) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return b.Reader.ReadAt(p, off)
}
func (b *bufferReadAtCloser) Size() int64 { return int64(b.Len()) }

func (v *NetFoxVFS) Open(ctx context.Context, p string) (vfs.ReadAtCloser, error) {
	name := v.Base(p)
	if name == "<Add connection>" {
		return nil, os.ErrNotExist
	}
	configs := v.getConfigs()
	cfg, ok := configs[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	// #nosec G117 -- this user-opened virtual connection file intentionally exposes the owning user's editable connection fields.
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return &bufferReadAtCloser{Reader: bytes.NewReader(data)}, nil
}

type netfoxWriter struct {
	v    *NetFoxVFS
	name string
	buf  bytes.Buffer
}

func (w *netfoxWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (w *netfoxWriter) Close() error {
	var cfg NetFoxConfig
	if err := json.Unmarshal(w.buf.Bytes(), &cfg); err != nil {
		return fmt.Errorf("netfox: invalid connection JSON: %w", err)
	}
	return w.v.updateConfigs(func(configs map[string]NetFoxConfig) error {
		configs[w.name] = cfg
		return nil
	})
}
func (v *NetFoxVFS) Create(ctx context.Context, p string) (io.WriteCloser, error) {
	return &netfoxWriter{v: v, name: v.Base(p)}, nil
}
func (v *NetFoxVFS) ParentVFS() vfs.VFS { return nil }
func (v *NetFoxVFS) Close() error       { return nil }
func (v *NetFoxVFS) IsReadOnly() bool   { return true }
func (v *NetFoxVFS) Clone() vfs.VFS {
	return NewNetFoxVFS(v.path)
}
