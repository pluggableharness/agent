package xdg

import (
	"path/filepath"
	"testing"
)

// setHome points os.UserHomeDir at dir on every platform CI runs.
// os.UserHomeDir reads $HOME on Unix and %USERPROFILE% on Windows, so a
// test that sets only one of them silently exercises the caller's real
// home directory on the other platform — which is what made these tests
// pass on Linux/macOS and fail on Windows.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

func TestResolveAllXDGVarsSet(t *testing.T) {
	t.Run("all env vars explicitly set", func(t *testing.T) {
		configHome := t.TempDir()
		cacheHome := t.TempDir()
		dataHome := t.TempDir()
		stateHome := t.TempDir()
		projectDir := t.TempDir()

		t.Setenv("XDG_CONFIG_HOME", configHome)
		t.Setenv("XDG_CACHE_HOME", cacheHome)
		t.Setenv("XDG_DATA_HOME", dataHome)
		t.Setenv("XDG_STATE_HOME", stateHome)

		p, err := Resolve(projectDir)
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}

		if p.ProjectConfig != filepath.Join(projectDir, "agent.hcl") {
			t.Errorf("ProjectConfig = %q, want %q", p.ProjectConfig, filepath.Join(projectDir, "agent.hcl"))
		}
		if p.LockFile != filepath.Join(projectDir, ".agent", "agent.lock.hcl") {
			t.Errorf("LockFile = %q, want %q", p.LockFile, filepath.Join(projectDir, ".agent", "agent.lock.hcl"))
		}

		if p.ConfigDir != filepath.Join(configHome, "agent") {
			t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, filepath.Join(configHome, "agent"))
		}
		if p.GlobalConfig != filepath.Join(configHome, "agent", "config.hcl") {
			t.Errorf("GlobalConfig = %q, want %q", p.GlobalConfig, filepath.Join(configHome, "agent", "config.hcl"))
		}

		if p.CacheDir != filepath.Join(cacheHome, "agent") {
			t.Errorf("CacheDir = %q, want %q", p.CacheDir, filepath.Join(cacheHome, "agent"))
		}
		if p.PluginCacheDir != filepath.Join(cacheHome, "agent", "plugins") {
			t.Errorf("PluginCacheDir = %q, want %q", p.PluginCacheDir, filepath.Join(cacheHome, "agent", "plugins"))
		}

		if p.DataDir != filepath.Join(dataHome, "agent") {
			t.Errorf("DataDir = %q, want %q", p.DataDir, filepath.Join(dataHome, "agent"))
		}

		if p.StateDir != filepath.Join(stateHome, "agent") {
			t.Errorf("StateDir = %q, want %q", p.StateDir, filepath.Join(stateHome, "agent"))
		}
		if p.SessionsDir != filepath.Join(stateHome, "agent", "sessions") {
			t.Errorf("SessionsDir = %q, want %q", p.SessionsDir, filepath.Join(stateHome, "agent", "sessions"))
		}
	})
}

func TestResolveXDGVarsUnset(t *testing.T) {
	tests := []struct {
		name           string
		unsetVar       string
		expectedSubdir string
	}{
		{
			name:           "XDG_CONFIG_HOME unset defaults to HOME/.config",
			unsetVar:       "XDG_CONFIG_HOME",
			expectedSubdir: ".config",
		},
		{
			name:           "XDG_CACHE_HOME unset defaults to HOME/.cache",
			unsetVar:       "XDG_CACHE_HOME",
			expectedSubdir: ".cache",
		},
		{
			name:           "XDG_DATA_HOME unset defaults to HOME/.local/share",
			unsetVar:       "XDG_DATA_HOME",
			expectedSubdir: filepath.Join(".local", "share"),
		},
		{
			name:           "XDG_STATE_HOME unset defaults to HOME/.local/state",
			unsetVar:       "XDG_STATE_HOME",
			expectedSubdir: filepath.Join(".local", "state"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempHome := t.TempDir()
			projectDir := t.TempDir()

			setHome(t, tempHome)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_CACHE_HOME", "")
			t.Setenv("XDG_DATA_HOME", "")
			t.Setenv("XDG_STATE_HOME", "")

			// Set the other env vars to avoid defaults
			if tt.unsetVar != "XDG_CONFIG_HOME" {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempHome, ".config"))
			}
			if tt.unsetVar != "XDG_CACHE_HOME" {
				t.Setenv("XDG_CACHE_HOME", filepath.Join(tempHome, ".cache"))
			}
			if tt.unsetVar != "XDG_DATA_HOME" {
				t.Setenv("XDG_DATA_HOME", filepath.Join(tempHome, ".local", "share"))
			}
			if tt.unsetVar != "XDG_STATE_HOME" {
				t.Setenv("XDG_STATE_HOME", filepath.Join(tempHome, ".local", "state"))
			}

			p, err := Resolve(projectDir)
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}

			expectedPath := filepath.Join(tempHome, tt.expectedSubdir, "agent")
			var actualPath string

			switch tt.unsetVar {
			case "XDG_CONFIG_HOME":
				actualPath = p.ConfigDir
			case "XDG_CACHE_HOME":
				actualPath = p.CacheDir
			case "XDG_DATA_HOME":
				actualPath = p.DataDir
			case "XDG_STATE_HOME":
				actualPath = p.StateDir
			}

			if actualPath != expectedPath {
				t.Errorf("got %q, want %q", actualPath, expectedPath)
			}
		})
	}
}

func TestResolveAllXDGVarsUnset(t *testing.T) {
	tempHome := t.TempDir()
	projectDir := t.TempDir()

	setHome(t, tempHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedConfigDir := filepath.Join(tempHome, ".config", "agent")
	expectedCacheDir := filepath.Join(tempHome, ".cache", "agent")
	expectedDataDir := filepath.Join(tempHome, ".local", "share", "agent")
	expectedStateDir := filepath.Join(tempHome, ".local", "state", "agent")

	if p.ConfigDir != expectedConfigDir {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, expectedConfigDir)
	}
	if p.CacheDir != expectedCacheDir {
		t.Errorf("CacheDir = %q, want %q", p.CacheDir, expectedCacheDir)
	}
	if p.DataDir != expectedDataDir {
		t.Errorf("DataDir = %q, want %q", p.DataDir, expectedDataDir)
	}
	if p.StateDir != expectedStateDir {
		t.Errorf("StateDir = %q, want %q", p.StateDir, expectedStateDir)
	}
}

func TestResolveProjectDirAbsolute(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if !filepath.IsAbs(p.ProjectConfig) {
		t.Errorf("ProjectConfig not absolute: %q", p.ProjectConfig)
	}
	if !filepath.IsAbs(p.LockFile) {
		t.Errorf("LockFile not absolute: %q", p.LockFile)
	}
}

func TestResolveProjectDirRelative(t *testing.T) {
	t.Parallel()

	// Use a relative path
	projectDir := "."

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedProjectConfig := filepath.Join(".", "agent.hcl")
	expectedLockFile := filepath.Join(".", ".agent", "agent.lock.hcl")

	if p.ProjectConfig != expectedProjectConfig {
		t.Errorf("ProjectConfig = %q, want %q", p.ProjectConfig, expectedProjectConfig)
	}
	if p.LockFile != expectedLockFile {
		t.Errorf("LockFile = %q, want %q", p.LockFile, expectedLockFile)
	}
}

func TestResolveSuffixPaths(t *testing.T) {
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	dataHome := t.TempDir()
	stateHome := t.TempDir()
	projectDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	// Verify exact suffix paths
	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{
			name:     "ProjectConfig suffix",
			got:      p.ProjectConfig,
			expected: filepath.Join(projectDir, "agent.hcl"),
		},
		{
			name:     "LockFile suffix",
			got:      p.LockFile,
			expected: filepath.Join(projectDir, ".agent", "agent.lock.hcl"),
		},
		{
			name:     "GlobalConfig suffix",
			got:      p.GlobalConfig,
			expected: filepath.Join(configHome, "agent", "config.hcl"),
		},
		{
			name:     "PluginCacheDir suffix",
			got:      p.PluginCacheDir,
			expected: filepath.Join(cacheHome, "agent", "plugins"),
		},
		{
			name:     "SessionsDir suffix",
			got:      p.SessionsDir,
			expected: filepath.Join(stateHome, "agent", "sessions"),
		},
	}

	for _, tt := range tests {
		if tt.got != tt.expected {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.expected)
		}
	}
}

func TestResolveConfigDirSuffix(t *testing.T) {
	configHome := t.TempDir()
	projectDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedConfigDir := filepath.Join(configHome, "agent")
	if p.ConfigDir != expectedConfigDir {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, expectedConfigDir)
	}
}

func TestResolveCacheDirSuffix(t *testing.T) {
	cacheHome := t.TempDir()
	projectDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedCacheDir := filepath.Join(cacheHome, "agent")
	if p.CacheDir != expectedCacheDir {
		t.Errorf("CacheDir = %q, want %q", p.CacheDir, expectedCacheDir)
	}
}

func TestResolveDataDirSuffix(t *testing.T) {
	dataHome := t.TempDir()
	projectDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedDataDir := filepath.Join(dataHome, "agent")
	if p.DataDir != expectedDataDir {
		t.Errorf("DataDir = %q, want %q", p.DataDir, expectedDataDir)
	}
}

func TestResolveStateDirSuffix(t *testing.T) {
	stateHome := t.TempDir()
	projectDir := t.TempDir()

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", stateHome)

	p, err := Resolve(projectDir)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	expectedStateDir := filepath.Join(stateHome, "agent")
	if p.StateDir != expectedStateDir {
		t.Errorf("StateDir = %q, want %q", p.StateDir, expectedStateDir)
	}
}

func TestGetenvOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		envVar     string
		envValue   string
		defaultVal string
		expected   string
	}{
		{
			name:       "env var set returns value",
			envVar:     "TEST_VAR",
			envValue:   "/some/path",
			defaultVal: "/default",
			expected:   "/some/path",
		},
		{
			name:       "env var unset returns default",
			envVar:     "TEST_UNSET_VAR",
			envValue:   "",
			defaultVal: "/default",
			expected:   "/default",
		},
		{
			name:       "empty env var returns default",
			envVar:     "TEST_EMPTY_VAR",
			envValue:   "",
			defaultVal: "/default",
			expected:   "/default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.envVar, tt.envValue)
			} else {
				t.Setenv(tt.envVar, "")
			}

			result := getenvOrDefault(tt.envVar, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("getenvOrDefault(%q, %q) = %q, want %q", tt.envVar, tt.defaultVal, result, tt.expected)
			}
		})
	}
}
