package config

import (
	"fmt"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// attributeOptions collects AttributeOption values before Attribute
// validates them as a whole. Defaults (all fields at their Go zero value)
// match ConfigAttribute's own wire defaults: not required, not sensitive,
// no description, no nested schema, no default.
type attributeOptions struct {
	required         bool
	sensitive        bool
	description      string
	objectAttributes []*configv1.ConfigAttribute
	defaultJSON      string
	hasDefault       bool
}

// AttributeOption configures one optional field of a ConfigAttribute built
// by Attribute. An option only records the caller's intent; Attribute
// validates the fully-assembled attribute once, after every option has run.
type AttributeOption func(*attributeOptions)

// WithRequired marks the attribute as required: agent.hcl MUST set it, and
// the kernel MUST reject a Configure call that omits it.
func WithRequired() AttributeOption {
	return func(o *attributeOptions) { o.required = true }
}

// WithSensitive marks the attribute as able to hold a secret. A sensitive
// attribute's agent.hcl expression is restricted to env(...) indirection
// (blocks-reference.md#secrets-sensitive-and-env) and, per
// ErrSensitiveDefault, cannot also carry a WithDefault value.
func WithSensitive() AttributeOption {
	return func(o *attributeOptions) { o.sensitive = true }
}

// WithDescription sets the human-readable description shown wherever this
// attribute's schema is surfaced to an operator (docs generation,
// validation errors).
func WithDescription(description string) AttributeOption {
	return func(o *attributeOptions) { o.description = description }
}

// WithDefault sets DefaultJson to the given JSON-encoded text — the value
// the schema-to-cty bridge uses when this attribute is optional and
// agent.hcl omits it. defaultJSON's shape MUST match the attribute's
// declared type (a JSON string for ATTR_TYPE_STRING, a JSON array of
// numbers for ATTR_TYPE_LIST_NUMBER, and so on); Attribute rejects a
// mismatch with ErrDefaultTypeMismatch, and rejects any default at all on a
// sensitive attribute with ErrSensitiveDefault.
func WithDefault(defaultJSON string) AttributeOption {
	return func(o *attributeOptions) {
		o.defaultJSON = defaultJSON
		o.hasDefault = true
	}
}

// WithObjectAttributes sets the nested schema for an ATTR_TYPE_OBJECT
// attribute. It is only legal when Attribute's typ argument is
// ATTR_TYPE_OBJECT; Attribute rejects any other combination with
// ErrObjectAttributesMismatch. Each element is expected to already be a
// validated *configv1.ConfigAttribute — typically one built by a nested
// call to Attribute itself.
func WithObjectAttributes(attrs ...*configv1.ConfigAttribute) AttributeOption {
	return func(o *attributeOptions) { o.objectAttributes = attrs }
}

// Attribute builds one ConfigAttribute and validates it immediately against
// the schema-to-cty bridge's invariants (package doc, blocks-reference.md#the-schema-to-cty-bridge):
// ObjectAttributes set iff typ is ATTR_TYPE_OBJECT, no DefaultJson on a
// sensitive attribute, and DefaultJson's JSON shape matching typ. A caller
// gets back either a known-good *configv1.ConfigAttribute or an error
// identifying which invariant it violated (compare with errors.Is against
// ErrUnspecifiedType, ErrObjectAttributesMismatch, ErrSensitiveDefault, or
// ErrDefaultTypeMismatch).
func Attribute(name string, typ configv1.AttrType, opts ...AttributeOption) (*configv1.ConfigAttribute, error) {
	var o attributeOptions
	for _, opt := range opts {
		opt(&o)
	}

	attr := &configv1.ConfigAttribute{
		Name:             name,
		Type:             typ,
		Required:         o.required,
		Sensitive:        o.sensitive,
		Description:      o.description,
		ObjectAttributes: o.objectAttributes,
	}
	if o.hasDefault {
		defaultJSON := o.defaultJSON
		attr.DefaultJson = &defaultJSON
	}

	if err := validateAttribute(attr); err != nil {
		return nil, fmt.Errorf("config: attribute: %w", err)
	}
	return attr, nil
}
