package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	PhotoRoot, DataDir, ThumbnailDir string
}

func Load() (Config, error) {
	c := Config{
		PhotoRoot:    value("PHOTO_ROOT", "/photos"),
		DataDir:      value("DATA_DIR", "/data"),
		ThumbnailDir: value("THUMBNAIL_DIR", "/thumbnails"),
	}
	for name, path := range map[string]string{
		"PHOTO_ROOT":    c.PhotoRoot,
		"DATA_DIR":      c.DataDir,
		"THUMBNAIL_DIR": c.ThumbnailDir,
	} {
		if !filepath.IsAbs(path) {
			return Config{}, fmt.Errorf("%s must be absolute", name)
		}
	}
	return c, nil
}

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
