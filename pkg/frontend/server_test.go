package frontend_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	"github.com/pluggableharness/agent/pkg/frontend"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
)

func TestService_GetCapabilities(t *testing.T) {
	t.Parallel()

	slash := &commonv1.PromptExpansionSpec{Name: "explain"}
	provider := &fakeProvider{
		capabilitiesFunc: func(context.Context) (*frontend.Capabilities, error) {
			return frontend.NewCapabilities(nil, frontend.WithSlashCommands(slash)), nil
		},
	}
	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	resp, err := client.GetCapabilities(t.Context(), &frontendv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities() error = %v", err)
	}
	got := resp.GetCapabilities().GetSlashCommands()
	if len(got) != 1 || got[0].GetName() != "explain" {
		t.Errorf("GetCapabilities() slash commands = %v, want [explain]", got)
	}
}

func TestService_GetCapabilities_Error(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		capabilitiesFunc: func(context.Context) (*frontend.Capabilities, error) {
			return nil, &frontend.Error{
				Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_UNKNOWN,
				Message:  "boom",
			}
		},
	}
	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	_, err := client.GetCapabilities(t.Context(), &frontendv1.GetCapabilitiesRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("GetCapabilities() code = %v, want Internal", status.Code(err))
	}
}

func TestService_Configure(t *testing.T) {
	t.Parallel()

	var gotConfig *structpb.Struct
	provider := &fakeProvider{
		configureFunc: func(_ context.Context, config *structpb.Struct) error {
			gotConfig = config
			return nil
		},
	}
	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	cfg, err := structpb.NewStruct(map[string]any{"theme": "dark"})
	if err != nil {
		t.Fatalf("structpb.NewStruct() error = %v", err)
	}
	if _, err := client.Configure(t.Context(), &frontendv1.ConfigureRequest{Config: cfg}); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if gotConfig.GetFields()["theme"].GetStringValue() != "dark" {
		t.Errorf("Configure() received config = %v, want theme=dark", gotConfig)
	}
}

func TestService_Configure_InvalidArgument(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return &frontend.Error{
				Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT,
				Message:  "malformed theme",
			}
		},
	}
	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	_, err := client.Configure(t.Context(), &frontendv1.ConfigureRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Configure() code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestService_Configure_UnmappedError(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		configureFunc: func(context.Context, *structpb.Struct) error {
			return errors.New("plain error, no category")
		},
	}
	client := newTestServer(t, frontend.NewService(provider, testIdentity, nil))

	_, err := client.Configure(t.Context(), &frontendv1.ConfigureRequest{})
	if status.Code(err) != codes.Internal {
		t.Errorf("Configure() code = %v, want Internal", status.Code(err))
	}
}

func TestService_Describe(t *testing.T) {
	t.Parallel()

	client := newTestServer(t, frontend.NewService(&fakeProvider{}, testIdentity, nil))

	resp, err := client.Describe(t.Context(), &frontendv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != testIdentity.Name {
		t.Errorf("Describe() producer name = %q, want %q", producer.GetName(), testIdentity.Name)
	}
	if producer.GetVersion() != testIdentity.Version {
		t.Errorf("Describe() producer version = %q, want %q", producer.GetVersion(), testIdentity.Version)
	}
	if producer.GetCategory() != commonv1.Category_CATEGORY_FRONTEND {
		t.Errorf("Describe() producer category = %v, want CATEGORY_FRONTEND", producer.GetCategory())
	}
}
