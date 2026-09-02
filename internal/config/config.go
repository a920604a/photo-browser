package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
