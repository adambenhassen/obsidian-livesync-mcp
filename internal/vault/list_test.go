package vault

import "testing"

func TestListRecursiveAndFiltered(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a.md", "x", false)
	mustWrite(t, v, "sub/b.md", "x", false)
	mustWrite(t, v, "sub/c.txt", "x", false) // non-md ignored

	all, err := v.List("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List recursive = %d notes, want 2 (%+v)", len(all), all)
	}

	top, err := v.List("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Path != "a.md" {
		t.Fatalf("List non-recursive top = %+v", top)
	}

	sub, err := v.List("sub", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 1 || sub[0].Path != "sub/b.md" {
		t.Fatalf("List sub = %+v", sub)
	}
}

func TestMetadata(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "m.md", "12345", false)
	n, err := v.Metadata("m.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.Path != "m.md" || n.Size != 5 || n.ModTime.IsZero() {
		t.Fatalf("Metadata = %+v", n)
	}
}
