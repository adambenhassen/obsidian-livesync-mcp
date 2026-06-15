package vault

import "testing"

func TestSearchFilename(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "Meeting Notes.md", "body", false)
	mustWrite(t, v, "groceries.md", "body", false)
	res, err := v.Search("meeting", "filename")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != "Meeting Notes.md" {
		t.Fatalf("filename search = %+v", res)
	}
}

func TestSearchContent(t *testing.T) {
	v := newTestVault(t)
	mustWrite(t, v, "a.md", "the quick brown fox", false)
	mustWrite(t, v, "b.md", "nothing here", false)
	res, err := v.Search("brown", "content")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != "a.md" {
		t.Fatalf("content search = %+v", res)
	}
}

func TestSearchInvalidMode(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Search("x", "regex"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
