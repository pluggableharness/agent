// Package anthropic implements the Anthropic model provider — the
// repository's reference implementation of
// docs/specifications/model/README.md's ModelService contract, served as
// a hashicorp/go-plugin subprocess by cmd/anthropic.
//
// It is deliberately built the way a third party would build it: against
// pkg/model, pkg/plugin, pkg/config, and pkg/content alone, plus the
// standard library. It never imports another internal/ package, and a
// depguard rule in .golangci.yml enforces that mechanically rather than
// leaving it to good intentions — see CLAUDE.md for why that rule is the
// point of this package rather than an incidental tidiness.
//
// The package splits three ways:
//
//   - This directory owns the model.Provider implementation itself
//     (provider.go), the agent.hcl config schema and its decoding
//     (config.go), and the secret-safe error construction both use
//     (errors.go).
//   - catalog/ owns the model roster and its pricing — pure data.
//   - messages/ owns everything vendor-shaped: Anthropic's own JSON
//     types, the canonical-to-vendor request translation, the SSE reader,
//     the vendor-event-to-Sink translation, the HTTP client, and the
//     HTTP-status-to-model.Error classification table.
//
// Two things this package deliberately does not do. It computes no cost:
// the kernel owns that, from the Usage counts this plugin reports plus
// the catalog's declared Pricing
// (docs/specifications/model/protocol.md#cost-computation). And it
// retries nothing: every failure is classified into the right
// model.Error category with Retryable and RetryAfter set, and the
// kernel's own retry loop decides what to do with it
// (.claude/rules/grpc.md).
package anthropic
