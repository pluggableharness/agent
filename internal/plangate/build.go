package plangate

import (
	"context"
	"fmt"

	planv1 "github.com/pluggableharness/agent/pkg/plan/proto/v1"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"

	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/telemetry"
)

// ProvisionalItem is one PENDING plan item minted by the turn driver
// before a Plan formally exists — at turn-algorithm.md's step 7, so
// PreToolCallPayload.plan_item can be populated for the pre-tool-call hook
// — paired with the handles Build needs to finish it.
//
// The snapshot fields [plan-apply-gate.md#snapshot-rationale] requires
// (kind, risk, description) are ALREADY captured on Item by the time it
// reaches Build; Build's only additive job is preview. Build carries Item
// forward by identity, not by copy: the *planv1.PlanItem a caller hands in
// is the same pointer the returned Plan holds, and the same one Decide
// later stamps a decision onto. That identity is what lets a caller keep
// its own handle on an item across all three phases.
type ProvisionalItem struct {
	// Item is the PENDING plan item. MUST NOT be nil.
	Item *planv1.PlanItem
	// Provider is the operation's agent.hcl local name — the same value
	// carried on Item.provider, kept here so Build never has to trust
	// two fields to agree before it can name a provider in a log line.
	Provider string
	// Handle is the resolved tool handle for this operation, used only
	// for Preview: SupportsPreview decides whether the RPC is attempted
	// at all, and Client issues it.
	Handle providercatalog.ToolHandle
}

// BuildRequest is one turn's worth of provisional items.
type BuildRequest struct {
	// TurnID is the turn this plan belongs to. MUST NOT be empty.
	TurnID string
	// Items are the turn's provisional plan items, in identification
	// order — the order the returned Plan preserves.
	Items []ProvisionalItem
}

// Build assembles req into a Plan, populating each TOOL_KIND_RESOURCE
// item's preview from its provider's Preview RPC
// ([plan-apply-gate.md#preview-flow]).
//
// Preview is best-effort by specification: a provider that does not
// implement it, a timeout, and an RPC failure all degrade to an ABSENT
// preview for that item and never abort plan construction. A frontend
// falls back to rendering the item's raw input, exactly as it would for a
// plan built before Preview existed.
//
// Build DOES fail on a caller error it must not paper over: a
// data_source or interactive item arriving with preview already populated
// returns ErrPreviewNotAllowed rather than being silently cleared, because
// [plan-apply-gate.md#preview-flow] makes preview a resource-item concept
// and a populated one on any other kind means the caller built the item
// wrong.
func (g *Gate) Build(ctx context.Context, req BuildRequest) (_ *planv1.Plan, err error) {
	ctx, span := g.telem.StartPlanBuild(ctx, req.TurnID)
	defer func() { telemetry.EndSpan(span, err) }()
	g.logger.DebugContext(ctx, "plangate: building plan",
		"session_id", g.sessionID, "turn_id", req.TurnID, "item_count", len(req.Items))

	if req.TurnID == "" {
		return nil, fmt.Errorf("plangate: build: %w", ErrNoTurnID)
	}

	items := make([]*planv1.PlanItem, 0, len(req.Items))
	for i, prov := range req.Items {
		if prov.Item == nil {
			return nil, fmt.Errorf("plangate: build: item %d: %w", i, ErrNilItem)
		}
		if prov.Item.GetKind() == toolv1.ToolKind_TOOL_KIND_RESOURCE {
			g.attachPreview(ctx, prov)
		} else if prov.Item.GetPreview() != nil {
			return nil, fmt.Errorf("plangate: build: item %d (%s.%s, kind %v): %w",
				i, prov.Provider, prov.Item.GetOperationName(), prov.Item.GetKind(), ErrPreviewNotAllowed)
		}
		items = append(items, prov.Item)
	}

	return &planv1.Plan{TurnId: req.TurnID, Items: items}, nil
}

// attachPreview issues one provider's Preview RPC under this Gate's
// per-RPC deadline and stores the result on prov.Item, or leaves preview
// absent if the provider has none, the deadline expires, or the call
// fails. It never returns an error: every failure mode here is an absent
// preview by specification, and swallowing it here is what keeps that MUST
// NOT ("never to an aborted plan") true in one place instead of at every
// call site.
func (g *Gate) attachPreview(ctx context.Context, prov ProvisionalItem) {
	if !prov.Handle.SupportsPreview || prov.Handle.Client == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, g.previewTimeout)
	defer cancel()

	ctx, span := g.telem.StartToolPreview(ctx, prov.Item.GetOperationName(), prov.Handle.Producer)
	resp, err := prov.Handle.Client.Preview(ctx, &toolv1.PreviewRequest{
		Call: &toolv1.ToolCall{
			Id:        prov.Item.GetCallId(),
			ToolName:  prov.Item.GetOperationName(),
			Arguments: prov.Item.GetInput(),
		},
	})
	telemetry.EndSpan(span, err)
	if err != nil {
		g.logger.WarnContext(ctx, "plangate: preview failed; building item without one",
			"session_id", g.sessionID,
			"provider", prov.Provider,
			"operation", prov.Item.GetOperationName(),
			"call_id", prov.Item.GetCallId(),
			"err", err)
		return
	}

	prov.Item.Preview = resp.GetPreview()
}
