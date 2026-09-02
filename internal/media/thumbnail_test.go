package media

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeVips(t *testing.T, dir string, fail bool) (script, argvLog string) {
	t.Helper()
	argvLog = filepath.Join(dir, "argv.log")
	script = filepath.Join(dir, "fake-vips.sh")
	body := "#!/bin/bash\n" +
		"echo \"$@\" >> \"" + argvLog + "\"\n" +
		"prev=\"\"; out=\"\"\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--output\" ]; then out=\"$a\"; fi\n" +
		"  prev=\"$a\"\n" +
		"done\n"
	if fail {
		body += "exit 7\n"
	} else {
		body += "path=\"${out%%\\[*}\"\n" +
			"printf 'fake-webp' > \"$path\"\n"
	}
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, argvLog
}

func TestThumbnailGenerateSuccess(t *testing.T) {
	work := t.TempDir()
	thumbsDir := filepath.Join(work, "thumbs")
	script, argvLog := writeFakeVips(t, work, false)

	source := filepath.Join(work, "src.jpg")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	th := &Thumbnailer{Dir: thumbsDir, Command: script}
	if err := th.Generate(context.Background(), source, "42-100-999", 0); err != nil {
		t.Fatal(err)
	}

	final := filepath.Join(thumbsDir, "42-100-999.webp")
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("final missing: %v", err)
	}

	// No leftover temp files
	entries, err := os.ReadDir(thumbsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".thumb-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}

	logBytes, err := os.ReadFile(argvLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "--size 512x512") {
		t.Fatalf("missing size arg: %s", log)
	}
	if !strings.Contains(log, "Q=80") {
		t.Fatalf("missing quality: %s", log)
	}
	if !strings.Contains(log, "strip") {
		t.Fatalf("missing strip: %s", log)
	}
	if !strings.Contains(log, source) {
		t.Fatalf("missing source: %s", log)
	}
	// vipsthumbnail --output must have pointed at a temp path in thumbsDir,
	// not directly at the final name.
	if strings.Contains(log, "42-100-999.webp") {
		t.Fatalf("output must be temp, not final: %s", log)
	}
	if !strings.Contains(log, ".thumb-") {
		t.Fatalf("output path missing temp prefix: %s", log)
	}
}

func TestThumbnailGenerateFailureLeavesNothing(t *testing.T) {
	work := t.TempDir()
	thumbsDir := filepath.Join(work, "thumbs")
	script, _ := writeFakeVips(t, work, true)

	source := filepath.Join(work, "src.jpg")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	th := &Thumbnailer{Dir: thumbsDir, Command: script}
	err := th.Generate(context.Background(), source, "1-2-3", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(filepath.Join(thumbsDir, "1-2-3.webp")); statErr == nil {
		t.Fatal("final file should not exist on failure")
	}
	entries, err := os.ReadDir(thumbsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".thumb-") {
			t.Fatalf("leftover temp file after failure: %s", e.Name())
		}
	}
}

func TestThumbnailInvalidKey(t *testing.T) {
	th := &Thumbnailer{Dir: t.TempDir(), Command: "/bin/false"}
	for _, key := range []string{"", "..", ".", "a/b", "../evil", "/abs"} {
		if err := th.Generate(context.Background(), "unused", key, 0); err == nil {
			t.Fatalf("expected error for key %q", key)
		}
	}
}

// Real vipsthumbnail smoke test — only runs when the binary is on PATH.
func TestThumbnailRealVips(t *testing.T) {
	if _, err := os.Stat("/usr/bin/vipsthumbnail"); err != nil {
		t.Skip("vipsthumbnail not installed")
	}
	work := t.TempDir()
	src := filepath.Join(work, "big.png")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, makeImage(800, 600)); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	th := NewThumbnailer(filepath.Join(work, "thumbs"))
	if err := th.Generate(context.Background(), src, "real-1", 800); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(work, "thumbs", "real-1.webp")
	st, err := os.Stat(final)
	if err != nil {
		t.Fatalf("final missing: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("empty webp")
	}
}
