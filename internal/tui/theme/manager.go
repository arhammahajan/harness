package theme

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/arhammahajan/harness/internal/config"
	"github.com/arhammahajan/harness/internal/logging"
)

// Manager handles theme registration, selection, and retrieval.
// It maintains a registry of available themes and tracks the currently active theme.
type Manager struct {
	themes      map[string]Theme
	currentName string
	mu          sync.RWMutex
}

// Global instance of the theme manager
var globalManager = &Manager{
	themes:      make(map[string]Theme),
	currentName: "",
}

var (
	listenersMu          sync.RWMutex
	themeChangeListeners []func()
)

// OnThemeChange registers a callback invoked whenever the active theme or
// dark/light background mode changes.
func OnThemeChange(fn func()) {
	if fn == nil {
		return
	}
	listenersMu.Lock()
	themeChangeListeners = append(themeChangeListeners, fn)
	listenersMu.Unlock()
}

func notifyThemeChange() {
	listenersMu.RLock()
	listeners := make([]func(), len(themeChangeListeners))
	copy(listeners, themeChangeListeners)
	listenersMu.RUnlock()

	for _, fn := range listeners {
		fn()
	}
}

// RegisterTheme adds a new theme to the registry.
// If this is the first theme registered, it becomes the default.
func RegisterTheme(name string, theme Theme) {
	globalManager.mu.Lock()
	defer globalManager.mu.Unlock()

	globalManager.themes[name] = theme

	// If this is the first theme, make it the default
	if globalManager.currentName == "" {
		globalManager.currentName = name
	}
}

// SetTheme changes the active theme to the one with the specified name.
// Returns an error if the theme doesn't exist.
func SetTheme(name string) error {
	globalManager.mu.Lock()
	if _, exists := globalManager.themes[name]; !exists {
		globalManager.mu.Unlock()
		return fmt.Errorf("theme '%s' not found", name)
	}

	globalManager.currentName = name

	// Update the config file using viper
	if err := updateConfigTheme(name); err != nil {
		// Log the error but don't fail the theme change
		logging.Warn("Warning: Failed to update config file with new theme", "err", err)
	}
	globalManager.mu.Unlock()

	notifyThemeChange()
	return nil
}

// CurrentTheme returns the currently active theme.
// If no theme is set, it returns nil.
func CurrentTheme() Theme {
	globalManager.mu.RLock()
	defer globalManager.mu.RUnlock()

	if globalManager.currentName == "" {
		return nil
	}

	return globalManager.themes[globalManager.currentName]
}

// CurrentThemeName returns the name of the currently active theme.
func CurrentThemeName() string {
	globalManager.mu.RLock()
	defer globalManager.mu.RUnlock()

	return globalManager.currentName
}

// AvailableThemes returns a list of all registered theme names.
func AvailableThemes() []string {
	globalManager.mu.RLock()
	defer globalManager.mu.RUnlock()

	names := make([]string, 0, len(globalManager.themes))
	for name := range globalManager.themes {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if a == "harness" {
			return -1
		} else if b == "harness" {
			return 1
		}
		return strings.Compare(a, b)
	})
	return names
}

// GetTheme returns a specific theme by name.
// Returns nil if the theme doesn't exist.
func GetTheme(name string) Theme {
	globalManager.mu.RLock()
	defer globalManager.mu.RUnlock()

	return globalManager.themes[name]
}

// updateConfigTheme updates the theme setting in the configuration file
func updateConfigTheme(themeName string) error {
	// Use the config package to update the theme
	return config.UpdateTheme(themeName)
}
