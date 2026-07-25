# schemavalidate

Pure-domain JSON-Schema validator for the project's restricted subset.

## Implementation notes

- **Type dispatch on `schema.Type`** — the proto-generated SchemaType enum.
- **No type coercion** — a value must already be the correct protobuf kind (StringValue for string, NumberValue for number, etc.).
- **Required field checking** — only for OBJECT type; validation iterates `schema.Required` and checks each is present in `value.StructValue.Fields`.
- **Enum validation** — only for STRING type; value is checked against `schema.EnumValues` for membership.
- **Recursive validation** — OBJECT properties and ARRAY items are validated recursively, preserving the error message chain with field/index context.
- **Nil handling** — a nil value is an error for any typed schema; nil schema or unspecified type is "no constraint" and accepts anything.
- **Error wrapping** — every validation failure wraps `ErrValidation` via `fmt.Errorf(...%w`, allowing callers to use `errors.Is` for type checking.

## Testing strategy

- **Table-driven tests** for each type (string, number, boolean, array, object).
- **Type mismatch tests** — e.g., passing a number when string is expected.
- **Constraint tests** — required fields, enum values, nested objects, arrays of objects.
- **Edge cases** — empty string, zero, empty array, empty object, nil values.
- **Nested validation** — objects containing objects, arrays of objects, to ensure recursive validation works.
- **Fuzz test** — arbitrary input against fixed schemas to ensure no panics.

Coverage target: ~95% (all type branches, all constraint kinds, error paths).
