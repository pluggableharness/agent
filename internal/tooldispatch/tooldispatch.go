// Package tooldispatch implements the turn-level tool-call scheduler and
// Invoke client described in
// docs/specifications/agent-loop/turn-algorithm.md#turn-level-tool-call-concurrency
// and docs/specifications/tool/protocol.md#invoke. See doc.go for the
// package-level overview and CLAUDE.md for the lock-ordering rule and the
// interactive/concurrent structural split.
package tooldispatch

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/callhash"
	"github.com/pluggableharness/agent/internal/circuitbreaker"
	"github.com/pluggableharness/agent/internal/interactive"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/statebackend"
	"github.com/pluggableharness/agent/internal/telemetry"
	"github.com/pluggableharness/agent/internal/telemetry/drivers/noop"

	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// maxWeight is the semaphore.Weighted capacity used for every provider
// lock. An exclusive (safe:false or undeclared ConcurrencySpec) acquire
// takes the whole capacity; a shared (safe:true) acquire takes weight 1,
// so up to maxWeight-1 shared calls can run concurrently against one
// provider while a single exclusive call excludes all of them. The exact
// value is arbitrary as long as it comfortably exceeds any real turn's
// call fan-out.
const maxWeight = 1 << 20

// eventSchemaVersion is the events.schema_version this package writes for
// both EVENT_KIND_TOOL_CALL and EVENT_KIND_TOOL_RESULT rows — "1", per
// state-backend.md#the-kind-enum's "Each message above IS
// events.schema_version = 1 of its kind's payload" rule for the current,
// only-ever-released generation of ToolCallEvent/ToolResultEvent.
const eventSchemaVersion = "1"

// BreakerTrippedDetail is the Outcome.Error.Details field name carrying a
// crash-driven circuit-breaker trip — see Outcome.Error's doc comment for
// why the signal rides inside Details rather than as a field of its own.
//
// It is exported because it is a real cross-package contract: this
// package writes it, and internal/turn reads it to route a tripped
// provider through the limit-reached path. Both sides MUST reference this
// constant rather than repeating the literal, so the producer and the
// consumer cannot silently drift apart — which is exactly what happened
// before internal/turn read it at all.
const BreakerTrippedDetail = "breaker_tripped"

// BreakerProviderDetail is the Outcome.Error.Details field naming the
// provider whose breaker tripped. It accompanies BreakerTrippedDetail and
// exists for a caller inspecting a persisted tool_result event, which no
// longer has the live ToolHandle the provider name would otherwise come
// from.
const BreakerProviderDetail = "provider"

// Call is one resolved tool call ready to execute: the kernel-built
// ToolCall plus the live provider handle to invoke it against. Declared
// here rather than imported from internal/plangate — this package MUST
// NOT import plangate (see CLAUDE.md); a future internal/turn glues
// plangate's plan/apply output and this package's scheduling together.
type Call struct {
	// Call is the fully-built ToolCall — id, tool_name, arguments, and
	// call_context all already set by the caller (this package never
	// mutates or completes a ToolCall).
	Call *toolv1.ToolCall
	// Handle is the live resolved provider/operation this call executes
	// against — its Schema is what ConcurrencySpec, output_schema, and
	// default_timeout are read from, and its Client is what Invoke is
	// called on.
	Handle providercatalog.ToolHandle
}

// Outcome is one call's terminal result, in the shape both step 9's
// data_source group and step 12's post-approval resource group share.
// Exactly one of Result/Error is non-nil.
type Outcome struct {
	// Call is the ToolCall this outcome answers, echoed back for a
	// caller that only has the Outcome slice in hand.
	Call *toolv1.ToolCall
	// Result is the successful terminal payload. Nil iff Error is set.
	Result *toolv1.ToolResult
	// Error is the failed terminal payload. Nil iff Result is set.
	//
	// A TOOL_ERROR_CATEGORY_PROCESS_CRASHED Error whose crash tripped
	// cfg.Breaker carries a "breaker_tripped": true boolean field in
	// Details — this package has no limit-reached path of its own to
	// route through (that's the future internal/turn's job), so the trip
	// signal rides inside the existing Details field rather than
	// widening this struct's shape. See CLAUDE.md.
	Error *toolv1.ToolError
	// ExitCode is set when the provider emitted an exit_status event
	// (exec-family operations only); nil otherwise.
	ExitCode *int32
	// Sequence is the state-backend sequence number of the persisted
	// tool_result event for this call.
	Sequence int64
}

// EventSink is the subset of *statebackend.Session this package needs to
// persist tool_call/tool_result events — narrowed to one method so a
// caller can inject a fake in tests without standing up a real session
// file, per go-layout.md's "define the interface where it's consumed"
// rule. *statebackend.Session satisfies this directly.
type EventSink interface {
	AppendEvent(ctx context.Context, ev statebackend.Event) (int64, error)
}

// Config is a Scheduler's dependencies and tunables. Every field is
// required for a production Scheduler except SerializeAll, which
// defaults to false (per-call ConcurrencySpec honored).
type Config struct {
	// Interactive resolves an interactive-kind call to a human's (or
	// synthetic) answer. Used only by ExecuteInteractive.
	Interactive interactive.Resolver
	// Breaker tracks per-provider crash counts. A crash recorded by
	// Execute that trips Breaker is surfaced via Outcome.Error.Details —
	// see Outcome's doc comment. A nil Breaker disables crash tracking.
	Breaker *circuitbreaker.Breaker
	// Events persists one tool_call event before, and one tool_result
	// event after, every call this Scheduler runs.
	Events EventSink
	// DefaultTimeout is the deadline applied to Invoke when the
	// operation's own ToolSchema.default_timeout is absent
	// (settings.default_tool_timeout_ms). Zero means no deadline is
	// applied at all in that case.
	DefaultTimeout time.Duration
	// SerializeAll forces strictly sequential execution in Execute,
	// regardless of any call's declared ConcurrencySpec — set true for a
	// model whose ModelSpec.supports_parallel_tool_calls is false.
	SerializeAll bool
	// Clock supplies the display-only timestamp and the ULID event id
	// stamped onto every persisted tool_call/tool_result event, and the
	// two readings the Invoke duration metric is the difference of.
	// Defaults to time.Now.
	//
	// It is injectable for the same reason internal/plangate, internal/
	// hookdispatch, internal/sessionstate, and internal/modelcall all take
	// one: a test pins it, and one reading per event keeps an event's ULID
	// timestamp and its Timestamp column the same instant rather than two
	// adjacent ones. Never an ordering authority — sequence is
	// (.claude/rules/determinism.md).
	Clock func() time.Time
	// Telemetry provides tracing/metrics. A nil Telemetry falls back to
	// a Provider with every signal disabled, matching internal/
	// sessionstate and internal/eventbus's own fallback convention.
	Telemetry *telemetry.Provider
	// Logger receives structured logs. A nil Logger falls back to
	// slog.Default().
	Logger *slog.Logger
}

// Scheduler runs tool calls per
// turn-algorithm.md#turn-level-tool-call-concurrency, serving both step
// 9 (data_source, concurrent) and step 12 (resource, after plan
// approval) with the same scheduling mechanism — "one mechanism for
// both, not two separate rules." The zero value is not usable; construct
// with New.
//
// # Lock ordering
//
// Every call acquires its provider-wide semaphore FIRST and its
// per-key semaphore (if any) SECOND, and releases in the reverse order.
// This is the one rule that makes the two-level scheme deadlock-free by
// construction: since every goroutine that ever holds a key semaphore
// already holds the provider semaphore acquired in the same order first,
// no cycle of goroutines can each be waiting on a lock the next one
// already holds in the opposite order. Never acquire a key semaphore
// before its provider semaphore.
type Scheduler struct {
	cfg Config

	mu           sync.Mutex
	providerSems map[string]*semaphore.Weighted
	keySems      map[string]*semaphore.Weighted

	loggedMu        sync.Mutex
	loggedUnspecOut map[string]struct{} // "provider\x00tool" already DEBUG-logged for unspecified output_schema
}

// defaultTelemetryProvider builds the Provider a Scheduler falls back to
// when New is called with a nil Telemetry, matching internal/
// sessionstate.defaultTelemetryProvider's fallback convention.
func defaultTelemetryProvider() (*telemetry.Provider, error) {
	return telemetry.New(context.Background(), telemetry.Config{}, noop.New(), nil)
}

// New returns a Scheduler configured per cfg. A nil cfg.Logger falls
// back to slog.Default(); a nil cfg.Telemetry falls back to a Provider
// with every signal disabled.
func New(cfg Config) *Scheduler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Telemetry == nil {
		// Unreachable in practice once wired: this package's own fixed,
		// valid telemetry.Config{} zero value cannot fail, the same
		// reasoning internal/sessionstate.NewLive gives for panicking
		// here rather than threading an error through a constructor
		// every other caller expects to be infallible given no required
		// arguments.
		prov, err := defaultTelemetryProvider()
		if err != nil {
			panic(err)
		}
		cfg.Telemetry = prov
	}

	return &Scheduler{
		cfg:             cfg,
		providerSems:    make(map[string]*semaphore.Weighted),
		keySems:         make(map[string]*semaphore.Weighted),
		loggedUnspecOut: make(map[string]struct{}),
	}
}

// callTimeout resolves one call's Invoke deadline: the operation's own
// declared ToolSchema.default_timeout when it has one, otherwise
// cfg.DefaultTimeout (settings.default_tool_timeout_ms). Zero means no
// deadline is applied.
func (s *Scheduler) callTimeout(schema *toolv1.ToolSchema) time.Duration {
	if dt := schema.GetDefaultTimeout(); dt != nil {
		return dt.AsDuration()
	}
	return s.cfg.DefaultTimeout
}

// providerSemaphore returns the shared provider-wide semaphore for
// provider, creating it on first use.
func (s *Scheduler) providerSemaphore(provider string) *semaphore.Weighted {
	s.mu.Lock()
	defer s.mu.Unlock()
	sem, ok := s.providerSems[provider]
	if !ok {
		sem = semaphore.NewWeighted(maxWeight)
		s.providerSems[provider] = sem
	}
	return sem
}

// keySemaphore returns the shared per-key semaphore for key, creating it
// on first use. key already encodes (provider_name, tool_name,
// value(key_fields)) — see concurrencyKey.
func (s *Scheduler) keySemaphore(key string) *semaphore.Weighted {
	s.mu.Lock()
	defer s.mu.Unlock()
	sem, ok := s.keySems[key]
	if !ok {
		sem = semaphore.NewWeighted(1)
		s.keySems[key] = sem
	}
	return sem
}

// concurrencyKey computes the ConcurrencySpec scheduling key for one
// call, per tool/data-types.md#concurrencyspec: safe reports whether the
// operation declared itself concurrency-safe (false for a nil spec — "a
// provider that does not populate ConcurrencySpec at all MUST be treated
// by the kernel as safe: false", the conservative default); key/hasKey
// report the per-key serialization token, only meaningful when safe is
// true and the operation declared a non-empty key_fields. Per data-types.md's
// "omitting key_fields under safe == true asserts that no two calls to
// this operation can ever conflict" — a safe:true operation with no
// key_fields gets no per-key lock at all, only the shared provider-wide
// weight.
func concurrencyKey(provider, tool string, args *structpb.Struct, spec *toolv1.ConcurrencySpec) (safe bool, key string, hasKey bool) {
	safe = spec.GetSafe()
	if !safe {
		return false, "", false
	}
	keyFields := spec.GetKeyFields()
	if len(keyFields) == 0 {
		return true, "", false
	}
	value := callhash.Fields(args, keyFields)
	return true, provider + "\x00" + tool + "\x00" + value, true
}

// acquireLocks acquires provider (and, if hasKey, key) semaphores for one
// call, in that order — see Scheduler's "Lock ordering" doc comment. The
// returned release func releases in the reverse order and is always
// non-nil when err is nil; a caller MUST defer release() immediately.
// Skips locking entirely (returns a no-op release, nil error) when
// s.cfg.SerializeAll is set, since a single sequential caller can never
// contend with itself.
func (s *Scheduler) acquireLocks(ctx context.Context, provider string, safe bool, key string, hasKey bool) (release func(), err error) {
	if s.cfg.SerializeAll {
		return func() {}, nil
	}

	providerSem := s.providerSemaphore(provider)
	weight := int64(1)
	if !safe {
		weight = maxWeight
	}
	if err := providerSem.Acquire(ctx, weight); err != nil {
		return nil, err
	}

	var keySem *semaphore.Weighted
	if hasKey {
		keySem = s.keySemaphore(key)
		if err := keySem.Acquire(ctx, 1); err != nil {
			providerSem.Release(weight)
			return nil, err
		}
	}

	return func() {
		if keySem != nil {
			keySem.Release(1)
		}
		providerSem.Release(weight)
	}, nil
}
