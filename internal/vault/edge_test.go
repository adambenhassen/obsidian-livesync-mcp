package vault

import (
	"path/filepath"
	"testing"
)

// Normalised-but-in-root paths must resolve to the obvious location, never
// escape, and never error on the path itself.
func TestResolveNormalizationStaysInRoot(t *testing.T) {
	root := t.TempDir()
	v, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"Daily/./note.md":   filepath.Join(root, "Daily", "note.md"),
		"Daily//note.md":    filepath.Join(root, "Daily", "note.md"),
		"./note.md":         filepath.Join(root, "note.md"),
		"Daily/../note.md":  filepath.Join(root, "note.md"), // .. that stays inside
		"a/b/../../c.md":    filepath.Join(root, "c.md"),
		"Notes/Sub/deep.md": filepath.Join(root, "Notes", "Sub", "deep.md"),
	}
	for in, want := range cases {
		got, err := v.resolve(in)
		if err != nil {
			t.Errorf("resolve(%q) error = %v, want nil", in, err)
			continue
		}
		if got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

// Obsidian vaults routinely contain Unicode, spaces, and emoji in names.
func TestUnicodePathRoundtrip(t *testing.T) {
	v := newTestVault(t)
	name := "Notes/café — déjà vu 📝.md"
	mustWrite(t, v, name, "naïve résumé", false)

	if got := mustRead(t, v, name); got != "naïve résumé" {
		t.Errorf("Read() = %q", got)
	}
	res, err := v.Search("café", "filename")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != name {
		t.Fatalf("filename search for unicode = %+v", res)
	}
}

func TestWriteEmptyContent(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "empty.md", "", false)
	if got := mustRead(t, v, "empty.md"); got != "" {
		t.Errorf("Read() = %q, want empty", got)
	}
	n, err := v.Metadata("empty.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.Size != 0 {
		t.Errorf("Size = %d, want 0", n.Size)
	}
}

func TestListEmptyVaultReturnsEmpty(t *testing.T) {
	v := newTestVault(t)
	notes, err := v.List("", true)
	if err != nil {
		t.Fatalf("List on empty vault error = %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("List on empty vault = %+v, want empty", notes)
	}
}
