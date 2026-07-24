package config

import (
	"fmt"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// Schema assembles a ConfigSchema from attrs, validating every attribute in
// the tree — including nested ObjectAttributes — before returning it. An
// attribute built through Attribute is already known-good, so this pass is
// normally a no-op; it exists so Schema itself, not just Attribute, is the
// place a caller can trust to catch a hand-built *configv1.ConfigAttribute
// struct literal that bypassed Attribute (see doc.go). It returns an error
// identifying the first violated invariant, checked with errors.Is against
// ErrUnspecifiedType, ErrObjectAttributesMismatch, ErrSensitiveDefault, or
// ErrDefaultTypeMismatch.
func Schema(attrs ...*configv1.ConfigAttribute) (*configv1.ConfigSchema, error) {
	for _, attr := range attrs {
		if err := validateAttribute(attr); err != nil {
			return nil, fmt.Errorf("config: schema: %w", err)
		}
	}
	return &configv1.ConfigSchema{Attributes: attrs}, nil
}
