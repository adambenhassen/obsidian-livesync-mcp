//go:build integration

package test

import (
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

	d := daemon.New(cli, dbDir, vaultDir, 5)
	if err := d.Start(t.Context()); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	defer func() {
		if err := d.Stop(); err != nil {
			t.Logf("daemon stop: %v", err)
		}
	}()
	time.Sleep(3 * time.Second) // let the initial mirror scan settle

	v, err := vault.New(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("mcp-it-%d.md", time.Now().UnixNano())
	if err := v.Write(name, "integration body", true); err != nil {
		t.Fatal(err)
	}

	// Write propagation: the note appears in CouchDB as a live LiveSync doc.
	if !couch.waitForState(t, name, stateLive, 20*time.Second) {
		t.Fatalf("note %q did not propagate to CouchDB within timeout", name)
	}

	// The file must still be present (daemon did not revert it).
	if _, err := v.Read(name); err != nil {
		t.Fatalf("note disappeared after sync: %v", err)
	}

	// Deletion propagation — RESOLVED design open question.
	//
	// A filesystem unlink DOES propagate: the daemon's watcher pushes a LiveSync
	// tombstone, i.e. the CouchDB doc body gains "deleted": true with a bumped
	// _rev. Note LiveSync soft-deletes — the doc is NOT removed via CouchDB's
	// native _deleted, so it still appears in _all_docs; the body's "deleted"
	// field is the correct signal.
	if err := v.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read(name); !os.IsNotExist(err) {
		t.Fatalf("note %q should be removed from the local vault, err=%v", name, err)
	}
	if !couch.waitForState(t, name, stateDeleted, 20*time.Second) {
		t.Fatalf("deletion of %q did not propagate to CouchDB (no tombstone)", name)
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

type docState int

const (
	stateAbsent docState = iota
	stateLive
	stateDeleted
)

func (s docState) String() string {
	switch s {
	case stateLive:
		return "live"
	case stateDeleted:
		return "deleted"
	default:
		return "absent"
	}
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

// state fetches the LiveSync document at the given note path and reports whether
// it is absent, live, or soft-deleted (body "deleted": true).
func (c *couchClient) state(t *testing.T, notePath string) docState {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+"/"+c.db+"/"+notePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Logf("couch query error: %v", err)
		return stateAbsent
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	if resp.StatusCode == http.StatusNotFound {
		return stateAbsent
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Logf("couch read error: %v", err)
		return stateAbsent
	}
	var doc struct {
		Deleted bool `json:"deleted"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Logf("couch decode error: %v (body=%s)", err, string(body))
		return stateAbsent
	}
	if doc.Deleted {
		return stateDeleted
	}
	return stateLive
}

// waitForState polls until the note reaches want, returning whether it did.
func (c *couchClient) waitForState(t *testing.T, notePath string, want docState, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.state(t, notePath) == want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
