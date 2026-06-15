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
	if c.ReadOnly {
		t.Errorf("ReadOnly = true, want default false")
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

func TestLoadCouchDBConnection(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("COUCHDB_URI", "http://couch:5984")
	t.Setenv("COUCHDB_USER", "admin")
	t.Setenv("COUCHDB_PASSWORD", "secret")
	t.Setenv("COUCHDB_DBNAME", "livesync")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.CouchURI != "http://couch:5984" || c.CouchUser != "admin" ||
		c.CouchPassword != "secret" || c.CouchDBName != "livesync" {
		t.Errorf("unexpected CouchDB config: %+v", c)
	}
}

func TestLoadReadOnly(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("READ_ONLY", "true")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !c.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

func TestLoadInvalidReadOnlyErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("READ_ONLY", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-boolean READ_ONLY")
	}
}

func TestLoadMissingVaultErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LIVESYNC_VAULT is unset")
	}
}
