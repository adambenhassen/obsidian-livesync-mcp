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
	if New("", "u", "p", "db") != nil {
		t.Error("New with empty uri should return nil")
	}
	if New("http://x", "u", "p", "") != nil {
		t.Error("New with empty db should return nil")
	}
	if New("http://x", "u", "p", "db") == nil {
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

	c := New(srv.URL, "admin", "secret", "livesync")
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

func TestConflictsNoneWhenNull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, `{"_id":"x.md","_rev":"1-aaa"}`) // no _conflicts
	}))
	defer srv.Close()

	got, err := New(srv.URL, "", "", "db").Conflicts(t.Context(), "x.md")
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

	got, err := New(srv.URL, "", "", "db").Conflicts(t.Context(), "nope.md")
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

	if _, err := New(srv.URL, "", "", "db").Conflicts(t.Context(), "x.md"); err == nil {
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

	if _, err := New(srv.URL, "", "", "wrongdb").Conflicts(t.Context(), "x.md"); err == nil {
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

	got, err := New(srv.URL, "", "", "db").Conflicts(t.Context(), "x.md")
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

	if _, err := New(srv.URL, "", "", "db").Conflicts(t.Context(), "Daily/Plan B é.md"); err != nil {
		t.Fatal(err)
	}
	if want := "/db/daily%2Fplan%20b%20%C3%A9.md?conflicts=true"; gotURI != want {
		t.Errorf("request uri = %q, want %q", gotURI, want)
	}
}
