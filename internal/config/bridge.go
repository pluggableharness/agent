package config

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/hclsecret"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"

	"github.com/zclconf/go-cty/cty/function"
)

// DecodeProviderConfig implements the schema-to-cty bridge
// (configuration.md §4): it builds an hcldec.Spec from schema, validates
// every `sensitive` attribute's raw expression BEFORE evaluating anything,
// decodes body against that spec (with hclsecret.EnvFunction registered so
// env(...) calls resolve, failing fast on an unset variable), and marshals
// the result to JSON matching what a provider's Configure RPC expects — a
// google.protobuf.Struct (config.pb.go's own doc: Configure receives
// decoded JSON, never HCL/cty).
//
// This is the ONLY place a cty.Value exists in this package's provider
// config path. Everything upstream (LoadFile) only captures a raw,
// undecoded hcl.Body per provider — a provider's ConfigSchema only exists
// once its plugin subprocess is loaded and queried, which this package has
// no part in.
func DecodeProviderConfig(body hcl.Body, schema *configv1.ConfigSchema) (*structpb.Struct, error) {
	spec, sensitiveAttrs, err := buildSpec(schema)
	if err != nil {
		return nil, err
	}

	if err := validateSensitiveAttrs(body, sensitiveAttrs); err != nil {
		return nil, err
	}

	evalCtx := &hcl.EvalContext{
		Functions: map[string]function.Function{hclsecret.EnvFunctionName: hclsecret.EnvFunction},
	}

	val, diags := hcldec.Decode(body, spec, evalCtx)
	if diags.HasErrors() {
		return nil, fmt.Errorf("config: decode provider config: %w", diags)
	}

	data, err := ctyjson.Marshal(val, val.Type())
	if err != nil {
		return nil, fmt.Errorf("config: decode provider config: marshal: %w", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("config: decode provider config: unmarshal: %w", err)
	}

	result, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("config: decode provider config: struct: %w", err)
	}
	return result, nil
}

// buildSpec builds an hcldec.ObjectSpec from schema's attributes, and
// separately reports which attribute names are sensitive (so the caller
// can validate their expressions before decode evaluates anything).
func buildSpec(schema *configv1.ConfigSchema) (hcldec.Spec, map[string]bool, error) {
	obj := hcldec.ObjectSpec{}
	sensitive := map[string]bool{}

	for _, attr := range schema.GetAttributes() {
		ctyType, err := ctyTypeForAttr(attr.GetType())
		if err != nil {
			return nil, nil, fmt.Errorf("config: attribute %q: %w", attr.GetName(), err)
		}
		obj[attr.GetName()] = &hcldec.AttrSpec{
			Name:     attr.GetName(),
			Type:     ctyType,
			Required: attr.GetRequired(),
		}
		if attr.GetSensitive() {
			sensitive[attr.GetName()] = true
		}
	}

	return obj, sensitive, nil
}

// ctyTypeForAttr maps a ConfigAttribute's AttrType (configuration.md §4's
// fixed 7-value subset) onto the corresponding cty.Type.
func ctyTypeForAttr(t configv1.AttrType) (cty.Type, error) {
	switch t {
	case configv1.AttrType_ATTR_TYPE_STRING:
		return cty.String, nil
	case configv1.AttrType_ATTR_TYPE_NUMBER:
		return cty.Number, nil
	case configv1.AttrType_ATTR_TYPE_BOOL:
		return cty.Bool, nil
	case configv1.AttrType_ATTR_TYPE_LIST_STRING:
		return cty.List(cty.String), nil
	case configv1.AttrType_ATTR_TYPE_LIST_NUMBER:
		return cty.List(cty.Number), nil
	case configv1.AttrType_ATTR_TYPE_MAP_STRING:
		return cty.Map(cty.String), nil
	case configv1.AttrType_ATTR_TYPE_OBJECT:
		// No nested block types in v1 (configuration.md §4) — this is an
		// ordinary attribute whose value happens to be object-shaped,
		// accepted dynamically rather than validated against a fixed
		// shape hcldec would otherwise enforce.
		return cty.DynamicPseudoType, nil
	default:
		return cty.NilType, fmt.Errorf("%w: %v", ErrInvalidAttrType, t)
	}
}

// validateSensitiveAttrs checks every attribute named in sensitiveAttrs
// against hclsecret.ValidateSensitiveExpr, using the body's raw,
// unevaluated attributes — configuration.md §4 requires this check happen
// BEFORE hcldec.Decode ever evaluates the expression.
func validateSensitiveAttrs(body hcl.Body, sensitiveAttrs map[string]bool) error {
	if len(sensitiveAttrs) == 0 {
		return nil
	}
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return fmt.Errorf("config: %w", diags)
	}
	for name := range sensitiveAttrs {
		attr, ok := attrs[name]
		if !ok {
			continue // absent optional sensitive attribute — nothing to validate
		}
		if err := hclsecret.ValidateSensitiveExpr(attr); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	return nil
}
