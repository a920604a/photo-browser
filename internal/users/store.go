package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const timeFormat = "2006-01-02T15:04:05Z07:00"

var (
	ErrDuplicate   = errors.New("users: duplicate identity")
	ErrInvalidRole = errors.New("users: invalid role")
	ErrNotFound    = errors.New("users: not found")
)

type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	conn dbtx
}

func NewStore(db *sql.DB) *Store { return &Store{conn: db} }

func NormalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func validRole(r string) bool { return r == "admin" || r == "member" }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func fmtTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Store) Add(ctx context.Context, u User, now time.Time) (int64, error) {
	if !u.FirebaseUID.Valid && !u.NormalizedEmail.Valid {
		return 0, errors.New("users.Add: firebase_uid or normalized_email required")
	}
	if !validRole(u.Role) {
		return 0, ErrInvalidRole
	}
	nowStr := fmtTime(now)
	var id int64
	err := s.conn.QueryRowContext(ctx, `
		INSERT INTO users(firebase_uid,email,normalized_email,role,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)
		RETURNING id`,
		u.FirebaseUID, u.Email, u.NormalizedEmail, u.Role, boolInt(u.Enabled), nowStr, nowStr,
	).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrDuplicate
		}
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateRole(ctx context.Context, id int64, role string, now time.Time) error {
	if !validRole(role) {
		return ErrInvalidRole
	}
	res, err := s.conn.ExecContext(ctx,
		`UPDATE users SET role=?, updated_at=? WHERE id=?`,
		role, fmtTime(now), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetEnabled(ctx context.Context, id int64, enabled bool, now time.Time) error {
	res, err := s.conn.ExecContext(ctx,
		`UPDATE users SET enabled=?, updated_at=? WHERE id=?`,
		boolInt(enabled), fmtTime(now), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LookupByUID(ctx context.Context, uid string) (User, bool, error) {
	if uid == "" {
		return User{}, false, nil
	}
	return s.lookup(ctx, `firebase_uid=?`, uid)
}

func (s *Store) LookupByEmail(ctx context.Context, email string) (User, bool, error) {
	n := NormalizeEmail(email)
	if n == "" {
		return User{}, false, nil
	}
	return s.lookup(ctx, `normalized_email=?`, n)
}

func (s *Store) GetByID(ctx context.Context, id int64) (User, bool, error) {
	return s.lookup(ctx, `id=?`, id)
}

func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.conn.QueryContext(ctx,
		`SELECT id, firebase_uid, email, normalized_email, role, enabled, created_at, updated_at
		 FROM users ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(r rowScanner) (User, error) {
	var u User
	var enabled int
	var createdAt, updatedAt string
	if err := r.Scan(
		&u.ID, &u.FirebaseUID, &u.Email, &u.NormalizedEmail,
		&u.Role, &enabled, &createdAt, &updatedAt,
	); err != nil {
		return User{}, err
	}
	u.Enabled = enabled != 0
	u.CreatedAt = parseTime(createdAt)
	u.UpdatedAt = parseTime(updatedAt)
	return u, nil
}

func (s *Store) lookup(ctx context.Context, where string, arg any) (User, bool, error) {
	row := s.conn.QueryRowContext(ctx,
		`SELECT id, firebase_uid, email, normalized_email, role, enabled, created_at, updated_at
		 FROM users WHERE `+where,
		arg,
	)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	return u, true, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "constraint failed")
}
