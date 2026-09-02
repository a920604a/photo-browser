package catalog

import "database/sql"

type Fingerprint struct {
	Size, MTimeNS int64
}

type Category struct {
	ID             int64
	Name           string
	RelativePath   string
	LastSeenScanID int64
}

type Album struct {
	ID             int64
	CategoryID     int64
	Name           string
	RelativePath   string
	LastSeenScanID int64
}

type Photo struct {
	ID             int64
	AlbumID        int64
	Filename       string
	RelativePath   string
	MIMEType       string
	FileSize       int64
	FileMTimeNS    int64
	Width          int
	Height         int
	TakenAt        sql.NullString
	ThumbnailKey   sql.NullString
	LastSeenScanID int64
}

type ScanCounts struct {
	Seen, New, Changed, Unchanged, Removed, Warnings, Errors int64
}
