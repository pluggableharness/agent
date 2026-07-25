package callhash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"google.golang.org/protobuf/types/known/structpb"
)

// Call returns the deterministic hash of one tool call, per
// turn-algorithm.md#doom-loop-detection:
// hash_of(call) = hash(tool_name, canonicalize(input_json)).
// Uses SHA-256 over "toolName\x00" + Canonical(args), hex-encoded.
func Call(toolName string, args *structpb.Struct) string {
	h := sha256.New()
	h.Write([]byte(toolName))
	h.Write([]byte{0})
	// Wrap the struct in a Value for canonical encoding.
	if args == nil {
		args = &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	}
	h.Write(Canonical(&structpb.Value{
		Kind: &structpb.Value_StructValue{StructValue: args},
	}))
	return hex.EncodeToString(h.Sum(nil))
}

// Fields returns the canonical serialization of the named key_fields'
// values from args, forming the value(key_fields) component of a
// ConcurrencySpec scheduling key (tool/data-types.md#concurrencyspec).
// A named field absent from args MUST contribute an explicit null, so
// "path omitted" and "path: null" hash identically to each other and
// both differ from any set value. Field order in the output MUST follow
// the order keyFields is given in (not sorted), since that's the
// declared key shape a caller controls.
func Fields(args *structpb.Struct, keyFields []string) string {
	if args == nil {
		args = &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	}
	if len(keyFields) == 0 {
		return ""
	}

	// Build a slice of key-value pairs preserving keyFields order.
	result := make([]*structpb.Value, len(keyFields))
	for i, fieldName := range keyFields {
		if v, ok := args.Fields[fieldName]; ok {
			result[i] = v
		} else {
			// Absent field contributes explicit null.
			result[i] = &structpb.Value{Kind: &structpb.Value_NullValue{}}
		}
	}

	// Marshal as JSON array preserving order.
	return string(canonicalJSONArray(result))
}

// Canonical is the single deterministic JSON encoding used by both Call
// and Fields: object keys sorted, no insignificant whitespace, no
// dependence on Go map iteration order (determinism.md#serialization).
// Must produce byte-identical output for the same logical value
// regardless of how many times it's re-marshaled or which order a
// structpb.Struct's internal map happens to iterate.
func Canonical(v *structpb.Value) []byte {
	if v == nil {
		return []byte("null")
	}
	return canonicalValue(v)
}

func canonicalValue(v *structpb.Value) []byte {
	if v == nil {
		return []byte("null")
	}

	switch kind := v.Kind.(type) {
	case *structpb.Value_NullValue:
		return []byte("null")
	case *structpb.Value_BoolValue:
		if kind.BoolValue {
			return []byte("true")
		}
		return []byte("false")
	case *structpb.Value_NumberValue:
		// Use JSON standard number encoding via json.Number.
		b, _ := json.Marshal(kind.NumberValue)
		return b
	case *structpb.Value_StringValue:
		b, _ := json.Marshal(kind.StringValue)
		return b
	case *structpb.Value_StructValue:
		return canonicalJSONStruct(kind.StructValue)
	case *structpb.Value_ListValue:
		return canonicalJSONArray(kind.ListValue.Values)
	default:
		return []byte("null")
	}
}

func canonicalJSONStruct(s *structpb.Struct) []byte {
	if s == nil || len(s.Fields) == 0 {
		return []byte("{}")
	}

	// Sort keys to ensure deterministic output independent of map iteration order.
	keys := make([]string, 0, len(s.Fields))
	for k := range s.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build JSON object with sorted keys.
	var buf []byte
	buf = append(buf, '{')
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		// Key as JSON string.
		keyJSON, _ := json.Marshal(k)
		buf = append(buf, keyJSON...)
		buf = append(buf, ':')
		// Value as canonical JSON.
		buf = append(buf, canonicalValue(s.Fields[k])...)
	}
	buf = append(buf, '}')
	return buf
}

func canonicalJSONArray(values []*structpb.Value) []byte {
	if len(values) == 0 {
		return []byte("[]")
	}

	var buf []byte
	buf = append(buf, '[')
	for i, v := range values {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, canonicalValue(v)...)
	}
	buf = append(buf, ']')
	return buf
}
