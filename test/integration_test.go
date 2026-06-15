//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/daemon"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

// TestWriteNoteRoundtripToCouchDB proves the end-to-end claim: a note written
// to the vault propagates through the supervised livesync-cli daemon to the
// remote CouchDB, and that deletion also propagates (resolving the design's
// open question).
//
// Requires a configured CLI + CouchDB. Run via docker compose (see
// test/README.md) or point the env vars at your own infra:
//
//	LIVESYNC_IT=1
//	LIVESYNC_CLI   path to the livesync-cli launcher
//	LIVESYNC_DB    a database dir whose .livesync/settings.json is configured
//	COUCHDB_URL    e.g. http://localhost:5984
//	COUCHDB_USER, COUCHDB_PASSWORD, COUCHDB_DBNAME
//
// The remote-doc assertions assume E2EE is OFF (the compose default), so the
// note path appears verbatim in CouchDB document ids.
func TestWriteNoteRoundtripToCouchDB(t *testing.T) {
	if os.Getenv("LIVESYNC_IT") != "1" {
		t.Skip("set LIVESYNC_IT=1 with a configured CLI + CouchDB to run")
	}
	cli := mustEnv(t, "LIVESYNC_CLI")
	dbDir := mustEnv(t, "LIVESYNC_DB")
	couch := newCouch(t)
	vaultDir := t.TempDir()

	d := daemon.New(cli, dbDir, vaultDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	defer d.Stop()
	time.Sleep(3 * time.Second) // let the initial mirror scan settle

	v, err := vault.New(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("mcp-it-%d.md", time.Now().UnixNano())
	if err := v.Write(name, "integration body", true); err != nil {
		t.Fatal(err)
	}

	// Propagation: the note's path should appear among CouchDB document ids.
	if !couch.waitForDoc(t, name, true, 20*time.Second) {
		t.Fatalf("note %q did not propagate to CouchDB within timeout", name)
	}

	// The file must still be present (daemon did not revert it).
	if _, err := v.Read(name); err != nil {
		t.Fatalf("note disappeared after sync: %v", err)
	}

	// Deletion propagation (resolves the design open question).
	if err := v.Delete(name); err != nil {
		t.Fatal(err)
	}
	if !couch.waitForDoc(t, name, false, 20*time.Second) {
		t.Fatalf("note %q still live in CouchDB after delete (deletion not "+
			"propagated by daemon); route delete_note through `livesync-cli rm`", name)
	}
}

func mustEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("%s is required for the integration test", key)
	}
	return v
}

type couchClient struct {
	base, user, pass, db string
	hc                   *http.Client
}

func newCouch(t *testing.T) *couchClient {
	t.Helper()
	url := os.Getenv("COUCHDB_URL")
	if url == "" {
		url = "http://localhost:5984"
	}
	return &couchClient{
		base: strings.TrimRight(url, "/"),
		user: os.Getenv("COUCHDB_USER"),
		pass: os.Getenv("COUCHDB_PASSWORD"),
		db:   mustEnv(t, "COUCHDB_DBNAME"),
		hc:   &http.Client{Timeout: 5 * time.Second},
	}
}

// liveDocWithID reports whether a non-deleted document whose id contains sub
// currently exists in the database.
func (c *couchClient) liveDocWithID(t *testing.T, sub string) bool {
	t.Helper()
	req, err := http.NewRequest("GET", c.base+"/"+c.db+"/_all_docs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Logf("couch query error: %v", err)
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out struct {
		Rows []struct {
			ID    string `json:"id"`
			Value struct {
				Deleted bool `json:"deleted"`
			} `json:"value"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Logf("couch decode error: %v (body=%s)", err, string(body))
		return false
	}
	for _, r := range out.Rows {
		if strings.Contains(r.ID, sub) && !r.Value.Deleted {
			return true
		}
	}
	return false
}

// waitForDoc polls until a live doc matching sub is present (want=true) or
// absent (want=false), returning whether the desired state was reached.
func (c *couchClient) waitForDoc(t *testing.T, sub string, want bool, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.liveDocWithID(t, sub) == want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
