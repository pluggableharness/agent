// Package metadata is the shared builder package for MetadataBlock — the
// typed "Metadata" surface of the four frontend state surfaces (input,
// state, metadata, transcript). A plugin author composes intent from
// these primitives; a frontend maps Tone tokens to whatever it has
// (ANSI color, CSS class, spoken label). A block never carries a color,
// a width, or a position.
//
// See docs/specifications/frontend/ and api/pluggableharness/metadata/v1.
package metadata
