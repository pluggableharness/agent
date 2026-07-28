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
  model = {
    source  = "github.com/pluggableharness/plugin-provider-model"
    version = "~> 0.1"
  }
}

settings {
  default_frontend = "tui"
  log_level        = "debug"
  telemetry        = false
}

agent_profile "default" {
  model {
    primary {
      provider = "model"
      id       = "REPLACE-WITH-A-MODEL-ID"
    }
  }
}
