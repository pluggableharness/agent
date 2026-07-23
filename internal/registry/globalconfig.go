package registry

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/pluggableharness/agent/internal/hclsecret"
	"github.com/pluggableharness/agent/internal/telemetry"
)

var globalConfigSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "dev_overrides"},
		{Type: "registry_mirror"},
	},
}

// GlobalConfig is the per-user, never-committed global config file at
// $XDG_CONFIG_HOME/agent/config.hcl. configuration.md §10: it holds
// exactly these two blocks and MUST NOT contain project-specific provider
// configuration — provider auth belongs in a project's agent.hcl via
// env(...) indirection, never here.
type GlobalConfig struct {
	// Maps a required_providers local name to a local binary path. When
	// present for a given name, the kernel MUST use that binary directly
	// instead of resolving through the registry/version-constraint
	// machinery — mirrors Terraform's dev_overrides exactly.
	DevOverrides map[string]string

	// Per-source-prefix registry redirection.
	RegistryMirror MirrorTable
}

// LoadGlobalConfig parses path as a global config file. It performs file
// I/O, so per internal/CLAUDE.md it logs entry at DEBUG and wraps the
// operation in a telemetry span via prov.StartGlobalConfigLoad, ended with
// the call's error.
func LoadGlobalConfig(ctx context.Context, prov *telemetry.Provider, path string) (_ *GlobalConfig, err error) {
	ctx, span := prov.StartGlobalConfigLoad(ctx, path)
	defer func() { telemetry.EndSpan(span, err) }()
	slog.DebugContext(ctx, "registry: loading global config", "path", path)

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		err = fmt.Errorf("registry: load global config: %w", diags)
		return nil, err
	}

	content, diags := file.Body.Content(globalConfigSchema)
	if diags.HasErrors() {
		err = fmt.Errorf("registry: load global config: %w", diags)
		return nil, err
	}

	cfg := &GlobalConfig{DevOverrides: map[string]string{}}
	for _, block := range content.Blocks {
		switch block.Type {
		case "dev_overrides":
			overrides, decodeErr := decodeDevOverrides(block.Body)
			if decodeErr != nil {
				err = decodeErr
				return nil, err
			}
			cfg.DevOverrides = overrides
		case "registry_mirror":
			mirror, decodeErr := decodeRegistryMirror(block.Body)
			if decodeErr != nil {
				err = decodeErr
				return nil, err
			}
			cfg.RegistryMirror = mirror
		}
	}
	return cfg, nil
}

// decodeDevOverrides decodes a dev_overrides block: an arbitrary set of
// `local_name = "path"` attributes, no fixed schema (the local names are
// whatever the project's required_providers happen to declare, which this
// package has no visibility into).
func decodeDevOverrides(body hcl.Body) (map[string]string, error) {
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return nil, fmt.Errorf("registry: dev_overrides: %w", diags)
	}
	overrides := make(map[string]string, len(attrs))
	for name, attr := range attrs {
		path, err := attrString(attr)
		if err != nil {
			return nil, fmt.Errorf("registry: dev_overrides: %q: %w", name, err)
		}
		overrides[name] = path
	}
	return overrides, nil
}

func decodeRegistryMirror(body hcl.Body) (MirrorTable, error) {
	schema := &hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "default", Required: true}},
		Blocks:     []hcl.BlockHeaderSchema{{Type: "mirror"}},
	}
	content, diags := body.Content(schema)
	if diags.HasErrors() {
		return MirrorTable{}, fmt.Errorf("registry: registry_mirror: %w", diags)
	}

	defaultURL, err := attrString(content.Attributes["default"])
	if err != nil {
		return MirrorTable{}, fmt.Errorf("registry: registry_mirror: default: %w", err)
	}

	rm := MirrorTable{Default: defaultURL}
	for _, block := range content.Blocks {
		mirror, err := decodeMirror(block.Body)
		if err != nil {
			return MirrorTable{}, err
		}
		rm.Mirrors = append(rm.Mirrors, mirror)
	}
	return rm, nil
}

func decodeMirror(body hcl.Body) (Mirror, error) {
	schema := &hcl.BodySchema{Attributes: []hcl.AttributeSchema{
		{Name: "prefix", Required: true},
		{Name: "url", Required: true},
		{Name: "auth", Required: false},
	}}
	content, diags := body.Content(schema)
	if diags.HasErrors() {
		return Mirror{}, fmt.Errorf("registry: mirror: %w", diags)
	}

	prefix, err := attrString(content.Attributes["prefix"])
	if err != nil {
		return Mirror{}, fmt.Errorf("registry: mirror: prefix: %w", err)
	}
	url, err := attrString(content.Attributes["url"])
	if err != nil {
		return Mirror{}, fmt.Errorf("registry: mirror %q: url: %w", prefix, err)
	}

	m := Mirror{Prefix: prefix, URL: url}
	if authAttr, ok := content.Attributes["auth"]; ok {
		// auth MUST be exactly env("NAME") — checked against the raw,
		// unevaluated expression before it's ever evaluated, identically
		// to a provider's sensitive attributes (internal/hclsecret).
		if err := hclsecret.ValidateSensitiveExpr(authAttr); err != nil {
			return Mirror{}, fmt.Errorf("registry: mirror %q: %w", prefix, err)
		}
		evalCtx := &hcl.EvalContext{
			Functions: map[string]function.Function{hclsecret.EnvFunctionName: hclsecret.EnvFunction},
		}
		val, diags := authAttr.Expr.Value(evalCtx)
		if diags.HasErrors() {
			return Mirror{}, fmt.Errorf("registry: mirror %q: auth: %w", prefix, diags)
		}
		m.Auth = val.AsString()
	}
	return m, nil
}

// attrString evaluates attr as a plain string-typed expression. Used for
// attributes that carry no secrets and need no functions in scope (a nil
// EvalContext is sufficient for a literal string).
func attrString(attr *hcl.Attribute) (string, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return "", diags
	}
	if val.Type() != cty.String {
		return "", fmt.Errorf("registry: attribute %q: %w", attr.Name, ErrInvalidValue)
	}
	return val.AsString(), nil
}
