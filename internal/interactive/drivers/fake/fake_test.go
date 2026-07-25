package fake

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/pluggableharness/agent/internal/interactive"
)

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()

	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}

func TestResolver_scripted(t *testing.T) {
	t.Parallel()

	answered := interactive.Response{Payload: mustStruct(t, map[string]any{"answer": "yes"})}
	scriptedErr := errors.New("fake_test: scripted failure")

	tests := []struct {
		name        string
		resp        interactive.Response
		err         error
		wantAnswer  string
		wantErr     error
		wantPayload bool
	}{
		{
			name:        "human answered",
			resp:        answered,
			wantAnswer:  "yes",
			wantPayload: true,
		},
		{
			name:    "no frontend",
			err:     interactive.ErrNoFrontend,
			wantErr: interactive.ErrNoFrontend,
		},
		{
			name:    "arbitrary scripted error wins over a scripted response",
			resp:    answered,
			err:     scriptedErr,
			wantErr: scriptedErr,
		},
		{
			name: "zero value answers with an empty response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := New(tt.resp, tt.err)
			req := interactive.Request{CallID: "call-1", ToolName: "ask_user", Arguments: mustStruct(t, map[string]any{"q": "?"})}

			got, err := r.Resolve(context.Background(), req)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if got.Payload != nil {
					t.Errorf("Resolve payload = %v, want nil alongside an error", got.Payload)
				}
			} else {
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				if tt.wantPayload {
					if answer := got.Payload.GetFields()["answer"].GetStringValue(); answer != tt.wantAnswer {
						t.Errorf("Resolve payload answer = %q, want %q", answer, tt.wantAnswer)
					}
				} else if got.Payload != nil {
					t.Errorf("Resolve payload = %v, want nil for a zero-value fake", got.Payload)
				}
			}

			// Every call is recorded regardless of which answer was scripted.
			reqs := r.Requests()
			if len(reqs) != 1 {
				t.Fatalf("Requests() = %d records, want 1", len(reqs))
			}
			if reqs[0].CallID != req.CallID || reqs[0].ToolName != req.ToolName {
				t.Errorf("Requests()[0] = %+v, want CallID %q / ToolName %q", reqs[0], req.CallID, req.ToolName)
			}
		})
	}
}

func TestResolver_zeroValueUsable(t *testing.T) {
	t.Parallel()

	var r Resolver
	got, err := r.Resolve(context.Background(), interactive.Request{ToolName: "ask_user"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Payload != nil {
		t.Errorf("Resolve payload = %v, want nil", got.Payload)
	}
	if len(r.Requests()) != 1 {
		t.Errorf("Requests() = %d records, want 1", len(r.Requests()))
	}
}

func TestResolver_recordsInCallOrder(t *testing.T) {
	t.Parallel()

	r := New(interactive.Response{}, nil)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := r.Resolve(context.Background(), interactive.Request{CallID: id}); err != nil {
			t.Fatalf("Resolve(%q): %v", id, err)
		}
	}

	reqs := r.Requests()
	want := []string{"a", "b", "c"}
	if len(reqs) != len(want) {
		t.Fatalf("Requests() = %d records, want %d", len(reqs), len(want))
	}
	for i, id := range want {
		if reqs[i].CallID != id {
			t.Errorf("Requests()[%d].CallID = %q, want %q", i, reqs[i].CallID, id)
		}
	}

	// Requests returns a copy — mutating it must not corrupt the fake.
	reqs[0].CallID = "mutated"
	if r.Requests()[0].CallID != "a" {
		t.Error("Requests() returned an aliased slice, want a copy")
	}
}

func TestResolver_canceledContext(t *testing.T) {
	t.Parallel()

	r := New(interactive.Response{Payload: mustStruct(t, map[string]any{"answer": "yes"})}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := r.Resolve(ctx, interactive.Request{CallID: "call-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve error = %v, want errors.Is context.Canceled", err)
	}
	if len(r.Requests()) != 0 {
		t.Errorf("Requests() = %d records, want 0 — a canceled call records nothing", len(r.Requests()))
	}
}

var _ interactive.Resolver = (*Resolver)(nil)
