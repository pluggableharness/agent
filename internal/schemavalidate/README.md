# schemavalidate

Validates already-parsed JSON values against the project's common JSON-Schema subset.

## Usage

```go
import (
	"errors"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"github.com/pluggableharness/agent/internal/schemavalidate"
)

schema := &schemav1.Schema{
	Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
	Properties: map[string]*schemav1.Schema{
		"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
		"age":  {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
	},
	Required: []string{"name", "age"},
}

value := structpb.NewStructValue(&structpb.Struct{
	Fields: map[string]*structpb.Value{
		"name": structpb.NewStringValue("Alice"),
		"age":  structpb.NewNumberValue(30),
	},
})

if err := schemavalidate.Validate(value, schema); err != nil {
	if errors.Is(err, schemavalidate.ErrValidation) {
		// Value violates the schema constraints
		log.Printf("Invalid value: %v", err)
	}
	// Handle error
}
```

## Supported Schema Features

- **Types**: `object`, `string`, `number`, `boolean`, `array`
- **Constraints**:
  - Objects: `properties` (named sub-schemas), `required` (mandatory fields)
  - Strings: `enum_values` (fixed set of allowed strings)
  - Arrays: `items` (element schema, required)
  - All types: `description` (documentation, not validated)

## Unsupported Features

The following JSON Schema features are deliberately not supported and have no way to appear in a Schema value:

- `oneOf`, `anyOf`, `allOf` — no sum types beyond the root choice
- `$ref` — no schema reuse via reference
- `pattern` — no regex constraints
- `format` — no format hints like `date-time`, `email`, `uri`
- `additionalProperties` — no schema for properties not in `properties`
- Primitive type distinctions: `integer` is represented as `number`

## Error Handling

`Validate` returns an error on the first violation encountered. Every returned error wraps `ErrValidation`, which allows callers to distinguish validation failures from programming errors:

```go
err := schemavalidate.Validate(value, schema)
if errors.Is(err, schemavalidate.ErrValidation) {
	// Handle validation failure
}
```

The error message includes the path to the first failing constraint for debugging:

- `schemavalidate: expected string, got *structpb.Value_NumberValue: validation failed`
- `schemavalidate: property "address": schemavalidate: required field "street" is missing: validation failed`
- `schemavalidate: array element 2: schemavalidate: string value "unknown" not in enum [active inactive pending]: validation failed`

## Design Notes

- **No logging**: this package is pure domain logic with no I/O or side effects, suitable for use in the plan/apply gate.
- **Deterministic**: suitable for replay-safe session operations.
- **First-error-only**: does not attempt to collect all validation failures; the first is sufficient for the caller to reject the value.
- **Nil is error**: a nil value violates any non-unspecified schema; a nil schema (or unspecified type) is treated as "no constraint".
