package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	PhotoRoot          string
	DataDir            string
	ThumbnailDir       string
	HTTPListen         string
	FirebaseProjectID  string
	FirebaseIssuer     string
	FirebaseJWKSURL    string
	AllowedOrigins     []string
	InternalOriginals  string
	InternalThumbnails string

	// Below this much free space on the thumbnail volume, an index run stops
	// generating thumbnails. Zero disables the check.
	ThumbnailMinFreeBytes int64
}

const defaultJWKS = "https://www.googleapis.com/service_accounts/v1/jwks/securetoken@system.gserviceaccount.com"

func Load() (Config, error) {
	c := Config{
		PhotoRoot:          value("PHOTO_ROOT", "/photos"),
		DataDir:            value("DATA_DIR", "/data"),
		ThumbnailDir:       value("THUMBNAIL_DIR", "/thumbnails"),
		HTTPListen:         value("HTTP_LISTEN", ":8080"),
		FirebaseProjectID:  value("FIREBASE_PROJECT_ID", ""),
		FirebaseIssuer:     value("FIREBASE_ISSUER", ""),
		FirebaseJWKSURL:    value("FIREBASE_JWKS_URL", defaultJWKS),
		InternalOriginals:  value("INTERNAL_MEDIA_ORIGINALS", "/internal-media/originals"),
		InternalThumbnails: value("INTERNAL_MEDIA_THUMBNAILS", "/internal-media/thumbnails"),
		// 2 GiB leaves SQLite and its WAL room to breathe on a volume shared
		// with the originals.
		ThumbnailMinFreeBytes: intValue("THUMBNAIL_MIN_FREE_BYTES", 2*1024*1024*1024),
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
	if c.FirebaseProjectID != "" && c.FirebaseIssuer == "" {
		c.FirebaseIssuer = "https://securetoken.google.com/" + c.FirebaseProjectID
	}
	raw := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS"))
	if raw != "" {
		for _, o := range strings.Split(raw, ",") {
			o = strings.TrimSpace(o)
			if o == "" {
				continue
			}
			if o == "*" {
				return Config{}, errors.New("ALLOWED_ORIGINS must not be *")
			}
			c.AllowedOrigins = append(c.AllowedOrigins, o)
		}
	}
	return c, nil
}

func intValue(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
