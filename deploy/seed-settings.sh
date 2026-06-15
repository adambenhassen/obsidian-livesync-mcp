#!/bin/sh
# Seed the livesync-cli settings (CouchDB connection + E2EE) into the database
# dir, idempotently. Shared by the runtime entrypoint and the e2e test runner.
#
# Reads: LIVESYNC_DB, COUCHDB_URI, COUCHDB_USER, COUCHDB_PASSWORD,
#        COUCHDB_DBNAME, COUCHDB_PASSPHRASE / COUCHDB_PASSPHRASE_B64 (optional
#        E2EE), USE_PATH_OBFUSCATION (must match the vault's setting).
set -e

DB_DIR="${LIVESYNC_DB:-/db}"
SETTINGS_FILE="$DB_DIR/.livesync/settings.json"
mkdir -p "$DB_DIR/.livesync"

# Validate COUCHDB_PASSPHRASE_B64 up front (fail fast). The actual decode happens
# in the E2EE block below, in Node, so it matches internal/config/config.go's
# byte-exact base64.StdEncoding semantics: a shell "$(... | base64 -d)" capture
# would strip trailing newlines, and GNU base64 -d silently accepts non-canonical
# input (whitespace, missing padding) that StdEncoding rejects — either way the
# daemon and the Go server could derive different passphrases. The canonical
# round-trip check below accepts exactly the inputs StdEncoding accepts.
if [ -n "${COUCHDB_PASSPHRASE_B64:-}" ]; then
    if ! COUCHDB_PASSPHRASE_B64="$COUCHDB_PASSPHRASE_B64" node -e '
const b64 = process.env.COUCHDB_PASSPHRASE_B64;
if (Buffer.from(b64, "base64").toString("base64") !== b64) process.exit(1);
'; then
        echo "[seed] COUCHDB_PASSPHRASE_B64 must be valid standard (padded) base64" >&2
        exit 1
    fi
fi

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
    node <<'NODE'
const fs = require("node:fs");
const p = process.env.SETTINGS_FILE;
const data = JSON.parse(fs.readFileSync(p, "utf-8"));
data.couchDB_URI = process.env.COUCHDB_URI;
data.couchDB_USER = process.env.COUCHDB_USER;
data.couchDB_PASSWORD = process.env.COUCHDB_PASSWORD;
data.couchDB_DBNAME = process.env.COUCHDB_DBNAME;
data.remoteType = "";
data.isConfigured = true;
fs.writeFileSync(p, JSON.stringify(data, null, 2), "utf-8");
console.error(`[seed] configured ${data.couchDB_URI} db=${data.couchDB_DBNAME}`);
NODE
fi

# Re-assert the E2EE config on EVERY boot (idempotently), regardless of the
# connection-seed gate above. LiveSync's sls+ startup migration drops the
# top-level `passphrase` from settings.json after the first run, so seeding it
# only once would leave every restart with no passphrase — and the daemon would
# then write each note as a 0-byte file. The passphrase is decoded here (not in
# the shell) so trailing bytes survive and the result matches config.go. Must run
# after the connection seed and before the daemon starts.
SETTINGS_FILE="$SETTINGS_FILE" \
COUCHDB_PASSPHRASE="${COUCHDB_PASSPHRASE:-}" \
COUCHDB_PASSPHRASE_B64="${COUCHDB_PASSPHRASE_B64:-}" \
USE_PATH_OBFUSCATION="${USE_PATH_OBFUSCATION:-}" \
node <<'NODE'
const fs = require("node:fs");
const p = process.env.SETTINGS_FILE;
const data = JSON.parse(fs.readFileSync(p, "utf-8"));
// COUCHDB_PASSPHRASE_B64 (standard padded base64) overrides COUCHDB_PASSPHRASE,
// decoded byte-for-byte to mirror internal/config/config.go.
const b64 = process.env.COUCHDB_PASSPHRASE_B64 || "";
const pass = b64 !== ""
  ? Buffer.from(b64, "base64").toString("utf8")
  : (process.env.COUCHDB_PASSPHRASE || "");
const obfuscate = process.env.USE_PATH_OBFUSCATION === "true";
// Mirror config.go's fail-fast: obfuscation derives the document id from the
// passphrase, so an empty one would silently produce 0-byte files / false
// conflict all-clears on an obfuscated vault.
if (obfuscate && pass === "") {
  console.error("[seed] USE_PATH_OBFUSCATION=true requires COUCHDB_PASSPHRASE or COUCHDB_PASSPHRASE_B64");
  process.exit(1);
}
data.encrypt = pass !== "";
data.passphrase = pass;
// Must match the value used when the vault was created. Mismatched against an
// obfuscated vault, the daemon syncs but writes 0-byte files (cannot resolve
// content chunks). Default false preserves the init-settings default.
data.usePathObfuscation = obfuscate;
fs.writeFileSync(p, JSON.stringify(data, null, 2), "utf-8");
console.error(`[seed] e2ee re-asserted (encrypt=${data.encrypt} usePathObfuscation=${data.usePathObfuscation})`);
NODE
