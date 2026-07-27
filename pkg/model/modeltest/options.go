package modeltest

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Option configures a conformance run.
type Option func(*config)

// config is the resolved option set for one run.
type config struct {
	configure     *structpb.Struct
	modelID       string
	streamRequest *modelv1.StreamCompletionRequest
	skipStream    bool
	identity      identityExpectation
	callTimeout   time.Duration
	// inProcess records which drive mode resolved this config, so a check
	// that only means something against a built binary can say so.
	inProcess bool
}

// identityExpectation is what Describe must report, when the caller cares.
// Empty fields are not checked, since a plugin stamping its version with
// -ldflags legitimately reports "0.0.0" in a test build.
type identityExpectation struct {
	name    string
	version string
	source  string
}

// WithConfig supplies the config object the run passes to Configure.
//
// This is also how a provider is pointed at a recorded transcript or a
// local test server rather than a real vendor: a conformance run must not
// make billed network calls, and the provider's own base-URL attribute is
// the supported way to redirect it.
func WithConfig(cfg *structpb.Struct) Option {
	return func(c *config) { c.configure = cfg }
}

// WithCallTimeout bounds each RPC the suite issues. Defaults to
// DefaultCallTimeout.
//
// Lower it against a fake or a recorded transcript, where any real delay
// means the provider is wedged and waiting the full default only makes
// the failure slower to see. Raise it for a provider that legitimately
// talks to a slow vendor.
func WithCallTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.callTimeout = d
		}
	}
}

// WithModelID selects which advertised model the behavioral checks
// exercise. Defaults to the first model in GetCapabilities' response.
func WithModelID(id string) Option {
	return func(c *config) { c.modelID = id }
}

// WithStreamRequest replaces the request the behavioral checks send.
//
// Use it to drive content the default request cannot reach — a tool call,
// an image, or the prompt that makes a vendor emit encrypted reasoning —
// so the opportunistic checks have something to bite on. The request's
// model_id is overwritten with the resolved model, so a caller does not
// have to keep the two in sync.
func WithStreamRequest(req *modelv1.StreamCompletionRequest) Option {
	return func(c *config) { c.streamRequest = req }
}

// WithoutStreamCompletion skips every behavioral check, leaving only the
// declarative ones.
//
// Intended for a provider that genuinely cannot complete a request in a
// hermetic environment. It is a real reduction in coverage and the run
// reports it as skipped rather than passing quietly.
func WithoutStreamCompletion() Option {
	return func(c *config) { c.skipStream = true }
}

// WithExpectedIdentity asserts what Describe reports. Empty arguments are
// not checked — a test build legitimately carries an unstamped version.
func WithExpectedIdentity(name, version, source string) Option {
	return func(c *config) {
		c.identity = identityExpectation{name: name, version: version, source: source}
	}
}

// resolve applies opts over the defaults.
func resolve(opts []Option) *config {
	c := &config{configure: &structpb.Struct{}, callTimeout: DefaultCallTimeout}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// streamRequestFor returns the request the behavioral checks should send
// for modelID, either the caller's or a minimal default.
func (c *config) streamRequestFor(modelID string) *modelv1.StreamCompletionRequest {
	req := c.streamRequest
	if req == nil {
		req = &modelv1.StreamCompletionRequest{
			Messages: []*contentv1.Message{{
				Role: contentv1.Role_ROLE_USER,
				Content: []*contentv1.ContentBlock{{
					Block: &contentv1.ContentBlock_Text{
						Text: &contentv1.TextBlock{Text: "Reply with the single word: pong"},
					},
				}},
			}},
		}
	}
	// Cloned so a caller reusing one request across runs, or across a
	// Run/RunBinary pair, never has it mutated underneath them.
	out, ok := proto.Clone(req).(*modelv1.StreamCompletionRequest)
	if !ok {
		// Unreachable for a well-formed request: proto.Clone returns the
		// same concrete type it was given. Checked rather than asserted
		// per go-style.md's comma-ok rule.
		return req
	}
	out.ModelId = modelID
	return out
}
