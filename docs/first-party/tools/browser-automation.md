# Browser Automation

## 1. What it is

Browser automation is the capability class that lets an agent drive an actual (or embedded) web browser as part of its own tool loop: opening a URL, clicking or typing at coordinates or element indices, scrolling, reading back rendered content (text, DOM, screenshots, console/network output), and managing tabs/session storage. It sits apart from `web_fetch` (a single GET-and-return-text operation — see [the reference catalog](../../specifications/tool/reference-catalog.md)) in that it maintains persistent browser *state* across a sequence of calls — navigation history, open tabs, cookies, a live DOM — and lets the model interact with that state iteratively rather than receiving one static document.

In a coding agent's workflow this covers two distinct use cases: (1) **end-to-end / UI testing** — driving a real Chromium instance via Puppeteer or Playwright to click through a web app the agent just built or modified, take screenshots, and read console errors; and (2) **local dev-server preview** — an embedded browser pane inside the IDE showing whatever the user's own dev server is currently rendering, primarily for the *human* to see, with the agent given read access to that same rendered state (DOM, screenshot, console) rather than full interactive control.

Because it requires either a real browser process (Chromium via CDP/Puppeteer/Playwright) or an embedded webview, it is one of the heavier capabilities to implement and sandbox correctly, and it is not part of PluggableHarness Agent's first-party tool reference catalog (see [the reference catalog](../../specifications/tool/reference-catalog.md) for what is).

## 2. Adoption and mechanism

Coverage is uneven and skews toward IDE extensions and Docker-sandboxed autonomous agents, which already run inside a GUI-capable process or a disposable container and can cheaply embed a webview or drive a headless browser. CLI-first and terminal-first harnesses generally leave this to a user-supplied Playwright MCP server rather than bundling a browser dependency.

A few representative implementations:

- An IDE extension (e.g. Cline's `browser_action`) drives Puppeteer against Chrome in debug mode, exposing launch/click/type/scroll/close as one parameterized tool and returning a screenshot plus console logs after every action.
- A Docker-sandboxed autonomous agent (e.g. OpenHands) exposes a large flat bundle of individually-named `browser_*` tools (navigate, click, type, get_state, scroll, list_tabs, get/set_storage, start/stop_recording) built around an element-index addressing model: a `get_state`-equivalent call returns interactive elements enumerated by index, and subsequent clicks/types reference that index rather than a raw coordinate — more robust to layout shifts than pixel-position addressing.
- A dev-server-preview implementation (e.g. Windsurf's `browser_preview` cluster) is architecturally distinct from the other two: it has no independent browser process at all, just an embedded webview pointed at whatever the user's own locally-running dev server is showing, opened after a shell command starts that server. This is a preview pane for the human with read access for the model, not a general-purpose browser the model drives to arbitrary URLs.

Some harnesses fold browser automation into a more general GUI-automation tool (e.g. a `computer_control`-style operation that happens to work against a browser window like any other application) rather than shipping a browser-specific tool at all.

## 3. Cross-tool variation

**Interaction addressing model** splits along two lines: coordinate-based (older, desktop-automation lineage — click at x/y, scroll by pixel delta) versus element-index-based (newer, LLM-ergonomic — enumerate interactive elements and reference them by index). The latter avoids the model having to reason about pixel positions from a screenshot and is more robust to layout changes between calls.

**Underlying engine and process model** varies: Puppeteer-managed Chrome in debug mode, a Playwright-backed process (often via a bundled MCP server), a proprietary headless browser, or — for the dev-server-preview pattern — no independent browser process at all, just an embedded webview over a locally-running server.

**Granularity of the tool surface** ranges from one do-everything tool with an action enum (launch/click/type/scroll/close) to a large flat bundle of individually-named tools (one function per operation), to a cluster of narrowly-scoped read tools (extract DOM, screenshot, list pages) paired with one control tool.

**Return-value behavior** differs meaningfully: some implementations return a full screenshot after every action, which is token/context-heavy; others return clean, filtered Markdown text or a structured element list instead, avoiding a forced image on every turn.

**Feature completeness** varies widely — some implementations are close to a full Playwright/Selenium API surface (arbitrary JS execution in page context, raw keystroke sequences), while others expose only a curated subset. Session recording (for replay/observability) and direct storage manipulation (cookies, localStorage, sessionStorage as first-class operations) are present in some implementations and absent from most.

## 4. Permission, sandbox & safety

Browser automation inherits whichever general-purpose approval model the harness already uses — there is no browser-specific approval primitive distinct from the harness's baseline, with one partial exception: some harnesses give "use browser" its own auto-approval toggle, independent of "read files"/"edit files"/"execute commands", or give individual browser actions their own allow/block lists (e.g. auto-run Navigate while gating Click). Everywhere else, a browser tool is treated like any other named tool under the harness's general per-call or per-category approval model. The most conservative posture observed restricts browser-adjacent GUI automation to sandboxed cloud environments only, opt-in and disabled by default, with no access to local machine credentials.

Sandboxing for the browser process itself is largely absent as a distinct concern — where a harness has a general execution sandbox (a container, a restricted-syscall shell), the browser tool inherits whatever filesystem/process isolation that provides rather than getting a purpose-built boundary of its own. Network-scoping a bundled Playwright MCP server to localhost-only access is a common mitigation, but that's a network decision, not a process sandbox.

**Risk profile**: browser automation is arguably higher-risk than plain `web_fetch` in the same way `bash` is higher-risk than a read-only data source — a single tool spans low-risk operations (screenshot, read DOM) and high-risk ones (arbitrary JS execution, form submission, credential entry into a logged-in session, cookie/storage exfiltration). No implementation observed splits this into separate read-only vs. mutating tool names; every one treats "browser" as one undifferentiated capability at the approval layer, which mirrors exactly the `bash`/`exec` classification precedent (see [the reference catalog](../../specifications/tool/reference-catalog.md#ambiguous-classification-calls)).

## 5. Convergent patterns & divergences

**Convergent**: every implementation treats this as a bundle of related operations under either one parameterized tool or a tightly-scoped set of individually-named tools — nobody exposes raw CDP/Playwright API access directly to the model. Screenshot/content-extraction as a first-class return value is universal too, even in the most minimal implementations.

**Where implementations split**: purpose (general-purpose interactive automation for testing/scraping vs. read-mostly preview of the user's own dev server); addressing model (coordinate-based vs. element-index-based, part of a broader trend toward giving the model structured, indexable references instead of raw positional ones); and sandboxing (no consistent trend — unlike shell execution, where OS-level sandboxing has migrated from research tools into production CLIs, browser automation shows no analogous trend; sandboxing, where present at all, is inherited wholesale from the harness's general execution environment).

## 6. Implications for PluggableHarness Agent

Browser automation is excluded from the first-party tool reference catalog, grouped with cron/scheduling and memory-as-a-tool as differentiators left to future or third-party providers rather than the reference set (see [the reference catalog](../../specifications/tool/reference-catalog.md)).

A few reasons this exclusion holds up:

- **No dominant implementation pattern exists to standardize around.** Unlike `edit_file`, where a plurality pattern (`old_str → new_str`) is identifiable across implementations, browser automation splits along at least two structurally different axes (coordinate vs. element-index addressing; interactive-automation vs. dev-server-preview purpose) with no clear winner. A first-party reference operation would have to arbitrarily pick a lane rather than converge an existing field.
- **The capability doesn't cleanly fit the `kind`/`risk` model without the same ambiguity already flagged for `bash`.** Every implementation treats "browser" as one undifferentiated tool spanning read-only actions and high-risk ones — exactly the situation the reference catalog resolves for `bash`/`exec` by classifying the whole operation `resource`/`high` and pushing finer-grained distinctions to policy rather than the protocol. A hypothetical `browser`/`navigate` provider would face the identical call: classify the whole tool `resource`/`high` (or `critical`, given the added exfiltration surface of storage/cookie access that `bash` doesn't have), and let policy narrow it from there. No spec change is needed to accommodate this — it falls out of the existing `kind`/`risk` model plus the `bash` precedent.
- **It is not a plugin-category mismatch either.** Browser automation is squarely an ordinary tool-provider-shaped `resource`/`interactive`-adjacent operation set; it's excluded from the reference catalog on convergence grounds, not excluded from the protocol. Any third-party provider implementing it (e.g. wrapping Playwright) would use the same `GetSchema`/`Configure`/`Invoke` surface as any other tool provider — no protocol change needed to admit it later if demand emerges.
