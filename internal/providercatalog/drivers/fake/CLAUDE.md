# internal/providercatalog/drivers/fake — agent notes

- **The `Add` methods are construction-time only.** They write to the maps and slice that the lookup methods read, with no mutex — build the catalog fully, then hand it to the code under test. That code runs parallel tool calls under `-race`, and concurrent *reads* of a finished `Catalog` are safe; an `Add` racing a lookup is not. Don't add a mutex to "fix" this: locking a fake to permit mid-test registration hides a test that should have declared its providers up front.

- **This fake instruments nothing, and that is correct.** The `logging-telemetry.md` driver rule ("every driver method that does real work logs and spans") is about drivers that do real work; these methods are map reads in a test double. A `slog` call here would pollute every consumer's test output for no diagnostic value.

- **Lookup keys are parameters, not fields read off the handle.** `AddTool` takes the operation name rather than reading `h.Schema.Name` specifically so a scenario that does not care about schemas can register a zero `ToolHandle`; `AddHook` takes the local name because `HookHandle` carries none. Keep it that way — deriving a key from a handle's own fields would make a nil `Schema` silently register the tool under `""`.

- **`AddContext` stamps `Position` with the append index; a struct literal does not.** Call order is declaration order for the adder path. A test needing a gap, a duplicate, or a deliberately shuffled ordering assigns `ContextProviders` directly — `Contexts()` sorts by `Position` either way (stably, so equal positions keep slice order).

- **The stub clients in `fake_test.go` embed the generated client interfaces and implement no method.** They exist for identity assertions on a handle's `Client` field; calling an RPC on one panics with a nil-embedded-interface dereference, which is the intended signal that this fake never invokes RPCs. A consumer's own tests that need a *responsive* client write their own stub the same way, overriding only the RPCs that test exercises — this package deliberately does not ship one, since what a useful client stub returns is entirely consumer-specific.
