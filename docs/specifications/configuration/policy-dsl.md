# Policy DSL

`policy "<name>" { ... }` blocks are the first-party, kernel-privileged rule-matching DSL that decides `allow`/`ask`/`deny` for resource, data-source, and interactive tool calls. Mechanically, policy is the kernel-privileged `veto`-mode subscriber at the `plan-ready` hook — always run, always respected. See [`glossary.md`](../glossary.md) and [`architecture.md#policy--first-party-not-a-plugin-category`](../architecture.md#policy--first-party-not-a-plugin-category).

```hcl
policy "auto_approve_reads" {
  match  = { kind = "data_source" }
  action = "allow"
}

policy "gate_filesystem_writes" {
  match  = { provider = "filesystem", kind = "resource" }
  action = "ask"
}

policy "block_high_risk" {
  match  = { risk = "critical" }
  action = "deny"
}
```

## Match schema

```protobuf
PolicyMatch {
  kind       enum { resource, data_source }?   // MAY be omitted
  provider   string?                            // MAY be omitted
  tool_name  string?                             // MAY be omitted
  risk       RiskClass?    // per tool/data-types.md#riskclass    // MAY be omitted
}
```

An omitted field matches anything; specified fields are ANDed together. A `policy` block with an entirely empty `match = {}` matches every call — legal, but MUST be flagged with a config-load-time warning (almost always an authoring mistake).

**`PolicyMatch.kind` stays two-valued — a documented, deliberate v1 limitation.** [`tool/protocol.md`](../tool/protocol.md#kind-interactive)'s third tool kind, `interactive`, is **not** mirrored onto policy match kinds — `kind` accepts only `resource` or `data_source`. An operator therefore cannot write `match = { kind = "interactive" }`; extending `kind` to a third value would require a deliberate future spec change. This does not put interactive calls outside policy evaluation: interactive calls can still be targeted through `tool_name`, `provider`, or `risk`, and they route through the same non-interactive-style precheck data-source calls use — see [Evaluation semantics](#evaluation-semantics) below and [`../agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md#data-source-and-interactive-calls).

Each match field, when omitted, matches unambiguously as "not specified" rather than as some zero value. The call being evaluated, in contrast, does carry the full three-value tool kind (including `interactive`) — only the match criteria's `kind` field is restricted to two values.

## Conflict detection

Conflicting matches resolve **most-specific-wins**, with specificity defined as a fixed field hierarchy (most to least specific): `tool_name` > `provider` > `risk` > `kind`. Comparison is lexicographic over "does this rule specify this field," in that field order:

```text
specificity(rule) = (rule.match.tool_name != nil,
                      rule.match.provider  != nil,
                      rule.match.risk      != nil,
                      rule.match.kind      != nil)
```

compared as a 4-tuple of booleans, most-specific tuple wins.

A conflict requires an identical specificity tuple **and**, for every field both rules specify, equal values — a field only one of the two rules specifies never disqualifies a conflict; it's the fields *both* specify that must agree. **Two rules whose match criteria conflict under this rule MUST be a config-load-time error.**

Specificity-tuple equality alone is not sufficient for conflict detection: two rules can share an identical tuple (e.g. both `(true, false, false, false)`, i.e. both specify only `tool_name`) while specifying disjoint *values* for that field, e.g. one rule matching `tool_name = "read_file"` and another matching `tool_name = "write_file"`. A real call has exactly one `tool_name`, so the two rules can never both match the same call — they are not actually in conflict, and flagging them as a load-time error would force an operator to artificially over-specify two rules that were never ambiguous. See [`examples.md#a-non-conflicting-same-tuple-pair`](examples.md#a-non-conflicting-same-tuple-pair) for a worked instance.

Conflict checking scans all rule pairs and reports the first conflicting pair found, not necessarily every conflicting pair in the set — catching at least one conflict at load time is all this rule requires; it does not guarantee an exhaustive report of every conflict when several exist simultaneously.

## Evaluation semantics

Policy evaluation covers **all** tool calls, not only resource calls — widened specifically so that a rule like `match = { kind = "data_source" }, action = "allow"` is meaningfully expressible, while preserving the "data sources execute freely by default" principle that motivated the resource/data-source split in the first place:

```go
evaluate_policy(call) -> PolicyDecision:
  candidates := [p for p in policy_rules if match(p.match, call)]
  if candidates is empty:
    return (kind(call) in {data_source, interactive}) ? allow : ask
       // conservative default for resources, free default for reads
       // and interactive calls (agent-loop.md's plan-apply-gate reuses
       // this precheck verbatim for interactive calls)
  winning := most_specific(candidates)   // ties are a load-time error,
                                          // never reached here
  if kind(call) in {data_source, interactive} and winning.action == ask:
    log_warning("policy rule %q resolved to ask against a %s call; "
                "no apply-time gate exists for it — downgraded to deny",
                winning.name, kind(call))
    return deny
  return winning.action
```

- **`allow`** on a `data_source`/`interactive` call is a no-op — the call was going to execute anyway — but now the auto-approve-reads pattern is meaningfully expressible as the *explicit* form of the otherwise-implicit default.
- **`deny`** on a `data_source`/`interactive` call is meaningful: an operator can hard-block a specific read or interactive operation (e.g. "never call `web.web_search`") as a genuine blocklist, distinct from the resource-oriented approval gate. `deny` MUST synthesize a `tool_result` denial block the same way a denied resource call does — see [`../agent-loop/turn-algorithm.md`](../agent-loop/turn-algorithm.md).
- **`ask`** has no meaning against a `data_source`/`interactive` call — there is no apply step to gate a read (or a blocked-on-human-input call) behind. Rather than making this a config-load-time error (which would force every operator to redundantly scope `kind = resource` on every `ask` rule just to avoid the edge case), the kernel **downgrades it to `deny` at evaluation time and logs why** — the operator sees the real behavior against reads without being forced into more verbose match criteria for the common case.
- For `resource` calls, evaluation and `allow`/`ask`/`deny` semantics are exactly what [`../agent-loop/plan-apply-gate.md`](../agent-loop/plan-apply-gate.md) specifies — unchanged by this section.
- **Interactive calls extend the same data-source-shaped precheck** — they are not gated by the resource plan/apply flow (nothing to approve, only a question to answer), but they DO pass through this non-interactive, allow/deny-only policy lane before executing. See [`../agent-loop/plan-apply-gate.md#data-source-and-interactive-calls`](../agent-loop/plan-apply-gate.md#data-source-and-interactive-calls).

Evaluation surfaces whether a winning `ask` was downgraded to `deny` for a `data_source`/`interactive` call, so the required warning (above) can be logged by the caller. If more than one candidate ties for most specific — a state conflict detection is meant to reject at config-load time — evaluation treats it as an already-validated precondition and deterministically picks one of the tied candidates rather than erroring at runtime.
