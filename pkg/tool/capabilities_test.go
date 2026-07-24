package tool_test

import (
	"context"
	"errors"
	"testing"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	configv1 "github.com/pluggableharness/agent/pkg/config/proto/v1"
	"github.com/pluggableharness/agent/pkg/tool"
)

func TestBuildGetSchemaResponseBasic(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		schemaFunc: func(context.Context) ([]*tool.Schema, error) {
			return []*tool.Schema{validSchema("read_file"), validSchema("glob")}, nil
		},
	}

	resp, err := tool.BuildGetSchemaResponse(t.Context(), p)
	if err != nil {
		t.Fatalf("BuildGetSchemaResponse: %v", err)
	}
	if got := len(resp.GetTools()); got != 2 {
		t.Fatalf("len(Tools) = %d, want 2", got)
	}
	if resp.GetConfigSchema() != nil {
		t.Errorf("ConfigSchema = %v, want nil (provider does not implement ConfigSchemaProvider)", resp.GetConfigSchema())
	}
	if resp.GetSlashCommands() != nil {
		t.Errorf("SlashCommands = %v, want nil", resp.GetSlashCommands())
	}
	if resp.GetSupportedHookPoints() != nil {
		t.Errorf("SupportedHookPoints = %v, want nil", resp.GetSupportedHookPoints())
	}
}

func TestBuildGetSchemaResponseSchemaError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	p := &fakeProvider{
		schemaFunc: func(context.Context) ([]*tool.Schema, error) { return nil, wantErr },
	}

	_, err := tool.BuildGetSchemaResponse(t.Context(), p)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildGetSchemaResponse() error = %v, want wrapping %v", err, wantErr)
	}
}

func TestBuildGetSchemaResponseInvalidSchema(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{
		schemaFunc: func(context.Context) ([]*tool.Schema, error) {
			return []*tool.Schema{{Name: ""}}, nil // missing everything
		},
	}

	_, err := tool.BuildGetSchemaResponse(t.Context(), p)
	if err == nil {
		t.Fatal("BuildGetSchemaResponse() with an invalid Schema: want error, got nil")
	}
}

func TestBuildGetSchemaResponseOptionalCapabilities(t *testing.T) {
	t.Parallel()

	base := &fakeProvider{
		schemaFunc: func(context.Context) ([]*tool.Schema, error) {
			return []*tool.Schema{validSchema("op")}, nil
		},
	}
	wantSchema := &configv1.ConfigSchema{}
	wantSlash := []*commonv1.PromptExpansionSpec{{Name: "foo", Template: "do foo"}}
	wantHooks := []commonv1.HookPoint{commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL}

	p := &fakeFullProvider{
		fakeProvider:     base,
		configSchemaFunc: func() (*configv1.ConfigSchema, error) { return wantSchema, nil },
		slashCommands:    wantSlash,
		hookPoints:       wantHooks,
	}

	resp, err := tool.BuildGetSchemaResponse(t.Context(), p)
	if err != nil {
		t.Fatalf("BuildGetSchemaResponse: %v", err)
	}
	if resp.GetConfigSchema() != wantSchema {
		t.Errorf("ConfigSchema = %v, want %v", resp.GetConfigSchema(), wantSchema)
	}
	if len(resp.GetSlashCommands()) != 1 || resp.GetSlashCommands()[0].GetName() != "foo" {
		t.Errorf("SlashCommands = %v", resp.GetSlashCommands())
	}
	if len(resp.GetSupportedHookPoints()) != 1 || resp.GetSupportedHookPoints()[0] != commonv1.HookPoint_HOOK_POINT_PRE_TOOL_CALL {
		t.Errorf("SupportedHookPoints = %v", resp.GetSupportedHookPoints())
	}
}

func TestBuildGetSchemaResponseConfigSchemaError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("bad config schema")
	base := &fakeProvider{
		schemaFunc: func(context.Context) ([]*tool.Schema, error) { return nil, nil },
	}
	p := &fakeFullProvider{
		fakeProvider:     base,
		configSchemaFunc: func() (*configv1.ConfigSchema, error) { return nil, wantErr },
	}

	_, err := tool.BuildGetSchemaResponse(t.Context(), p)
	if !errors.Is(err, wantErr) {
		t.Fatalf("BuildGetSchemaResponse() error = %v, want wrapping %v", err, wantErr)
	}
}
