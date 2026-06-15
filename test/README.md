# Integration tests

`integration_test.go` proves the end-to-end claims against a real CouchDB. Gated
behind the `integration` build tag and `LIVESYNC_IT=1`, so they never run during
`go test ./...`.

- **`TestWriteNoteRoundtripToCouchDB`** — a written note propagates to CouchDB,
  and a delete propagates as a soft-delete tombstone.
- **`TestExistingRemoteDataIsPreserved`** — the data-safety guarantee: a fresh
  instance (empty vault, new db) pointed at a CouchDB that already holds a vault
  **pulls the data down** and leaves the remote untouched (same revision, no
  tombstone). This is the scenario most likely to destroy a user's notes.
- **`TestRestartPreservesData`** — stopping and restarting an instance keeps the
  data locally and remotely.

## Easiest path — Docker Compose

The repo ships a full stack (`docker-compose.yml`): CouchDB, a one-shot
initialiser, and a dedicated `e2e-test` service (under the `test` profile) that
bundles the Go toolchain + a from-source `livesync-cli` and runs this test.

```bash
docker compose --profile test run --rm e2e-test
```

That one command brings up CouchDB, initialises the databases, seeds an isolated
db dir, then runs the whole gated suite (`go test -tags integration ./test/... -v`)
against the real CouchDB. Expected: `PASS`.

The `e2e-test` service uses its own db volume, so it never contends with a
running `livesync-mcp` for the single-process database.

Tear down with `docker compose --profile test down -v`.

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
  go test -tags integration ./test/... -v
```

(To run a single test, add `-run TestExistingRemoteDataIsPreserved`.)

Expected: PASS. Without `LIVESYNC_IT=1`, the test SKIPs.

## Notes

- The remote-doc assertions assume E2EE is **off** (compose default), so the
  note path appears verbatim in CouchDB document ids. With a passphrase set,
  ids are obfuscated and the substring check won't match.
- The CLI's leveldb-backed db dir is **single-process**: never run another CLI
  command against `LIVESYNC_DB` while the daemon is running.
