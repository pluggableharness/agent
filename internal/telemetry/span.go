package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Span names for this package's instrumentation scope (pluggableharness-agent/kernel).
const (
	spanNameSession          = "session"
	spanNameTurn             = "turn"
	spanNameHookDispatch     = "hook.dispatch"
	spanNameHookSubscriber   = "hook.subscriber"
	spanNameModelCall        = "model.call"
	spanNameToolExecute      = "tool.execute"
	spanNamePolicyEvaluate   = "policy.evaluate"
	spanNameRunSessionSpawn  = "session.spawn"
	spanNameConfigLoad       = "config.load"
	spanNameGlobalConfigLoad = "registry.global_config.load"
	spanNameLockFileLoad     = "registry.lockfile.load"
	spanNameChecksumVerify   = "registry.checksum.verify"
	spanNamePluginLaunch     = "plugin.launch"

	spanNameStateBackendSessionCreate   = "statebackend.session.create"
	spanNameStateBackendSessionOpen     = "statebackend.session.open"
	spanNameStateBackendEventAppend     = "statebackend.event.append"
	spanNameStateBackendMessageAppend   = "statebackend.message.append"
	spanNameStateBackendPlanAppend      = "statebackend.plan.append"
	spanNameStateBackendStatusSet       = "statebackend.session.set_status"
	spanNameStateBackendSessionClose    = "statebackend.session.close"
	spanNameStateBackendIntegrityCheck  = "statebackend.session.integrity_check"
	spanNameStateBackendMetaQuery       = "statebackend.query.meta"
	spanNameStateBackendEventsQuery     = "statebackend.query.events"
	spanNameStateBackendProducersQuery  = "statebackend.query.producers"
	spanNameStateBackendCostQuery       = "statebackend.query.total_cost"
	spanNameStateBackendCostLedgerQuery = "statebackend.query.cost_ledger"
	spanNameStateBackendPlanItemsQuery  = "statebackend.query.plan_items"

	spanNameEventBusPublish = "eventbus.publish"

	spanNameKernelCallbackExportSpans        = "kernelcallback.export_spans"
	spanNameKernelCallbackRecordMetrics      = "kernelcallback.record_metrics"
	spanNameKernelCallbackGetTelemetryConfig = "kernelcallback.get_telemetry_config"
	spanNameKernelCallbackGetConfig          = "kernelcallback.get_config"
	spanNameKernelCallbackPublish            = "kernelcallback.publish"
	spanNameKernelCallbackSubscribe          = "kernelcallback.subscribe"
	spanNameKernelCallbackReadEvents         = "kernelcallback.read_events"
	spanNameKernelCallbackGetSession         = "kernelcallback.get_session"
)

// SessionSpan describes the session a StartSession call is opening
// (agent-loop.md §7's RunSession tree).
type SessionSpan struct {
	SessionID       string
	ParentSessionID string // empty for a root session
	RootSessionID   string // equal to SessionID for a root session
	Depth           int
	AgentProfile    string
}

// StartSession opens the span covering one whole session (root or
// sub-agent), from session-start to session-end (agent-loop.md §1, §7).
func (p *Provider) StartSession(ctx context.Context, s SessionSpan) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameSession, trace.WithAttributes(
		SessionIDKey.String(s.SessionID),
		SessionParentIDKey.String(s.ParentSessionID),
		SessionRootIDKey.String(s.RootSessionID),
		SessionDepthKey.Int(s.Depth),
		AgentProfileKey.String(s.AgentProfile),
	))
}

// StartTurn opens the span covering one RunTurn iteration
// (agent-loop.md §2).
func (p *Provider) StartTurn(ctx context.Context, turnIndex int) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameTurn, trace.WithAttributes(TurnIndexKey.Int(turnIndex)))
}

// StartHookDispatch opens the span covering one hook point's whole
// dispatch — the ordered subscriber chain for point, not a single
// subscriber (agent-loop.md §4). A subscriber's own work, instrumented via
// StartHookSubscriber using the ctx this returns, nests as a child of this
// span, so concurrent observe-mode subscribers (agent-loop.md §4.4) are
// visible as sibling children in the trace.
func (p *Provider) StartHookDispatch(ctx context.Context, point string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameHookDispatch, trace.WithAttributes(HookPointKey.String(point)))
}

// StartHookSubscriber opens the span covering one subscriber's invocation
// within a hook dispatch (agent-loop.md §4). producer may be nil for a
// kernel-internal subscriber (e.g. the policy engine's plan-ready veto).
func (p *Provider) StartHookSubscriber(ctx context.Context, mode string, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	attrs := append([]attribute.KeyValue{SubscriberModeKey.String(mode)}, producerAttributes(producer)...)
	return p.tracer.Start(ctx, spanNameHookSubscriber, trace.WithAttributes(attrs...))
}

// StartModelCall opens the span covering steps 3-4 of RunTurn
// (agent-loop.md §2): the StreamCompletion RPC and accumulating its
// stream into the canonical message. Call RecordUsage on the returned
// span once usage is known, and end it with EndSpan.
func (p *Provider) StartModelCall(ctx context.Context, modelID string, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	attrs := append([]attribute.KeyValue{
		semconv.GenAIRequestModelKey.String(modelID),
		ModelIDKey.String(modelID),
	}, producerAttributes(producer)...)
	return p.tracer.Start(ctx, spanNameModelCall, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
}

// StartToolExecute opens the span covering one resolved tool call's
// execution (steps 9/9b/12 of agent-loop.md §2).
func (p *Provider) StartToolExecute(ctx context.Context, toolName, toolKind string, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	attrs := append([]attribute.KeyValue{
		ToolNameKey.String(toolName),
		ToolKindKey.String(toolKind),
	}, producerAttributes(producer)...)
	return p.tracer.Start(ctx, spanNameToolExecute, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
}

// StartPolicyEvaluate opens the span covering plan/policy evaluation — the
// plan-ready hook's veto chain plus any tool-call prechecks
// (agent-loop.md §5.1).
func (p *Provider) StartPolicyEvaluate(ctx context.Context) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNamePolicyEvaluate)
}

// StartRunSessionSpawn opens the span covering a RunSession callback that
// spawns a sub-agent session (kernel-callbacks.md §1, agent-loop.md §7).
// The returned ctx is what carries the trace across the callback channel
// into the child session's own StartSession span (grpchooks.go).
func (p *Provider) StartRunSessionSpawn(ctx context.Context, childSessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameRunSessionSpawn, trace.WithAttributes(SessionIDKey.String(childSessionID)))
}

// StartConfigLoad opens the span covering one local `agent.hcl` file load
// (configuration.md), for use by config.LoadFile.
func (p *Provider) StartConfigLoad(ctx context.Context, path string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameConfigLoad, trace.WithAttributes(FilePathKey.String(path)))
}

// StartGlobalConfigLoad opens the span covering one local global config
// file load (configuration.md), for use by registry.LoadGlobalConfig.
func (p *Provider) StartGlobalConfigLoad(ctx context.Context, path string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameGlobalConfigLoad, trace.WithAttributes(FilePathKey.String(path)))
}

// StartLockFileLoad opens the span covering one local lock file load
// (configuration.md), for use by registry.LoadLockFile.
func (p *Provider) StartLockFileLoad(ctx context.Context, path string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameLockFileLoad, trace.WithAttributes(FilePathKey.String(path)))
}

// StartChecksumVerify opens the span covering one plugin binary checksum
// verification against the lock file (configuration.md), for use by
// registry.VerifyChecksum.
func (p *Provider) StartChecksumVerify(ctx context.Context, path, platform string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameChecksumVerify, trace.WithAttributes(
		FilePathKey.String(path),
		PlatformKey.String(platform),
	))
}

// StartPluginLaunch opens the span covering one plugin subprocess launch:
// spawn (exec.CommandContext) through handshake completion, up to but not
// including the first category RPC. Ended via EndSpan by the caller
// (internal/pluginruntime.Launch).
func (p *Provider) StartPluginLaunch(ctx context.Context, category, name, version string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNamePluginLaunch, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		ProducerCategoryKey.String(category),
		ProducerNameKey.String(name),
		ProducerVersionKey.String(version),
	))
}

// StartStateBackendSessionCreate opens the span covering one session file's
// creation — file create, schema apply, PRAGMA user_version stamp, and the
// initial session_meta insert (docs/specifications/state-backend.md#file-layout,
// docs/specifications/state-backend.md#schema-migration) — for use by
// statebackend.Store.Create.
func (p *Provider) StartStateBackendSessionCreate(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendSessionCreate, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendSessionOpen opens the span covering one session file's
// open — the PRAGMA user_version check before any other operation touches
// the file, plus any migration it triggers
// (docs/specifications/state-backend.md#schema-migration) — for use by
// statebackend.Store.Open.
func (p *Provider) StartStateBackendSessionOpen(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendSessionOpen, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendEventAppend opens the span covering one events-table
// append plus its same-transaction producers upsert
// (docs/specifications/state-backend.md#events,
// docs/specifications/state-backend.md#producers), for use by
// statebackend.Session.AppendEvent.
func (p *Provider) StartStateBackendEventAppend(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendEventAppend, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendMessageAppend opens the span covering one events-table
// append plus its same-transaction cost_ledger insert
// (docs/specifications/state-backend.md#cost_ledger), for use by
// statebackend.Session.AppendMessage.
func (p *Provider) StartStateBackendMessageAppend(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendMessageAppend, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendPlanAppend opens the span covering one events-table
// append plus its same-transaction plan_items inserts
// (docs/specifications/state-backend.md#plan_items), for use by
// statebackend.Session.AppendPlan.
func (p *Provider) StartStateBackendPlanAppend(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendPlanAppend, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendStatusSet opens the span covering one in-place
// session_meta update (docs/specifications/state-backend.md#session_meta —
// the one mutable table), for use by statebackend.Session.SetStatus.
func (p *Provider) StartStateBackendStatusSet(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendStatusSet, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendSessionClose opens the span covering one session file's
// close: PRAGMA wal_checkpoint(TRUNCATE) followed by closing the
// underlying *sql.DB, for use by statebackend.Session.Close.
func (p *Provider) StartStateBackendSessionClose(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendSessionClose, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendIntegrityCheck opens the span covering one session
// file's PRAGMA integrity_check on open, plus any salvage recovery it
// triggers (docs/specifications/state-backend.md#corruption-recovery), for
// use by statebackend.Store's Open-time integrity check.
func (p *Provider) StartStateBackendIntegrityCheck(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendIntegrityCheck, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendMetaQuery opens the span covering one session_meta read
// (docs/specifications/state-backend.md#session_meta), for use by
// statebackend.Session.Meta.
func (p *Provider) StartStateBackendMetaQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendMetaQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendEventsQuery opens the span covering one full,
// sequence-ordered events replay read
// (docs/specifications/state-backend.md#events), for use by
// statebackend.Session.Events. The span stays open for the whole
// iter.Seq2 iteration, not just the initial query.
func (p *Provider) StartStateBackendEventsQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendEventsQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendProducersQuery opens the span covering one distinct
// producers read (docs/specifications/state-backend.md#producers), for use
// by statebackend.Session.Producers.
func (p *Provider) StartStateBackendProducersQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendProducersQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendCostQuery opens the span covering one cost_ledger
// SUM(cost_usd) rollup read (docs/specifications/state-backend.md#cost_ledger),
// for use by statebackend.Session.TotalCostUSD.
func (p *Provider) StartStateBackendCostQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendCostQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendCostLedgerQuery opens the span covering one full
// cost_ledger read (docs/specifications/state-backend.md#cost_ledger), for
// use by statebackend.Session.CostLedger.
func (p *Provider) StartStateBackendCostLedgerQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendCostLedgerQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartStateBackendPlanItemsQuery opens the span covering one full
// plan_items read (docs/specifications/state-backend.md#plan_items), for
// use by statebackend.Session.PlanItems.
func (p *Provider) StartStateBackendPlanItemsQuery(ctx context.Context, sessionID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameStateBackendPlanItemsQuery, trace.WithAttributes(SessionIDKey.String(sessionID)))
}

// StartEventBusPublish opens the span covering one internal/eventbus
// Publish call's whole fan-out — enqueuing the event onto every current
// subscriber of topic, not the (out-of-band, per-subscriber) delivery
// that follows. topic is unbounded, so it is attached to this span only,
// never to a metric (EventBusTopicKey's doc comment).
func (p *Provider) StartEventBusPublish(ctx context.Context, topic string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameEventBusPublish, trace.WithAttributes(EventBusTopicKey.String(topic)))
}

// StartKernelCallbackExportSpans opens the span covering one ExportSpans
// call (kernel-callbacks.md's ExportSpans) — the relay-bridge handler's
// own span, distinct from any span carried inside the relayed batch
// itself (observability.md#the-relay-model).
func (p *Provider) StartKernelCallbackExportSpans(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackExportSpans, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackRecordMetrics opens the span covering one
// RecordMetrics call (kernel-callbacks.md's RecordMetrics).
func (p *Provider) StartKernelCallbackRecordMetrics(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackRecordMetrics, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackGetTelemetryConfig opens the span covering one
// GetTelemetryConfig call (kernel-callbacks.md's GetTelemetryConfig).
func (p *Provider) StartKernelCallbackGetTelemetryConfig(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackGetTelemetryConfig, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackGetConfig opens the span covering one GetConfig call
// (kernel-callbacks.md's GetConfig). The span carries only producer
// attribution, never the config values themselves — GetConfig's own
// MUST NOT-echo rule applies to spans exactly as it does to logs.
func (p *Provider) StartKernelCallbackGetConfig(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackGetConfig, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackPublish opens the span covering one Publish call
// (kernel-callbacks.md's Publish) — the RPC handler's own span, distinct
// from StartEventBusPublish, which covers the underlying internal/eventbus
// fan-out this handler calls into.
func (p *Provider) StartKernelCallbackPublish(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackPublish, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackSubscribe opens the span covering one Subscribe
// stream's whole lifetime, from the initial call to the stream closing
// (kernel-callbacks.md's Subscribe) — a long-lived span, unlike this
// file's other RPC spans.
func (p *Provider) StartKernelCallbackSubscribe(ctx context.Context, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, spanNameKernelCallbackSubscribe, trace.WithAttributes(producerAttributes(producer)...))
}

// StartKernelCallbackReadEvents opens the span covering one ReadEvents
// call (kernel-callbacks.md's ReadEvents).
func (p *Provider) StartKernelCallbackReadEvents(ctx context.Context, sessionID string, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	attrs := append([]attribute.KeyValue{SessionIDKey.String(sessionID)}, producerAttributes(producer)...)
	return p.tracer.Start(ctx, spanNameKernelCallbackReadEvents, trace.WithAttributes(attrs...))
}

// StartKernelCallbackGetSession opens the span covering one GetSession
// call (kernel-callbacks.md's GetSession).
func (p *Provider) StartKernelCallbackGetSession(ctx context.Context, sessionID string, producer *commonv1.ProducerRef) (context.Context, trace.Span) {
	attrs := append([]attribute.KeyValue{SessionIDKey.String(sessionID)}, producerAttributes(producer)...)
	return p.tracer.Start(ctx, spanNameKernelCallbackGetSession, trace.WithAttributes(attrs...))
}

// EndSpan ends span, recording err onto it first if non-nil (RecordError
// plus a codes.Error status) so a failed hook/tool/model call is visibly
// distinguishable from a successful one in any trace viewer. Every Start*
// span in this file MUST be ended via EndSpan rather than a bare
// span.End(), so error recording stays consistent across call sites.
func EndSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}
