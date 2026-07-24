package config

import (
	"encoding/json"
	"fmt"
	"sort"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// validateAttribute checks one already-assembled ConfigAttribute against
// every invariant this package enforces, then recurses into
// ObjectAttributes so a nested attribute — however it was built — gets the
// same treatment as a top-level one (blocks-reference.md#the-schema-to-cty-bridge,
// "Nested object attributes": "each nested ConfigAttribute gets the same
// required/sensitive/description treatment... rather than accepting an
// unvalidated dynamic object"). It is the single source of truth Attribute
// and Schema both call, so a hand-built struct literal that reaches Schema
// without going through Attribute is still caught.
func validateAttribute(attr *configv1.ConfigAttribute) error {
	name := attr.GetName()

	if attr.GetType() == configv1.AttrType_ATTR_TYPE_UNSPECIFIED {
		return fmt.Errorf("attribute %q: %w", name, ErrUnspecifiedType)
	}

	isObject := attr.GetType() == configv1.AttrType_ATTR_TYPE_OBJECT
	hasObjectAttributes := len(attr.GetObjectAttributes()) > 0
	if isObject != hasObjectAttributes {
		return fmt.Errorf("attribute %q: %w", name, ErrObjectAttributesMismatch)
	}

	if attr.DefaultJson != nil {
		if attr.GetSensitive() {
			return fmt.Errorf("attribute %q: %w", name, ErrSensitiveDefault)
		}
		if err := validateDefaultShape(attr.GetDefaultJson(), attr.GetType(), attr.GetObjectAttributes()); err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
	}

	for _, child := range attr.GetObjectAttributes() {
		if err := validateAttribute(child); err != nil {
			return err
		}
	}
	return nil
}

// validateDefaultShape checks that raw is valid JSON whose shape matches
// typ, per blocks-reference.md#the-schema-to-cty-bridge's "Declared
// defaults" encoding rule: a JSON string for ATTR_TYPE_STRING, a JSON
// number for ATTR_TYPE_NUMBER, a JSON array of strings/numbers for the two
// list types, a JSON object of string values for ATTR_TYPE_MAP_STRING, and
// a JSON object matching objectAttrs' shape — recursively — for
// ATTR_TYPE_OBJECT.
func validateDefaultShape(raw string, typ configv1.AttrType, objectAttrs []*configv1.ConfigAttribute) error {
	switch typ {
	case configv1.AttrType_ATTR_TYPE_STRING:
		var v string
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_NUMBER:
		var v float64
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_BOOL:
		var v bool
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_LIST_STRING:
		var v []string
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_LIST_NUMBER:
		var v []float64
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_MAP_STRING:
		var v map[string]string
		return decodeDefaultShape(raw, &v)
	case configv1.AttrType_ATTR_TYPE_OBJECT:
		return validateObjectDefaultShape(raw, objectAttrs)
	default:
		// AttrType_ATTR_TYPE_UNSPECIFIED and anything the wire type adds
		// later that this package doesn't yet know how to shape-check.
		return fmt.Errorf("%w: unrecognized attribute type %s", ErrDefaultTypeMismatch, typ)
	}
}

// decodeDefaultShape unmarshals raw into dst, reporting any failure —
// syntax error or a JSON value of the wrong Go-mapped kind — as
// ErrDefaultTypeMismatch.
func decodeDefaultShape(raw string, dst any) error {
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return fmt.Errorf("%w: %w", ErrDefaultTypeMismatch, err)
	}
	return nil
}

// validateObjectDefaultShape checks that raw is a JSON object and that
// each field present in it matches the corresponding declared
// objectAttrs entry's type, recursively. A field objectAttrs declares but
// raw omits is fine — the default need not populate every nested field,
// exactly as an optional top-level attribute need not appear in
// agent.hcl at all.
func validateObjectDefaultShape(raw string, objectAttrs []*configv1.ConfigAttribute) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return fmt.Errorf("%w: %w", ErrDefaultTypeMismatch, err)
	}

	byName := make(map[string]*configv1.ConfigAttribute, len(objectAttrs))
	for _, oa := range objectAttrs {
		byName[oa.GetName()] = oa
	}

	// Sorted iteration keeps the returned error deterministic when more
	// than one field is invalid — required by determinism.md whenever map
	// contents feed observable output, and it makes this function's own
	// behavior reproducible regardless of Go's randomized map order.
	fieldNames := make([]string, 0, len(fields))
	for fieldName := range fields {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)

	for _, fieldName := range fieldNames {
		oa, ok := byName[fieldName]
		if !ok {
			return fmt.Errorf("%w: field %q is not declared in object_attributes", ErrDefaultTypeMismatch, fieldName)
		}
		if err := validateDefaultShape(string(fields[fieldName]), oa.GetType(), oa.GetObjectAttributes()); err != nil {
			return fmt.Errorf("field %q: %w", fieldName, err)
		}
	}
	return nil
}
