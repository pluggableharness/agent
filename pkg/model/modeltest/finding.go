package modeltest

import (
	"fmt"
	"sort"
	"strings"
)

// Severity classifies one check's outcome.
type Severity int

const (
	// SeverityFail is a violated requirement.
	SeverityFail Severity = iota
	// SeveritySkip is a check the run could not reach — most often
	// because the provider never produced the content it inspects. A skip
	// is reported rather than silently omitted, so an unexercised check
	// never reads as a pass.
	SeveritySkip
)

// String renders a Severity for a report line.
func (s Severity) String() string {
	if s == SeveritySkip {
		return "SKIP"
	}
	return "FAIL"
}

// Finding is one check's outcome.
type Finding struct {
	// Check names the requirement, e.g. "StreamCompletion/terminal-event".
	Check string
	// Severity is whether this is a violation or an unreached check.
	Severity Severity
	// Message states what was observed and why it matters.
	Message string
}

// String renders a Finding as one report line.
func (f Finding) String() string {
	return fmt.Sprintf("%s  %s: %s", f.Severity, f.Check, f.Message)
}

// Report is the full outcome of one conformance run.
type Report struct {
	Findings []Finding
}

// Failures returns only the violated requirements.
func (r Report) Failures() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityFail {
			out = append(out, f)
		}
	}
	return out
}

// Skips returns only the checks the run could not reach.
func (r Report) Skips() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeveritySkip {
			out = append(out, f)
		}
	}
	return out
}

// OK reports whether the run found no violations. Skips do not fail a run
// — they are reported so a reader can see what was not covered.
func (r Report) OK() bool {
	return len(r.Failures()) == 0
}

// String renders the whole report, failures first, each group sorted by
// check name so two runs over the same provider produce identical output.
func (r Report) String() string {
	var sb strings.Builder
	write := func(group []Finding) {
		sort.Slice(group, func(i, j int) bool { return group[i].Check < group[j].Check })
		for _, f := range group {
			sb.WriteString(f.String())
			sb.WriteString("\n")
		}
	}
	write(r.Failures())
	write(r.Skips())
	return sb.String()
}

// recorder accumulates findings under a check-name prefix.
//
// The suite's checks talk to this rather than to *testing.T, which is
// what makes the suite testable — a conformance suite that cannot itself
// be shown to reject a bad provider is worth very little — and what lets
// a non-test binary reuse the identical assertions.
type recorder struct {
	prefix   string
	findings *[]Finding
}

// newRecorder returns a recorder writing into findings.
func newRecorder(findings *[]Finding) *recorder {
	return &recorder{findings: findings}
}

// sub returns a recorder whose findings are named under name.
func (r *recorder) sub(name string) *recorder {
	prefix := name
	if r.prefix != "" {
		prefix = r.prefix + "/" + name
	}
	return &recorder{prefix: prefix, findings: r.findings}
}

// failf records a violated requirement.
func (r *recorder) failf(check, format string, args ...any) {
	r.record(check, SeverityFail, fmt.Sprintf(format, args...))
}

// skipf records a check the run could not reach.
func (r *recorder) skipf(check, format string, args ...any) {
	r.record(check, SeveritySkip, fmt.Sprintf(format, args...))
}

func (r *recorder) record(check string, sev Severity, msg string) {
	name := check
	if r.prefix != "" {
		name = r.prefix + "/" + check
	}
	*r.findings = append(*r.findings, Finding{Check: name, Severity: sev, Message: msg})
}
