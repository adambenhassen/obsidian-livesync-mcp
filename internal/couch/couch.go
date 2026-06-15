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
// disabled — callers treat a nil Client as "no conflicts").
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
// empty slice when there are none (or the document does not exist yet).
func (c *Client) Conflicts(ctx context.Context, notePath string) ([]string, error) {
	// LiveSync doc _id is the lowercased vault path; escape it as a single path
	// segment (slashes included).
	id := strings.ReplaceAll(url.PathEscape(strings.ToLower(notePath)), "/", "%2F")
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
