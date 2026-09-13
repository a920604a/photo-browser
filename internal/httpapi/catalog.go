package httpapi

import (
	"net/http"
	"strconv"

	"photo-browser/internal/catalog"
)

type categoryDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
}

type albumDTO struct {
	ID                int64  `json:"id"`
	CategoryID        int64  `json:"category_id"`
	Name              string `json:"name"`
	RelativePath      string `json:"relative_path"`
	CoverPhotoID      int64  `json:"cover_photo_id,omitempty"`
	CoverThumbnailKey string `json:"cover_thumbnail_key,omitempty"`
}

type photoDTO struct {
	ID           int64  `json:"id"`
	AlbumID      int64  `json:"album_id"`
	Filename     string `json:"filename"`
	RelativePath string `json:"relative_path"`
	MIMEType     string `json:"mime_type"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	TakenAt      string `json:"taken_at,omitempty"`
	ThumbnailKey string `json:"thumbnail_key,omitempty"`
	FileSize     int64  `json:"file_size"`
	FileMTimeNS  int64  `json:"file_mtime_ns"`
}

func toCategoryDTO(c catalog.Category) categoryDTO {
	return categoryDTO{ID: c.ID, Name: c.Name, RelativePath: c.RelativePath}
}

func toAlbumDTO(a catalog.Album) albumDTO {
	return albumDTO{ID: a.ID, CategoryID: a.CategoryID, Name: a.Name, RelativePath: a.RelativePath}
}

// withCover fills the cover fields so a client can build the album thumbnail
// URL (/media/photos/{cover_photo_id}/thumb) without a second request. A
// coverless album (or a lookup error) simply leaves the omitempty fields unset.
func withCover(r *http.Request, d RouterDeps, dto *albumDTO) {
	if pid, key, ok, err := d.Catalog.AlbumCover(r.Context(), dto.ID); err == nil && ok {
		dto.CoverPhotoID = pid
		dto.CoverThumbnailKey = key
	}
}

func toPhotoDTO(p catalog.Photo) photoDTO {
	d := photoDTO{
		ID: p.ID, AlbumID: p.AlbumID, Filename: p.Filename, RelativePath: p.RelativePath,
		MIMEType: p.MIMEType, Width: p.Width, Height: p.Height,
		FileSize: p.FileSize, FileMTimeNS: p.FileMTimeNS,
	}
	if p.TakenAt.Valid {
		d.TakenAt = p.TakenAt.String
	}
	if p.ThumbnailKey.Valid {
		d.ThumbnailKey = p.ThumbnailKey.String
	}
	return d
}

// httpCursorToStore converts the wire cursor into the storage cursor.
// Zero httpapi.Cursor (Version==0) means "first page".
func httpCursorToStore(c Cursor) catalog.PageCursor {
	if c.Version == 0 && c.Secondary == "" && c.ID == 0 {
		return catalog.PageCursor{IsFirst: true}
	}
	return catalog.PageCursor{Secondary: c.Secondary, ID: c.ID}
}

// storeCursorToHTTP produces the client-facing token, or "" when there are no more pages.
func storeCursorToHTTP(c catalog.PageCursor) string {
	if c == (catalog.PageCursor{}) {
		return ""
	}
	return EncodeCursor(Cursor{Version: cursorVersion, Secondary: c.Secondary, ID: c.ID})
}

func parsePathID(r *http.Request, name string) (int64, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func listCategories(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cats, err := d.Catalog.ListCategories(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]categoryDTO, len(cats))
		for i, c := range cats {
			items[i] = toCategoryDTO(c)
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func listCategoryAlbums(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "category_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid category id")
			return
		}
		if _, found, err := d.Catalog.GetCategory(r.Context(), id); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		} else if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "category not found")
			return
		}
		albums, err := d.Catalog.ListAlbumsByCategory(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]albumDTO, len(albums))
		for i, a := range albums {
			items[i] = toAlbumDTO(a)
			withCover(r, d, &items[i])
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func listAlbums(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := ParsePage(r.URL.Query())
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid pagination")
			return
		}
		cur, err := DecodeCursor(page.Cursor)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid cursor")
			return
		}
		albums, next, err := d.Catalog.ListAlbums(r.Context(), httpCursorToStore(cur), page.Limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]albumDTO, len(albums))
		for i, a := range albums {
			items[i] = toAlbumDTO(a)
			withCover(r, d, &items[i])
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": storeCursorToHTTP(next),
		})
	}
}

func getAlbum(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "album_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid album id")
			return
		}
		a, found, err := d.Catalog.GetAlbum(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "album not found")
			return
		}
		dto := toAlbumDTO(a)
		withCover(r, d, &dto)
		WriteJSON(w, http.StatusOK, dto)
	}
}

func listAlbumPhotos(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "album_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid album id")
			return
		}
		if _, found, err := d.Catalog.GetAlbum(r.Context(), id); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		} else if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "album not found")
			return
		}
		page, err := ParsePage(r.URL.Query())
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid pagination")
			return
		}
		cur, err := DecodeCursor(page.Cursor)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid cursor")
			return
		}
		photos, next, err := d.Catalog.ListAlbumPhotos(r.Context(), id, httpCursorToStore(cur), page.Limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]photoDTO, len(photos))
		for i, p := range photos {
			items[i] = toPhotoDTO(p)
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": storeCursorToHTTP(next),
		})
	}
}

func listPhotos(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := ParsePage(r.URL.Query())
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid pagination")
			return
		}
		cur, err := DecodeCursor(page.Cursor)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid cursor")
			return
		}
		photos, next, err := d.Catalog.ListPhotos(r.Context(), httpCursorToStore(cur), page.Limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]photoDTO, len(photos))
		for i, p := range photos {
			items[i] = toPhotoDTO(p)
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": storeCursorToHTTP(next),
		})
	}
}

func listTimeline(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, err := ParsePage(r.URL.Query())
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid pagination")
			return
		}
		cur, err := DecodeCursor(page.Cursor)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid cursor")
			return
		}
		photos, next, err := d.Catalog.ListTimeline(r.Context(), httpCursorToStore(cur), page.Limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]photoDTO, len(photos))
		for i, p := range photos {
			items[i] = toPhotoDTO(p)
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": storeCursorToHTTP(next),
		})
	}
}

func getPhoto(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "photo_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid photo id")
			return
		}
		p, found, err := d.Catalog.GetPhoto(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "photo not found")
			return
		}
		WriteJSON(w, http.StatusOK, toPhotoDTO(p))
	}
}
