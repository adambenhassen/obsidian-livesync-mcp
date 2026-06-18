# syntax=docker/dockerfile:1
#
# livesync-mcp — Docker image
#
# Bundles two runtimes in one image:
#   1. livesync-cli (Node)  — built from the upstream obsidian-livesync repo.
#      It owns chunking, E2EE, and CouchDB <-> filesystem sync.
#   2. livesync-mcp (Go)    — the MCP HTTP server, which supervises the CLI
#      daemon and does plain-filesystem CRUD on the vault.
#
# The upstream npm build runs untrusted lifecycle scripts; doing it here keeps
# it isolated inside the build container.

# Pin upstream for reproducible builds (override with --build-arg).
ARG OLS_REPO=https://github.com/vrtmrz/obsidian-livesync.git
ARG OLS_REF=1a1f816872d82c288776ca3cc37f7a4f238dacea

# ─────────────────────────────────────────────────────────────────────────────
#  Stage 1 — build livesync-cli from upstream source
# ─────────────────────────────────────────────────────────────────────────────
FROM node:22-slim AS cli-builder
ARG OLS_REPO
ARG OLS_REF
RUN apt-get update \
    && apt-get install -y --no-install-recommends git python3 make g++ ca-certificates \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
# --no-checkout + checkout -f: a plain clone normalizes line endings per the
# repo's .gitattributes, leaving files (e.g. src/apps/cli/README.md) "modified"
# in the work tree, which makes a subsequent `git checkout <ref>` abort. Skip
# the initial checkout and force-populate the work tree at the pinned ref.
RUN git clone --no-checkout "$OLS_REPO" . \
    && git checkout -f "$OLS_REF" \
    && git submodule update --init --recursive
RUN npm install
RUN cd src/apps/cli && npm run build

# Compile the runtime-only (Vite-external) deps against the runtime base image.
WORKDIR /deps
RUN cp /src/src/apps/cli/runtime-package.json package.json \
    && npm install --omit=dev

# ─────────────────────────────────────────────────────────────────────────────
#  Stage 2 — build the Go MCP server
# ─────────────────────────────────────────────────────────────────────────────
FROM golang:1.26 AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /livesync-mcp ./cmd/livesync-mcp

# ─────────────────────────────────────────────────────────────────────────────
#  Stage 3 — e2e test runner (Go toolchain + Node CLI + source)
#  Runs the gated integration test (test/integration_test.go) against a real
#  CouchDB. Build with `--target e2e`; driven by the compose `e2e-test` service.
# ─────────────────────────────────────────────────────────────────────────────
FROM node:22-slim AS e2e
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=golang:1.26 /usr/local/go /usr/local/go
# Reuse the module cache already populated by go-builder (offline; GOPROXY=off).
COPY --from=go-builder /go/pkg/mod /go/pkg/mod
ENV PATH=/usr/local/go/bin:/usr/local/bin:$PATH \
    GOPATH=/go \
    GOPROXY=off \
    GOFLAGS=-mod=mod \
    GOCACHE=/tmp/gocache \
    LIVESYNC_CLI=livesync-cli
WORKDIR /app
# livesync-cli bundle + runtime deps + launcher
COPY --from=cli-builder /deps/node_modules ./node_modules
COPY --from=cli-builder /src/src/apps/cli/dist ./dist
COPY deploy/livesync-cli /usr/local/bin/livesync-cli
COPY deploy/seed-settings.sh /usr/local/bin/seed-settings.sh
COPY deploy/run-e2e.sh /usr/local/bin/run-e2e.sh
RUN chmod +x /usr/local/bin/livesync-cli /usr/local/bin/seed-settings.sh /usr/local/bin/run-e2e.sh
# Go module + source
COPY go.mod go.sum ./
COPY . .
ENTRYPOINT ["/usr/local/bin/run-e2e.sh"]

# ─────────────────────────────────────────────────────────────────────────────
#  Stage 4 — runtime (LAST stage = the default `docker build` target, so a bare
#  build produces the server image, not the test runner). Compose also pins
#  `target: runtime` for clarity.
# ─────────────────────────────────────────────────────────────────────────────
FROM node:22-slim AS runtime
WORKDIR /app

# livesync-cli bundle + its runtime node_modules
COPY --from=cli-builder /deps/node_modules ./node_modules
COPY --from=cli-builder /src/src/apps/cli/dist ./dist

# Thin wrapper: `livesync-cli <db> daemon --vault <vault>` maps straight to the
# bundle (no database-path injection — the Go supervisor passes it explicitly).
COPY deploy/livesync-cli /usr/local/bin/livesync-cli
COPY --from=go-builder /livesync-mcp /usr/local/bin/livesync-mcp
COPY deploy/seed-settings.sh /usr/local/bin/seed-settings.sh
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/livesync-cli /usr/local/bin/seed-settings.sh /usr/local/bin/entrypoint.sh

ENV LIVESYNC_CLI=livesync-cli \
    LIVESYNC_VAULT=/vault \
    LIVESYNC_DB=/db \
    LIVESYNC_INTERVAL=5 \
    MCP_ADDR=0.0.0.0:8765
EXPOSE 8765
VOLUME ["/vault", "/db"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
