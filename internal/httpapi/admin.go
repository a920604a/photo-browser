package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"photo-browser/internal/indexer"
	"photo-browser/internal/users"
)

type adminUserDTO struct {
	ID        int64  `json:"id"`
	UID       string `json:"firebase_uid,omitempty"`
	Email     string `json:"email,omitempty"`
	Role      string `json:"role"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toAdminUserDTO(u users.User) adminUserDTO {
	d := adminUserDTO{
		ID: u.ID, Role: u.Role, Enabled: u.Enabled,
		CreatedAt: u.CreatedAt.Format(time.RFC3339),
		UpdatedAt: u.UpdatedAt.Format(time.RFC3339),
	}
	if u.FirebaseUID.Valid {
		d.UID = u.FirebaseUID.String
	}
	if u.Email.Valid {
		d.Email = u.Email.String
	}
	return d
}

func listAdminUsers(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := d.Users.List(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		items := make([]adminUserDTO, len(list))
		for i, u := range list {
			items[i] = toAdminUserDTO(u)
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

func addAdminUser(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body struct {
			FirebaseUID string `json:"firebase_uid"`
			Email       string `json:"email"`
			Role        string `json:"role"`
		}
		if err := decodeJSON(r.Body, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid json")
			return
		}
		if body.FirebaseUID == "" && body.Email == "" {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "firebase_uid or email required")
			return
		}
		if body.Role == "" {
			body.Role = "member"
		}
		u := users.User{Role: body.Role, Enabled: true}
		if body.FirebaseUID != "" {
			u.FirebaseUID = sql.NullString{String: body.FirebaseUID, Valid: true}
		}
		if body.Email != "" {
			u.Email = sql.NullString{String: body.Email, Valid: true}
			u.NormalizedEmail = sql.NullString{String: users.NormalizeEmail(body.Email), Valid: true}
		}
		id, err := d.Users.Add(r.Context(), u, d.Now())
		switch {
		case errors.Is(err, users.ErrDuplicate):
			WriteError(w, http.StatusConflict, CodeConflict, "user already exists")
			return
		case errors.Is(err, users.ErrInvalidRole):
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid role")
			return
		case err != nil:
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"id": id})
	}
}

func patchAdminUser(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "user_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid user id")
			return
		}
		defer r.Body.Close()
		var body struct {
			Role    *string `json:"role"`
			Enabled *bool   `json:"enabled"`
		}
		if err := decodeJSON(r.Body, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid json")
			return
		}
		if body.Role == nil && body.Enabled == nil {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "role or enabled required")
			return
		}
		if body.Role != nil {
			if err := d.Users.UpdateRole(r.Context(), id, *body.Role, d.Now()); err != nil {
				switch {
				case errors.Is(err, users.ErrNotFound):
					WriteError(w, http.StatusNotFound, CodeNotFound, "user not found")
					return
				case errors.Is(err, users.ErrInvalidRole):
					WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid role")
					return
				default:
					WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
					return
				}
			}
		}
		if body.Enabled != nil {
			if err := d.Users.SetEnabled(r.Context(), id, *body.Enabled, d.Now()); err != nil {
				if errors.Is(err, users.ErrNotFound) {
					WriteError(w, http.StatusNotFound, CodeNotFound, "user not found")
					return
				}
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{"id": id})
	}
}

// startIndexRun reserves a new scan_run row synchronously so we can return the
// id in a 202, then executes the scan in a background goroutine. Concurrent
// admin requests are gated by a size-1 semaphore.
func startIndexRun(d RouterDeps, sem chan struct{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Indexer == nil {
			WriteError(w, http.StatusServiceUnavailable, CodeServiceUnavailable, "indexer unavailable")
			return
		}
		select {
		case sem <- struct{}{}:
		default:
			WriteError(w, http.StatusConflict, CodeConflict, "index already running")
			return
		}
		defer r.Body.Close()
		body := struct {
			RebuildThumbnails bool `json:"rebuild_thumbnails"`
		}{}
		_ = decodeJSON(r.Body, &body)

		scanID, err := d.Catalog.StartScan(r.Context(), d.Now())
		if err != nil {
			<-sem
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		go func() {
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_, _ = d.Indexer.RunWithScanID(ctx, scanID, indexer.Options{RebuildThumbnails: body.RebuildThumbnails})
		}()
		WriteJSON(w, http.StatusAccepted, map[string]any{"scan_id": scanID})
	}
}

func getIndexRun(d RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePathID(r, "scan_id")
		if !ok {
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid scan id")
			return
		}
		run, found, err := d.Catalog.GetScanRun(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if !found {
			WriteError(w, http.StatusNotFound, CodeNotFound, "scan not found")
			return
		}
		out := map[string]any{
			"id":         run.ID,
			"started_at": run.StartedAt,
			"status":     run.Status,
			"counts": map[string]int64{
				"seen":      run.Counts.Seen,
				"new":       run.Counts.New,
				"changed":   run.Counts.Changed,
				"unchanged": run.Counts.Unchanged,
				"removed":   run.Counts.Removed,
				"warnings":  run.Counts.Warnings,
				"errors":    run.Counts.Errors,
			},
		}
		if run.FinishedAt.Valid {
			out["finished_at"] = run.FinishedAt.String
		}
		if run.ErrorSummary.Valid {
			out["error_summary"] = run.ErrorSummary.String
		}
		WriteJSON(w, http.StatusOK, out)
	}
}

func decodeJSON(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// _ ensures strconv is retained if future refactors drop its explicit use.
var _ = strconv.ParseInt
