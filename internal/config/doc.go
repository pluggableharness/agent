// Package config parses and structurally validates agent.hcl — the
// top-level block dispatch (specifications/configuration.md §1-3),
// required_providers/provider/settings/hook (§5, §6, §9, §8.6), and the
// schema-to-cty bridge (§4) that turns a provider's advertised ConfigSchema
// into decoded JSON for its Configure RPC.
//
// A provider{} block's body is captured raw (as an undecoded hcl.Body), not
// eagerly decoded: a provider's ConfigSchema only exists once its plugin
// subprocess is loaded and queried, which is outside this package's job.
// Call DecodeProviderConfig once a real ConfigSchema is available.
//
// policy{} and agent_profile{} blocks are decoded into internal/policy and
// internal/agentprofile's types respectively — this package owns parsing
// their agent.hcl syntax, not their runtime semantics (policy evaluation,
// depth-budget inheritance, model-fallback eligibility), which live in
// those sibling packages.
package config
