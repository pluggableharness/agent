package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
)

// decodeRequiredProviders decodes required_providers{}'s arbitrary set of
// `local_name = { source = "...", version = "..." }` attributes
// (configuration.md §5). Local names are whatever the operator chooses, so
// this uses JustAttributes rather than a fixed schema.
func decodeRequiredProviders(body hcl.Body) (map[string]RequiredProvider, error) {
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil, fmt.Errorf("config: required_providers: %w", diags)
	}

	result := make(map[string]RequiredProvider, len(attrs))
	for name, attr := range attrs {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return nil, fmt.Errorf("config: required_providers: %q: %w", name, diags)
		}
		if !val.Type().IsObjectType() && !val.Type().IsMapType() {
			return nil, fmt.Errorf("config: required_providers: %q: %w", name, ErrInvalidValue)
		}

		fields := val.AsValueMap()
		source, ok := fields["source"]
		if !ok {
			return nil, fmt.Errorf("config: required_providers: %q: source: %w", name, ErrMissingField)
		}
		version, ok := fields["version"]
		if !ok {
			return nil, fmt.Errorf("config: required_providers: %q: version: %w", name, ErrMissingField)
		}

		result[name] = RequiredProvider{
			Source:     source.AsString(),
			Constraint: version.AsString(),
		}
	}
	return result, nil
}
