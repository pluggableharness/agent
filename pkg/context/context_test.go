package context_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	pluggablecontext "github.com/pluggableharness/agent/pkg/context"
	"github.com/pluggableharness/agent/pkg/plugin"
)

func TestStability_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    pluggablecontext.Stability
		want string
	}{
		{"static", pluggablecontext.StabilityStatic, "static"},
		{"dynamic", pluggablecontext.StabilityDynamic, "dynamic"},
		{"unspecified", pluggablecontext.StabilityUnspecified, "unspecified"},
		{"out of range", pluggablecontext.Stability(99), "unspecified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.s.String(); got != tt.want {
				t.Errorf("Stability(%d).String() = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// fakeProvider is a hand-written pluggablecontext.Provider fake
// (go-testing.md: fakes, not mocking frameworks).
type fakeProvider struct {
	getCapabilitiesFunc func() (*pluggablecontext.Capabilities, error)
	configureFunc       func(*structpb.Struct) error
	contributeFunc      func(*pluggablecontext.Request) (*pluggablecontext.Contribution, error)
}

func (f *fakeProvider) GetCapabilities(context.Context) (*pluggablecontext.Capabilities, error) {
	if f.getCapabilitiesFunc != nil {
		return f.getCapabilitiesFunc()
	}
	return &pluggablecontext.Capabilities{}, nil
}

func (f *fakeProvider) Configure(_ context.Context, cfg *structpb.Struct) error {
	if f.configureFunc != nil {
		return f.configureFunc(cfg)
	}
	return nil
}

func (f *fakeProvider) Contribute(_ context.Context, req *pluggablecontext.Request) (*pluggablecontext.Contribution, error) {
	if f.contributeFunc != nil {
		return f.contributeFunc(req)
	}
	return &pluggablecontext.Contribution{}, nil
}

var _ pluggablecontext.Provider = (*fakeProvider)(nil)

func TestCheckOwnSectionOnly(t *testing.T) {
	t.Parallel()

	own := func(label string) *pluggablecontext.Section {
		return &pluggablecontext.Section{Provider: "agents-md", Label: label, Content: "c"}
	}
	foreign := &pluggablecontext.Section{Provider: "claude-md", Label: "CLAUDE.md", Content: "conventions"}

	tests := []struct {
		name      string
		prior     []*pluggablecontext.Section
		chain     []*pluggablecontext.Section
		compactor bool
		wantErr   bool
	}{
		{
			name:  "valid append",
			prior: []*pluggablecontext.Section{foreign},
			chain: []*pluggablecontext.Section{foreign, own("root")},
		},
		{
			name:  "valid edit of own section",
			prior: []*pluggablecontext.Section{foreign, own("root")},
			chain: []*pluggablecontext.Section{foreign, own("root-edited")},
		},
		{
			name:    "foreign section mutated",
			prior:   []*pluggablecontext.Section{foreign},
			chain:   []*pluggablecontext.Section{{Provider: "claude-md", Label: "changed", Content: "x"}},
			wantErr: true,
		},
		{
			name:    "chain drops a prior section",
			prior:   []*pluggablecontext.Section{foreign, own("root")},
			chain:   []*pluggablecontext.Section{own("root")},
			wantErr: true,
		},
		{
			name:    "appended section has wrong provider",
			prior:   []*pluggablecontext.Section{foreign},
			chain:   []*pluggablecontext.Section{foreign, {Provider: "someone-else", Label: "x"}},
			wantErr: true,
		},
		{
			name:      "compactor may rewrite anything",
			prior:     []*pluggablecontext.Section{foreign, own("root")},
			chain:     []*pluggablecontext.Section{{Provider: "claude-md", Label: "rewritten"}},
			compactor: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := pluggablecontext.CheckOwnSectionOnly(tt.prior, tt.chain, "agents-md", tt.compactor)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckOwnSectionOnly() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				var ctxErr *pluggablecontext.Error
				if !errors.As(err, &ctxErr) {
					t.Fatalf("CheckOwnSectionOnly() error type = %T, want *Error", err)
				}
				if ctxErr.Category != pluggablecontext.ErrorCategoryScopeViolation {
					t.Errorf("CheckOwnSectionOnly() error category = %v, want ErrorCategoryScopeViolation", ctxErr.Category)
				}
			}
		})
	}
}

func TestCountTokens_dialFailure(t *testing.T) {
	t.Parallel()

	cb := plugin.NewCallback()
	_, err := pluggablecontext.CountTokens(t.Context(), cb, nil, "hello")
	if err == nil {
		t.Fatal("CountTokens() error = nil, want non-nil (callback broker unset)")
	}
}
