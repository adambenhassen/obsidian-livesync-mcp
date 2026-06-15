#!/bin/sh
# Seed the livesync-cli settings (CouchDB connection) into the database dir,
# idempotently. Shared by the runtime entrypoint and the e2e test runner.
#
# Reads: LIVESYNC_DB, COUCHDB_URI, COUCHDB_USER, COUCHDB_PASSWORD,
#        COUCHDB_DBNAME, COUCHDB_PASSPHRASE (optional E2EE),
#        USE_PATH_OBFUSCATION (must match the vault's setting).
set -e

DB_DIR="${LIVESYNC_DB:-/db}"
SETTINGS_FILE="$DB_DIR/.livesync/settings.json"
mkdir -p "$DB_DIR/.livesync"

if [ ! -f "$SETTINGS_FILE" ]; then
    echo "[seed] generating settings -> $SETTINGS_FILE"
    livesync-cli init-settings --force "$SETTINGS_FILE" >&2
fi

# Seed the CouchDB connection only if no remote is configured yet. Re-seeding on
# every boot would re-trigger the CLI's sls+ migration and churn the settings.
# Fail hard if the settings file can't be inspected — otherwise an empty result
# would silently skip seeding and the server would start without a remote.
if ! NEEDS_SEED=$(SETTINGS_FILE="$SETTINGS_FILE" node -e '
const fs=require("fs");const d=JSON.parse(fs.readFileSync(process.env.SETTINGS_FILE,"utf8"));
const hasRemote = !!d.couchDB_URI || (d.remoteConfigurations && Object.keys(d.remoteConfigurations).length>0);
process.stdout.write(hasRemote ? "0" : "1");
'); then
    echo "[seed] failed to inspect $SETTINGS_FILE" >&2
    exit 1
fi
case "$NEEDS_SEED" in
    0 | 1) ;;
    *)
        echo "[seed] unexpected settings inspection result: '$NEEDS_SEED'" >&2
        exit 1
        ;;
esac

if [ "$NEEDS_SEED" = "1" ]; then
    echo "[seed] seeding CouchDB connection"
    SETTINGS_FILE="$SETTINGS_FILE" \
    COUCHDB_URI="${COUCHDB_URI:-}" \
    COUCHDB_USER="${COUCHDB_USER:-}" \
    COUCHDB_PASSWORD="${COUCHDB_PASSWORD:-}" \
    COUCHDB_DBNAME="${COUCHDB_DBNAME:-}" \
    COUCHDB_PASSPHRASE="${COUCHDB_PASSPHRASE:-}" \
    USE_PATH_OBFUSCATION="${USE_PATH_OBFUSCATION:-}" \
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
// Must match the value used when the vault was created. Mismatched against an
// obfuscated vault, the daemon syncs but writes 0-byte files (cannot resolve
// content chunks). Default false preserves the init-settings default.
data.usePathObfuscation = process.env.USE_PATH_OBFUSCATION === "true";
data.isConfigured = true;
fs.writeFileSync(p, JSON.stringify(data, null, 2), "utf-8");
console.error(`[seed] configured ${data.couchDB_URI} db=${data.couchDB_DBNAME}`);
NODE
fi
