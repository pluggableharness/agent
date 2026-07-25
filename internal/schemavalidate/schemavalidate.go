package schemavalidate

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// ErrValidation is the sentinel every validation failure wraps via
// errors.Is; wrap with fmt.Errorf("schemavalidate: %s: %w", <what
// failed>, ErrValidation) so a caller can distinguish "this value is
// invalid" from a programming error in this package itself.
var ErrValidation = errors.New("validation failed")

// Validate checks v against schema, per the common JSON-Schema subset
// this project supports (object/string/number/boolean/array/enum — no
// oneOf/$ref chains, no exotic keywords). Returns a wrapped
// ErrValidation naming the first violation found (missing required
// property, wrong type, value not in an enum's declared set, array item
// failing its own item schema) — do not attempt to collect every
// violation, the first is sufficient for a caller to reject the value.
// A schema of unspecified/unknown type is treated as "no constraint" —
// log nothing (this package never logs, it's pure), just accept
// anything.
func Validate(v *structpb.Value, schema *schemav1.Schema) error {
	if schema == nil || schema.Type == schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED {
		// No constraint; accept anything.
		return nil
	}

	return validateValue(v, schema)
}

func validateValue(v *structpb.Value, schema *schemav1.Schema) error {
	if v == nil {
		return fmt.Errorf("schemavalidate: value is nil: %w", ErrValidation)
	}

	switch schema.Type {
	case schemav1.SchemaType_SCHEMA_TYPE_OBJECT:
		return validateObject(v, schema)
	case schemav1.SchemaType_SCHEMA_TYPE_STRING:
		return validateString(v, schema)
	case schemav1.SchemaType_SCHEMA_TYPE_NUMBER:
		return validateNumber(v, schema)
	case schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN:
		return validateBoolean(v, schema)
	case schemav1.SchemaType_SCHEMA_TYPE_ARRAY:
		return validateArray(v, schema)
	case schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED:
		return nil
	default:
		// Unknown type; accept anything.
		return nil
	}
}

func validateObject(v *structpb.Value, schema *schemav1.Schema) error {
	structVal := v.GetStructValue()
	if structVal == nil {
		return fmt.Errorf("schemavalidate: expected object, got %T: %w", v.Kind, ErrValidation)
	}

	fields := structVal.GetFields()
	if fields == nil {
		fields = make(map[string]*structpb.Value)
	}

	// Check required fields exist.
	for _, req := range schema.GetRequired() {
		if _, ok := fields[req]; !ok {
			return fmt.Errorf("schemavalidate: required field %q is missing: %w", req, ErrValidation)
		}
	}

	// Validate each property that is present against its schema.
	for name, val := range fields {
		propSchema, ok := schema.GetProperties()[name]
		if !ok {
			// Property not in schema. For objects without explicit
			// additionalProperties, we don't validate unknown properties.
			// This is by design per the subset: no additionalProperties
			// constraints are supported.
			continue
		}

		if err := validateValue(val, propSchema); err != nil {
			return fmt.Errorf("schemavalidate: property %q: %w", name, err)
		}
	}

	return nil
}

func validateString(v *structpb.Value, schema *schemav1.Schema) error {
	// Check if the value is actually a string type in the structpb.Value.
	if _, ok := v.Kind.(*structpb.Value_StringValue); !ok {
		return fmt.Errorf("schemavalidate: expected string, got %T: %w", v.Kind, ErrValidation)
	}

	strVal := v.GetStringValue()

	// Check enum constraint if present.
	enumValues := schema.GetEnumValues()
	if len(enumValues) > 0 {
		found := false
		for _, ev := range enumValues {
			if strVal == ev {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("schemavalidate: string value %q not in enum %v: %w", strVal, enumValues, ErrValidation)
		}
	}

	return nil
}

func validateNumber(v *structpb.Value, _ *schemav1.Schema) error {
	if _, ok := v.Kind.(*structpb.Value_NumberValue); !ok {
		return fmt.Errorf("schemavalidate: expected number, got %T: %w", v.Kind, ErrValidation)
	}
	return nil
}

func validateBoolean(v *structpb.Value, _ *schemav1.Schema) error {
	if _, ok := v.Kind.(*structpb.Value_BoolValue); !ok {
		return fmt.Errorf("schemavalidate: expected boolean, got %T: %w", v.Kind, ErrValidation)
	}
	return nil
}

func validateArray(v *structpb.Value, schema *schemav1.Schema) error {
	listVal := v.GetListValue()
	if listVal == nil {
		return fmt.Errorf("schemavalidate: expected array, got %T: %w", v.Kind, ErrValidation)
	}

	itemSchema := schema.GetItems()
	if itemSchema == nil {
		// Array schema must have items defined per the spec.
		// Treat a missing items schema as an error in the schema itself,
		// but for robustness, accept all items.
		return nil
	}

	values := listVal.GetValues()
	for i, item := range values {
		if err := validateValue(item, itemSchema); err != nil {
			return fmt.Errorf("schemavalidate: array element %d: %w", i, err)
		}
	}

	return nil
}
