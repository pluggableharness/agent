package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHCL(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.hcl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test fixture: %v", err)
	}
	return path
}

func TestLoadGlobalConfig(t *testing.T) {
	t.Run("dev_overrides and registry_mirror", func(t *testing.T) {
		t.Setenv("REGISTRY_TEST_TOKEN", "tok-123")
		path := writeHCL(t, `
dev_overrides {
  anthropic = "/home/steven/code/provider-anthropic/provider-anthropic"
}

registry_mirror {
  default = "https://registry.internal.example.com"

  mirror {
    prefix = "github.com/agentco/"
    url    = "https://registry.internal.example.com/agentco"
    auth   = env("REGISTRY_TEST_TOKEN")
  }
}
`)
		cfg, err := LoadGlobalConfig(context.Background(), testProvider(t), path)
		if err != nil {
			t.Fatalf("LoadGlobalConfig: unexpected error: %v", err)
		}
		if cfg.DevOverrides["anthropic"] != "/home/steven/code/provider-anthropic/provider-anthropic" {
			t.Fatalf("DevOverrides[anthropic] = %q, unexpected", cfg.DevOverrides["anthropic"])
		}
		if cfg.RegistryMirror.Default != "https://registry.internal.example.com" {
			t.Fatalf("RegistryMirror.Default = %q, unexpected", cfg.RegistryMirror.Default)
		}
		if len(cfg.RegistryMirror.Mirrors) != 1 {
			t.Fatalf("len(Mirrors) = %d, want 1", len(cfg.RegistryMirror.Mirrors))
		}
		m := cfg.RegistryMirror.Mirrors[0]
		if m.Prefix != "github.com/agentco/" || m.URL != "https://registry.internal.example.com/agentco" {
			t.Fatalf("mirror = %+v, unexpected", m)
		}
		if m.Auth != "tok-123" {
			t.Fatalf("mirror.Auth = %q, want tok-123", m.Auth)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := writeHCL(t, "")
		cfg, err := LoadGlobalConfig(context.Background(), testProvider(t), path)
		if err != nil {
			t.Fatalf("LoadGlobalConfig: unexpected error: %v", err)
		}
		if len(cfg.DevOverrides) != 0 {
			t.Fatalf("DevOverrides = %v, want empty", cfg.DevOverrides)
		}
	})

	t.Run("literal auth is rejected", func(t *testing.T) {
		path := writeHCL(t, `
registry_mirror {
  default = "https://example.com"
  mirror {
    prefix = "github.com/x/"
    url    = "https://example.com/x"
    auth   = "literal-token"
  }
}
`)
		if _, err := LoadGlobalConfig(context.Background(), testProvider(t), path); err == nil {
			t.Fatal("LoadGlobalConfig: want error for literal auth, got nil")
		}
	})

	t.Run("unknown top-level block", func(t *testing.T) {
		path := writeHCL(t, `
provider "anthropic" {
  api_key = "should not be allowed here"
}
`)
		if _, err := LoadGlobalConfig(context.Background(), testProvider(t), path); err == nil {
			t.Fatal("LoadGlobalConfig: want error for a project-specific provider block, got nil")
		}
	})
}

// TestLoadGlobalConfig_instrumentation asserts LoadGlobalConfig's
// internal/CLAUDE.md-mandated instrumentation: exactly one span recorded on
// a successful call, and an entry-level DEBUG log carrying the path.
func TestLoadGlobalConfig_instrumentation(t *testing.T) {
	path := writeHCL(t, "")
	logs := captureLogs(t)
	prov, backend := testProviderWithBackend(t)

	if _, err := LoadGlobalConfig(context.Background(), prov, path); err != nil {
		t.Fatalf("LoadGlobalConfig: unexpected error: %v", err)
	}

	spans := flushedSpans(t, prov, backend)
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "registry.global_config.load" {
		t.Errorf("span name = %q, want registry.global_config.load", got)
	}

	if got := logs.String(); !strings.Contains(got, "registry: loading global config") || !strings.Contains(got, path) {
		t.Errorf("debug log = %q, want it to contain entry message and path %q", got, path)
	}
}
