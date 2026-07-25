package anthropic

import (
	"github.com/pluggableharness/agent/pkg/model"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// configError builds the invalid_request model error every Configure-time
// failure reports.
//
// invalid_request rather than auth_error even for a missing api_key: at
// Configure time nothing has been presented to Anthropic, so nothing has
// been refused. What is wrong is the operator's agent.hcl, which is
// exactly what docs/specifications/model/conformance.md's error taxonomy
// means by invalid_request. auth_error is reserved for a credential the
// vendor actually rejected, and the kernel treats the two very
// differently — auth_error MUST NOT be retried or fallen back from, and
// surfaces to a human.
//
// Never retryable: the same config produces the same failure.
//
// The caller is responsible for keeping secrets out of message. Every
// call site in this package passes either a fixed string or an attribute
// name, never a config value — see config_test.go's
// TestDecodeSettings_neverEchoesTheKey, which runs the rejection paths
// with a real-looking key present specifically so a future edit that
// interpolated the config wholesale would fail there.
func configError(message string) error {
	return &model.Error{
		Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
		Message:   "anthropic: configure: " + message,
		Retryable: false,
	}
}

// notConfiguredError is what StreamCompletion and CountTokens report when
// they are called before Configure succeeded. The kernel always calls
// Configure first, so this is a kernel-side ordering bug rather than an
// operator mistake — invalid_request is the taxonomy's slot for
// "almost always a kernel/adapter bug", and it is explicitly
// non-retryable because the ordering will not fix itself.
func notConfiguredError(rpc string) error {
	return &model.Error{
		Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
		Message:   "anthropic: " + rpc + ": provider is not configured — Configure must succeed first",
		Retryable: false,
	}
}

// unknownModelError reports a model_id this provider does not serve. The
// kernel resolves a model against GetCapabilities before dispatching, so
// reaching here means the kernel's view and the catalog's disagree —
// again a kernel/adapter bug rather than a vendor condition, and again
// not something a retry can fix.
//
// The requested id is safe to include: it came from the kernel's own
// request, not from configuration, and naming it is the whole diagnostic
// value of the message.
func unknownModelError(rpc, modelID string) error {
	return &model.Error{
		Category:  modelv1.ModelErrorCategory_MODEL_ERROR_CATEGORY_INVALID_REQUEST,
		Message:   "anthropic: " + rpc + ": unknown model " + modelID,
		Retryable: false,
	}
}
