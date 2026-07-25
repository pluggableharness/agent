package providerresolve_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/plugincache"
	"github.com/pluggableharness/agent/internal/providerresolve"
	"github.com/pluggableharness/agent/internal/registry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

const testPlatform = "linux_amd64"

// discardLogger returns a logger writing nowhere, so a test's output isn't
// buried under this package's DEBUG lines.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// rangeAt builds an hcl.Range at byte offset for the canonical test
// filename — enough for Order, which only reads Filename and Start.Byte.
func rangeAt(offset int) hcl.Range {
	return hcl.Range{
		Filename: "agent.hcl",
		Start:    hcl.Pos{Byte: offset},
		End:      hcl.Pos{Byte: offset + 1},
	}
}

// writeBinary creates an executable placeholder at the plugin-cache path
// for (source, version) under cacheDir for testPlatform, and returns that
// path.
func writeBinary(t *testing.T, cacheDir, source, version string) string {
	t.Helper()

	path := plugincache.BinaryPath(cacheDir, source, version, testPlatform)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// writeNonExecutable creates a present-but-unrunnable file at the
// plugin-cache path for (source, version) under testPlatform.
func writeNonExecutable(t *testing.T, cacheDir, source, version string) {
	t.Helper()

	path := plugincache.BinaryPath(cacheDir, source, version, testPlatform)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("not a binary"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		required map[string]config.RequiredProvider
		ranges   map[string]hcl.Range
		want     []string
	}{
		{
			name:     "empty",
			required: map[string]config.RequiredProvider{},
			ranges:   map[string]hcl.Range{},
			want:     []string{},
		},
		{
			name: "textual position wins over alphabetical",
			required: map[string]config.RequiredProvider{
				"zulu":  {Source: "github.com/x/zulu"},
				"alpha": {Source: "github.com/x/alpha"},
			},
			ranges: map[string]hcl.Range{
				"zulu":  rangeAt(10),
				"alpha": rangeAt(20),
			},
			want: []string{"zulu", "alpha"},
		},
		{
			name: "unpositioned entries sort last, then by name",
			required: map[string]config.RequiredProvider{
				"positioned": {},
				"beta":       {},
				"alpha":      {},
			},
			ranges: map[string]hcl.Range{
				"positioned": rangeAt(99),
			},
			want: []string{"positioned", "alpha", "beta"},
		},
		{
			name: "all unpositioned falls back to name order",
			required: map[string]config.RequiredProvider{
				"c": {}, "a": {}, "b": {},
			},
			ranges: map[string]hcl.Range{},
			want:   []string{"a", "b", "c"},
		},
		{
			name: "same offset in different files sorts by filename",
			required: map[string]config.RequiredProvider{
				"second": {}, "first": {},
			},
			ranges: map[string]hcl.Range{
				"second": {Filename: "b.hcl", Start: hcl.Pos{Byte: 1}},
				"first":  {Filename: "a.hcl", Start: hcl.Pos{Byte: 1}},
			},
			want: []string{"first", "second"},
		},
		{
			name: "identical positions fall back to name for a total order",
			required: map[string]config.RequiredProvider{
				"b": {}, "a": {},
			},
			ranges: map[string]hcl.Range{
				"b": rangeAt(5),
				"a": rangeAt(5),
			},
			want: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := providerresolve.Order(tt.required, tt.ranges)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Order() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestOrder_deterministicAcrossRuns guards the one property map iteration
// order would silently break: repeating the same call must return the
// identical sequence every time (.claude/rules/determinism.md).
func TestOrder_deterministicAcrossRuns(t *testing.T) {
	t.Parallel()

	required := map[string]config.RequiredProvider{}
	ranges := map[string]hcl.Range{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		required[name] = config.RequiredProvider{}
	}
	// Half positioned in reverse-alphabetical textual order, half not.
	ranges["h"] = rangeAt(1)
	ranges["g"] = rangeAt(2)
	ranges["f"] = rangeAt(3)
	ranges["e"] = rangeAt(4)

	want := []string{"h", "g", "f", "e", "a", "b", "c", "d"}
	for range 50 {
		if got := providerresolve.Order(required, ranges); !reflect.DeepEqual(got, want) {
			t.Fatalf("Order() = %v, want %v", got, want)
		}
	}
}

func TestResolve_devOverrideBypassesLockAndChecksum(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	overridePath := filepath.Join(dir, "provider-anthropic")
	if err := os.WriteFile(overridePath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write override binary: %v", err)
	}

	in := providerresolve.Input{
		Config: &config.Config{
			RequiredProviders: map[string]config.RequiredProvider{
				"anthropic": {Source: "github.com/agentco/provider-anthropic", Constraint: "~> 1.2"},
			},
			ProviderRanges: map[string]hcl.Range{"anthropic": rangeAt(1)},
		},
		// Deliberately no lock row and no checksum: dev_overrides bypasses both.
		Lock:     &registry.LockFile{Version: 1, Providers: map[string]registry.LockedProvider{}},
		Global:   &registry.GlobalConfig{DevOverrides: map[string]string{"anthropic": overridePath}},
		CacheDir: filepath.Join(dir, "cache"),
		Platform: testPlatform,
		Logger:   discardLogger(),
	}

	got, err := providerresolve.Resolve(context.Background(), in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve returned %d entries, want 1", len(got))
	}
	r := got[0]
	if !r.ViaDevOverride {
		t.Error("ViaDevOverride = false, want true")
	}
	if r.Locked != nil {
		t.Errorf("Locked = %+v, want nil for a dev-override provider", r.Locked)
	}
	if r.BinaryPath != overridePath {
		t.Errorf("BinaryPath = %q, want %q", r.BinaryPath, overridePath)
	}
	if r.Category != commonv1.Category_CATEGORY_UNSPECIFIED {
		t.Errorf("Category = %v, want CATEGORY_UNSPECIFIED (only a live Describe knows it)", r.Category)
	}
	if r.Version != "" {
		t.Errorf("Version = %q, want empty — a dev override resolves no version", r.Version)
	}
}

func TestResolve_lockedProvider(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	const source = "github.com/agentco/provider-anthropic"
	path := writeBinary(t, cacheDir, source, "1.2.3")

	in := providerresolve.Input{
		Config: &config.Config{
			RequiredProviders: map[string]config.RequiredProvider{
				"anthropic": {Source: source, Constraint: "~> 1.2"},
			},
			ProviderRanges: map[string]hcl.Range{"anthropic": rangeAt(1)},
		},
		Lock: &registry.LockFile{Version: 1, Providers: map[string]registry.LockedProvider{
			"anthropic": {
				Source:    source,
				Version:   "1.2.3",
				Category:  "model",
				Checksums: map[string]string{testPlatform: "sha256:deadbeef"},
			},
		}},
		CacheDir: cacheDir,
		Platform: testPlatform,
		Logger:   discardLogger(),
	}

	got, err := providerresolve.Resolve(context.Background(), in)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Resolve returned %d entries, want 1", len(got))
	}
	r := got[0]
	if r.LocalName != "anthropic" || r.Source != source || r.Version != "1.2.3" {
		t.Errorf("identity = {%q %q %q}, want {anthropic %q 1.2.3}", r.LocalName, r.Source, r.Version, source)
	}
	if r.Category != commonv1.Category_CATEGORY_MODEL {
		t.Errorf("Category = %v, want CATEGORY_MODEL from the lock file's cached record", r.Category)
	}
	if r.BinaryPath != path {
		t.Errorf("BinaryPath = %q, want %q", r.BinaryPath, path)
	}
	if r.ViaDevOverride {
		t.Error("ViaDevOverride = true, want false")
	}
	if r.Locked == nil || r.Locked.Version != "1.2.3" {
		t.Errorf("Locked = %+v, want the lock row for 1.2.3", r.Locked)
	}
}

// TestResolve_categoryText locks in the lock-file category string ->
// generated enum translation, including the deliberate
// unrecognized-value-is-not-an-error behavior.
func TestResolve_categoryText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want commonv1.Category
	}{
		{"model", commonv1.Category_CATEGORY_MODEL},
		{"tool", commonv1.Category_CATEGORY_TOOL},
		{"context", commonv1.Category_CATEGORY_CONTEXT},
		{"memory", commonv1.Category_CATEGORY_MEMORY},
		{"frontend", commonv1.Category_CATEGORY_FRONTEND},
		{"widget", commonv1.Category_CATEGORY_WIDGET},
		{"slashcommand", commonv1.Category_CATEGORY_SLASHCOMMAND},
		{"", commonv1.Category_CATEGORY_UNSPECIFIED},
		{"nonsense", commonv1.Category_CATEGORY_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run("category_"+tt.text, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			cacheDir := filepath.Join(dir, "cache")
			const source = "github.com/agentco/p"
			writeBinary(t, cacheDir, source, "1.0.0")

			in := providerresolve.Input{
				Config: &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: source}}},
				Lock: &registry.LockFile{Providers: map[string]registry.LockedProvider{
					"p": {Source: source, Version: "1.0.0", Category: tt.text, Checksums: map[string]string{testPlatform: "sha256:x"}},
				}},
				CacheDir: cacheDir,
				Platform: testPlatform,
				Logger:   discardLogger(),
			}
			got, err := providerresolve.Resolve(context.Background(), in)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got[0].Category != tt.want {
				t.Errorf("Category for %q = %v, want %v", tt.text, got[0].Category, tt.want)
			}
		})
	}
}

func TestResolve_missingReasons(t *testing.T) {
	t.Parallel()

	const source = "github.com/agentco/p"

	tests := []struct {
		name string
		// setup populates the cache dir and returns the lock rows to use.
		setup func(t *testing.T, cacheDir string) map[string]registry.LockedProvider
		want  providerresolve.MissingReason
		// wantPath reports whether the Missing entry must name a path.
		wantPath bool
		skipOn   string
	}{
		{
			name: "not locked",
			setup: func(*testing.T, string) map[string]registry.LockedProvider {
				return map[string]registry.LockedProvider{}
			},
			want: providerresolve.MissingNotLocked,
		},
		{
			name: "not cached",
			setup: func(*testing.T, string) map[string]registry.LockedProvider {
				return map[string]registry.LockedProvider{
					"p": {Source: source, Version: "1.0.0", Checksums: map[string]string{testPlatform: "sha256:x"}},
				}
			},
			want:     providerresolve.MissingNotCached,
			wantPath: true,
		},
		{
			name: "no checksum for this platform",
			setup: func(t *testing.T, cacheDir string) map[string]registry.LockedProvider {
				writeBinary(t, cacheDir, source, "1.0.0")
				return map[string]registry.LockedProvider{
					"p": {Source: source, Version: "1.0.0", Checksums: map[string]string{"darwin_arm64": "sha256:x"}},
				}
			},
			want:     providerresolve.MissingNoChecksum,
			wantPath: true,
		},
		{
			name: "not executable",
			setup: func(t *testing.T, cacheDir string) map[string]registry.LockedProvider {
				writeNonExecutable(t, cacheDir, source, "1.0.0")
				return map[string]registry.LockedProvider{
					"p": {Source: source, Version: "1.0.0", Checksums: map[string]string{testPlatform: "sha256:x"}},
				}
			},
			want:     providerresolve.MissingNotExecutable,
			wantPath: true,
			skipOn:   "windows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.skipOn == runtime.GOOS {
				t.Skipf("%s has no POSIX executable bit", runtime.GOOS)
			}

			cacheDir := filepath.Join(t.TempDir(), "cache")
			providers := tt.setup(t, cacheDir)

			in := providerresolve.Input{
				Config:   &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: source, Constraint: "~> 1.0"}}},
				Lock:     &registry.LockFile{Providers: providers},
				CacheDir: cacheDir,
				Platform: testPlatform,
				Logger:   discardLogger(),
			}

			got, err := providerresolve.Resolve(context.Background(), in)
			if got != nil {
				t.Errorf("Resolve returned %v, want nil alongside the error", got)
			}
			var missErr *providerresolve.MissingError
			if !errors.As(err, &missErr) {
				t.Fatalf("Resolve error = %v, want *MissingError", err)
			}
			if len(missErr.Missing) != 1 {
				t.Fatalf("Missing = %+v, want exactly one entry", missErr.Missing)
			}
			m := missErr.Missing[0]
			if m.Reason != tt.want {
				t.Errorf("Reason = %v, want %v", m.Reason, tt.want)
			}
			if m.LocalName != "p" || m.Source != source || m.Constraint != "~> 1.0" {
				t.Errorf("identity = {%q %q %q}, want {p %q ~> 1.0}", m.LocalName, m.Source, m.Constraint, source)
			}
			if gotPath := m.Path != ""; gotPath != tt.wantPath {
				t.Errorf("Path = %q, wantPath = %v", m.Path, tt.wantPath)
			}
		})
	}
}

// TestResolve_devOverrideMissingBinary confirms a dev_overrides entry
// pointing at nothing is reported like any other unresolvable provider,
// rather than handed to a launcher as a path that cannot be exec'd.
func TestResolve_devOverrideMissingBinary(t *testing.T) {
	t.Parallel()

	in := providerresolve.Input{
		Config:   &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: "github.com/agentco/p"}}},
		Global:   &registry.GlobalConfig{DevOverrides: map[string]string{"p": filepath.Join(t.TempDir(), "nope")}},
		CacheDir: t.TempDir(),
		Platform: testPlatform,
		Logger:   discardLogger(),
	}

	_, err := providerresolve.Resolve(context.Background(), in)
	var missErr *providerresolve.MissingError
	if !errors.As(err, &missErr) {
		t.Fatalf("Resolve error = %v, want *MissingError", err)
	}
	if missErr.Missing[0].Reason != providerresolve.MissingNotCached {
		t.Errorf("Reason = %v, want MissingNotCached", missErr.Missing[0].Reason)
	}
}

// TestResolve_emptyDevOverridePathIgnored confirms an override declared
// with an empty path falls through to the ordinary lock-file path rather
// than resolving to "".
func TestResolve_emptyDevOverridePathIgnored(t *testing.T) {
	t.Parallel()

	in := providerresolve.Input{
		Config:   &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: "github.com/agentco/p"}}},
		Global:   &registry.GlobalConfig{DevOverrides: map[string]string{"p": ""}},
		CacheDir: t.TempDir(),
		Platform: testPlatform,
		Logger:   discardLogger(),
	}

	_, err := providerresolve.Resolve(context.Background(), in)
	var missErr *providerresolve.MissingError
	if !errors.As(err, &missErr) {
		t.Fatalf("Resolve error = %v, want *MissingError", err)
	}
	if missErr.Missing[0].Reason != providerresolve.MissingNotLocked {
		t.Errorf("Reason = %v, want MissingNotLocked", missErr.Missing[0].Reason)
	}
}

// TestResolve_accumulatesEveryProblem is the whole point of the
// accumulating design: four providers failing four different ways report
// in one pass, in Order's sequence, not one error per run.
func TestResolve_accumulatesEveryProblem(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows has no POSIX executable bit, so MissingNotExecutable is unreachable there")
	}

	cacheDir := filepath.Join(t.TempDir(), "cache")
	writeBinary(t, cacheDir, "github.com/agentco/nochecksum", "1.0.0")
	writeNonExecutable(t, cacheDir, "github.com/agentco/notexec", "1.0.0")
	writeBinary(t, cacheDir, "github.com/agentco/ok", "1.0.0")

	in := providerresolve.Input{
		Config: &config.Config{
			RequiredProviders: map[string]config.RequiredProvider{
				"notlocked":  {Source: "github.com/agentco/notlocked"},
				"notcached":  {Source: "github.com/agentco/notcached"},
				"nochecksum": {Source: "github.com/agentco/nochecksum"},
				"notexec":    {Source: "github.com/agentco/notexec"},
				"ok":         {Source: "github.com/agentco/ok"},
			},
			// Declared out of alphabetical order so the assertion below
			// proves accumulation follows Order, not map iteration.
			ProviderRanges: map[string]hcl.Range{
				"notexec":    rangeAt(10),
				"nochecksum": rangeAt(20),
				"notcached":  rangeAt(30),
				"notlocked":  rangeAt(40),
			},
		},
		Lock: &registry.LockFile{Providers: map[string]registry.LockedProvider{
			"notcached":  {Source: "github.com/agentco/notcached", Version: "1.0.0", Checksums: map[string]string{testPlatform: "sha256:x"}},
			"nochecksum": {Source: "github.com/agentco/nochecksum", Version: "1.0.0", Checksums: map[string]string{}},
			"notexec":    {Source: "github.com/agentco/notexec", Version: "1.0.0", Checksums: map[string]string{testPlatform: "sha256:x"}},
			"ok":         {Source: "github.com/agentco/ok", Version: "1.0.0", Checksums: map[string]string{testPlatform: "sha256:x"}},
		}},
		CacheDir: cacheDir,
		Platform: testPlatform,
		Logger:   discardLogger(),
	}

	_, err := providerresolve.Resolve(context.Background(), in)
	var missErr *providerresolve.MissingError
	if !errors.As(err, &missErr) {
		t.Fatalf("Resolve error = %v, want *MissingError", err)
	}

	type entry struct {
		name   string
		reason providerresolve.MissingReason
	}
	want := []entry{
		{"notexec", providerresolve.MissingNotExecutable},
		{"nochecksum", providerresolve.MissingNoChecksum},
		{"notcached", providerresolve.MissingNotCached},
		{"notlocked", providerresolve.MissingNotLocked},
		// "ok" has no provider{} block, so it sorts last — and it resolves
		// fine, so it is absent from Missing entirely.
	}
	got := make([]entry, 0, len(missErr.Missing))
	for _, m := range missErr.Missing {
		got = append(got, entry{m.LocalName, m.Reason})
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Missing = %v, want %v (Order's sequence)", got, want)
	}

	// The rendered message sorts by LocalName regardless of the above.
	msg := missErr.Error()
	for _, name := range []string{"notlocked", "notcached", "nochecksum", "notexec"} {
		if !strings.Contains(msg, name) {
			t.Errorf("Error() = %q, missing %q", msg, name)
		}
	}
	if idx := strings.Index(msg, "nochecksum"); idx == -1 || idx > strings.Index(msg, "notcached") {
		t.Errorf("Error() lines are not sorted by LocalName:\n%s", msg)
	}
}

func TestResolve_nilConfigAndNilLock(t *testing.T) {
	t.Parallel()

	got, err := providerresolve.Resolve(context.Background(), providerresolve.Input{Logger: discardLogger()})
	if err != nil {
		t.Fatalf("Resolve with a nil Config: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Resolve with a nil Config returned %v, want no entries", got)
	}

	in := providerresolve.Input{
		Config:   &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: "github.com/agentco/p"}}},
		Platform: testPlatform,
		Logger:   discardLogger(),
	}
	_, err = providerresolve.Resolve(context.Background(), in)
	var missErr *providerresolve.MissingError
	if !errors.As(err, &missErr) {
		t.Fatalf("Resolve with a nil Lock: error = %v, want *MissingError", err)
	}
	if missErr.Missing[0].Reason != providerresolve.MissingNotLocked {
		t.Errorf("Reason = %v, want MissingNotLocked for a nil lock file", missErr.Missing[0].Reason)
	}
}

// TestResolve_nilLoggerDefaults confirms the documented nil-Logger
// fallback works rather than panicking on the first plugincache call.
func TestResolve_nilLoggerDefaults(t *testing.T) {
	// Not parallel: swaps the process-wide slog default.
	prev := slog.Default()
	slog.SetDefault(discardLogger())
	t.Cleanup(func() { slog.SetDefault(prev) })

	in := providerresolve.Input{
		Config:   &config.Config{RequiredProviders: map[string]config.RequiredProvider{"p": {Source: "github.com/agentco/p"}}},
		CacheDir: t.TempDir(),
		Platform: testPlatform,
	}
	if _, err := providerresolve.Resolve(context.Background(), in); err == nil {
		t.Fatal("Resolve = nil error, want *MissingError")
	}
}

func TestMissingReason_String(t *testing.T) {
	t.Parallel()

	tests := map[providerresolve.MissingReason]string{
		providerresolve.MissingNotLocked:     "not locked",
		providerresolve.MissingNotCached:     "not cached",
		providerresolve.MissingNoChecksum:    "no checksum recorded",
		providerresolve.MissingNotExecutable: "not executable",
		providerresolve.MissingReason(99):    "unknown reason 99",
	}
	for reason, want := range tests {
		if got := reason.String(); got != want {
			t.Errorf("MissingReason(%d).String() = %q, want %q", int(reason), got, want)
		}
	}
}

func TestMissingError_Error(t *testing.T) {
	t.Parallel()

	err := &providerresolve.MissingError{Missing: []providerresolve.Missing{
		{LocalName: "zulu", Source: "github.com/x/zulu", Constraint: "~> 1.0", Reason: providerresolve.MissingNotLocked},
		{LocalName: "alpha", Source: "github.com/x/alpha", Version: "2.0.0", Reason: providerresolve.MissingNotCached, Path: "/cache/alpha"},
	}}

	msg := err.Error()
	if !strings.HasPrefix(msg, "providerresolve: unresolved providers:") {
		t.Errorf("Error() = %q, want the package-prefixed header", msg)
	}
	lines := strings.Split(msg, "\n")
	if len(lines) != 3 {
		t.Fatalf("Error() produced %d lines, want 3 (header + one per entry):\n%s", len(lines), msg)
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "2.0.0") || !strings.Contains(lines[1], "/cache/alpha") {
		t.Errorf("first entry line = %q, want alpha@2.0.0 with its path", lines[1])
	}
	if !strings.Contains(lines[2], "zulu") || !strings.Contains(lines[2], "~> 1.0") {
		t.Errorf("second entry line = %q, want zulu with its constraint", lines[2])
	}
}
