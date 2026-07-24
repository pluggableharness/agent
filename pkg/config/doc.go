// Package config builds validated pluggableharness.config.v1.ConfigSchema
// and ConfigAttribute values for a plugin's GetCapabilities/GetSchema
// response — the schema the kernel decodes a matching agent.hcl provider
// block against before ever calling that provider's Configure RPC (see
// docs/specifications/configuration/blocks-reference.md#the-schema-to-cty-bridge).
//
// The generated pkg/config/proto/v1 types are a flat struct literal: nothing
// stops a plugin author from hand-assembling a ConfigAttribute that silently
// violates one of the invariants blocks-reference.md's schema-to-cty bridge
// section requires. This package exists so a caller cannot easily construct
// an invalid one. It enforces three rules, each drawn directly from that
// section:
//
//   - ObjectAttributes MUST be non-empty if and only if Type is
//     AttrType_ATTR_TYPE_OBJECT; it MUST be empty for every other type
//     (blocks-reference.md#the-schema-to-cty-bridge, "Nested object
//     attributes").
//   - DefaultJson MUST NOT be set on an attribute with Sensitive == true — a
//     declared default is a literal value baked into the schema
//     advertisement itself, which is exactly what the secrets rule forbids
//     regardless of where the literal appears (blocks-reference.md#the-schema-to-cty-bridge,
//     "Declared defaults").
//   - DefaultJson's JSON shape MUST match the attribute's declared Type,
//     recursively for a nested ATTR_TYPE_OBJECT default
//     (blocks-reference.md#the-schema-to-cty-bridge, "Declared defaults").
//
// Attribute is the primary entry point: it validates a single attribute
// immediately, so a caller gets a *configv1.ConfigAttribute that is already
// known-good or an error explaining which invariant it violated. Schema
// additionally re-validates the whole attribute tree — including any
// ObjectAttributes supplied as hand-built struct literals rather than
// through Attribute — before assembling a *configv1.ConfigSchema, so an
// invalid attribute cannot reach the wire regardless of how it was built.
package config
