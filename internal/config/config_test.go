package config

import "testing"

func TestLoadRequiresAbsolutePaths(t *testing.T) {
	for _, tc := range []struct{ photos, data, thumbs string }{
		{"photos", "/data", "/thumbs"},
		{"/photos", "data", "/thumbs"},
		{"/photos", "/data", "thumbs"},
	} {
		t.Setenv("PHOTO_ROOT", tc.photos)
		t.Setenv("DATA_DIR", tc.data)
		t.Setenv("THUMBNAIL_DIR", tc.thumbs)
		t.Setenv("ALLOWED_ORIGINS", "")
		if _, err := Load(); err == nil {
			t.Fatalf("Load(%q, %q, %q) succeeded", tc.photos, tc.data, tc.thumbs)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("THUMBNAIL_DIR", "")
	t.Setenv("HTTP_LISTEN", "")
	t.Setenv("FIREBASE_PROJECT_ID", "")
	t.Setenv("FIREBASE_ISSUER", "")
	t.Setenv("FIREBASE_JWKS_URL", "")
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("INTERNAL_MEDIA_ORIGINALS", "")
	t.Setenv("INTERNAL_MEDIA_THUMBNAILS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhotoRoot != "/photos" || cfg.DataDir != "/data" || cfg.ThumbnailDir != "/thumbnails" {
		t.Fatalf("unexpected paths: %+v", cfg)
	}
	if cfg.HTTPListen != ":8080" {
		t.Fatalf("HTTPListen=%q", cfg.HTTPListen)
	}
	if cfg.FirebaseJWKSURL == "" {
		t.Fatal("FirebaseJWKSURL default empty")
	}
	if cfg.InternalOriginals != "/internal-media/originals" ||
		cfg.InternalThumbnails != "/internal-media/thumbnails" {
		t.Fatalf("internal paths=%+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("AllowedOrigins default nonempty: %v", cfg.AllowedOrigins)
	}
}

func TestLoadDerivesFirebaseIssuer(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "/photos")
	t.Setenv("DATA_DIR", "/data")
	t.Setenv("THUMBNAIL_DIR", "/thumbnails")
	t.Setenv("FIREBASE_PROJECT_ID", "demo-proj")
	t.Setenv("FIREBASE_ISSUER", "")
	t.Setenv("ALLOWED_ORIGINS", "https://photos.example.com , https://alt.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FirebaseIssuer != "https://securetoken.google.com/demo-proj" {
		t.Fatalf("issuer=%q", cfg.FirebaseIssuer)
	}
	if len(cfg.AllowedOrigins) != 2 ||
		cfg.AllowedOrigins[0] != "https://photos.example.com" ||
		cfg.AllowedOrigins[1] != "https://alt.example.com" {
		t.Fatalf("origins=%v", cfg.AllowedOrigins)
	}
}

func TestLoadRejectsWildcardOrigin(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "/photos")
	t.Setenv("DATA_DIR", "/data")
	t.Setenv("THUMBNAIL_DIR", "/thumbnails")
	t.Setenv("FIREBASE_PROJECT_ID", "demo-proj")
	t.Setenv("ALLOWED_ORIGINS", "*")
	if _, err := Load(); err == nil {
		t.Fatal("wildcard origin must be rejected")
	}
}

func TestLoadRespectsExplicitIssuerOverride(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "/photos")
	t.Setenv("DATA_DIR", "/data")
	t.Setenv("THUMBNAIL_DIR", "/thumbnails")
	t.Setenv("FIREBASE_PROJECT_ID", "demo-proj")
	t.Setenv("FIREBASE_ISSUER", "https://issuer.override.example")
	t.Setenv("ALLOWED_ORIGINS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FirebaseIssuer != "https://issuer.override.example" {
		t.Fatalf("issuer override lost: %q", cfg.FirebaseIssuer)
	}
}

func TestThumbnailMinFreeBytesDefaultsAndParses(t *testing.T) {
	const def = 2 * 1024 * 1024 * 1024

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ThumbnailMinFreeBytes != def {
		t.Fatalf("default=%d want %d", c.ThumbnailMinFreeBytes, def)
	}

	t.Setenv("THUMBNAIL_MIN_FREE_BYTES", "0")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	} else if c.ThumbnailMinFreeBytes != 0 {
		t.Fatalf("explicit 0 should disable the check, got %d", c.ThumbnailMinFreeBytes)
	}

	t.Setenv("THUMBNAIL_MIN_FREE_BYTES", "not-a-number")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	} else if c.ThumbnailMinFreeBytes != def {
		t.Fatalf("garbage should fall back to the default, got %d", c.ThumbnailMinFreeBytes)
	}

	t.Setenv("THUMBNAIL_MIN_FREE_BYTES", "1073741824")
	if c, err = Load(); err != nil {
		t.Fatal(err)
	} else if c.ThumbnailMinFreeBytes != 1073741824 {
		t.Fatalf("explicit value not parsed, got %d", c.ThumbnailMinFreeBytes)
	}
}
