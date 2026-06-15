package config

import (
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
	Interval int    // LIVESYNC_INTERVAL, daemon poll seconds; 0 = continuous
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
		return Config{}, fmt.Errorf("LIVESYNC_VAULT is required")
	}
	if c.DBDir == "" {
		return Config{}, fmt.Errorf("LIVESYNC_DB is required")
	}
	if v := os.Getenv("LIVESYNC_INTERVAL"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("LIVESYNC_INTERVAL must be an integer: %w", err)
		}
		c.Interval = n
	}
	return c, nil
}
