package widget_test

import (
	"context"

	structpb "google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/widget"
)

// fakeProvider is a hand-written widget.Provider fake (go-testing.md:
// fakes, not mocking frameworks). Each method's behavior is controlled by
// a caller-set func field; a nil field returns a zero value and a nil
// error, which is enough for tests that only exercise one method.
type fakeProvider struct {
	getCapabilitiesFunc func(ctx context.Context) (widget.Capabilities, error)
	configureFunc       func(ctx context.Context, config *structpb.Struct) error
	attachFunc          func(ctx context.Context, req widget.AttachRequest, sender *widget.UpdateSender) error
}

func (f *fakeProvider) GetCapabilities(ctx context.Context) (widget.Capabilities, error) {
	if f.getCapabilitiesFunc != nil {
		return f.getCapabilitiesFunc(ctx)
	}
	return widget.Capabilities{}, nil
}

func (f *fakeProvider) Configure(ctx context.Context, config *structpb.Struct) error {
	if f.configureFunc != nil {
		return f.configureFunc(ctx, config)
	}
	return nil
}

func (f *fakeProvider) Attach(ctx context.Context, req widget.AttachRequest, sender *widget.UpdateSender) error {
	if f.attachFunc != nil {
		return f.attachFunc(ctx, req, sender)
	}
	return nil
}

var _ widget.Provider = (*fakeProvider)(nil)
