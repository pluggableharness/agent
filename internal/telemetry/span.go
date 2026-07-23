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
