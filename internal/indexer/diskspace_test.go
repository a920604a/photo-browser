package indexer

import "testing"

func TestFreeBytesReportsSomethingForTempDir(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("free bytes = 0 for a writable temp dir")
	}
}

func TestFreeBytesFailsForMissingPath(t *testing.T) {
	if _, err := FreeBytes("/definitely/not/a/path"); err == nil {
		t.Fatal("expected an error for a missing path")
	}
}
