package anthropic

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// Config attribute names, as an operator writes them in the provider
// block of agent.hcl. Kept as constants so the schema declaration and the
// decoder cannot drift apart into a field the schema advertises and the
// decoder never reads.
const (
	attrAPIKey         = "api_key"
	attrBaseURL        = "base_url"
	attrRequestTimeout = "request_timeout_seconds"
)

// Defaults for the two optional attributes.
const (
	// DefaultBaseURL is Anthropic's public API endpoint. base_url exists
	// to be overridden by a proxy or a test server, not because the
	// endpoint is expected to vary in normal use.
	DefaultBaseURL = "https://api.anthropic.com"

	// DefaultRequestTimeout bounds one HTTP request/stream. Ten minutes
	// matches Anthropic's own SDK default and is deliberately generous:
	// a high-effort completion on a large context legitimately runs for
	// minutes, and a timeout that fires mid-stream looks to the kernel
	// like a provider failure rather than a client impatience.
	DefaultRequestTimeout = 10 * time.Minute

	// maxRequestTimeoutSeconds bounds what an operator may configure. A
	// timeout this long is already far past any legitimate completion; a
	// larger one is a typo (or a missing decimal point) that would wedge
	// a turn for hours rather than failing it.
	maxRequestTimeoutSeconds = 3600
)

// settings is the decoded, validated form of the provider's agent.hcl
// block — what Configure produces and StreamCompletion reads.
type settings struct {
	// apiKey is the operator's Anthropic API key. Never logged, never
	// echoed into an error message or an emitted event
	// (docs/specifications/model/protocol.md#configure).
	apiKey string
	// baseURL is the API endpoint, without a trailing slash.
	baseURL string
	// requestTimeout bounds one HTTP request.
	requestTimeout time.Duration
}

// ConfigSchema returns the provider's agent.hcl schema, per
// docs/specifications/model/protocol.md#getcapabilities — the kernel needs
// it before it ever calls Configure, so it rides along on
// GetCapabilities' response.
func ConfigSchema() (*configv1.ConfigSchema, error) {
	apiKey, err := config.Attribute(attrAPIKey, configv1.AttrType_ATTR_TYPE_STRING,
		config.WithRequired(),
		// Sensitive restricts the attribute's agent.hcl expression to
		// env(...) indirection and keeps the value out of anything the
		// kernel renders or logs. It also forbids a default, which is
		// correct: there is no sane fallback for a credential.
		config.WithSensitive(),
		config.WithDescription("Anthropic API key. Written as env(\"ANTHROPIC_API_KEY\"); the kernel resolves the indirection before Configure is called."),
	)
	if err != nil {
		return nil, fmt.Errorf("anthropic: config schema: %w", err)
	}

	baseURL, err := config.Attribute(attrBaseURL, configv1.AttrType_ATTR_TYPE_STRING,
		config.WithDefault(`"`+DefaultBaseURL+`"`),
		config.WithDescription("API endpoint override, for a proxy or a gateway. Defaults to Anthropic's public endpoint."),
	)
	if err != nil {
		return nil, fmt.Errorf("anthropic: config schema: %w", err)
	}

	timeout, err := config.Attribute(attrRequestTimeout, configv1.AttrType_ATTR_TYPE_NUMBER,
		config.WithDefault(fmt.Sprintf("%d", int(DefaultRequestTimeout.Seconds()))),
		config.WithDescription("Per-request timeout in seconds. A long completion legitimately runs for minutes; this is a ceiling, not a target."),
	)
	if err != nil {
		return nil, fmt.Errorf("anthropic: config schema: %w", err)
	}

	schema, err := config.Schema(apiKey, baseURL, timeout)
	if err != nil {
		return nil, fmt.Errorf("anthropic: config schema: %w", err)
	}
	return schema, nil
}

// decodeSettings converts the Struct the kernel's schema-to-cty bridge
// produced into validated settings.
//
// Every failure here is MODEL_ERROR_CATEGORY_INVALID_REQUEST rather than
// AUTH_ERROR, including a missing api_key: at Configure time the key has
// not been presented to Anthropic, so nothing has rejected it — what is
// wrong is the operator's config, which is what invalid_request means.
// AUTH_ERROR is reserved for a key the vendor actually refused.
//
// Configure MUST fail here rather than deferring to the first
// StreamCompletion call (docs/specifications/model/protocol.md#configure),
// which is why this validates rather than filling in blanks.
func decodeSettings(cfg *structpb.Struct) (settings, error) {
	fields := cfg.GetFields()

	apiKey, err := requiredString(fields, attrAPIKey)
	if err != nil {
		return settings{}, err
	}

	baseURL := DefaultBaseURL
	if v, ok := fields[attrBaseURL]; ok && v.GetStringValue() != "" {
		baseURL = v.GetStringValue()
	}
	if err := validateBaseURL(baseURL); err != nil {
		return settings{}, err
	}

	timeout := DefaultRequestTimeout
	if v, ok := fields[attrRequestTimeout]; ok {
		seconds := v.GetNumberValue()
		if seconds <= 0 || seconds > maxRequestTimeoutSeconds {
			return settings{}, configError(fmt.Sprintf(
				"%s must be between 1 and %d, got %v", attrRequestTimeout, maxRequestTimeoutSeconds, seconds))
		}
		timeout = time.Duration(seconds * float64(time.Second))
	}

	return settings{
		apiKey:         apiKey,
		baseURL:        strings.TrimRight(baseURL, "/"),
		requestTimeout: timeout,
	}, nil
}

// requiredString reads a non-empty string attribute, or reports which one
// was missing. The value itself is never included in an error, because
// the only required attribute is the API key.
func requiredString(fields map[string]*structpb.Value, name string) (string, error) {
	v, ok := fields[name]
	if !ok {
		return "", configError(name + " is required")
	}
	s := v.GetStringValue()
	if s == "" {
		return "", configError(name + " is required and must be a non-empty string")
	}
	return s, nil
}

// validateBaseURL rejects an endpoint the HTTP client could not use, and
// rejects a plaintext one that could leave the machine: the API key
// travels in a header on every request, so http:// to a remote host would
// hand it to anything on the path. An operator with a genuine remote HTTP
// proxy is better served by terminating TLS at that proxy than by this
// plugin quietly downgrading.
//
// Plain http:// to a loopback host is allowed, and that carve-out is
// deliberate rather than a convenience: it is what lets the integration
// tier point this plugin at an httptest.Server replaying a recorded
// transcript, and a loopback request never reaches a network anyone else
// can observe. A real Anthropic endpoint is never on loopback, so the
// exemption cannot widen into the case it is protecting against.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return configError(fmt.Sprintf("%s is not a valid URL: %v", attrBaseURL, err))
	}
	if u.Host == "" {
		return configError(attrBaseURL + " must be an absolute URL, e.g. https://api.anthropic.com")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return configError(fmt.Sprintf(
		"%s must use https (got %q) — the API key is sent as a request header on every call; plain http is accepted only for a loopback host",
		attrBaseURL, u.Scheme))
}

// isLoopbackHost reports whether host is unambiguously this machine.
// "localhost" is matched by name because it is not an IP literal, and
// every other case is decided by net.IP rather than by string prefix —
// "127.0.0.1.evil.com" is a hostname, not a loopback address, and a
// prefix check would wave it through.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
