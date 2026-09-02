package httpapi_test

import (
	"encoding/base64"
	"errors"
	"testing"

	"photo-browser/internal/httpapi"
)

func TestCursorEmptyDecodesToZero(t *testing.T) {
	c, err := httpapi.DecodeCursor("")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if c != (httpapi.Cursor{}) {
		t.Fatalf("expected zero cursor, got %+v", c)
	}
}

func TestCursorRoundTripWithSecondary(t *testing.T) {
	in := httpapi.Cursor{Version: 1, Secondary: "2024-01-01T00:00:00", ID: 42}
	got, err := httpapi.DecodeCursor(httpapi.EncodeCursor(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != in {
		t.Fatalf("round-trip mismatch: in=%+v got=%+v", in, got)
	}
}

func TestCursorRoundTripWithoutSecondary(t *testing.T) {
	in := httpapi.Cursor{Version: 1, ID: -7}
	got, err := httpapi.DecodeCursor(httpapi.EncodeCursor(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != in {
		t.Fatalf("round-trip mismatch: in=%+v got=%+v", in, got)
	}
}

func TestCursorRejectsUnknownVersion(t *testing.T) {
	bad := base64.RawURLEncoding.EncodeToString(make([]byte, 29))
	// zero version explicitly
	_, err := httpapi.DecodeCursor(bad)
	if !errors.Is(err, httpapi.ErrBadCursor) {
		t.Fatalf("err=%v want ErrBadCursor", err)
	}
}

func TestCursorRejectsGarbage(t *testing.T) {
	if _, err := httpapi.DecodeCursor("!!!"); !errors.Is(err, httpapi.ErrBadCursor) {
		t.Fatalf("err=%v want ErrBadCursor", err)
	}
}

func TestCursorRejectsShortPayload(t *testing.T) {
	short := base64.RawURLEncoding.EncodeToString([]byte{1, 0})
	if _, err := httpapi.DecodeCursor(short); !errors.Is(err, httpapi.ErrBadCursor) {
		t.Fatalf("err=%v want ErrBadCursor", err)
	}
}
