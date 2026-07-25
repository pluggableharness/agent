package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths is every filesystem location the kernel needs, resolved once at
// startup, per architecture.md#xdg-layout.
type Paths struct {
	// ProjectConfig is "./agent.hcl" relative to projectDir.
	ProjectConfig string
	// LockFile is "./.agent/agent.lock.hcl" relative to projectDir.
	LockFile string

	// ConfigDir is "$XDG_CONFIG_HOME/agent".
	ConfigDir string
	// GlobalConfig is ConfigDir + "/config.hcl".
	GlobalConfig string

	// CacheDir is "$XDG_CACHE_HOME/agent".
	CacheDir string
	// PluginCacheDir is CacheDir + "/plugins" — downloaded plugin
	// binaries, keyed by name/version/platform/checksum (a subdirectory
	// layout, not this package's concern — internal/plugincache owns
	// the layout within this directory).
	PluginCacheDir string

	// DataDir is "$XDG_DATA_HOME/agent" — persistent plugin data.
	DataDir string

	// StateDir is "$XDG_STATE_HOME/agent".
	StateDir string
	// SessionsDir is StateDir + "/sessions" — one sqlite file per
	// session lives here (state-backend.md#file-layout).
	SessionsDir string
}

// Resolve computes Paths for a kernel running with projectDir as its
// current project directory (typically the caller's working directory;
// pass an absolute path). It resolves the four XDG env vars with the
// standard XDG Base Directory fallback defaults when unset:
// XDG_CONFIG_HOME -> $HOME/.config, XDG_CACHE_HOME -> $HOME/.cache,
// XDG_DATA_HOME -> $HOME/.local/share, XDG_STATE_HOME -> $HOME/.local/state.
// It does not create any directory or touch the filesystem beyond
// resolving $HOME via os.UserHomeDir() when needed — directory creation
// is each consumer's own responsibility (e.g. internal/statebackend
// already creates its own sessions directory with 0700).
func Resolve(projectDir string) (Paths, error) {
	p := Paths{}

	// Resolve project-local paths
	p.ProjectConfig = filepath.Join(projectDir, "agent.hcl")
	p.LockFile = filepath.Join(projectDir, ".agent", "agent.lock.hcl")

	// Resolve home directory for XDG defaults
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home directory: %w", err)
	}

	// Resolve XDG env vars with standard defaults
	configHome := getenvOrDefault("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cacheHome := getenvOrDefault("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	dataHome := getenvOrDefault("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	stateHome := getenvOrDefault("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))

	// Resolve config directory
	p.ConfigDir = filepath.Join(configHome, "agent")
	p.GlobalConfig = filepath.Join(p.ConfigDir, "config.hcl")

	// Resolve cache directory
	p.CacheDir = filepath.Join(cacheHome, "agent")
	p.PluginCacheDir = filepath.Join(p.CacheDir, "plugins")

	// Resolve data directory
	p.DataDir = filepath.Join(dataHome, "agent")

	// Resolve state directory
	p.StateDir = filepath.Join(stateHome, "agent")
	p.SessionsDir = filepath.Join(p.StateDir, "sessions")

	return p, nil
}

// getenvOrDefault returns the value of an environment variable or a default
// if it's unset or empty.
func getenvOrDefault(key, defaultVal string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return defaultVal
}
