package theme

// ExportToOklab exposes the Oklab conversion to this package's external tests,
// which need it to assert that a gradient's lightness behaves.
var ExportToOklab = toOklab
