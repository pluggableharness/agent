package pluginhost

import (
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"
)

// This file holds the adapters that let Registry satisfy interfaces
// declared by its consumers, structurally, without this package
// importing any of them.
//
// The direction matters: internal/tokencount declares its own
// one-method ModelLookup and explicitly documents "do NOT import
// internal/pluginhost or any concrete plugin registry here — a future
// phase's registry type will satisfy this interface structurally". The
// method below is that satisfaction. It lives here rather than in the
// consumer because the consumer is deliberately kept ignorant of any
// concrete registry, while this package already knows both the lookup
// and the client type; no import cycle is created either way, since
// nothing in internal/tokencount imports this package.

// ModelClientByLocalName resolves an agent.hcl required_providers local
// name to that plugin's ModelService client, satisfying
// internal/tokencount.ModelLookup structurally.
//
// ok is false when no plugin is loaded under that local name, and also
// when one is but is not a model plugin — an agent_profile naming a tool
// provider in a model{} block is a configuration mistake, and reporting
// it as "not found" is exactly what tokencount's resolution algorithm
// expects (it falls back to the documented heuristic rather than
// erroring).
func (r *Registry) ModelClientByLocalName(name string) (modelv1.ModelServiceClient, bool) {
	live, ok := r.ByLocalName(name)
	if !ok {
		return nil, false
	}
	return live.ModelClient()
}
