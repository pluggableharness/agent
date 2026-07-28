package widget_test

import (
	"context"

	structpb "google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/widget"
)

// fakeProvider is a hand-written widget.Provider fake.
type fakeProvider struct {
	getCapabilitiesFunc func(ctx context.Context) (widget.Capabilities, error)
	configureFunc       func(ctx context.Context, config *structpb.Struct) error
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

var _ widget.Provider = (*fakeProvider)(nil)
