package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/policy"
	toolv1 "github.com/pluggableharness/agent/pkg/tool/proto/v1"
)

var policySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "match", Required: true},
		{Name: "action", Required: true},
	},
}

// decodePolicy decodes a policy "<name>" { match = {...}, action = "..." }
// block into internal/policy's Rule type (configuration.md §7).
func decodePolicy(name string, body hcl.Body) (policy.Rule, error) {
	content, diags := body.Content(policySchema)
	if diags.HasErrors() {
		return policy.Rule{}, fmt.Errorf("config: policy %q: %w", name, diags)
	}

	match, err := decodeMatch(content.Attributes["match"])
	if err != nil {
		return policy.Rule{}, fmt.Errorf("config: policy %q: %w", name, err)
	}

	actionStr, err := attrString(content.Attributes["action"])
	if err != nil {
		return policy.Rule{}, fmt.Errorf("config: policy %q: action: %w", name, err)
	}
	action, err := parseAction(actionStr)
	if err != nil {
		return policy.Rule{}, fmt.Errorf("config: policy %q: %w", name, err)
	}

	return policy.Rule{Name: name, Match: match, Action: action}, nil
}

// decodeMatch decodes a policy block's `match = { ... }` object attribute
// into a policy.Match. Every field is optional (configuration.md §7.1: an
// omitted field matches anything).
func decodeMatch(attr *hcl.Attribute) (policy.Match, error) {
	val, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return policy.Match{}, fmt.Errorf("match: %w", diags)
	}
	if !val.Type().IsObjectType() && !val.Type().IsMapType() {
		return policy.Match{}, fmt.Errorf("match: %w", ErrInvalidValue)
	}

	fields := val.AsValueMap()
	var m policy.Match

	if v, ok := fields["tool_name"]; ok {
		s := v.AsString()
		m.ToolName = &s
	}
	if v, ok := fields["provider"]; ok {
		s := v.AsString()
		m.Provider = &s
	}
	if v, ok := fields["risk"]; ok {
		risk, err := parseRiskClass(v.AsString())
		if err != nil {
			return policy.Match{}, fmt.Errorf("match: %w", err)
		}
		m.Risk = &risk
	}
	if v, ok := fields["kind"]; ok {
		kind, err := parsePolicyKind(v.AsString())
		if err != nil {
			return policy.Match{}, fmt.Errorf("match: %w", err)
		}
		m.Kind = &kind
	}

	return m, nil
}

func parseAction(s string) (policy.Action, error) {
	switch s {
	case "allow":
		return policy.ActionAllow, nil
	case "ask":
		return policy.ActionAsk, nil
	case "deny":
		return policy.ActionDeny, nil
	default:
		return policy.ActionUnspecified, fmt.Errorf("action: %w: %q", ErrInvalidValue, s)
	}
}

// parsePolicyKind parses match.kind's 2-value subset (configuration.md
// §7.1) — NOT tool.md's 3-value ToolKind; see internal/policy.Kind's doc
// comment for why "interactive" is deliberately not a legal value here.
func parsePolicyKind(s string) (policy.Kind, error) {
	switch s {
	case "resource":
		return policy.KindResource, nil
	case "data_source":
		return policy.KindDataSource, nil
	default:
		return policy.KindUnspecified, fmt.Errorf("kind: %w: %q", ErrInvalidValue, s)
	}
}

func parseRiskClass(s string) (toolv1.RiskClass, error) {
	switch s {
	case "read_only":
		return toolv1.RiskClass_RISK_CLASS_READ_ONLY, nil
	case "low":
		return toolv1.RiskClass_RISK_CLASS_LOW, nil
	case "moderate":
		return toolv1.RiskClass_RISK_CLASS_MODERATE, nil
	case "high":
		return toolv1.RiskClass_RISK_CLASS_HIGH, nil
	case "critical":
		return toolv1.RiskClass_RISK_CLASS_CRITICAL, nil
	default:
		return toolv1.RiskClass_RISK_CLASS_UNSPECIFIED, fmt.Errorf("risk: %w: %q", ErrInvalidValue, s)
	}
}
