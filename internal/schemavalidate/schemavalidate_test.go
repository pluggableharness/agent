package schemavalidate

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

func TestValidateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name:  "valid string",
			value: structpb.NewStringValue("hello"),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
			},
			wantErr: false,
		},
		{
			name:  "empty string",
			value: structpb.NewStringValue(""),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
			},
			wantErr: false,
		},
		{
			name:  "string with enum constraint, value in set",
			value: structpb.NewStringValue("red"),
			schema: &schemav1.Schema{
				Type:       schemav1.SchemaType_SCHEMA_TYPE_STRING,
				EnumValues: []string{"red", "green", "blue"},
			},
			wantErr: false,
		},
		{
			name:  "string with enum constraint, value not in set",
			value: structpb.NewStringValue("yellow"),
			schema: &schemav1.Schema{
				Type:       schemav1.SchemaType_SCHEMA_TYPE_STRING,
				EnumValues: []string{"red", "green", "blue"},
			},
			wantErr: true,
		},
		{
			name:  "wrong type: number when string expected",
			value: structpb.NewNumberValue(42),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
			},
			wantErr: true,
		},
		{
			name:  "wrong type: boolean when string expected",
			value: structpb.NewBoolValue(true),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name:  "valid positive number",
			value: structpb.NewNumberValue(42),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
			},
			wantErr: false,
		},
		{
			name:  "valid negative number",
			value: structpb.NewNumberValue(-3.14),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
			},
			wantErr: false,
		},
		{
			name:  "valid zero",
			value: structpb.NewNumberValue(0),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
			},
			wantErr: false,
		},
		{
			name:  "wrong type: string when number expected",
			value: structpb.NewStringValue("42"),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateBoolean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name:  "valid true",
			value: structpb.NewBoolValue(true),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN,
			},
			wantErr: false,
		},
		{
			name:  "valid false",
			value: structpb.NewBoolValue(false),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN,
			},
			wantErr: false,
		},
		{
			name:  "wrong type: string when boolean expected",
			value: structpb.NewStringValue("true"),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN,
			},
			wantErr: true,
		},
		{
			name:  "wrong type: number when boolean expected",
			value: structpb.NewNumberValue(1),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name: "valid array of strings",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewStringValue("a"),
					structpb.NewStringValue("b"),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
				},
			},
			wantErr: false,
		},
		{
			name: "empty array",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
				},
			},
			wantErr: false,
		},
		{
			name: "array with wrong element type",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewStringValue("a"),
					structpb.NewNumberValue(42),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
				},
			},
			wantErr: true,
		},
		{
			name: "array of numbers",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewNumberValue(1),
					structpb.NewNumberValue(2),
					structpb.NewNumberValue(3),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
				},
			},
			wantErr: false,
		},
		{
			name:  "wrong type: string when array expected",
			value: structpb.NewStringValue("[1,2,3]"),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name: "valid object",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name": structpb.NewStringValue("Alice"),
					"age":  structpb.NewNumberValue(30),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					"age":  {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
				},
				Required: []string{"name", "age"},
			},
			wantErr: false,
		},
		{
			name: "object missing required field",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name": structpb.NewStringValue("Alice"),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					"age":  {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
				},
				Required: []string{"name", "age"},
			},
			wantErr: true,
		},
		{
			name: "object with extra field",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name":  structpb.NewStringValue("Alice"),
					"email": structpb.NewStringValue("alice@example.com"),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
				},
				Required: []string{"name"},
			},
			wantErr: false,
		},
		{
			name: "object with wrong property type",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name": structpb.NewNumberValue(42),
					"age":  structpb.NewNumberValue(30),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					"age":  {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
				},
				Required: []string{"name", "age"},
			},
			wantErr: true,
		},
		{
			name:  "wrong type: string when object expected",
			value: structpb.NewStringValue(`{"name":"Alice"}`),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
				},
				Required: []string{"name"},
			},
			wantErr: true,
		},
		{
			name: "empty object with no required fields",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{},
			}),
			schema: &schemav1.Schema{
				Type:       schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{},
				Required:   []string{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateNestedObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name: "valid nested object",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name": structpb.NewStringValue("Alice"),
					"address": structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"street": structpb.NewStringValue("123 Main St"),
							"city":   structpb.NewStringValue("Wonderland"),
						},
					}),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					"address": {
						Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
						Properties: map[string]*schemav1.Schema{
							"street": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
							"city":   {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
						},
						Required: []string{"street", "city"},
					},
				},
				Required: []string{"name", "address"},
			},
			wantErr: false,
		},
		{
			name: "nested object missing required field",
			value: structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"name": structpb.NewStringValue("Alice"),
					"address": structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"street": structpb.NewStringValue("123 Main St"),
						},
					}),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					"address": {
						Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
						Properties: map[string]*schemav1.Schema{
							"street": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
							"city":   {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
						},
						Required: []string{"street", "city"},
					},
				},
				Required: []string{"name", "address"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateArrayOfObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *structpb.Value
		schema  *schemav1.Schema
		wantErr bool
	}{
		{
			name: "valid array of objects",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"id":   structpb.NewNumberValue(1),
							"name": structpb.NewStringValue("Alice"),
						},
					}),
					structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"id":   structpb.NewNumberValue(2),
							"name": structpb.NewStringValue("Bob"),
						},
					}),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
					Properties: map[string]*schemav1.Schema{
						"id":   {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
						"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					},
					Required: []string{"id", "name"},
				},
			},
			wantErr: false,
		},
		{
			name: "array of objects with invalid item",
			value: structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"id":   structpb.NewNumberValue(1),
							"name": structpb.NewStringValue("Alice"),
						},
					}),
					structpb.NewStructValue(&structpb.Struct{
						Fields: map[string]*structpb.Value{
							"id": structpb.NewNumberValue(2),
						},
					}),
				},
			}),
			schema: &schemav1.Schema{
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
					Properties: map[string]*schemav1.Schema{
						"id":   {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
						"name": {Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
					},
					Required: []string{"id", "name"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !errors.Is(err, ErrValidation) {
					t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
				}
			}
		})
	}
}

func TestValidateUnspecifiedSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  *structpb.Value
		schema *schemav1.Schema
	}{
		{
			name:   "unspecified schema accepts string",
			value:  structpb.NewStringValue("hello"),
			schema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED},
		},
		{
			name:   "unspecified schema accepts number",
			value:  structpb.NewNumberValue(42),
			schema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED},
		},
		{
			name:   "unspecified schema accepts object",
			value:  structpb.NewStructValue(&structpb.Struct{}),
			schema: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_UNSPECIFIED},
		},
		{
			name:   "nil schema accepts anything",
			value:  structpb.NewStringValue("hello"),
			schema: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.value, tt.schema)
			if err != nil {
				t.Errorf("Validate() expected no error, got %v", err)
			}
		})
	}
}

func TestValidateNilValue(t *testing.T) {
	t.Parallel()

	schema := &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING}
	err := Validate(nil, schema)
	if err == nil {
		t.Error("Validate() expected error for nil value")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("Validate() error does not wrap ErrValidation: %v", err)
	}
}

func TestValidateSatisfiesAllConstraints(t *testing.T) {
	t.Parallel()

	// Complex schema with nested objects, arrays, enums, and required fields.
	schema := &schemav1.Schema{
		Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Properties: map[string]*schemav1.Schema{
			"name": {
				Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
			},
			"status": {
				Type:       schemav1.SchemaType_SCHEMA_TYPE_STRING,
				EnumValues: []string{"active", "inactive", "pending"},
			},
			"tags": {
				Type: schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
				Items: &schemav1.Schema{
					Type: schemav1.SchemaType_SCHEMA_TYPE_STRING,
				},
			},
			"metadata": {
				Type: schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
				Properties: map[string]*schemav1.Schema{
					"version": {Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER},
				},
				Required: []string{"version"},
			},
		},
		Required: []string{"name", "status"},
	}

	value := structpb.NewStructValue(&structpb.Struct{
		Fields: map[string]*structpb.Value{
			"name":   structpb.NewStringValue("example"),
			"status": structpb.NewStringValue("active"),
			"tags": structpb.NewListValue(&structpb.ListValue{
				Values: []*structpb.Value{
					structpb.NewStringValue("tag1"),
					structpb.NewStringValue("tag2"),
				},
			}),
			"metadata": structpb.NewStructValue(&structpb.Struct{
				Fields: map[string]*structpb.Value{
					"version": structpb.NewNumberValue(1),
				},
			}),
		},
	})

	err := Validate(value, schema)
	if err != nil {
		t.Errorf("Validate() expected no error, got %v", err)
	}
}
