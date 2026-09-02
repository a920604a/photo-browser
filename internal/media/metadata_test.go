package media

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// A minimal 1x1 lossy WebP file (from libwebp cwebp -q 80).
const tinyWebPBase64 = "UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA"

func writeImage(t *testing.T, path string, img image.Image, encode func(w *os.File, img image.Image) error) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func makeImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func TestReadMetadataJPEG(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.jpg")
	writeImage(t, p, makeImage(120, 80), func(f *os.File, img image.Image) error {
		return jpeg.Encode(f, img, &jpeg.Options{Quality: 80})
	})
	m, err := ReadMetadata(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.MIMEType != "image/jpeg" || m.Width != 120 || m.Height != 80 {
		t.Fatalf("meta = %+v", m)
	}
	if m.TakenAt.Valid {
		t.Fatalf("expected no EXIF, got %v", m.TakenAt)
	}
}

func TestReadMetadataPNG(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	writeImage(t, p, makeImage(50, 40), func(f *os.File, img image.Image) error {
		return png.Encode(f, img)
	})
	m, err := ReadMetadata(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.MIMEType != "image/png" || m.Width != 50 || m.Height != 40 {
		t.Fatalf("meta = %+v", m)
	}
	if m.TakenAt.Valid {
		t.Fatalf("expected no EXIF")
	}
}

func TestReadMetadataWebP(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(tinyWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "a.webp")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMetadata(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.MIMEType != "image/webp" {
		t.Fatalf("mime %q", m.MIMEType)
	}
	if m.Width != 1 || m.Height != 1 {
		t.Fatalf("dims %d,%d", m.Width, m.Height)
	}
}

func TestReadMetadataInvalid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.jpg")
	if err := os.WriteFile(p, bytes.Repeat([]byte{0xFF}, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadMetadata(p)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestParseEXIFDate(t *testing.T) {
	cases := []struct {
		in    string
		valid bool
		out   string
	}{
		{"2017:06:27 14:03:02", true, "2017-06-27T14:03:02"},
		{"  2020:01:02 03:04:05  ", true, "2020-01-02T03:04:05"},
		{"", false, ""},
		{"not a date", false, ""},
		{"2017-06-27 14:03:02", false, ""},
	}
	for _, tc := range cases {
		got := parseEXIFDate(tc.in)
		if got.Valid != tc.valid {
			t.Fatalf("parseEXIFDate(%q).Valid = %v want %v", tc.in, got.Valid, tc.valid)
		}
		if got.Valid && got.String != tc.out {
			t.Fatalf("parseEXIFDate(%q) = %q want %q", tc.in, got.String, tc.out)
		}
	}
}
