package httpapi

import (
	"context"
	"net/http"
	"time"
)

func healthLive(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func healthReady(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		db := d.Catalog.RawDB()
		if db == nil {
			WriteError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "database unavailable")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			WriteError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "database unavailable")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
