package plugin

import (
	"github.com/pluggableharness/agent/pkg/common"
	commonv1 "github.com/pluggableharness/agent/pkg/common/proto/v1"
)

// Identity is a plugin build's own self-reported identity, supplied by the
// plugin author rather than read from a lock-file entry. This matters
// specifically for a binary resolved via dev_overrides
// (docs/specifications/configuration/lock-file.md's "dev_overrides and
// identity without a lock entry" section): such a binary has no
// provider "<name>" { ... } row in agent.lock.hcl at all, so the kernel
// has no source/version/checksums to read identity from the way it would
// for a normally-resolved plugin, and instead obtains
// {name, version, source, category, protocol_version} directly from the
// plugin process itself via that category's own Describe RPC.
type Identity struct {
	// Name is the plugin's declared name, e.g. "filesystem" — unique
	// within the plugin's category, not globally.
	Name string
	// Version is the plugin's exact version, semver-formatted, e.g.
	// "1.2.3".
	Version string
	// Source is the resolved source address the plugin was installed
	// from, e.g. "github.com/agentco/filesystem-provider".
	Source string
}

// ProducerRef builds the common.v1.ProducerRef every category's Describe
// RPC returns, from this identity plus category (the plugin category this
// build is serving) and the protocol version this build was compiled
// against (pkg/common.ProtocolVersion). Category-specific SDKs (pkg/tool,
// pkg/model, ...) call this from their own Describe implementation — this
// package does not implement Describe itself, since DescribeResponse is a
// distinct generated type per category.
func (id Identity) ProducerRef(category commonv1.Category) *commonv1.ProducerRef {
	return &commonv1.ProducerRef{
		Name:            id.Name,
		Version:         id.Version,
		Source:          id.Source,
		Category:        category,
		ProtocolVersion: uint32(common.ProtocolVersion),
	}
}
