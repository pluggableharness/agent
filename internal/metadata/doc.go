// Package metadata is the kernel-side MetadataBlock collection: upsert on
// PublishMetadata, liveness flip on RetractMetadata / publisher exit, never
// delete. Frontends snapshot via ListMetadata and subscribe to topic
// kernel.metadata for live updates.
//
// See docs/specifications/frontend/ and kernel-callbacks.md.
package metadata
