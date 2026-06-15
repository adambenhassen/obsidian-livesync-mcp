package vault

import (
	"os"
	"path/filepath"
	"testing"
)

// A symlink inside the vault that points outside it must not be followed by
// resolve(): the vault is mutated by an external sync process, so symlinks are
// attacker-influenceable and a lexical-only guard would leak the filesystem.
func TestResolveRejectsSymlinkEscapeFile(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "leak.md")); err != nil {
		t.Fatal(err)
	}
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.resolve("leak.md"); err == nil {
		t.Fatal("resolve(symlink-to-outside) = nil error, want escape error")
	}
}

func TestResolveRejectsSymlinkEscapeDir(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, "evil")); err != nil {
		t.Fatal(err)
	}
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	// Writing "evil/x.md" would escape via the symlinked directory.
	if _, err := v.resolve("evil/x.md"); err == nil {
		t.Fatal("resolve(through-symlinked-dir) = nil error, want escape error")
	}
}

// A symlink that stays inside the vault is fine.
func TestResolveAllowsInVaultSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.resolve("link/note.md"); err != nil {
		t.Fatalf("resolve(in-vault symlink) error = %v, want nil", err)
	}
}
