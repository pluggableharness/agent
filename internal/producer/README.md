# internal/producer

Server-derived plugin-identity attribution
(`pluggableharness.agent.common.v1.ProducerRef`), carried across a `context.Context`.

Producer identity is used to tag kernel-native output — log entries,
spans, metrics — with which plugin (category/name/version) a given call is
attributed to. It MUST be set only by trusted, kernel-side code that
authenticates a callback connection (`specifications/kernel-callbacks.md`
§4/§5: server-derived, never client-supplied), never populated from a
value a plugin sends over the wire.

## What this package does

- `producer.go` — `WithProducer` attaches a `*commonv1.ProducerRef` to a
  context; `FromContext` retrieves it, with a comma-ok result matching
  Go's usual "value present" idiom.

## How it fits in

This pair started as two unexported-key helpers inside `internal/log`
(`handler.go`), which was the first of two callers to need them. A third
consumer — the kernel-callback server's span attribution for `Emit`, still
being built — pushed the pair out into its own leaf package rather than
duplicating it a second time. `internal/log` now imports this package like
any other consumer.
