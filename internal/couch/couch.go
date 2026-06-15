// Package couch provides read-only conflict lookups against the remote CouchDB.
//
// It talks to CouchDB over plain HTTP (not the single-process leveldb the daemon
// holds), so it can be queried live without pausing sync. LiveSync stores each
// note as a CouchDB document whose _id is the **lowercased** vault path; this
// package mirrors that so a lookup by vault path resolves to the right doc.
//
// Limitation: with end-to-end encryption enabled the document ids are obfuscated
// (not the path), so conflict detection only works for non-encrypted vaults.
package couch

import (
	"context"
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
	hc   *http.Client
}

// New returns a Client, or nil if uri or db is empty (conflict detection
// disabled). A nil *Client is safe to call — its Conflicts method returns no
// conflicts — so callers don't have to special-case the disabled state.
func New(uri, user, pass, db string) *Client {
	if uri == "" || db == "" {
		return nil
	}
	return &Client{
		base: strings.TrimRight(uri, "/"),
		user: user,
		pass: pass,
		db:   db,
		hc:   &http.Client{Timeout: 5 * time.Second},
	}
}

// Conflicts returns the conflicting revision ids for the note at notePath, or an
// empty slice when there are none (or the document does not exist). A nil Client
// (conflict detection disabled) returns no conflicts without erroring.
func (c *Client) Conflicts(ctx context.Context, notePath string) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	// LiveSync's doc _id is the lowercased, cleaned vault path; clean it so a
	// non-canonical spelling (./x, sub//x) maps to the same doc the vault writes.
	// Escape it as a single path segment (slashes as %2F; CouchDB decodes %20 and
	// other percent-escapes back to the literal id).
	id := strings.ReplaceAll(url.PathEscape(strings.ToLower(path.Clean(notePath))), "/", "%2F")
	endpoint := c.base + "/" + c.db + "/" + id + "?conflicts=true"

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
