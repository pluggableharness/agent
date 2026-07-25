package providerresolve

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/pluggableharness/agent/internal/config"
	"github.com/pluggableharness/agent/internal/plugincache"
	"github.com/pluggableharness/agent/internal/registry"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Resolved is one required_providers entry, fully resolved to a
// launchable binary.
type Resolved struct {
	// LocalName is the required_providers local name — the key a
	// provider{} block, an agent_profile's model{}/tools, and every later
	// lookup use. It is not the plugin's own published name, which is
	// only knowable from a live Describe probe.
	LocalName string

	// Source is the git-forge address declared in required_providers.
	Source string

	// Version is the concrete version the lock file resolved. Empty for a
	// dev-override provider, which bypasses version resolution entirely.
	Version string

	// Category is the plugin category, taken from the lock file's own
	// cached record of a previously-discovered category
	// (registry.LockedProvider.Category). It is
	// CATEGORY_UNSPECIFIED whenever that record is absent — always for a
	// dev-override provider, and for any lock row written before the
	// field existed. A provider's real category is only authoritatively
	// knowable from a live Describe probe, which is a launching
	// component's job (internal/pluginhost), not this package's.
	Category commonv1.Category

	// BinaryPath is the on-disk binary to exec: the dev_overrides path
	// for an overridden provider, otherwise the plugin-cache path for
	// (source, version, platform).
	BinaryPath string

	// Platform is the "<os>_<arch>" key this entry was resolved for —
	// the same key BinaryPath was built from and, for a locked provider,
	// the key Locked.Checksums was confirmed to carry. Carried here so a
	// later checksum verification uses the platform this resolution
	// actually happened for, rather than re-deriving one that could
	// silently disagree with it.
	Platform string

	// ViaDevOverride reports whether this entry came from the global
	// config's dev_overrides map, bypassing the registry/lock machinery
	// (configuration/settings-and-global.md#dev_overrides).
	ViaDevOverride bool

	// Locked is the lock-file row this entry resolved against, for a
	// later checksum verification. nil for a dev-override provider —
	// there is deliberately no lock row to verify against.
	Locked *registry.LockedProvider
}

// Input bundles everything Resolve reads. Every field is supplied by the
// caller that already loaded it; this package opens no config file of its
// own.
type Input struct {
	// Config is the loaded agent.hcl. Required — a nil Config resolves to
	// nothing, since required_providers is where the whole list comes
	// from.
	Config *config.Config

	// Lock is the loaded project lock file. A nil Lock is treated as a
	// lock file with no rows, so every non-overridden provider reports
	// MissingNotLocked rather than panicking on a fresh checkout.
	Lock *registry.LockFile

	// Global is the loaded global config, read only for its
	// DevOverrides map. MAY be nil.
	Global *registry.GlobalConfig

	// CacheDir is the resolved plugin cache root
	// (internal/xdg.Paths.PluginCacheDir).
	CacheDir string

	// Platform is the "<os>_<arch>" key both the cache layout and the
	// lock file's checksums map are keyed by
	// (internal/plugincache.Platform).
	Platform string

	// Logger receives this pass's DEBUG lines, including the per-binary
	// existence checks internal/plugincache logs. Defaults to
	// slog.Default() when nil.
	Logger *slog.Logger
}

// Order returns required's local names in the deterministic sequence
// every later stage depends on: the textual position of each name's
// provider{} block in agent.hcl.
//
// configuration/agent-profiles.md resolves hook ordering by "textual
// declaration position in agent.hcl", with an implicit subscription's
// position being wherever its provider{} block appears. That rule is
// applied here, one stage earlier, because this same sequence is what
// later becomes launch order — and launch order is hook-dispatch order,
// whose reverse is shutdown order. Deriving all three from one textual
// sort keeps them consistent by construction.
//
// A local name with no provider{} block (a provider declared in
// required_providers but never configured) has no textual position, so
// it sorts after every name that does, then by name. Names with a
// position sort by file, then by byte offset, then by name — a total
// order in every case, never map iteration order
// (.claude/rules/determinism.md).
func Order(required map[string]config.RequiredProvider, ranges map[string]hcl.Range) []string {
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		a, aOK := ranges[names[i]]
		b, bOK := ranges[names[j]]
		switch {
		case aOK != bOK:
			return aOK // a positioned entry sorts before an unpositioned one
		case !aOK:
			return names[i] < names[j]
		case a.Filename != b.Filename:
			return a.Filename < b.Filename
		case a.Start.Byte != b.Start.Byte:
			return a.Start.Byte < b.Start.Byte
		default:
			return names[i] < names[j]
		}
	})
	return names
}

// Resolve resolves every required_providers entry, in Order's sequence,
// to a launchable binary.
//
// Per entry, a dev_overrides match wins first and bypasses the whole
// registry path — no lock row is consulted and no checksum is verified
// (configuration/settings-and-global.md#dev_overrides: "the kernel MUST
// use that binary directly instead of resolving through the
// registry/version-constraint machinery"). Everything else resolves
// through the lock file: the row MUST exist, the cached binary for this
// platform MUST exist, and a checksum for this platform MUST be
// recorded. Both paths additionally require the named binary to be
// executable, which is not registry machinery but a property of the file
// itself.
//
// Every problem is accumulated rather than returned on first sight: on
// any failure Resolve returns a nil slice and a single *MissingError
// listing every unresolvable provider in Order's sequence, so one pass
// over a fresh checkout reports everything that has to be installed.
func Resolve(ctx context.Context, in Input) ([]Resolved, error) {
	logger := in.Logger
	if logger == nil {
		logger = slog.Default()
	}

	required := map[string]config.RequiredProvider{}
	ranges := map[string]hcl.Range{}
	if in.Config != nil {
		required = in.Config.RequiredProviders
		ranges = in.Config.ProviderRanges
	}

	names := Order(required, ranges)
	resolved := make([]Resolved, 0, len(names))
	var missing []Missing

	for _, name := range names {
		req := required[name]
		one, problem := resolveOne(ctx, logger, in, name, req)
		if problem != nil {
			missing = append(missing, *problem)
			continue
		}
		resolved = append(resolved, one)
	}

	if len(missing) > 0 {
		logger.DebugContext(ctx, "providerresolve: unresolved providers", "count", len(missing))
		return nil, &MissingError{Missing: missing}
	}

	logger.DebugContext(ctx, "providerresolve: resolved providers", "count", len(resolved))
	return resolved, nil
}

// resolveOne resolves a single required_providers entry, returning either
// a Resolved value or the one Missing entry describing why it could not
// be resolved. Exactly one of the two is non-zero/non-nil.
func resolveOne(ctx context.Context, logger *slog.Logger, in Input, name string, req config.RequiredProvider) (Resolved, *Missing) {
	if path, ok := devOverride(in.Global, name); ok {
		logger.DebugContext(ctx, "providerresolve: dev override", "provider", name, "path", path)
		if problem := checkBinary(ctx, logger, path); problem != nil {
			problem.LocalName = name
			problem.Source = req.Source
			problem.Constraint = req.Constraint
			return Resolved{}, problem
		}
		return Resolved{
			LocalName:      name,
			Source:         req.Source,
			Category:       commonv1.Category_CATEGORY_UNSPECIFIED,
			BinaryPath:     path,
			Platform:       in.Platform,
			ViaDevOverride: true,
		}, nil
	}

	locked, ok := lockedProvider(in.Lock, name)
	if !ok {
		return Resolved{}, &Missing{
			LocalName:  name,
			Source:     req.Source,
			Constraint: req.Constraint,
			Reason:     MissingNotLocked,
		}
	}

	path := plugincache.BinaryPath(in.CacheDir, locked.Source, locked.Version, in.Platform)
	if problem := checkBinary(ctx, logger, path); problem != nil {
		problem.LocalName = name
		problem.Source = req.Source
		problem.Constraint = req.Constraint
		problem.Version = locked.Version
		return Resolved{}, problem
	}

	if _, recorded := locked.Checksums[in.Platform]; !recorded {
		return Resolved{}, &Missing{
			LocalName:  name,
			Source:     req.Source,
			Constraint: req.Constraint,
			Version:    locked.Version,
			Reason:     MissingNoChecksum,
			Path:       path,
		}
	}

	return Resolved{
		LocalName:  name,
		Source:     req.Source,
		Version:    locked.Version,
		Category:   parseCategory(locked.Category),
		BinaryPath: path,
		Platform:   in.Platform,
		Locked:     &locked,
	}, nil
}

// devOverride reports the dev_overrides binary path declared for name, if
// any. A nil Global (no global config file) simply has no overrides.
func devOverride(global *registry.GlobalConfig, name string) (string, bool) {
	if global == nil {
		return "", false
	}
	path, ok := global.DevOverrides[name]
	if !ok || path == "" {
		return "", false
	}
	return path, true
}

// lockedProvider returns the lock-file row for name. A nil LockFile is
// treated as one with no rows — the fresh-checkout case, which must
// report MissingNotLocked per provider rather than failing as a whole.
func lockedProvider(lock *registry.LockFile, name string) (registry.LockedProvider, bool) {
	if lock == nil {
		return registry.LockedProvider{}, false
	}
	locked, ok := lock.Providers[name]
	return locked, ok
}

// checkBinary reports whether path is a present, executable regular file,
// returning a partially-populated *Missing (Reason and Path only — the
// caller fills in the provider-identifying fields it knows) when it is
// not. A stat error other than "not found" is reported as
// MissingNotCached too: from a caller's perspective an unreadable binary
// is equally unlaunchable, and the underlying error is already logged by
// internal/plugincache.
func checkBinary(ctx context.Context, logger *slog.Logger, path string) *Missing {
	exists, err := plugincache.Exists(ctx, logger, path)
	if err != nil {
		logger.DebugContext(ctx, "providerresolve: plugin binary unreadable", "path", path, "error", err)
		return &Missing{Reason: MissingNotCached, Path: path}
	}
	if !exists {
		return &Missing{Reason: MissingNotCached, Path: path}
	}
	if !executable(path) {
		return &Missing{Reason: MissingNotExecutable, Path: path}
	}
	return nil
}

// executable reports whether path carries an executable bit for anyone.
// The check is skipped on Windows, which has no POSIX mode bits and
// decides executability from the file extension instead — reporting every
// binary there as MissingNotExecutable would be a false negative, not a
// stricter check. runtime.GOOS is deliberately used rather than
// Input.Platform: the mode bits belong to the local filesystem holding
// the binary, not to the platform key the cache path is built from.
func executable(path string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.Mode().Perm()&0o111 != 0
}

// categoryText maps registry.LockedProvider.Category's plain-string form
// onto the generated enum. The lock file records the category as text
// (internal/registry deliberately holds no proto dependency), so the
// translation lives here, in the first consumer that needs the enum.
var categoryText = map[string]commonv1.Category{
	"model":        commonv1.Category_CATEGORY_MODEL,
	"tool":         commonv1.Category_CATEGORY_TOOL,
	"context":      commonv1.Category_CATEGORY_CONTEXT,
	"memory":       commonv1.Category_CATEGORY_MEMORY,
	"frontend":     commonv1.Category_CATEGORY_FRONTEND,
	"widget":       commonv1.Category_CATEGORY_WIDGET,
	"slashcommand": commonv1.Category_CATEGORY_SLASHCOMMAND,
}

// parseCategory translates a lock file's recorded category text to the
// generated enum, returning CATEGORY_UNSPECIFIED for an empty or
// unrecognized value. An unrecognized value is deliberately not an error:
// the field is a cache of an already-discovered category, and a launching
// component re-probes via Describe whenever it is unspecified, so a
// garbled value costs one probe rather than failing startup.
func parseCategory(text string) commonv1.Category {
	return categoryText[text]
}
