package vault

import "testing"

// newTestVault creates a Vault rooted at a fresh temp dir, failing the test on
// error. Keeps the table-style tests free of repeated error plumbing.
func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// mustWrite writes a note, failing the test on error.
func mustWrite(t *testing.T, v *Vault, path, content string, overwrite bool) {
	t.Helper()
	if err := v.Write(path, content, overwrite); err != nil {
		t.Fatal(err)
	}
}

// mustRead reads a note, failing the test on error.
func mustRead(t *testing.T, v *Vault, path string) string {
	t.Helper()
	got, err := v.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
