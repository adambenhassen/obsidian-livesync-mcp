package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/auth"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/config"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/couch"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/daemon"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/mcpserver"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// newHandler builds the HTTP handler: the MCP endpoint (read-only or full,
// per cfg.ReadOnly, behind bearer auth) plus an unauthenticated /healthz that
// reports the sync daemon's liveness via healthy.
func newHandler(v *vault.Vault, cfg config.Config, healthy func() bool) http.Handler {
	// Conflict detection is optional: nil checker when CouchDB isn't configured.
	var checker mcpserver.ConflictChecker
	if cc := couch.New(cfg.CouchURI, cfg.CouchUser, cfg.CouchPassword, cfg.CouchDBName); cc != nil {
		checker = cc
	}
	srv := mcpserver.New(v, cfg.ReadOnly, checker)
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !healthy() {
			http.Error(w, "sync daemon down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/mcp", auth.RequireBearer(cfg.APIKey, mcpHandler))
	return mux
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	v, err := vault.New(cfg.VaultDir)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d := daemon.New(cfg.CLIPath, cfg.DBDir, cfg.VaultDir, cfg.Interval)
	if err := d.Start(ctx); err != nil {
		return err
	}
	defer func() {
		if err := d.Stop(); err != nil {
			log.Printf("error stopping livesync-cli daemon: %v", err)
		}
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newHandler(v, cfg, d.Healthy),
		ReadHeaderTimeout: 10 * time.Second,
	}
	// If the sync daemon dies on its own, stop serving and exit non-zero so the
	// supervisor (Docker restart policy / systemd) restarts the whole process
	// rather than silently serving an MCP API that no longer syncs to CouchDB.
	daemonDied := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done(): // graceful shutdown (signal)
		case <-d.Done(): // daemon exited on its own
			close(daemonDied)
			stop()
		}
	}()

	go func() {
		<-ctx.Done()
		if err := httpSrv.Shutdown(context.Background()); err != nil {
			log.Printf("error shutting down http server: %v", err)
		}
	}()

	log.Printf("livesync-mcp listening on %s (MCP at /mcp)", cfg.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	select {
	case <-daemonDied:
		return errors.New("livesync-cli daemon exited; restart to resume syncing")
	default:
		return nil
	}
}
