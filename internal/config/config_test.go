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

func TestLoadPathObfuscation(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("COUCHDB_PASSPHRASE", "hunter2")
	t.Setenv("USE_PATH_OBFUSCATION", "true")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.CouchPassphrase != "hunter2" {
		t.Errorf("CouchPassphrase = %q, want hunter2", c.CouchPassphrase)
	}
	if !c.UsePathObfuscation {
		t.Error("UsePathObfuscation = false, want true")
	}
}

func TestLoadPathObfuscationDefaultsFalse(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.UsePathObfuscation {
		t.Error("UsePathObfuscation = true, want default false")
	}
}

func TestLoadInvalidPathObfuscationErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("USE_PATH_OBFUSCATION", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-boolean USE_PATH_OBFUSCATION")
	}
}

func TestLoadHandleFilenameCaseSensitive(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("HANDLE_FILENAME_CASE_SENSITIVE", "true")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !c.HandleFilenameCaseSensitive {
		t.Error("HandleFilenameCaseSensitive = false, want true")
	}
}

func TestLoadInvalidHandleFilenameCaseSensitiveErrors(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("HANDLE_FILENAME_CASE_SENSITIVE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for non-boolean HANDLE_FILENAME_CASE_SENSITIVE")
	}
}

// Obfuscation derives the doc id from the passphrase; an empty passphrase would
// silently fall back to plaintext-path lookups and report every note as
// conflict-free. Fail fast instead.
func TestLoadObfuscationRequiresPassphrase(t *testing.T) {
	t.Setenv("LIVESYNC_VAULT", "/tmp/vault")
	t.Setenv("LIVESYNC_DB", "/tmp/db")
	t.Setenv("USE_PATH_OBFUSCATION", "true")
	t.Setenv("COUCHDB_PASSPHRASE", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for USE_PATH_OBFUSCATION=true with empty COUCHDB_PASSPHRASE")
	}
}

func TestConflictIDParams(t *testing.T) {
	tests := []struct {
		name                string
		cfg                 Config
		wantObfuscate       string
		wantCaseInsensitive bool
	}{
		{"default", Config{}, "", true},
		{"obfuscation off ignores passphrase", Config{CouchPassphrase: "p"}, "", true},
		{"obfuscation on uses passphrase", Config{UsePathObfuscation: true, CouchPassphrase: "p"}, "p", true},
		{"case-sensitive vault", Config{HandleFilenameCaseSensitive: true}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotObfuscate, gotCaseInsensitive := tt.cfg.ConflictIDParams()
			if gotObfuscate != tt.wantObfuscate || gotCaseInsensitive != tt.wantCaseInsensitive {
				t.Errorf("ConflictIDParams() = (%q, %v), want (%q, %v)",
					gotObfuscate, gotCaseInsensitive, tt.wantObfuscate, tt.wantCaseInsensitive)
			}
		})
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
