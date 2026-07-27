package modeltest

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// collected is what one drained StreamCompletion produced.
type collected struct {
	events    []*modelv1.StreamEvent
	stops     int
	errs      int
	terminals int
	// lastIsTerminal reports whether the final event was the terminal one,
	// which is how "exactly one terminal event" is checked as a position
	// rather than only a count.
	lastIsTerminal bool
	usage          *modelv1.Usage
	err            error
}

// drain reads a stream to completion, recording what it saw.
func drain(stream modelv1.ModelService_StreamCompletionClient) collected {
	var got collected
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			got.err = err
			return got
		}
		got.events = append(got.events, ev)

		terminal := false
		switch e := ev.GetEvent().(type) {
		case *modelv1.StreamEvent_Stop_:
			got.stops++
			terminal = true
		case *modelv1.StreamEvent_Error_:
			got.errs++
			terminal = true
		case *modelv1.StreamEvent_Usage:
			got.usage = e.Usage
		}
		if terminal {
			got.terminals++
		}
		got.lastIsTerminal = terminal
	}
}

// checkStream drives one completion and asserts the stream's shape.
func checkStream(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config, spec *modelv1.ModelSpec) {
	ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
	defer cancel()

	stream, err := client.StreamCompletion(ctx, cfg.streamRequestFor(spec.GetId()))
	if err != nil {
		rec.failf("implemented", "StreamCompletion is a MUST and returned %v", err)
		return
	}
	got := drain(stream)

	if got.err != nil && statusCode(got.err) != codes.OK {
		rec.failf("transport", "the stream ended with a transport error: %v", got.err)
		return
	}

	// A stream that ends without a terminal event looks to the kernel like
	// a clean turn that produced nothing, which is indistinguishable from
	// success and therefore worse than a failure.
	if got.terminals == 0 {
		rec.failf("terminal-event", "the stream produced no terminal event; exactly one stop or error is required")
		return
	}
	if got.terminals > 1 {
		rec.failf("terminal-event", "the stream produced %d terminal events, want exactly 1", got.terminals)
	}
	if !got.lastIsTerminal {
		rec.failf("terminal-event", "the terminal event was not the last event on the stream")
	}

	if got.errs > 0 {
		checkErrorEvents(rec, got.events)
		// An error terminal is a legitimate outcome — the provider may be
		// pointed at a fixture that fails — so the remaining assertions,
		// which describe a successful completion, do not apply.
		return
	}

	if got.usage == nil {
		rec.failf("usage", "the stream produced no usage event, so the kernel has no token counts to compute cost from")
	}
	checkStopReason(rec, got.events)
	checkOpportunistic(rec, got.events, spec)
}

// checkStopReason asserts the terminal stop names a real reason.
func checkStopReason(rec *recorder, events []*modelv1.StreamEvent) {
	for _, ev := range events {
		stop, ok := ev.GetEvent().(*modelv1.StreamEvent_Stop_)
		if !ok {
			continue
		}
		reason := stop.Stop.GetReason()
		if reason == modelv1.StopReason_STOP_REASON_UNSPECIFIED {
			rec.failf("stop-reason", "the stop event carries STOP_REASON_UNSPECIFIED, which tells the kernel nothing about why the turn ended")
		}
		// matched_stop_sequence is set iff the reason is STOP_SEQUENCE.
		if reason == modelv1.StopReason_STOP_REASON_STOP_SEQUENCE && stop.Stop.GetMatchedStopSequence() == "" {
			rec.failf("stop-reason", "the stop reason is STOP_SEQUENCE but matched_stop_sequence is empty")
		}
		if reason != modelv1.StopReason_STOP_REASON_STOP_SEQUENCE && stop.Stop.MatchedStopSequence != nil {
			rec.failf("stop-reason", "matched_stop_sequence is set alongside reason %v, where it is meaningless", reason)
		}
	}
}

// checkErrorEvents asserts every in-band error is classified.
func checkErrorEvents(rec *recorder, events []*modelv1.StreamEvent) {
	for _, ev := range events {
		e, ok := ev.GetEvent().(*modelv1.StreamEvent_Error_)
		if !ok {
			continue
		}
		modelErr := e.Error.GetError()
		if modelErr.GetCategory() == modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNSPECIFIED {
			rec.failf("error-category", "an error event carries an unspecified category; the kernel's retry and fallback behavior depends on telling categories apart")
		}
		if modelErr.GetMessage() == "" {
			rec.failf("error-message", "an error event carries no message")
		}
		if modelErr.GetCategory() == modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_UNKNOWN && modelErr.GetRawDetail() == "" {
			rec.failf("error-raw-detail", "an UNKNOWN error omits raw_detail, leaving nothing to debug from")
		}
	}
}

// checkOpportunistic asserts the handling of content the suite cannot
// force a vendor to produce. Each check reports itself as skipped when the
// run did not reach it, so an unexercised check never reads as a pass.
func checkOpportunistic(rec *recorder, events []*modelv1.StreamEvent, spec *modelv1.ModelSpec) {
	func() {
		rec := rec.sub("RedactedThinking")
		var seen int
		for _, ev := range events {
			r, ok := ev.GetEvent().(*modelv1.StreamEvent_RedactedThinking_)
			if !ok {
				continue
			}
			seen++
			// The block is opaque and carries a vendor integrity check. An
			// empty payload means the adapter dropped or mangled it, which
			// typically makes the vendor reject the WHOLE conversation on
			// the next turn — a delayed, silent failure.
			if len(r.RedactedThinking.GetData()) == 0 {
				rec.failf("verbatim", "a redacted_thinking event carries no data; the block must be forwarded verbatim")
			}
		}
		if seen == 0 {
			rec.skipf("verbatim", "no redacted_thinking block was produced; supply a request that triggers one via WithStreamRequest to exercise this")
			return
		}
		if !spec.GetThinking().GetSupported() {
			rec.failf("capability", "a redacted_thinking block was produced by a model declaring no thinking capability")
		}
	}()

	func() {
		rec := rec.sub("ToolCallPairing")
		starts := map[string]string{}
		var dones int
		for _, ev := range events {
			switch e := ev.GetEvent().(type) {
			case *modelv1.StreamEvent_ToolCallStart_:
				if e.ToolCallStart.GetId() == "" {
					rec.failf("ids", "a tool_call_start carries no id, so its deltas cannot be correlated")
				}
				if e.ToolCallStart.GetName() == "" {
					rec.failf("ids", "a tool_call_start carries no tool name")
				}
				starts[e.ToolCallStart.GetId()] = e.ToolCallStart.GetName()
			case *modelv1.StreamEvent_ToolCallDelta_:
				if _, ok := starts[e.ToolCallDelta.GetId()]; !ok {
					rec.failf("pairing", "tool_call_delta for id %q has no preceding tool_call_start", e.ToolCallDelta.GetId())
				}
			case *modelv1.StreamEvent_ToolCallDone_:
				dones++
				if _, ok := starts[e.ToolCallDone.GetId()]; !ok {
					rec.failf("pairing", "tool_call_done for id %q has no preceding tool_call_start", e.ToolCallDone.GetId())
				}
			}
		}
		if len(starts) == 0 {
			rec.skipf("pairing", "no tool call was produced; declare a tool via WithStreamRequest to exercise this")
			return
		}
		if dones != len(starts) {
			rec.failf("pairing", "%d tool calls started but %d completed; every started call must be closed", len(starts), dones)
		}
	}()

	func() {
		rec := rec.sub("RateLimits")
		var usage *modelv1.Usage
		for _, ev := range events {
			if u, ok := ev.GetEvent().(*modelv1.StreamEvent_Usage); ok {
				usage = u.Usage
			}
		}
		if usage == nil || len(usage.GetRateLimits()) == 0 {
			rec.skipf("kind", "the vendor published no rate-limit state")
			return
		}
		for _, rl := range usage.GetRateLimits() {
			if rl.GetKind() == modelv1.RateLimitKind_RATE_LIMIT_KIND_UNSPECIFIED {
				rec.failf("kind", "a rate-limit snapshot names no budget kind; \"you have 2%% left\" is unactionable without saying 2%% of what")
			}
		}
	}()
}

// checkCancellation asserts a canceled stream is not reported as a
// failure.
//
// This check is weaker than it appears, and saying so is more useful than
// implying otherwise. Two things limit it, both properties of gRPC rather
// than of any provider:
//
//   - Once a client cancels, its own Recv returns immediately with
//     codes.Canceled. It does not wait for the server, so a provider that
//     ignores its context and keeps generating — still billing the
//     operator for a turn the kernel has abandoned — is invisible from
//     this side. Detecting that needs the server's own view, which a
//     black-box conformance client does not have; it stays a code-review
//     item, and RunBinary cannot check it at all.
//   - For the same reason, an application error the provider returns
//     *after* cancellation is masked by codes.Canceled before it reaches
//     here.
//
// What remains is still worth asserting: the stream must start, and
// whatever terminal code does surface must be a cancellation rather than
// a failure. A provider that mishandles cancellation upstream of its own
// return — classifying it as an error before the stream unwinds — is
// caught here, and that is the common shape of the mistake.
func checkCancellation(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config, modelID string) {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	stream, err := client.StreamCompletion(streamCtx, cfg.streamRequestFor(modelID))
	if err != nil {
		rec.failf("stream", "StreamCompletion returned %v", err)
		return
	}
	cancelStream()

	for {
		_, recvErr := stream.Recv()
		if recvErr == nil {
			continue
		}
		if errors.Is(recvErr, io.EOF) {
			return
		}
		switch statusCode(recvErr) {
		case codes.Canceled, codes.DeadlineExceeded:
			return
		default:
			rec.failf("not-an-error",
				"a canceled stream reported %v (%v); cancellation is normal control flow, never an application error",
				statusCode(recvErr), recvErr)
			return
		}
	}
}

// checkUnknownModel asserts an unroutable model id is rejected as a
// malformed request rather than, say, silently served by a default.
func checkUnknownModel(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config) {
	ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
	defer cancel()

	const bogus = "modeltest-no-such-model"
	stream, err := client.StreamCompletion(ctx, &modelv1.StreamCompletionRequest{ModelId: bogus})
	if err != nil {
		assertInvalidArgument(rec, err, "an unknown model id")
		return
	}
	got := drain(stream)
	if got.err != nil {
		assertInvalidArgument(rec, got.err, "an unknown model id")
		return
	}
	for _, ev := range got.events {
		if e, ok := ev.GetEvent().(*modelv1.StreamEvent_Error_); ok {
			if e.Error.GetError().GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
				rec.failf("rejected", "an unknown model id produced category %v, want INVALID_REQUEST", e.Error.GetError().GetCategory())
			}
			return
		}
	}
	rec.failf("rejected", "an unknown model id was served without an error; the kernel would attribute the result to a model that does not exist")
}

// checkCapabilityGates asserts content a model declares it cannot accept
// is rejected rather than silently dropped.
func checkCapabilityGates(ctx context.Context, rec *recorder, client modelv1.ModelServiceClient, cfg *config, spec *modelv1.ModelSpec) {
	gates := []struct {
		name      string
		supported bool
		block     *contentv1.ContentBlock
	}{
		{
			name:      "image",
			supported: spec.GetSupportsVision(),
			block: &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Image{
				Image: &contentv1.ImageBlock{MediaType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
			}},
		},
		{
			name:      "document",
			supported: spec.GetSupportsDocuments(),
			block: &contentv1.ContentBlock{Block: &contentv1.ContentBlock_Document{
				Document: &contentv1.DocumentBlock{MediaType: "application/pdf", Data: []byte("%PDF-")},
			}},
		},
	}

	for _, g := range gates {
		func() {
			rec := rec.sub(g.name)
			if g.supported {
				rec.skipf("rejected", "this model declares %s support, so there is no rejection to check", g.name)
				return
			}

			ctx, cancel := context.WithTimeout(ctx, cfg.callTimeout)
			defer cancel()

			req := &modelv1.StreamCompletionRequest{
				ModelId: spec.GetId(),
				Messages: []*contentv1.Message{{
					Role:    contentv1.Role_ROLE_USER,
					Content: []*contentv1.ContentBlock{g.block},
				}},
			}
			stream, err := client.StreamCompletion(ctx, req)
			if err != nil {
				assertInvalidArgument(rec, err, g.name+" sent to a model that does not support it")
				return
			}
			got := drain(stream)
			if got.err != nil {
				assertInvalidArgument(rec, got.err, g.name+" sent to a model that does not support it")
				return
			}
			for _, ev := range got.events {
				if e, ok := ev.GetEvent().(*modelv1.StreamEvent_Error_); ok {
					if e.Error.GetError().GetCategory() != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
						rec.failf("rejected", "the %s rejection used category %v, want INVALID_REQUEST", g.name, e.Error.GetError().GetCategory())
					}
					return
				}
			}
			rec.failf("rejected",
				"a %s block was accepted by a model declaring no %s support; it MUST be rejected, not silently dropped",
				g.name, g.name)
		}()
	}
}

// assertInvalidArgument checks err carries the code the taxonomy maps
// invalid_request to.
func assertInvalidArgument(rec *recorder, err error, what string) {
	if got := statusCode(err); got != codes.InvalidArgument {
		rec.failf("rejected", "%s produced %v (%v), want codes.InvalidArgument", what, got, err)
	}
}
