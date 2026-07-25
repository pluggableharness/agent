package schemavalidate

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// FuzzValidate tests that the Validate function never panics when given
// arbitrary input, regardless of whether the input is valid or invalid.
func FuzzValidate(f *testing.F) {
	// Seed with some representative schemas and values.
	f.Add(int32(schemav1.SchemaType_SCHEMA_TYPE_STRING), "", 0.0)
	f.Add(int32(schemav1.SchemaType_SCHEMA_TYPE_NUMBER), "", 42.0)
	f.Add(int32(schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN), "", 1.0)
	f.Add(int32(schemav1.SchemaType_SCHEMA_TYPE_ARRAY), "", 0.0)
	f.Add(int32(schemav1.SchemaType_SCHEMA_TYPE_OBJECT), "", 0.0)

	f.Fuzz(func(_ *testing.T, schemaTypeInt int32, stringVal string, numVal float64) {
		// Create a schema with the fuzzed type.
		schema := &schemav1.Schema{
			Type: schemav1.SchemaType(schemaTypeInt),
		}

		// Create arbitrary values to test against.
		var values []*structpb.Value
		values = append(values,
			structpb.NewStringValue(stringVal),
			structpb.NewNumberValue(numVal),
			structpb.NewBoolValue(numVal > 0),
			structpb.NewListValue(&structpb.ListValue{}),
			structpb.NewStructValue(&structpb.Struct{}),
		)

		for _, val := range values {
			// This should never panic, regardless of the input.
			_ = Validate(val, schema)
		}

		// Test with nil value.
		_ = Validate(nil, schema)

		// Test with nil schema.
		_ = Validate(structpb.NewStringValue(stringVal), nil)
	})
}
