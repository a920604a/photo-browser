package httpapi

import "net/http"

func meHandler(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing user context")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"uid":   u.UID,
		"email": u.Email,
		"role":  u.Role,
	})
}
