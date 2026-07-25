package anthropic

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// TestCapabilities_servesTheRosterAndSchema checks the one RPC the kernel
// calls before every routing decision. It must not touch the network and
// must not need Configure to have run — a provider that could only
// describe itself after being configured would deadlock the kernel, which
// needs the config schema in order to configure it.
func TestCapabilities_servesTheRosterAndSchema(t *testing.T) {
	t.Parallel()

	p := New()
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities on an unconfigured provider: %v", err)
	}
	if len(caps.Models) == 0 {
		t.Fatal("no models advertised")
	}
	if caps.ConfigSchema == nil {
		t.Fatal("no config schema advertised — the kernel could never configure this provider")
	}

	var sawOpus5 bool
	for _, m := range caps.Models {
		if m.ID == "claude-opus-5" {
			sawOpus5 = true
		}
	}
	if !sawOpus5 {
		t.Error("the roster is missing claude-opus-5")
	}
}

// TestProvider_implementsTokenCounter pins the SHOULD from
// docs/specifications/model/protocol.md#counttokens. Anthropic exposes a
// real tokenizer endpoint, so declining to implement it would mean the
// kernel silently falling back to ceil(bytes/4) for every context-budget
// decision against this provider.
func TestProvider_implementsTokenCounter(t *testing.T) {
	t.Parallel()

	if _, ok := any(New()).(model.TokenCounter); !ok {
		t.Fatal("Provider must implement model.TokenCounter")
	}
}

// TestProvider_doesNotImplementRenderer records a deliberate choice
// rather than an omission: Render is a MAY, and the kernel's generic
// fallback renders this provider's payloads (text, tool calls, usage)
// correctly. If someone implements it later they should delete this test
// consciously, not discover it failing.
func TestProvider_doesNotImplementRenderer(t *testing.T) {
	t.Parallel()

	if _, ok := any(New()).(model.Renderer); ok {
		t.Fatal("Render is now implemented — delete this test and say why in the commit")
	}
}

// TestRPCs_beforeConfigureAreRejected covers the ordering the kernel is
// supposed to guarantee. Reaching an RPC unconfigured is a kernel bug, so
// it must be a clean invalid_request rather than a nil dereference.
func TestRPCs_beforeConfigureAreRejected(t *testing.T) {
	t.Parallel()

	p := New()

	// A nil sink is safe here precisely because the guard returns before
	// anything touches it — which is the property being asserted.
	streamErr := p.StreamCompletion(context.Background(),
		&modelv1.StreamCompletionRequest{ModelId: "claude-opus-5"}, nil)
	assertInvalidRequest(t, streamErr, "not configured")

	_, countErr := p.CountTokens(context.Background(), "hello", "claude-opus-5")
	assertInvalidRequest(t, countErr, "not configured")
}

// TestConfigure_rejectsABadConfig proves Configure fails immediately
// rather than deferring to the first completion, per
// docs/specifications/model/protocol.md#configure.
func TestConfigure_rejectsABadConfig(t *testing.T) {
	t.Parallel()

	p := New()
	empty, err := structpb.NewStruct(map[string]any{})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	assertInvalidRequest(t, p.Configure(context.Background(), empty), "api_key is required")

	// A failed Configure must leave the provider unconfigured rather than
	// half-configured: a later completion should report the ordering
	// problem, not fire a request with an empty credential.
	assertInvalidRequest(t,
		p.StreamCompletion(context.Background(), &modelv1.StreamCompletionRequest{ModelId: "claude-opus-5"}, nil),
		"not configured")
}

// TestStreamCompletion_rejectsAnUnknownModel covers the case where the
// kernel's view of the roster and the catalog's have diverged.
func TestStreamCompletion_rejectsAnUnknownModel(t *testing.T) {
	t.Parallel()

	p := configuredProvider(t)

	err := p.StreamCompletion(context.Background(),
		&modelv1.StreamCompletionRequest{ModelId: "claude-does-not-exist"}, nil)
	assertInvalidRequest(t, err, "unknown model")

	_, countErr := p.CountTokens(context.Background(), "hello", "claude-does-not-exist")
	assertInvalidRequest(t, countErr, "unknown model")
}

// TestConfigure_neverLeaksTheKey guards
// docs/specifications/model/protocol.md#configure's secret rule at the
// provider level, complementing config_test.go's coverage of the decoder:
// no error surfaced by any RPC may contain the credential.
func TestConfigure_neverLeaksTheKey(t *testing.T) {
	t.Parallel()

	const secret = "sk-ant-provider-level-secret"
	p := New(WithTransport(failingTransport{}))

	cfg, err := structpb.NewStruct(map[string]any{"api_key": secret})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if err := p.Configure(context.Background(), cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	// Force a transport-level failure and confirm the key is absent from
	// whatever comes back.
	_, countErr := p.CountTokens(context.Background(), "hello", "claude-opus-5")
	if countErr == nil {
		t.Fatal("expected the failing transport to produce an error")
	}
	if strings.Contains(countErr.Error(), secret) {
		t.Fatalf("the api key leaked into an RPC error: %q", countErr.Error())
	}
}

// configuredProvider returns a Provider configured against a transport
// that always fails, which is enough for every test here — none of them
// exercises a successful vendor round trip, which is the integration
// tier's job.
func configuredProvider(t *testing.T) *Provider {
	t.Helper()

	p := New(WithTransport(failingTransport{}))
	cfg, err := structpb.NewStruct(map[string]any{"api_key": "sk-ant-unit-test"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if err := p.Configure(context.Background(), cfg); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	return p
}

// failingTransport fails every request, so a unit test can reach the
// provider's own guards without a network.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unit test: the network is not available")
}

// assertInvalidRequest checks err is a *model.Error classified
// invalid_request, non-retryable, and mentioning want.
func assertInvalidRequest(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected an error mentioning %q, got nil", want)
	}
	var modelErr *model.Error
	if !errors.As(err, &modelErr) {
		t.Fatalf("error is %T, want a *model.Error the kernel can classify: %v", err, err)
	}
	if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
		t.Errorf("category = %v, want INVALID_REQUEST", modelErr.Category)
	}
	if modelErr.Retryable {
		t.Error("an adapter/kernel bug is not retryable")
	}
	if !strings.Contains(modelErr.Message, want) {
		t.Errorf("message %q does not mention %q", modelErr.Message, want)
	}
}
