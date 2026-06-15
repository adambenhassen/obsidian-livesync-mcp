#!/bin/sh
# Seed an isolated db dir, then run the gated Go integration test against the
# CouchDB reachable at COUCHDB_URL. Used by the compose `e2e-test` service.
set -e

/usr/local/bin/seed-settings.sh

echo "[e2e] running integration test"
exec go test -tags integration ./test/... -run Roundtrip -v -count=1
