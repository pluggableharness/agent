package config_test

import (
	"errors"
	"testing"

	"github.com/pluggableharness/agent/pkg/config"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

func TestAttribute_Valid(t *testing.T) {
	t.Parallel()

	nested, err := config.Attribute("port", configv1.AttrType_ATTR_TYPE_NUMBER, config.WithRequired())
	if err != nil {
		t.Fatalf("Attribute(port) = _, %v, want nil error", err)
	}

	tests := []struct {
		name string
		typ  configv1.AttrType
		opts []config.AttributeOption
	}{
		{
			name: "string with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_STRING,
			opts: []config.AttributeOption{config.WithDefault(`"us-east-1"`)},
		},
		{
			name: "number with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_NUMBER,
			opts: []config.AttributeOption{config.WithDefault("30")},
		},
		{
			name: "bool with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_BOOL,
			opts: []config.AttributeOption{config.WithDefault("true")},
		},
		{
			name: "list_string with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_LIST_STRING,
			opts: []config.AttributeOption{config.WithDefault(`["CLAUDE.md","**/CLAUDE.md"]`)},
		},
		{
			name: "list_number with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_LIST_NUMBER,
			opts: []config.AttributeOption{config.WithDefault("[1,2,3]")},
		},
		{
			name: "map_string with matching default",
			typ:  configv1.AttrType_ATTR_TYPE_MAP_STRING,
			opts: []config.AttributeOption{config.WithDefault(`{"a":"b"}`)},
		},
		{
			name: "required sensitive without default",
			typ:  configv1.AttrType_ATTR_TYPE_STRING,
			opts: []config.AttributeOption{config.WithRequired(), config.WithSensitive()},
		},
		{
			name: "description set",
			typ:  configv1.AttrType_ATTR_TYPE_STRING,
			opts: []config.AttributeOption{config.WithDescription("token budget in characters")},
		},
		{
			name: "object with nested attributes",
			typ:  configv1.AttrType_ATTR_TYPE_OBJECT,
			opts: []config.AttributeOption{config.WithObjectAttributes(nested)},
		},
		{
			name: "object with matching nested default",
			typ:  configv1.AttrType_ATTR_TYPE_OBJECT,
			opts: []config.AttributeOption{
				config.WithObjectAttributes(nested),
				config.WithDefault(`{"port":8080}`),
			},
		},
		{
			name: "object with partial nested default",
			typ:  configv1.AttrType_ATTR_TYPE_OBJECT,
			opts: []config.AttributeOption{
				config.WithObjectAttributes(nested),
				config.WithDefault(`{}`),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.Attribute("attr", tt.typ, tt.opts...)
			if err != nil {
				t.Fatalf("Attribute(%q, %v) = _, %v, want nil error", "attr", tt.typ, err)
			}
			if got.GetName() != "attr" {
				t.Errorf("GetName() = %q, want %q", got.GetName(), "attr")
			}
			if got.GetType() != tt.typ {
				t.Errorf("GetType() = %v, want %v", got.GetType(), tt.typ)
			}
		})
	}
}

func TestAttribute_UnspecifiedType(t *testing.T) {
	t.Parallel()

	_, err := config.Attribute("attr", configv1.AttrType_ATTR_TYPE_UNSPECIFIED)
	if !errors.Is(err, config.ErrUnspecifiedType) {
		t.Errorf("Attribute(ATTR_TYPE_UNSPECIFIED) error = %v, want errors.Is ErrUnspecifiedType", err)
	}
}

func TestAttribute_ObjectAttributesMismatch(t *testing.T) {
	t.Parallel()

	child, err := config.Attribute("child", configv1.AttrType_ATTR_TYPE_STRING)
	if err != nil {
		t.Fatalf("Attribute(child) = _, %v, want nil error", err)
	}

	tests := []struct {
		name string
		typ  configv1.AttrType
		opts []config.AttributeOption
	}{
		{
			name: "object missing object_attributes",
			typ:  configv1.AttrType_ATTR_TYPE_OBJECT,
			opts: nil,
		},
		{
			name: "non-object with object_attributes",
			typ:  configv1.AttrType_ATTR_TYPE_STRING,
			opts: []config.AttributeOption{config.WithObjectAttributes(child)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Attribute("attr", tt.typ, tt.opts...)
			if !errors.Is(err, config.ErrObjectAttributesMismatch) {
				t.Errorf("Attribute(%v) error = %v, want errors.Is ErrObjectAttributesMismatch", tt.typ, err)
			}
		})
	}
}

func TestAttribute_SensitiveDefault(t *testing.T) {
	t.Parallel()

	_, err := config.Attribute("api_key", configv1.AttrType_ATTR_TYPE_STRING,
		config.WithSensitive(), config.WithDefault(`"literal-secret"`))
	if !errors.Is(err, config.ErrSensitiveDefault) {
		t.Errorf("Attribute(sensitive+default) error = %v, want errors.Is ErrSensitiveDefault", err)
	}
}

func TestAttribute_DefaultTypeMismatch(t *testing.T) {
	t.Parallel()

	nested, err := config.Attribute("port", configv1.AttrType_ATTR_TYPE_NUMBER)
	if err != nil {
		t.Fatalf("Attribute(port) = _, %v, want nil error", err)
	}

	tests := []struct {
		name        string
		typ         configv1.AttrType
		defaultJSON string
		objOpt      config.AttributeOption
	}{
		{name: "not valid JSON at all", typ: configv1.AttrType_ATTR_TYPE_STRING, defaultJSON: `not-json`},
		{name: "number for string", typ: configv1.AttrType_ATTR_TYPE_STRING, defaultJSON: `5`},
		{name: "string for number", typ: configv1.AttrType_ATTR_TYPE_NUMBER, defaultJSON: `"5"`},
		{name: "string for bool", typ: configv1.AttrType_ATTR_TYPE_BOOL, defaultJSON: `"true"`},
		{name: "list of numbers for list_string", typ: configv1.AttrType_ATTR_TYPE_LIST_STRING, defaultJSON: `[1,2]`},
		{name: "object for list_number", typ: configv1.AttrType_ATTR_TYPE_LIST_NUMBER, defaultJSON: `{}`},
		{name: "map of numbers for map_string", typ: configv1.AttrType_ATTR_TYPE_MAP_STRING, defaultJSON: `{"a":1}`},
		{name: "array for object", typ: configv1.AttrType_ATTR_TYPE_OBJECT, defaultJSON: `[1,2]`, objOpt: config.WithObjectAttributes(nested)},
		{name: "undeclared field in object default", typ: configv1.AttrType_ATTR_TYPE_OBJECT, defaultJSON: `{"bogus":1}`, objOpt: config.WithObjectAttributes(nested)},
		{name: "nested field wrong shape", typ: configv1.AttrType_ATTR_TYPE_OBJECT, defaultJSON: `{"port":"not-a-number"}`, objOpt: config.WithObjectAttributes(nested)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []config.AttributeOption{config.WithDefault(tt.defaultJSON)}
			if tt.objOpt != nil {
				opts = append(opts, tt.objOpt)
			}
			_, err := config.Attribute("attr", tt.typ, opts...)
			if !errors.Is(err, config.ErrDefaultTypeMismatch) {
				t.Errorf("Attribute(%v, default=%s) error = %v, want errors.Is ErrDefaultTypeMismatch", tt.typ, tt.defaultJSON, err)
			}
		})
	}
}

func TestAttribute_UnrecognizedTypeWithDefault(t *testing.T) {
	t.Parallel()

	// An out-of-range AttrType value that is nonetheless not the
	// zero/unspecified value exercises validateDefaultShape's default
	// branch directly, without object_attributes ever entering the picture.
	const bogus = configv1.AttrType(99)

	_, err := config.Attribute("attr", bogus, config.WithDefault(`1`))
	if !errors.Is(err, config.ErrDefaultTypeMismatch) {
		t.Errorf("Attribute(bogus type, default) error = %v, want errors.Is ErrDefaultTypeMismatch", err)
	}
}
