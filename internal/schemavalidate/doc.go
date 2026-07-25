// Package schemavalidate validates already-parsed JSON values against the
// project's common JSON-Schema subset (pluggableharness.schema.v1.Schema,
// defined in pkg/schema/proto/v1).
//
// It implements the three validation MUSTs elsewhere in the kernel:
//   - strict output_schema enforcement on tool results (docs/specifications/tool/protocol.md#invoke)
//   - corrected_input re-validation on a plan decision (docs/specifications/frontend/frontend-protocol.md#plan_decisioncorrected_input)
//   - tool-call input validation before Invoke
//
// The validator supports the following schema types and keywords, per
// docs/specifications/tool/data-types.md and docs/specifications/model/data-types.md#tool-schema:
//   - type: object, string, number, boolean, array (no int/float distinction)
//   - properties: named sub-schemas (OBJECT only)
//   - required: which of properties' keys are mandatory (OBJECT only)
//   - enum_values: constrains to one of a fixed set of strings (STRING only)
//   - items: the schema every array element must satisfy (ARRAY only)
//   - description: human-readable text (every node; not validated)
//
// Unsupported keywords (oneOf, anyOf, allOf, $ref, pattern, format, etc.)
// have no representation in the Schema protobuf and therefore no way to
// appear in a value being validated.
//
// Validation returns an error wrapping ErrValidation on the first violation
// found (missing required property, wrong type, value not in an enum's
// declared set, array item failing its own item schema). An unspecified or
// nil schema is treated as "no constraint" and accepts any value.
//
// # Pure domain logic
//
// This package is pure Go with no I/O and no logging (it never calls
// log/slog or imports internal/telemetry). It is deterministic and
// composable, suitable for use within the plan/apply decision boundary
// and replay-safe session operations.
package schemavalidate
