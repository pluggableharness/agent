package plugincache

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlatform(t *testing.T) {
	t.Parallel()

	p := Platform()

	// Check that it's non-empty.
	if p == "" {
		t.Fatal("Platform() returned empty string")
	}

	// Check that it matches the expected format "<os>_<arch>".
	parts := strings.Split(p, "_")
	if len(parts) != 2 {
		t.Fatalf("Platform() = %q; want format '<os>_<arch>' with exactly one '_'", p)
	}

	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("Platform() = %q; both os and arch must be non-empty", p)
	}
}

func TestBinaryPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		cacheDir       string
		source         string
		version        string
		platform       string
		expectedPath   string
		shouldContain  []string
		shouldNotEqual []string
	}{
		{
			name:     "simple github source",
			cacheDir: "/cache",
			source:   "github.com/agentco/provider-anthropic",
			version:  "1.2.3",
			platform: "linux_amd64",
			expectedPath: filepath.Join(
				"/cache",
				"github.com_agentco_provider-anthropic",
				"1.2.3",
				"linux_amd64",
				"provider-anthropic",
			),
			shouldContain: []string{"github.com_agentco_provider-anthropic", "1.2.3", "linux_amd64", "provider-anthropic"},
		},
		{
			name:     "different source path",
			cacheDir: "/cache",
			source:   "gitlab.com/team/my-plugin",
			version:  "0.1.0",
			platform: "darwin_arm64",
			expectedPath: filepath.Join(
				"/cache",
				"gitlab.com_team_my-plugin",
				"0.1.0",
				"darwin_arm64",
				"my-plugin",
			),
			shouldContain: []string{"gitlab.com_team_my-plugin", "0.1.0", "darwin_arm64", "my-plugin"},
		},
		{
			name:     "sanitization is deterministic",
			cacheDir: "/cache",
			source:   "github.com/agentco/provider-anthropic",
			version:  "1.0.0",
			platform: "linux_amd64",
		},
		{
			name:     "different sources produce different paths",
			cacheDir: "/cache",
			source:   "github.com/other/provider-gpt",
			version:  "1.0.0",
			platform: "linux_amd64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The table writes cacheDir with forward slashes for
			// readability; BinaryPath returns a filepath.Join result, which
			// is backslash-separated on Windows. Normalize the input to the
			// platform's own separator so the prefix assertion below
			// compares like with like rather than passing only on POSIX.
			cacheDir := filepath.FromSlash(tt.cacheDir)

			path := BinaryPath(cacheDir, tt.source, tt.version, tt.platform)

			// Check exact match if expectedPath is set.
			if tt.expectedPath != "" && path != tt.expectedPath {
				t.Errorf("BinaryPath() = %q; want %q", path, tt.expectedPath)
			}

			// Check that expected parts are present.
			for _, part := range tt.shouldContain {
				if !strings.Contains(path, part) {
					t.Errorf("BinaryPath() = %q; should contain %q", path, part)
				}
			}

			// Check that the path starts with cacheDir.
			if !strings.HasPrefix(path, cacheDir) {
				t.Errorf("BinaryPath() = %q; should start with cacheDir %q", path, cacheDir)
			}
		})
	}

	// Test collision resistance: two different sources should not produce the same path.
	t.Run("collision-resistant", func(t *testing.T) {
		t.Parallel()

		path1 := BinaryPath("/cache", "github.com/agentco/provider-anthropic", "1.0.0", "linux_amd64")
		path2 := BinaryPath("/cache", "github.com/other/provider-gpt", "1.0.0", "linux_amd64")

		if path1 == path2 {
			t.Errorf("Two different sources produced the same path: %q", path1)
		}
	})

	// Test that sanitization replaces slashes.
	t.Run("slashes-replaced", func(t *testing.T) {
		t.Parallel()

		path := BinaryPath("/cache", "github.com/agentco/provider-anthropic", "1.0.0", "linux_amd64")

		// The path should not contain unescaped slashes in the sanitized-source segment
		// (except as path separators). The sanitized source segment is the first
		// component after cacheDir.
		parts := strings.Split(path, string(os.PathSeparator))

		// parts[0] is empty (leading /)
		// parts[1] is "cache"
		// parts[2] is the sanitized-source, which should not contain "/"
		if len(parts) > 2 {
			if strings.Contains(parts[2], "/") {
				t.Errorf("sanitized source contains unescaped slashes: %q", parts[2])
			}
		}
	})
}

func TestExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	t.Run("file exists", func(t *testing.T) {
		t.Parallel()

		// Create a temporary file.
		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test-binary")
		if err := os.WriteFile(tmpFile, []byte("test"), 0o755); err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}

		exists, err := Exists(ctx, logger, tmpFile)
		if err != nil {
			t.Fatalf("Exists() returned error for existing file: %v", err)
		}
		if !exists {
			t.Errorf("Exists() = false; want true for existing file")
		}
	})

	t.Run("file does not exist", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		nonExistentPath := filepath.Join(tmpDir, "does-not-exist")

		exists, err := Exists(ctx, logger, nonExistentPath)
		if err != nil {
			t.Fatalf("Exists() returned error for non-existent file: %v", err)
		}
		if exists {
			t.Errorf("Exists() = true; want false for non-existent file")
		}
	})

	t.Run("parent directory does not exist", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		nonExistentPath := filepath.Join(tmpDir, "no-such-dir", "binary")

		exists, err := Exists(ctx, logger, nonExistentPath)
		if err != nil {
			t.Fatalf("Exists() returned error for path with non-existent parent: %v", err)
		}
		if exists {
			t.Errorf("Exists() = true; want false when parent dir does not exist")
		}
	})

	t.Run("is regular file", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		// Create a regular file.
		regularFile := filepath.Join(tmpDir, "regular")
		if err := os.WriteFile(regularFile, []byte("content"), 0o644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		exists, err := Exists(ctx, logger, regularFile)
		if err != nil {
			t.Fatalf("Exists() returned error for regular file: %v", err)
		}
		if !exists {
			t.Errorf("Exists() = false; want true for regular file")
		}
	})

	t.Run("directory returns false without error", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()

		// Check a directory path. Should return (false, nil) since it's not a regular file.
		exists, err := Exists(ctx, logger, tmpDir)
		if err != nil {
			t.Fatalf("Exists() returned error for directory: %v", err)
		}
		if exists {
			t.Errorf("Exists() = true; want false for directory")
		}
	})

	t.Run("permission denied handled as error", func(t *testing.T) {
		t.Parallel()

		// A 0o000 directory mode is a POSIX permission semantic. Windows
		// derives access from ACLs and ignores the mode bits os.Mkdir
		// carries, so the stat below succeeds there and this case exercises
		// nothing. The behavior under test — Exists distinguishing "can't
		// tell" from "not installed" — is real on every platform; only this
		// way of provoking it is not.
		if runtime.GOOS == "windows" {
			t.Skip("directory mode bits do not deny access on Windows; ACLs govern instead")
		}

		tmpDir := t.TempDir()

		// Create a nested path with a directory that has no read permission.
		restricted := filepath.Join(tmpDir, "restricted")
		if err := os.Mkdir(restricted, 0o000); err != nil {
			t.Fatalf("failed to create restricted dir: %v", err)
		}
		t.Cleanup(func() {
			// Restore permissions for cleanup.
			os.Chmod(restricted, 0o755)
		})

		testPath := filepath.Join(restricted, "binary")

		exists, err := Exists(ctx, logger, testPath)
		// Permission denied should return false and an error.
		if exists {
			t.Errorf("Exists() = true; want false for permission denied")
		}
		if err == nil {
			t.Errorf("Exists() returned nil error for permission denied; want error")
		}
	})
}

// TestBinaryPath_everyComponentStaysInsideCacheDir pins that no
// caller-supplied component can make the resolved path escape cacheDir.
// source was sanitized from the start; version and platform were not, even
// though both are equally caller-supplied (they come from a lock-file row)
// and filepath.Join resolves ".." rather than treating it as a literal
// directory name.
func TestBinaryPath_everyComponentStaysInsideCacheDir(t *testing.T) {
	t.Parallel()

	const cacheDir = "/cache"

	tests := []struct {
		name     string
		source   string
		version  string
		platform string
	}{
		{"traversal in version", "github.com/agentco/p", "../../../tmp/evil", "linux_amd64"},
		{"traversal in platform", "github.com/agentco/p", "1.2.3", "../../.."},
		{"traversal in source", "../../../tmp/evil", "1.2.3", "linux_amd64"},
		{"bare dotdot version", "github.com/agentco/p", "..", "linux_amd64"},
		{"bare dot version", "github.com/agentco/p", ".", "linux_amd64"},
		{"backslash in version", "github.com/agentco/p", `..\..\evil`, "linux_amd64"},
		// A colon cannot escape cacheDir — Windows' filepath.Join appends
		// to its first element and Clean never strips a leading prefix —
		// but it does suppress the separator after the element it ends
		// (Join(`C:`, `f`) == `C:f`), gluing two components into one, and
		// on NTFS it names an Alternate Data Stream rather than a
		// directory entry. Both are covered by neutralizing it.
		{"drive prefix in version", "github.com/agentco/p", "C:", "linux_amd64"},
		{"drive prefix in platform", "github.com/agentco/p", "1.2.3", `C:\windows`},
		{"drive prefix in source", "C:", "1.2.3", "linux_amd64"},
		{"colon inside version", "github.com/agentco/p", "1.0:stream", "linux_amd64"},
		{"dotdot source yields dotdot binary name", "..", "1.2.3", "linux_amd64"},
		{"dot source yields dot binary name", ".", "1.2.3", "linux_amd64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := BinaryPath(cacheDir, tt.source, tt.version, tt.platform)

			// Containment is checked via filepath.Rel, not a string prefix
			// against cacheDir: filepath.Join normalizes separators, so on
			// Windows a "/cache" argument comes back as "\cache\..." and a
			// literal prefix comparison fails on the separator rather than
			// on the property under test. Rel is the separator-agnostic
			// idiom — an escape shows up as a relative path that starts by
			// walking back out.
			rel, err := filepath.Rel(cacheDir, got)
			if err != nil {
				t.Fatalf("filepath.Rel(%q, %q): %v", cacheDir, got, err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("BinaryPath = %q escapes cacheDir %q (relative path %q)", got, cacheDir, rel)
			}
			// A ".." SUBSTRING is harmless — ".._.._tmp" is an ordinary
			// literal directory name. What must not survive is a path
			// COMPONENT that is exactly "." or "..", since those are the
			// only two filepath resolves rather than treats as a name.
			for component := range strings.SplitSeq(got, string(filepath.Separator)) {
				if component == "." || component == ".." {
					t.Errorf("BinaryPath = %q contains an unescaped %q component", got, component)
				}
			}
		})
	}
}

// TestBinaryPath_wellFormedInputIsUnchanged guards the other side: the
// sanitization must be a no-op for every realistic input, so no existing
// cached binary's path moves.
func TestBinaryPath_wellFormedInputIsUnchanged(t *testing.T) {
	t.Parallel()

	got := BinaryPath("/cache", "github.com/agentco/provider-anthropic", "1.2.3", "linux_amd64")
	want := filepath.Join("/cache", "github.com_agentco_provider-anthropic", "1.2.3", "linux_amd64", "provider-anthropic")
	if got != want {
		t.Errorf("BinaryPath = %q, want %q", got, want)
	}
}
