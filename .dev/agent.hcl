# Project config for a local dev run. Not the repository's own config —
# this repo is the kernel, not a project — so it lives under .dev/ and is
# passed explicitly with -config.
#
# Both providers resolve through dev_overrides in .dev/agent/config.hcl, so
# the source/version below are never fetched. They still have to be
# declared: required_providers is what creates the local name a
# dev_overrides entry, a provider block, and an agent_profile all refer to.

required_providers {
  tui = {
    source  = "github.com/pluggableharness/plugin-frontend-tui"
    version = "~> 0.1"
  }
  xai = {
    source  = "github.com/pluggableharness/plugin-provider-xai"
    version = "~> 0.1"
  }
}

settings {
  default_frontend = "tui"
  log_level        = "debug"
  telemetry        = false
}

provider "xai" {
  # Pinned off deliberately, and the reason is a real protocol gap rather
  # than a preference.
  #
  # pluginhost fetches GetCapabilities at bring-up step 5, BEFORE Configure
  # at step 8 — it has to, because the ConfigSchema that decoding the
  # provider block needs arrives with that advertisement. So a provider
  # whose roster depends on Configure-time state advertises its built-in
  # roster and only then swaps in the remote one, leaving the kernel
  # holding a catalog the provider itself no longer honors: the kernel
  # offers "grok-4-3" while StreamCompletion rejects it as unknown, since
  # the live catalog names it "grok-4.3".
  #
  # Capabilities are never re-fetched (providercatalog is built once, in
  # bringUp), so nothing reconciles them later. Until a refresh path
  # exists, keeping the roster static is what makes advertisement and
  # enforcement agree.
  fetch_models = "false"
}

agent_profile "default" {
  model {
    primary {
      # In the compiled-in roster AND a valid API id, which is what makes
      # this the one combination that works. xAI resolves it server-side
      # to grok-4.3 and says so on the wire — the remap StreamMetadata's
      # actual_model now captures. Note the roster's other ids are
      # hyphenated (grok-4-3) and the API rejects them outright; the live
      # catalog spells that model grok-4.3.
      provider = "xai"
      id       = "grok-4"
    }
  }
}
