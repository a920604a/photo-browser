package httpapi

import (
	"encoding/json"
	"net/http"
)

type ErrorCode string

const (
	CodeBadRequest         ErrorCode = "bad_request"
	CodeUnauthorized       ErrorCode = "unauthorized"
	CodeForbidden          ErrorCode = "forbidden"
	CodeNotFound           ErrorCode = "not_found"
	CodeConflict           ErrorCode = "conflict"
	CodeInternal           ErrorCode = "internal"
	CodeServiceUnavailable ErrorCode = "service_unavailable"
)

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// WriteError renders a fixed JSON envelope. Callers must NOT pass SQL, stack,
// or filesystem paths in message — only safe, opaque wording.
func WriteError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Every JSON response is scoped to one authenticated user, so it must never
	// land in a shared cache — Cloudflare's included.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// WriteJSON serializes v as JSON with the given status. Internal only.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
