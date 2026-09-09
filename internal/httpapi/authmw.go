package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"photo-browser/internal/auth"
	"photo-browser/internal/users"
)

type ctxKey int

const ctxUserKey ctxKey = 1

// AuthUser is what handlers see after WithAuth accepts a request.
type AuthUser struct {
	ID    int64
	UID   string
	Email string
	Role  string
}

// UserFromContext returns the request's authenticated user, if any.
func UserFromContext(ctx context.Context) (AuthUser, bool) {
	v, ok := ctx.Value(ctxUserKey).(AuthUser)
	return v, ok
}

// WithAuth verifies the Firebase ID token then intersects the token identity
// with the local allowlist. Both stages fail closed.
func WithAuth(v *auth.Verifier, us *users.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") {
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing bearer token")
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
		if raw == "" {
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing bearer token")
			return
		}
		claims, err := v.Verify(r.Context(), raw)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid token")
			return
		}

		var u users.User
		var found bool
		if claims.UID != "" {
			u, found, err = us.LookupByUID(r.Context(), claims.UID)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
		}
		if !found && claims.EmailVerified && claims.Email != "" {
			u, found, err = us.LookupByEmail(r.Context(), claims.Email)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
		}
		if !found || !u.Enabled {
			WriteError(w, http.StatusForbidden, CodeForbidden, "access denied")
			return
		}
		au := AuthUser{ID: u.ID, UID: claims.UID, Email: claims.Email, Role: u.Role}
		ctx := context.WithValue(r.Context(), ctxUserKey, au)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireRole gates the next handler on the authenticated user's role.
func RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok || u.Role != role {
			WriteError(w, http.StatusForbidden, CodeForbidden, "access denied")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ErrAuthUnexpected surfaces internal wiring failures if middleware is misused
// in tests. Not returned by exported functions today.
var ErrAuthUnexpected = errors.New("httpapi.WithAuth: unexpected state")
