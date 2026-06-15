#!/bin/sh
# Initialise a single-node CouchDB for LiveSync: create the system databases
# and the target sync database. Idempotent (PUT on an existing db is a no-op 412).
set -e

: "${COUCHDB_URL:=http://couchdb:5984}"
: "${COUCHDB_USER:=admin}"
: "${COUCHDB_PASSWORD:=password}"
: "${COUCHDB_DBNAME:=livesync}"

AUTH="-u ${COUCHDB_USER}:${COUCHDB_PASSWORD}"

echo "[couch-init] waiting for CouchDB at ${COUCHDB_URL} ..."
until curl -fsS $AUTH "${COUCHDB_URL}/_up" >/dev/null 2>&1; do
    sleep 1
done

for db in _users _replicator _global_changes "${COUCHDB_DBNAME}"; do
    code=$(curl -s -o /dev/null -w '%{http_code}' $AUTH -X PUT "${COUCHDB_URL}/${db}")
    case "$code" in
        201|202) echo "[couch-init] created ${db}" ;;
        412)     echo "[couch-init] ${db} already exists" ;;
        *)       echo "[couch-init] PUT ${db} -> HTTP ${code}" ;;
    esac
done

# LiveSync benefits from higher document/request size limits. These are best
# effort (a managed CouchDB may forbid _config writes), but report failures
# instead of masking them with `|| true` so a misconfigured node is visible.
tune() {
    name="$1"
    code=$(curl -s -o /dev/null -w '%{http_code}' $AUTH -X PUT "${COUCHDB_URL}/_node/_local/_config/$name" -d "$2")
    case "$code" in
        200) ;;
        *) echo "[couch-init] WARNING: could not set $name (HTTP $code)" >&2 ;;
    esac
}
tune "couchdb/max_document_size" '"50000000"'
tune "chttpd/max_http_request_size" '"4294967296"'

echo "[couch-init] done"
