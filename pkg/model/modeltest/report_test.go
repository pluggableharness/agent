package modeltest_test

import (
	"strings"
	"testing"

	"github.com/pluggableharness/agent/pkg/model/modeltest"
)

func TestReport_okIgnoresSkips(t *testing.T) {
	t.Parallel()

	// A skip means a requirement was not reached, which is worth seeing
	// but is not a violation — a run that skipped everything and violated
	// nothing has still not failed.
	rep := modeltest.Report{Findings: []modeltest.Finding{
		{Check: "a", Severity: modeltest.SeveritySkip, Message: "not reached"},
	}}
	if !rep.OK() {
		t.Error("OK() = false for a skip-only report, want true")
	}
	if len(rep.Skips()) != 1 || len(rep.Failures()) != 0 {
		t.Errorf("Skips=%d Failures=%d, want 1 and 0", len(rep.Skips()), len(rep.Failures()))
	}
}

func TestReport_okIsFalseWithAnyFailure(t *testing.T) {
	t.Parallel()

	rep := modeltest.Report{Findings: []modeltest.Finding{
		{Check: "a", Severity: modeltest.SeveritySkip, Message: "not reached"},
		{Check: "b", Severity: modeltest.SeverityFail, Message: "violated"},
	}}
	if rep.OK() {
		t.Error("OK() = true with a failure present, want false")
	}
}

func TestReport_stringOrdersFailuresFirstAndSortsEachGroup(t *testing.T) {
	t.Parallel()

	// Failures first because they are what a reader is looking for, and
	// each group sorted so two runs over the same provider produce
	// byte-identical output — an unsorted report makes a diff unreadable.
	rep := modeltest.Report{Findings: []modeltest.Finding{
		{Check: "zeta", Severity: modeltest.SeveritySkip, Message: "s1"},
		{Check: "beta", Severity: modeltest.SeverityFail, Message: "f1"},
		{Check: "alpha", Severity: modeltest.SeveritySkip, Message: "s2"},
		{Check: "alpha", Severity: modeltest.SeverityFail, Message: "f2"},
	}}

	got := rep.String()
	want := strings.Join([]string{
		"FAIL  alpha: f2",
		"FAIL  beta: f1",
		"SKIP  alpha: s2",
		"SKIP  zeta: s1",
		"",
	}, "\n")
	if got != want {
		t.Errorf("String() =\n%q\nwant\n%q", got, want)
	}
}

func TestSeverity_string(t *testing.T) {
	t.Parallel()

	if got := modeltest.SeverityFail.String(); got != "FAIL" {
		t.Errorf("SeverityFail = %q, want FAIL", got)
	}
	if got := modeltest.SeveritySkip.String(); got != "SKIP" {
		t.Errorf("SeveritySkip = %q, want SKIP", got)
	}
}

func TestReport_emptyIsOK(t *testing.T) {
	t.Parallel()

	var rep modeltest.Report
	if !rep.OK() {
		t.Error("OK() = false for an empty report, want true")
	}
	if rep.String() != "" {
		t.Errorf("String() = %q, want empty", rep.String())
	}
}
