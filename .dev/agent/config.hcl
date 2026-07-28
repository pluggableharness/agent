# Global config for a local dev run — $XDG_CONFIG_HOME/agent/config.hcl,
# with XDG_CONFIG_HOME pointed at .dev/ (see .dev/README.md).
#
# dev_overrides maps a required_providers local name to a binary on disk.
# The kernel uses that binary directly and skips the whole registry path:
# no version constraint, no lock-file row, no checksum. Identity comes from
# the plugin's own Describe RPC instead, which is exactly what
# dev_overrides exists for
# (docs/specifications/configuration/lock-file.md#dev_overrides-and-identity-without-a-lock-entry).
#
# Paths must be absolute — edit these to match your checkouts.

dev_overrides {
  tui = "/home/steven/pluggableharness/plugin-frontend-tui/bin/frontend_tui"

  # A session cannot start without a model provider: internal/session
  # resolves the profile's model chain against the live catalog and fails
  # with ErrNoDefaultModel if nothing answers. This one authenticates from
  # ~/.grok/auth.json, so a `grok login` session is enough — no API key.
  xai = "/home/steven/pluggableharness/plugin-provider-xai/bin/provider_xai"
}
