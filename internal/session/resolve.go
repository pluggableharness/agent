package session

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	modelv1 "github.com/pluggableharness/agent/pkg/model/proto/v1"

	"github.com/pluggableharness/agent/internal/agentprofile"
	"github.com/pluggableharness/agent/internal/bounds"
	"github.com/pluggableharness/agent/internal/providercatalog"
	"github.com/pluggableharness/agent/internal/sessionscope"
)

// resolution is everything a session's identity and capability set is
// fixed to before its first turn: which profile, which model, which tools,
// which bounds, which plugins get a callback grant. It is computed once,
// before the session file exists, so a resolution failure never leaves a
// half-created session or an outstanding grant behind.
type resolution struct {
	// profileName is the resolved profile's name as persisted to
	// session_meta.profile and reported to session-start subscribers.
	profileName string
	// profile is the resolved profile itself.
	profile agentprofile.AgentProfile
	// model is the live handle every turn calls.
	model providercatalog.ModelHandle
	// target is the id/context_window/effective_ceiling triple every
	// context provider budgets against.
	target *modelv1.ModelTarget
	// tools are the operations in scope, keyed by the
	// "<provider>.<tool>" name the model sees — exactly
	// agentprofile.ResolveTools's resolved key.
	tools map[string]providercatalog.ToolHandle
	// limits are the profile's three loop bounds.
	limits bounds.Limits
	// remainingDepth is this root session's depth budget.
	remainingDepth int
	// keys are the plugins that get a session-lifetime callback grant, in
	// a deterministic order.
	keys []sessionscope.Key
}

// resolve computes a session's whole capability set from spec and the
// currently-loaded provider catalog.
func (r *Runner) resolve(spec Spec) (resolution, error) {
	name, profile, err := r.resolveProfile(spec.Profile)
	if err != nil {
		return resolution{}, err
	}

	tools, err := r.resolveTools(profile)
	if err != nil {
		return resolution{}, err
	}

	model, err := r.resolveModel(profile, len(tools) > 0)
	if err != nil {
		return resolution{}, err
	}

	return resolution{
		profileName: name,
		profile:     profile,
		model:       model,
		target:      modelTarget(model),
		tools:       tools,
		limits: bounds.Limits{
			MaxTurns:     profile.MaxTurns,
			MaxCostUSD:   profile.MaxCostUSD,
			MaxWallClock: time.Duration(profile.MaxWallClockS) * time.Second,
		},
		remainingDepth: agentprofile.RootRemainingDepth(profile, r.kernelDefaultMaxDepth),
		keys:           r.grantKeys(model, tools),
	}, nil
}

// resolveProfile implements
// agent-profiles.md#the-implicit-root-profile: an empty name means
// "default", a configured block wins, and an absent "default" block falls
// back to BuiltinDefaultProfile. Any other absent name is an error — a
// caller naming a profile that doesn't exist has a typo, not an intent to
// run under kernel defaults.
func (r *Runner) resolveProfile(name string) (string, agentprofile.AgentProfile, error) {
	if name == "" {
		name = DefaultProfileName
	}
	if profile, ok := r.profiles[name]; ok {
		return name, profile, nil
	}
	if name == DefaultProfileName {
		return name, BuiltinDefaultProfile(), nil
	}
	return "", agentprofile.AgentProfile{}, fmt.Errorf("session: resolve profile %q: %w", name, ErrUnknownProfile)
}

// resolveTools expands the profile's tool scoping against the loaded
// providers' advertised operations and resolves each surviving entry to a
// live handle. Entries are walked in sorted order so a config with two bad
// entries always reports the same one first (determinism.md).
func (r *Runner) resolveTools(profile agentprofile.AgentProfile) (map[string]providercatalog.ToolHandle, error) {
	scoped, err := agentprofile.ResolveTools(profile.Tools, r.catalog.ToolNames())
	if err != nil {
		return nil, fmt.Errorf("session: resolve tools: %w", err)
	}

	tools := make(map[string]providercatalog.ToolHandle, len(scoped))
	for _, name := range slices.Sorted(maps.Keys(scoped)) {
		provider, operation, ok := strings.Cut(name, ".")
		if !ok {
			// Unreachable: ResolveTools rejects an entry with no
			// separator (ErrMalformedToolScope) and every key it
			// produces is built as provider+"."+tool.
			return nil, fmt.Errorf("session: resolve tools: %q has no provider separator", name)
		}
		handle, err := r.catalog.Tool(provider, operation)
		if err != nil {
			return nil, fmt.Errorf("session: resolve tool %q: %w", name, err)
		}
		tools[name] = handle
	}
	return tools, nil
}

// resolveModel walks the profile's model{} chain via
// agentprofile.SelectModel and resolves the winner to a live handle. A
// profile with no model{} block at all — only ever BuiltinDefaultProfile,
// since a declared block is required by config validation — falls back to
// the sole loaded model when there is exactly one.
//
// needsToolUse is the only turn requirement that varies here: a session
// with tools in scope needs a tool-use-capable candidate, one without
// does not. The model is selected once per session rather than once per
// turn, deliberately: the requirement set is constant across a session's
// ordinary turns, and re-routing mid-session would silently change which
// model answers adjacent turns for no reason a caller asked for.
func (r *Runner) resolveModel(profile agentprofile.AgentProfile, needsToolUse bool) (providercatalog.ModelHandle, error) {
	specs := r.catalog.ModelSpecs()

	block := profile.Model
	if block.Primary == (agentprofile.ModelRef{}) && len(block.Fallbacks) == 0 {
		ref, err := soleLoadedModel(specs)
		if err != nil {
			return providercatalog.ModelHandle{}, err
		}
		block = agentprofile.ModelBlock{Primary: ref}
	}

	ref, err := agentprofile.SelectModel(block, specs, agentprofile.TurnRequirements{NeedsToolUse: needsToolUse})
	if err != nil {
		return providercatalog.ModelHandle{}, fmt.Errorf("session: select model: %w", err)
	}
	handle, err := r.catalog.Model(ref)
	if err != nil {
		return providercatalog.ModelHandle{}, fmt.Errorf("session: resolve model %s.%s: %w", ref.Provider, ref.ID, err)
	}
	return handle, nil
}

// soleLoadedModel returns the one loaded model when the catalog holds
// exactly one, and ErrNoDefaultModel otherwise. See ErrNoDefaultModel for
// why "exactly one" is the only case this package will guess at.
func soleLoadedModel(specs map[agentprofile.ModelRef]*modelv1.ModelSpec) (agentprofile.ModelRef, error) {
	if len(specs) != 1 {
		return agentprofile.ModelRef{}, fmt.Errorf("session: %d models loaded: %w", len(specs), ErrNoDefaultModel)
	}
	for ref := range specs {
		return ref, nil
	}
	return agentprofile.ModelRef{}, ErrNoDefaultModel // unreachable: len == 1
}

// modelTarget builds the ModelTarget every turn carries, reserving
// effectiveCeilingPercent of the model's declared context window for the
// turn's own output and fixed overhead.
func modelTarget(handle providercatalog.ModelHandle) *modelv1.ModelTarget {
	window := handle.Spec.GetContextWindow()
	return &modelv1.ModelTarget{
		Id:               handle.Ref.ID,
		ContextWindow:    window,
		EffectiveCeiling: window * effectiveCeilingPercent / 100,
	}
}

// grantKeys returns the deduplicated plugins that hold a session-lifetime
// callback grant: the routed model provider, every scoped tool's provider,
// and every loaded context provider (all of which the turn loop invokes on
// this session's behalf and which therefore call back naming it).
//
// Hook subscribers are not enumerated separately, and cannot be: a hook
// subscription rides the same connection as its plugin's primary category
// service (providercatalog.Catalog.Hook resolves by local name and the
// interface exposes no listing), so a hook-serving plugin that is also a
// model/tool/context provider is already covered here. A plugin that
// serves hooks and nothing else would not be — a real gap, recorded in
// this package's CLAUDE.md rather than papered over by widening
// providercatalog's interface from this side.
//
// The order is deterministic: model first, then tools by scoped name, then
// contexts by catalog position (determinism.md).
func (r *Runner) grantKeys(model providercatalog.ModelHandle, tools map[string]providercatalog.ToolHandle) []sessionscope.Key {
	seen := make(map[sessionscope.Key]bool, 1+len(tools))
	keys := make([]sessionscope.Key, 0, 1+len(tools))

	add := func(producer *commonv1.ProducerRef) {
		if producer == nil {
			return
		}
		key := sessionscope.KeyFor(producer)
		if seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}

	add(model.Producer)
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		add(tools[name].Producer)
	}
	for _, handle := range r.catalog.Contexts() {
		add(handle.Producer)
	}
	return keys
}
