#!/bin/sh
# Seed the livesync-cli settings into the database dir, then launch the MCP
# server (which supervises the CLI daemon).
#
# Continuous sync is driven by LIVESYNC_INTERVAL (the daemon's `--interval`
# poll), not by the liveSync settings flag — the CLI resets that flag during its
# startup migration, whereas the CLI flag is always honoured.
set -e

mkdir -p "${LIVESYNC_VAULT:-/vault}"
/usr/local/bin/seed-settings.sh

echo "[entrypoint] starting livesync-mcp (interval=${LIVESYNC_INTERVAL:-0}s)"
exec livesync-mcp
