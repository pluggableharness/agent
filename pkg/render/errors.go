package render

import "errors"

// ErrUnknownSchemaVersion is returned by (*VersionRegistry).Render when no
// VersionedRenderer has been registered for the requested schema_version.
// docs/specifications/frontend/render-tree.md#schema-versioning requires a
// plugin's Render implementation to keep decoding every schema_version it
// has ever emitted; an unregistered version reaching Render means either a
// plugin build regressed (it forgot to keep an old decoder registered) or
// the caller passed a version this plugin never emitted, and either way
// the caller needs a distinguishable error rather than a generic decode
// failure.
var ErrUnknownSchemaVersion = errors.New("render: unknown schema version")
