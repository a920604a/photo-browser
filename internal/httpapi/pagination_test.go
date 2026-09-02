package httpapi_test

import (
	"errors"
	"net/url"
	"testing"

	"photo-browser/internal/httpapi"
)

func mustParse(t *testing.T, raw string) httpapi.PageParams {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := httpapi.ParsePage(q)
	if err != nil {
		t.Fatalf("ParsePage(%q): %v", raw, err)
	}
	return p
}

func TestParsePageDefaultLimit(t *testing.T) {
	p := mustParse(t, "")
	if p.Limit != httpapi.DefaultLimit || p.Cursor != "" {
		t.Fatalf("p=%+v", p)
	}
}

func TestParsePageCustomLimit(t *testing.T) {
	p := mustParse(t, "limit=100&cursor=abc")
	if p.Limit != 100 || p.Cursor != "abc" {
		t.Fatalf("p=%+v", p)
	}
}

func TestParsePageClampsToMax(t *testing.T) {
	p := mustParse(t, "limit=9999")
	if p.Limit != httpapi.MaxLimit {
		t.Fatalf("limit=%d", p.Limit)
	}
}

func TestParsePageRejectsBadLimit(t *testing.T) {
	for _, raw := range []string{"limit=-1", "limit=0", "limit=abc"} {
		q, _ := url.ParseQuery(raw)
		if _, err := httpapi.ParsePage(q); !errors.Is(err, httpapi.ErrBadPagination) {
			t.Fatalf("%s: err=%v", raw, err)
		}
	}
}
