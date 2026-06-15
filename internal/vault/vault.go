package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathEscape is returned when a note path resolves outside the vault root.
var ErrPathEscape = errors.New("note path escapes vault root")

// Vault provides filesystem CRUD over notes under a single root directory.
type Vault struct {
	root string // absolute, cleaned
}

// New validates that root exists and is a directory, then returns a Vault.
func New(root string) (*Vault, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("vault root is not a directory")
	}
	return &Vault{root: abs}, nil
}

// resolve converts a vault-relative, forward-slashed note path into a safe
// absolute filesystem path, rejecting any path that escapes the root.
func (v *Vault) resolve(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", ErrPathEscape
	}
	// Clean the relative path and reject any that traverses above the root.
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	abs := filepath.Join(v.root, clean)
	// Defence in depth: confirm the joined path is still under the root.
	check, err := filepath.Rel(v.root, abs)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	return abs, nil
}
