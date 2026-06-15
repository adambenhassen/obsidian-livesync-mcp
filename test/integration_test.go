//go:build integration

package test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/couch"
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
// single test drive several independent instances against one CouchDB. Optional
// opts mutate the settings map before it is written (e.g. to enable path
// obfuscation).
func seedDB(t *testing.T, dbDir string, opts ...func(map[string]any)) {
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
	for _, opt := range opts {
		opt(m)
	}
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

// TestPathObfuscatedVaultSyncsContent guards the USE_PATH_OBFUSCATION fix. A
// vault created with Obsidian's "Use path obfuscation" enabled previously synced
// as 0-byte files: the daemon could list paths but not resolve the content
// chunks, because usePathObfuscation was left at its init-settings default of
// false. With it seeded to match the vault, content roundtrips intact.
//
// Path obfuscation is an E2EE feature — it engages only with encryption +
// passphrase (verified empirically: usePathObfuscation alone leaves the doc
// stored under its plaintext id). So this test enables encrypt/passphrase too,
// matching deploy/seed-settings.sh, and runs against an ISOLATED database so the
// encrypted vault never mixes with the plaintext vaults the other tests leave in
// the shared db.
//
// Under obfuscation the CouchDB document id is a hash, not the plaintext path, so
// the note is not retrievable by path — that property doubles as proof obfuscation
// actually engaged. The content assertion is a two-instance filesystem roundtrip:
// write with obfuscation on, pull into a fresh obfuscation-on instance, and check
// the content survived (non-empty, equal) rather than arriving as the 0-byte file
// the bug produced.
func TestPathObfuscatedVaultSyncsContent(t *testing.T) {
	requireIT(t)
	db := fmt.Sprintf("obf-%d", time.Now().UnixNano())
	couch := newCouch(t)
	couch.db = db
	couch.createDB(t)
	t.Cleanup(func() { couch.dropDB(t) })

	obfuscate := func(m map[string]any) {
		m["couchDB_DBNAME"] = db
		m["usePathObfuscation"] = true
		m["encrypt"] = true
		m["passphrase"] = "integration-obfuscation-passphrase"
	}

	// Instance A — obfuscation on; write a note with real content.
	dbA, vaultA := t.TempDir(), t.TempDir()
	seedDB(t, dbA, obfuscate)
	dA := startDaemon(t, dbA, vaultA)
	time.Sleep(3 * time.Second) // let the initial mirror scan settle

	vA, err := vault.New(vaultA)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("obfuscated-%d.md", time.Now().UnixNano())
	const body = "content behind path obfuscation"
	before := couch.totalDocs(t)
	if err := vA.Write(name, body, true); err != nil {
		t.Fatal(err)
	}
	// The obfuscated id is unknown, so gate on CouchDB's doc count rising (the
	// note plus its content chunk(s) land as new docs).
	if !couch.waitForDocCount(t, before+1, 20*time.Second) {
		t.Fatalf("obfuscated note %q never propagated to CouchDB", name)
	}
	// Obfuscation must actually have engaged, otherwise the roundtrip below is a
	// no-op guard (plaintext content roundtrips fine too). Under path obfuscation
	// the document id is a hash of the path, so the note is NOT retrievable by its
	// plaintext path — a plaintext vault would be.
	if _, _, found := couch.getDoc(t, name); found {
		t.Fatalf("note %q stored under its plaintext id; path obfuscation did not engage", name)
	}
	if err := dA.Stop(); err != nil {
		t.Fatalf("stop instance A: %v", err)
	}

	// Instance B — fresh deployment, obfuscation on; must pull the note with its
	// content intact (the bug produced a 0-byte file here).
	dbB, vaultB := t.TempDir(), t.TempDir()
	seedDB(t, dbB, obfuscate)
	startDaemon(t, dbB, vaultB)

	vB, err := vault.New(vaultB)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := waitForLocalNote(t, vB, name, 25*time.Second)
	if !ok {
		t.Fatalf("obfuscated note %q was NOT pulled into the fresh vault", name)
	}
	if got != body {
		t.Fatalf("pulled content = %q, want %q (empty = the obfuscation bug)", got, body)
	}
}

// TestObfuscatedConflictDetection is the golden proof that conflict detection
// works under path obfuscation: the production couch client, given the vault's
// passphrase, derives the same obfuscated document id the real daemon writes and
// detects an injected conflict on it.
//
// The transitive guarantee: a conflict is injected at the id the test computes
// from LiveSync's algorithm, and that id is first confirmed to be the daemon's
// actual doc (getDocByID found). The production client is then asked for
// conflicts BY PLAINTEXT PATH; it sees the injection only if its own id
// derivation matches. So daemon-id == reference-id == production-id, end to end.
func TestObfuscatedConflictDetection(t *testing.T) {
	requireIT(t)
	db := fmt.Sprintf("obf-conflict-%d", time.Now().UnixNano())
	cc := newCouch(t)
	cc.db = db
	cc.createDB(t)
	t.Cleanup(func() { cc.dropDB(t) })

	const passphrase = "integration-obfuscation-passphrase"
	obfuscate := func(m map[string]any) {
		m["couchDB_DBNAME"] = db
		m["usePathObfuscation"] = true
		m["encrypt"] = true
		m["passphrase"] = passphrase
	}

	dbDir, vaultDir := t.TempDir(), t.TempDir()
	seedDB(t, dbDir, obfuscate)
	startDaemon(t, dbDir, vaultDir)
	time.Sleep(3 * time.Second) // let the initial mirror scan settle

	v, err := vault.New(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("obf-conflict-%d.md", time.Now().UnixNano())
	before := cc.totalDocs(t)
	if err := v.Write(name, "body behind obfuscation", true); err != nil {
		t.Fatal(err)
	}
	if !cc.waitForDocCount(t, before+1, 20*time.Second) {
		t.Fatalf("obfuscated note %q never propagated to CouchDB", name)
	}

	// The daemon must have stored the doc at exactly the id our reference
	// algorithm computes — the golden match.
	id := referenceObfuscatedID(passphrase, name)
	if _, found := cc.getDocByID(t, id); !found {
		t.Fatalf("daemon did not store %q at computed obfuscated id %q", name, id)
	}

	// Inject a conflicting (losing) revision onto that doc.
	cc.injectConflict(t, id)

	// Production path: ask by PLAINTEXT path; the client must derive the same id
	// and surface the conflict.
	client := couch.New(cc.base, cc.user, cc.pass, db, passphrase)
	conflicts, err := client.Conflicts(t.Context(), name)
	if err != nil {
		t.Fatalf("Conflicts: %v", err)
	}
	if !slices.Contains(conflicts, injectedConflictRev) {
		t.Fatalf("expected injected conflict %q on obfuscated note %q, got %v",
			injectedConflictRev, name, conflicts)
	}
}

// TestSeedScriptBase64PassphraseSurvivesRestart proves the stock Docker
// entrypoint needs NO custom wrapper to handle a base64-encoded E2EE passphrase,
// even across a restart. It runs the real deploy/seed-settings.sh with ONLY
// COUCHDB_PASSPHRASE_B64 set (no plaintext) against a path-obfuscated E2EE vault
// and checks the two gaps the fix closes:
//
//  1. gap 1 — the script must base64-decode COUCHDB_PASSPHRASE_B64 (mirroring
//     internal/config) instead of seeding an empty passphrase; verified at the
//     settings level and end-to-end (a note reads back non-empty, not 0 bytes).
//  2. gap 2 — the daemon's sls+ migration drops the top-level passphrase after
//     the first run, so re-running the seed step (a container restart) must
//     re-assert it. To test this deterministically (the real migration's timing
//     is unreliable within a test window), the test simulates the drop directly,
//     then re-seeds and asserts the passphrase is restored, and finally restarts
//     the daemon and confirms the note is still non-empty.
//
// Gap 1's first-seed assertion fails on the unfixed script (it never decoded the
// B64 var → empty passphrase → 0-byte note). Gap 2's post-restart assertion fails
// on a script that seeds E2EE only once (the simulated drop is never restored).
func TestSeedScriptBase64PassphraseSurvivesRestart(t *testing.T) {
	requireIT(t)
	const passphrase = "integration-obfuscation-passphrase"
	const body = "content seeded from a base64 passphrase"
	b64 := base64.StdEncoding.EncodeToString([]byte(passphrase))

	db := fmt.Sprintf("seed-b64-%d", time.Now().UnixNano())
	cc := newCouch(t)
	cc.db = db
	cc.createDB(t)
	t.Cleanup(func() { cc.dropDB(t) })

	// Only the B64 var — no plaintext COUCHDB_PASSPHRASE — so this exercises the
	// decode path a B64-only deployment relies on.
	seedEnv := []string{
		"COUCHDB_PASSPHRASE_B64=" + b64,
		"USE_PATH_OBFUSCATION=true",
	}
	dbDir, vaultDir := t.TempDir(), t.TempDir()

	// First boot: seed from B64 alone; the decoded passphrase must land verbatim.
	seedDBViaScript(t, dbDir, db, seedEnv...)
	if got := readSetting(t, dbDir, "passphrase"); got != passphrase {
		t.Fatalf("first seed: passphrase = %v, want %q (COUCHDB_PASSPHRASE_B64 not decoded)", got, passphrase)
	}
	if got := readSetting(t, dbDir, "usePathObfuscation"); got != true {
		t.Fatalf("first seed: usePathObfuscation = %v, want true", got)
	}

	d := startDaemon(t, dbDir, vaultDir)
	time.Sleep(3 * time.Second) // let the initial mirror scan settle

	v, err := vault.New(vaultDir)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("seed-b64-%d.md", time.Now().UnixNano())
	before := cc.totalDocs(t)
	if err := v.Write(name, body, true); err != nil {
		t.Fatal(err)
	}
	if !cc.waitForDocCount(t, before+1, 20*time.Second) {
		t.Fatalf("note %q never propagated to CouchDB", name)
	}
	// Obfuscation/E2EE must actually have engaged (doc not stored at the plaintext
	// id) — otherwise the passphrase was irrelevant and the test proves nothing.
	if _, _, found := cc.getDoc(t, name); found {
		t.Fatalf("note %q stored at its plaintext id; obfuscation/E2EE did not engage", name)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("stop daemon: %v", err)
	}

	// Simulate the sls+ migration dropping the top-level passphrase (its real
	// timing is unreliable within a test window). This is the precondition gap 2
	// guards against; doing it deterministically makes the re-assert below a real
	// regression sentinel rather than one that only fires if the migration raced.
	t.Logf("passphrase before simulated migration drop = %v", readSetting(t, dbDir, "passphrase"))
	dropPassphrase(t, dbDir)
	if got := readSetting(t, dbDir, "passphrase"); got != nil && got != "" {
		t.Fatalf("precondition: passphrase not dropped, got %v", got)
	}

	// Restart: re-run the entrypoint's seed step. The CouchDB connection is
	// already configured (no re-seed), but E2EE must be re-asserted every boot.
	seedDBViaScript(t, dbDir, db, seedEnv...)
	if got := readSetting(t, dbDir, "passphrase"); got != passphrase {
		t.Fatalf("after restart: passphrase = %v, want %q (E2EE not re-asserted on reboot)", got, passphrase)
	}

	// Restart the daemon against the SAME db + vault. With the passphrase
	// re-asserted the note stays intact; the regression rewrote it as 0 bytes.
	startDaemon(t, dbDir, vaultDir)
	time.Sleep(4 * time.Second) // give the restart mirror time to (over)write
	got, err := v.Read(name)
	if err != nil {
		t.Fatalf("note %q unreadable after restart: %v", name, err)
	}
	if got != body {
		t.Fatalf("after restart: content = %q, want %q (empty = the 0-byte regression)", got, body)
	}
}

// TestSeedScriptRejectsInvalidBase64 guards the strict-base64 contract: the seed
// script must reject any COUCHDB_PASSPHRASE_B64 that Go's base64.StdEncoding would
// reject, so the daemon seed and the Go server never derive different passphrases.
// A lenient decode (GNU base64 -d) would silently accept these and, for the
// whitespace-only case, seed an empty passphrase — the 0-byte-file regression.
func TestSeedScriptRejectsInvalidBase64(t *testing.T) {
	requireIT(t)
	if _, err := os.Stat(seedSettingsScript); err != nil {
		t.Skipf("seed-settings.sh not found at %s: %v", seedSettingsScript, err)
	}
	cases := map[string]string{
		"garbage":         "!!!not base64!!!",
		"whitespace-only": " \n ",
		"missing-padding": "aGVsbG8",   // "hello" without the trailing '='
		"embedded-space":  "aGVs bG8=", // canonical bytes split by a space
	}
	for name, b64 := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "sh", seedSettingsScript)
			cmd.Env = append(os.Environ(),
				"LIVESYNC_DB="+t.TempDir(),
				"COUCHDB_PASSPHRASE_B64="+b64,
				"USE_PATH_OBFUSCATION=true",
			)
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("expected non-zero exit for invalid base64 %q, got success:\n%s", b64, out)
			}
		})
	}
}

// seedSettingsScript is where the Docker image installs deploy/seed-settings.sh;
// the e2e test stage runs from there.
const seedSettingsScript = "/usr/local/bin/seed-settings.sh"

// seedDBViaScript runs the real deploy/seed-settings.sh against dbDir, exercising
// the production seeding path (base64 decode + the always-on E2EE re-assert)
// instead of the in-test seedDB shortcut. The test skips if the script is absent
// (i.e. running outside the e2e image). extraEnv entries (e.g.
// COUCHDB_PASSPHRASE_B64) are appended last so they win over anything inherited.
func seedDBViaScript(t *testing.T, dbDir, dbName string, extraEnv ...string) {
	t.Helper()
	script := seedSettingsScript
	if _, err := os.Stat(script); err != nil {
		t.Skipf("seed-settings.sh not found at %s: %v", script, err)
	}
	uri := os.Getenv("COUCHDB_URI")
	if uri == "" {
		uri = os.Getenv("COUCHDB_URL")
	}
	if uri == "" {
		t.Fatal("COUCHDB_URI (or COUCHDB_URL) must be set to seed via the script")
	}
	cmd := exec.CommandContext(t.Context(), "sh", script)
	cmd.Env = append(os.Environ(),
		"LIVESYNC_DB="+dbDir,
		"COUCHDB_URI="+uri,
		"COUCHDB_USER="+os.Getenv("COUCHDB_USER"),
		"COUCHDB_PASSWORD="+os.Getenv("COUCHDB_PASSWORD"),
		"COUCHDB_DBNAME="+dbName,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed-settings.sh: %v\n%s", err, out)
	}
}

// readSetting returns a single key from dbDir's .livesync/settings.json.
func readSetting(t *testing.T, dbDir, key string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dbDir, ".livesync", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return m[key]
}

// dropPassphrase rewrites dbDir's settings.json without the top-level passphrase
// and with encrypt=false, reproducing what LiveSync's sls+ startup migration does
// after the first run. Lets the restart re-assert be tested deterministically.
func dropPassphrase(t *testing.T, dbDir string) {
	t.Helper()
	path := filepath.Join(dbDir, ".livesync", "settings.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	delete(m, "passphrase")
	m["encrypt"] = false
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// referenceObfuscatedID computes LiveSync's obfuscated document id independently
// of the production code, so the test binds the production client to the
// daemon's behavior rather than to itself.
func referenceObfuscatedID(passphrase, notePath string) string {
	lower := strings.ToLower(path.Clean(notePath))
	hp := sha256.Sum256([]byte(passphrase))
	inner := hex.EncodeToString(hp[:])
	full := sha256.Sum256([]byte(inner + ":" + lower))
	return "f:" + hex.EncodeToString(full[:])
}

// getDocByID fetches a document by its raw CouchDB id, reporting its revision and
// existence. Unlike getDoc it does no path→id mapping — the id is used verbatim.
func (c *couchClient) getDocByID(t *testing.T, id string) (rev string, found bool) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+"/"+c.db+"/"+id, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// A transport failure is not "not found"; fail loudly so it can't be
		// misread as the daemon storing the doc under a different id.
		t.Fatalf("getDocByID %q: %v", id, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	if resp.StatusCode == http.StatusNotFound {
		return "", false
	}
	var doc struct {
		Rev string `json:"_rev"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("getDocByID %q decode: %v", id, err)
	}
	return doc.Rev, true
}

// injectedConflictRev is a generation-1, near-minimal revid: CouchDB always
// picks the daemon's real revision as the winner, so this one is the losing leaf
// that surfaces in _conflicts.
const injectedConflictRev = "1-0000000000000000000000000000dead"

// injectConflict adds a divergent (losing) revision to the document at id via a
// new_edits=false bulk write, creating an unresolved conflict regardless of which
// branch CouchDB picks as the winner.
func (c *couchClient) injectConflict(t *testing.T, id string) {
	t.Helper()
	body := fmt.Sprintf(`{"new_edits":false,"docs":[{"_id":%q,"_rev":%q,"deleted":false}]}`, id, injectedConflictRev)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, c.base+"/"+c.db+"/_bulk_docs", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatalf("inject conflict: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("inject conflict: status %d", resp.StatusCode)
	}
	// _bulk_docs returns 201 even when individual docs are rejected. With
	// new_edits=false the array carries only the failures, so any element with an
	// error means the conflict revision was not actually stored.
	var results []struct {
		ID     string `json:"id"`
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("inject conflict: decode response: %v", err)
	}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("inject conflict: doc %q rejected: %s (%s)", r.ID, r.Error, r.Reason)
		}
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

// createDB creates the client's CouchDB database (idempotent: an existing db is
// not an error). Used to isolate the encrypted obfuscation test from the shared db.
func (c *couchClient) createDB(t *testing.T) {
	t.Helper()
	c.adminReq(t, http.MethodPut, http.StatusCreated, http.StatusPreconditionFailed)
}

// dropDB deletes the client's CouchDB database (idempotent: a missing db is ok).
func (c *couchClient) dropDB(t *testing.T) {
	t.Helper()
	c.adminReq(t, http.MethodDelete, http.StatusOK, http.StatusNotFound)
}

// adminReq issues method against the database root and fails unless the response
// status is one of okStatuses. It uses context.Background() (not t.Context())
// because dropDB runs from t.Cleanup, where the test context is already canceled.
func (c *couchClient) adminReq(t *testing.T, method string, okStatuses ...int) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+"/"+c.db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Fatalf("couch %s %s: %v", method, c.db, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	if slices.Contains(okStatuses, resp.StatusCode) {
		return
	}
	t.Fatalf("couch %s %s: status %d, want one of %v", method, c.db, resp.StatusCode, okStatuses)
}

// totalDocs returns the CouchDB database's total document count (_all_docs
// total_rows). Used to detect that an obfuscated note synced when its document
// id is unknowable from the plaintext path.
func (c *couchClient) totalDocs(t *testing.T) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.base+"/"+c.db+"/_all_docs?limit=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		t.Logf("couch _all_docs error: %v", err)
		return -1
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("couch body close: %v", cerr)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Logf("couch read error: %v", err)
		return -1
	}
	var out struct {
		TotalRows int `json:"total_rows"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Logf("couch decode error: %v (body=%s)", err, string(body))
		return -1
	}
	return out.TotalRows
}

// waitForDocCount polls until the database holds at least want documents.
func (c *couchClient) waitForDocCount(t *testing.T, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.totalDocs(t) >= want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
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
