package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/auth"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/config"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/daemon"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/mcpserver"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	v, err := vault.New(cfg.VaultDir)
	if err != nil {
		log.Fatalf("vault: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	d := daemon.New(cfg.CLIPath, cfg.DBDir, cfg.VaultDir)
	if err := d.Start(ctx); err != nil {
		log.Fatalf("failed to start livesync-cli daemon: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			log.Printf("error stopping livesync-cli daemon: %v", err)
		}
	}()

	srv := mcpserver.New(v)
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv }, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.Healthy() {
			http.Error(w, "sync daemon down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/mcp", auth.RequireBearer(cfg.APIKey, mcpHandler))

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = httpSrv.Shutdown(context.Background())
	}()

	log.Printf("livesync-mcp listening on %s (MCP at /mcp)", cfg.Addr)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server: %v", err)
	}
	os.Exit(0)
}
