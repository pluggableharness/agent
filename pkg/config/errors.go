package config

import "errors"

// ErrUnspecifiedType is returned when an attribute's Type is left at its
// zero value, AttrType_ATTR_TYPE_UNSPECIFIED. The generated type's own doc
// comment states this value is "never valid for a real attribute; its
// presence on the wire means a caller forgot to set the field" — this
// package rejects it at construction time rather than letting it reach the
// kernel's schema-to-cty bridge.
var ErrUnspecifiedType = errors.New("config: type must not be ATTR_TYPE_UNSPECIFIED")

// ErrObjectAttributesMismatch is returned when ObjectAttributes is empty on
// an ATTR_TYPE_OBJECT attribute, or non-empty on any other type. See
// docs/specifications/configuration/blocks-reference.md#the-schema-to-cty-bridge,
// "Nested object attributes": ObjectAttributes "MUST be set (non-empty) iff
// type == object; MUST be empty for every other type."
var ErrObjectAttributesMismatch = errors.New("config: object_attributes must be set iff type is ATTR_TYPE_OBJECT")

// ErrSensitiveDefault is returned when both DefaultJson and Sensitive are
// set on the same attribute. See
// docs/specifications/configuration/blocks-reference.md#the-schema-to-cty-bridge,
// "Declared defaults": "default_json MUST NOT be set on an attribute with
// sensitive = true — a declared default is a literal value baked into the
// schema advertisement itself, which is exactly the literal-secret-value
// case 'Secrets' below forbids regardless of where the literal appears."
var ErrSensitiveDefault = errors.New("config: default_json must not be set when sensitive is true")

// ErrDefaultTypeMismatch is returned when DefaultJson does not parse as
// JSON, or parses but its shape does not match the attribute's declared
// Type. See docs/specifications/configuration/blocks-reference.md#the-schema-to-cty-bridge,
// "Declared defaults": "A default_json that doesn't parse as JSON, or that
// parses but doesn't match type's expected shape, MUST be rejected as a
// config-load-time error."
var ErrDefaultTypeMismatch = errors.New("config: default_json does not match the attribute's declared type")
