package pluginhost

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/pluggableharness/agent/internal/kernelcallback"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
)

// callbackSlot is the kernelv1.KernelCallbackServiceServer actually
// served on a plugin's callback broker: a stable value that forwards
// every RPC to whichever *kernelcallback.Server is currently installed
// in it.
//
// It exists to resolve a genuine ordering problem, not as indirection
// for its own sake. internal/pluginruntime.Launch requires a callback
// server up front, because go-plugin serves it on the broker during the
// dispense that Launch performs — but internal/kernelcallback.Config
// fixes both Producer and ResolvedConfig at construction (deliberately;
// see that package's CLAUDE.md), and neither is known that early:
// the plugin's real identity only arrives with Describe, and its
// resolved config can only be decoded once Describe's schema fetch has
// happened. Handing pluginruntime a slot instead of a finished server
// lets Supervisor install the real one — correct identity, correct
// resolved config — before Configure is ever called, which matters
// because kernel-callbacks.md permits a plugin to call GetConfig or Log
// from inside its own Configure handler.
//
// The forwarding target is an atomic.Pointer rather than a mutex-guarded
// field because the plugin subprocess can call back concurrently with
// the supervisor's own bring-up sequence: the subprocess is already
// running by the time set is called.
type callbackSlot struct {
	kernelv1.UnimplementedKernelCallbackServiceServer

	inner atomic.Pointer[kernelcallback.Server]
}

var _ kernelv1.KernelCallbackServiceServer = (*callbackSlot)(nil)

// newCallbackSlot returns a slot already serving initial, so a callback
// arriving before the first set still reaches a real server rather than
// a nil dereference.
func newCallbackSlot(initial *kernelcallback.Server) *callbackSlot {
	s := &callbackSlot{}
	s.inner.Store(initial)
	return s
}

// set installs srv as the target every subsequent RPC forwards to.
func (s *callbackSlot) set(srv *kernelcallback.Server) {
	s.inner.Store(srv)
}

// server returns the currently installed target.
func (s *callbackSlot) server() *kernelcallback.Server {
	return s.inner.Load()
}

// The forwarding methods below are deliberately exhaustive rather than
// relying on the embedded Unimplemented server for the ones this package
// has no opinion about: falling through to Unimplemented would silently
// disable a kernel callback that internal/kernelcallback does implement,
// and the compiler would never say so.

// RunSession forwards to the installed server.
func (s *callbackSlot) RunSession(ctx context.Context, req *kernelv1.RunSessionRequest) (*kernelv1.RunSessionResult, error) {
	return s.server().RunSession(ctx, req)
}

// CountTokens forwards to the installed server.
func (s *callbackSlot) CountTokens(ctx context.Context, req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
	return s.server().CountTokens(ctx, req)
}

// Emit forwards to the installed server.
func (s *callbackSlot) Emit(ctx context.Context, req *kernelv1.EmitRequest) (*kernelv1.EmitResult, error) {
	return s.server().Emit(ctx, req)
}

// Log forwards to the installed server.
func (s *callbackSlot) Log(ctx context.Context, req *kernelv1.LogRequest) (*kernelv1.LogResult, error) {
	return s.server().Log(ctx, req)
}

// ExportSpans forwards to the installed server.
func (s *callbackSlot) ExportSpans(ctx context.Context, req *kernelv1.ExportSpansRequest) (*kernelv1.ExportSpansResult, error) {
	return s.server().ExportSpans(ctx, req)
}

// RecordMetrics forwards to the installed server.
func (s *callbackSlot) RecordMetrics(ctx context.Context, req *kernelv1.RecordMetricsRequest) (*kernelv1.RecordMetricsResult, error) {
	return s.server().RecordMetrics(ctx, req)
}

// GetTelemetryConfig forwards to the installed server.
func (s *callbackSlot) GetTelemetryConfig(ctx context.Context, req *kernelv1.GetTelemetryConfigRequest) (*kernelv1.GetTelemetryConfigResult, error) {
	return s.server().GetTelemetryConfig(ctx, req)
}

// GetConfig forwards to the installed server. This is the RPC the whole
// slot exists for: a plugin calling it from inside its own Configure
// handler must see the config the kernel decoded for it, which is only
// installed moments before Configure is issued.
func (s *callbackSlot) GetConfig(ctx context.Context, req *kernelv1.GetConfigRequest) (*kernelv1.GetConfigResult, error) {
	return s.server().GetConfig(ctx, req)
}

// Publish forwards to the installed server.
func (s *callbackSlot) Publish(ctx context.Context, req *kernelv1.PublishRequest) (*kernelv1.PublishResult, error) {
	return s.server().Publish(ctx, req)
}

// Subscribe forwards to the installed server.
func (s *callbackSlot) Subscribe(req *kernelv1.SubscribeRequest, stream grpc.ServerStreamingServer[kernelv1.BusEvent]) error {
	return s.server().Subscribe(req, stream)
}

// ReadEvents forwards to the installed server.
func (s *callbackSlot) ReadEvents(req *kernelv1.ReadEventsRequest, stream grpc.ServerStreamingServer[kernelv1.StoredEvent]) error {
	return s.server().ReadEvents(req, stream)
}

// GetSession forwards to the installed server.
func (s *callbackSlot) GetSession(ctx context.Context, req *kernelv1.GetSessionRequest) (*kernelv1.GetSessionResult, error) {
	return s.server().GetSession(ctx, req)
}

// CreateSession forwards to the installed server.
func (s *callbackSlot) CreateSession(ctx context.Context, req *kernelv1.CreateSessionRequest) (*kernelv1.CreateSessionResult, error) {
	return s.server().CreateSession(ctx, req)
}

// AttachSession forwards to the installed server.
func (s *callbackSlot) AttachSession(ctx context.Context, req *kernelv1.AttachSessionRequest) (*kernelv1.AttachSessionResult, error) {
	return s.server().AttachSession(ctx, req)
}

// ResumeSession forwards to the installed server.
func (s *callbackSlot) ResumeSession(ctx context.Context, req *kernelv1.ResumeSessionRequest) (*kernelv1.ResumeSessionResult, error) {
	return s.server().ResumeSession(ctx, req)
}

// DetachSession forwards to the installed server.
func (s *callbackSlot) DetachSession(ctx context.Context, req *kernelv1.DetachSessionRequest) (*kernelv1.DetachSessionResult, error) {
	return s.server().DetachSession(ctx, req)
}

// ListSessions forwards to the installed server.
func (s *callbackSlot) ListSessions(ctx context.Context, req *kernelv1.ListSessionsRequest) (*kernelv1.ListSessionsResult, error) {
	return s.server().ListSessions(ctx, req)
}

// GetSessionState forwards to the installed server.
func (s *callbackSlot) GetSessionState(ctx context.Context, req *kernelv1.GetSessionStateRequest) (*kernelv1.GetSessionStateResult, error) {
	return s.server().GetSessionState(ctx, req)
}

// SubmitInput forwards to the installed server.
func (s *callbackSlot) SubmitInput(ctx context.Context, req *kernelv1.SubmitInputRequest) (*kernelv1.SubmitInputResult, error) {
	return s.server().SubmitInput(ctx, req)
}

// Interrupt forwards to the installed server.
func (s *callbackSlot) Interrupt(ctx context.Context, req *kernelv1.InterruptRequest) (*kernelv1.InterruptResult, error) {
	return s.server().Interrupt(ctx, req)
}

// PublishMetadata forwards to the installed server.
func (s *callbackSlot) PublishMetadata(ctx context.Context, req *kernelv1.PublishMetadataRequest) (*kernelv1.PublishMetadataResult, error) {
	return s.server().PublishMetadata(ctx, req)
}

// RetractMetadata forwards to the installed server.
func (s *callbackSlot) RetractMetadata(ctx context.Context, req *kernelv1.RetractMetadataRequest) (*kernelv1.RetractMetadataResult, error) {
	return s.server().RetractMetadata(ctx, req)
}

// ListMetadata forwards to the installed server.
func (s *callbackSlot) ListMetadata(ctx context.Context, req *kernelv1.ListMetadataRequest) (*kernelv1.ListMetadataResult, error) {
	return s.server().ListMetadata(ctx, req)
}

// ResolvePlanDecision forwards to the installed server.
func (s *callbackSlot) ResolvePlanDecision(ctx context.Context, req *kernelv1.ResolvePlanDecisionRequest) (*kernelv1.ResolvePlanDecisionResult, error) {
	return s.server().ResolvePlanDecision(ctx, req)
}

// ResolveInteractive forwards to the installed server.
func (s *callbackSlot) ResolveInteractive(ctx context.Context, req *kernelv1.ResolveInteractiveRequest) (*kernelv1.ResolveInteractiveResult, error) {
	return s.server().ResolveInteractive(ctx, req)
}

// InvokeSlashCommand forwards to the installed server.
func (s *callbackSlot) InvokeSlashCommand(ctx context.Context, req *kernelv1.InvokeSlashCommandRequest) (*kernelv1.InvokeSlashCommandResult, error) {
	return s.server().InvokeSlashCommand(ctx, req)
}

// TriggerAction forwards to the installed server.
func (s *callbackSlot) TriggerAction(ctx context.Context, req *kernelv1.TriggerActionRequest) (*kernelv1.TriggerActionResult, error) {
	return s.server().TriggerAction(ctx, req)
}

// StreamDeltas forwards to the installed server.
func (s *callbackSlot) StreamDeltas(req *kernelv1.StreamDeltasRequest, stream kernelv1.KernelCallbackService_StreamDeltasServer) error {
	return s.server().StreamDeltas(req, stream)
}
