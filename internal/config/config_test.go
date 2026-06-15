package config

import (
	"testing"
)

func TestLoadDefaultsAndRequired(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("MCP_API_KEY", "secret")
	// MCP_ADDR and LIVESYNC_CLI unset → defaults apply.

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Addr != "127.0.0.1:8765" {
		t.Errorf("Addr = %q, want default", c.Addr)
	}
	if c.CLIPath != "livesync-cli" {
		t.Errorf("CLIPath = %q, want default", c.CLIPath)
	}
	if c.Interval != 0 {
		t.Errorf("Interval = %d, want default 0", c.Interval)
	}
	if c.VaultDir != "/tmp/vault" || c.DBDir != "/tmp/db" || c.APIKey != "secret" {
		t.Errorf("unexpected config: %+v", c)
	}
}

func TestLoadInterval(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("LIVESYNC_INTERVAL", "5")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.Interval != 5 {
		t.Errorf("Interval = %d, want 5", c.Interval)
	}
}

func TestLoadInvalidIntervalErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("LIVESYNC_INTERVAL", "notanumber")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-numeric LIVESYNC_INTERVAL")
	}
}

func TestLoadMissingVaultErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LIVESYNC_VAULT is unset")
	}
}
