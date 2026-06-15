#!/bin/sh
# Seed the livesync-cli settings into the database dir, then launch the MCP
# server (which supervises the CLI daemon).
#
# CouchDB connection is taken from the environment:
#   COUCHDB_URI, COUCHDB_USER, COUCHDB_PASSWORD, COUCHDB_DBNAME
# Optional E2EE: COUCHDB_PASSPHRASE (enables encryption when non-empty).
set -e

DB_DIR="${LIVESYNC_DB:-/db}"
VAULT_DIR="${LIVESYNC_VAULT:-/vault}"
SETTINGS_FILE="$DB_DIR/.livesync/settings.json"

mkdir -p "$DB_DIR/.livesync" "$VAULT_DIR"

if [ ! -f "$SETTINGS_FILE" ]; then
    echo "[entrypoint] generating settings -> $SETTINGS_FILE"
    livesync-cli init-settings --force "$SETTINGS_FILE" >/dev/null
fi

# Apply CouchDB connection details from the environment.
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
console.error(`[entrypoint] settings configured for ${data.couchDB_URI} db=${data.couchDB_DBNAME}`);
NODE

echo "[entrypoint] starting livesync-mcp"
exec livesync-mcp
