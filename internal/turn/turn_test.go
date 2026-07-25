package turn

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/pluggableharness/agent/internal/providercatalog/drivers/fake"
	"github.com/pluggableharness/agent/internal/statebackend"
)

// TestNew_requiresEveryCollaborator asserts New names the first missing
// dependency rather than returning a Driver that would panic on its first
// turn.
func TestNew_requiresEveryCollaborator(t *testing.T) {
	t.Parallel()

	full := func() Config {
		rec := &recorder{}
		return Config{
			Hooks:   &fakeHooks{t: t, rec: rec},
			Context: &fakeContext{rec: rec},
			Model:   &fakeModel{t: t, rec: rec},
			Gate:    &fakeGate{rec: rec},
			Tools:   &fakeTools{rec: rec},
			Catalog: fake.New(),
		}
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "Hooks", mutate: func(c *Config) { c.Hooks = nil }},
		{name: "Context", mutate: func(c *Config) { c.Context = nil }},
		{name: "Model", mutate: func(c *Config) { c.Model = nil }},
		{name: "Gate", mutate: func(c *Config) { c.Gate = nil }},
		{name: "Tools", mutate: func(c *Config) { c.Tools = nil }},
		{name: "Catalog", mutate: func(c *Config) { c.Catalog = nil }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := full()
			tc.mutate(&cfg)
			d, err := New(cfg)
			if !errors.Is(err, ErrMissingCollaborator) {
				t.Fatalf("New: got %v, want ErrMissingCollaborator", err)
			}
			if d != nil {
				t.Fatalf("New: returned a Driver alongside an error")
			}
			if got, want := err.Error(), "turn: new: Config."+tc.name+" is required"; got != want {
				t.Fatalf("New: error %q, want %q", got, want)
			}
		})
	}
}

// TestNew_defaultsOptionalDependencies asserts a Config carrying only the
// required collaborators still produces a usable Driver — an ID minter, a
// clock, a logger, and a telemetry provider are all filled in.
func TestNew_defaultsOptionalDependencies(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	d, err := New(Config{
		Hooks:   &fakeHooks{t: t, rec: rec},
		Context: &fakeContext{rec: rec},
		Model:   &fakeModel{t: t, rec: rec},
		Gate:    &fakeGate{rec: rec},
		Tools:   &fakeTools{rec: rec},
		Catalog: fake.New(),
	})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	switch {
	case d.ids == nil:
		t.Fatalf("New: IDs was not defaulted")
	case d.clock == nil:
		t.Fatalf("New: Clock was not defaulted")
	case d.logger == nil:
		t.Fatalf("New: Logger was not defaulted")
	case d.telem == nil:
		t.Fatalf("New: Telemetry was not defaulted")
	}
	if id := d.ids.New(); statebackend.ValidateSessionID(id) != nil {
		t.Fatalf("New: default minter produced %q, want a canonical ULID", id)
	}
}

// TestNew_honorsSuppliedOptionalDependencies asserts a caller's own minter,
// clock, and logger are used rather than silently replaced.
func TestNew_honorsSuppliedOptionalDependencies(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	logger := slog.New(slog.DiscardHandler)
	minter := &seqMinter{}

	d, err := New(Config{
		Hooks:   &fakeHooks{t: t, rec: rec},
		Context: &fakeContext{rec: rec},
		Model:   &fakeModel{t: t, rec: rec},
		Gate:    &fakeGate{rec: rec},
		Tools:   &fakeTools{rec: rec},
		Catalog: fake.New(),
		IDs:     minter,
		Clock:   clock,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	if d.ids.New() != "id-1" {
		t.Fatalf("New: supplied IDMinter was not used")
	}
	if !d.clock().Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("New: supplied Clock was not used")
	}
	if d.logger != logger {
		t.Fatalf("New: supplied Logger was not used")
	}
}

// TestULIDMinter_isTheHouseScheme asserts the production default mints the
// same canonical ULID every other kernel-assigned identifier uses, rather
// than inventing a second id scheme (determinism.md).
func TestULIDMinter_isTheHouseScheme(t *testing.T) {
	t.Parallel()

	m := ulidMinter{clock: time.Now}
	first, second := m.New(), m.New()
	if err := statebackend.ValidateSessionID(first); err != nil {
		t.Fatalf("minted id %q is not a canonical ULID: %v", first, err)
	}
	if first == second {
		t.Fatalf("minter returned the same id twice: %q", first)
	}
}

// TestDedupeSorted covers the tripped-provider reporting helper, including
// the ordering guarantee that keeps a logged or persisted set free of Go
// map iteration order.
func TestDedupeSorted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "empty is nil", in: nil, want: nil},
		{name: "sorts", in: []string{"z", "a"}, want: []string{"a", "z"}},
		{name: "dedupes", in: []string{"a", "b", "a"}, want: []string{"a", "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := dedupeSorted(tc.in); !equalStrings(got, tc.want) {
				t.Fatalf("dedupeSorted(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
