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
// where sanitized-source replaces "/" with "_" (a git-forge address contains
// slashes, which cannot appear in a single path segment) and binary-name is
// the last path segment of source (e.g. "provider-anthropic").
//
// Sanitization: forward slashes in the source are deterministically replaced
// with underscores to form a single path-safe directory segment. Two different
// sources will produce different sanitized forms (collision-resistant for
// realistic git-forge addresses).
func BinaryPath(cacheDir, source, version, platform string) string {
	// Extract the binary name — the last path segment of the source.
	// For "github.com/agentco/provider-anthropic", this is "provider-anthropic".
	binaryName := filepath.Base(source)

	// Sanitize the source by replacing "/" with "_" to form a single path-safe
	// segment. E.g. "github.com/agentco/provider-anthropic" → "github.com_agentco_provider-anthropic".
	sanitized := strings.ReplaceAll(source, "/", "_")

	return filepath.Join(cacheDir, sanitized, version, platform, binaryName)
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
