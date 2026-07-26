package plugincache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Platform returns this process's platform key in the "<os>_<arch>" form
// used throughout configuration/lock-file.md's checksums map, e.g.
// "linux_amd64". Built from runtime.GOOS + "_" + runtime.GOARCH.
func Platform() string {
	return runtime.GOOS + "_" + runtime.GOARCH
}

// BinaryPath returns the on-disk path a plugin binary for (source, version,
// platform) would live at within cacheDir (the caller's resolved
// PluginCacheDir, e.g. from internal/xdg.Paths.PluginCacheDir). source is
// the git-forge address (e.g. "github.com/agentco/provider-anthropic"); this
// function only computes the path — it does not check existence.
//
// Layout: cacheDir/<sanitized-source>/<version>/<platform>/<binary-name>
// where each of the three middle components is reduced to a single
// path-safe directory segment and binary-name is the last path segment of
// source (e.g. "provider-anthropic").
//
// Sanitization is applied to EVERY caller-supplied component, not just the
// source. A git-forge address obviously contains slashes, but version and
// platform are equally caller-supplied — they come from a lock-file row —
// and a separator or a ".." in either would let filepath.Join resolve the
// result outside cacheDir entirely. That is not a privilege boundary (the
// lock file is already the source of truth for what is allowed to run, and
// registry.VerifyChecksum checks the binary against that same row), but a
// path escaping the cache directory is never what a caller means, and a
// malformed version silently resolving to a nonsense path outside the
// cache reports "not installed" rather than "your lock file is malformed."
// Every well-formed input — a semver version, a "<os>_<arch>" platform —
// is unaffected, so this changes no real path.
func BinaryPath(cacheDir, source, version, platform string) string {
	// Extract the binary name — the last path segment of the source.
	// For "github.com/agentco/provider-anthropic", this is "provider-anthropic".
	binaryName := filepath.Base(source)

	return filepath.Join(
		cacheDir,
		pathSegment(source),
		pathSegment(version),
		pathSegment(platform),
		binaryName,
	)
}

// pathSegment reduces s to a single path-safe directory segment: both
// separators are replaced with "_" (forward slash on every platform,
// backslash because it separates on Windows too), and a segment that would
// otherwise be "." or ".." — the two names filepath.Join resolves rather
// than treats as a literal directory — is escaped the same way.
//
// The replacement is deterministic and injective enough for realistic
// inputs: two different git-forge addresses, versions, or platforms
// produce two different segments.
func pathSegment(s string) string {
	out := strings.ReplaceAll(s, "/", "_")
	out = strings.ReplaceAll(out, `\`, "_")
	switch out {
	case ".", "..":
		return strings.Repeat("_", len(out))
	default:
		return out
	}
}

// Exists reports whether the binary at path is present and is a regular
// file. Logs the checked path at DEBUG. Returns (false, nil) for
// os.IsNotExist and for paths that exist but are not regular files,
// and (false, err) for any other stat error (permission denied, etc.)
// — the caller must be able to distinguish "not installed" from "can't tell".
func Exists(ctx context.Context, logger *slog.Logger, path string) (bool, error) {
	logger.DebugContext(ctx, "checking plugin binary existence", "path", path)

	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("plugincache: stat: %w", err)
	}

	// Check that it's a regular file, not a directory or other type.
	// If it exists but is not a regular file, return (false, nil) — the
	// binary is not installed (a directory is not the binary we're looking for).
	if !stat.Mode().IsRegular() {
		return false, nil
	}

	return true, nil
}
