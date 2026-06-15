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

# LiveSync needs higher revs_limit tolerance and bulk limits; safe defaults.
curl -s $AUTH -X PUT "${COUCHDB_URL}/_node/_local/_config/couchdb/max_document_size" -d '"50000000"' >/dev/null || true
curl -s $AUTH -X PUT "${COUCHDB_URL}/_node/_local/_config/chttpd/max_http_request_size" -d '"4294967296"' >/dev/null || true

echo "[couch-init] done"
