package kernel

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/doomloop"
)

// An empty Prompt is no longer an error: it selects frontend-hosted mode.
// Whether that mode is actually usable depends on a frontend provider being
// loaded, which normalize cannot know and hostFrontend reports instead.
func TestOptionsNormalize_emptyPromptSelectsHostedMode(t *testing.T) {
	t.Parallel()

	got, err := (Options{}).normalize()
	if err != nil {
		t.Fatalf("normalize with no prompt = %v, want nil", err)
	}
	if got.Prompt != "" {
		t.Errorf("Prompt = %q, want empty", got.Prompt)
	}
}

func TestOptionsNormalize_fillsDefaults(t *testing.T) {
	t.Parallel()

	got, err := Options{Prompt: "hi"}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.WorkingDirectory == "" {
		t.Error("WorkingDirectory not defaulted")
	}
	if got.Stdout == nil || got.Stderr == nil {
		t.Error("Stdout/Stderr not defaulted")
	}
}

func TestOptionsNormalize_keepsExplicitValues(t *testing.T) {
	t.Parallel()

	stdout, stderr := &stringSink{}, &stringSink{}
	got, err := Options{Prompt: "hi", WorkingDirectory: "/somewhere", Stdout: stdout, Stderr: stderr}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.WorkingDirectory != "/somewhere" {
		t.Errorf("WorkingDirectory = %q, want /somewhere", got.WorkingDirectory)
	}
	if got.Stdout != stdout || got.Stderr != stderr {
		t.Error("explicit writers replaced")
	}
}

func TestFinalText(t *testing.T) {
	t.Parallel()

	text := func(s string) *contentv1.ContentBlock {
		return &contentv1.ContentBlock{
			Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: s}},
		}
	}
	thinking := &contentv1.ContentBlock{
		Block: &contentv1.ContentBlock_Thinking{Thinking: &contentv1.ThinkingBlock{Text: "ignored"}},
	}

	tests := []struct {
		name string
		msg  *contentv1.Message
		want string
	}{
		{"nil message", nil, ""},
		{"no blocks", &contentv1.Message{}, ""},
		{"one text block", &contentv1.Message{Content: []*contentv1.ContentBlock{text("hi")}}, "hi\n"},
		{"blocks concatenate in order", &contentv1.Message{Content: []*contentv1.ContentBlock{text("a"), text("b")}}, "ab\n"},
		{"non-text blocks are skipped", &contentv1.Message{Content: []*contentv1.ContentBlock{thinking, text("real")}}, "real\n"},
		{"only non-text blocks render nothing", &contentv1.Message{Content: []*contentv1.ContentBlock{thinking}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := finalText(tc.msg); got != tc.want {
				t.Errorf("finalText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaxDepth(t *testing.T) {
	t.Parallel()

	five := 5
	zero := 0
	negative := -3
	tests := []struct {
		name string
		in   *int
		want int
	}{
		{"unset is effectively unbounded", nil, math.MaxInt32},
		{"zero is effectively unbounded", &zero, math.MaxInt32},
		{"negative is effectively unbounded", &negative, math.MaxInt32},
		{"a real limit passes through", &five, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := maxDepth(tc.in); got != tc.want {
				t.Errorf("maxDepth = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDoomLoopConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   config.DoomLoopSettings
		want doomloop.Config
	}{
		{"zero value falls back to the canonical default", config.DoomLoopSettings{}, doomloop.DefaultConfig},
		{"a missing threshold falls back", config.DoomLoopSettings{WindowSize: 9}, doomloop.DefaultConfig},
		{"a configured pair passes through", config.DoomLoopSettings{WindowSize: 9, Threshold: 4}, doomloop.Config{WindowSize: 9, Threshold: 4}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := doomLoopConfig(tc.in); got != tc.want {
				t.Errorf("doomLoopConfig = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTimeoutHelpers(t *testing.T) {
	t.Parallel()

	if got, want := hookTimeout(0), time.Duration(config.DefaultHookTimeoutMS)*time.Millisecond; got != want {
		t.Errorf("hookTimeout(0) = %v, want %v", got, want)
	}
	if got, want := hookTimeout(250), 250*time.Millisecond; got != want {
		t.Errorf("hookTimeout(250) = %v, want %v", got, want)
	}
	if got, want := toolTimeout(-1), time.Duration(config.DefaultToolTimeoutMS)*time.Millisecond; got != want {
		t.Errorf("toolTimeout(-1) = %v, want %v", got, want)
	}
	if got, want := toolTimeout(1500), 1500*time.Millisecond; got != want {
		t.Errorf("toolTimeout(1500) = %v, want %v", got, want)
	}
}

func TestFileExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if fileExists(filepath.Join(dir, "nope.hcl")) {
		t.Error("fileExists reported a missing file as present")
	}
	if !fileExists(dir) {
		t.Error("fileExists reported an existing path as absent")
	}
}

// TestRun_missingConfigIsAClearError asserts the single most likely
// first-run failure names the path and says what the file is for, rather
// than surfacing an HCL parser diagnostic.
func TestRun_missingConfigIsAClearError(t *testing.T) {
	project := newProject(t, "")

	err := Run(context.Background(), testOptions(t, project, &stringSink{}, &stringSink{}))
	if err == nil {
		t.Fatal("Run with no agent.hcl succeeded, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, DefaultConfigFile) {
		t.Errorf("error %q does not name the config file", msg)
	}
	if !strings.Contains(msg, "no config file at") {
		t.Errorf("error %q is not the missing-config message", msg)
	}
}

// TestRun_invalidConfigFailsBeforeAnythingStarts asserts a malformed
// agent.hcl surfaces the loader's own diagnostic rather than crashing
// later during plugin bring-up.
func TestRun_invalidConfigFailsBeforeAnythingStarts(t *testing.T) {
	project := newProject(t, "settings { this is not hcl")

	err := Run(context.Background(), testOptions(t, project, &stringSink{}, &stringSink{}))
	if err == nil {
		t.Fatal("Run with malformed agent.hcl succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("error %q is not a config-load failure", err)
	}
}

// TestRun_unknownLogLevelIsRejected asserts the -log-level override is
// validated rather than silently ignored.
func TestRun_unknownLogLevelIsRejected(t *testing.T) {
	project := newProject(t, minimalConfig)
	opts := testOptions(t, project, &stringSink{}, &stringSink{})
	opts.LogLevel = "chatty"

	err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "log level") {
		t.Fatalf("Run with an unknown log level = %v, want a log-level error", err)
	}
}

// TestRun_hostedModeWithoutAFrontendIsRejected asserts the kernel refuses
// to sit idle. Without a prompt it waits for a frontend to drive it, so a
// configuration that loads none would block forever with nothing an
// operator could see happening — the one outcome worse than an error.
func TestRun_hostedModeWithoutAFrontendIsRejected(t *testing.T) {
	project := newProject(t, minimalConfig)
	opts := testOptions(t, project, &stringSink{}, &stringSink{})
	opts.Prompt = ""

	err := Run(context.Background(), opts)
	if !errors.Is(err, ErrNoFrontend) {
		t.Fatalf("Run with no prompt and no frontend = %v, want ErrNoFrontend", err)
	}
}

// TestRun_noModelProviderFailsTheSession asserts a kernel with no plugins
// at all still brings up, runs, and reports the real reason it cannot
// proceed — the profile has no model to route to.
func TestRun_noModelProviderFailsTheSession(t *testing.T) {
	project := newProject(t, minimalConfig)
	stdout := &stringSink{}

	err := Run(context.Background(), testOptions(t, project, stdout, &stringSink{}))
	if err == nil {
		t.Fatal("Run with no model provider succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "kernel: session") {
		t.Errorf("error %q is not a session failure", err)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want nothing written on a failed session", stdout.String())
	}
}
