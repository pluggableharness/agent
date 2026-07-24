package memory_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/pkg/memory"
	memoryv1 "github.com/pluggableharness/agent/pkg/memory/proto/v1"
	"github.com/pluggableharness/agent/pkg/plugin"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
)

var errInjected = errors.New("fake: injected failure")

func testIdentity() plugin.Identity {
	return plugin.Identity{Name: "test-memory", Version: "1.0.0", Source: "github.com/agentco/test-memory"}
}

func TestService_GetCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		provider     memory.Provider
		wantSupports bool
		wantErr      codes.Code
	}{
		{
			name: "non-ratifying provider claiming true is corrected to false",
			provider: &fakeProvider{
				capabilitiesFunc: func(context.Context) (memory.Capabilities, error) {
					return memory.Capabilities{RatificationSupported: true, SupportedTypes: []memory.Type{memory.TypeUser}}, nil
				},
			},
			wantSupports: false,
		},
		{
			name: "ratifier reports true regardless of its own Capabilities claim",
			provider: &fakeRatifier{
				fakeProvider: fakeProvider{
					capabilitiesFunc: func(context.Context) (memory.Capabilities, error) {
						return memory.Capabilities{RatificationSupported: false}, nil
					},
				},
			},
			wantSupports: true,
		},
		{
			name: "partial ratifier (ApproveRecord only) is treated as incapable",
			provider: &fakePartialRatifier{
				fakeProvider: fakeProvider{
					capabilitiesFunc: func(context.Context) (memory.Capabilities, error) {
						return memory.Capabilities{RatificationSupported: true}, nil
					},
				},
			},
			wantSupports: false,
		},
		{
			name: "provider error propagates",
			provider: &fakeProvider{
				capabilitiesFunc: func(context.Context) (memory.Capabilities, error) {
					return memory.Capabilities{}, errInjected
				},
			},
			wantErr: codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newTestClient(t, memory.NewService(tt.provider, testIdentity(), plugin.NewCallback()))
			resp, err := client.GetCapabilities(t.Context(), &memoryv1.GetCapabilitiesRequest{})
			if tt.wantErr != codes.OK {
				assertCode(t, err, tt.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("GetCapabilities() error = %v, want nil", err)
			}
			if got := resp.GetCapabilities().GetRatificationSupported(); got != tt.wantSupports {
				t.Errorf("GetCapabilities().RatificationSupported = %v, want %v", got, tt.wantSupports)
			}
		})
	}
}

func TestService_Configure(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		var called bool
		provider := &fakeProvider{configureFunc: func(context.Context, *structpb.Struct) error {
			called = true
			return nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		if _, err := client.Configure(t.Context(), &memoryv1.ConfigureRequest{}); err != nil {
			t.Fatalf("Configure() error = %v, want nil", err)
		}
		if !called {
			t.Error("Configure() did not call through to the provider")
		}
	})

	t.Run("error propagates", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{configureFunc: func(context.Context, *structpb.Struct) error {
			return memory.SourceUnavailable("db down")
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Configure(t.Context(), &memoryv1.ConfigureRequest{})
		assertCode(t, err, codes.Unavailable)
	})
}

func TestService_Recall(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{recallFunc: func(_ context.Context, req memory.RecallRequest) (memory.RecallResult, error) {
			if req.TokenBudget != 100 {
				t.Errorf("RecallRequest.TokenBudget = %d, want 100", req.TokenBudget)
			}
			return memory.RecallResult{Records: []memory.Record{{ID: "r1", Type: memory.TypeProject, Scope: memory.ScopeProject, Content: "hello"}}}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.Recall(t.Context(), &memoryv1.RecallRequest{TokenBudget: 100})
		if err != nil {
			t.Fatalf("Recall() error = %v, want nil", err)
		}
		if len(resp.GetRecords()) != 1 || resp.GetRecords()[0].GetId() != "r1" {
			t.Errorf("Recall() records = %v, want one record with id r1", resp.GetRecords())
		}
	})

	t.Run("budget exceeded maps to ResourceExhausted", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{recallFunc: func(context.Context, memory.RecallRequest) (memory.RecallResult, error) {
			return memory.RecallResult{}, memory.BudgetExceeded("too many candidates")
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Recall(t.Context(), &memoryv1.RecallRequest{})
		assertCode(t, err, codes.ResourceExhausted)
	})

	t.Run("canceled context maps to Canceled, not an application error", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{recallFunc: func(context.Context, memory.RecallRequest) (memory.RecallResult, error) {
			return memory.RecallResult{}, context.Canceled
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Recall(t.Context(), &memoryv1.RecallRequest{})
		assertCode(t, err, codes.Canceled)
	})
}

func TestService_Record(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{recordFunc: func(_ context.Context, req memory.RecordRequest) (memory.RecordResult, error) {
			if req.Content != "hello" {
				t.Errorf("RecordRequest.Content = %q, want %q", req.Content, "hello")
			}
			return memory.RecordResult{ID: "hello-1", Status: memory.RecordStatusCanonical}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.Record(t.Context(), &memoryv1.RecordRequest{Content: textBlocks("hello")})
		if err != nil {
			t.Fatalf("Record() error = %v, want nil", err)
		}
		if resp.GetResult().GetId() != "hello-1" {
			t.Errorf("Record().Result.Id = %q, want %q", resp.GetResult().GetId(), "hello-1")
		}
	})

	t.Run("pending status from a non-ratifying provider is rejected", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{recordFunc: func(context.Context, memory.RecordRequest) (memory.RecordResult, error) {
			return memory.RecordResult{ID: "x", Status: memory.RecordStatusPending}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Record(t.Context(), &memoryv1.RecordRequest{})
		assertCode(t, err, codes.Internal)
	})

	t.Run("pending status from a ratifying provider is allowed", func(t *testing.T) {
		t.Parallel()
		provider := &fakeRatifier{fakeProvider: fakeProvider{recordFunc: func(context.Context, memory.RecordRequest) (memory.RecordResult, error) {
			return memory.RecordResult{ID: "x", Status: memory.RecordStatusPending}, nil
		}}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.Record(t.Context(), &memoryv1.RecordRequest{})
		if err != nil {
			t.Fatalf("Record() error = %v, want nil", err)
		}
		if resp.GetResult().GetStatus() != memoryv1.RecordStatus_RECORD_STATUS_PENDING {
			t.Errorf("Record().Result.Status = %v, want PENDING", resp.GetResult().GetStatus())
		}
	})

	t.Run("non-text content is rejected", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Record(t.Context(), &memoryv1.RecordRequest{Content: imageBlocks()})
		assertCode(t, err, codes.Internal)
	})
}

func TestService_UpdateRecord(t *testing.T) {
	t.Parallel()

	t.Run("missing id fails not_found without calling the provider", func(t *testing.T) {
		t.Parallel()
		var called bool
		provider := &fakeProvider{updateRecordFunc: func(context.Context, memory.UpdateRecordRequest) (memory.RecordResult, error) {
			called = true
			return memory.RecordResult{}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.UpdateRecord(t.Context(), &memoryv1.UpdateRecordRequest{})
		assertCode(t, err, codes.NotFound)
		if called {
			t.Error("UpdateRecord() called through to the provider with an empty id")
		}
	})

	t.Run("unknown id fails not_found", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{updateRecordFunc: func(context.Context, memory.UpdateRecordRequest) (memory.RecordResult, error) {
			return memory.RecordResult{}, memory.NotFound("no such record")
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.UpdateRecord(t.Context(), &memoryv1.UpdateRecordRequest{Id: "missing"})
		assertCode(t, err, codes.NotFound)
	})

	t.Run("success replaces content wholesale", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{updateRecordFunc: func(_ context.Context, req memory.UpdateRecordRequest) (memory.RecordResult, error) {
			if req.Content != "new content" {
				t.Errorf("UpdateRecordRequest.Content = %q, want %q", req.Content, "new content")
			}
			return memory.RecordResult{ID: req.ID, Status: memory.RecordStatusCanonical}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.UpdateRecord(t.Context(), &memoryv1.UpdateRecordRequest{Id: "r1", Content: textBlocks("new content")})
		if err != nil {
			t.Fatalf("UpdateRecord() error = %v, want nil", err)
		}
	})
}

func TestService_DeleteRecord(t *testing.T) {
	t.Parallel()

	t.Run("missing id fails not_found without calling the provider", func(t *testing.T) {
		t.Parallel()
		var called bool
		provider := &fakeProvider{deleteRecordFunc: func(context.Context, string) (memory.DeleteResult, error) {
			called = true
			return memory.DeleteResult{}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.DeleteRecord(t.Context(), &memoryv1.DeleteRecordRequest{})
		assertCode(t, err, codes.NotFound)
		if called {
			t.Error("DeleteRecord() called through to the provider with an empty id")
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{deleteRecordFunc: func(context.Context, string) (memory.DeleteResult, error) {
			return memory.DeleteResult{Deleted: true}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.DeleteRecord(t.Context(), &memoryv1.DeleteRecordRequest{Id: "r1"})
		if err != nil {
			t.Fatalf("DeleteRecord() error = %v, want nil", err)
		}
		if !resp.GetResult().GetDeleted() {
			t.Error("DeleteRecord().Result.Deleted = false, want true")
		}
	})
}

func TestService_ApproveRejectRecord(t *testing.T) {
	t.Parallel()

	t.Run("non-ratifying provider fails ratification_unsupported", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))

		_, err := client.ApproveRecord(t.Context(), &memoryv1.ApproveRecordRequest{Id: "r1"})
		assertCode(t, err, codes.FailedPrecondition)

		_, err = client.RejectRecord(t.Context(), &memoryv1.RejectRecordRequest{Id: "r1"})
		assertCode(t, err, codes.FailedPrecondition)
	})

	t.Run("partial ratifier (ApproveRecord only) still fails ratification_unsupported", func(t *testing.T) {
		t.Parallel()
		provider := &fakePartialRatifier{}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))

		_, err := client.ApproveRecord(t.Context(), &memoryv1.ApproveRecordRequest{Id: "r1"})
		assertCode(t, err, codes.FailedPrecondition)
	})

	t.Run("ratifier approves and rejects", func(t *testing.T) {
		t.Parallel()
		provider := &fakeRatifier{
			approveFunc: func(_ context.Context, id string) (memory.RecordResult, error) {
				return memory.RecordResult{ID: id, Status: memory.RecordStatusCanonical}, nil
			},
			rejectFunc: func(context.Context, string) (memory.DeleteResult, error) {
				return memory.DeleteResult{Deleted: true}, nil
			},
		}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))

		approveResp, err := client.ApproveRecord(t.Context(), &memoryv1.ApproveRecordRequest{Id: "r1"})
		if err != nil {
			t.Fatalf("ApproveRecord() error = %v, want nil", err)
		}
		if approveResp.GetResult().GetStatus() != memoryv1.RecordStatus_RECORD_STATUS_CANONICAL {
			t.Errorf("ApproveRecord().Result.Status = %v, want CANONICAL", approveResp.GetResult().GetStatus())
		}

		rejectResp, err := client.RejectRecord(t.Context(), &memoryv1.RejectRecordRequest{Id: "r1"})
		if err != nil {
			t.Fatalf("RejectRecord() error = %v, want nil", err)
		}
		if !rejectResp.GetResult().GetDeleted() {
			t.Error("RejectRecord().Result.Deleted = false, want true")
		}
	})

	t.Run("missing id fails not_found", func(t *testing.T) {
		t.Parallel()
		provider := &fakeRatifier{}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.ApproveRecord(t.Context(), &memoryv1.ApproveRecordRequest{})
		assertCode(t, err, codes.NotFound)
	})
}

func TestService_Render(t *testing.T) {
	t.Parallel()

	t.Run("unimplemented without a renderer", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.Render(t.Context(), &memoryv1.RenderRequest{})
		assertCode(t, err, codes.Unimplemented)
	})

	t.Run("renderer returns a tree", func(t *testing.T) {
		t.Parallel()
		provider := &fakeRenderer{renderFunc: func(_ context.Context, _ []byte, schemaVersion string) (*renderv1.RenderTree, error) {
			if schemaVersion != "v1" {
				t.Errorf("schemaVersion = %q, want %q", schemaVersion, "v1")
			}
			return &renderv1.RenderTree{}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.Render(t.Context(), &memoryv1.RenderRequest{SchemaVersion: "v1"})
		if err != nil {
			t.Fatalf("Render() error = %v, want nil", err)
		}
		if resp.GetTree() == nil {
			t.Error("Render().Tree = nil, want non-nil")
		}
	})
}

func TestService_ListRecords(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{listRecordsFunc: func(_ context.Context, req memory.ListRecordsRequest) (memory.ListRecordsResult, error) {
		if req.StatusFilter != nil {
			t.Errorf("StatusFilter = %v, want nil (both canonical and pending eligible)", *req.StatusFilter)
		}
		return memory.ListRecordsResult{
			Records:       []memory.Record{{ID: "r1"}, {ID: "r2"}},
			NextPageToken: "next",
		}, nil
	}}
	client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
	resp, err := client.ListRecords(t.Context(), &memoryv1.ListRecordsRequest{})
	if err != nil {
		t.Fatalf("ListRecords() error = %v, want nil", err)
	}
	if len(resp.GetRecords()) != 2 {
		t.Errorf("ListRecords() records = %d, want 2", len(resp.GetRecords()))
	}
	if resp.GetNextPageToken() != "next" {
		t.Errorf("ListRecords().NextPageToken = %q, want %q", resp.GetNextPageToken(), "next")
	}
}

func TestService_GetRecord(t *testing.T) {
	t.Parallel()

	t.Run("missing id fails not_found without calling the provider", func(t *testing.T) {
		t.Parallel()
		var called bool
		provider := &fakeProvider{getRecordFunc: func(context.Context, string) (memory.Record, error) {
			called = true
			return memory.Record{}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.GetRecord(t.Context(), &memoryv1.GetRecordRequest{})
		assertCode(t, err, codes.NotFound)
		if called {
			t.Error("GetRecord() called through to the provider with an empty id")
		}
	})

	t.Run("unknown id fails not_found", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{getRecordFunc: func(context.Context, string) (memory.Record, error) {
			return memory.Record{}, memory.NotFound("no such record")
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		_, err := client.GetRecord(t.Context(), &memoryv1.GetRecordRequest{Id: "missing"})
		assertCode(t, err, codes.NotFound)
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		provider := &fakeProvider{getRecordFunc: func(_ context.Context, id string) (memory.Record, error) {
			return memory.Record{ID: id, Type: memory.TypeReference, Scope: memory.ScopeGlobal, Content: "pointer"}, nil
		}}
		client := newTestClient(t, memory.NewService(provider, testIdentity(), plugin.NewCallback()))
		resp, err := client.GetRecord(t.Context(), &memoryv1.GetRecordRequest{Id: "r1"})
		if err != nil {
			t.Fatalf("GetRecord() error = %v, want nil", err)
		}
		if resp.GetRecord().GetId() != "r1" {
			t.Errorf("GetRecord().Record.Id = %q, want %q", resp.GetRecord().GetId(), "r1")
		}
	})
}

func TestService_Describe(t *testing.T) {
	t.Parallel()

	identity := testIdentity()
	client := newTestClient(t, memory.NewService(&fakeProvider{}, identity, plugin.NewCallback()))
	resp, err := client.Describe(t.Context(), &memoryv1.DescribeRequest{})
	if err != nil {
		t.Fatalf("Describe() error = %v, want nil", err)
	}
	producer := resp.GetProducer()
	if producer.GetName() != identity.Name || producer.GetVersion() != identity.Version || producer.GetSource() != identity.Source {
		t.Errorf("Describe().Producer = %+v, want name/version/source matching identity %+v", producer, identity)
	}
}

// assertCode fails t unless err is a gRPC status error carrying want.
func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %v", want)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error = %v, not a gRPC status error", err)
	}
	if st.Code() != want {
		t.Errorf("error code = %v, want %v", st.Code(), want)
	}
}
