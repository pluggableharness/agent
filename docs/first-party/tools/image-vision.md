# Image / Vision Input

## What it is

Image/vision input is the ability of a coding-agent harness to place image content — a screenshot, a diagram, a UI mockup, a PDF page, an image embedded in a document — in front of the underlying LLM as part of its input context, so the model can visually inspect it rather than only reading text. It sits alongside file read, notebook editing, and diagnostics as a "what can the model perceive" capability, not a "what can the model change" one.

Concretely, harnesses expose this in one of three shapes: (a) folded into an existing general-purpose file-read tool, which returns image bytes/base64 alongside text when the target path is an image; (b) a dedicated single-purpose tool (`view_image`, `read_image`) whose only job is loading an image file or URL and handing it to the model as vision content; or (c) as a byproduct of another operation — most commonly browser automation, where a screenshot action captures the current viewport and feeds it back as an image observation rather than text. A fourth, narrower pattern is images *embedded inside another document format* (a PDF page, a `.docx` figure) that a document-reading tool extracts and forwards as image content.

This is fundamentally a **model capability**, not a tool operation: it only works at all when the selected model declares vision support, and the wire-level mechanism is a canonical `image` content-block that any model-provider adapter must translate to/from its vendor's format. A tool that "reads an image" is, from the protocol's perspective, an ordinary read operation whose output schema happens to include an image content-block — the vision part of the equation belongs to the model provider, not the tool provider.

A narrower, genuinely tool-shaped case is image *generation* — producing new image content from a text/reference-image prompt. That's an ordinary mutating operation with a tool-provider shape (it creates an artifact), addressed separately below, not conflated with vision *input*.

## Common implementation patterns

Four distinct implementation patterns recur:

- **Folded into general file-read**: the same tool that reads source text also returns image content when the target path is an image, downscaling oversized images to fit model limits alongside plain text and PDF pages. This is the majority pattern where the capability exists at all.
- **Dedicated single-purpose tool** (`view_image`, `read_image`): a standalone tool whose only job is loading an image and handing it to the model, typically taking a path and an optional detail/resolution parameter and erroring if the active model lacks vision support.
- **Screenshot/GUI-capture as a byproduct of browser automation**: the image isn't read from disk but generated at runtime by the harness driving a browser or desktop, then returned as vision content. This ties the capability to browser automation's risk profile rather than to file-reading.
- **Document-embedded image extraction**: images arrive nested inside another document format (a PDF page, a Word-document figure) rather than as standalone files.

Some harnesses expose no callable tool for image input at all — the most plausible reading is that images enter the conversation as a direct chat attachment/paste handled entirely by the model-provider/frontend layer, never touching the tool registry. This is independent evidence that the capability doesn't strictly need a tool-provider surface to exist.

A capability-gating convergence is worth noting: dedicated vision tools commonly return an explicit error rather than silently degrading when the active model lacks vision support — application-level enforcement of the same rule the model-provider protocol requires (image content MUST be rejected with a clear error, not silently dropped, when the model doesn't support vision).

## Permission, sandbox & safety

The file-read-shaped variant of this capability (folded into a general read tool, or a dedicated `read_image`-style tool) carries the same low risk profile as ordinary file reading — no approval requirement, no OS-level sandboxing distinct from the file-read tool it sits alongside.

The **screenshot/GUI-capture** variant is materially different: it inherits whatever permission and sandboxing regime the harness applies to browser or desktop automation, not to file reads. A browser-driven screenshot action commonly requires approval by default (configurable via manual/allow-listed/auto-run modes); a desktop-automation screenshot tool may run only in a sandboxed, opt-in environment with no access to local machine credentials. In other words: **risk here attaches to the capture mechanism (browser/desktop control), not to the vision modality itself** — a pure `read_image`-style tool is no riskier than `read_file`, while a screenshot tool bundled with click/type/navigate is exactly as risky as the browser-automation cluster it's part of.

No OS-level sandboxing distinguishes image reading specifically; sandboxing, where it exists, is scoped to shell/exec, not to file-read or vision tools.

## Design considerations

Where a dedicated or folded-in tool exists at all, it is uniformly read-only/no-approval — no common implementation gates a plain "read this image file" call behind user confirmation. There's also convergence on capability-gating discipline: an explicit error on a non-vision model, matching the model-provider protocol's own reject rule.

Whether vision input needs a tool at all is a genuine architectural split: some fold it into an existing file-read tool, some expose it as its own primitive, and some show no tool at all — images most likely enter via direct chat attachment. This is the difference between "vision is a mode of an existing tool's output" and "vision is a frontend/model concern the tool layer never touches."

A related, unresolved split: capturing a screenshot rather than reading a file pulls the capability's risk profile toward whatever governs browser/GUI-automation tools instead of file-read tools. No unified risk treatment spans the two paths — a `read_image` call and a `screenshot` call feed the model literally the same kind of content-block but can sit under completely different approval regimes.

Vision input adoption generally lags behind general tool-calling maturity — several harnesses with otherwise mature, modern tool architectures show no vision input path at all.

## Implications for PluggableHarness Agent

This capability is **not itself a tool-provider operation** — it's the model-provider capability declared via `ModelSpec.supports_vision` and the canonical `image` content-block, which every adapter MUST support wherever vision is supported and MUST reject cleanly otherwise (see [`provider/data-types.md`](../../specifications/provider/data-types.md#modelspec) and its [canonical message schema](../../specifications/provider/data-types.md#canonical-message--content-block-schema)). It does not appear, and should not appear, as its own row in the [tool reference catalog](../../specifications/tool/reference-catalog.md).

Where it *does* touch the tool layer is exactly the majority pattern above: the reference catalog already lists `filesystem`/`read_file` as a first-party reference tool (`data_source`/`read_only`). The natural design implication is that a reference `read_file` implementation's `output_schema` SHOULD be able to return an `image` content-block when the target path is an image and the routed model supports vision — rather than inventing a separate `read_image` catalog entry. This doesn't require a catalog amendment on its own: `read_file`'s existing `kind`/`risk` already fit an image read exactly as well as a text read, since neither mutates or reads anything external beyond the filesystem.

The screenshot/GUI-capture variant is different: it's downstream of browser automation, which the reference catalog deliberately excludes as a differentiator left to third-party providers. A third-party browser automation provider that exposes a screenshot-equivalent operation would need its `output_schema` to support an `image` content-block too, following the same mechanism — but such a provider should inherit `bash`/`exec`-style `resource`/`high` treatment (or whatever `kind`/`risk` the browser-automation operation itself carries) rather than being reclassified as `read_only` just because its output happens to be an image.

Image *generation* is the one piece of this capability that would be an ordinary tool-provider operation if added: it creates an artifact, so it would be `kind = resource` with some `risk` above `read_only` (plausibly `low`, similar to writing a scratch file, absent evidence of a wider blast radius). It's well below the common-core bar and isn't in the reference catalog — consistent with leaving it to third-party providers for now, the same treatment given to browser automation and cron/scheduling.
