package schema

import (
	"fmt"

	schemav1 "github.com/pluggableharness/agent/pkg/schema/proto/v1"
)

// Object builds an OBJECT schema node with the given named property
// sub-schemas. WithRequired(names...) marks a subset of properties' keys
// as required; every named property MUST already be a key of properties,
// or Object returns an error. Object also rejects a nil entry in
// properties — a nil *schemav1.Schema is not a valid subset schema, and
// letting it through would produce a Schema that fails validation only
// much later, at the kernel/adapter boundary rather than at construction
// time.
func Object(properties map[string]*schemav1.Schema, opts ...Option) (*schemav1.Schema, error) {
	o := resolve(opts)

	for name, prop := range properties {
		if prop == nil {
			return nil, fmt.Errorf("schema: object: property %q is nil", name)
		}
	}
	for _, name := range o.required {
		if _, ok := properties[name]; !ok {
			return nil, fmt.Errorf("schema: object: required name %q is not a key of properties", name)
		}
	}

	return &schemav1.Schema{
		Type:        schemav1.SchemaType_SCHEMA_TYPE_OBJECT,
		Description: o.description,
		Properties:  properties,
		Required:    o.required,
	}, nil
}

// String builds a STRING schema node. WithEnum constrains the value to a
// fixed set of strings; WithDescription sets the description.
func String(opts ...Option) *schemav1.Schema {
	o := resolve(opts)
	return &schemav1.Schema{
		Type:        schemav1.SchemaType_SCHEMA_TYPE_STRING,
		Description: o.description,
		EnumValues:  o.enumValues,
	}
}

// Number builds a NUMBER schema node. model/data-types.md#tool-schema's
// subset does not distinguish integer from floating point — both are
// SCHEMA_TYPE_NUMBER on the wire — so Number is the one builder that
// actually produces this type; Integer is a wire-identical convenience
// alias of it.
func Number(opts ...Option) *schemav1.Schema {
	o := resolve(opts)
	return &schemav1.Schema{
		Type:        schemav1.SchemaType_SCHEMA_TYPE_NUMBER,
		Description: o.description,
	}
}

// Integer builds a NUMBER schema node, identical on the wire to Number.
// It exists purely so a Go author can write down integer intent at the
// call site; no adapter or downstream consumer can distinguish the
// result from Number's, because the generated SchemaType enum has no
// dedicated integer value (see doc.go).
func Integer(opts ...Option) *schemav1.Schema {
	return Number(opts...)
}

// Boolean builds a BOOLEAN schema node.
func Boolean(opts ...Option) *schemav1.Schema {
	o := resolve(opts)
	return &schemav1.Schema{
		Type:        schemav1.SchemaType_SCHEMA_TYPE_BOOLEAN,
		Description: o.description,
	}
}

// Array builds an ARRAY schema node whose elements must each satisfy
// items. items MUST be non-nil — an ARRAY node without an element schema
// is not representable per model/data-types.md#tool-schema ("items —
// ARRAY only: the schema every element of the array must satisfy") — so
// Array returns an error rather than silently producing an invalid node.
func Array(items *schemav1.Schema, opts ...Option) (*schemav1.Schema, error) {
	if items == nil {
		return nil, fmt.Errorf("schema: array: items must not be nil")
	}
	o := resolve(opts)
	return &schemav1.Schema{
		Type:        schemav1.SchemaType_SCHEMA_TYPE_ARRAY,
		Description: o.description,
		Items:       items,
	}, nil
}

// Enum builds a STRING schema node constrained to one of values — sugar
// for String(WithEnum(values...)). Use String directly to combine an enum
// with a description.
func Enum(values ...string) *schemav1.Schema {
	return String(WithEnum(values...))
}
