package httpapi

import (
	"fmt"
	"net/http"
	"path"
	"strings"
)

func photoThumbnail(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		photoID, ok := parsePathID(r, "photo_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid photo id")
			return
		}
		key := r.PathValue("thumbnail_key")
		if !isSafeThumbnailKey(key) {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid thumbnail key")
			return
		}
		p, found, err := d.Catalog.GetPhoto(r.Context(), photoID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if !found || !p.ThumbnailKey.Valid || p.ThumbnailKey.String != key {
			WriteError(w, http.StatusNotFound, CodeNotFound, "not found")
			return
		}
		redirect := path.Join(d.Config.InternalThumbnails, key+".webp")
		w.Header().Set("X-Accel-Redirect", redirect)
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
		w.Header().Set("ETag", fmt.Sprintf("%q", key))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "image/webp")
		w.WriteHeader(http.StatusOK)
	}
}

func photoOriginal(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		photoID, ok := parsePathID(r, "photo_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid photo id")
			return
		}
		p, found, err := d.Catalog.GetPhoto(r.Context(), photoID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "not found")
			return
		}
		if !isSafeRelativePath(p.RelativePath) {
			// Should never happen for a valid indexer entry; treat as 500 without leaking path.
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		redirect := d.Config.InternalOriginals
		if !strings.HasSuffix(redirect, "/") {
			redirect += "/"
		}
		redirect += escapePathSegments(p.RelativePath)
		w.Header().Set("X-Accel-Redirect", redirect)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("ETag", fmt.Sprintf(`"%d-%d"`, p.FileSize, p.FileMTimeNS))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if p.MIMEType != "" {
			w.Header().Set("Content-Type", p.MIMEType)
		}
		w.WriteHeader(http.StatusOK)
	}
}

func isSafeThumbnailKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}
	if strings.ContainsAny(key, "/\\\x00") {
		return false
	}
	if path.Base(key) != key {
		return false
	}
	return true
}

func isSafeRelativePath(p string) bool {
	if p == "" {
		return false
	}
	if strings.ContainsRune(p, '\x00') {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// escapePathSegments URL-escapes each segment individually so `/` separators
// stay intact for Nginx's alias directive to resolve.
func escapePathSegments(p string) string {
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = pathEscape(s)
	}
	return strings.Join(segments, "/")
}

// pathEscape percent-encodes a single path segment. We keep unicode letters
// as raw bytes so Nginx's UTF-8 alias handling still works, but escape reserved
// characters that would trip up header parsing.
func pathEscape(seg string) string {
	var b strings.Builder
	b.Grow(len(seg))
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch {
		case c == '?' || c == '#' || c == '%' || c == ' ' || c == '"' || c == '<' || c == '>' || c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
