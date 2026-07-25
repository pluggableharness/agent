# internal/plandecision/drivers/fake — agent notes

- **This fake is not registered in the selector, and shouldn't be.** Its per-call scripted responses have no representation in `drivers.Config`, and a name-selectable fake would be one more way to obtain a resolver that never asks a human. Tests import it directly.
- **Overrunning the script is `ErrExhausted`, not a repeat of the last response.** Silently repeating would let a test that resolves more items than it scripted still pass, asserting against a verdict nobody wrote down. Keep the error.
- **`Resolve` checks `ctx.Err()` before recording the call**, so a cancelled resolve leaves `Calls()` untouched — matching what a real resolver does (it never got far enough to do anything) and letting a test assert "the gate stopped calling us after cancellation".
- **It honors `ctx` cancellation deliberately, even though a fake could ignore it.** Every `plandecision.Resolver` implementation must; a fake that didn't would let a consumer's cancellation bug pass in tests and surface only against the real `drivers/frontend`.
- **Mutex-guarded because the plan/apply gate resolves items concurrently.** Tests run under `-race`; don't drop the lock "because it's just a test double".
- The zero value is a valid `Resolver` with an empty queue — every call fails with `ErrExhausted`. That's intentional (a fake nobody scripted approves nothing), not an oversight to paper over with a default response.
