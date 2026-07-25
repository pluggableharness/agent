package anthropic

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// TestConfigSchema_declaresTheThreeAttributes pins the schema shape the
// kernel reads before it ever calls Configure, and in particular that
// api_key is both required and sensitive — sensitive is what restricts it
// to env(...) indirection in agent.hcl and keeps it out of rendered
// output.
func TestConfigSchema_declaresTheThreeAttributes(t *testing.T) {
	t.Parallel()

	schema, err := ConfigSchema()
	if err != nil {
		t.Fatalf("ConfigSchema: %v", err)
	}

	byName := make(map[string]bool)
	for _, attr := range schema.GetAttributes() {
		byName[attr.GetName()] = true
		switch attr.GetName() {
		case attrAPIKey:
			if !attr.GetRequired() {
				t.Error("api_key must be required")
			}
			if !attr.GetSensitive() {
				t.Error("api_key must be sensitive")
			}
			if attr.GetDefaultJson() != "" {
				t.Error("a credential must not carry a default")
			}
		case attrBaseURL, attrRequestTimeout:
			if attr.GetRequired() {
				t.Errorf("%s must be optional", attr.GetName())
			}
			if attr.GetDefaultJson() == "" {
				t.Errorf("%s must declare a default", attr.GetName())
			}
		}
	}
	for _, want := range []string{attrAPIKey, attrBaseURL, attrRequestTimeout} {
		if !byName[want] {
			t.Errorf("schema is missing %q", want)
		}
	}
}

// TestDecodeSettings_accepts covers the shapes an operator can legally
// write, including the two optional attributes falling back to their
// documented defaults.
func TestDecodeSettings_accepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fields      map[string]any
		wantBaseURL string
		wantTimeout time.Duration
	}{
		{
			name:        "only the required key",
			fields:      map[string]any{attrAPIKey: "sk-ant-test"},
			wantBaseURL: DefaultBaseURL,
			wantTimeout: DefaultRequestTimeout,
		},
		{
			name: "every attribute set",
			fields: map[string]any{
				attrAPIKey:         "sk-ant-test",
				attrBaseURL:        "https://gateway.example.com",
				attrRequestTimeout: 42.0,
			},
			wantBaseURL: "https://gateway.example.com",
			wantTimeout: 42 * time.Second,
		},
		{
			name: "a trailing slash on base_url is trimmed",
			fields: map[string]any{
				attrAPIKey:  "sk-ant-test",
				attrBaseURL: "https://gateway.example.com/",
			},
			wantBaseURL: "https://gateway.example.com",
			wantTimeout: DefaultRequestTimeout,
		},
		{
			name: "an empty base_url falls back to the default",
			fields: map[string]any{
				attrAPIKey:  "sk-ant-test",
				attrBaseURL: "",
			},
			wantBaseURL: DefaultBaseURL,
			wantTimeout: DefaultRequestTimeout,
		},
		{
			// The carve-out the integration tier depends on: an
			// httptest.Server listens on plain http at 127.0.0.1.
			name: "plain http to a loopback IP",
			fields: map[string]any{
				attrAPIKey:  "sk-ant-test",
				attrBaseURL: "http://127.0.0.1:53219",
			},
			wantBaseURL: "http://127.0.0.1:53219",
			wantTimeout: DefaultRequestTimeout,
		},
		{
			name: "plain http to localhost by name",
			fields: map[string]any{
				attrAPIKey:  "sk-ant-test",
				attrBaseURL: "http://localhost:8080",
			},
			wantBaseURL: "http://localhost:8080",
			wantTimeout: DefaultRequestTimeout,
		},
		{
			name: "plain http to the IPv6 loopback",
			fields: map[string]any{
				attrAPIKey:  "sk-ant-test",
				attrBaseURL: "http://[::1]:8080",
			},
			wantBaseURL: "http://[::1]:8080",
			wantTimeout: DefaultRequestTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeSettings(mustStruct(t, tc.fields))
			if err != nil {
				t.Fatalf("decodeSettings: %v", err)
			}
			if got.apiKey != "sk-ant-test" {
				t.Errorf("apiKey = %q, want the configured key", got.apiKey)
			}
			if got.baseURL != tc.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", got.baseURL, tc.wantBaseURL)
			}
			if got.requestTimeout != tc.wantTimeout {
				t.Errorf("requestTimeout = %v, want %v", got.requestTimeout, tc.wantTimeout)
			}
		})
	}
}

// TestDecodeSettings_rejects covers every way a config can be wrong. All
// of them must be invalid_request and non-retryable: retrying the same
// bad config produces the same failure, and nothing has been presented to
// the vendor yet for an auth_error to be the honest classification.
func TestDecodeSettings_rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fields     map[string]any
		wantSubstr string
	}{
		{"no api_key at all", map[string]any{}, "api_key is required"},
		{"an empty api_key", map[string]any{attrAPIKey: ""}, "api_key is required"},
		{
			"a plaintext base_url to a remote host",
			map[string]any{attrAPIKey: "k", attrBaseURL: "http://gateway.example.com"},
			"must use https",
		},
		{
			// The loopback carve-out must not be reachable by a hostname
			// that merely starts with a loopback literal — that is a real
			// remote host, and a prefix check would wave it through.
			"a plaintext host that only looks like loopback",
			map[string]any{attrAPIKey: "k", attrBaseURL: "http://127.0.0.1.evil.example.com"},
			"must use https",
		},
		{
			"a non-loopback private address over plaintext",
			map[string]any{attrAPIKey: "k", attrBaseURL: "http://10.0.0.5:8080"},
			"must use https",
		},
		{
			"an unsupported scheme",
			map[string]any{attrAPIKey: "k", attrBaseURL: "ftp://example.com"},
			"must use https",
		},
		{
			"a relative base_url",
			map[string]any{attrAPIKey: "k", attrBaseURL: "/v1"},
			"must be an absolute URL",
		},
		{
			"an unparseable base_url",
			map[string]any{attrAPIKey: "k", attrBaseURL: "https://exa mple.com/\x7f"},
			attrBaseURL,
		},
		{
			"a zero timeout",
			map[string]any{attrAPIKey: "k", attrRequestTimeout: 0.0},
			attrRequestTimeout,
		},
		{
			"a negative timeout",
			map[string]any{attrAPIKey: "k", attrRequestTimeout: -1.0},
			attrRequestTimeout,
		},
		{
			"an absurdly long timeout",
			map[string]any{attrAPIKey: "k", attrRequestTimeout: 999999.0},
			attrRequestTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeSettings(mustStruct(t, tc.fields))
			if err == nil {
				t.Fatal("decodeSettings accepted an invalid config")
			}

			var modelErr *model.Error
			if !errors.As(err, &modelErr) {
				t.Fatalf("error is %T, want a *model.Error the kernel can classify", err)
			}
			if modelErr.Category != modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST {
				t.Errorf("category = %v, want INVALID_REQUEST", modelErr.Category)
			}
			if modelErr.Retryable {
				t.Error("a bad config is not retryable — the same config fails identically")
			}
			if !strings.Contains(modelErr.Message, tc.wantSubstr) {
				t.Errorf("message %q does not mention %q", modelErr.Message, tc.wantSubstr)
			}
		})
	}
}

// TestDecodeSettings_neverEchoesTheKey guards
// docs/specifications/model/protocol.md#configure's rule that a plugin
// MUST NOT put a secret into an error message. The rejection paths below
// all run with a real-looking key present, so any handler that
// interpolated the config wholesale would leak it here.
func TestDecodeSettings_neverEchoesTheKey(t *testing.T) {
	t.Parallel()

	const secret = "sk-ant-super-secret-value"
	bad := []map[string]any{
		{attrAPIKey: secret, attrBaseURL: "http://insecure.example.com"},
		{attrAPIKey: secret, attrBaseURL: "not-a-url"},
		{attrAPIKey: secret, attrRequestTimeout: -5.0},
	}

	for _, fields := range bad {
		_, err := decodeSettings(mustStruct(t, fields))
		if err == nil {
			t.Fatal("expected a rejection")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the api key leaked into an error message: %q", err.Error())
		}
	}
}

// TestDecodeSettings_nilStruct is the degenerate input the kernel would
// send for a provider block with no attributes at all. It must be the
// same clean rejection as an empty struct, not a panic.
func TestDecodeSettings_nilStruct(t *testing.T) {
	t.Parallel()

	if _, err := decodeSettings(nil); err == nil {
		t.Fatal("a nil config must be rejected, not defaulted")
	}
}

// mustStruct builds the structpb.Struct the kernel's schema-to-cty bridge
// would have produced for these fields.
func mustStruct(t *testing.T, fields map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("structpb.NewStruct(%v): %v", fields, err)
	}
	return s
}
