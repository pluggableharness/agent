package frontend

import (
	"errors"
	"reflect"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	contentv1 "github.com/pluggableharness/agent/pkg/content/proto/v1"
	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
	renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"
	sessionv1 "github.com/pluggableharness/agent/pkg/session/proto/v1"
	slashcommandv1 "github.com/pluggableharness/agent/pkg/slashcommand/proto/v1"
)

func strp(s string) *string { return &s }

func TestClientEventRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event ClientEvent
	}{
		{"user_message", ClientEvent{SessionID: "sess-1", Payload: UserMessage{
			Content: []*contentv1.ContentBlock{{Block: &contentv1.ContentBlock_Text{Text: &contentv1.TextBlock{Text: "hi"}}}},
		}}},
		{"slash_command", ClientEvent{SessionID: "sess-1", Payload: SlashCommand{Name: "help", Args: "me"}}},
		{"plan_decision", ClientEvent{SessionID: "sess-1", Payload: PlanDecision{
			PlanItemID:     "item-1",
			Decision:       frontendv1.ClientDecision_CLIENT_DECISION_ALLOW,
			CorrectedInput: &structpb.Struct{},
			Scope:          PlanScopeSession,
		}}},
		{"interactive_response", ClientEvent{SessionID: "sess-1", Payload: InteractiveResponse{
			CallID: "call-1", Response: &structpb.Struct{},
		}}},
		{"action_trigger", ClientEvent{SessionID: "sess-1", Payload: ActionTrigger{
			NodeID: "node-1", ToolName: "grep", Args: &structpb.Struct{}, Provider: "ripgrep",
		}}},
		{"interrupt", ClientEvent{SessionID: "sess-1", Payload: Interrupt{}}},
		{"hello", ClientEvent{Payload: Hello{ProtocolVersion: 3}}},
		{"create_session", ClientEvent{Payload: CreateSession{
			RequestID: "req-1", Profile: strp("default"), InitialPrompt: strp("hi"), WorkingDirectory: strp("/tmp"),
		}}},
		{"attach_session", ClientEvent{Payload: AttachSession{RequestID: "req-2", SessionID: "sess-2"}}},
		{"resume_session", ClientEvent{Payload: ResumeSession{RequestID: "req-3", SessionID: "sess-3"}}},
		{"detach_session", ClientEvent{Payload: DetachSession{RequestID: "req-4", SessionID: "sess-4"}}},
		{"list_sessions", ClientEvent{Payload: ListSessions{
			RequestID: "req-5", ParentSessionID: strp("sess-0"), RootsOnly: true,
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proto, err := toClientEventProto(tt.event)
			if err != nil {
				t.Fatalf("toClientEventProto() error = %v", err)
			}
			got, err := fromClientEventProto(proto)
			if err != nil {
				t.Fatalf("fromClientEventProto() error = %v", err)
			}
			if got.SessionID != tt.event.SessionID {
				t.Errorf("SessionID = %q, want %q", got.SessionID, tt.event.SessionID)
			}
			if !reflect.DeepEqual(got.Payload, tt.event.Payload) {
				t.Errorf("Payload = %#v, want %#v", got.Payload, tt.event.Payload)
			}
		})
	}
}

func TestClientEvent_SessionIDValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      *frontendv1.ClientEvent
		wantErr error
	}{
		{
			name: "session-scoped missing session_id",
			in: &frontendv1.ClientEvent{
				Event: &frontendv1.ClientEvent_UserMessage_{UserMessage: &frontendv1.ClientEvent_UserMessage{}},
			},
			wantErr: ErrMissingSessionID,
		},
		{
			name: "control variant with unexpected session_id",
			in: &frontendv1.ClientEvent{
				SessionId: "sess-1",
				Event:     &frontendv1.ClientEvent_Hello_{Hello: &frontendv1.ClientEvent_Hello{}},
			},
			wantErr: ErrUnexpectedSessionID,
		},
		{
			name:    "empty oneof",
			in:      &frontendv1.ClientEvent{},
			wantErr: ErrEmptyClientEvent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := fromClientEventProto(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("fromClientEventProto() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestToClientEventProto_EmptyPayload(t *testing.T) {
	t.Parallel()

	_, err := toClientEventProto(ClientEvent{})
	if !errors.Is(err, ErrEmptyClientEvent) {
		t.Errorf("toClientEventProto(empty) error = %v, want %v", err, ErrEmptyClientEvent)
	}
}

func TestServerEventRoundTrip(t *testing.T) {
	t.Parallel()

	requestID := strp("req-9")

	tests := []struct {
		name  string
		event ServerEvent
	}{
		{"stream_delta", ServerEvent{SessionID: "s1", Payload: StreamDelta{TargetID: "t1", Text: "chunk"}}},
		{"render", ServerEvent{SessionID: "s1", Payload: Render{Content: &renderv1.PlacedContent{Region: renderv1.Region_REGION_MAIN_CHAT}}}},
		{"permission_request", ServerEvent{SessionID: "s1", Payload: PermissionRequest{}}},
		{"plan_ready", ServerEvent{SessionID: "s1", Payload: PlanReady{}}},
		{"interactive_request", ServerEvent{SessionID: "s1", Payload: InteractiveRequest{CallID: "c1", ToolName: "ask"}}},
		{"session_tree_update", ServerEvent{SessionID: "s1", Payload: SessionTreeUpdate{
			ParentSessionID: "p1", ChildSessionID: "c1", Status: sessionv1.SessionStatus_SESSION_STATUS_RUNNING,
		}}},
		{"error", ServerEvent{SessionID: "s1", RequestID: requestID, Payload: ErrorEvent{
			Err: &Error{Category: frontendv1.FrontendErrorCategory_FRONTEND_ERROR_CATEGORY_RENDER_FAILED, Message: "bad diff"},
		}}},
		{"session_created", ServerEvent{SessionID: "s1", RequestID: requestID, Payload: SessionCreated{Info: &sessionv1.SessionInfo{SessionId: "s1"}}}},
		{"session_attached", ServerEvent{SessionID: "s1", RequestID: requestID, Payload: SessionAttached{Info: &sessionv1.SessionInfo{SessionId: "s1"}}}},
		{"backfill_complete", ServerEvent{SessionID: "s1", RequestID: requestID, Payload: BackfillComplete{LastSequence: 42}}},
		{"session_detached", ServerEvent{SessionID: "s1", RequestID: requestID, Payload: SessionDetached{}}},
		{"session_list", ServerEvent{RequestID: requestID, Payload: SessionList{Sessions: []*sessionv1.SessionInfo{{SessionId: "s1"}}}}},
		{"slash_command_registry", ServerEvent{SessionID: "s1", Payload: SlashCommandRegistry{
			DirectInvokeCommands:    []*slashcommandv1.SlashCommandSpec{{Name: "run"}},
			PromptExpansionCommands: []*commonv1.PromptExpansionSpec{{Name: "help"}},
		}}},
		{"usage_update", ServerEvent{SessionID: "s1", Payload: UsageUpdate{
			CumulativeCostUSD: 1.5, UsedTokens: 100, EffectiveCeiling: 200,
		}}},
		{"session_status_update", ServerEvent{SessionID: "s1", Payload: SessionStatusUpdate{Status: sessionv1.SessionStatus_SESSION_STATUS_COMPLETED}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proto, err := toServerEventProto(tt.event)
			if err != nil {
				t.Fatalf("toServerEventProto() error = %v", err)
			}
			got, err := fromServerEventProto(proto)
			if err != nil {
				t.Fatalf("fromServerEventProto() error = %v", err)
			}
			if got.SessionID != tt.event.SessionID {
				t.Errorf("SessionID = %q, want %q", got.SessionID, tt.event.SessionID)
			}
			gotReq, wantReq := got.RequestID, tt.event.RequestID
			switch {
			case gotReq == nil && wantReq == nil:
			case gotReq == nil || wantReq == nil:
				t.Errorf("RequestID = %v, want %v", gotReq, wantReq)
			case *gotReq != *wantReq:
				t.Errorf("RequestID = %q, want %q", *gotReq, *wantReq)
			}
		})
	}
}

func TestToServerEventProto_EmptyPayload(t *testing.T) {
	t.Parallel()

	_, err := toServerEventProto(ServerEvent{})
	if !errors.Is(err, ErrEmptyServerEvent) {
		t.Errorf("toServerEventProto(empty) error = %v, want %v", err, ErrEmptyServerEvent)
	}
}

func TestToServerEventProto_NilFrontendError(t *testing.T) {
	t.Parallel()

	_, err := toServerEventProto(ServerEvent{Payload: ErrorEvent{}})
	if !errors.Is(err, ErrNilFrontendError) {
		t.Errorf("toServerEventProto(ErrorEvent{}) error = %v, want %v", err, ErrNilFrontendError)
	}
}

func TestFromServerEventProto_EmptyOneof(t *testing.T) {
	t.Parallel()

	_, err := fromServerEventProto(&frontendv1.ServerEvent{})
	if !errors.Is(err, ErrEmptyServerEvent) {
		t.Errorf("fromServerEventProto(empty) error = %v, want %v", err, ErrEmptyServerEvent)
	}
}

func TestPlanScopeConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain PlanScope
		proto  frontendv1.PlanDecisionScope
	}{
		{PlanScopeOnce, frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE},
		{PlanScopeSession, frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION},
		{PlanScopeAlways, frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS},
	}
	for _, tt := range tests {
		if got := planScopeToProto(tt.domain); got != tt.proto {
			t.Errorf("planScopeToProto(%v) = %v, want %v", tt.domain, got, tt.proto)
		}
		if got := planScopeFromProto(tt.proto); got != tt.domain {
			t.Errorf("planScopeFromProto(%v) = %v, want %v", tt.proto, got, tt.domain)
		}
	}

	// The generated enum's own zero value, UNSPECIFIED, maps to
	// PlanScopeOnce too — doc.go's "PlanDecisionScope defaults to ONCE".
	if got := planScopeFromProto(frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_UNSPECIFIED); got != PlanScopeOnce {
		t.Errorf("planScopeFromProto(UNSPECIFIED) = %v, want PlanScopeOnce", got)
	}
	if PlanScopeOnce != 0 {
		t.Errorf("PlanScopeOnce = %d, want 0 (the Go zero value)", PlanScopeOnce)
	}
}

func TestPlanScope_String(t *testing.T) {
	t.Parallel()

	tests := map[PlanScope]string{
		PlanScopeOnce:    "once",
		PlanScopeSession: "session",
		PlanScopeAlways:  "always",
		PlanScope(99):    "unknown",
	}
	for scope, want := range tests {
		if got := scope.String(); got != want {
			t.Errorf("PlanScope(%d).String() = %q, want %q", scope, got, want)
		}
	}
}
