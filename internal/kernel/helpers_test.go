package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalConfig is the smallest agent.hcl this kernel accepts: a settings
// block with the three attributes internal/config marks required, telemetry
// off, and no providers at all. Every bring-up test starts from it.
const minimalConfig = `
settings {
  default_frontend = "none"
  log_level        = "error"
  telemetry        = false
}
`

// newProject writes body as agent.hcl in a fresh temp directory, points
// every XDG variable at sibling temp directories so no test touches the
// operator's real home, and returns the project directory.
//
// It uses t.Setenv, so a test calling it MUST NOT call t.Parallel.
func newProject(t *testing.T, body string) string {
	t.Helper()

	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(project, DefaultConfigFile), []byte(body), 0o600); err != nil {
			t.Fatalf("write agent.hcl: %v", err)
		}
	}

	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, filepath.Join(root, v))
	}
	return project
}

// testOptions returns Options rooted at project, with both writers
// captured so a test can assert on what reached stdout without polluting
// the test binary's own output.
func testOptions(t *testing.T, project string, stdout, stderr *stringSink) Options {
	t.Helper()
	return Options{
		Prompt:           "hello",
		WorkingDirectory: project,
		ConfigPath:       filepath.Join(project, DefaultConfigFile),
		LogLevel:         "error",
		Stdout:           stdout,
		Stderr:           stderr,
	}
}

// stringSink is an io.Writer accumulating everything written to it.
type stringSink struct{ b []byte }

func (s *stringSink) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringSink) String() string { return string(s.b) }
