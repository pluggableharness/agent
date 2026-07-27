package config

import (
	"errors"
	"slices"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// providerBody parses src as agent.hcl and returns the named provider
// block's raw body.
func providerBody(t *testing.T, src string) hcl.Body {
	t.Helper()

	f, diags := hclparse.NewParser().ParseHCL([]byte(src), "test.hcl")
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	content, _, diags := f.Body.PartialContent(topLevelSchema)
	if diags.HasErrors() {
		t.Fatalf("root content: %v", diags)
	}
	for _, block := range content.Blocks {
		if block.Type == "provider" {
			return block.Body
		}
	}
	t.Fatal("no provider block in fixture")
	return nil
}

func TestExtractProviderEnv_liftsTheBlockAndLeavesTheRest(t *testing.T) {
	t.Parallel()

	body := providerBody(t, `
provider "anthropic" {
  api_key = "sk-test"

  environment {
    HTTPS_PROXY = "http://proxy.internal:3128"
    NO_PROXY    = "localhost"
  }
}
`)

	env, remain, err := extractProviderEnv(body)
	if err != nil {
		t.Fatalf("extractProviderEnv: %v", err)
	}
	if got, want := env["HTTPS_PROXY"], "http://proxy.internal:3128"; got != want {
		t.Errorf("HTTPS_PROXY = %q, want %q", got, want)
	}
	if got, want := env["NO_PROXY"], "localhost"; got != want {
		t.Errorf("NO_PROXY = %q, want %q", got, want)
	}

	// The remaining body must still carry the provider's own attributes.
	//
	// Read via PartialContent, not JustAttributes: HCL's remain body does
	// NOT hide an already-consumed block, so JustAttributes still rejects
	// the environment{} block outright. That is exactly why
	// validateSensitiveAttrs in bridge.go asks for named attributes rather
	// than all of them — a provider using environment{} would otherwise
	// fail the secret check.
	content, _, diags := remain.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "api_key"}},
	})
	if diags.HasErrors() {
		t.Fatalf("remaining attributes: %v", diags)
	}
	if _, ok := content.Attributes["api_key"]; !ok {
		t.Error("the provider's own api_key did not survive the split")
	}
}

func TestExtractProviderEnv_absentBlockLeavesTheBodyUntouched(t *testing.T) {
	t.Parallel()

	body := providerBody(t, `
provider "anthropic" {
  api_key = "sk-test"
}
`)

	env, remain, err := extractProviderEnv(body)
	if err != nil {
		t.Fatalf("extractProviderEnv: %v", err)
	}
	if env != nil {
		t.Errorf("env = %v, want nil for a provider declaring none", env)
	}
	if remain == nil {
		t.Fatal("remain = nil, want the body unchanged")
	}
}

func TestExtractProviderEnv_resolvesEnvIndirection(t *testing.T) {
	// Not parallel: t.Setenv forbids it.
	t.Setenv("MODELTEST_PROXY", "http://resolved:3128")

	body := providerBody(t, `
provider "anthropic" {
  environment {
    HTTPS_PROXY = env("MODELTEST_PROXY")
  }
}
`)

	env, _, err := extractProviderEnv(body)
	if err != nil {
		t.Fatalf("extractProviderEnv: %v", err)
	}
	if got, want := env["HTTPS_PROXY"], "http://resolved:3128"; got != want {
		t.Errorf("HTTPS_PROXY = %q, want %q", got, want)
	}
}

func TestExtractProviderEnv_rejectsUnusableDeclarations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src     string
		wantErr error
	}{
		// A subprocess environment carries only strings; stringifying a
		// number silently would hide the config error rather than fix it.
		"non-string value": {
			src: `provider "p" {
  environment {
    PORT = 8080
  }
}`,
			wantErr: ErrEnvValueNotString,
		},
		"null value": {
			src: `provider "p" {
  environment {
    A = null
  }
}`,
			wantErr: ErrEnvValueUnusable,
		},
		"two environment blocks": {
			src: `provider "p" {
  environment {
    A = "1"
  }
  environment {
    B = "2"
  }
}`,
			wantErr: ErrDuplicateBlock,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, _, err := extractProviderEnv(providerBody(t, tt.src))
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateEnvName(t *testing.T) {
	t.Parallel()

	// Tested directly rather than through a config fixture because HCL's
	// own grammar forbids a quoted argument name, so neither of these is
	// reachable from agent.hcl today. The check is defense in depth: an
	// "=" would let a single entry smuggle in a second, and an empty name
	// produces a malformed entry the subprocess silently ignores.
	if err := validateEnvName(""); !errors.Is(err, ErrEnvNameEmpty) {
		t.Errorf("validateEnvName(\"\") = %v, want ErrEnvNameEmpty", err)
	}
	if err := validateEnvName("A=B"); !errors.Is(err, ErrEnvNameInvalid) {
		t.Errorf("validateEnvName(\"A=B\") = %v, want ErrEnvNameInvalid", err)
	}
	if err := validateEnvName("HTTPS_PROXY"); err != nil {
		t.Errorf("validateEnvName(\"HTTPS_PROXY\") = %v, want nil", err)
	}
}

func TestEnvEntries_isSortedAndShaped(t *testing.T) {
	t.Parallel()

	// Sorted because Go map order is randomized, and a subprocess whose
	// environment differs run to run is exactly the nondeterminism
	// determinism.md exists to prevent.
	got := EnvEntries(map[string]string{"ZED": "3", "ALPHA": "1", "MID": "2"})
	want := []string{"ALPHA=1", "MID=2", "ZED=3"}
	if !slices.Equal(got, want) {
		t.Errorf("EnvEntries = %v, want %v", got, want)
	}
	if EnvEntries(nil) != nil {
		t.Error("EnvEntries(nil) is non-nil, want nil so an absent block adds nothing")
	}
}

// TestExtractProviderEnv_coexistsWithASensitiveAttribute is the
// regression guard for the interaction that broke first: HCL's remain
// body does not hide an already-consumed block, so validateSensitiveAttrs
// reading the body with JustAttributes rejected every provider that used
// environment{} at all — including, and especially, the ones carrying a
// credential.
func TestExtractProviderEnv_coexistsWithASensitiveAttribute(t *testing.T) {
	// Not parallel: t.Setenv forbids it.
	t.Setenv("MODELTEST_KEY", "sk-from-the-environment")

	body := providerBody(t, `
provider "anthropic" {
  api_key = env("MODELTEST_KEY")

  environment {
    HTTPS_PROXY = "http://proxy.internal:3128"
  }
}
`)

	env, remain, err := extractProviderEnv(body)
	if err != nil {
		t.Fatalf("extractProviderEnv: %v", err)
	}
	if env["HTTPS_PROXY"] == "" {
		t.Fatal("the environment block was not extracted")
	}

	schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{{
		Name:      "api_key",
		Type:      configv1.AttrType_ATTR_TYPE_STRING,
		Required:  true,
		Sensitive: true,
	}}}

	decoded, err := DecodeProviderConfig(remain, schema)
	if err != nil {
		t.Fatalf("DecodeProviderConfig alongside an environment block: %v", err)
	}
	if got := decoded.GetFields()["api_key"].GetStringValue(); got != "sk-from-the-environment" {
		t.Errorf("api_key = %q, want the resolved secret", got)
	}
}
