# livesync-mcp

A standalone **MCP HTTP server** (Go) that lets AI agents read and write an
Obsidian vault synced by [Self-hosted LiveSync](https://github.com/vrtmrz/obsidian-livesync).
It exposes the note CRUD surface (list, read, write, append, delete, move,
search, metadata) over MCP Streamable HTTP.

## Architecture

```
AI agent ──HTTP (MCP Streamable)──▶ livesync-mcp (Go) ──fs──▶ ./vault/*.md
                                                                   ▲
                                                                   │ chokidar watch +
                                                                   │ bidirectional sync
                                  livesync-cli daemon (Node) ──────┴──▶ remote CouchDB
```

The Go process supervises a `livesync-cli <db> daemon --vault <vault>`
subprocess (Node), which owns all chunking, end-to-end encryption, and conflict
handling. The Go MCP server only ever touches plain `.md` files under the vault;
writes propagate to CouchDB via the daemon's filesystem watcher — exactly as if
a human edited the note in Obsidian.

The data boundary is the **filesystem**, which is why the MCP server can be Go:
it never touches LiveSync's TypeScript. Trade-off: notes are eventually
consistent (~1s sync latency), not read straight from the live DB.

## Prerequisites

- A built `livesync-cli` (Node) — not published to npm; build it from the
  upstream repo or use the bundled Docker image (see below).
- A reachable CouchDB configured as a LiveSync remote.
- A database directory whose `.livesync/settings.json` holds the CouchDB
  connection (the Docker entrypoint seeds this for you).

## Configuration

All configuration is via environment variables:

| Variable         | Required | Default            | Purpose |
|------------------|----------|--------------------|---------|
| `LIVESYNC_VAULT` | yes      | —                  | Vault directory of `.md` files |
| `LIVESYNC_DB`    | yes      | —                  | livesync-cli database directory |
| `LIVESYNC_CLI`   | no       | `livesync-cli`     | Path to the CLI launcher |
| `MCP_ADDR`       | no       | `127.0.0.1:8765`   | HTTP listen address |
| `MCP_API_KEY`    | no       | _(empty)_          | Bearer token; empty disables auth |

## Running

### Docker Compose (CouchDB + CLI + MCP, all-in-one)

```bash
docker compose up --build
curl -s -H "Authorization: Bearer changeme" http://localhost:8765/healthz
```

This builds `livesync-cli` from source inside the image, stands up CouchDB,
seeds the sync database, and starts the MCP server on `:8765`.

### Locally (you supply the CLI + a configured db dir)

```bash
LIVESYNC_VAULT=/path/to/vault \
LIVESYNC_DB=/path/to/db \
LIVESYNC_CLI=/path/to/livesync-cli \
MCP_API_KEY=changeme \
  go run ./cmd/livesync-mcp
```

## Endpoints

- `POST /mcp` — MCP Streamable HTTP. Requires `Authorization: Bearer <MCP_API_KEY>`
  when a key is set.
- `GET /healthz` — `200` when the sync daemon is running, `503` when it is down.

## MCP tools

| Tool | Behaviour |
|------|-----------|
| `list_notes(folder?, recursive?)` | List notes (+ size/mtime) under a folder |
| `read_note(path)` | Return note content |
| `write_note(path, content, overwrite?)` | Create or update a note |
| `append_to_note(path, content)` | Append to a note |
| `delete_note(path)` | Delete a note (propagates to CouchDB) |
| `move_note(from, to)` | Rename / move a note |
| `search_notes(query, mode)` | Search by `filename` or `content` |
| `get_note_metadata(path)` | Size, modification time |

All note paths are **vault-relative and forward-slashed** (e.g.
`Daily/2026-06-15.md`); absolute paths, `..` traversal, and symlinks escaping
the vault are rejected.

### Deletion semantics

`delete_note` removes the file from the vault, and the daemon propagates the
deletion to CouchDB. Verified end-to-end: LiveSync **soft-deletes** — the
CouchDB document is not removed via CouchDB's native `_deleted`; instead its body
gains `"deleted": true` (with a bumped `_rev`), which is the tombstone other
LiveSync clients use to remove the note locally. So a deleted note still appears
in `_all_docs`, but its body carries the deletion marker.

## Example MCP client config

```json
{
  "mcpServers": {
    "livesync": {
      "type": "http",
      "url": "http://localhost:8765/mcp",
      "headers": { "Authorization": "Bearer changeme" }
    }
  }
}
```

## Testing

```bash
go test ./...        # unit tests (no external deps)
go vet ./...
```

End-to-end tests against a real CouchDB are gated behind a build tag — see
[`test/README.md`](test/README.md).
