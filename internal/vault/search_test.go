package vault

import "testing"

func TestSearchFilename(t *testing.T) {
	v, _ := New(t.TempDir())
	_ = v.Write("Meeting Notes.md", "body", false)
	_ = v.Write("groceries.md", "body", false)
	res, err := v.Search("meeting", "filename")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != "Meeting Notes.md" {
		t.Fatalf("filename search = %+v", res)
	}
}

func TestSearchContent(t *testing.T) {
	v, _ := New(t.TempDir())
	_ = v.Write("a.md", "the quick brown fox", false)
	_ = v.Write("b.md", "nothing here", false)
	res, err := v.Search("brown", "content")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != "a.md" {
		t.Fatalf("content search = %+v", res)
	}
}

func TestSearchInvalidMode(t *testing.T) {
	v, _ := New(t.TempDir())
	if _, err := v.Search("x", "regex"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
