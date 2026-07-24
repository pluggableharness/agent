package slashcommand_test

import (
	"context"
	"errors"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/slashcommand"
)

func TestBuildGetCapabilitiesResponseBasic(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) {
			return []*slashcommand.Spec{validSpec("deploy"), validSpec("release-notes")}, nil
		},
	}

	resp, err := slashcommand.BuildGetCapabilitiesResponse(t.Context(), p)
	if err != nil {
		t.Fatalf("BuildGetCapabilitiesResponse: %v", err)
	}
	if got := len(resp.GetCommands()); got != 2 {
		t.Fatalf("len(Commands) = %d, want 2", got)
	}
	if resp.GetConfigSchema() != nil {
		t.Errorf("ConfigSchema = %v, want nil (provider does not implement ConfigSchemaProvider)", resp.GetConfigSchema())
	}
	if resp.GetSupportedHookPoints() != nil {
		t.Errorf("SupportedHookPoints = %v, want nil", resp.GetSupportedHookPoints())
	}
}

func TestBuildGetCapabilitiesResponseCapabilitiesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) { return nil, wantErr },
	}

	_, err := slashcommand.BuildGetCapabilitiesResponse(t.Context(), p)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildGetCapabilitiesResponse() error = %v, want wrapping %v", err, wantErr)
	}
}

func TestBuildGetCapabilitiesResponseInvalidSpec(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) {
			return []*slashcommand.Spec{{Name: ""}}, nil // missing everything
		},
	}

	_, err := slashcommand.BuildGetCapabilitiesResponse(t.Context(), p)
	if err == nil {
		t.Fatal("BuildGetCapabilitiesResponse() with an invalid Spec: want error, got nil")
	}
}

func TestBuildGetCapabilitiesResponseOptionalCapabilities(t *testing.T) {
	t.Parallel()

	base := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) {
			return []*slashcommand.Spec{validSpec("deploy")}, nil
		},
	}
	wantSchema := &configv1.ConfigSchema{}
	wantHooks := []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL}

	p := &fakeFullProvider{
		fakeProvider:     base,
		configSchemaFunc: func() (*configv1.ConfigSchema, error) { return wantSchema, nil },
		hookPoints:       wantHooks,
	}

	resp, err := slashcommand.BuildGetCapabilitiesResponse(t.Context(), p)
	if err != nil {
		t.Fatalf("BuildGetCapabilitiesResponse: %v", err)
	}
	if resp.GetConfigSchema() != wantSchema {
		t.Errorf("ConfigSchema = %v, want %v", resp.GetConfigSchema(), wantSchema)
	}
	if len(resp.GetSupportedHookPoints()) != 1 || resp.GetSupportedHookPoints()[0] != commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL {
		t.Errorf("SupportedHookPoints = %v", resp.GetSupportedHookPoints())
	}
}

func TestBuildGetCapabilitiesResponseConfigSchemaError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("bad config schema")
	base := &fakeProvider{
		capabilitiesFunc: func(context.Context) ([]*slashcommand.Spec, error) { return nil, nil },
	}
	p := &fakeFullProvider{
		fakeProvider:     base,
		configSchemaFunc: func() (*configv1.ConfigSchema, error) { return nil, wantErr },
	}

	_, err := slashcommand.BuildGetCapabilitiesResponse(t.Context(), p)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildGetCapabilitiesResponse() error = %v, want wrapping %v", err, wantErr)
	}
}
