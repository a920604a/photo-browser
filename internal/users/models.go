package users

import (
	"database/sql"
	"time"
)

type User struct {
	ID              int64
	FirebaseUID     sql.NullString
	Email           sql.NullString
	NormalizedEmail sql.NullString
	Role            string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
