# internal/kernelcallback

The composed `kernelv1.KernelCallbackServiceServer` — the four-method
plugin-to-kernel callback service (`RunSession`, `CountTokens`, `Emit`,
`Log`) described in `specifications/kernel-callbacks.md` §1. Every plugin
subprocess, regardless of category, is handed a client connection to this
service at handshake time; `Server` here is the kernel-side implementation
that connection talks to.

## What this package does

- Delegates `Log` to `internal/log.Server`, which already implements the
  full `Log` RPC (entry validation, level translation, session/producer
  attribution). `internal/log` is unchanged by this package — it never even
  needs to know a composed server exists.
- Stubs `RunSession`, `CountTokens`, and `Emit`, each returning
  `codes.Unimplemented`. These aren't oversights: the packages that carry
  out their real semantics (`agent-loop.md` §7 for `RunSession`,
  `kernel-callbacks.md` §2/§3 for `CountTokens`, `kernel-callbacks.md` §4
  for `Emit`) don't exist yet. Embedding
  `kernelv1.UnimplementedKernelCallbackServiceServer` would already give
  `codes.Unimplemented` for free, but this package defines its own stub
  methods with package-specific error messages so a caller sees
  `kernelcallback: RunSession not implemented`, not a generic proto-gen
  message.

## Producer identity is per-instance, not per-call

`kernel-callbacks.md` §4/§5 require producer attribution to be
server-derived: a property of which plugin's broker connection a call
arrived on, established once at handshake, never a field the calling
plugin supplies on the request. This package expresses that by binding one
`Server` instance to exactly one plugin's `*commonv1.ProducerRef` at
construction time (`NewServer`). Every RPC that instance serves — today
just `Log`, later all four — uses that same fixed identity. There is no
shared server instance juggling multiple plugins' identities and no
interceptor threading identity onto the context from outside; the identity
lives in the `Server` value itself.

## How this fits in

A follow-up task wires `Server` onto the plugin-runtime's callback broker
(the `hashicorp/go-plugin` bidirectional connection handed to each launched
plugin subprocess) — that wiring doesn't exist yet, so don't look for it
here.
