package media

import (
	"database/sql"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	_ "golang.org/x/image/webp"
)

type Metadata struct {
	MIMEType      string
	Width, Height int
	TakenAt       sql.NullString
}

func ReadMetadata(path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return Metadata{}, fmt.Errorf("decode %q: %w", path, err)
	}
	mime := formatMIME(format)
	if mime == "" {
		return Metadata{}, fmt.Errorf("unsupported format %q", format)
	}
	meta := Metadata{
		MIMEType: mime,
		Width:    cfg.Width,
		Height:   cfg.Height,
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return meta, nil
	}
	x, err := exif.Decode(f)
	if err != nil {
		return meta, nil
	}
	tag, err := x.Get(exif.DateTimeOriginal)
	if err != nil {
		return meta, nil
	}
	s, err := tag.StringVal()
	if err != nil {
		return meta, nil
	}
	meta.TakenAt = parseEXIFDate(s)
	return meta, nil
}

func formatMIME(format string) string {
	switch format {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return ""
	}
}

func parseEXIFDate(v string) sql.NullString {
	t, err := time.Parse("2006:01:02 15:04:05", strings.TrimSpace(v))
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format("2006-01-02T15:04:05"), Valid: true}
}
