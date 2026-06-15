package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

// newConnectedClient drives registered tools through the in-memory transport.
func newConnectedClient(t *testing.T, v *vault.Vault) (*mcp.ClientSession, func()) {
	t.Helper()
	srv := New(v)
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, func() { _ = cs.Close() }
}

func TestWriteAndReadNoteViaTools(t *testing.T) {
	v, _ := vault.New(t.TempDir())
	cs, done := newConnectedClient(t, v)
	defer done()

	_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_note",
		Arguments: map[string]any{"path": "x.md", "content": "hello", "overwrite": false},
	})
	if err != nil {
		t.Fatalf("write_note: %v", err)
	}

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_note",
		Arguments: map[string]any{"path": "x.md"},
	})
	if err != nil {
		t.Fatalf("read_note: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if text != "hello" {
		t.Errorf("read_note text = %q, want %q", text, "hello")
	}
}

func TestSearchViaTools(t *testing.T) {
	v, _ := vault.New(t.TempDir())
	_ = v.Write("findme.md", "needle in haystack", false)
	_ = v.Write("other.md", "nothing", false)
	cs, done := newConnectedClient(t, v)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search_notes",
		Arguments: map[string]any{"query": "needle", "mode": "content"},
	})
	if err != nil {
		t.Fatalf("search_notes: %v", err)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if want := "findme.md"; !contains(text, want) {
		t.Errorf("search result %q does not contain %q", text, want)
	}
}

func TestDeleteViaTools(t *testing.T) {
	v, _ := vault.New(t.TempDir())
	_ = v.Write("gone.md", "data", false)
	cs, done := newConnectedClient(t, v)
	defer done()

	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "delete_note",
		Arguments: map[string]any{"path": "gone.md"},
	}); err != nil {
		t.Fatalf("delete_note: %v", err)
	}
	if _, err := v.Read("gone.md"); err == nil {
		t.Fatal("note should be deleted")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
