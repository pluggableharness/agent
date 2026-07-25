package callhash

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

func TestCanonical_KeyOrderIndependence(t *testing.T) {
	t.Parallel()

	// Build the same struct two different ways: insert fields in different orders.
	// The canonical output must be identical.

	s1 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"a": structpb.NewNumberValue(1),
			"b": structpb.NewStringValue("hello"),
			"c": structpb.NewBoolValue(true),
		},
	}

	s2 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"c": structpb.NewBoolValue(true),
			"a": structpb.NewNumberValue(1),
			"b": structpb.NewStringValue("hello"),
		},
	}

	c1 := Canonical(&structpb.Value{Kind: &structpb.Value_StructValue{StructValue: s1}})
	c2 := Canonical(&structpb.Value{Kind: &structpb.Value_StructValue{StructValue: s2}})

	if string(c1) != string(c2) {
		t.Fatalf("Canonical output differs with different field insertion order\nFirst: %s\nSecond: %s", c1, c2)
	}

	// Verify the output is sorted by keys.
	expected := `{"a":1,"b":"hello","c":true}`
	if string(c1) != expected {
		t.Fatalf("Canonical output not sorted correctly: got %s, want %s", c1, expected)
	}
}

func TestCanonical_NestedObjects(t *testing.T) {
	t.Parallel()

	outer := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"z": structpb.NewStringValue("outer"),
			"inner": {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"y": structpb.NewNumberValue(2),
							"x": structpb.NewNumberValue(1),
						},
					},
				},
			},
		},
	}

	c := Canonical(&structpb.Value{Kind: &structpb.Value_StructValue{StructValue: outer}})
	expected := `{"inner":{"x":1,"y":2},"z":"outer"}`
	if string(c) != expected {
		t.Fatalf("Nested object canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestCanonical_Arrays(t *testing.T) {
	t.Parallel()

	arr := &structpb.Value{
		Kind: &structpb.Value_ListValue{
			ListValue: &structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewNumberValue(1),
					structpb.NewStringValue("two"),
					structpb.NewBoolValue(false),
					structpb.NewNullValue(),
				},
			},
		},
	}

	c := Canonical(arr)
	expected := `[1,"two",false,null]`
	if string(c) != expected {
		t.Fatalf("Array canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestCanonical_Null(t *testing.T) {
	t.Parallel()

	c := Canonical(structpb.NewNullValue())
	expected := "null"
	if string(c) != expected {
		t.Fatalf("Null canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestCanonical_Booleans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    *structpb.Value
		expected string
	}{
		{"true", structpb.NewBoolValue(true), "true"},
		{"false", structpb.NewBoolValue(false), "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Canonical(tt.value)
			if string(c) != tt.expected {
				t.Fatalf("got %s, want %s", c, tt.expected)
			}
		})
	}
}

func TestCanonical_Numbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    float64
		expected string
	}{
		{"integer", 42, "42"},
		{"float", 3.14, "3.14"},
		{"negative", -5, "-5"},
		{"zero", 0, "0"},
		{"scientific", 1e3, "1000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Canonical(structpb.NewNumberValue(tt.value))
			if string(c) != tt.expected {
				t.Fatalf("got %s, want %s", c, tt.expected)
			}
		})
	}
}

func TestCanonical_Strings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"simple", "hello", `"hello"`},
		{"empty", "", `""`},
		{"with spaces", "hello world", `"hello world"`},
		{"with quotes", `say "hi"`, `"say \"hi\""`},
		{"with newline", "line1\nline2", `"line1\nline2"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Canonical(structpb.NewStringValue(tt.value))
			if string(c) != tt.expected {
				t.Fatalf("got %s, want %s", c, tt.expected)
			}
		})
	}
}

func TestCanonical_NilValue(t *testing.T) {
	t.Parallel()

	c := Canonical(nil)
	expected := "null"
	if string(c) != expected {
		t.Fatalf("nil Canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestCanonical_EmptyStruct(t *testing.T) {
	t.Parallel()

	s := &structpb.Struct{Fields: make(map[string]*structpb.Value)}
	c := Canonical(&structpb.Value{Kind: &structpb.Value_StructValue{StructValue: s}})
	expected := "{}"
	if string(c) != expected {
		t.Fatalf("empty struct canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestCanonical_EmptyArray(t *testing.T) {
	t.Parallel()

	arr := &structpb.Value{
		Kind: &structpb.Value_ListValue{
			ListValue: &structpb.ListValue{Values: []*structpb.Value{}},
		},
	}

	c := Canonical(arr)
	expected := "[]"
	if string(c) != expected {
		t.Fatalf("empty array canonical output wrong: got %s, want %s", c, expected)
	}
}

func TestFields_AbsentVsNullVsSame(t *testing.T) {
	t.Parallel()

	// Build three structs: one with absent field, one with explicit null, one with a value.
	absent := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"other": structpb.NewStringValue("data"),
		},
	}

	withNull := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path":  structpb.NewNullValue(),
			"other": structpb.NewStringValue("data"),
		},
	}

	withValue := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path":  structpb.NewStringValue("some/path"),
			"other": structpb.NewStringValue("data"),
		},
	}

	// Absent and null should produce identical output.
	absentFields := Fields(absent, []string{"path"})
	nullFields := Fields(withNull, []string{"path"})

	if absentFields != nullFields {
		t.Fatalf("absent and null should produce same Fields output: absent=%s, null=%s", absentFields, nullFields)
	}

	// Value should differ from both.
	valueFields := Fields(withValue, []string{"path"})
	if valueFields == absentFields {
		t.Fatalf("value should differ from absent/null: got %s, same as %s", valueFields, absentFields)
	}

	// Verify the exact values.
	if absentFields != "[null]" {
		t.Fatalf("absent/null Fields should be [null], got %s", absentFields)
	}
	if valueFields != `["some/path"]` {
		t.Fatalf("value Fields should be [\"some/path\"], got %s", valueFields)
	}
}

func TestFields_KeyFieldOrder(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"a": structpb.NewNumberValue(1),
			"b": structpb.NewNumberValue(2),
			"c": structpb.NewNumberValue(3),
		},
	}

	// Order in the keyFields slice should be preserved in output, not sorted.
	order1 := Fields(args, []string{"c", "a", "b"})
	order2 := Fields(args, []string{"b", "c", "a"})

	if order1 != "[3,1,2]" {
		t.Fatalf("Fields order1 wrong: got %s, want [3,1,2]", order1)
	}
	if order2 != "[2,3,1]" {
		t.Fatalf("Fields order2 wrong: got %s, want [2,3,1]", order2)
	}

	// Different orders should produce different results.
	if order1 == order2 {
		t.Fatalf("different key field orders should produce different Fields output")
	}
}

func TestFields_EmptyKeyFields(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"a": structpb.NewNumberValue(1),
		},
	}

	result := Fields(args, []string{})
	if result != "" {
		t.Fatalf("empty keyFields should return empty string, got %s", result)
	}
}

func TestFields_NilArgs(t *testing.T) {
	t.Parallel()

	result := Fields(nil, []string{"path"})
	if result != "[null]" {
		t.Fatalf("nil args should treat all fields as absent, got %s", result)
	}
}

func TestFields_NestedValues(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"config": {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"nested": structpb.NewStringValue("value"),
						},
					},
				},
			},
		},
	}

	result := Fields(args, []string{"config"})
	if result != `[{"nested":"value"}]` {
		t.Fatalf("nested object not canonical: got %s", result)
	}
}

func TestCall_ConsistentHash(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path": structpb.NewStringValue("test.txt"),
			"data": structpb.NewStringValue("content"),
		},
	}

	h1 := Call("write", args)
	h2 := Call("write", args)

	if h1 != h2 {
		t.Fatalf("identical calls should produce identical hashes: %s != %s", h1, h2)
	}
}

func TestCall_DifferentToolName(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path": structpb.NewStringValue("test.txt"),
		},
	}

	h1 := Call("write", args)
	h2 := Call("read", args)

	if h1 == h2 {
		t.Fatalf("different tool names should produce different hashes")
	}
}

func TestCall_DifferentArgs(t *testing.T) {
	t.Parallel()

	args1 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path": structpb.NewStringValue("a.txt"),
		},
	}

	args2 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path": structpb.NewStringValue("b.txt"),
		},
	}

	h1 := Call("write", args1)
	h2 := Call("write", args2)

	if h1 == h2 {
		t.Fatalf("different args should produce different hashes")
	}
}

func TestCall_KeyOrderIndependence(t *testing.T) {
	t.Parallel()

	// Build the same args two different ways: field insertion order differs.
	args1 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"a": structpb.NewNumberValue(1),
			"b": structpb.NewStringValue("hello"),
			"c": structpb.NewBoolValue(true),
		},
	}

	args2 := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"c": structpb.NewBoolValue(true),
			"b": structpb.NewStringValue("hello"),
			"a": structpb.NewNumberValue(1),
		},
	}

	h1 := Call("test", args1)
	h2 := Call("test", args2)

	if h1 != h2 {
		t.Fatalf("field insertion order should not affect hash: %s != %s", h1, h2)
	}
}

func TestCall_HashFormat(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"x": structpb.NewNumberValue(1),
		},
	}

	hash := Call("tool", args)

	// SHA-256 produces 32 bytes = 64 hex chars.
	if len(hash) != 64 {
		t.Fatalf("hash should be 64 hex chars (SHA-256), got %d", len(hash))
	}

	// Verify it's valid hex.
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Fatalf("hash contains non-hex character: %c", c)
		}
	}
}

func TestCall_NilArgs(t *testing.T) {
	t.Parallel()

	h1 := Call("tool", nil)
	h2 := Call("tool", &structpb.Struct{Fields: make(map[string]*structpb.Value)})

	if h1 != h2 {
		t.Fatalf("nil args and empty struct should produce same hash")
	}
}

func TestCanonical_ComplexNesting(t *testing.T) {
	t.Parallel()

	// Test a deeply nested structure.
	s := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"z": structpb.NewNumberValue(1),
			"a": {
				Kind: &structpb.Value_StructValue{
					StructValue: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"array": {
								Kind: &structpb.Value_ListValue{
									ListValue: &structpb.ListValue{
										Values: []*structpb.Value{
											structpb.NewNumberValue(1),
											{
												Kind: &structpb.Value_StructValue{
													StructValue: &structpb.Struct{
														Fields: map[string]*structpb.Value{
															"nested": structpb.NewStringValue("deep"),
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	c := Canonical(&structpb.Value{Kind: &structpb.Value_StructValue{StructValue: s}})
	expected := `{"a":{"array":[1,{"nested":"deep"}]},"z":1}`
	if string(c) != expected {
		t.Fatalf("complex nested structure canonical wrong:\ngot:  %s\nwant: %s", c, expected)
	}
}

func TestFields_MultipleKeyFields(t *testing.T) {
	t.Parallel()

	args := &structpb.Struct{
		Fields: map[string]*structpb.Value{
			"path":  structpb.NewStringValue("file.txt"),
			"mode":  structpb.NewStringValue("write"),
			"other": structpb.NewStringValue("ignored"),
		},
	}

	result := Fields(args, []string{"path", "mode"})
	expected := `["file.txt","write"]`
	if result != expected {
		t.Fatalf("multiple key fields wrong: got %s, want %s", result, expected)
	}
}
