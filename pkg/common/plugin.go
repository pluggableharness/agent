// Package common holds the hand-written, cross-category glue that every
// PluggableHarness Agent plugin category (provider, tool, context, memory, frontend,
// widget) and the kernel-side plugin runtime compile against identically:
// the go-plugin handshake, the shared callback broker ID, and small helpers
// derived from the generated pluggableharness.agent.common.v1 types in ./proto/v1. It is
// deliberately tiny — anything category-specific belongs in that category's
// own pkg/<category> package, not here.
package common

import (
	"strings"

	"github.com/hashicorp/go-plugin"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// ProtocolVersion is the go-plugin handshake version this build speaks. It
// is bumped only together with a breaking proto v1->v2 change
// (.claude/rules/plugin-runtime.md's "Handshake" section) — never
// independently.
const ProtocolVersion uint = 1

const (
	magicCookieKey   = "PLUGGABLEHARNESS_AGENT_PLUGIN"
	magicCookieValue = "pluggableharness-agent-v1-a6f3c9d2-plugin-handshake"
)

// Handshake is the single plugin.HandshakeConfig every one of the six
// plugin categories MUST share — one magic cookie, one ProtocolVersion
// field. Different categories MUST NOT be given different cookies
// (.claude/rules/plugin-runtime.md).
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   magicCookieKey,
	MagicCookieValue: magicCookieValue,
}

// CallbackBrokerID is the well-known, FIXED go-plugin GRPCBroker stream ID
// on which the kernel serves KernelCallbackService back to every launched
// plugin, and the plugin dials to reach it. It is fixed (not
// wire-negotiated) because no ConfigureRequest — or any other message, in
// any of the six category protos — carries a broker-ID field (confirmed by
// direct proto read during design). A fixed, out-of-band constant both
// sides compile against removes the need for one, the same way the magic
// cookie above is agreed out-of-band rather than negotiated. Since the
// kernel is the only party that ever calls broker.AcceptAndServe (never
// broker.NextId()), this ID cannot collide with anything else.
const CallbackBrokerID uint32 = 1

// PluginKey returns the go-plugin plugin-map key for category c — the
// string both the kernel (host) and a plugin key their plugin.Plugin
// adapter on. Exactly one entry is ever present in a single launch's
// plugin map (one category per subprocess). An unrecognized or
// CATEGORY_UNSPECIFIED value falls back to the enum's own lowercased
// String() rather than panicking, since PluginKey has no way to signal an
// error and an unrecognized category should never reach this call in a
// correctly wired kernel or plugin.
func PluginKey(c commonv1.Category) string {
	switch c {
	case commonv1.Category_CATEGORY_PROVIDER:
		return "provider"
	case commonv1.Category_CATEGORY_TOOL:
		return "tool"
	case commonv1.Category_CATEGORY_CONTEXT:
		return "context"
	case commonv1.Category_CATEGORY_MEMORY:
		return "memory"
	case commonv1.Category_CATEGORY_FRONTEND:
		return "frontend"
	case commonv1.Category_CATEGORY_WIDGET:
		return "widget"
	default:
		return strings.ToLower(strings.TrimPrefix(c.String(), "CATEGORY_"))
	}
}
