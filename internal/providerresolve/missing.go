package providerresolve

import (
	"fmt"
	"sort"
	"strings"
)

// MissingReason classifies why one required_providers entry could not be
// resolved to a launchable binary. The zero value is deliberately unused
// so an uninitialized Missing is obviously wrong rather than silently
// reading as a real reason.
type MissingReason int

// The reasons a provider can fail to resolve, in the order Resolve
// checks them.
const (
	_ MissingReason = iota

	// MissingNotLocked means required_providers declares the entry but
	// the lock file has no row for it — the provider was never resolved
	// and installed (configuration/lock-file.md).
	MissingNotLocked

	// MissingNotCached means the lock file names a version whose binary
	// is not present in the plugin cache for this platform. Missing.Path
	// carries the path that was checked.
	MissingNotCached

	// MissingNoChecksum means the lock row exists but records no checksum
	// for this platform, so the binary cannot be verified before it runs.
	// The lock file is the source of truth for "what's allowed to run",
	// not merely a cache hint (configuration.md §11), so an unverifiable
	// binary is unresolvable rather than a warning.
	MissingNoChecksum

	// MissingNotExecutable means the binary is present but carries no
	// executable bit for anyone. Missing.Path carries the checked path.
	MissingNotExecutable
)

// String returns a short, stable, lowercase label for r, used in
// MissingError's message and safe to log.
func (r MissingReason) String() string {
	switch r {
	case MissingNotLocked:
		return "not locked"
	case MissingNotCached:
		return "not cached"
	case MissingNoChecksum:
		return "no checksum recorded"
	case MissingNotExecutable:
		return "not executable"
	default:
		return fmt.Sprintf("unknown reason %d", int(r))
	}
}

// Missing is one required_providers entry Resolve could not resolve.
type Missing struct {
	// LocalName is the required_providers local name that failed.
	LocalName string

	// Source is the git-forge address declared for it, carried so an
	// operator reading the error knows what to install without
	// cross-referencing agent.hcl.
	Source string

	// Constraint is the raw version constraint declared in
	// required_providers (e.g. "~> 1.2.3").
	Constraint string

	// Version is the concrete version the lock file resolved, empty when
	// Reason is MissingNotLocked (there is no lock row to read one from).
	Version string

	// Reason classifies the failure.
	Reason MissingReason

	// Path is the binary path that was checked, empty for reasons that
	// never got as far as naming one (MissingNotLocked).
	Path string
}

// MissingError reports every required_providers entry Resolve could not
// resolve, accumulated across the whole pass rather than reported one at
// a time.
type MissingError struct {
	// Missing holds one entry per unresolvable provider. Resolve
	// populates it in Order's sequence; Error sorts by LocalName so the
	// rendered message is stable regardless of how it was built.
	Missing []Missing
}

// Error implements the error interface with one line per missing entry,
// sorted by LocalName.
func (e *MissingError) Error() string {
	entries := make([]Missing, len(e.Missing))
	copy(entries, e.Missing)
	sort.Slice(entries, func(i, j int) bool { return entries[i].LocalName < entries[j].LocalName })

	var b strings.Builder
	b.WriteString("providerresolve: unresolved providers:")
	for _, m := range entries {
		b.WriteString("\n  ")
		b.WriteString(m.LocalName)
		b.WriteString(" (")
		b.WriteString(m.Source)
		if m.Version != "" {
			b.WriteString("@")
			b.WriteString(m.Version)
		} else if m.Constraint != "" {
			b.WriteString(" ")
			b.WriteString(m.Constraint)
		}
		b.WriteString("): ")
		b.WriteString(m.Reason.String())
		if m.Path != "" {
			b.WriteString(": ")
			b.WriteString(m.Path)
		}
	}
	return b.String()
}
