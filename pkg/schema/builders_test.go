package schema_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/pluggableharness/agent/pkg/schema"
	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// TestString asserts String produces a STRING node carrying the
// description and enum values from the given options.
func TestString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []schema.Option
		want *schemav1.Schema
	}{
		{
			name: "no options",
			opts: nil,
			want: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING},
		},
		{
			name: "description",
			opts: []schema.Option{schema.WithDescription("a name")},
			want: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, Description: "a name"},
		},
		{
			name: "enum",
			opts: []schema.Option{schema.WithEnum("red", "green", "blue")},
			want: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, EnumValues: []string{"red", "green", "blue"}},
		},
		{
			name: "description and enum",
			opts: []schema.Option{schema.WithDescription("a color"), schema.WithEnum("red", "green")},
			want: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, Description: "a color", EnumValues: []string{"red", "green"}},
		},
		{
			name: "nil option is a no-op",
			opts: []schema.Option{nil, schema.WithDescription("still set")},
			want: &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_STRING, Description: "still set"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := schema.String(tt.opts...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("String(%v) = %+v, want %+v", tt.opts, got, tt.want)
			}
		})
	}
}

// TestEnum asserts Enum is equivalent sugar for String(WithEnum(...)).
func TestEnum(t *testing.T) {
	t.Parallel()

	got := schema.Enum("a", "b", "c")
	want := schema.String(schema.WithEnum("a", "b", "c"))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Enum(a, b, c) = %+v, want %+v", got, want)
	}
	if got.GetType() != schemav1.SchemaType_SCHEMA_TYPE_STRING {
		t.Errorf("Enum(...).Type = %v, want SCHEMA_TYPE_STRING", got.GetType())
	}
}

// TestNumber asserts Number produces a NUMBER node.
func TestNumber(t *testing.T) {
	t.Parallel()

	got := schema.Number(schema.WithDescription("a count"))
	want := &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_NUMBER, Description: "a count"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Number(...) = %+v, want %+v", got, want)
	}
}

// TestIntegerAliasesNumber asserts Integer is wire-identical to Number,
// since the generated SchemaType enum has no dedicated integer value.
func TestIntegerAliasesNumber(t *testing.T) {
	t.Parallel()

	got := schema.Integer(schema.WithDescription("a count"))
	want := schema.Number(schema.WithDescription("a count"))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Integer(...) = %+v, want %+v (same as Number)", got, want)
	}
}

// TestBoolean asserts Boolean produces a BOOLEAN node.
func TestBoolean(t *testing.T) {
	t.Parallel()

	got := schema.Boolean(schema.WithDescription("a flag"))
	want := &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN, Description: "a flag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Boolean(...) = %+v, want %+v", got, want)
	}
}

// TestBooleanIgnoresEnumOption asserts an inapplicable option (WithEnum on
// a non-String builder) is a silent no-op, per the Option doc comment,
// rather than corrupting the produced node.
func TestBooleanIgnoresEnumOption(t *testing.T) {
	t.Parallel()

	got := schema.Boolean(schema.WithEnum("yes", "no"))
	want := &schemav1.Schema{Type: schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Boolean(WithEnum(...)) = %+v, want %+v (enum ignored)", got, want)
	}
}

// TestArray asserts Array wraps an items schema and rejects a nil items
// argument, since an ARRAY node without an element schema is not
// representable per model/data-types.md#tool-schema.
func TestArray(t *testing.T) {
	t.Parallel()

	t.Run("valid items", func(t *testing.T) {
		t.Parallel()

		items := schema.String()
		got, err := schema.Array(items, schema.WithDescription("a list of strings"))
		if err != nil {
			t.Fatalf("Array(items, ...) returned error: %v", err)
		}
		want := &schemav1.Schema{
			Type:        schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
			Description: "a list of strings",
			Items:       items,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Array(items, ...) = %+v, want %+v", got, want)
		}
	})

	t.Run("nil items", func(t *testing.T) {
		t.Parallel()

		got, err := schema.Array(nil)
		if err == nil {
			t.Fatalf("Array(nil) returned nil error, want error")
		}
		if got != nil {
			t.Errorf("Array(nil) = %+v, want nil on error", got)
		}
	})
}

// TestObject asserts Object attaches the given properties and, via
// WithRequired, a validated required list.
func TestObject(t *testing.T) {
	t.Parallel()

	t.Run("properties and required", func(t *testing.T) {
		t.Parallel()

		props := map[string]*schemav1.Schema{
			"name": schema.String(schema.WithDescription("display name")),
			"age":  schema.Integer(),
		}
		got, err := schema.Object(props, schema.WithDescription("a person"), schema.WithRequired("name"))
		if err != nil {
			t.Fatalf("Object(...) returned error: %v", err)
		}
		want := &schemav1.Schema{
			Type:        schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
			Description: "a person",
			Properties:  props,
			Required:    []string{"name"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Object(...) = %+v, want %+v", got, want)
		}
	})

	t.Run("no options", func(t *testing.T) {
		t.Parallel()

		props := map[string]*schemav1.Schema{"name": schema.String()}
		got, err := schema.Object(props)
		if err != nil {
			t.Fatalf("Object(props) returned error: %v", err)
		}
		if got.GetType() != schemav1.SchemaType_SCHEMA_TYPE_OBJECT {
			t.Errorf("Object(props).Type = %v, want SCHEMA_TYPE_OBJECT", got.GetType())
		}
		if len(got.GetRequired()) != 0 {
			t.Errorf("Object(props).Required = %v, want empty", got.GetRequired())
		}
	})

	t.Run("required name absent from properties", func(t *testing.T) {
		t.Parallel()

		props := map[string]*schemav1.Schema{"name": schema.String()}
		got, err := schema.Object(props, schema.WithRequired("nonexistent"))
		if err == nil {
			t.Fatalf("Object(...) returned nil error, want error for unknown required name")
		}
		if got != nil {
			t.Errorf("Object(...) = %+v, want nil on error", got)
		}
	})

	t.Run("nil property value", func(t *testing.T) {
		t.Parallel()

		props := map[string]*schemav1.Schema{"broken": nil}
		got, err := schema.Object(props)
		if err == nil {
			t.Fatalf("Object(...) returned nil error, want error for nil property")
		}
		if got != nil {
			t.Errorf("Object(...) = %+v, want nil on error", got)
		}
	})
}

// TestObjectArrayOfObjectComposition builds a realistic nested schema —
// an object with an array-of-object property — to prove builder
// composition works end to end, the way a real tool's input_schema would
// be assembled.
func TestObjectArrayOfObjectComposition(t *testing.T) {
	t.Parallel()

	item, err := schema.Object(map[string]*schemav1.Schema{
		"id":   schema.String(schema.WithDescription("item ID")),
		"tags": schema.String(schema.WithEnum("urgent", "normal", "low")),
	}, schema.WithRequired("id"))
	if err != nil {
		t.Fatalf("Object(item) returned error: %v", err)
	}

	items, err := schema.Array(item, schema.WithDescription("the work items"))
	if err != nil {
		t.Fatalf("Array(item) returned error: %v", err)
	}

	root, err := schema.Object(map[string]*schemav1.Schema{
		"title": schema.String(schema.WithDescription("batch title")),
		"items": items,
		"count": schema.Integer(schema.WithDescription("expected item count")),
	}, schema.WithDescription("a batch of work items"), schema.WithRequired("title", "items"))
	if err != nil {
		t.Fatalf("Object(root) returned error: %v", err)
	}

	if root.GetType() != schemav1.SchemaType_SCHEMA_TYPE_OBJECT {
		t.Fatalf("root.Type = %v, want SCHEMA_TYPE_OBJECT", root.GetType())
	}
	wantRequired := []string{"title", "items"}
	if !reflect.DeepEqual(root.GetRequired(), wantRequired) {
		t.Errorf("root.Required = %v, want %v", root.GetRequired(), wantRequired)
	}

	gotItems := root.GetProperties()["items"]
	if gotItems.GetType() != schemav1.SchemaType_SCHEMA_TYPE_ARRAY {
		t.Fatalf("root.Properties[items].Type = %v, want SCHEMA_TYPE_ARRAY", gotItems.GetType())
	}
	gotItem := gotItems.GetItems()
	if gotItem.GetType() != schemav1.SchemaType_SCHEMA_TYPE_OBJECT {
		t.Fatalf("root.Properties[items].Items.Type = %v, want SCHEMA_TYPE_OBJECT", gotItem.GetType())
	}
	if !reflect.DeepEqual(gotItem.GetRequired(), []string{"id"}) {
		t.Errorf("item.Required = %v, want [id]", gotItem.GetRequired())
	}
	gotTags := gotItem.GetProperties()["tags"]
	wantTags := []string{"urgent", "normal", "low"}
	if !reflect.DeepEqual(gotTags.GetEnumValues(), wantTags) {
		t.Errorf("item.Properties[tags].EnumValues = %v, want %v", gotTags.GetEnumValues(), wantTags)
	}

	gotCount := root.GetProperties()["count"]
	if gotCount.GetType() != schemav1.SchemaType_SCHEMA_TYPE_NUMBER {
		t.Errorf("root.Properties[count].Type = %v, want SCHEMA_TYPE_NUMBER", gotCount.GetType())
	}
}

// TestObjectErrorsAreWrapped asserts Object's error messages carry the
// "schema: object:" prefix this package's error-wrapping convention
// requires, and are plain errors (not sentinels) since each failure
// carries call-specific data (a name) rather than being a fixed condition.
func TestObjectErrorsAreWrapped(t *testing.T) {
	t.Parallel()

	_, err := schema.Object(map[string]*schemav1.Schema{"a": schema.String()}, schema.WithRequired("missing"))
	if err == nil {
		t.Fatalf("Object(...) returned nil error, want error")
	}
	if got, want := err.Error(), "schema: object: "; len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("Object(...) error = %q, want prefix %q", got, want)
	}
	// Sanity: errors.Is against an unrelated sentinel correctly reports false
	// rather than panicking, confirming this is an ordinary %w-wrapped error.
	if errors.Is(err, errArbitrarySentinel) {
		t.Errorf("errors.Is(err, unrelated sentinel) = true, want false")
	}
}

var errArbitrarySentinel = errors.New("unrelated")
