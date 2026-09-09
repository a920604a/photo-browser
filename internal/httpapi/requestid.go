package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"regexp"
)

type requestIDKey struct{}

var reqIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// WithRequestID assigns each request an ID: reuses X-Request-ID from client if it
// matches the safe pattern, else generates a fresh 128-bit base64url token.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !reqIDPattern.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the id assigned by WithRequestID, "" if unset.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
