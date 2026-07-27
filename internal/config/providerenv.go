package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/pluggableharness/agent/internal/hclsecret"
)

// providerEnvBlockType is the kernel-owned block inside provider{} that
// names environment variables to pass through to that plugin's
// subprocess.
const providerEnvBlockType = "environment"

// providerEnvSchema matches the environment{} block without consuming
// anything else, so the remaining body is still the provider's own.
var providerEnvSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: providerEnvBlockType}},
}

// extractProviderEnv lifts the environment{} block out of a provider{}
// body, returning the resolved variables and the remaining body.
//
// The remaining body is what later gets decoded against the provider's
// own ConfigSchema. Splitting here rather than at decode time is what
// keeps a kernel-owned name from colliding with a provider that happens
// to declare an attribute called "environment".
//
// A body with no environment{} block yields a nil map and the body
// unchanged, which is the ordinary case.
func extractProviderEnv(body hcl.Body) (map[string]string, hcl.Body, error) {
	content, remain, diags := body.PartialContent(providerEnvSchema)
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("environment: %w", diags)
	}
	if len(content.Blocks) == 0 {
		return nil, body, nil
	}
	if len(content.Blocks) > 1 {
		return nil, nil, fmt.Errorf("environment: %w", ErrDuplicateBlock)
	}

	attrs, diags := content.Blocks[0].Body.JustAttributes()
	if diags.HasErrors() {
		return nil, nil, fmt.Errorf("environment: %w", diags)
	}

	// env(...) is available here for the same reason it is in a provider's
	// own attributes: the value an operator wants to forward is usually
	// already in their own environment, and naming it beats copying it
	// into a config file.
	evalCtx := &hcl.EvalContext{
		Functions: map[string]function.Function{hclsecret.EnvFunctionName: hclsecret.EnvFunction},
	}

	out := make(map[string]string, len(attrs))
	for name, attr := range attrs {
		if err := validateEnvName(name); err != nil {
			return nil, nil, fmt.Errorf("environment: %w", err)
		}
		val, valDiags := attr.Expr.Value(evalCtx)
		if valDiags.HasErrors() {
			return nil, nil, fmt.Errorf("environment: %s: %w", name, valDiags)
		}
		if val.IsNull() || !val.IsKnown() {
			return nil, nil, fmt.Errorf("environment: %s: %w", name, ErrEnvValueUnusable)
		}
		if val.Type().FriendlyName() != "string" {
			return nil, nil, fmt.Errorf("environment: %s: %w", name, ErrEnvValueNotString)
		}
		out[name] = val.AsString()
	}
	return out, remain, nil
}

// validateEnvName rejects a name that cannot be a POSIX environment
// variable. An "=" would let one entry smuggle in a second, and an empty
// name produces a malformed entry the subprocess silently ignores.
//
// Defense in depth rather than a reachable config path: HCL's own grammar
// forbids a quoted argument name, so neither case can be written in
// agent.hcl today. It is kept because the cost is one comparison and the
// failure it guards against is silent.
func validateEnvName(name string) error {
	if name == "" {
		return ErrEnvNameEmpty
	}
	if strings.ContainsAny(name, "=\x00") {
		return fmt.Errorf("%w: %q", ErrEnvNameInvalid, name)
	}
	return nil
}

// EnvEntries renders env as sorted "KEY=VALUE" entries, the shape
// exec.Cmd expects.
//
// Sorted because a subprocess's environment is otherwise assembled in Go
// map order, which is randomized — and a launch that differs run to run
// is exactly the kind of nondeterminism .claude/rules/determinism.md
// exists to prevent.
func EnvEntries(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+env[name])
	}
	return out
}
