package model

import (
	"math"
	"sort"

	"google.golang.org/protobuf/types/known/structpb"

	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// Options is a read-only view over a request's provider_options
// (docs/specifications/model/data-types.md#provider_options) — the
// vendor-specific knobs the kernel passes through untouched.
//
// The zero value is usable and empty, so a Provider never has to nil-check
// before reading: a request that carried no provider_options answers false
// to every lookup, which is the same answer an absent key gives. That is
// deliberate — an adapter reading an optional knob wants one branch ("did
// the operator set this?"), not two ("was the field present, and if so was
// the key present?").
//
// Every lookup follows os.LookupEnv's (value, ok) convention. ok is false
// for an absent key AND for a key whose value is the wrong JSON type, so a
// misconfigured value falls back to the adapter's default rather than
// silently becoming a zero value. A Provider that needs to reject a
// wrong-typed value rather than ignore it should check Has first.
type Options struct {
	s *structpb.Struct
}

// ProviderOptions returns a view over req's provider_options. A nil req, or
// one with no provider_options set, yields an empty Options rather than an
// error — absent options are the ordinary case, not a failure.
func ProviderOptions(req *modelv1.StreamCompletionRequest) Options {
	return Options{s: req.GetProviderOptions()}
}

// Has reports whether key is present, regardless of its value's type.
// Use it to distinguish "the operator set this to something unusable" from
// "the operator did not set this", which the (value, ok) lookups below
// deliberately collapse.
func (o Options) Has(key string) bool {
	_, ok := o.s.GetFields()[key]
	return ok
}

// Keys returns the keys present, sorted.
//
// Sorted because a provider may include them in a log line or an error
// message, and Go map iteration order is randomized — an unsorted list
// would make otherwise-identical runs differ (.claude/rules/determinism.md's
// serialization rule).
func (o Options) Keys() []string {
	fields := o.s.GetFields()
	if len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LookupString returns key's string value.
func (o Options) LookupString(key string) (string, bool) {
	v, ok := o.s.GetFields()[key]
	if !ok {
		return "", false
	}
	if _, isString := v.GetKind().(*structpb.Value_StringValue); !isString {
		return "", false
	}
	return v.GetStringValue(), true
}

// LookupBool returns key's boolean value.
func (o Options) LookupBool(key string) (bool, bool) {
	v, ok := o.s.GetFields()[key]
	if !ok {
		return false, false
	}
	if _, isBool := v.GetKind().(*structpb.Value_BoolValue); !isBool {
		return false, false
	}
	return v.GetBoolValue(), true
}

// LookupFloat64 returns key's numeric value.
func (o Options) LookupFloat64(key string) (float64, bool) {
	v, ok := o.s.GetFields()[key]
	if !ok {
		return 0, false
	}
	if _, isNumber := v.GetKind().(*structpb.Value_NumberValue); !isNumber {
		return 0, false
	}
	return v.GetNumberValue(), true
}

// LookupInt64 returns key's numeric value as an int64.
//
// A Struct carries every number as a float64 (it is JSON's model, not
// protobuf's), so this rejects a value that is not exactly representable as
// an int64: a fractional value, a NaN or infinity, or one outside int64's
// range all report false rather than truncating. Silently truncating a
// token budget or a retry count to something the operator did not write is
// worse than falling back to the adapter's default.
func (o Options) LookupInt64(key string) (int64, bool) {
	f, ok := o.LookupFloat64(key)
	if !ok {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	// Compared as float64 against the exact powers of two bracketing
	// int64's range, not against math.MaxInt64: converting math.MaxInt64 to
	// float64 rounds it *up* to 2^63, so a direct f > float64(math.MaxInt64)
	// comparison would let 2^63 itself through and overflow the conversion
	// below.
	const twoPow63 = float64(1 << 63)
	if f >= twoPow63 || f < -twoPow63 {
		return 0, false
	}
	return int64(f), true
}
