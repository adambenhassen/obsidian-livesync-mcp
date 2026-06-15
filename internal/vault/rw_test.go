package vault

import (
	"errors"
	"testing"
)

func TestWriteThenRead(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "Notes/hello.md", "# Hi", false)
	if got := mustRead(t, v, "Notes/hello.md"); got != "# Hi" {
		t.Errorf("Read() = %q, want %q", got, "# Hi")
	}
}

func TestWriteNoOverwriteFailsIfExists(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a.md", "one", false)
	err := v.Write("a.md", "two", false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Write() error = %v, want ErrExists", err)
	}
}

func TestReadMissingReturnsNotExist(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Read("nope.md"); err == nil {
		t.Fatal("expected error reading missing note")
	}
}
