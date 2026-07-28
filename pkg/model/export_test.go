package model

// export_test.go bridges convert.go's unexported domain<->proto conversion
// functions to package model_test's black-box tests (convert_test.go),
// following the standard Go "export_test.go" pattern rather than exposing
// these as part of the package's real public API — a plugin author never
// needs to call them directly, only Provider/NewCapabilities/Sink.

import modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

var (
	CapabilitiesToProtoForTest   = capabilitiesToProto
	CapabilitiesFromProtoForTest = capabilitiesFromProto
	ModelSpecToProtoForTest      = modelSpecToProto
	ModelSpecFromProtoForTest    = modelSpecFromProto
	ThinkingSpecToProtoForTest   = thinkingSpecToProto
	ThinkingSpecFromProtoForTest = thinkingSpecFromProto
	CachingSpecToProtoForTest    = cachingSpecToProto
	CachingSpecFromProtoForTest  = cachingSpecFromProto
	PricingToProtoForTest        = pricingToProto
	PricingFromProtoForTest      = pricingFromProto
	PricingTierToProtoForTest    = pricingTierToProto
	PricingTierFromProtoForTest  = pricingTierFromProto
	UsageToProtoForTest          = usageToProto
	UsageFromProtoForTest        = usageFromProto
	AccountToProtoForTest        = accountToProto
	AccountFromProtoForTest      = accountFromProto
	ModelErrorFromProtoForTest   = modelErrorFromProto
)

// ModelErrorToProtoForTest exposes (*Error).toProto — a method,
// rather than a bare func, so it needs a small wrapper instead of a
// var-of-func-value alias like the rest of this file.
func ModelErrorToProtoForTest(e *Error) *modelv1.ModelError {
	return e.toProto()
}

// StatusFromErrForTest exposes statusFromErr, used by errors_test.go to
// verify the cancellation short-circuit and the *Error/unmapped-error
// mapping without going through a real RPC.
var StatusFromErrForTest = statusFromErr
