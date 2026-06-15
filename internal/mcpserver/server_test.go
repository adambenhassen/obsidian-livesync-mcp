package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

func newVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustWrite(t *testing.T, v *vault.Vault, path, content string) {
	t.Helper()
	if err := v.Write(path, content, false); err != nil {
		t.Fatal(err)
	}
}

// firstText returns the text of a tool result's first content item.
func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("tool result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

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
	return cs, func() {
		if err := cs.Close(); err != nil {
			t.Errorf("client close: %v", err)
		}
	}
}

func TestWriteAndReadNoteViaTools(t *testing.T) {
	v := newVault(t)
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
	if text := firstText(t, res); text != "hello" {
		t.Errorf("read_note text = %q, want %q", text, "hello")
	}
}

func TestSearchViaTools(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "findme.md", "needle in haystack")
	mustWrite(t, v, "other.md", "nothing")
	cs, done := newConnectedClient(t, v)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search_notes",
		Arguments: map[string]any{"query": "needle", "mode": "content"},
	})
	if err != nil {
		t.Fatalf("search_notes: %v", err)
	}
	if text := firstText(t, res); !strings.Contains(text, "findme.md") {
		t.Errorf("search result %q does not contain %q", text, "findme.md")
	}
}

func TestDeleteViaTools(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "gone.md", "data")
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
