# Tool provider — examples

## A tool provider block in `agent.hcl`

```hcl
required_providers {
  filesystem = {
    source  = "github.com/agentco/provider-filesystem"
    version = "~> 2.0.0"
  }
}

provider "filesystem" {
  allowed_roots = ["${path.root}"]
}
```

`allowed_roots` is the kind of ordinary `Configure` field [`protocol.md#configure`](protocol.md#configure) describes as a provider's capability boundary — not a secret, but a jail root the plugin enforces internally. Resolving `env(...)`-style indirection for any actual secret fields (a hosted `web_search` provider's API key, say) follows the same kernel-side HCL/`cty` bridge described in [`model/examples.md`](../model/examples.md#a-provider-block-in-agenthcl) — the plugin always receives a resolved literal value.

## The wire protocol

This is the wire protocol's service declaration and core call/event messages, in protobuf form (trimmed to the essentials):

```protobuf
service ToolService {
  rpc GetSchema(GetSchemaRequest) returns (GetSchemaResponse);
  rpc Configure(ConfigureRequest) returns (ConfigureResponse);
  rpc Invoke(InvokeRequest) returns (stream InvokeResponse);
  rpc Render(RenderRequest) returns (RenderResponse);
  rpc Preview(PreviewRequest) returns (PreviewResponse);
  rpc Describe(DescribeRequest) returns (DescribeResponse);
}

message ToolCall {
  string id = 1;
  string tool_name = 2;
  google.protobuf.Struct arguments = 3;
  pluggableharness.common.v1.CallContext call_context = 4;
}

message PreviewRequest {
  ToolCall call = 1;
}

message PreviewResponse {
  pluggableharness.render.v1.RenderTree preview = 1;
}

message DescribeRequest {}

message DescribeResponse {
  pluggableharness.common.v1.ProducerRef producer = 1;
}

message ToolEvent {
  oneof event {
    OutputChunk output_chunk = 1;
    Progress progress = 2;
    PartialResult partial_result = 3;
    ExitStatus exit_status = 4;
    ToolResult result = 5;
    ToolError error = 6;
  }

  message OutputChunk {
    OutputStream stream = 1;
    bytes data = 2;
  }

  message Progress {
    string message = 1;
    optional double fraction_complete = 2;
  }

  message PartialResult {
    google.protobuf.Struct payload = 1;
  }

  message ExitStatus {
    int32 exit_code = 1;
    optional string signal = 2;
  }
}

message ToolResult {
  google.protobuf.Struct payload = 1;
}

message ConcurrencySpec {
  bool safe = 1;
  repeated string key_fields = 2;
}
```

`InvokeRequest`/`InvokeResponse` are thin per-RPC envelopes (`{ call = 1; }` / `{ event = 1; }`) around `ToolCall`/`ToolEvent` — see [`data-types.md#toolcall--toolevent--toolresult`](data-types.md#toolcall--toolevent--toolresult).

## A full `Invoke` event sequence

A `bash` tool call that runs a test suite, streams live output, then reports a non-zero exit and a structured result, expressed as the `oneof ToolEvent.event` variants:

```text
→ InvokeRequest{
    call: {
      id: "tc_42",
      tool_name: "bash",
      arguments: {"command": "go test ./..."},
      call_context: {session_id: "01J...", turn_id: "01J...", working_directory: "/home/steven/code/aiagent"},
    }
  }

← InvokeResponse{event: {progress:      {message: "starting process"}}}
← InvokeResponse{event: {output_chunk:  {stream: STDOUT, data: "ok  \tgithub.com/pluggableharness/agent/foo\t0.4s\n"}}}
← InvokeResponse{event: {output_chunk:  {stream: STDOUT, data: "FAIL\tgithub.com/pluggableharness/agent/bar\t0.2s\n"}}}
← InvokeResponse{event: {output_chunk:  {stream: STDERR, data: "--- FAIL: TestBar (0.00s)\n"}}}
← InvokeResponse{event: {exit_status:   {exit_code: 1}}}
← InvokeResponse{event: {result:        {payload: {"exit_code": 1, "summary": "1 package failed"}}}}
```

`exit_status` and `result` are distinct events here precisely because the tool does post-processing (building the `summary` field) after the child process itself has already exited — see [`protocol.md#invoke`](protocol.md#invoke). Note this call's `result` carries a *successful* `ToolEvent` (the tool ran to completion and reports what happened) even though the tested code failed — a non-zero exit from the underlying operation is ordinary `execution_failed`-shaped content inside `result.payload`, not a terminal `ToolError`; see [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy).

If the kernel cancels the stream mid-flight (user hit Ctrl-C while `bash` was still running), the plugin sees the gRPC stream close, sends SIGTERM to the child process, and — per [`protocol.md#invoke`](protocol.md#invoke) — MUST make a best effort to report what already happened before closing:

```text
← InvokeResponse{event: {output_chunk: {stream: STDOUT, data: "ok  \tgithub.com/pluggableharness/agent/foo\t0.4s\n"}}}
← InvokeResponse{event: {partial_result: {payload: {"note": "process received SIGTERM; output above is valid"}}}}
```

— no terminal `result` or `error` follows; the kernel's own bookkeeping records the call as cancelled once the stream closes without one, per [`conformance.md#error-taxonomy`](conformance.md#error-taxonomy)'s `cancelled` category.
