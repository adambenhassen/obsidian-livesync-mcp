# Integration test

`integration_test.go` proves the end-to-end claim: a note written to the vault
propagates through the supervised `livesync-cli` daemon to the remote CouchDB,
and that **deletion** also propagates. It is gated behind the `integration`
build tag and the `LIVESYNC_IT=1` env var, so it never runs during `go test ./...`.

## Easiest path — Docker Compose

The repo ships a full stack (`docker-compose.yml`): CouchDB, a one-shot
initialiser, and the `livesync-mcp` image (which bundles a from-source
`livesync-cli`).

```bash
# Bring up CouchDB + initialise the databases.
docker compose up -d couchdb couchdb-init

# Run the integration test against that CouchDB, using the livesync-cli built
# into the image. (Builds the image on first run.)
docker compose run --rm --entrypoint "" \
  -e LIVESYNC_IT=1 \
  -e LIVESYNC_CLI=livesync-cli \
  -e LIVESYNC_DB=/db \
  -e COUCHDB_URL=http://couchdb:5984 \
  -e COUCHDB_USER=admin -e COUCHDB_PASSWORD=password -e COUCHDB_DBNAME=livesync \
  livesync-mcp sh -c '/usr/local/bin/entrypoint.sh & sleep 4; \
    cd /src && go test -tags integration ./test/ -run Roundtrip -v'
```

> The `entrypoint.sh &` seeds `/db/.livesync/settings.json` before the test
> starts its own daemon against the same configured db dir.

Tear down with `docker compose down -v`.

## Manual path — your own CLI + CouchDB

1. Build `livesync-cli` from the [obsidian-livesync](https://github.com/vrtmrz/obsidian-livesync)
   repo (`git submodule update --init --recursive`, `npm install`, then
   `cd src/apps/cli && npm run build`; the launcher is `node dist/index.cjs`).
2. Create a database dir whose `.livesync/settings.json` is configured for your
   CouchDB (`livesync-cli init-settings`, then fill in `couchDB_URI`,
   `couchDB_USER`, `couchDB_PASSWORD`, `couchDB_DBNAME`, `isConfigured: true`).
3. Run:

```bash
LIVESYNC_IT=1 \
LIVESYNC_CLI=/path/to/livesync-cli \
LIVESYNC_DB=/path/to/db \
COUCHDB_URL=http://localhost:5984 \
COUCHDB_USER=admin COUCHDB_PASSWORD=password COUCHDB_DBNAME=livesync \
  go test -tags integration ./test/ -run Roundtrip -v
```

Expected: PASS. Without `LIVESYNC_IT=1`, the test SKIPs.

## Notes

- The remote-doc assertions assume E2EE is **off** (compose default), so the
  note path appears verbatim in CouchDB document ids. With a passphrase set,
  ids are obfuscated and the substring check won't match.
- The CLI's leveldb-backed db dir is **single-process**: never run another CLI
  command against `LIVESYNC_DB` while the daemon is running.
