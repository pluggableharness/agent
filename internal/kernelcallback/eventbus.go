package kernelcallback

import (
	"context"
	"fmt"
	"strings"

	"github.com/pluggableharness/agent/internal/eventbus"
	"github.com/pluggableharness/agent/internal/telemetry"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// validateEventType reports whether eventType is a valid PublishRequest.event_type:
// a single, non-empty, dot-free, wildcard-free segment
// (kernel-callbacks.md's Publish, event-bus.md's topic grammar).
func validateEventType(eventType string) error {
	if eventType == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.ContainsAny(eventType, ".*") {
		return fmt.Errorf("event_type %q must not contain \".\" or \"*\"", eventType)
	}
	return nil
}

// validateTopicFilter reports whether filter is a valid SubscribeRequest
// topic_filters entry per event-bus.md's filter grammar: an exact topic
// (no "*" anywhere), or a trailing wildcard where "*" is the filter's last
// character and either the whole filter ("*", matching everything) or
// immediately preceded by "." (a whole-segment wildcard, e.g. "kernel.*").
// This is deliberately stricter than internal/eventbus's own
// isWildcardFilter (any trailing "*"), which is a generic pub/sub
// primitive that doesn't police this wire-level grammar itself — see that
// package's filter.go doc comment.
func validateTopicFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("topic filter must not be empty")
	}
	idx := strings.IndexByte(filter, '*')
	if idx == -1 {
		return nil // an exact filter, no wildcard at all.
	}
	if idx != len(filter)-1 {
		return fmt.Errorf("topic filter %q: \"*\" is only valid as the filter's last character", filter)
	}
	if idx > 0 && filter[idx-1] != '.' {
		return fmt.Errorf("topic filter %q: a wildcard must be the bare filter \"*\" or immediately preceded by \".\"", filter)
	}
	return nil
}

// Publish implements the Publish RPC (kernel-callbacks.md's Publish): puts
// one event onto the event bus under a topic the kernel constructs from
// s.producer's server-derived identity — a plugin never supplies its own
// topic (event-bus.md#topic-grammar).
func (s *Server) Publish(ctx context.Context, req *kernelv1.PublishRequest) (*kernelv1.PublishResult, error) {
	ctx, span := s.telemetry.StartKernelCallbackPublish(ctx, s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	s.logger.DebugContext(ctx, "kernelcallback: publish", "event_type", req.GetEventType())

	if validateErr := validateEventType(req.GetEventType()); validateErr != nil {
		err = status.Error(codes.InvalidArgument, "kernelcallback: publish: "+validateErr.Error())
		s.logger.WarnContext(ctx, "kernelcallback: publish: rejected", "err", err)
		return nil, err
	}
	if req.GetPayloadType() == "" {
		err = status.Error(codes.InvalidArgument, "kernelcallback: publish: payload_type is required")
		s.logger.WarnContext(ctx, "kernelcallback: publish: rejected", "err", err)
		return nil, err
	}
	if req.GetSchemaVersion() == "" {
		err = status.Error(codes.InvalidArgument, "kernelcallback: publish: schema_version is required")
		s.logger.WarnContext(ctx, "kernelcallback: publish: rejected", "err", err)
		return nil, err
	}

	topic := producerScopedName(s.producer, req.GetEventType())
	busEvent := &kernelv1.BusEvent{
		Topic:         topic,
		Payload:       req.GetPayload(),
		PayloadType:   req.GetPayloadType(),
		SchemaVersion: req.GetSchemaVersion(),
		Time:          timestamppb.Now(),
	}

	if pubErr := s.bus.Publish(ctx, eventbus.Event{Topic: topic, Payload: busEvent}); pubErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: publish: %v", pubErr)
		s.logger.ErrorContext(ctx, "kernelcallback: publish: failed", "err", pubErr)
		return nil, err
	}

	return &kernelv1.PublishResult{Topic: topic}, nil
}

// Subscribe implements the Subscribe RPC (kernel-callbacks.md's
// Subscribe): a server-streaming subscription to the event bus, filtered
// by topic_filters (event-bus.md#filter-grammar). See
// event-bus.md#backpressure for why this handler unilaterally closes the
// stream — with codes.ResourceExhausted, never silently — once its
// undelivered-event queue exceeds s.busSubscribeQueueBound; the
// underlying internal/eventbus.Bus itself stays unbounded/never-drop, per
// that package's own contract — the bound applies only to this bridge.
func (s *Server) Subscribe(req *kernelv1.SubscribeRequest, stream kernelv1.KernelCallbackService_SubscribeServer) error {
	ctx := stream.Context()
	ctx, span := s.telemetry.StartKernelCallbackSubscribe(ctx, s.producer)
	var err error
	defer func() { telemetry.EndSpan(span, err) }()

	filters := req.GetTopicFilters()
	s.logger.DebugContext(ctx, "kernelcallback: subscribe", "filters", filters)

	if len(filters) == 0 {
		err = status.Error(codes.InvalidArgument, "kernelcallback: subscribe: topic_filters is required and must be non-empty")
		s.logger.WarnContext(ctx, "kernelcallback: subscribe: rejected", "err", err)
		return err
	}
	for _, f := range filters {
		if validateErr := validateTopicFilter(f); validateErr != nil {
			err = status.Error(codes.InvalidArgument, "kernelcallback: subscribe: "+validateErr.Error())
			s.logger.WarnContext(ctx, "kernelcallback: subscribe: rejected", "err", err)
			return err
		}
	}

	// events is the bridge's own bounded buffer, layered on top of
	// internal/eventbus's unbounded per-subscriber queue — see the doc
	// comment above. overflow is signalled at most once (buffered 1,
	// non-blocking send) the first time events fills up.
	events := make(chan *kernelv1.BusEvent, s.busSubscribeQueueBound)
	overflow := make(chan struct{}, 1)

	handler := func(_ context.Context, ev eventbus.Event) {
		busEvent, ok := ev.Payload.(*kernelv1.BusEvent)
		if !ok {
			// Every Publish path in this package sets exactly this
			// Payload shape; a mismatch here would mean some other
			// in-process caller is publishing directly onto a
			// plugin-facing topic, which nothing in this codebase does.
			s.logger.ErrorContext(ctx, "kernelcallback: subscribe: received a non-BusEvent payload, dropping", "topic", ev.Topic)
			return
		}
		select {
		case events <- busEvent:
		default:
			select {
			case overflow <- struct{}{}:
			default:
			}
		}
	}

	sub, subErr := s.bus.SubscribeFilters(ctx, filters, handler)
	if subErr != nil {
		err = status.Errorf(codes.Internal, "kernelcallback: subscribe: %v", subErr)
		s.logger.ErrorContext(ctx, "kernelcallback: subscribe: failed", "err", subErr)
		return err
	}
	defer func() { _ = sub.Close() }()

	for {
		select {
		case <-ctx.Done():
			// Ordinary stream close/cancel — not an error
			// (.claude/rules/grpc.md's cancellation rule).
			return nil
		case <-overflow:
			s.telemetry.Instruments().EventBusSubscribeStreamsClosed.Add(ctx, 1)
			err = status.Error(codes.ResourceExhausted, "kernelcallback: subscribe: stream exceeded its backpressure bound")
			s.logger.WarnContext(ctx, "kernelcallback: subscribe: closing slow-consumer stream", "bound", s.busSubscribeQueueBound)
			return err
		case busEvent := <-events:
			if sendErr := stream.Send(busEvent); sendErr != nil {
				err = sendErr
				return err
			}
		}
	}
}
