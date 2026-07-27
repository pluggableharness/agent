// Package sse decodes a server-sent event stream into its raw frames.
//
// Every major LLM vendor streams completions over SSE, so a model-provider
// plugin author would otherwise write this same framing loop once per
// vendor. It lives here rather than inside any one provider because
// nothing about it is vendor-specific: the framing is the SSE wire format
// (one "name: value" field per line, blank-line-separated events,
// ":"-prefixed comments ignored, multi-line data concatenated with "\n").
//
// # What this package does and does not do
//
// A Scanner yields frames. It never interprets one: decoding a frame's
// Data into a vendor's own event type, deciding which event names are
// terminal, and recognizing a vendor's end-of-stream sentinel are all
// caller concerns, because vendors disagree about every one of them.
// Anthropic treats the JSON payload's own "type" field as authoritative
// and ignores the event: line entirely; OpenAI uses the event: line and
// ends a stream with a literal "[DONE]" data payload. A framing package
// that took a position on either would be wrong for somebody.
//
// # Usage
//
//	scan := sse.NewScanner(resp.Body)
//	for scan.Next() {
//		if scan.IsDone() {
//			break
//		}
//		var ev vendorEvent
//		if err := json.Unmarshal(scan.Data(), &ev); err != nil {
//			return err
//		}
//		// ...
//	}
//	if err := scan.Err(); err != nil {
//		return err
//	}
package sse
