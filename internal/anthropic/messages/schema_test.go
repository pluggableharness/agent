package messages

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// decodeSchemaJSON unmarshals raw into a generic map for structural
// comparison against a hand-built expectation, rather than asserting on
// exact bytes — key order is already guaranteed deterministic by
// encoding/json's own map-key sort, so a structural comparison is both
// sufficient and less brittle here than a literal string match.
func decodeSchemaJSON(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal schema JSON: %v", err)
	}
	return m
}

func TestSchemaToJSON_nilSchema(t *testing.T) {
	t.Parallel()

	got, err := schemaToJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	if got := decodeSchemaJSON(t, got); !reflect.DeepEqual(got, want) {
		t.Fatalf("nil schema: got %#v, want %#v", got, want)
	}
}

func TestSchemaToJSON_typeMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *schemav1.Schema
		want map[string]any
	}{
		{
			name: "object with no properties",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT},
			want: map[string]any{"type": "object"},
		},
		{
			name: "string",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
			want: map[string]any{"type": "string"},
		},
		{
			name: "number",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
			want: map[string]any{"type": "number"},
		},
		{
			name: "boolean",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN},
			want: map[string]any{"type": "boolean"},
		},
		{
			name: "array with no items",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY},
			want: map[string]any{"type": "array"},
		},
		{
			name: "description",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, Description: "a name"},
			want: map[string]any{"type": "string", "description": "a name"},
		},
		{
			name: "string with enum values",
			in:   &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, EnumValues: []string{"low", "medium", "high"}},
			want: map[string]any{"type": "string", "enum": []any{"low", "medium", "high"}},
		},
		{
			name: "object with properties and required",
			in: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"path": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
				},
				Required: []string{"path"},
			},
			want: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []any{"path"},
			},
		},
		{
			name: "array with items",
			in: &schemav1.Schema{
				Type:  schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
			},
			want: map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := schemaToJSON(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := decodeSchemaJSON(t, raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSchemaToJSON_nesting(t *testing.T) {
	t.Parallel()

	in := &schemav1.Schema{
		Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Properties: map[string]*schemav1.Schema{
			"files": {
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
					Properties: map[string]*schemav1.Schema{
						"path":  {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
						"lines": {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
					},
					Required: []string{"path"},
				},
			},
		},
		Required: []string{"files"},
	}

	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":  map[string]any{"type": "string"},
						"lines": map[string]any{"type": "number"},
					},
					"required": []any{"path"},
				},
			},
		},
		"required": []any{"files"},
	}

	raw, err := schemaToJSON(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := decodeSchemaJSON(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestSchemaToJSON_unspecifiedTypeErrors(t *testing.T) {
	t.Parallel()

	if _, err := schemaToJSON(&schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED}); err == nil {
		t.Fatal("expected an error for SCHEMA_TYPE_UNSPECIFIED, got nil")
	}
}

func TestSchemaToJSON_nestedInvalidTypePropagates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   *schemav1.Schema
	}{
		{
			name: "invalid property type",
			in: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"bad": {Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED},
				},
			},
		},
		{
			name: "invalid array items type",
			in: &schemav1.Schema{
				Type:  schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := schemaToJSON(tc.in); err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

// TestSchemaToJSON_isByteIdenticalAcrossRuns exists to stop a future edit
// from reintroducing protojson (or any other marshaler that doesn't sort
// map keys) into schemaToJSON. schemav1.Schema.Properties is a proto
// map<string, Schema>, which decodes into a Go map with randomized
// iteration order — schemaToJSON is only deterministic because it builds a
// native map[string]any tree and lets encoding/json sort the keys during
// marshaling. If this test ever starts failing, the fix is in
// schemaToJSON's marshaler, never in the test: the real-world failure mode
// of non-deterministic tool-schema bytes is a silently and permanently
// disabled Anthropic prompt cache, discovered only in a bill weeks later.
func TestSchemaToJSON_isByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()

	// Deliberately not in alphabetical order, and nested two levels deep
	// (each top-level property is itself an object with its own
	// sub-properties), so a naive unsorted marshaler would show it.
	keys := []string{
		"zulu", "yankee", "xray", "whiskey", "victor",
		"uniform", "tango", "sierra", "romeo", "quebec", "papa",
	}
	props := make(map[string]*schemav1.Schema, len(keys))
	for i, k := range keys {
		props[k] = &schemav1.Schema{
			Type:        schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
			Description: fmt.Sprintf("field %d", i),
			Properties: map[string]*schemav1.Schema{
				"inner_a": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
				"inner_b": {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
			},
			Required: []string{"inner_a"},
		}
	}
	root := &schemav1.Schema{
		Type:       schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Properties: props,
	}

	first, err := schemaToJSON(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range 100 {
		got, err := schemaToJSON(root)
		if err != nil {
			t.Fatalf("run %d: unexpected error: %v", i, err)
		}
		if string(got) != string(first) {
			t.Fatalf("run %d produced different bytes than run 0:\nrun 0: %s\nrun %d: %s", i, first, i, got)
		}
	}
}
