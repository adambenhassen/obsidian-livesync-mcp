package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Config holds runtime settings sourced from environment variables.
type Config struct {
	Addr     string // MCP_ADDR, default 127.0.0.1:8765
	APIKey   string // MCP_API_KEY, optional (empty disables auth)
	VaultDir string // LIVESYNC_VAULT, required
	DBDir    string // LIVESYNC_DB, required
	CLIPath  string // LIVESYNC_CLI, default "livesync-cli"
	Interval int    // LIVESYNC_INTERVAL, daemon CouchDB poll seconds; must be > 0 to sync
	ReadOnly bool   // READ_ONLY, when true only read tools are exposed

	// CouchDB connection for conflict detection (optional; empty disables it).
	// These are the same vars the daemon's settings are seeded from.
	CouchURI      string // COUCHDB_URI
	CouchUser     string // COUCHDB_USER
	CouchPassword string // COUCHDB_PASSWORD
	CouchDBName   string // COUCHDB_DBNAME
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment, applying defaults and
// validating required fields.
func Load() (Config, error) {
	c := Config{
		Addr:     env("MCP_ADDR", "127.0.0.1:8765"),
		APIKey:   os.Getenv("MCP_API_KEY"),
		VaultDir: os.Getenv("LIVESYNC_VAULT"),
		DBDir:    os.Getenv("LIVESYNC_DB"),
		CLIPath:  env("LIVESYNC_CLI", "livesync-cli"),
	}
	if c.VaultDir == "" {
		return Config{}, errors.New("LIVESYNC_VAULT is required")
	}
	if c.DBDir == "" {
		return Config{}, errors.New("LIVESYNC_DB is required")
	}
	if v := os.Getenv("LIVESYNC_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("LIVESYNC_INTERVAL must be an integer: %w", err)
		}
		c.Interval = n
	}
	if v := os.Getenv("READ_ONLY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("READ_ONLY must be a boolean: %w", err)
		}
		c.ReadOnly = b
	}
	c.CouchURI = os.Getenv("COUCHDB_URI")
	c.CouchUser = os.Getenv("COUCHDB_USER")
	c.CouchPassword = os.Getenv("COUCHDB_PASSWORD")
	c.CouchDBName = os.Getenv("COUCHDB_DBNAME")
	return c, nil
}
