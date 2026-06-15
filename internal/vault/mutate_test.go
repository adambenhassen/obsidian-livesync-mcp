package vault

import "testing"

func TestAppendCreatesAndAppends(t *testing.T) {
	v, _ := New(t.TempDir())
	if err := v.Append("log.md", "line1\n"); err != nil {
		t.Fatal(err)
	}
	if err := v.Append("log.md", "line2\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := v.Read("log.md")
	if got != "line1\nline2\n" {
		t.Errorf("Read() = %q", got)
	}
}

func TestDeleteRemovesNote(t *testing.T) {
	v, _ := New(t.TempDir())
	_ = v.Write("x.md", "data", false)
	if err := v.Delete("x.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("x.md"); err == nil {
		t.Fatal("expected note to be gone")
	}
}

func TestMoveRelocatesNote(t *testing.T) {
	v, _ := New(t.TempDir())
	_ = v.Write("from.md", "body", false)
	if err := v.Move("from.md", "sub/to.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("from.md"); err == nil {
		t.Fatal("source should be gone")
	}
	got, _ := v.Read("sub/to.md")
	if got != "body" {
		t.Errorf("moved content = %q", got)
	}
}
