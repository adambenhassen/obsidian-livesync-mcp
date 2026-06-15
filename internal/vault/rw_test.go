package vault

import (
	"errors"
	"testing"
)

func TestWriteThenRead(t *testing.T) {
	v, _ := New(t.TempDir())
	if err := v.Write("Notes/hello.md", "# Hi", false); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := v.Read("Notes/hello.md")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != "# Hi" {
		t.Errorf("Read() = %q, want %q", got, "# Hi")
	}
}

func TestWriteNoOverwriteFailsIfExists(t *testing.T) {
	v, _ := New(t.TempDir())
	_ = v.Write("a.md", "one", false)
	err := v.Write("a.md", "two", false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Write() error = %v, want ErrExists", err)
	}
}

func TestReadMissingReturnsNotExist(t *testing.T) {
	v, _ := New(t.TempDir())
	if _, err := v.Read("nope.md"); err == nil {
		t.Fatal("expected error reading missing note")
	}
}
