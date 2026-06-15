package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/config"
	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

type bearerRT struct {
	token string
	rt    http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func newTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func connectMCP(t *testing.T, baseURL, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	hc := &http.Client{Transport: bearerRT{token: token, rt: http.DefaultTransport}}
	tr := &mcp.StreamableClientTransport{Endpoint: baseURL + "/mcp", HTTPClient: hc}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("client close: %v", err)
		}
	})
	return cs
}

// Full mode: write_note reaches the vault through the real HTTP+auth+MCP wiring.
func TestHandlerFullModeAllowsWrite(t *testing.T) {
	v := newTestVault(t)
	ts := httptest.NewServer(newHandler(v, config.Config{APIKey: "tok"}, func() bool { return true }))
	t.Cleanup(ts.Close) // runs after the client is closed (LIFO)

	cs := connectMCP(t, ts.URL, "tok")
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_note",
		Arguments: map[string]any{"path": "x.md", "content": "hi", "overwrite": true},
	}); err != nil {
		t.Fatalf("write_note: %v", err)
	}
	if got, err := v.Read("x.md"); err != nil || got != "hi" {
		t.Fatalf("write did not land: got %q err %v", got, err)
	}
}

// Read-only mode (wired from config.ReadOnly): write_note must be unavailable.
func TestHandlerReadOnlyHidesWrite(t *testing.T) {
	v := newTestVault(t)
	ts := httptest.NewServer(newHandler(v, config.Config{APIKey: "tok", ReadOnly: true}, func() bool { return true }))
	t.Cleanup(ts.Close) // runs after the client is closed (LIFO)

	cs := connectMCP(t, ts.URL, "tok")
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_note",
		Arguments: map[string]any{"path": "x.md", "content": "hi"},
	}); err == nil {
		t.Fatal("write_note should be unavailable in read-only mode")
	}
}

func TestHandlerMCPRequiresAuth(t *testing.T) {
	v := newTestVault(t)
	ts := httptest.NewServer(newHandler(v, config.Config{APIKey: "tok"}, func() bool { return true }))
	defer ts.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/mcp", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("body close: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/mcp without token = %d, want 401", resp.StatusCode)
	}
}

func TestHandlerHealthzReflectsDaemon(t *testing.T) {
	v := newTestVault(t)
	for _, tc := range []struct {
		healthy bool
		want    int
	}{
		{true, http.StatusOK},
		{false, http.StatusServiceUnavailable},
	} {
		ts := httptest.NewServer(newHandler(v, config.Config{}, func() bool { return tc.healthy }))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/healthz", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil {
			t.Logf("body close: %v", err)
		}
		ts.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("healthz(healthy=%v) = %d, want %d", tc.healthy, resp.StatusCode, tc.want)
		}
	}
}
