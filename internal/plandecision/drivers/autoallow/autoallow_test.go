package autoallow_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/pluggableharness/agent/internal/plandecision"
	"github.com/pluggableharness/agent/internal/plandecision/drivers/autoallow"
	"github.com/pluggableharness/agent/internal/telemetry"
	telemetryfake "github.com/pluggableharness/agent/internal/telemetry/drivers/fake"
	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

// entry is one captured slog record, flattened to what the assertions
// below care about.
type entry struct {
	level   slog.Level
	message string
	attrs   map[string]string
}

// recorder is a hand-written slog.Handler test double (go-testing.md:
// fakes, not mocking frameworks) that keeps every record in memory so a
// test can assert exactly what this resolver logged.
type recorder struct {
	mu      sync.Mutex
	entries []entry
}

func (*recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := make(map[string]string, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry{level: rec.Level, message: rec.Message, attrs: attrs})
	return nil
}

func (r *recorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recorder) WithGroup(string) slog.Handler      { return r }

// at returns every record captured at exactly level.
func (r *recorder) at(level slog.Level) []entry {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]entry, 0, len(r.entries))
	for _, e := range r.entries {
		if e.level == level {
			out = append(out, e)
		}
	}
	return out
}

func (r *recorder) logger() *slog.Logger { return slog.New(r) }

// item is a representative ask-decision plan item.
func item() *planv1.PlanItem {
	return &planv1.PlanItem{
		Id:            "pi-1",
		CallId:        "call-1",
		Provider:      "filesystem",
		OperationName: "write_file",
		Decision:      planv1.PlanDecision_PLAN_DECISION_ASK,
		Kind:          toolv1.ToolKind_TOOL_KIND_RESOURCE,
		Risk:          toolv1.RiskClass_RISK_CLASS_HIGH,
	}
}

func request() plandecision.Request {
	return plandecision.Request{SessionID: "sess-1", TurnID: "turn-1", Item: item()}
}

// newResolver builds an acknowledged resolver logging into rec, failing
// the test if construction fails.
func newResolver(t *testing.T, rec *recorder) plandecision.Resolver {
	t.Helper()

	r, err := autoallow.New(autoallow.Config{
		AcknowledgeUnsafeAutoAllow: true,
		Logger:                     rec.logger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestNew_refusesWithoutAcknowledgement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  autoallow.Config
	}{
		{name: "zero value config", cfg: autoallow.Config{}},
		{name: "acknowledgement explicitly false", cfg: autoallow.Config{AcknowledgeUnsafeAutoAllow: false}},
		{name: "logger set but not acknowledged", cfg: autoallow.Config{Logger: slog.Default()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := autoallow.New(tt.cfg)
			if !errors.Is(err, autoallow.ErrNotAcknowledged) {
				t.Fatalf("New: error = %v, want errors.Is ErrNotAcknowledged", err)
			}
			if r != nil {
				t.Fatal("New returned a non-nil Resolver alongside ErrNotAcknowledged")
			}
		})
	}
}

func TestNew_acknowledgedLogsConstructionWarning(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	r := newResolver(t, rec)
	if r == nil {
		t.Fatal("New returned a nil Resolver with a nil error")
	}

	warns := rec.at(slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("construction WARN records = %d, want 1", len(warns))
	}
	for _, key := range []string{"deviation", "reason", "replacement", "decided_by"} {
		if warns[0].attrs[key] == "" {
			t.Errorf("construction WARN is missing a non-empty %q attribute", key)
		}
	}
	if got := warns[0].attrs["decided_by"]; got != autoallow.DecidedBy {
		t.Errorf("construction WARN decided_by = %q, want %q", got, autoallow.DecidedBy)
	}
}

func TestNew_defaultsNilLoggerAndTelemetry(t *testing.T) {
	// Not parallel: slog.Default() is process-global state this test
	// swaps for the duration of the call.
	rec := &recorder{}
	prev := slog.Default()
	slog.SetDefault(rec.logger())
	t.Cleanup(func() { slog.SetDefault(prev) })

	r, err := autoallow.New(autoallow.Config{AcknowledgeUnsafeAutoAllow: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := r.Resolve(context.Background(), request()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// One WARN at construction, one per resolution — a nil Logger and a
	// nil Telemetry must never make this resolver silent.
	if got := len(rec.at(slog.LevelWarn)); got != 2 {
		t.Fatalf("WARN records = %d, want 2 (construction + resolution)", got)
	}
}

// TestResolve_alwaysAllows is behavioral requirement 1: the verdict is
// PLAN_DECISION_ALLOW regardless of the item's risk, kind, or provider.
func TestResolve_alwaysAllows(t *testing.T) {
	t.Parallel()

	risks := []toolv1.RiskClass{
		toolv1.RiskClass_RISK_CLASS_UNSPECIFIED,
		toolv1.RiskClass_RISK_CLASS_READ_ONLY,
		toolv1.RiskClass_RISK_CLASS_LOW,
		toolv1.RiskClass_RISK_CLASS_MODERATE,
		toolv1.RiskClass_RISK_CLASS_HIGH,
		toolv1.RiskClass_RISK_CLASS_CRITICAL,
	}
	kinds := []toolv1.ToolKind{
		toolv1.ToolKind_TOOL_KIND_RESOURCE,
		toolv1.ToolKind_TOOL_KIND_DATA_SOURCE,
		toolv1.ToolKind_TOOL_KIND_INTERACTIVE,
	}
	providers := []string{"filesystem", "shell", "kubernetes"}

	for _, risk := range risks {
		for _, kind := range kinds {
			for _, provider := range providers {
				name := risk.String() + "/" + kind.String() + "/" + provider
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					r := newResolver(t, &recorder{})
					req := request()
					req.Item.Risk = risk
					req.Item.Kind = kind
					req.Item.Provider = provider

					got, err := r.Resolve(context.Background(), req)
					if err != nil {
						t.Fatalf("Resolve: %v", err)
					}
					if got.Decision != planv1.PlanDecision_PLAN_DECISION_ALLOW {
						t.Fatalf("Decision = %v, want PLAN_DECISION_ALLOW", got.Decision)
					}
					if err := plandecision.ValidateDecision(req, got); err != nil {
						t.Fatalf("ValidateDecision: %v", err)
					}
				})
			}
		}
	}
}

// TestResolve_alwaysScopeOnce is behavioral requirement 2: the scope is
// always ONCE, never SESSION or ALWAYS, so this resolver leaves no
// durable state for the real frontend resolver to reconcile.
func TestResolve_alwaysScopeOnce(t *testing.T) {
	t.Parallel()

	r := newResolver(t, &recorder{})

	const resolutions = 5
	for i := range resolutions {
		got, err := r.Resolve(context.Background(), request())
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
		if got.Scope != planv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE {
			t.Fatalf("Resolve #%d: Scope = %v, want PLAN_DECISION_SCOPE_ONCE", i, got.Scope)
		}
	}
}

// TestResolve_neverCorrectsInput is behavioral requirement 3: this
// resolver blanket-approves the original input, it never proposes a
// correction.
func TestResolve_neverCorrectsInput(t *testing.T) {
	t.Parallel()

	r := newResolver(t, &recorder{})

	got, err := r.Resolve(context.Background(), request())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.CorrectedInput != nil {
		t.Fatalf("CorrectedInput = %v, want nil", got.CorrectedInput)
	}
}

// TestResolve_decidedByIsVerbatim is behavioral requirement 4: the
// DecidedBy constant is stamped verbatim on every resolution, with no
// per-item variation, truncation, or wrapping.
func TestResolve_decidedByIsVerbatim(t *testing.T) {
	t.Parallel()

	const want = "UNSAFE-AUTO-ALLOW(no-frontend-attached)"
	if autoallow.DecidedBy != want {
		t.Fatalf("DecidedBy = %q, want %q — this string is load-bearing for audit; do not soften it", autoallow.DecidedBy, want)
	}

	r := newResolver(t, &recorder{})

	for i, provider := range []string{"filesystem", "shell", "http"} {
		req := request()
		req.Item.Id = provider + "-item"
		req.Item.Provider = provider

		got, err := r.Resolve(context.Background(), req)
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
		if got.DecidedBy != want {
			t.Fatalf("Resolve #%d: DecidedBy = %q, want %q", i, got.DecidedBy, want)
		}
	}
}

// TestResolve_logsOneWarnPerResolution is behavioral requirement 5.
func TestResolve_logsOneWarnPerResolution(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	r := newResolver(t, rec)

	const resolutions = 3
	for i := range resolutions {
		if _, err := r.Resolve(context.Background(), request()); err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
	}

	warns := rec.at(slog.LevelWarn)
	// One construction WARN plus exactly one per resolution.
	if len(warns) != resolutions+1 {
		t.Fatalf("WARN records = %d, want %d", len(warns), resolutions+1)
	}

	// logging-telemetry.md's driver rule: entry/exit at DEBUG, alongside
	// (never instead of) the WARN.
	if got := len(rec.at(slog.LevelDebug)); got != resolutions*2 {
		t.Errorf("DEBUG records = %d, want %d (entry + exit per resolution)", got, resolutions*2)
	}

	want := map[string]string{
		"session_id":     "sess-1",
		"plan_item_id":   "pi-1",
		"provider":       "filesystem",
		"operation_name": "write_file",
		"risk":           toolv1.RiskClass_RISK_CLASS_HIGH.String(),
	}
	for i, w := range warns[1:] {
		for key, val := range want {
			if got := w.attrs[key]; got != val {
				t.Errorf("resolution WARN #%d: attr %q = %q, want %q", i, key, got, val)
			}
		}
	}
}

// TestResolve_honorsContextCancellation is behavioral requirement 6.
func TestResolve_honorsContextCancellation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want error
	}{
		{
			name: "already cancelled",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			want: context.Canceled,
		},
		{
			name: "deadline already exceeded",
			ctx: func(t *testing.T) context.Context {
				t.Helper()
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
				t.Cleanup(cancel)
				return ctx
			},
			want: context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			r := newResolver(t, rec)

			got, err := r.Resolve(tt.ctx(t), request())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve: error = %v, want errors.Is %v", err, tt.want)
			}
			if got.Decision != planv1.PlanDecision_PLAN_DECISION_UNSPECIFIED || got.DecidedBy != "" {
				t.Fatalf("Resolve returned a populated Decision %+v alongside an error", got)
			}
			// A cancelled resolve must not claim to have auto-allowed
			// anything: only the construction WARN may be present.
			if n := len(rec.at(slog.LevelWarn)); n != 1 {
				t.Fatalf("WARN records = %d, want 1 (construction only)", n)
			}
		})
	}
}

func TestResolve_rejectsRequestWithoutItem(t *testing.T) {
	t.Parallel()

	r := newResolver(t, &recorder{})

	if _, err := r.Resolve(context.Background(), plandecision.Request{SessionID: "sess-1"}); !errors.Is(err, plandecision.ErrNilItem) {
		t.Fatalf("Resolve: error = %v, want errors.Is plandecision.ErrNilItem", err)
	}
}

func TestResolve_recordsSpanAndMetric(t *testing.T) {
	t.Parallel()

	backend := telemetryfake.New()
	cfg := telemetry.DefaultConfig
	cfg.ServiceName = "autoallow_test"
	prov, err := telemetry.New(context.Background(), cfg, backend, nil)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	t.Cleanup(func() { _ = prov.Shutdown(context.Background()) })

	r, err := autoallow.New(autoallow.Config{
		AcknowledgeUnsafeAutoAllow: true,
		Logger:                     (&recorder{}).logger(),
		Telemetry:                  prov,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := r.Resolve(context.Background(), request()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if err := prov.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	spans := backend.Spans.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("recorded spans = %d, want 1", len(spans))
	}
	if spans[0].Name != "plan.decision.resolve" {
		t.Errorf("span name = %q, want plan.decision.resolve", spans[0].Name)
	}

	var rm metricdata.ResourceMetrics
	if err := backend.Metrics.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !hasMetric(rm, "pluggableharness.policy.decisions") {
		t.Error("pluggableharness.policy.decisions metric was not recorded")
	}
}

func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}
