#!/bin/sh
# Seed the livesync-cli settings into the database dir, then launch the MCP
# server (which supervises the CLI daemon).
#
# CouchDB connection is taken from the environment:
#   COUCHDB_URI, COUCHDB_USER, COUCHDB_PASSWORD, COUCHDB_DBNAME
# Optional E2EE: COUCHDB_PASSPHRASE (enables encryption when non-empty).
#
# Continuous sync is driven by LIVESYNC_INTERVAL (the daemon's `--interval`
# poll), not by the liveSync settings flag — the CLI resets that flag during its
# startup migration, whereas the CLI flag is always honoured.
set -e

DB_DIR="${LIVESYNC_DB:-/db}"
VAULT_DIR="${LIVESYNC_VAULT:-/vault}"
SETTINGS_FILE="$DB_DIR/.livesync/settings.json"

mkdir -p "$DB_DIR/.livesync" "$VAULT_DIR"

if [ ! -f "$SETTINGS_FILE" ]; then
    echo "[entrypoint] generating settings -> $SETTINGS_FILE"
    livesync-cli init-settings --force "$SETTINGS_FILE" >/dev/null
fi

# Seed the CouchDB connection only if no remote is configured yet. Re-seeding on
# every boot would re-trigger the migration and churn the settings, so we guard
# on whether a remote already exists (top-level URI or a migrated remote entry).
NEEDS_SEED=$(SETTINGS_FILE="$SETTINGS_FILE" node -e '
const fs=require("fs");const d=JSON.parse(fs.readFileSync(process.env.SETTINGS_FILE,"utf8"));
const hasRemote = !!d.couchDB_URI || (d.remoteConfigurations && Object.keys(d.remoteConfigurations).length>0);
process.stdout.write(hasRemote ? "0" : "1");
')

if [ "$NEEDS_SEED" = "1" ]; then
    echo "[entrypoint] seeding CouchDB connection"
    SETTINGS_FILE="$SETTINGS_FILE" \
    COUCHDB_URI="${COUCHDB_URI:-}" \
    COUCHDB_USER="${COUCHDB_USER:-}" \
    COUCHDB_PASSWORD="${COUCHDB_PASSWORD:-}" \
    COUCHDB_DBNAME="${COUCHDB_DBNAME:-}" \
    COUCHDB_PASSPHRASE="${COUCHDB_PASSPHRASE:-}" \
    node <<'NODE'
const fs = require("node:fs");
const p = process.env.SETTINGS_FILE;
const data = JSON.parse(fs.readFileSync(p, "utf-8"));
data.couchDB_URI = process.env.COUCHDB_URI;
data.couchDB_USER = process.env.COUCHDB_USER;
data.couchDB_PASSWORD = process.env.COUCHDB_PASSWORD;
data.couchDB_DBNAME = process.env.COUCHDB_DBNAME;
data.remoteType = "";
const pass = process.env.COUCHDB_PASSPHRASE || "";
data.encrypt = pass !== "";
data.passphrase = pass;
data.isConfigured = true;
fs.writeFileSync(p, JSON.stringify(data, null, 2), "utf-8");
console.error(`[entrypoint] configured ${data.couchDB_URI} db=${data.couchDB_DBNAME}`);
NODE
fi

echo "[entrypoint] starting livesync-mcp (interval=${LIVESYNC_INTERVAL:-0}s)"
exec livesync-mcp
