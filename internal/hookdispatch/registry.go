package hookdispatch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/providercatalog"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
	hookv1 "github.com/pluggableharness/agent/pkg/hook/proto/v1"
)

// Registry construction errors. Every one of these is a config-load
// failure, surfaced before a session ever runs a turn — never a dispatch-
// time surprise.
var (
	// ErrUnknownPoint is returned for a hook{} label that names no
	// dispatchable hook point (hook-dispatch.md#hook-points' eight-row
	// table). context-assemble wraps this too: it is a real hook point,
	// but it is served by ContextService.Contribute, not by
	// HookSubscriberService.
	ErrUnknownPoint = errors.New("hookdispatch: unknown hook point")

	// ErrUnknownMode is returned for a mode attribute that is not one of
	// observe/transform/veto.
	ErrUnknownMode = errors.New("hookdispatch: unknown hook mode")

	// ErrPointNotAdvertised is returned when a subscription names a point
	// the plugin's own capabilities never advertised in
	// supported_hook_points (model/protocol.md#getcapabilities' MUST).
	ErrPointNotAdvertised = errors.New("hookdispatch: hook point not advertised by provider")

	// ErrVetoNotPermitted is returned for a veto-mode subscription at a
	// point that gates no blockable action — see vetoBearingPoints
	// (points.go) for which points are veto-bearing and why.
	ErrVetoNotPermitted = errors.New("hookdispatch: veto mode not permitted at this hook point")

	// ErrDuplicateSubscription is returned when the same provider
	// subscribes twice at the same point. Declaration position is the sole
	// ordering authority, and one provider cannot hold two positions in
	// one chain.
	ErrDuplicateSubscription = errors.New("hookdispatch: duplicate provider subscription at hook point")

	// ErrMissingPosition is returned when a subscription has no textual
	// declaration position to sort by — an implicit subscription whose
	// provider{} block has no entry in Config.ProviderRanges.
	ErrMissingPosition = errors.New("hookdispatch: subscription has no declaration position")

	// ErrInvalidTimeout is returned for a negative per-subscriber timeout
	// or a non-positive default. Zero is permitted on a per-subscriber
	// override — an operator declaring timeout_ms = 0 declared a
	// zero-millisecond deadline, which is a choice, not a mistake.
	ErrInvalidTimeout = errors.New("hookdispatch: invalid hook timeout")
)

// Position is a subscription's textual declaration position — the sole
// ordering authority for a hook point's chain, per
// configuration/agent-profiles.md#explicit-hook-subscriptions ("Ordering
// across implicit and explicit subscriptions ... is resolved by textual
// declaration position in agent.hcl").
//
// The spec's rule assumes a single agent.hcl. architecture.md's XDG
// layout permits "other *.hcl in project dir, merged", so this kernel
// resolves the multi-file case by ordering files lexicographically by
// filename before ordering blocks by byte offset within a file — a
// deterministic total order that never depends on filesystem enumeration
// order (determinism.md). NewRegistry derives FileIndex itself from the
// hcl.Range filenames it is given; a caller never assigns one.
type Position struct {
	// FileIndex is this subscription's file's index into the
	// lexicographically-sorted list of every filename NewRegistry saw.
	FileIndex int
	// ByteStart is the declaring block's hcl.Range.Start.Byte within that
	// file.
	ByteStart int
}

// compare orders a by declaration position: file first, then byte offset
// within the file. Two subscriptions in one chain can never compare equal
// — a duplicate (provider, point) pair is rejected at construction, and
// two distinct blocks in one file cannot start at the same byte — so this
// is a total order.
func (p Position) compare(q Position) int {
	if c := cmp.Compare(p.FileIndex, q.FileIndex); c != 0 {
		return c
	}
	return cmp.Compare(p.ByteStart, q.ByteStart)
}

// Origin records what kind of declaration put a subscriber in a chain.
type Origin int

const (
	// OriginImplicit is a subscription a provider's category implies
	// rather than one an operator wrote a hook{} block for. Its position
	// is the provider{} block's own range.
	OriginImplicit Origin = iota
	// OriginExplicit is an agent.hcl hook{} block. Its position is that
	// block's range.
	OriginExplicit
	// OriginKernel is the kernel-privileged in-process veto — the policy
	// engine. It is pinned ahead of every plugin subscriber and is
	// excluded from the positional sort entirely, so it never appears on
	// a Subscriber.
	OriginKernel
)

// String renders o for logs and test failures.
func (o Origin) String() string {
	switch o {
	case OriginImplicit:
		return "implicit"
	case OriginExplicit:
		return "explicit"
	case OriginKernel:
		return "kernel"
	default:
		return fmt.Sprintf("Origin(%d)", int(o))
	}
}

// Implicit is one category-derived hook subscription — the kind
// configuration/agent-profiles.md#explicit-hook-subscriptions calls
// "implicit by provider category".
//
// It is a NewRegistry parameter rather than something this package
// derives, deliberately: no category-to-hook-point derivation table
// exists anywhere in this codebase or in any spec table that could be
// cited, so inventing one here would be a fabricated mapping wearing a
// kernel's authority. Whichever component eventually learns each loaded
// plugin's category-implied points builds these and hands them over.
type Implicit struct {
	// Provider is the plugin's agent.hcl local name — the same name
	// providercatalog.Catalog.Hook takes.
	Provider string
	// Point is the hook point the provider's category implies.
	Point commonv1.HookPoint
	// Mode is the subscription mode the category implies.
	Mode hookv1.HookMode
}

// Subscriber is one resolved entry in one hook point's ordered chain.
type Subscriber struct {
	// Provider is the plugin's agent.hcl local name.
	Provider string
	// Producer identifies the plugin build serving this subscription. It
	// is what a hook_error event this subscriber causes is attributed to
	// (state-backend.md#the-kind-enum).
	Producer *commonv1.ProducerRef
	// Client is the dialed HookSubscriberService client.
	Client hookv1.HookSubscriberServiceClient
	// Mode is the declared subscription mode.
	Mode hookv1.HookMode
	// Timeout is the effective per-subscriber deadline: the hook{}
	// block's timeout_ms override if it declared one, otherwise the
	// registry's default (hook-dispatch.md#per-subscriber-timeout).
	Timeout time.Duration
	// Position is the textual declaration position this chain is sorted
	// by.
	Position Position
	// Origin records whether this came from a provider{} block's implied
	// subscription or an explicit hook{} block.
	Origin Origin
}

// KernelVeto is an in-process, non-plugin veto subscriber. Only a
// kernel-owned component may hold this slot:
// architecture.md#policy--first-party-not-a-plugin-category puts policy
// outside the plugin categories entirely, so it never goes through
// HookSubscriberService.
//
// That is a restriction on who *may* be pinned, not an expectation that
// policy always is. Policy's real evaluation is per-item — policy.Evaluate
// per call, feeding a plan item's decided_by (plan-apply-gate.md) — and
// that path does not come through this package. A plan gate that already
// evaluates policy per item MUST NOT also pin a policy veto here; see this
// package's CLAUDE.md for why the coarse decision would corrupt the audit
// trail.
//
// It is declared as a narrow interface so this package never imports
// internal/policy.
type KernelVeto interface {
	// Name identifies this veto for Outcome.DeniedBy and for logs. It is
	// not a plugin name and never becomes an event producer.
	Name() string
	// Veto evaluates payload and returns ALLOW or DENY. An error, and a
	// ctx deadline firing, both fail closed to DENY exactly as a plugin
	// veto subscriber's failure does (hook-dispatch.md#timeout-behavior
	// draws no first-party/third-party distinction).
	Veto(ctx context.Context, payload *hookv1.HookPayload) (hookv1.HookDecision, error)
}

// Registry holds the resolved, declaration-ordered subscriber chain for
// every hook point, plus the kernel-privileged veto pinned ahead of each
// chain. Construct with NewRegistry; the zero value is not usable.
//
// A Registry is immutable once Pin has been called for whatever kernel
// vetoes a session has, and is safe for concurrent reads thereafter.
type Registry struct {
	chains         map[commonv1.HookPoint][]Subscriber
	pinned         map[commonv1.HookPoint]KernelVeto
	defaultTimeout time.Duration
}

// pending is one subscription before its Position is resolved — file
// indices can only be assigned once every declaring filename is known.
type pending struct {
	provider string
	point    commonv1.HookPoint
	mode     hookv1.HookMode
	timeout  time.Duration
	rng      hcl.Range
	origin   Origin
}

// NewRegistry merges implicit (category-derived) and explicit (agent.hcl
// hook{}) subscriptions into one declaration-ordered chain per hook
// point.
//
// cat resolves each named provider's dialed HookSubscriberService and its
// advertised supported_hook_points. providerRanges is
// config.Config.ProviderRanges — the provider{} block positions an
// implicit subscription is ordered by. defaultTimeout is
// Settings.DefaultHookTimeoutMS as a Duration, used for every subscription
// that declares no timeout_ms override.
//
// It rejects, at construction time rather than at dispatch time:
//   - a subscription naming a point the plugin never advertised
//     (ErrPointNotAdvertised);
//   - a veto-mode subscription at a non-veto-bearing point
//     (ErrVetoNotPermitted);
//   - a duplicate (provider, point) pair (ErrDuplicateSubscription);
//   - an unknown point label or mode string, a provider absent from cat,
//     an implicit subscription whose provider has no declared range, and
//     a negative timeout.
func NewRegistry(
	cat providercatalog.Catalog,
	implicit []Implicit,
	hooks []config.Hook,
	providerRanges map[string]hcl.Range,
	defaultTimeout time.Duration,
) (*Registry, error) {
	if cat == nil {
		return nil, errors.New("hookdispatch: new registry: catalog is nil")
	}
	if defaultTimeout <= 0 {
		return nil, fmt.Errorf("hookdispatch: new registry: %w: default timeout must be positive, got %s", ErrInvalidTimeout, defaultTimeout)
	}

	pendings, err := collectPending(implicit, hooks, providerRanges, defaultTimeout)
	if err != nil {
		return nil, err
	}

	handles, err := resolveHandles(cat, pendings)
	if err != nil {
		return nil, err
	}

	fileIndex := indexFilenames(pendings)

	chains := make(map[commonv1.HookPoint][]Subscriber)
	for _, p := range pendings {
		chains[p.point] = append(chains[p.point], Subscriber{
			Provider: p.provider,
			Producer: handles[p.provider].Producer,
			Client:   handles[p.provider].Client,
			Mode:     p.mode,
			Timeout:  p.timeout,
			Position: Position{FileIndex: fileIndex[p.rng.Filename], ByteStart: p.rng.Start.Byte},
			Origin:   p.origin,
		})
	}
	for point := range chains {
		slices.SortStableFunc(chains[point], func(a, b Subscriber) int {
			return a.Position.compare(b.Position)
		})
	}

	return &Registry{
		chains:         chains,
		pinned:         make(map[commonv1.HookPoint]KernelVeto),
		defaultTimeout: defaultTimeout,
	}, nil
}

// collectPending flattens implicit and explicit subscriptions into one
// slice, validating everything resolvable without touching the catalog:
// point labels, modes, veto-bearing points, declaration positions,
// timeouts, and duplicates.
func collectPending(
	implicit []Implicit,
	hooks []config.Hook,
	providerRanges map[string]hcl.Range,
	defaultTimeout time.Duration,
) ([]pending, error) {
	type key struct {
		provider string
		point    commonv1.HookPoint
	}
	seen := make(map[key]struct{}, len(implicit)+len(hooks))
	out := make([]pending, 0, len(implicit)+len(hooks))

	add := func(p pending) error {
		if _, ok := hookPointText[p.point]; !ok {
			return fmt.Errorf("hookdispatch: provider %q: %w: %v", p.provider, ErrUnknownPoint, p.point)
		}
		if _, ok := hookModeText[p.mode]; !ok {
			return fmt.Errorf("hookdispatch: provider %q: %w: %v", p.provider, ErrUnknownMode, p.mode)
		}
		if p.mode == hookv1.HookMode_HOOK_MODE_VETO && !IsVetoBearing(p.point) {
			text, _ := PointText(p.point)
			return fmt.Errorf("hookdispatch: provider %q: %w: %s", p.provider, ErrVetoNotPermitted, text)
		}
		if p.timeout < 0 {
			return fmt.Errorf("hookdispatch: provider %q: %w: %s", p.provider, ErrInvalidTimeout, p.timeout)
		}
		if p.rng.Filename == "" {
			return fmt.Errorf("hookdispatch: provider %q: %w", p.provider, ErrMissingPosition)
		}
		k := key{provider: p.provider, point: p.point}
		if _, dup := seen[k]; dup {
			text, _ := PointText(p.point)
			return fmt.Errorf("hookdispatch: provider %q: %w: %s", p.provider, ErrDuplicateSubscription, text)
		}
		seen[k] = struct{}{}
		out = append(out, p)
		return nil
	}

	for _, im := range implicit {
		rng, ok := providerRanges[im.Provider]
		if !ok {
			return nil, fmt.Errorf("hookdispatch: provider %q: %w: no provider{} block range", im.Provider, ErrMissingPosition)
		}
		if err := add(pending{
			provider: im.Provider,
			point:    im.Point,
			mode:     im.Mode,
			timeout:  defaultTimeout,
			rng:      rng,
			origin:   OriginImplicit,
		}); err != nil {
			return nil, err
		}
	}

	for _, h := range hooks {
		point, err := pointFromText(h.Point)
		if err != nil {
			return nil, fmt.Errorf("hookdispatch: hook %q: provider %q: %w", h.Point, h.Provider, err)
		}
		mode, err := modeFromText(h.Mode)
		if err != nil {
			return nil, fmt.Errorf("hookdispatch: hook %q: provider %q: %w", h.Point, h.Provider, err)
		}
		timeout := defaultTimeout
		if h.TimeoutMS != nil {
			timeout = time.Duration(*h.TimeoutMS) * time.Millisecond
		}
		if err := add(pending{
			provider: h.Provider,
			point:    point,
			mode:     mode,
			timeout:  timeout,
			rng:      h.Range,
			origin:   OriginExplicit,
		}); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// resolveHandles looks each distinct provider up in cat exactly once and
// checks every subscription against that plugin's advertised
// supported_hook_points.
func resolveHandles(cat providercatalog.Catalog, pendings []pending) (map[string]providercatalog.HookHandle, error) {
	handles := make(map[string]providercatalog.HookHandle)
	for _, p := range pendings {
		h, ok := handles[p.provider]
		if !ok {
			var err error
			h, err = cat.Hook(p.provider)
			if err != nil {
				return nil, fmt.Errorf("hookdispatch: provider %q: %w", p.provider, err)
			}
			handles[p.provider] = h
		}
		if !slices.Contains(h.SupportedPoints, p.point) {
			text, _ := PointText(p.point)
			return nil, fmt.Errorf("hookdispatch: provider %q: %w: %s", p.provider, ErrPointNotAdvertised, text)
		}
	}
	return handles, nil
}

// indexFilenames assigns each distinct declaring filename its index into
// the lexicographically-sorted filename list — Position.FileIndex's
// multi-file ordering rule.
func indexFilenames(pendings []pending) map[string]int {
	names := make([]string, 0, len(pendings))
	for _, p := range pendings {
		if !slices.Contains(names, p.rng.Filename) {
			names = append(names, p.rng.Filename)
		}
	}
	slices.Sort(names)

	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	return index
}

// Pin registers v as the kernel-privileged veto subscriber at point,
// placed ahead of every plugin subscriber unconditionally.
//
// Pinning rather than positioning is what actually guarantees
// hook-dispatch.md#veto-mode-subscription-trust-model's promise that a
// third-party veto subscriber "cannot override a DENY policy has already
// produced earlier in the chain": policy has no agent.hcl block, so it
// has no textual position to be sorted by, and only running it first
// makes "earlier in the chain" true in every configuration.
//
// Pinning is optional, and leaving a point unpinned is the right call
// whenever the kernel component in question already decides upstream by
// another path (KernelVeto's doc comment).
//
// Pin panics if point is not veto-bearing — a kernel veto at a point that
// gates nothing is a wiring bug in kernel code, not operator input.
// Pinning twice at one point replaces the previous veto.
func (r *Registry) Pin(point commonv1.HookPoint, v KernelVeto) {
	if !IsVetoBearing(point) {
		text, ok := PointText(point)
		if !ok {
			text = point.String()
		}
		panic(fmt.Sprintf("hookdispatch: pin: %s is not a veto-bearing hook point", text))
	}
	r.pinned[point] = v
}

// Subscribers returns point's chain in declaration order. The returned
// slice aliases the registry's own storage and MUST NOT be mutated; it is
// exposed so a caller can skip assembling a payload for a point nothing
// subscribes to.
func (r *Registry) Subscribers(point commonv1.HookPoint) []Subscriber {
	return r.chains[point]
}

// PinnedVeto returns the kernel-privileged veto registered at point, if
// any.
func (r *Registry) PinnedVeto(point commonv1.HookPoint) (KernelVeto, bool) {
	v, ok := r.pinned[point]
	return v, ok
}
