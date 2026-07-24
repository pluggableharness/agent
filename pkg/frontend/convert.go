package frontend

import (
	"errors"
	"fmt"

	frontendv1 "github.com/pluggableharness/agent/pkg/frontend/proto/v1"
)

// Sentinel errors identifying which invariant a malformed ClientEvent or
// ServerEvent violated — compare with errors.Is.
var (
	// ErrMissingSessionID is returned by fromClientEventProto when a
	// session-scoped variant (user_message..interrupt) arrives with an
	// empty top-level session_id — frontend-protocol.md's error taxonomy
	// names this FRONTEND_ERROR_CATEGORY_INVALID_CLIENT_EVENT explicitly.
	ErrMissingSessionID = errors.New("frontend: session-scoped client event missing session_id")
	// ErrUnexpectedSessionID is returned by fromClientEventProto when a
	// connection-level control variant (hello..list_sessions) arrives
	// with a non-empty top-level session_id, which none of the six ever
	// has a session to scope to before its own response arrives.
	ErrUnexpectedSessionID = errors.New("frontend: control client event carries unexpected session_id")
	// ErrEmptyClientEvent is returned by fromClientEventProto when the
	// generated ClientEvent's oneof carries no variant at all.
	ErrEmptyClientEvent = errors.New("frontend: client event carries no variant")
	// ErrEmptyServerEvent is returned by toServerEventProto/
	// fromServerEventProto when a ServerEvent carries a nil Payload.
	ErrEmptyServerEvent = errors.New("frontend: server event carries no payload")
	// ErrNilFrontendError is returned when an ErrorEvent's Err field is
	// nil — a caller-constructed ServerEvent that skipped the one field
	// ErrorEvent exists to carry.
	ErrNilFrontendError = errors.New("frontend: error event carries no Error")
)

// fromClientEventProto converts in into its domain form, validating the
// session_id placement invariant frontend-protocol.md's ClientEvent
// section documents (REQUIRED for session-scoped variants, empty for the
// six connection-level control variants).
func fromClientEventProto(in *frontendv1.ClientEvent) (ClientEvent, error) {
	sessionID := in.GetSessionId()

	switch ev := in.GetEvent().(type) {
	case *frontendv1.ClientEvent_UserMessage_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		return ClientEvent{SessionID: sessionID, Payload: UserMessage{
			Content: ev.UserMessage.GetContent(),
		}}, nil

	case *frontendv1.ClientEvent_SlashCommand_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		return ClientEvent{SessionID: sessionID, Payload: SlashCommand{
			Name: ev.SlashCommand.GetName(),
			Args: ev.SlashCommand.GetArgs(),
		}}, nil

	case *frontendv1.ClientEvent_PlanDecision_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		pd := ev.PlanDecision
		return ClientEvent{SessionID: sessionID, Payload: PlanDecision{
			PlanItemID:     pd.GetPlanItemId(),
			Decision:       pd.GetDecision(),
			CorrectedInput: pd.GetCorrectedInput(),
			Scope:          planScopeFromProto(pd.GetScope()),
		}}, nil

	case *frontendv1.ClientEvent_InteractiveResponse_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		ir := ev.InteractiveResponse
		return ClientEvent{SessionID: sessionID, Payload: InteractiveResponse{
			CallID:   ir.GetCallId(),
			Response: ir.GetResponse(),
		}}, nil

	case *frontendv1.ClientEvent_ActionTrigger_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		at := ev.ActionTrigger
		return ClientEvent{SessionID: sessionID, Payload: ActionTrigger{
			NodeID:   at.GetNodeId(),
			ToolName: at.GetToolName(),
			Args:     at.GetArgs(),
			Provider: at.GetProvider(),
		}}, nil

	case *frontendv1.ClientEvent_Interrupt_:
		if sessionID == "" {
			return ClientEvent{}, ErrMissingSessionID
		}
		return ClientEvent{SessionID: sessionID, Payload: Interrupt{}}, nil

	case *frontendv1.ClientEvent_Hello_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		return ClientEvent{Payload: Hello{ProtocolVersion: ev.Hello.GetProtocolVersion()}}, nil

	case *frontendv1.ClientEvent_CreateSession_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		cs := ev.CreateSession
		return ClientEvent{Payload: CreateSession{
			RequestID:        cs.GetRequestId(),
			Profile:          cs.Profile,
			InitialPrompt:    cs.InitialPrompt,
			WorkingDirectory: cs.WorkingDirectory,
		}}, nil

	case *frontendv1.ClientEvent_AttachSession_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		as := ev.AttachSession
		return ClientEvent{Payload: AttachSession{
			RequestID: as.GetRequestId(),
			SessionID: as.GetSessionId(),
		}}, nil

	case *frontendv1.ClientEvent_ResumeSession_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		rs := ev.ResumeSession
		return ClientEvent{Payload: ResumeSession{
			RequestID: rs.GetRequestId(),
			SessionID: rs.GetSessionId(),
		}}, nil

	case *frontendv1.ClientEvent_DetachSession_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		ds := ev.DetachSession
		return ClientEvent{Payload: DetachSession{
			RequestID: ds.GetRequestId(),
			SessionID: ds.GetSessionId(),
		}}, nil

	case *frontendv1.ClientEvent_ListSessions_:
		if sessionID != "" {
			return ClientEvent{}, ErrUnexpectedSessionID
		}
		ls := ev.ListSessions
		return ClientEvent{Payload: ListSessions{
			RequestID:       ls.GetRequestId(),
			Status:          ls.Status,
			ParentSessionID: ls.ParentSessionId,
			RootsOnly:       ls.GetRootsOnly(),
		}}, nil

	default:
		return ClientEvent{}, ErrEmptyClientEvent
	}
}

// toClientEventProto converts ev into its generated wire form. Provided
// for symmetry and so a test (in this package or a plugin author's own)
// can build a ClientEvent to send without importing frontendv1 directly.
func toClientEventProto(ev ClientEvent) (*frontendv1.ClientEvent, error) {
	out := &frontendv1.ClientEvent{SessionId: ev.SessionID}

	switch p := ev.Payload.(type) {
	case UserMessage:
		out.Event = &frontendv1.ClientEvent_UserMessage_{
			UserMessage: &frontendv1.ClientEvent_UserMessage{Content: p.Content},
		}
	case SlashCommand:
		out.Event = &frontendv1.ClientEvent_SlashCommand_{
			SlashCommand: &frontendv1.ClientEvent_SlashCommand{Name: p.Name, Args: p.Args},
		}
	case PlanDecision:
		out.Event = &frontendv1.ClientEvent_PlanDecision_{
			PlanDecision: &frontendv1.ClientEvent_PlanDecision{
				PlanItemId:     p.PlanItemID,
				Decision:       p.Decision,
				CorrectedInput: p.CorrectedInput,
				Scope:          planScopeToProto(p.Scope),
			},
		}
	case InteractiveResponse:
		out.Event = &frontendv1.ClientEvent_InteractiveResponse_{
			InteractiveResponse: &frontendv1.ClientEvent_InteractiveResponse{
				CallId:   p.CallID,
				Response: p.Response,
			},
		}
	case ActionTrigger:
		out.Event = &frontendv1.ClientEvent_ActionTrigger_{
			ActionTrigger: &frontendv1.ClientEvent_ActionTrigger{
				NodeId:   p.NodeID,
				ToolName: p.ToolName,
				Args:     p.Args,
				Provider: p.Provider,
			},
		}
	case Interrupt:
		out.Event = &frontendv1.ClientEvent_Interrupt_{Interrupt: &frontendv1.ClientEvent_Interrupt{}}
	case Hello:
		out.Event = &frontendv1.ClientEvent_Hello_{
			Hello: &frontendv1.ClientEvent_Hello{ProtocolVersion: p.ProtocolVersion},
		}
	case CreateSession:
		out.Event = &frontendv1.ClientEvent_CreateSession_{
			CreateSession: &frontendv1.ClientEvent_CreateSession{
				RequestId:        p.RequestID,
				Profile:          p.Profile,
				InitialPrompt:    p.InitialPrompt,
				WorkingDirectory: p.WorkingDirectory,
			},
		}
	case AttachSession:
		out.Event = &frontendv1.ClientEvent_AttachSession_{
			AttachSession: &frontendv1.ClientEvent_AttachSession{RequestId: p.RequestID, SessionId: p.SessionID},
		}
	case ResumeSession:
		out.Event = &frontendv1.ClientEvent_ResumeSession_{
			ResumeSession: &frontendv1.ClientEvent_ResumeSession{RequestId: p.RequestID, SessionId: p.SessionID},
		}
	case DetachSession:
		out.Event = &frontendv1.ClientEvent_DetachSession_{
			DetachSession: &frontendv1.ClientEvent_DetachSession{RequestId: p.RequestID, SessionId: p.SessionID},
		}
	case ListSessions:
		out.Event = &frontendv1.ClientEvent_ListSessions_{
			ListSessions: &frontendv1.ClientEvent_ListSessions{
				RequestId:       p.RequestID,
				Status:          p.Status,
				ParentSessionId: p.ParentSessionID,
				RootsOnly:       p.RootsOnly,
			},
		}
	default:
		return nil, fmt.Errorf("frontend: to client event proto: %w", ErrEmptyClientEvent)
	}

	return out, nil
}

// fromServerEventProto converts in into its domain form.
func fromServerEventProto(in *frontendv1.ServerEvent) (ServerEvent, error) {
	out := ServerEvent{SessionID: in.GetSessionId(), RequestID: in.RequestId}

	switch ev := in.GetEvent().(type) {
	case *frontendv1.ServerEvent_StreamDelta_:
		out.Payload = StreamDelta{TargetID: ev.StreamDelta.GetTargetId(), Text: ev.StreamDelta.GetText()}
	case *frontendv1.ServerEvent_Render_:
		out.Payload = Render{Content: ev.Render.GetContent()}
	case *frontendv1.ServerEvent_PermissionRequest_:
		out.Payload = PermissionRequest{PlanItem: ev.PermissionRequest.GetPlanItem()}
	case *frontendv1.ServerEvent_PlanReady_:
		out.Payload = PlanReady{Plan: ev.PlanReady.GetPlan()}
	case *frontendv1.ServerEvent_InteractiveRequest_:
		out.Payload = InteractiveRequest{
			CallID:   ev.InteractiveRequest.GetCallId(),
			ToolName: ev.InteractiveRequest.GetToolName(),
			Prompt:   ev.InteractiveRequest.GetPrompt(),
		}
	case *frontendv1.ServerEvent_SessionTreeUpdate_:
		out.Payload = SessionTreeUpdate{
			ParentSessionID: ev.SessionTreeUpdate.GetParentSessionId(),
			ChildSessionID:  ev.SessionTreeUpdate.GetChildSessionId(),
			Status:          ev.SessionTreeUpdate.GetStatus(),
		}
	case *frontendv1.ServerEvent_Error_:
		fe := ev.Error.GetError()
		out.Payload = ErrorEvent{Err: &Error{Category: fe.GetCategory(), Message: fe.GetMessage()}}
	case *frontendv1.ServerEvent_SessionCreated_:
		out.Payload = SessionCreated{Info: ev.SessionCreated.GetInfo()}
	case *frontendv1.ServerEvent_SessionAttached_:
		out.Payload = SessionAttached{Info: ev.SessionAttached.GetInfo()}
	case *frontendv1.ServerEvent_BackfillComplete_:
		out.Payload = BackfillComplete{LastSequence: ev.BackfillComplete.GetLastSequence()}
	case *frontendv1.ServerEvent_SessionDetached_:
		out.Payload = SessionDetached{}
	case *frontendv1.ServerEvent_SessionList_:
		out.Payload = SessionList{Sessions: ev.SessionList.GetSessions()}
	case *frontendv1.ServerEvent_SlashCommandRegistry_:
		out.Payload = SlashCommandRegistry{
			DirectInvokeCommands:    ev.SlashCommandRegistry.GetDirectInvokeCommands(),
			PromptExpansionCommands: ev.SlashCommandRegistry.GetPromptExpansionCommands(),
		}
	case *frontendv1.ServerEvent_UsageUpdate_:
		out.Payload = UsageUpdate{
			Turn:              ev.UsageUpdate.GetTurn(),
			CumulativeCostUSD: ev.UsageUpdate.GetCumulativeCostUsd(),
			UsedTokens:        ev.UsageUpdate.GetUsedTokens(),
			EffectiveCeiling:  ev.UsageUpdate.GetEffectiveCeiling(),
		}
	case *frontendv1.ServerEvent_SessionStatusUpdate_:
		out.Payload = SessionStatusUpdate{Status: ev.SessionStatusUpdate.GetStatus()}
	default:
		return ServerEvent{}, ErrEmptyServerEvent
	}

	return out, nil
}

// toServerEventProto converts ev into its generated wire form.
func toServerEventProto(ev ServerEvent) (*frontendv1.ServerEvent, error) {
	out := &frontendv1.ServerEvent{SessionId: ev.SessionID, RequestId: ev.RequestID}

	switch p := ev.Payload.(type) {
	case StreamDelta:
		out.Event = &frontendv1.ServerEvent_StreamDelta_{
			StreamDelta: &frontendv1.ServerEvent_StreamDelta{TargetId: p.TargetID, Text: p.Text},
		}
	case Render:
		out.Event = &frontendv1.ServerEvent_Render_{Render: &frontendv1.ServerEvent_Render{Content: p.Content}}
	case PermissionRequest:
		out.Event = &frontendv1.ServerEvent_PermissionRequest_{
			PermissionRequest: &frontendv1.ServerEvent_PermissionRequest{PlanItem: p.PlanItem},
		}
	case PlanReady:
		out.Event = &frontendv1.ServerEvent_PlanReady_{PlanReady: &frontendv1.ServerEvent_PlanReady{Plan: p.Plan}}
	case InteractiveRequest:
		out.Event = &frontendv1.ServerEvent_InteractiveRequest_{
			InteractiveRequest: &frontendv1.ServerEvent_InteractiveRequest{
				CallId:   p.CallID,
				ToolName: p.ToolName,
				Prompt:   p.Prompt,
			},
		}
	case SessionTreeUpdate:
		out.Event = &frontendv1.ServerEvent_SessionTreeUpdate_{
			SessionTreeUpdate: &frontendv1.ServerEvent_SessionTreeUpdate{
				ParentSessionId: p.ParentSessionID,
				ChildSessionId:  p.ChildSessionID,
				Status:          p.Status,
			},
		}
	case ErrorEvent:
		if p.Err == nil {
			return nil, ErrNilFrontendError
		}
		out.Event = &frontendv1.ServerEvent_Error_{
			Error: &frontendv1.ServerEvent_Error{
				Error: &frontendv1.FrontendError{Category: p.Err.Category, Message: p.Err.Message},
			},
		}
	case SessionCreated:
		out.Event = &frontendv1.ServerEvent_SessionCreated_{
			SessionCreated: &frontendv1.ServerEvent_SessionCreated{Info: p.Info},
		}
	case SessionAttached:
		out.Event = &frontendv1.ServerEvent_SessionAttached_{
			SessionAttached: &frontendv1.ServerEvent_SessionAttached{Info: p.Info},
		}
	case BackfillComplete:
		out.Event = &frontendv1.ServerEvent_BackfillComplete_{
			BackfillComplete: &frontendv1.ServerEvent_BackfillComplete{LastSequence: p.LastSequence},
		}
	case SessionDetached:
		out.Event = &frontendv1.ServerEvent_SessionDetached_{SessionDetached: &frontendv1.ServerEvent_SessionDetached{}}
	case SessionList:
		out.Event = &frontendv1.ServerEvent_SessionList_{
			SessionList: &frontendv1.ServerEvent_SessionList{Sessions: p.Sessions},
		}
	case SlashCommandRegistry:
		out.Event = &frontendv1.ServerEvent_SlashCommandRegistry_{
			SlashCommandRegistry: &frontendv1.ServerEvent_SlashCommandRegistry{
				DirectInvokeCommands:    p.DirectInvokeCommands,
				PromptExpansionCommands: p.PromptExpansionCommands,
			},
		}
	case UsageUpdate:
		out.Event = &frontendv1.ServerEvent_UsageUpdate_{
			UsageUpdate: &frontendv1.ServerEvent_UsageUpdate{
				Turn:              p.Turn,
				CumulativeCostUsd: p.CumulativeCostUSD,
				UsedTokens:        p.UsedTokens,
				EffectiveCeiling:  p.EffectiveCeiling,
			},
		}
	case SessionStatusUpdate:
		out.Event = &frontendv1.ServerEvent_SessionStatusUpdate_{
			SessionStatusUpdate: &frontendv1.ServerEvent_SessionStatusUpdate{Status: p.Status},
		}
	default:
		return nil, ErrEmptyServerEvent
	}

	return out, nil
}

// planScopeFromProto converts a wire PlanDecisionScope to its domain
// PlanScope, mapping both PLAN_DECISION_SCOPE_UNSPECIFIED and
// PLAN_DECISION_SCOPE_ONCE to PlanScopeOnce — see PlanScope's doc comment.
func planScopeFromProto(s frontendv1.PlanDecisionScope) PlanScope {
	switch s {
	case frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION:
		return PlanScopeSession
	case frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS:
		return PlanScopeAlways
	default:
		return PlanScopeOnce
	}
}

// planScopeToProto converts a domain PlanScope to its wire
// PlanDecisionScope. Never produces PLAN_DECISION_SCOPE_UNSPECIFIED — the
// domain type's zero value, PlanScopeOnce, already carries the
// spec-mandated default.
func planScopeToProto(s PlanScope) frontendv1.PlanDecisionScope {
	switch s {
	case PlanScopeSession:
		return frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_SESSION
	case PlanScopeAlways:
		return frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ALWAYS
	default:
		return frontendv1.PlanDecisionScope_PLAN_DECISION_SCOPE_ONCE
	}
}
