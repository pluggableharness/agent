package callhash

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

// FuzzCanonicalStability exercises Canonical against arbitrary JSON input,
// asserting that canonicalization is stable and idempotent:
//  1. Feed arbitrary JSON strings as seed inputs.
//  2. Unmarshal to structpb.Value.
//  3. Marshal-canonicalize-remarshal-canonicalize, assert both canonical outputs are identical.
//  4. No panic on arbitrary attacker-controlled input.
func FuzzCanonicalStability(f *testing.F) {
	// Add seed examples: various JSON structures.
	f.Add(`{}`)
	f.Add(`{"a":1}`)
	f.Add(`{"z":1,"a":2}`)
	f.Add(`[]`)
	f.Add(`[1,2,3]`)
	f.Add(`[{"a":1},{"b":2}]`)
	f.Add(`{"nested":{"deep":{"value":42}}}`)
	f.Add(`{"array":[1,"two",true,null,{"inner":3}]}`)
	f.Add(`null`)
	f.Add(`true`)
	f.Add(`false`)
	f.Add(`42`)
	f.Add(`3.14`)
	f.Add(`"hello"`)
	f.Add(`"with\nescape"`)
	f.Add(`{"a":null}`)
	f.Add(`{"z":{"y":{"x":"deep"}}}`)

	f.Fuzz(func(t *testing.T, jsonStr string) {
		// Parse the JSON to a generic interface{} first (standard JSON unmarshaling).
		var parsed interface{}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
			// Not valid JSON; skip.
			return
		}

		// Marshal back to JSON bytes.
		marshaledBytes, err := json.Marshal(parsed)
		if err != nil {
			// Should not happen if Unmarshal succeeded.
			t.Fatalf("re-marshal failed: %v", err)
		}

		// Now unmarshal to structpb.Value and canonicalize.
		var val structpb.Value
		if err := json.Unmarshal(marshaledBytes, &val); err != nil {
			// structpb.Value may fail on some valid JSON; skip.
			return
		}

		// First canonicalization.
		c1 := Canonical(&val)

		// Re-marshal the canonical output back to a value.
		var val2 structpb.Value
		if err := json.Unmarshal(c1, &val2); err != nil {
			t.Fatalf("canonical output not valid JSON: %s, error: %v", c1, err)
		}

		// Second canonicalization of the re-marshaled value.
		c2 := Canonical(&val2)

		// Both canonical outputs must be identical (idempotent).
		if string(c1) != string(c2) {
			t.Fatalf("canonical not idempotent:\nFirst:  %s\nSecond: %s", c1, c2)
		}
	})
}

// FuzzFieldsNoNil exercises Fields against arbitrary JSON input,
// asserting it never panics on arbitrary input and produces consistent results.
func FuzzFieldsNoNil(f *testing.F) {
	f.Add(`{}`, "")
	f.Add(`{"path":"test"}`, "path")
	f.Add(`{"a":1,"b":2}`, "a,b")
	f.Add(`{"x":null}`, "x")
	f.Add(`[]`, "")
	f.Add(`null`, "")
	f.Add(`42`, "")

	f.Fuzz(func(t *testing.T, jsonStr string, keyFieldsStr string) {
		// Parse JSON to structpb.Struct or handle non-struct cases.
		var val interface{}
		if err := json.Unmarshal([]byte(jsonStr), &val); err != nil {
			return
		}

		// Parse key fields (simple comma-separated).
		var keyFields []string
		if keyFieldsStr != "" {
			// This is simplified; a real parser would handle escaping.
			// For fuzzing, just use single characters or simple names.
			// To avoid parsing complexity in the fuzzer, we'll just skip if it's not simple.
			if len(keyFieldsStr) > 0 && keyFieldsStr[0] != ',' {
				keyFields = []string{keyFieldsStr}
			}
		}

		// Build a structpb.Struct from the parsed value.
		valBytes, _ := json.Marshal(val)
		var s structpb.Struct
		if err := json.Unmarshal(valBytes, &s); err != nil {
			// If it's not a struct, that's fine; Fields handles nil.
			s = structpb.Struct{Fields: make(map[string]*structpb.Value)}
		}

		// Call Fields; it must not panic.
		result := Fields(&s, keyFields)

		// Call again with the same inputs; result must be identical.
		result2 := Fields(&s, keyFields)

		if result != result2 {
			t.Fatalf("Fields not deterministic:\nFirst:  %s\nSecond: %s", result, result2)
		}

		// If result is non-empty, it should be valid JSON (as a JSON array).
		if result != "" {
			var v interface{}
			if err := json.Unmarshal([]byte(result), &v); err != nil {
				t.Fatalf("Fields output not valid JSON: %s", result)
			}
		}
	})
}

// FuzzCallNoNil exercises Call against arbitrary JSON input and tool names,
// asserting it never panics and produces consistent hashes.
func FuzzCallNoNil(f *testing.F) {
	f.Add(`{}`, "tool1")
	f.Add(`{"a":1}`, "tool2")
	f.Add(`null`, "tool")
	f.Add(`[]`, "test")
	f.Add(`42`, "read")
	f.Add(`"string"`, "write")

	f.Fuzz(func(t *testing.T, jsonStr string, toolName string) {
		// Parse JSON.
		var val interface{}
		if err := json.Unmarshal([]byte(jsonStr), &val); err != nil {
			return
		}

		// Build structpb.Struct.
		valBytes, _ := json.Marshal(val)
		var s structpb.Struct
		_ = json.Unmarshal(valBytes, &s)

		// Call must not panic.
		hash1 := Call(toolName, &s)

		// Call again; hash must be identical.
		hash2 := Call(toolName, &s)

		if hash1 != hash2 {
			t.Fatalf("Call not deterministic for tool %q", toolName)
		}

		// Hash should be 64 hex characters (SHA-256).
		if len(hash1) != 64 {
			t.Fatalf("Call hash invalid length: %d, want 64", len(hash1))
		}

		// Verify it's valid hex.
		for _, c := range hash1 {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("Call hash contains non-hex: %c", c)
			}
		}
	})
}
