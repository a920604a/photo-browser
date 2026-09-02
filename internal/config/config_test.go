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
		if _, err := Load(); err == nil {
			t.Fatalf("Load(%q, %q, %q) succeeded", tc.photos, tc.data, tc.thumbs)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PHOTO_ROOT", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("THUMBNAIL_DIR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PhotoRoot != "/photos" || cfg.DataDir != "/data" || cfg.ThumbnailDir != "/thumbnails" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}
