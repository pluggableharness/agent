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

  # The model provider is built outside this repository. A session cannot
  # start without one: internal/session resolves the profile's model chain
  # against the live catalog and fails with ErrNoDefaultModel if nothing
  # answers. Point this at your provider binary.
  model = "/path/to/your/model-provider/bin/model_provider"
}
