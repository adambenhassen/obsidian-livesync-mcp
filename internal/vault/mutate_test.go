package vault

import (
	"errors"
	"testing"
)

func TestAppendCreatesAndAppends(t *testing.T) {
	v := newTestVault(t)
	if err := v.Append("log.md", "line1\n"); err != nil {
		t.Fatal(err)
	}
	if err := v.Append("log.md", "line2\n"); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, v, "log.md"); got != "line1\nline2\n" {
		t.Errorf("Read() = %q", got)
	}
}

func TestDeleteRemovesNote(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "x.md", "data", false)
	if err := v.Delete("x.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("x.md"); err == nil {
		t.Fatal("expected note to be gone")
	}
}

func TestMoveRelocatesNote(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "from.md", "body", false)
	if err := v.Move("from.md", "sub/to.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Read("from.md"); err == nil {
		t.Fatal("source should be gone")
	}
	if got := mustRead(t, v, "sub/to.md"); got != "body" {
		t.Errorf("moved content = %q", got)
	}
}

func TestMoveRefusesToClobberExisting(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "src.md", "source", false)
	mustWrite(t, v, "dst.md", "precious destination", false)

	err := v.Move("src.md", "dst.md")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Move onto existing note = %v, want ErrExists", err)
	}
	// Both notes must be intact (no data lost).
	if got := mustRead(t, v, "dst.md"); got != "precious destination" {
		t.Errorf("destination was clobbered: %q", got)
	}
	if got := mustRead(t, v, "src.md"); got != "source" {
		t.Errorf("source should be untouched after refused move: %q", got)
	}
}
