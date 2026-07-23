package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"github.com/pluggableharness/agent/internal/telemetry"
)

// VerifyChecksum computes binaryPath's sha256 digest and compares it
// against locked's recorded checksum for platform (an "<os>_<arch>" key,
// e.g. "linux_amd64"). MUST be called on every install, not just the first
// time a version is resolved (configuration.md §11) — the lock file is the
// source of truth for "what's allowed to run," not merely a cache hint.
//
// Plain equality, not a timing-safe comparison: this verifies a published
// binary's integrity against a known-good hash, not a secret token, so
// there's no timing side-channel to defend against here (unlike
// hmac.Equal's use case for MACs/session tokens).
//
// VerifyChecksum performs file I/O, so per internal/CLAUDE.md it logs entry
// at DEBUG and wraps the operation in a telemetry span via
// prov.StartChecksumVerify, ended with the call's error.
func VerifyChecksum(ctx context.Context, prov *telemetry.Provider, binaryPath, platform string, locked LockedProvider) (err error) {
	ctx, span := prov.StartChecksumVerify(ctx, binaryPath, platform)
	defer func() { telemetry.EndSpan(span, err) }()
	slog.DebugContext(ctx, "registry: verifying checksum", "path", binaryPath, "platform", platform)

	want, ok := locked.Checksums[platform]
	if !ok {
		err = fmt.Errorf("%w: %q", ErrChecksumNotRecorded, platform)
		return err
	}

	data, readErr := os.ReadFile(binaryPath) // #nosec G304 -- verifying a resolved provider binary's checksum inherently requires reading the path the registry resolved it to
	if readErr != nil {
		err = fmt.Errorf("registry: verify checksum: %w", readErr)
		return err
	}

	sum := sha256.Sum256(data)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != want {
		err = fmt.Errorf("%w: platform %q: got %s, want %s", ErrChecksumMismatch, platform, got, want)
		return err
	}
	return nil
}
