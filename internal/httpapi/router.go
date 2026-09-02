package httpapi

import (
	"net/http"
	"time"

	"photo-browser/internal/auth"
	"photo-browser/internal/catalog"
	"photo-browser/internal/config"
	"photo-browser/internal/indexer"
	"photo-browser/internal/users"
)

// RouterDeps holds the concrete dependencies each handler closure needs.
// Fields are populated by the caller; nothing is inferred from env.
type RouterDeps struct {
	Config   config.Config
	Users    *users.Store
	Catalog  *catalog.Store
	Verifier *auth.Verifier
	Indexer  *indexer.Indexer
	Now      func() time.Time
}

// NewRouter wires the ServeMux, middleware chain, and handlers. It is safe
// to call multiple times per process (tests do); each router owns its own mux.
func NewRouter(d RouterDeps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health/live", healthLive)
	mux.HandleFunc("GET /api/v1/health/ready", healthReady(d))

	authMW := func(next http.Handler) http.Handler {
		return WithAuth(d.Verifier, d.Users, next)
	}

	mux.Handle("GET /api/v1/me", authMW(http.HandlerFunc(meHandler)))

	mux.Handle("GET /api/v1/categories", authMW(listCategories(d)))
	mux.Handle("GET /api/v1/categories/{category_id}/albums", authMW(listCategoryAlbums(d)))
	mux.Handle("GET /api/v1/albums", authMW(listAlbums(d)))
	mux.Handle("GET /api/v1/albums/{album_id}", authMW(getAlbum(d)))
	mux.Handle("GET /api/v1/albums/{album_id}/photos", authMW(listAlbumPhotos(d)))
	mux.Handle("GET /api/v1/photos", authMW(listPhotos(d)))
	mux.Handle("GET /api/v1/photos/timeline", authMW(listTimeline(d)))
	mux.Handle("GET /api/v1/photos/{photo_id}", authMW(getPhoto(d)))
	mux.Handle("GET /api/v1/photos/{photo_id}/thumbnail/{thumbnail_key}", authMW(photoThumbnail(d)))
	mux.Handle("GET /api/v1/photos/{photo_id}/original", authMW(photoOriginal(d)))

	var h http.Handler = mux
	h = WithCORS(d.Config.AllowedOrigins, h)
	h = WithRequestID(h)
	return h
}
