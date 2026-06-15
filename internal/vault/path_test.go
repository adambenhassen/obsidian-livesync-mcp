package vault

import (
	"path/filepath"
	"testing"
)

func TestResolveRejectsTraversal(t *testing.T) {
	v, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"../escape.md", "../../etc/passwd", "foo/../../bar.md", "/abs/path.md"}
	for _, p := range bad {
		if _, err := v.resolve(p); err == nil {
			t.Errorf("resolve(%q) = nil error, want escape error", p)
		}
	}
}

func TestResolveAcceptsCleanRelative(t *testing.T) {
	root := t.TempDir()
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := v.resolve("Daily/note.md")
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	want := filepath.Join(root, "Daily", "note.md")
	if abs != want {
		t.Errorf("resolve() = %q, want %q", abs, want)
	}
}
