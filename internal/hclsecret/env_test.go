package hclsecret

import (
	"errors"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestEnvFunction(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		t.Setenv("HCLSECRET_TEST_VAR", "hunter2")
		got, err := EnvFunction.Call([]cty.Value{cty.StringVal("HCLSECRET_TEST_VAR")})
		if err != nil {
			t.Fatalf("EnvFunction.Call: unexpected error: %v", err)
		}
		if got.AsString() != "hunter2" {
			t.Fatalf("EnvFunction.Call = %q, want %q", got.AsString(), "hunter2")
		}
	})

	t.Run("unset", func(t *testing.T) {
		_, err := EnvFunction.Call([]cty.Value{cty.StringVal("HCLSECRET_TEST_VAR_DEFINITELY_UNSET")})
		if err == nil {
			t.Fatal("EnvFunction.Call: want error for unset variable, got nil")
		}
		if !errors.Is(err, ErrEnvUnset) {
			t.Fatalf("EnvFunction.Call error = %v, want wrapping ErrEnvUnset", err)
		}
	})
}
