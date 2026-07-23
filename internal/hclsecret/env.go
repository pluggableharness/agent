package hclsecret

import (
	"errors"
	"fmt"
	"os"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// EnvFunctionName is the exact function name a sensitive expression (or
// registry_mirror.mirror.auth) MUST call — configuration.md §4.
const EnvFunctionName = "env"

// ErrEnvUnset is returned when env(name) is evaluated against an unset
// environment variable.
var ErrEnvUnset = errors.New("hclsecret: environment variable not set")

// EnvFunction is the HCL `env(name)` function. It MUST fail fast — return
// an error, not resolve to an empty string — when the named variable is
// unset (configuration.md §4: "a silently-empty string reaching Configure
// is indistinguishable from intentionally blank"). Register this in the
// hcl.EvalContext used to evaluate any body that may contain a sensitive
// attribute or a registry_mirror.mirror.auth expression.
var EnvFunction = function.New(&function.Spec{
	Params: []function.Parameter{
		{Name: "name", Type: cty.String},
	},
	Type: function.StaticReturnType(cty.String),
	Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
		name := args[0].AsString()
		value, ok := os.LookupEnv(name)
		if !ok {
			return cty.UnknownVal(cty.String), fmt.Errorf("hclsecret: env(%q): %w", name, ErrEnvUnset)
		}
		return cty.StringVal(value), nil
	},
})
