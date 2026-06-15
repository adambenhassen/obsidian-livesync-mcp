// Package couch provides read-only conflict lookups against the remote CouchDB.
//
// It talks to CouchDB over plain HTTP (not the single-process leveldb the daemon
// holds), so it can be queried live without pausing sync. To resolve a vault
// path to its CouchDB document the package mirrors LiveSync's path→id scheme:
//
//   - Normally the _id is the (case-folded) vault path.
//   - With **path obfuscation** on, the _id is "f:" + SHA-256 of the passphrase
//     hash and the path (see docID). This is purely a deterministic hash of
//     path+passphrase — not per-document encryption — so given the passphrase we
//     can compute it and conflict detection still works.
//
// Note: path obfuscation (usePathObfuscation) is independent of end-to-end
// content encryption (encrypt). E2EE alone does not obfuscate the _id, so
// plaintext-path lookups work for encrypted-but-not-obfuscated vaults too.
//
// WARNING: on an obfuscated vault a WRONG (but non-empty) passphrase derives a
// valid-looking id that exists nowhere, which reads back as a 404 → "no conflict".
// That is an unfalsifiable false all-clear, indistinguishable at the HTTP layer
// from a genuinely conflict-free note. The empty-passphrase case is caught in
// config.Load; a mismatched passphrase is the operator's responsibility — it must
// match the vault.
package couch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Client looks up document conflicts in a CouchDB database.
type Client struct {
	base string
	user string
	pass string
	db   string
	// obfuscatePassphrase mirrors LiveSync's obfuscatePassphrase: the passphrase
	// when path obfuscation is enabled, empty otherwise. Empty = plaintext ids.
	obfuscatePassphrase string
	hc                  *http.Client
}

// New returns a Client, or nil if uri or db is empty (conflict detection
// disabled). A nil *Client is safe to call — its Conflicts method returns no
// conflicts — so callers don't have to special-case the disabled state.
//
// obfuscatePassphrase enables path-obfuscated id derivation when non-empty; it
// must be the vault's passphrase and is only meaningful when the vault has
// usePathObfuscation set. Pass "" for a non-obfuscated vault.
func New(uri, user, pass, db, obfuscatePassphrase string) *Client {
	if uri == "" || db == "" {
		return nil
	}
	return &Client{
		base:                strings.TrimRight(uri, "/"),
		user:                user,
		pass:                pass,
		db:                  db,
		obfuscatePassphrase: obfuscatePassphrase,
		hc:                  &http.Client{Timeout: 5 * time.Second},
	}
}

// Conflicts returns the conflicting revision ids for the note at notePath, or
// nil when there are none (or the document does not exist). A nil Client
// (conflict detection disabled) returns no conflicts without erroring.
func (c *Client) Conflicts(ctx context.Context, notePath string) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	endpoint := c.base + "/" + c.db + "/" + c.docID(notePath) + "?conflicts=true"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("couch: closing response body: %v", cerr)
		}
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		// 404 is "no such document" (→ no conflict) OR "no such database" (a
		// misconfigured COUCHDB_DBNAME). Surface the latter instead of letting a
		// wrong db name masquerade as a perpetually conflict-free vault.
		var body struct {
			Reason string `json:"reason"`
		}
		if derr := json.NewDecoder(resp.Body).Decode(&body); derr == nil &&
			strings.Contains(strings.ToLower(body.Reason), "database does not exist") {
			return nil, fmt.Errorf("couchdb database %q does not exist", c.db)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("couchdb conflict query for %q: HTTP %d", notePath, resp.StatusCode)
	}

	var doc struct {
		Conflicts []string `json:"_conflicts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("couchdb decode for %q: %w", notePath, err)
	}
	return doc.Conflicts, nil
}

// prefixObfuscated is LiveSync's PREFIX_OBFUSCATED: obfuscated document ids are
// the path's hash with this prefix.
const prefixObfuscated = "f:"

// docID maps a vault path to the URL path segment for its CouchDB document.
//
// The path is cleaned (so ./x and sub//x map to the same doc the vault writes)
// and lowercased — LiveSync lowercases ids unless handleFilenameCaseSensitive is
// set, which the server does not support (the bundled livesync-cli cannot sync
// such a vault), so ids are always case-folded here.
//
//   - Plaintext (no obfuscation): the path, escaped as one segment (slashes as
//     %2F; CouchDB decodes %20 and other percent-escapes back).
//   - Obfuscated: "f:" + SHA256(SHA256(passphrase) + ":" + path), a deterministic
//     mirror of LiveSync's path2id_base. The result is ASCII hex after the "f:"
//     prefix, so it needs no escaping.
func (c *Client) docID(notePath string) string {
	lower := strings.ToLower(path.Clean(notePath))
	if c.obfuscatePassphrase == "" {
		return strings.ReplaceAll(url.PathEscape(lower), "/", "%2F")
	}
	// The passphrase is hashed verbatim (case-significant in LiveSync).
	hashedPassphrase := sha256Hex(c.obfuscatePassphrase)
	return prefixObfuscated + sha256Hex(hashedPassphrase+":"+lower)
}

// sha256Hex returns the lowercase hex SHA-256 of s. This mirrors LiveSync's
// hashString (livesync-commonlib string_and_binary/path.ts, verified at
// vrtmrz/obsidian-livesync@1a1f816 / commonlib 5a552b3): its "stretching" loop
// re-hashes the original input each iteration rather than the running digest, so
// it reduces to a single SHA-256. TestObfuscatedConflictDetection guards against
// upstream drift by checking a real daemon stores docs at the derived id.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
