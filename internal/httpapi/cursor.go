package httpapi

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

// Cursor is the opaque pagination token exposed to clients. All list endpoints
// use the same wire format even when a particular query only fills a subset
// of the fields (e.g. album lists leave Secondary empty).
//
// Wire format (bytes):
//
//	[0]        version, currently always 1
//	[1]        secondary present (1) or absent (0)
//	[2..21]    fixed 19-byte ASCII secondary value, zero-padded (unused when absent)
//	[22..29]   int64 id, big-endian, sign bit copied as-is
//
// The empty string decodes to the zero Cursor (start of stream).
type Cursor struct {
	Version   byte
	Secondary string // e.g. "2024-01-01T00:00:00" or empty when absent
	ID        int64
}

// ErrBadCursor is returned by DecodeCursor when the wire form is malformed
// or advertises an unsupported version.
var ErrBadCursor = errors.New("httpapi: bad cursor")

const (
	cursorSecondaryLen = 19
	cursorWireLen      = 1 + 1 + cursorSecondaryLen + 8
	cursorVersion      = 1
)

// EncodeCursor produces the opaque token. Callers must set Version=1.
func EncodeCursor(c Cursor) string {
	buf := make([]byte, cursorWireLen)
	buf[0] = c.Version
	if c.Secondary != "" {
		buf[1] = 1
		padded := make([]byte, cursorSecondaryLen)
		copy(padded, c.Secondary)
		copy(buf[2:2+cursorSecondaryLen], padded)
	}
	binary.BigEndian.PutUint64(buf[2+cursorSecondaryLen:], uint64(c.ID))
	return base64.RawURLEncoding.EncodeToString(buf)
}

// DecodeCursor is the inverse of EncodeCursor. An empty input decodes to
// the zero Cursor to signal "first page".
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrBadCursor, err)
	}
	if len(raw) != cursorWireLen {
		return Cursor{}, ErrBadCursor
	}
	if raw[0] != cursorVersion {
		return Cursor{}, ErrBadCursor
	}
	c := Cursor{Version: raw[0]}
	if raw[1] == 1 {
		// Trim trailing zero bytes to recover the original secondary string.
		end := 2 + cursorSecondaryLen
		for end > 2 && raw[end-1] == 0 {
			end--
		}
		c.Secondary = string(raw[2:end])
	}
	c.ID = int64(binary.BigEndian.Uint64(raw[2+cursorSecondaryLen:]))
	return c, nil
}
