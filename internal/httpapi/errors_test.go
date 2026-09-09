package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"photo-browser/internal/httpapi"
)

func TestWriteErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, 403, httpapi.CodeForbidden, "access denied")
	if rec.Code != 403 {
		t.Fatalf("code=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type=%q", ct)
	}
	var body struct {
		Error struct {
			Code, Message string
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v body=%q", err, rec.Body.String())
	}
	if body.Error.Code != "forbidden" || body.Error.Message != "access denied" {
		t.Fatalf("body=%+v", body)
	}
	// Envelope must not expose internal keys.
	if strings.Contains(rec.Body.String(), "stack") ||
		strings.Contains(rec.Body.String(), "internal") &&
			body.Error.Code != "internal" {
		t.Fatalf("suspicious body: %q", rec.Body.String())
	}
}
