// Package fake implements providercatalog.Catalog as a scripted,
// in-memory test double — a hand-written fake per
// .claude/rules/go-testing.md, not a generated mock with call
// recording.
//
// Every future agent-loop package (turn driver, hook dispatch,
// plan/apply gate, tool scheduler, model caller, context assembler)
// builds its unit tests on this: declare which models, tools, context
// providers, and hook subscribers are "loaded", then run the real logic
// against them with no subprocess, no gRPC dial, and no plugin binary
// on disk.
//
// # Construction
//
// Two styles, deliberately both supported:
//
//	// Chained adders — the common case. Each Add stamps the lookup key
//	// into the handle, so a scenario names a provider once.
//	cat := fake.New().
//		AddModel(ref, providercatalog.ModelHandle{Spec: spec}).
//		AddTool("fs", "read_file", providercatalog.ToolHandle{Schema: schema}).
//		AddContext(providercatalog.ContextHandle{Provider: "git"}).
//		AddHook("fs", providercatalog.HookHandle{Client: hookClient})
//
//	// Struct literal — for a scenario that needs a state the adders
//	// cannot express, e.g. non-sequential ContextHandle.Position or a
//	// handle keyed under a name that disagrees with its own fields.
//	cat := &fake.Catalog{
//		Models: map[agentprofile.ModelRef]providercatalog.ModelHandle{ref: {Spec: spec}},
//	}
//
// The fields are exported for exactly the second case. A zero
// fake.Catalog is usable and reports ErrNotFound for every lookup,
// which is what a "nothing is loaded" scenario wants.
//
// # What it does not do
//
// It validates nothing, dials nothing, and never rejects a handle for
// being internally inconsistent — a ToolHandle with a nil Schema, or a
// ModelHandle whose Spec disagrees with its Ref, round-trips exactly as
// scripted. A test asserting how a consumer copes with a malformed
// handle is a legitimate use of this fake, so it must be able to hold
// one.
package fake
