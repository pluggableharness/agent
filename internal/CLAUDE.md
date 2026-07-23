# internal/ — Logging & Telemetry

The enforceable rules (when instrumentation is mandatory, the pure-domain
exemption, the logging level vocabulary, telemetry conventions, and the
review-time checklist) now live in `.claude/rules/logging-telemetry.md` —
that file is authoritative and loads automatically for any `internal/**/*.go`
change. Don't duplicate its content here; extend it there if a rule needs to
change, and keep this file to context that's specific to this directory
rather than restating the rule.

## Why this file exists

`internal/` packages have not, historically, done logging or telemetry
consistently: `internal/policy` and `internal/agentprofile` are pure-domain
and MUST stay that way (the exemption in `logging-telemetry.md` covers why);
`internal/hclsecret` is deliberately exempt (see its own `CLAUDE.md`);
`internal/config` and `internal/registry` are being retrofitted with
`log/slog`/`internal/telemetry` as part of this same tracked effort. Logging
and tracing/metrics integration is not optional polish added at the end —
it's part of the definition of "done" for any `internal/` package that does
real work.

The two mandatory dependencies: `log/slog` (stdlib, already the only
sanctioned logging mechanism per `.claude/rules/go-style.md`) and
`internal/telemetry` (this repo's OTel-native tracing/metrics module — see
its own `README.md`/`CLAUDE.md` for how it's built, as opposed to
`logging-telemetry.md`'s "when and how the rest of the codebase must use
it").
