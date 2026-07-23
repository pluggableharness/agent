# Context provider — examples

## The wire protocol

The wire protocol below is trimmed to the service declaration and the two central messages.

```protobuf
service ContextService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);

  // buf:lint:ignore RPC_REQUEST_STANDARD_NAME
  // buf:lint:ignore RPC_RESPONSE_STANDARD_NAME
  rpc Contribute(ContextRequest) returns (ContextContribution);

  rpc Render(RenderRequest) returns (RenderResponse);
}

message ContextRequest {
  string session_id = 1;
  string parent_session_id = 2;
  int64 turn_number = 3;
  int64 token_budget = 4;
  pluggableharness.agent.model.v1.ModelTarget model_target = 5;
  repeated string files_touched = 6;
  string working_directory = 7;
  repeated ContextSection prior_sections = 8;
  repeated pluggableharness.agent.content.v1.Message conversation_history = 9;
}

message ContextSection {
  string provider = 1;
  string label = 2;
  repeated pluggableharness.agent.content.v1.ContentBlock content = 3;
  int64 tokens = 4;
  Stability stability = 5;
  bool truncated = 6;
}
```

Note the two `buf:lint:ignore` annotations on `Contribute`: the request and response are the bare `ContextRequest`/`ContextContribution`, not `ContributeRequest`/`ContributeResponse` — an intentional, annotated deviation from buf's default RPC-naming lint, chosen because neither name is reused by another RPC in this file and the spec's own names (`ContextRequest`, `ContextContribution`) carry real documentation value that a generic `ContributeRequest` wrapper wouldn't. This mirrors [`provider/examples.md`](../provider/examples.md#the-wire-protocol)'s identical annotation on `StreamCompletion`.

## A worked `context-assemble` sequence

Two context providers declared in `agent.hcl`, in this order: a top-level CLAUDE.md reader (`static`, whole-session eager injection) followed by an AGENTS.md reader (`static`, JIT-scoped to touched subdirectories). Per [`data-types.md#ordering--chaining`](data-types.md#ordering--chaining), the kernel calls them in that declaration order, threading each one's returned chain into the next as `prior_sections`.

```hcl
required_providers {
  claude-md = { source = "github.com/agentco/context-claude-md", version = "~> 1.0" }
  agents-md = { source = "github.com/agentco/context-agents-md", version = "~> 1.0" }
}

context "claude-md" {
  path = "CLAUDE.md"
}

context "agents-md" {
  glob = "**/AGENTS.md"
}
```

**Turn 1** (`files_touched: []`, session start):

```text
→ ContextRequest{
    session_id: "sess_01", turn_number: 1,
    token_budget: 2000,
    model_target: { id: "claude-opus-5", context_window: 200000, effective_ceiling: 176000 },
    files_touched: [],
    prior_sections: [],
  }
← ContextContribution{
    sections: [
      { provider: "claude-md", label: "Project conventions (CLAUDE.md)",
        content: [{text: {text: "This repo uses..."}}], tokens: 480,
        stability: STABILITY_STATIC, truncated: false },
    ],
  }
```

The kernel then calls `agents-md` with `prior_sections` set to the one section above:

```text
→ ContextRequest{
    ... token_budget: 1500,
    prior_sections: [ { provider: "claude-md", ... } ],
  }
← ContextContribution{
    sections: [
      { provider: "claude-md", ... },   // unchanged — agents-md doesn't own it
      { provider: "agents-md", label: "AGENTS.md (root)",
        content: [{text: {text: "..."}}], tokens: 210,
        stability: STABILITY_STATIC, truncated: false },
    ],
  }
```

Neither provider mutated the other's section — each returned the full chain with only its own addition, per the own-section-only rule.

**Turn 4** (the agent has since edited `src/auth/validator.py`):

```text
→ ContextRequest{
    ... turn_number: 4,
    files_touched: ["src/auth/validator.py"],
    prior_sections: [ { provider: "claude-md", ... }, { provider: "agents-md", ... } ],
  }
← ContextContribution{
    sections: [
      { provider: "claude-md", ... },
      { provider: "agents-md", label: "AGENTS.md (root)", ... },   // reused unchanged
      { provider: "agents-md", label: "AGENTS.md (src/auth)",
        content: [{text: {text: "..."}}], tokens: 90,
        stability: STABILITY_STATIC, truncated: false },
    ],
  }
```

`agents-md` reacted to `files_touched` by contributing a second, subdirectory-scoped section this firing — JIT loading in practice (see [`README.md#firing-cadence--jit-loading`](README.md#firing-cadence--jit-loading)). Both `agents-md` sections carry the same `provider` name; the kernel and any compactor treat `provider` as an identity key for a provider's own contributions, not a guarantee of exactly one section per provider per turn.

## Budget worked example

Given a `claude-opus-5` session with `context_window: 200000`, `reserved_output: 8192`, and `system_overhead: 15808` (tool schemas + fixed framing):

```text
effective_ceiling = 200000 − 8192 − 15808 = 176000
```

`claude-md` declares `default_token_budget: 2000` in `GetCapabilities` and `agent.hcl` doesn't override it, so `token_budget: 2000` is what it receives on `ContextRequest` (per [`data-types.md#budget-mechanics`](data-types.md#budget-mechanics)). Its CLAUDE.md is 7.4KB of markdown; `CountTokens` resolves that to roughly 1,850 tokens against `claude-opus-5`'s real tokenizer — under budget, so the provider returns it whole, `truncated: false`.

`agents-md` declares `default_token_budget: 1500`; its combined root + subdirectory AGENTS.md content resolves to 1,620 tokens — over budget. Per the MUST in [`data-types.md#budget-mechanics`](data-types.md#budget-mechanics), `agents-md` itself performs the reduction (dropping its lowest-priority paragraph) before returning, landing at 1,480 tokens, `truncated: true`. Had it returned 1,620 tokens unreduced, the kernel would reject that section entirely rather than truncate the bytes itself — `agents-md` is the content producer and the only party that can cut mid-unit-safely.

At config-load time, the kernel sums the two providers' caps (2000 + 1500 =
3500) against the smallest model in any configured routing/fallback chain's
`effective_ceiling` and warns only if that sum could exceed it — 3500 tokens against a 176,000-token ceiling doesn't trip the warning here, but would if a sub-agent profile routed this same provider set to a much smaller-context model.
