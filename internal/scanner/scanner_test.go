package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func mkfile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWalkAcceptsAndWarnsAndOrders(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "旅遊/日本/a.jpg")
	mkfile(t, root, "旅遊/日本/B.WEBP")
	mkfile(t, root, "旅遊/日本/deeper/c.jpg")
	mkfile(t, root, "旅遊/日本/note.txt")
	mkfile(t, root, "旅遊/root.jpg")
	mkfile(t, root, "root.jpg")
	mkdir(t, root, ".hidden/album")
	mkfile(t, root, ".hidden/album/a.jpg")
	mkfile(t, root, "旅遊/.hidden/a.jpg")

	var warns []Warning
	entries, err := Walk(root, func(w Warning) { warns = append(warns, w) })
	if err != nil {
		t.Fatal(err)
	}

	var rels []string
	for _, e := range entries {
		rels = append(rels, e.RelativePath)
	}
	want := []string{"旅遊/日本/B.WEBP", "旅遊/日本/a.jpg"}
	if !equalStrings(rels, want) {
		t.Fatalf("entries = %v want %v", rels, want)
	}

	if !sort.StringsAreSorted(rels) {
		t.Fatalf("entries not sorted: %v", rels)
	}

	counts := map[string]int{}
	for _, w := range warns {
		counts[w.Code]++
	}
	for code, min := range map[string]int{
		CodeRootFile:     1,
		CodeCategoryFile: 1,
		CodeTooDeep:      1,
		CodeUnsupported:  1,
	} {
		if counts[code] < min {
			t.Fatalf("warning %q count %d < %d (all warns: %+v)", code, counts[code], min, warns)
		}
	}

	for _, e := range entries {
		if e.Size == 0 {
			t.Fatalf("entry %q missing size", e.RelativePath)
		}
		if e.MTimeNS == 0 {
			t.Fatalf("entry %q missing mtime", e.RelativePath)
		}
	}
}

func TestWalkSymlinkNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on Windows")
	}
	root := t.TempDir()
	mkfile(t, root, "旅遊/日本/real.jpg")

	target := filepath.Join(root, "旅遊", "日本", "real.jpg")
	link := filepath.Join(root, "旅遊", "日本", "link.jpg")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not permitted: %v", err)
	}
	dirLinkTarget := filepath.Join(root, "旅遊", "日本")
	dirLink := filepath.Join(root, "旅遊", "linked_album")
	if err := os.Symlink(dirLinkTarget, dirLink); err != nil {
		t.Skipf("dir symlink not permitted: %v", err)
	}
	catLink := filepath.Join(root, "linked_category")
	if err := os.Symlink(filepath.Join(root, "旅遊"), catLink); err != nil {
		t.Skipf("cat symlink not permitted: %v", err)
	}

	var warns []Warning
	entries, err := Walk(root, func(w Warning) { warns = append(warns, w) })
	if err != nil {
		t.Fatal(err)
	}

	// Only the real file should be indexed; symlinks emit warnings.
	if len(entries) != 1 || entries[0].Filename != "real.jpg" {
		t.Fatalf("entries=%+v", entries)
	}
	var symlinkWarns int
	for _, w := range warns {
		if w.Code == CodeSymlink {
			symlinkWarns++
		}
	}
	if symlinkWarns < 3 {
		t.Fatalf("expected >=3 symlink warnings, got %d: %+v", symlinkWarns, warns)
	}
}

func TestWalkRootMissing(t *testing.T) {
	_, err := Walk(filepath.Join(t.TempDir(), "nope"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWalkAlbumUnreadableFailsHard(t *testing.T) {
	root := t.TempDir()
	mkfile(t, root, "旅遊/日本/a.jpg")

	sentinel := errors.New("permission denied")
	inject := func(path string) ([]os.DirEntry, error) {
		if filepath.Base(path) == "日本" {
			return nil, sentinel
		}
		return os.ReadDir(path)
	}
	_, err := walkWith(root, nil, inject)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error not wrapped: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
