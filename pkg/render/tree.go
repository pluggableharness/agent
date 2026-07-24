package render

import renderv1 "github.com/pluggableharness/agent/pkg/render/proto/v1"

// Tree wraps a single root RenderNode into the RenderTree every category's
// Render RPC returns
// (docs/specifications/frontend/render-tree.md#rendertree: "The tree root
// is just a node, so RenderTree wraps a single root RenderNode"). To place
// more than one top-level node, wrap them first with Group, List, or
// Collapsible — RenderTree.Root is a single node, not a node slice.
func Tree(root *renderv1.RenderNode) *renderv1.RenderTree {
	return &renderv1.RenderTree{Root: root}
}
