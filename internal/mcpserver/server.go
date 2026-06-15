package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adambenhassen/obsidian-livesync-mcp/internal/vault"
)

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func jsonText(val any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	return text(string(b)), nil
}

type pathArgs struct {
	Path string `json:"path" jsonschema:"vault-relative note path"`
}

// New builds an MCP server exposing note tools backed by v. When readOnly is
// true, only the read tools are registered — the mutating tools (write, append,
// delete, move) are not advertised at all, so agents cannot modify the vault.
func New(v *vault.Vault, readOnly bool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "livesync-mcp", Version: "0.1.0"}, nil)
	registerReadTools(s, v)
	if !readOnly {
		registerWriteTools(s, v)
	}
	return s
}

func registerReadTools(s *mcp.Server, v *vault.Vault) {
	type listArgs struct {
		Folder    string `json:"folder,omitempty"    jsonschema:"vault-relative folder, empty for root"`
		Recursive bool   `json:"recursive,omitempty" jsonschema:"recurse into subfolders"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "list_notes", Description: "List notes under a folder."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
			notes, err := v.List(a.Folder, a.Recursive)
			if err != nil {
				return nil, nil, err
			}
			r, err := jsonText(notes)
			return r, nil, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "read_note", Description: "Read a note's content."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a pathArgs) (*mcp.CallToolResult, any, error) {
			body, err := v.Read(a.Path)
			if err != nil {
				return nil, nil, err
			}
			return text(body), nil, nil
		})

	type searchArgs struct {
		Query string `json:"query" jsonschema:"search text"`
		Mode  string `json:"mode"  jsonschema:"\"filename\" or \"content\""`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "search_notes", Description: "Search notes by filename or content."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a searchArgs) (*mcp.CallToolResult, any, error) {
			notes, err := v.Search(a.Query, a.Mode)
			if err != nil {
				return nil, nil, err
			}
			r, err := jsonText(notes)
			return r, nil, err
		})

	mcp.AddTool(s, &mcp.Tool{Name: "get_note_metadata", Description: "Get a note's metadata."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a pathArgs) (*mcp.CallToolResult, any, error) {
			n, err := v.Metadata(a.Path)
			if err != nil {
				return nil, nil, err
			}
			r, err := jsonText(n)
			return r, nil, err
		})
}

func registerWriteTools(s *mcp.Server, v *vault.Vault) {
	type writeArgs struct {
		Path      string `json:"path"                jsonschema:"vault-relative note path"`
		Content   string `json:"content"             jsonschema:"full note content"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"overwrite if it exists"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "write_note", Description: "Create or update a note."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a writeArgs) (*mcp.CallToolResult, any, error) {
			if err := v.Write(a.Path, a.Content, a.Overwrite); err != nil {
				return nil, nil, err
			}
			return text("ok"), nil, nil
		})

	type appendArgs struct {
		Path    string `json:"path"    jsonschema:"vault-relative note path"`
		Content string `json:"content" jsonschema:"text to append"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "append_to_note", Description: "Append text to a note."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a appendArgs) (*mcp.CallToolResult, any, error) {
			if err := v.Append(a.Path, a.Content); err != nil {
				return nil, nil, err
			}
			return text("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "delete_note", Description: "Delete a note."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a pathArgs) (*mcp.CallToolResult, any, error) {
			if err := v.Delete(a.Path); err != nil {
				return nil, nil, err
			}
			return text("ok"), nil, nil
		})

	type moveArgs struct {
		From string `json:"from" jsonschema:"current vault-relative path"`
		To   string `json:"to"   jsonschema:"new vault-relative path"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "move_note", Description: "Move or rename a note."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a moveArgs) (*mcp.CallToolResult, any, error) {
			if err := v.Move(a.From, a.To); err != nil {
				return nil, nil, err
			}
			return text("ok"), nil, nil
		})
}
