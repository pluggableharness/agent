// Package schema is the plugin-author-facing builder layer over
// pluggableharness.schema.v1.Schema (pkg/schema/proto/v1), the restricted
// JSON-Schema subset every tool provider's ToolSchema.input_schema/
// output_schema (docs/specifications/tool/protocol.md#getschema) and every
// slashcommand provider's SlashCommandSpec.input_schema
// (docs/specifications/slashcommand/data-types.md) is written in. The
// subset is defined once, canonically, in
// docs/specifications/model/data-types.md#tool-schema — tool/protocol.md
// and slashcommand/data-types.md both point back to that section rather
// than redefining it, and this package follows the same rule: the subset
// is described here for context, but the spec section is ground truth.
//
// # The supported subset
//
// Every adapter MUST support exactly these JSON-Schema keywords, and no
// others:
//
//   - type — one of object, string, number, boolean, array. There is no
//     dedicated integer type: model/data-types.md#tool-schema and the
//     generated SchemaType enum both fold integer into number ("the
//     subset does not distinguish the two"). Integer in this package is a
//     thin, wire-identical alias of Number, kept for authors who want to
//     express integer intent in Go source even though nothing on the wire
//     records that intent.
//   - properties — OBJECT only: named sub-schemas.
//   - required — OBJECT only: which of properties' keys are mandatory.
//     Every name MUST already be a key of properties; Object returns an
//     error otherwise.
//   - enum — STRING only: constrains the value to one of a fixed set of
//     strings.
//   - items — ARRAY only: the schema every element MUST satisfy. Array
//     returns an error if items is nil, since an ARRAY node without one is
//     not representable per model/data-types.md#tool-schema.
//   - description — every node: human-readable text shown to the model
//     during tool selection and in plan-diff UI.
//
// Deliberately absent, and NOT exposed by any builder in this package
// because the generated Schema message has no field for them: oneOf,
// anyOf, allOf, $ref, pattern (regex constraints), format, and non-trivial
// additionalProperties schemas. A tool or slashcommand author cannot reach
// for these through this package — there is no builder call that produces
// them — which is the enforcement mechanism: the restriction lives in the
// Go type system, not in runtime validation.
//
// # Usage
//
// Every builder returns *schemav1.Schema (or, for Object and Array, whose
// arguments can be invalid in ways the type system does not otherwise
// catch, (*schemav1.Schema, error)). Object's properties compose from
// nested builder calls, including Array-of-Object, the same way a
// hand-written JSON Schema document nests:
//
//	itemSchema, err := schema.Object(map[string]*schemav1.Schema{
//		"name": schema.String(schema.WithDescription("display name")),
//	}, schema.WithRequired("name"))
//
//	listSchema, err := schema.Array(itemSchema, schema.WithDescription("items"))
package schema
