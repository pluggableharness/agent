package messages

import (
	"encoding/json"
	"fmt"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// schemaTypeJSON maps a schemav1.SchemaType to the JSON Schema "type"
// keyword Anthropic's tool input_schema field expects.
var schemaTypeJSON = map[schemav1.SchemaType]string{
	schemav1.SchemaType_SCHEMA_TYPE_OBJECT:  "object",
	schemav1.SchemaType_SCHEMA_TYPE_STRING:  "string",
	schemav1.SchemaType_SCHEMA_TYPE_NUMBER:  "number",
	schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN: "boolean",
	schemav1.SchemaType_SCHEMA_TYPE_ARRAY:   "array",
}

// schemaToJSON converts s into the JSON Schema object Anthropic's tool
// input_schema field expects, as deterministic bytes. A nil s produces a
// valid empty object schema rather than an error — Anthropic requires an
// object schema even for a no-argument tool.
//
// The result is built as a tree of native Go maps/slices and marshaled with
// encoding/json rather than protojson: encoding/json sorts map[string]any
// keys during marshaling, which is what makes the properties object
// deterministic despite schemav1.Schema.Properties being a proto
// map<string, Schema> with random Go iteration order. protojson makes no
// such guarantee and, per this package's CLAUDE.md, is never used here —
// non-deterministic tool schema bytes would silently and permanently
// disable Anthropic's prompt cache from the first tool-bearing request
// onward.
func schemaToJSON(s *schemav1.Schema) (json.RawMessage, error) {
	if s == nil {
		return json.Marshal(map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		})
	}
	tree, err := schemaToTree(s)
	if err != nil {
		return nil, err
	}
	return json.Marshal(tree)
}

// schemaToTree recursively converts s into a native Go map, the shared
// building block schemaToJSON marshals for the top-level call and that
// schemaToTree itself calls for nested properties/items.
func schemaToTree(s *schemav1.Schema) (map[string]any, error) {
	typeName, ok := schemaTypeJSON[s.GetType()]
	if !ok {
		return nil, fmt.Errorf("schema: unsupported or unspecified type %v", s.GetType())
	}

	tree := map[string]any{"type": typeName}
	if d := s.GetDescription(); d != "" {
		tree["description"] = d
	}
	if props := s.GetProperties(); len(props) > 0 {
		propTree := make(map[string]any, len(props))
		for name, prop := range props {
			sub, err := schemaToTree(prop)
			if err != nil {
				return nil, err
			}
			propTree[name] = sub
		}
		tree["properties"] = propTree
	}
	if req := s.GetRequired(); len(req) > 0 {
		tree["required"] = req
	}
	if items := s.GetItems(); items != nil {
		sub, err := schemaToTree(items)
		if err != nil {
			return nil, err
		}
		tree["items"] = sub
	}
	if enum := s.GetEnumValues(); len(enum) > 0 {
		tree["enum"] = enum
	}
	return tree, nil
}
