package httpapi

import (
	"errors"
	"net/url"
	"strconv"
)

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// PageParams captures the parsed limit + cursor from query string. Callers
// must decode the cursor separately with DecodeCursor.
type PageParams struct {
	Limit  int
	Cursor string
}

// ErrBadPagination indicates that a query-string limit was unparseable or
// non-positive. Callers should surface 400.
var ErrBadPagination = errors.New("httpapi: bad pagination")

// ParsePage reads `limit` and `cursor` from the query string. Missing limit
// defaults to DefaultLimit; values above MaxLimit are clamped down.
func ParsePage(q url.Values) (PageParams, error) {
	p := PageParams{Limit: DefaultLimit, Cursor: q.Get("cursor")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return PageParams{}, ErrBadPagination
		}
		if n > MaxLimit {
			n = MaxLimit
		}
		p.Limit = n
	}
	return p, nil
}
