//go:build integration

package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/daemon"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

// requireIT skips a test unless the integration env is configured.
func requireIT(t *testing.T) {
	t.Helper()
	if os.Getenv("LIVESYNC_IT") != "1" {
		t.Skip("set LIVESYNC_IT=1 with a configured CLI + CouchDB to run")
	}
}

// seedDB writes a configured .livesync/settings.json into dbDir so a daemon can
// be started against it, pointed at the CouchDB from the COUCHDB_* env. Lets a
// single test drive several independent instances against one CouchDB.
func seedDB(t *testing.T, dbDir string) {
	t.Helper()
	cli := mustEnv(t, "LIVESYNC_CLI")
	settings := filepath.Join(dbDir, ".livesync", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o750); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G204: launches the configured test CLI (LIVESYNC_CLI), not user input
	if out, err := exec.CommandContext(t.Context(), cli, "init-settings", "--force", settings).CombinedOutput(); err != nil {
		t.Fatalf("init-settings: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	uri := os.Getenv("COUCHDB_URI")
	if uri == "" {
		uri = os.Getenv("COUCHDB_URL")
	}
	m["couchDB_URI"] = uri
	m["couchDB_USER"] = os.Getenv("COUCHDB_USER")
	m["couchDB_PASSWORD"] = os.Getenv("COUCHDB_PASSWORD")
	m["couchDB_DBNAME"] = mustEnv(t, "COUCHDB_DBNAME")
	m["remoteType"] = ""
	m["isConfigured"] = true
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// startDaemon starts a polling daemon for dbDir/vaultDir and registers cleanup.
// Stop is idempotent, so callers may also Stop explicitly mid-test.
func startDaemon(t *testing.T, dbDir, vaultDir string) *daemon.Daemon {
	t.Helper()
	d := daemon.New(mustEnv(t, "LIVESYNC_CLI"), dbDir, vaultDir, 5)
	if err := d.Start(t.Context()); err != nil {
		t.Fatalf("daemon start: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Stop(); err != nil {
			t.Logf("daemon stop: %v", err)
		}
	})
	return d
}

// waitForLocalNote polls the vault until the note is readable or timeout.
func waitForLocalNote(t *testing.T, v *vault.Vault, name string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got, err := v.Read(name); err == nil {
			return got, true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", false
}

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
	requireIT(t)
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

// TestExistingRemoteDataIsPreserved is the data-safety guarantee: pointing a
// BRAND-NEW deployment (empty vault, fresh db) at a CouchDB that already holds a
// populated vault must pull that data down — never treat the empty local vault
// as authoritative and wipe the remote. This is the scenario most likely to
// destroy a user's notes, so it is asserted explicitly.
func TestExistingRemoteDataIsPreserved(t *testing.T) {
	requireIT(t)
	couch := newCouch(t)

	// Instance A — create real "user data" and sync it to CouchDB.
	dbA, vaultA := t.TempDir(), t.TempDir()
	seedDB(t, dbA)
	dA := startDaemon(t, dbA, vaultA)
	time.Sleep(3 * time.Second)

	vA, err := vault.New(vaultA)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("preserve-%d.md", time.Now().UnixNano())
	const body = "irreplaceable user content"
	if err := vA.Write(name, body, true); err != nil {
		t.Fatal(err)
	}
	if !couch.waitForState(t, name, stateLive, 20*time.Second) {
		t.Fatalf("setup: note %q never reached CouchDB", name)
	}
	revBefore, _, _ := couch.getDoc(t, name)
	if err := dA.Stop(); err != nil {
		t.Fatalf("stop instance A: %v", err)
	}

	// Instance B — a fresh deployment (empty vault, new db) against the SAME
	// CouchDB. Must pull the existing note, not destroy it.
	dbB, vaultB := t.TempDir(), t.TempDir()
	seedDB(t, dbB)
	startDaemon(t, dbB, vaultB)

	vB, err := vault.New(vaultB)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := waitForLocalNote(t, vB, name, 25*time.Second)
	if !ok {
		t.Fatalf("existing note %q was NOT pulled into the fresh empty vault", name)
	}
	if got != body {
		t.Fatalf("pulled content = %q, want %q", got, body)
	}

	// The remote copy must be untouched: still live, same revision (no tombstone,
	// no rewrite by the fresh instance).
	revAfter, deleted, found := couch.getDoc(t, name)
	if !found || deleted {
		t.Fatalf("remote note destroyed by fresh instance (found=%v deleted=%v)", found, deleted)
	}
	if revAfter != revBefore {
		t.Fatalf("remote rev changed (%s -> %s): fresh instance rewrote existing data", revBefore, revAfter)
	}
}

// TestRestartPreservesData verifies that stopping and restarting an instance
// against its own db + vault keeps the data both locally and on the remote.
func TestRestartPreservesData(t *testing.T) {
	requireIT(t)
	couch := newCouch(t)
	db, vaultDir := t.TempDir(), t.TempDir()
	seedDB(t, db)

	d := startDaemon(t, db, vaultDir)
	time.Sleep(3 * time.Second)
	v, err := vault.New(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("durable-%d.md", time.Now().UnixNano())
	if err := v.Write(name, "survives restart", true); err != nil {
		t.Fatal(err)
	}
	if !couch.waitForState(t, name, stateLive, 20*time.Second) {
		t.Fatalf("setup: note %q never synced", name)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Restart against the same db + vault.
	startDaemon(t, db, vaultDir)
	time.Sleep(3 * time.Second)
	if _, err := v.Read(name); err != nil {
		t.Fatalf("note missing locally after restart: %v", err)
	}
	if st := couch.state(t, name); st != stateLive {
		t.Fatalf("note state in couch after restart = %s, want live", st)
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

// getDoc fetches the LiveSync document at notePath, returning its revision,
// soft-deleted flag (body "deleted": true), and whether it exists at all.
func (c *couchClient) getDoc(t *testing.T, notePath string) (rev string, deleted, found bool) {
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
		return "", false, false
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Logf("couch read error: %v", err)
		return "", false, false
	}
	var doc struct {
		Rev     string `json:"_rev"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Logf("couch decode error: %v (body=%s)", err, string(body))
		return "", false, false
	}
	return doc.Rev, doc.Deleted, true
}

// state reports whether the note is absent, live, or soft-deleted.
func (c *couchClient) state(t *testing.T, notePath string) docState {
	t.Helper()
	_, deleted, found := c.getDoc(t, notePath)
	switch {
	case !found:
		return stateAbsent
	case deleted:
		return stateDeleted
	default:
		return stateLive
	}
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
