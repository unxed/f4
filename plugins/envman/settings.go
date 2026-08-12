package envman

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	settingsDirectory = "plugins"
	settingsFileName  = "envman.json"
	maxSettingsBytes  = 8 << 20
)

// Store owns the versioned on-disk configuration and a concurrency-safe
// in-memory snapshot. Save replaces the settings file through a temporary file
// in the same directory and only publishes its snapshot after the replacement
// succeeds.
type Store struct {
	mu      sync.RWMutex
	ioMu    sync.Mutex
	path    string
	opts    EngineOptions
	current Config
}

// NewStore opens <configDir>/plugins/envman.json using runtime platform
// semantics. A missing file is an empty current-version configuration.
func NewStore(configDir string) (*Store, error) {
	return NewStoreWithOptions(configDir, runtimeOptions())
}

// NewStoreWithOptions is NewStore with injectable platform semantics for
// validation and tests.
func NewStoreWithOptions(configDir string, opts EngineOptions) (*Store, error) {
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("Environment Manager config directory is empty")
	}
	absolute, err := filepath.Abs(configDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Environment Manager config directory: %w", err)
	}
	store := &Store{
		path:    filepath.Join(absolute, settingsDirectory, settingsFileName),
		opts:    cloneOptions(opts),
		current: DefaultConfig(),
	}
	if err := store.Reload(); err != nil {
		// The initialized default snapshot is intentionally usable even when an
		// existing file is corrupt or belongs to a newer schema. Callers can log
		// the error and continue without losing access to the settings path.
		return store, err
	}
	return store, nil
}

// Path returns the absolute settings path.
func (store *Store) Path() string {
	if store == nil {
		return ""
	}
	return store.path
}

// Snapshot returns an independent copy of the last successfully loaded or
// saved configuration.
func (store *Store) Snapshot() Config {
	if store == nil {
		return DefaultConfig()
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneConfig(store.current)
}

// Reload replaces the in-memory snapshot after a complete, valid read. It
// leaves the previous snapshot intact on any error.
func (store *Store) Reload() error {
	if store == nil {
		return errors.New("Environment Manager settings store is unavailable")
	}
	store.ioMu.Lock()
	defer store.ioMu.Unlock()

	config, err := loadConfigFile(store.path, store.opts)
	if errors.Is(err, os.ErrNotExist) {
		config, err = DefaultConfig(), nil
	}
	if err != nil {
		return fmt.Errorf("load Environment Manager settings: %w", err)
	}
	store.mu.Lock()
	store.current = cloneConfig(config)
	store.mu.Unlock()
	return nil
}

// Save validates and atomically replaces the settings file, then updates the
// in-memory snapshot. Invalid configurations and failed writes are not
// published.
func (store *Store) Save(config Config) error {
	if store == nil {
		return errors.New("Environment Manager settings store is unavailable")
	}
	if err := config.Validate(store.opts); err != nil {
		return fmt.Errorf("validate Environment Manager settings: %w", err)
	}
	config = cloneConfig(config)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Environment Manager settings: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxSettingsBytes {
		return fmt.Errorf("Environment Manager settings exceed %d bytes", maxSettingsBytes)
	}

	store.ioMu.Lock()
	defer store.ioMu.Unlock()
	if err := replaceFileAtomically(store.path, data); err != nil {
		return fmt.Errorf("save Environment Manager settings: %w", err)
	}
	store.mu.Lock()
	store.current = config
	store.mu.Unlock()
	return nil
}

func loadConfigFile(path string, opts EngineOptions) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxSettingsBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > maxSettingsBytes {
		return Config{}, fmt.Errorf("settings file exceeds %d bytes", maxSettingsBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode JSON: multiple top-level values")
		}
		return Config{}, fmt.Errorf("decode trailing JSON: %w", err)
	}
	if err := config.Validate(opts); err != nil {
		return Config{}, err
	}
	return cloneConfig(config), nil
}

func replaceFileAtomically(path string, data []byte) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".envman-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if temporary != nil {
			if closeErr := temporary.Close(); returnErr == nil {
				returnErr = closeErr
			}
		}
		if temporaryPath != "" {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	for offset := 0; offset < len(data); {
		written, writeErr := temporary.Write(data[offset:])
		if writeErr != nil {
			return writeErr
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		offset += written
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		temporary = nil
		return err
	}
	temporary = nil
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	temporaryPath = ""

	// Syncing a directory is unsupported on some platforms. The same-directory
	// rename remains the atomic publication point; sync is a best-effort
	// durability improvement where the operating system permits it.
	if directoryHandle, openErr := os.Open(directory); openErr == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return nil
}
