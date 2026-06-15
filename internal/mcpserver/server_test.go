package mcpserver

import (
	"context"
	"errors"
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

// fakeChecker is a stub ConflictChecker for tests.
type fakeChecker struct {
	conflicts map[string][]string
	err       error
}

func (f fakeChecker) Conflicts(_ context.Context, path string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.conflicts[path], nil
}

// newConnectedClient drives registered tools through the in-memory transport.
func newConnectedClient(t *testing.T, v *vault.Vault, readOnly bool, checker ConflictChecker) (*mcp.ClientSession, func()) {
	t.Helper()
	srv := New(v, readOnly, checker)
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
	cs, done := newConnectedClient(t, v, false, nil)
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
	cs, done := newConnectedClient(t, v, false, nil)
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
	cs, done := newConnectedClient(t, v, false, nil)
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

func TestAppendAndMoveViaTools(t *testing.T) {
	v := newVault(t)
	cs, done := newConnectedClient(t, v, false, nil)
	defer done()

	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "append_to_note",
		Arguments: map[string]any{"path": "log.md", "content": "line\n"},
	}); err != nil {
		t.Fatalf("append_to_note: %v", err)
	}
	if got, err := v.Read("log.md"); err != nil || got != "line\n" {
		t.Fatalf("after append: got %q err %v", got, err)
	}

	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "move_note",
		Arguments: map[string]any{"from": "log.md", "to": "archive/log.md"},
	}); err != nil {
		t.Fatalf("move_note: %v", err)
	}
	if _, err := v.Read("log.md"); err == nil {
		t.Error("source should be gone after move")
	}
	if got, err := v.Read("archive/log.md"); err != nil || got != "line\n" {
		t.Fatalf("after move: got %q err %v", got, err)
	}
}

func TestWriteRefusesOnConflict(t *testing.T) {
	v := newVault(t)
	checker := fakeChecker{conflicts: map[string][]string{"x.md": {"1-aaa"}}}
	cs, done := newConnectedClient(t, v, false, checker)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_note",
		Arguments: map[string]any{"path": "x.md", "content": "hi", "overwrite": true},
	})
	if err != nil {
		t.Fatalf("write_note call: %v", err)
	}
	if !res.IsError {
		t.Fatal("write_note should return an error result for a conflicted note")
	}
	if _, rerr := v.Read("x.md"); rerr == nil {
		t.Error("conflicted note must not have been written")
	}
}

func TestWriteProceedsWhenConflictCheckFails(t *testing.T) {
	v := newVault(t)
	checker := fakeChecker{err: errors.New("couch unreachable")} // fail-open
	cs, done := newConnectedClient(t, v, false, checker)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "write_note",
		Arguments: map[string]any{"path": "x.md", "content": "hi", "overwrite": true},
	})
	if err != nil {
		t.Fatalf("write_note call: %v", err)
	}
	if res.IsError {
		t.Fatal("write should proceed when the conflict check itself errors (fail-open)")
	}
	got, rerr := v.Read("x.md")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if got != "hi" {
		t.Errorf("write did not land: %q", got)
	}
}

func TestAppendRefusesOnConflict(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "log.md", "line1\n")
	checker := fakeChecker{conflicts: map[string][]string{"log.md": {"1-aaa"}}}
	cs, done := newConnectedClient(t, v, false, checker)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "append_to_note",
		Arguments: map[string]any{"path": "log.md", "content": "line2\n"},
	})
	if err != nil {
		t.Fatalf("append_to_note call: %v", err)
	}
	if !res.IsError {
		t.Fatal("append_to_note should refuse a conflicted note")
	}
	got, rerr := v.Read("log.md")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if got != "line1\n" {
		t.Errorf("conflicted note was modified by append: %q", got)
	}
}

func TestMetadataIncludesConflicts(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "x.md", "data")
	checker := fakeChecker{conflicts: map[string][]string{"x.md": {"1-aaa", "1-bbb"}}}
	cs, done := newConnectedClient(t, v, false, checker)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note_metadata",
		Arguments: map[string]any{"path": "x.md"},
	})
	if err != nil {
		t.Fatalf("get_note_metadata: %v", err)
	}
	text := firstText(t, res)
	if !strings.Contains(text, `"conflicts":["1-aaa","1-bbb"]`) {
		t.Errorf("metadata missing conflicts: %s", text)
	}
	if !strings.Contains(text, `"conflictCheck":"ok"`) {
		t.Errorf("metadata should report conflictCheck=ok: %s", text)
	}
}

// A failed conflict check must NOT look like "no conflicts" — it reports
// conflictCheck=unavailable so an agent doesn't treat the empty list as a green light.
func TestMetadataReportsCheckUnavailable(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "x.md", "data")
	checker := fakeChecker{err: errors.New("couch unreachable")}
	cs, done := newConnectedClient(t, v, false, checker)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note_metadata",
		Arguments: map[string]any{"path": "x.md"},
	})
	if err != nil {
		t.Fatalf("get_note_metadata: %v", err)
	}
	if text := firstText(t, res); !strings.Contains(text, `"conflictCheck":"unavailable"`) {
		t.Errorf("metadata should report conflictCheck=unavailable on error: %s", text)
	}
}

func TestMetadataCheckDisabledWithoutChecker(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "x.md", "data")
	cs, done := newConnectedClient(t, v, false, nil)
	defer done()

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note_metadata",
		Arguments: map[string]any{"path": "x.md"},
	})
	if err != nil {
		t.Fatalf("get_note_metadata: %v", err)
	}
	if text := firstText(t, res); !strings.Contains(text, `"conflictCheck":"disabled"`) {
		t.Errorf("metadata should report conflictCheck=disabled with no checker: %s", text)
	}
}

func TestReadOnlyExposesOnlyReadTools(t *testing.T) {
	v := newVault(t)
	mustWrite(t, v, "seed.md", "existing content")
	cs, done := newConnectedClient(t, v, true, nil) // read-only
	defer done()

	// Read tools remain available.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_note",
		Arguments: map[string]any{"path": "seed.md"},
	})
	if err != nil {
		t.Fatalf("read_note should work in read-only mode: %v", err)
	}
	if got := firstText(t, res); got != "existing content" {
		t.Errorf("read_note = %q, want %q", got, "existing content")
	}

	// Mutating tools are not registered, so calling them errors.
	for _, name := range []string{"write_note", "append_to_note", "delete_note", "move_note"} {
		if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      name,
			Arguments: map[string]any{"path": "seed.md", "content": "x", "from": "seed.md", "to": "z.md"},
		}); err == nil {
			t.Errorf("%s should not be available in read-only mode", name)
		}
	}

	// The vault file is untouched.
	got, err := v.Read("seed.md")
	if err != nil {
		t.Fatal(err)
	}
	if got != "existing content" {
		t.Errorf("vault was mutated in read-only mode: %q", got)
	}
}
