// Package telemetry is the plugin-author-facing entry point into
// PluggableHarness Agent's OTel-native observability module (internal/telemetry). A
// plugin's main() calls Bootstrap once at startup and defers the returned
// shutdown func, and gets correctly-nested tracing/metrics — spans
// exported by the plugin nest under the kernel's own spans automatically
// once the kernel wires grpc.WithStatsHandler/grpc.StatsHandler using
// internal/telemetry.Provider's ClientHandler/ServerHandler on both ends
// of the hashicorp/go-plugin connection — with no further wiring on the
// plugin author's part.
//
// Unlike the other pkg/<category> directories, this package has no
// proto/ subdirectory: telemetry is not one of the six plugin categories
// and has no wire protocol of its own. It is a pure Go convenience
// wrapper.
package telemetry
