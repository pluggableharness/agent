package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "provider-anthropic")
	data := []byte("pretend binary contents")
	if err := os.WriteFile(binPath, data, 0o700); err != nil {
		t.Fatalf("write test binary: %v", err)
	}
	sum := sha256.Sum256(data)
	correct := "sha256:" + hex.EncodeToString(sum[:])

	t.Run("match", func(t *testing.T) {
		t.Parallel()
		locked := LockedProvider{Checksums: map[string]string{"linux_amd64": correct}}
		if err := VerifyChecksum(context.Background(), testProvider(t), binPath, "linux_amd64", locked); err != nil {
			t.Fatalf("VerifyChecksum: unexpected error: %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		t.Parallel()
		locked := LockedProvider{Checksums: map[string]string{"linux_amd64": "sha256:deadbeef"}}
		err := VerifyChecksum(context.Background(), testProvider(t), binPath, "linux_amd64", locked)
		if err == nil {
			t.Fatal("VerifyChecksum: want error for mismatched checksum, got nil")
		}
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("VerifyChecksum error = %v, want wrapping ErrChecksumMismatch", err)
		}
	})

	t.Run("no checksum recorded for platform", func(t *testing.T) {
		t.Parallel()
		locked := LockedProvider{Checksums: map[string]string{"darwin_arm64": correct}}
		err := VerifyChecksum(context.Background(), testProvider(t), binPath, "linux_amd64", locked)
		if err == nil {
			t.Fatal("VerifyChecksum: want error for unrecorded platform, got nil")
		}
		if !errors.Is(err, ErrChecksumNotRecorded) {
			t.Fatalf("VerifyChecksum error = %v, want wrapping ErrChecksumNotRecorded", err)
		}
	})

	t.Run("binary not found", func(t *testing.T) {
		t.Parallel()
		locked := LockedProvider{Checksums: map[string]string{"linux_amd64": correct}}
		if err := VerifyChecksum(context.Background(), testProvider(t), filepath.Join(dir, "does-not-exist"), "linux_amd64", locked); err == nil {
			t.Fatal("VerifyChecksum: want error for missing binary, got nil")
		}
	})
}

// TestVerifyChecksum_instrumentation asserts VerifyChecksum's
// internal/CLAUDE.md-mandated instrumentation: exactly one span recorded on
// a successful call, and an entry-level DEBUG log carrying the path and
// platform.
func TestVerifyChecksum_instrumentation(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "provider-anthropic")
	data := []byte("pretend binary contents")
	if err := os.WriteFile(binPath, data, 0o700); err != nil {
		t.Fatalf("write test binary: %v", err)
	}
	sum := sha256.Sum256(data)
	correct := "sha256:" + hex.EncodeToString(sum[:])
	locked := LockedProvider{Checksums: map[string]string{"linux_amd64": correct}}

	logs := captureLogs(t)
	prov, backend := testProviderWithBackend(t)

	if err := VerifyChecksum(context.Background(), prov, binPath, "linux_amd64", locked); err != nil {
		t.Fatalf("VerifyChecksum: unexpected error: %v", err)
	}

	spans := flushedSpans(t, prov, backend)
	if len(spans) != 1 {
		t.Fatalf("len(spans) = %d, want 1", len(spans))
	}
	if got := spans[0].Name; got != "registry.checksum.verify" {
		t.Errorf("span name = %q, want registry.checksum.verify", got)
	}

	got := logs.String()
	if !strings.Contains(got, "registry: verifying checksum") || !strings.Contains(got, binPath) || !strings.Contains(got, "linux_amd64") {
		t.Errorf("debug log = %q, want it to contain entry message, path %q, and platform", got, binPath)
	}
}
