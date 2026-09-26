package plughost

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/unxed/f4/internal/config"
	"github.com/unxed/f4/internal/i18n"
	"github.com/unxed/f4/vfs"
)

type UnregisterFunc struct {
	once sync.Once
	fn   func()
}

// NewUnregisterFunc wraps a teardown closure as a vfs.Registration that runs
// at most once, however many times a plugin hands it back.
func NewUnregisterFunc(fn func()) *UnregisterFunc { return &UnregisterFunc{fn: fn} }

func (r *UnregisterFunc) Unregister() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.fn != nil {
			r.fn()
		}
	})
}

type registeredPluginCommand struct {
	command vfs.PluginCommand
	token   *struct{}
}

var pluginCommandRegistry = struct {
	sync.RWMutex
	byID  map[string]registeredPluginCommand
	order []string
	// generation counts registrations and unregistrations, so a caller that
	// caches something built from PluginCommandsSnapshot (a generated menu,
	// say) can notice a plugin command coming or going without re-snapshotting
	// the registry on every call just to compare it against what it had. It
	// does not change when a command's own Visible(app) predicate would answer
	// differently on the same registered set — that is beyond what a counter
	// over the registry can see.
	generation uint64
}{byID: make(map[string]registeredPluginCommand)}

// PluginCommandRegistryGeneration reports pluginCommandRegistry's generation.
func PluginCommandRegistryGeneration() uint64 {
	pluginCommandRegistry.RLock()
	defer pluginCommandRegistry.RUnlock()
	return pluginCommandRegistry.generation
}

func validatePluginCommand(command vfs.PluginCommand) error {
	command.ID = strings.TrimSpace(command.ID)
	if command.ID == "" {
		return errors.New("plugin command ID is empty")
	}
	if strings.TrimSpace(command.Label) == "" {
		return fmt.Errorf("plugin command %q has an empty label", command.ID)
	}
	if command.Run == nil {
		return fmt.Errorf("plugin command %q has no handler", command.ID)
	}
	if command.Location != vfs.PluginCommandPanel && command.Location != vfs.PluginCommandConfig {
		return fmt.Errorf("plugin command %q has an invalid location", command.ID)
	}
	return nil
}

func clonePluginCommand(command vfs.PluginCommand) vfs.PluginCommand {
	command.SearchKeys = append([]string(nil), command.SearchKeys...)
	command.SearchTerms = append([]string(nil), command.SearchTerms...)
	command.LocalizedLabels = clonePluginCommandLocalizedText(command.LocalizedLabels)
	command.LocalizedDescriptions = clonePluginCommandLocalizedText(command.LocalizedDescriptions)
	return command
}

func clonePluginCommandLocalizedText(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for language, value := range values {
		cloned[language] = value
	}
	return cloned
}

func normalizePluginCommandLanguage(language string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
}

func pluginCommandLanguageCandidates() []string {
	seen := make(map[string]bool)
	var candidates []string
	appendLanguage := func(language string) {
		language = normalizePluginCommandLanguage(language)
		if language == "" {
			return
		}
		if !seen[language] {
			seen[language] = true
			candidates = append(candidates, language)
		}
		if separator := strings.IndexByte(language, '-'); separator > 0 {
			base := language[:separator]
			if !seen[base] {
				seen[base] = true
				candidates = append(candidates, base)
			}
		}
	}
	appendLanguage(config.App.Language)
	appendLanguage(config.App.FallbackLanguage)
	appendLanguage("en")
	return candidates
}

func pluginCommandLocalizedText(values map[string]string) string {
	for _, candidate := range pluginCommandLanguageCandidates() {
		for language, value := range values {
			if normalizePluginCommandLanguage(language) == candidate && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func pluginCommandDisplayText(key string, localized map[string]string, fallback string) string {
	key = strings.TrimSpace(key)
	if key != "" {
		if value := i18n.Msg(key); !strings.HasPrefix(value, "{") {
			return value
		}
	}
	if value := pluginCommandLocalizedText(localized); value != "" {
		return value
	}
	return fallback
}

func PluginCommandDisplayLabel(command vfs.PluginCommand) string {
	return pluginCommandDisplayText(command.LabelKey, command.LocalizedLabels, command.Label)
}

func PluginCommandDisplayDescription(command vfs.PluginCommand) string {
	return pluginCommandDisplayText(command.DescriptionKey, command.LocalizedDescriptions, command.Description)
}

func PluginCommandSearchTerms(command vfs.PluginCommand) []string {
	terms := append([]string(nil), command.SearchTerms...)
	appendLocalized := func(values map[string]string) {
		languages := make([]string, 0, len(values))
		for language := range values {
			languages = append(languages, language)
		}
		sort.Strings(languages)
		for _, language := range languages {
			terms = append(terms, values[language])
		}
	}
	appendLocalized(command.LocalizedLabels)
	appendLocalized(command.LocalizedDescriptions)
	return terms
}

func PluginCommandTranslationKeys(command vfs.PluginCommand) []string {
	keys := make([]string, 0, 2+len(command.SearchKeys))
	seen := make(map[string]bool, 2+len(command.SearchKeys))
	for _, key := range append([]string{command.LabelKey, command.DescriptionKey}, command.SearchKeys...) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

func RegisterPluginCommand(command vfs.PluginCommand) (vfs.Registration, error) {
	command.ID = strings.TrimSpace(command.ID)
	if err := validatePluginCommand(command); err != nil {
		return nil, err
	}
	command = clonePluginCommand(command)

	registryID := strings.ToLower(command.ID)
	token := &struct{}{}
	pluginCommandRegistry.Lock()
	if _, exists := pluginCommandRegistry.byID[registryID]; exists {
		pluginCommandRegistry.Unlock()
		return nil, fmt.Errorf("plugin command %q is already registered", command.ID)
	}
	pluginCommandRegistry.byID[registryID] = registeredPluginCommand{command: command, token: token}
	pluginCommandRegistry.order = append(pluginCommandRegistry.order, registryID)
	pluginCommandRegistry.generation++
	pluginCommandRegistry.Unlock()

	return &UnregisterFunc{fn: func() {
		pluginCommandRegistry.Lock()
		if current, ok := pluginCommandRegistry.byID[registryID]; ok && current.token == token {
			delete(pluginCommandRegistry.byID, registryID)
			for index, id := range pluginCommandRegistry.order {
				if id == registryID {
					pluginCommandRegistry.order = append(pluginCommandRegistry.order[:index], pluginCommandRegistry.order[index+1:]...)
					break
				}
			}
			pluginCommandRegistry.generation++
		}
		pluginCommandRegistry.Unlock()
	}}, nil
}

func PluginCommandsSnapshot(location vfs.PluginCommandLocation, app vfs.App) []vfs.PluginCommand {
	pluginCommandRegistry.RLock()
	ordered := append([]string(nil), pluginCommandRegistry.order...)
	registered := make(map[string]registeredPluginCommand, len(pluginCommandRegistry.byID))
	for id, command := range pluginCommandRegistry.byID {
		registered[id] = registeredPluginCommand{command: clonePluginCommand(command.command), token: command.token}
	}
	pluginCommandRegistry.RUnlock()

	commands := make([]vfs.PluginCommand, 0, len(registered))
	for _, id := range ordered {
		entry, ok := registered[id]
		if !ok || entry.command.Location != location {
			continue
		}
		if entry.command.Visible != nil && !entry.command.Visible(app) {
			continue
		}
		commands = append(commands, clonePluginCommand(entry.command))
	}
	return commands
}

// ExecutePluginCommand resolves a menu/palette selection against
// the live registry. Menus keep a label snapshot while open, but a plugin can
// disconnect and unregister in the meantime; retaining its old Run closure
// would call unloaded plugin code.
func ExecutePluginCommand(location vfs.PluginCommandLocation, id string, app vfs.App) bool {
	if App != nil && App.IsStale(app) {
		return false
	}
	if location == vfs.PluginCommandConfig && SettingsCommand != nil && SettingsCommand(id) {
		return true
	}

	registryID := strings.ToLower(strings.TrimSpace(id))
	pluginCommandRegistry.RLock()
	registered, ok := pluginCommandRegistry.byID[registryID]
	if ok {
		registered.command = clonePluginCommand(registered.command)
	}
	pluginCommandRegistry.RUnlock()
	if !ok || registered.command.Location != location {
		return false
	}
	command := registered.command
	if command.Visible != nil && !command.Visible(app) {
		return false
	}
	command.Run(app)
	return true
}

// PluginCommandByID returns a copy of one registered command. The copy is what
// makes it safe to read outside the registry lock: a plugin can unregister
// while the caller is still holding the value.
func PluginCommandByID(id string) (vfs.PluginCommand, bool) {
	pluginCommandRegistry.RLock()
	registered, ok := pluginCommandRegistry.byID[strings.ToLower(strings.TrimSpace(id))]
	if ok {
		registered.command = clonePluginCommand(registered.command)
	}
	pluginCommandRegistry.RUnlock()
	return registered.command, ok
}

// PluginCommandIDs lists the registered commands in registration order,
// including the ones currently hidden from the menu.
func PluginCommandIDs() []string {
	pluginCommandRegistry.RLock()
	defer pluginCommandRegistry.RUnlock()
	return append([]string(nil), pluginCommandRegistry.order...)
}

// SettingsCommand redirects bundled configuration commands through the settings host.
var SettingsCommand func(string) bool
