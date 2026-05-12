package receipts

import "testing"

func TestSourceFingerprint(t *testing.T) {
	pages := []string{" AAA ", "bbb"}
	got := SourceFingerprint(pages)
	if got == "" {
		t.Fatalf("SourceFingerprint returned blank")
	}
	if got != SourceFingerprint([]string{"aaa", "BBB"}) {
		t.Fatalf("SourceFingerprint should trim and lowercase page hashes")
	}
	if got == SourceFingerprint([]string{"bbb", "aaa"}) {
		t.Fatalf("SourceFingerprint should be page-order sensitive")
	}
	if SourceFingerprint(nil) != "" {
		t.Fatalf("SourceFingerprint(nil) should be blank")
	}
}
