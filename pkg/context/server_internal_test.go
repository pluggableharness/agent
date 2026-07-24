package context

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	contextv1 "github.com/pluggableharness/agent/pkg/context/proto/v1"
	kernelv1 "github.com/pluggableharness/agent/pkg/kernel/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
)

// This file is a white-box (package context, not context_test) test:
// Service.contribute and countTokens both need a *kernel.Client, which
// (per pkg/plugin/callback_internal_test.go's documented limitation)
// cannot be produced from a real *plugin.Callback outside pkg/plugin in a
// test. helpers_internal_test.go's newTestKernelClient builds one
// directly via kernel.NewClient over bufconn instead, bypassing
// plugin.Callback entirely — the seam server.go's Contribute/contribute
// split exists for. stubProvider is a second, package-local Provider fake
// (distinct from context_test.go's fakeProvider, which lives in the
// black-box package context_test and isn't visible here).
type stubProvider struct {
	contribute func(*Request) (*Contribution, error)
}

func (s *stubProvider) GetCapabilities(context.Context) (*Capabilities, error) {
	return &Capabilities{}, nil
}

func (s *stubProvider) Configure(context.Context, *structpb.Struct) error { return nil }

func (s *stubProvider) Contribute(_ context.Context, req *Request) (*Contribution, error) {
	return s.contribute(req)
}

var _ Provider = (*stubProvider)(nil)

func TestCountTokens_realRoundTrip(t *testing.T) {
	t.Parallel()

	var gotText string
	client := newTestKernelClient(t, &fakeKernelServer{
		countTokensFunc: func(req *kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			gotText = req.GetContent()[0].GetText().GetText()
			return &kernelv1.CountTokensResult{Count: 42, Exact: true}, nil
		},
	})

	got, err := countTokens(t.Context(), client, nil, "hello world")
	if err != nil {
		t.Fatalf("countTokens() error = %v, want nil", err)
	}
	if got != 42 {
		t.Errorf("countTokens() = %d, want 42 (proves it calls through the kernel, not a local heuristic)", got)
	}
	if gotText != "hello world" {
		t.Errorf("kernel received text = %q, want %q", gotText, "hello world")
	}
}

func TestService_contribute_fullChainAndCountTokensWiring(t *testing.T) {
	t.Parallel()

	client := newTestKernelClient(t, &fakeKernelServer{
		countTokensFunc: func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			return &kernelv1.CountTokensResult{Count: 999, Exact: false}, nil
		},
	})

	var sawCountTokens bool
	provider := &stubProvider{
		contribute: func(req *Request) (*Contribution, error) {
			if req.CountTokens == nil {
				t.Fatal("req.CountTokens = nil, want a bound closure")
			}
			count, err := req.CountTokens(context.Background(), "some text")
			if err != nil {
				t.Fatalf("req.CountTokens() error = %v, want nil", err)
			}
			if count != 999 {
				t.Errorf("req.CountTokens() = %d, want 999 (from the fake kernel server)", count)
			}
			sawCountTokens = true

			// Deliberately exceeds the request's token_budget (10) to
			// exercise Service.checkContribution's budget-warning branch
			// — it MUST log, not fail the RPC.
			return &Contribution{
				Sections: append(req.PriorSections, &Section{
					Provider: "claude-md", Label: "CLAUDE.md", Content: "way too much", Tokens: 999,
				}),
			}, nil
		},
	}
	svc := &Service{provider: provider, identity: plugin.Identity{Name: "claude-md"}, callback: plugin.NewCallback()}

	req := &contextv1.ContextRequest{TokenBudget: 10}
	domainReq, err := requestFromProto(req)
	if err != nil {
		t.Fatalf("requestFromProto() error = %v, want nil", err)
	}

	resp, err := svc.contribute(t.Context(), req, domainReq, client)
	if err != nil {
		t.Fatalf("contribute() error = %v, want nil", err)
	}
	if !sawCountTokens {
		t.Fatal("provider never called req.CountTokens")
	}
	if len(resp.GetSections()) != 1 {
		t.Fatalf("contribute() sections len = %d, want 1", len(resp.GetSections()))
	}
	if got := resp.GetSections()[0].GetProvider(); got != "claude-md" {
		t.Errorf("appended section provider = %q, want %q", got, "claude-md")
	}
}

func TestService_contribute_fullChainNotDelta(t *testing.T) {
	t.Parallel()

	client := newTestKernelClient(t, &fakeKernelServer{
		countTokensFunc: func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			return &kernelv1.CountTokensResult{Count: 1}, nil
		},
	})

	provider := &stubProvider{
		contribute: func(req *Request) (*Contribution, error) {
			return &Contribution{
				Sections: append(req.PriorSections, &Section{Provider: "agents-md", Label: "AGENTS.md (src/auth)"}),
			}, nil
		},
	}
	svc := &Service{provider: provider, identity: plugin.Identity{Name: "agents-md"}, callback: plugin.NewCallback()}

	req := &contextv1.ContextRequest{
		PriorSections: []*contentv1.ContextSection{
			{Provider: "claude-md", Label: "CLAUDE.md", Content: []*contentv1.ContentBlock{
				{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "conventions"}}},
			}},
		},
	}
	domainReq, err := requestFromProto(req)
	if err != nil {
		t.Fatalf("requestFromProto() error = %v, want nil", err)
	}

	resp, err := svc.contribute(t.Context(), req, domainReq, client)
	if err != nil {
		t.Fatalf("contribute() error = %v, want nil", err)
	}
	if len(resp.GetSections()) != 2 {
		t.Fatalf("contribute() sections len = %d, want 2 (prior + own, not a delta)", len(resp.GetSections()))
	}
	if got := resp.GetSections()[0].GetProvider(); got != "claude-md" {
		t.Errorf("first section provider = %q, want %q (unchanged prior section)", got, "claude-md")
	}
}

func TestService_contribute_scopeViolationLoggedNotFailed(t *testing.T) {
	t.Parallel()

	client := newTestKernelClient(t, &fakeKernelServer{
		countTokensFunc: func(*kernelv1.CountTokensRequest) (*kernelv1.CountTokensResult, error) {
			return &kernelv1.CountTokensResult{Count: 1}, nil
		},
	})

	provider := &stubProvider{
		contribute: func(*Request) (*Contribution, error) {
			// Mutates a section it doesn't own without declaring
			// compactor — a scope violation. Service.contribute MUST NOT
			// fail the RPC for this (the kernel is the enforcement
			// authority); it only logs.
			return &Contribution{
				Sections: []*Section{{Provider: "someone-else", Label: "hijacked"}},
			}, nil
		},
	}
	svc := &Service{provider: provider, identity: plugin.Identity{Name: "claude-md"}, callback: plugin.NewCallback()}

	req := &contextv1.ContextRequest{
		PriorSections: []*contentv1.ContextSection{
			{Provider: "claude-md", Label: "CLAUDE.md", Content: []*contentv1.ContentBlock{
				{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "conventions"}}},
			}},
		},
	}
	domainReq, err := requestFromProto(req)
	if err != nil {
		t.Fatalf("requestFromProto() error = %v, want nil", err)
	}

	if _, err := svc.contribute(t.Context(), req, domainReq, client); err != nil {
		t.Fatalf("contribute() error = %v, want nil (scope violation is log-only)", err)
	}
}
