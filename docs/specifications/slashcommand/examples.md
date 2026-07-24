# Slash-command provider — examples

## A slash-command provider block in `agent.hcl`

```hcl
required_providers {
  release-tools = {
    source  = "github.com/agentco/provider-release-tools"
    version = "~> 1.0.0"
  }
}

provider "release-tools" {
  changelog_path = "CHANGELOG.md"
}
```

As with every other category, `release-tools`'s category is never declared in `agent.hcl` — the kernel discovers it's a `slashcommand.v1` provider at runtime, from its own `GetCapabilities` response, per [`configuration/blocks-reference.md#required_providers`](../configuration/blocks-reference.md#required_providers). `Describe` (see [`protocol.md#describe`](protocol.md#describe)) is the only place `category` appears as an explicit field, and only for the `dev_overrides` identity case.

## The wire protocol

This is the wire protocol's service declaration and core call/event messages, in protobuf form (trimmed to the essentials):

```protobuf
service SlashCommandService {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (GetCapabilitiesResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Invoke(InvokeRequest) returns (stream InvokeResponse);
  rpc Render(RenderRequest) returns (RenderResponse);
  rpc Preview(PreviewRequest) returns (PreviewResponse);
  rpc Describe(DescribeRequest) returns (DescribeResponse);
}

message SlashCommandCall {
  string id = 1;
  string name = 2;
  google.protobuf.Struct arguments = 3;
  pluggableharness.common.v1.CallContext call_context = 4;
}

message PreviewRequest {
  SlashCommandCall call = 1;
}

message PreviewResponse {
  pluggableharness.render.v1.RenderTree preview = 1;
}

message DescribeRequest {}

message DescribeResponse {
  pluggableharness.common.v1.ProducerRef producer = 1;
}
```

`InvokeRequest`/`InvokeResponse` are thin per-RPC envelopes (`{ call = 1; }` / `{ event = 1; }`) around `SlashCommandCall`/`SlashCommandEvent` — see [`data-types.md#slashcommandcall--slashcommandevent`](data-types.md#slashcommandcall--slashcommandevent).

## A `GetCapabilitiesResponse` snippet

`release-tools` declaring a single `/changelog` command — a `TOOL_KIND_DATA_SOURCE` read of the repo's changelog file, so it executes freely without going through the plan/apply gate:

```text
← GetCapabilitiesResponse{
    commands: [
      {
        name: "changelog",
        description: "Show unreleased entries from CHANGELOG.md",
        input_schema: {type: "object", properties: {since: {type: "string"}}},
        kind: TOOL_KIND_DATA_SOURCE,
        risk: RISK_CLASS_READ_ONLY,
        concurrency: {safe: true},
        streaming: false,
        idempotent: true,
      }
    ],
    config_schema: {...},
    supported_hook_points: [],
  }
```

## A full `Invoke` event sequence

A `/changelog` call that reads the file and returns its unreleased section, expressed as the `oneof SlashCommandEvent.event` variants:

```text
→ InvokeRequest{
    call: {
      id: "sc_17",
      name: "changelog",
      arguments: {},
      call_context: {session_id: "01J...", turn_id: "01J...", working_directory: "/home/steven/code/aiagent"},
    }
  }

← InvokeResponse{event: {progress: {message: "reading CHANGELOG.md"}}}
← InvokeResponse{event: {result:   {payload: {"unreleased": "- Add slashcommand.v1 category\n"}}}}
```

Because `TOOL_KIND_DATA_SOURCE` commands bypass the resource plan/apply gate entirely (per [`protocol.md#invoke`](protocol.md#invoke)), this call never produces a `PlanItem` awaiting approval — it dispatches and streams back immediately.

If the kernel cancels the stream mid-flight (e.g. the user interrupted the turn while `/changelog` was still reading), the plugin sees the gRPC stream close and — per [`protocol.md#invoke`](protocol.md#invoke), reusing [`tool/protocol.md#invoke`](../tool/protocol.md#invoke)'s cancellation contract — MUST make a best-effort report of what already happened before closing, for any `TOOL_KIND_RESOURCE` command in flight:

```text
→ InvokeRequest{
    call: {
      id: "sc_18",
      name: "release",
      arguments: {"version": "1.4.0"},
      call_context: {session_id: "01J...", turn_id: "01J...", working_directory: "/home/steven/code/aiagent"},
    }
  }

← InvokeResponse{event: {progress:     {message: "tagging v1.4.0"}}}
← InvokeResponse{event: {output_chunk: {stream: OUTPUT_STREAM_STDOUT, data: "Created tag v1.4.0\n"}}}
← InvokeResponse{event: {partial_result: {payload: {"note": "tag created; changelog rewrite cancelled before it ran"}}}}
```

— no terminal `result` or `error` follows; the kernel's own bookkeeping records the call as cancelled once the stream closes without one, per [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)'s `cancelled` category, the same reused `tool.v1.ToolErrorCategory` value [`tool/conformance.md#error-taxonomy`](../tool/conformance.md#error-taxonomy) defines.
