package config

import (
	"errors"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
)

// parseBody parses an HCL block body from source, for exercising
// DecodeProviderConfig against real parsed bodies.
func parseBody(t *testing.T, src string) hcl.Body {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse: %v", diags)
	}
	return f.Body
}

func TestDecodeProviderConfig(t *testing.T) {
	t.Run("string, number, bool, list attributes", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "token_budget", Type: configv1.AttrType_ATTR_TYPE_NUMBER, Required: true},
			{Name: "enabled", Type: configv1.AttrType_ATTR_TYPE_BOOL, Required: true},
			{Name: "paths", Type: configv1.AttrType_ATTR_TYPE_LIST_STRING, Required: true},
		}}
		body := parseBody(t, `
token_budget = 4000
enabled      = true
paths        = ["CLAUDE.md", "**/CLAUDE.md"]
`)

		got, err := DecodeProviderConfig(body, schema)
		if err != nil {
			t.Fatalf("DecodeProviderConfig: unexpected error: %v", err)
		}

		fields := got.AsMap()
		if fields["token_budget"] != 4000.0 {
			t.Fatalf("token_budget = %v, want 4000", fields["token_budget"])
		}
		if fields["enabled"] != true {
			t.Fatalf("enabled = %v, want true", fields["enabled"])
		}
		paths, ok := fields["paths"].([]any)
		if !ok || len(paths) != 2 || paths[0] != "CLAUDE.md" {
			t.Fatalf("paths = %v, unexpected", fields["paths"])
		}
	})

	t.Run("sensitive attr with valid env() call", func(t *testing.T) {
		t.Setenv("BRIDGE_TEST_API_KEY", "sk-abc123")
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "api_key", Type: configv1.AttrType_ATTR_TYPE_STRING, Required: true, Sensitive: true},
		}}
		body := parseBody(t, `api_key = env("BRIDGE_TEST_API_KEY")`)

		got, err := DecodeProviderConfig(body, schema)
		if err != nil {
			t.Fatalf("DecodeProviderConfig: unexpected error: %v", err)
		}
		if got.AsMap()["api_key"] != "sk-abc123" {
			t.Fatalf("api_key = %v, want sk-abc123", got.AsMap()["api_key"])
		}
	})

	t.Run("sensitive attr with literal value is rejected", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "api_key", Type: configv1.AttrType_ATTR_TYPE_STRING, Required: true, Sensitive: true},
		}}
		body := parseBody(t, `api_key = "literal-secret"`)

		_, err := DecodeProviderConfig(body, schema)
		if err == nil {
			t.Fatal("DecodeProviderConfig: want error for literal sensitive value, got nil")
		}
	})

	t.Run("sensitive attr with unset env var fails fast", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "api_key", Type: configv1.AttrType_ATTR_TYPE_STRING, Required: true, Sensitive: true},
		}}
		body := parseBody(t, `api_key = env("BRIDGE_TEST_DEFINITELY_UNSET_VAR")`)

		_, err := DecodeProviderConfig(body, schema)
		if err == nil {
			t.Fatal("DecodeProviderConfig: want error for unset env var, got nil")
		}
	})

	t.Run("missing required attribute", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "api_key", Type: configv1.AttrType_ATTR_TYPE_STRING, Required: true},
		}}
		body := parseBody(t, ``)

		if _, err := DecodeProviderConfig(body, schema); err == nil {
			t.Fatal("DecodeProviderConfig: want error for missing required attribute, got nil")
		}
	})

	t.Run("object attribute type accepted dynamically", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "extra", Type: configv1.AttrType_ATTR_TYPE_OBJECT, Required: true},
		}}
		body := parseBody(t, `extra = { nested = "value" }`)

		got, err := DecodeProviderConfig(body, schema)
		if err != nil {
			t.Fatalf("DecodeProviderConfig: unexpected error: %v", err)
		}
		extra, ok := got.AsMap()["extra"].(map[string]any)
		if !ok || extra["nested"] != "value" {
			t.Fatalf("extra = %v, unexpected", got.AsMap()["extra"])
		}
	})

	t.Run("invalid AttrType", func(t *testing.T) {
		schema := &configv1.ConfigSchema{Attributes: []*configv1.ConfigAttribute{
			{Name: "x", Type: configv1.AttrType(99), Required: true},
		}}
		body := parseBody(t, `x = "y"`)

		_, err := DecodeProviderConfig(body, schema)
		if !errors.Is(err, ErrInvalidAttrType) {
			t.Fatalf("DecodeProviderConfig error = %v, want wrapping ErrInvalidAttrType", err)
		}
	})

	t.Run("no attributes at all", func(t *testing.T) {
		schema := &configv1.ConfigSchema{}
		body := parseBody(t, ``)

		got, err := DecodeProviderConfig(body, schema)
		if err != nil {
			t.Fatalf("DecodeProviderConfig: unexpected error: %v", err)
		}
		if len(got.AsMap()) != 0 {
			t.Fatalf("AsMap() = %v, want empty", got.AsMap())
		}
	})
}
