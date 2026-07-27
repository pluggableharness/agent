package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

func TestRun_exitCodesDistinguishUnusableFromViolated(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args     []string
		wantCode int
	}{
		// A binary that will not start has not failed the suite, it has
		// failed to be tested. A CI job conflating the two reports a
		// conformance regression when the real problem is a bad path.
		"missing binary": {
			args:     []string{filepath.Join(t.TempDir(), "does-not-exist")},
			wantCode: exitUnusable,
		},
		"no binary named": {
			args:     nil,
			wantCode: exitUnusable,
		},
		"too many arguments": {
			args:     []string{"a", "b"},
			wantCode: exitUnusable,
		},
		"unreadable config": {
			args:     []string{"-config", filepath.Join(t.TempDir(), "absent.json"), "some-binary"},
			wantCode: exitUnusable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			code, _ := run(context.Background(), tt.args, io.Discard)
			if code != tt.wantCode {
				t.Errorf("run(%v) = %d, want %d", tt.args, code, tt.wantCode)
			}
		})
	}
}

func TestRun_missingBinaryNamesTheBinary(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "not-a-plugin")
	_, err := run(context.Background(), []string{missing}, io.Discard)
	if err == nil {
		t.Fatal("run() = nil error for a missing binary")
	}
	// The path has to appear, or an operator with several plugins cannot
	// tell which one failed to launch.
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the binary %q", err, missing)
	}
}

func TestRun_usageErrorIsDistinguishable(t *testing.T) {
	t.Parallel()

	_, err := run(context.Background(), nil, io.Discard)
	if !errors.Is(err, errUsage) {
		t.Errorf("err = %v, want errUsage", err)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	valid := filepath.Join(dir, "valid.json")
	if err := os.WriteFile(valid, []byte(`{"api_key":"sk-test","port":8080}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := loadConfig(valid)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if got := cfg.GetFields()["api_key"].GetStringValue(); got != "sk-test" {
		t.Errorf("api_key = %q, want sk-test", got)
	}

	malformed := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(malformed, []byte(`not json`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(malformed); err == nil {
		t.Error("loadConfig accepted malformed JSON")
	}

	// A JSON array is valid JSON but not a config object; rejecting it
	// here beats a confusing failure inside Configure.
	array := filepath.Join(dir, "array.json")
	if err := os.WriteFile(array, []byte(`[1,2,3]`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadConfig(array); err == nil {
		t.Error("loadConfig accepted a JSON array as a config object")
	}
}

func TestFormatReport_summarizesEachOutcome(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		findings []modeltest.Finding
		want     string
	}{
		"clean": {want: "PASS — every check satisfied"},
		"skips only": {
			findings: []modeltest.Finding{{Check: "a", Severity: modeltest.SeveritySkip, Message: "not reached"}},
			want:     "PASS — no violations, 1 check(s) not reached",
		},
		"violations": {
			findings: []modeltest.Finding{{Check: "a", Severity: modeltest.SeverityFail, Message: "violated"}},
			want:     "FAIL — 1 violation(s), 0 check(s) not reached",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := formatReport("some-binary", modeltest.Report{Findings: tt.findings})
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatReport =\n%s\nwant a line containing %q", got, tt.want)
			}
			if !strings.Contains(got, "some-binary") {
				t.Errorf("formatReport does not name the binary:\n%s", got)
			}
		})
	}
}
