package couch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// writeBody writes a response body, failing the test on error.
func writeBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestNewDisabledWhenIncomplete(t *testing.T) {
	if New("", "u", "p", "db", "", true) != nil {
		t.Error("New with empty uri should return nil")
	}
	if New("http://x", "u", "p", "", "", true) != nil {
		t.Error("New with empty db should return nil")
	}
	if New("http://x", "u", "p", "db", "", true) == nil {
		t.Error("New with uri+db should return a client")
	}
}

func TestConflictsParsesAndLowercasesPath(t *testing.T) {
	var gotURI, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI // raw, keeps %2F encoding
		gotAuth = r.Header.Get("Authorization")
		writeBody(t, w, `{"_id":"daily/note.md","_rev":"2-bbb","_conflicts":["1-aaa"]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "admin", "secret", "livesync", "", true)
	got, err := c.Conflicts(t.Context(), "Daily/Note.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "1-aaa" {
		t.Errorf("conflicts = %v, want [1-aaa]", got)
	}
	// LiveSync lowercases the doc id and CouchDB needs slashes as %2F.
	if want := "/livesync/daily%2Fnote.md?conflicts=true"; gotURI != want {
		t.Errorf("request uri = %q, want %q", gotURI, want)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("expected basic auth header, got %q", gotAuth)
	}
}

// With path obfuscation on, the lookup must hit the obfuscated doc id
// "f:" + SHA256(SHA256(passphrase) + ":" + lowercased-path), not the plaintext
// path. The expected id was computed independently from LiveSync's algorithm.
func TestConflictsObfuscatedID(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		writeBody(t, w, `{"_id":"f:x","_conflicts":["1-aaa"]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "", "", "db", "hunter2", true)
	got, err := c.Conflicts(t.Context(), "Projects/Plan B.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "1-aaa" {
		t.Errorf("conflicts = %v, want [1-aaa]", got)
	}
	const want = "/db/f:291774b509a084e67b0291686b613ebdc5c81d07572a30d6a088d9b9b5c1b886?conflicts=true"
	if gotURI != want {
		t.Errorf("request uri = %q, want %q", gotURI, want)
	}
}

// TestDocID pins the path→id derivation directly, including the case-sensitivity
// switch. Obfuscated golden ids were computed independently from LiveSync's
// algorithm. handleFilenameCaseSensitive=true (caseInsensitive=false) must NOT
// lowercase, or the derived id won't match what such a vault stores.
func TestDocID(t *testing.T) {
	const passphrase = "hunter2"
	tests := []struct {
		name                string
		notePath            string
		obfuscatePassphrase string
		caseInsensitive     bool
		want                string
	}{
		{"plaintext lowercased", "Note-One.md", "", true, "note-one.md"},
		{"plaintext case-sensitive", "Note-One.md", "", false, "Note-One.md"},
		{"plaintext slash escaped", "Daily/Note.md", "", true, "daily%2Fnote.md"},
		{"obfuscated lowercased", "Note-One.md", passphrase, true,
			"f:9b3202d1fc2cf3b6482c836f9d5fbddc227488f1d10193cee62ca8a23ef4327a"},
		{"obfuscated case-sensitive", "Note-One.md", passphrase, false,
			"f:8f912fab6a603e4438cdcfcac7b092b3aea87152198e70f959433a1556991738"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{obfuscatePassphrase: tt.obfuscatePassphrase, caseInsensitive: tt.caseInsensitive}
			if got := c.docID(tt.notePath); got != tt.want {
				t.Errorf("docID(%q) = %q, want %q", tt.notePath, got, tt.want)
			}
		})
	}
}

// A non-canonical path spelling must resolve to the same id the vault writes,
// in both branches.
func TestDocIDCleansPath(t *testing.T) {
	for _, obf := range []string{"", "hunter2"} {
		c := &Client{obfuscatePassphrase: obf, caseInsensitive: true}
		if got, want := c.docID("./Daily//Note.md"), c.docID("Daily/Note.md"); got != want {
			t.Errorf("obfuscate=%q: docID did not canonicalize: %q != %q", obf, got, want)
		}
	}
}

func TestConflictsNoneWhenNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, `{"_id":"x.md","_rev":"1-aaa"}`) // no _conflicts
	}))
	defer srv.Close()

	got, err := New(srv.URL, "", "", "db", "", true).Conflicts(t.Context(), "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("conflicts = %v, want none", got)
	}
}

func TestConflictsMissingDocIsNoConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "", "", "db", "", true).Conflicts(t.Context(), "nope.md")
	if err != nil {
		t.Fatalf("404 should not error: %v", err)
	}
	if got != nil {
		t.Errorf("conflicts = %v, want nil", got)
	}
}

func TestConflictsServerErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", "", "db", "", true).Conflicts(t.Context(), "x.md"); err == nil {
		t.Error("expected error on HTTP 500")
	}
}

func TestConflictsNilClientIsDisabled(t *testing.T) {
	var c *Client // disabled
	got, err := c.Conflicts(t.Context(), "x.md")
	if err != nil || got != nil {
		t.Fatalf("nil client = (%v, %v), want (nil, nil)", got, err)
	}
}

// A 404 with "Database does not exist" is a misconfiguration, not "no conflict".
func TestConflictsMissingDatabaseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeBody(t, w, `{"error":"not_found","reason":"Database does not exist."}`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", "", "wrongdb", "", true).Conflicts(t.Context(), "x.md"); err == nil {
		t.Error("a missing database must surface an error, not look conflict-free")
	}
}

// A 404 for a missing document is genuinely "no conflict".
func TestConflictsMissingDocumentIsClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeBody(t, w, `{"error":"not_found","reason":"missing"}`)
	}))
	defer srv.Close()

	got, err := New(srv.URL, "", "", "db", "", true).Conflicts(t.Context(), "x.md")
	if err != nil || got != nil {
		t.Fatalf("missing doc = (%v, %v), want (nil, nil)", got, err)
	}
}

// Spaces and non-ASCII must be percent-encoded and slashes as %2F; CouchDB
// decodes percent-escapes back to the literal _id.
func TestConflictsEscapesSpacesAndUnicode(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		writeBody(t, w, `{"_id":"x"}`)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", "", "db", "", true).Conflicts(t.Context(), "Daily/Plan B é.md"); err != nil {
		t.Fatal(err)
	}
	if want := "/db/daily%2Fplan%20b%20%C3%A9.md?conflicts=true"; gotURI != want {
		t.Errorf("request uri = %q, want %q", gotURI, want)
	}
}
