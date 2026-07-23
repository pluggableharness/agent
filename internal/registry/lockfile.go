package registry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// SupportedLockFileVersion is the highest lock_file_version this package
// understands. configuration.md §11: the kernel MUST check this before
// reading anything else in the file, refusing to proceed if it's newer
// than understood — mirrors state-backend.md §9.1's schema migration
// posture.
const SupportedLockFileVersion = 1

var lockFileVersionSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "lock_file_version", Required: true}},
}

var lockFileSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{{Name: "lock_file_version", Required: true}},
	Blocks:     []hcl.BlockHeaderSchema{{Type: "provider", LabelNames: []string{"name"}}},
}

var lockedProviderSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "source", Required: true},
		{Name: "version", Required: true},
		{Name: "resolved_at", Required: true},
		{Name: "checksums", Required: true},
	},
}

// LockFile is the kernel-written lock file at .agent/agent.lock.hcl,
// mirroring .terraform.lock.hcl. configuration.md §11. Not
// operator-authored — this package only reads and verifies it.
type LockFile struct {
	// The lock file FORMAT's own version, independent of any individual
	// provider's version.
	Version int

	// Keyed by required_providers local name.
	Providers map[string]LockedProvider
}

// LockedProvider is one provider's resolved-and-locked state.
type LockedProvider struct {
	// The resolved git-forge source address.
	Source string

	// The exact resolved version (not a constraint — a concrete version).
	Version string

	// When this entry was last resolved, for audit/drift detection.
	ResolvedAt time.Time

	// Per-platform checksums, keyed "<os>_<arch>" (e.g. "linux_amd64"),
	// value "sha256:<hex>". MUST include an entry for every platform the
	// kernel actually installs a binary for, not just the invoking
	// machine's — the lock file is committed/shared across mixed-platform
	// teams.
	Checksums map[string]string
}

// LoadLockFile parses path as a lock file. lock_file_version is checked
// before anything else is decoded, per configuration.md §11. It performs
// file I/O, so per internal/CLAUDE.md it logs entry at DEBUG and wraps the
// operation in a telemetry span via prov.StartLockFileLoad, ended with the
// call's error.
func LoadLockFile(ctx context.Context, prov *telemetry.Provider, path string) (_ *LockFile, err error) {
	ctx, span := prov.StartLockFileLoad(ctx, path)
	defer func() { telemetry.EndSpan(span, err) }()
	slog.DebugContext(ctx, "registry: loading lock file", "path", path)

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		err = fmt.Errorf("registry: load lock file: %w", diags)
		return nil, err
	}

	version, verErr := lockFileVersion(file.Body)
	if verErr != nil {
		err = verErr
		return nil, err
	}
	if version != SupportedLockFileVersion {
		err = fmt.Errorf("registry: %w: got %d, support %d", ErrUnsupportedLockFileVersion, version, SupportedLockFileVersion)
		return nil, err
	}

	content, diags := file.Body.Content(lockFileSchema)
	if diags.HasErrors() {
		err = fmt.Errorf("registry: load lock file: %w", diags)
		return nil, err
	}

	lf := &LockFile{Version: version, Providers: map[string]LockedProvider{}}
	for _, block := range content.Blocks {
		name := block.Labels[0]
		provider, decodeErr := decodeLockedProvider(block.Body)
		if decodeErr != nil {
			err = fmt.Errorf("registry: load lock file: provider %q: %w", name, decodeErr)
			return nil, err
		}
		lf.Providers[name] = provider
	}
	return lf, nil
}

// lockFileVersion extracts just lock_file_version, via PartialContent, so
// it can be checked before the rest of the file is decoded at all.
func lockFileVersion(body hcl.Body) (int, error) {
	content, _, diags := body.PartialContent(lockFileVersionSchema)
	if diags.HasErrors() {
		return 0, fmt.Errorf("registry: load lock file: %w", diags)
	}
	val, diags := content.Attributes["lock_file_version"].Expr.Value(nil)
	if diags.HasErrors() {
		return 0, fmt.Errorf("registry: load lock file: lock_file_version: %w", diags)
	}
	bf := val.AsBigFloat()
	version, _ := bf.Int64()
	return int(version), nil
}

func decodeLockedProvider(body hcl.Body) (LockedProvider, error) {
	content, diags := body.Content(lockedProviderSchema)
	if diags.HasErrors() {
		return LockedProvider{}, diags
	}

	source, err := attrString(content.Attributes["source"])
	if err != nil {
		return LockedProvider{}, fmt.Errorf("source: %w", err)
	}
	version, err := attrString(content.Attributes["version"])
	if err != nil {
		return LockedProvider{}, fmt.Errorf("version: %w", err)
	}
	resolvedAtStr, err := attrString(content.Attributes["resolved_at"])
	if err != nil {
		return LockedProvider{}, fmt.Errorf("resolved_at: %w", err)
	}
	resolvedAt, err := time.Parse(time.RFC3339, resolvedAtStr)
	if err != nil {
		return LockedProvider{}, fmt.Errorf("resolved_at: %w", err)
	}

	checksumsVal, diags := content.Attributes["checksums"].Expr.Value(nil)
	if diags.HasErrors() {
		return LockedProvider{}, fmt.Errorf("checksums: %w", diags)
	}
	checksums := make(map[string]string)
	for platform, v := range checksumsVal.AsValueMap() {
		checksums[platform] = v.AsString()
	}

	return LockedProvider{
		Source:     source,
		Version:    version,
		ResolvedAt: resolvedAt,
		Checksums:  checksums,
	}, nil
}
