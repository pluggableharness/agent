package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoadLockFile(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		path := writeHCL(t, `
lock_file_version = 1

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums = {
    "linux_amd64"  = "sha256:1a2b3c"
    "darwin_arm64" = "sha256:0d1e2f"
  }
}
`)
		lf, err := LoadLockFile(context.Background(), testProvider(t), path)
		if err != nil {
			t.Fatalf("LoadLockFile: unexpected error: %v", err)
		}
		if lf.Version != 1 {
			t.Fatalf("Version = %d, want 1", lf.Version)
		}
		p, ok := lf.Providers["anthropic"]
		if !ok {
			t.Fatal("Providers[anthropic] missing")
		}
		if p.Source != "github.com/agentco/provider-anthropic" || p.Version != "1.2.4" {
			t.Fatalf("provider = %+v, unexpected", p)
		}
		wantTime := time.Date(2026, 7, 22, 18, 4, 0, 0, time.UTC)
		if !p.ResolvedAt.Equal(wantTime) {
			t.Fatalf("ResolvedAt = %v, want %v", p.ResolvedAt, wantTime)
		}
		if p.Checksums["linux_amd64"] != "sha256:1a2b3c" {
			t.Fatalf("Checksums[linux_amd64] = %q, unexpected", p.Checksums["linux_amd64"])
		}
		if p.Checksums["darwin_arm64"] != "sha256:0d1e2f" {
			t.Fatalf("Checksums[darwin_arm64] = %q, unexpected", p.Checksums["darwin_arm64"])
		}
	})

	t.Run("unsupported version rejected before decoding the rest", func(t *testing.T) {
		path := writeHCL(t, `
lock_file_version = 99

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums   = { "linux_amd64" = "sha256:x" }
}
`)
		_, err := LoadLockFile(context.Background(), testProvider(t), path)
		if err == nil {
			t.Fatal("LoadLockFile: want error for unsupported version, got nil")
		}
		if !errors.Is(err, ErrUnsupportedLockFileVersion) {
			t.Fatalf("LoadLockFile error = %v, want wrapping ErrUnsupportedLockFileVersion", err)
		}
	})

	t.Run("missing lock_file_version", func(t *testing.T) {
		path := writeHCL(t, `
provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums   = { "linux_amd64" = "sha256:x" }
}
`)
		if _, err := LoadLockFile(context.Background(), testProvider(t), path); err == nil {
			t.Fatal("LoadLockFile: want error for missing lock_file_version, got nil")
		}
	})

	t.Run("malformed resolved_at", func(t *testing.T) {
		path := writeHCL(t, `
lock_file_version = 1

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "not-a-timestamp"
  checksums   = { "linux_amd64" = "sha256:x" }
}
`)
		if _, err := LoadLockFile(context.Background(), testProvider(t), path); err == nil {
			t.Fatal("LoadLockFile: want error for malformed resolved_at, got nil")
		}
	})
}

// TestLoadLockFile_instrumentation asserts LoadLockFile's
// internal/CLAUDE.md-mandated instrumentation: exactly one span recorded on
// a successful call, and an entry-level DEBUG log carrying the path.
func TestLoadLockFile_instrumentation(t *testing.T) {
	path := writeHCL(t, `
lock_file_version = 1

provider "anthropic" {
  source      = "github.com/agentco/provider-anthropic"
  version     = "1.2.4"
  resolved_at = "2026-07-22T18:04:00Z"
  checksums   = { "linux_amd64" = "sha256:1a2b3c" }
}
`)
	logs := captureLogs(t)
	prov, backend := testProviderWithBackend(t)

	if _, err := LoadLockFile(context.Background(), prov, path); err != nil {
		t.Fatalf("LoadLockFile: unexpected error: %v", err)
	}

	spans := flushedSpans(t, prov, backend)
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "registry.lockfile.load" {
		t.Errorf("span name = %q, want registry.lockfile.load", got)
	}

	if got := logs.String(); !strings.Contains(got, "registry: loading lock file") || !strings.Contains(got, path) {
		t.Errorf("debug log = %q, want it to contain entry message and path %q", got, path)
	}
}
