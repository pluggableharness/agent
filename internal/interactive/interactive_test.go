package interactive

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

// stubResolver is the smallest possible Resolver, here only to prove the
// interface is implementable from outside a driver package.
type stubResolver struct {
	resp Response
	err  error
}

func (s stubResolver) Resolve(ctx context.Context, _ Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	return s.resp, s.err
}

var _ Resolver = stubResolver{}

func TestErrNoFrontend(t *testing.T) {
	t.Parallel()

	if ErrNoFrontend == nil {
		t.Fatal("ErrNoFrontend is nil")
	}
	if !errors.Is(ErrNoFrontend, ErrNoFrontend) {
		t.Error("errors.Is(ErrNoFrontend, ErrNoFrontend) = false, want true")
	}

	// The sentinel must survive wrapping, since the future tool scheduler
	// identifies it through however many layers a Resolve call is wrapped
	// in before it converts the refusal into a
	// TOOL_ERROR_CATEGORY_PERMISSION_DENIED ToolError.
	wrapped := fmt.Errorf("interactive: resolve: %w", ErrNoFrontend)
	if !errors.Is(wrapped, ErrNoFrontend) {
		t.Error("errors.Is(wrapped, ErrNoFrontend) = false, want true")
	}

	const want = "interactive: no frontend attached to answer an interactive call"
	if got := ErrNoFrontend.Error(); got != want {
		t.Errorf("ErrNoFrontend.Error() = %q, want %q", got, want)
	}
}

func TestResolver_contract(t *testing.T) {
	t.Parallel()

	payload, err := structpb.NewStruct(map[string]any{"answer": "yes"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	args, err := structpb.NewStruct(map[string]any{"question": "proceed?"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	tests := []struct {
		name       string
		resolver   Resolver
		cancel     bool
		wantAnswer string
		wantErr    error
	}{
		{
			name:       "answered",
			resolver:   stubResolver{resp: Response{Payload: payload}},
			wantAnswer: "yes",
		},
		{
			name:     "refused",
			resolver: stubResolver{err: ErrNoFrontend},
			wantErr:  ErrNoFrontend,
		},
		{
			name:     "canceled",
			resolver: stubResolver{resp: Response{Payload: payload}},
			cancel:   true,
			wantErr:  context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			if tt.cancel {
				cancel()
			}

			got, err := tt.resolver.Resolve(ctx, Request{
				CallID:    "call-1",
				ToolName:  "ask_user",
				Arguments: args,
			})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve error = %v, want errors.Is %v", err, tt.wantErr)
				}
				if got.Payload != nil {
					t.Errorf("Resolve payload = %v, want nil alongside an error", got.Payload)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if answer := got.Payload.GetFields()["answer"].GetStringValue(); answer != tt.wantAnswer {
				t.Errorf("Resolve payload answer = %q, want %q", answer, tt.wantAnswer)
			}
		})
	}
}
