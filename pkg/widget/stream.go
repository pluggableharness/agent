package widget

import (
	"context"
	"fmt"

	widgetv1 "github.com/pluggableharness/agent/pkg/widget/proto/v1"
)

// UpdateSender is the cancellation-safe handle a Provider's Attach method
// uses to push Update values for one session's Attach call. The zero
// value is not usable; Service.Attach constructs one per call and hands
// it to the Provider — a Provider never constructs one itself.
type UpdateSender struct {
	ctx    context.Context
	stream widgetv1.WidgetService_AttachServer
}

// newUpdateSender builds an UpdateSender bound to one Attach call's
// stream and context.
func newUpdateSender(ctx context.Context, stream widgetv1.WidgetService_AttachServer) *UpdateSender {
	return &UpdateSender{ctx: ctx, stream: stream}
}

// Send converts update to its wire representation and writes it to the
// underlying stream. If the session's context has already been
// canceled — the kernel closing this Attach call, ordinary control flow
// per docs/specifications/frontend/widget-protocol.md#transport, never an
// application error — Send returns ctx.Err() directly without attempting
// the write, so a Provider's Attach loop can check errors.Is(err,
// context.Canceled) uniformly regardless of whether cancellation landed
// before or during the write.
func (s *UpdateSender) Send(update Update) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.stream.Send(toProtoUpdate(update)); err != nil {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("widget: send update: %w", err)
	}
	return nil
}
