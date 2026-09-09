package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"photo-browser/internal/users"
)

const adminUsage = `usage:
  photo-app admin add-user  (--uid=<uid> | --email=<addr>) [--role=admin|member]
  photo-app admin list-users
  photo-app admin set-role  (--uid=<uid> | --email=<addr>) --role=admin|member
  photo-app admin enable    (--uid=<uid> | --email=<addr>)
  photo-app admin disable   (--uid=<uid> | --email=<addr>)`

// AdminEnv is everything admin_cli needs to talk to the DB. Tests inject
// their own store; Main wires the production store.
type AdminEnv struct {
	Users *users.Store
	Now   func() time.Time
}

// RunAdmin dispatches "admin <sub>" invocations. Exit codes:
//
//	0 success, 1 runtime failure, 2 usage error.
func RunAdmin(ctx context.Context, env AdminEnv, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, adminUsage)
		return 2
	}
	now := env.Now
	if now == nil {
		now = time.Now
	}
	switch args[0] {
	case "add-user":
		return adminAddUser(ctx, env, now, args[1:], stdout, stderr)
	case "list-users":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "list-users takes no arguments\n%s\n", adminUsage)
			return 2
		}
		return adminListUsers(ctx, env, stdout, stderr)
	case "set-role":
		return adminSetRole(ctx, env, now, args[1:], stdout, stderr)
	case "enable":
		return adminSetEnabled(ctx, env, now, args[1:], true, stdout, stderr)
	case "disable":
		return adminSetEnabled(ctx, env, now, args[1:], false, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown admin command: %s\n%s\n", args[0], adminUsage)
		return 2
	}
}

type userSelector struct{ uid, email, role string }

func parseSelector(args []string) (userSelector, error) {
	var sel userSelector
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--uid="):
			sel.uid = strings.TrimPrefix(a, "--uid=")
		case strings.HasPrefix(a, "--email="):
			sel.email = strings.TrimPrefix(a, "--email=")
		case strings.HasPrefix(a, "--role="):
			sel.role = strings.TrimPrefix(a, "--role=")
		default:
			return userSelector{}, fmt.Errorf("unknown flag: %s", a)
		}
	}
	return sel, nil
}

func adminAddUser(ctx context.Context, env AdminEnv, now func() time.Time, args []string, stdout, stderr io.Writer) int {
	sel, err := parseSelector(args)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n%s\n", err, adminUsage)
		return 2
	}
	if sel.uid == "" && sel.email == "" {
		fmt.Fprintf(stderr, "add-user requires --uid or --email\n%s\n", adminUsage)
		return 2
	}
	if sel.role == "" {
		sel.role = "member"
	}
	u := users.User{Role: sel.role, Enabled: true}
	if sel.uid != "" {
		u.FirebaseUID = sql.NullString{String: sel.uid, Valid: true}
	}
	if sel.email != "" {
		u.Email = sql.NullString{String: sel.email, Valid: true}
		u.NormalizedEmail = sql.NullString{String: users.NormalizeEmail(sel.email), Valid: true}
	}
	id, err := env.Users.Add(ctx, u, now())
	switch {
	case errors.Is(err, users.ErrDuplicate):
		fmt.Fprintln(stderr, "user already exists")
		return 1
	case errors.Is(err, users.ErrInvalidRole):
		fmt.Fprintf(stderr, "invalid role: %q\n%s\n", sel.role, adminUsage)
		return 2
	case err != nil:
		fmt.Fprintf(stderr, "add-user: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added user id=%d role=%s\n", id, u.Role)
	return 0
}

func adminListUsers(ctx context.Context, env AdminEnv, stdout, stderr io.Writer) int {
	list, err := env.Users.List(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "list-users: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "id\tuid\temail\trole\tenabled")
	for _, u := range list {
		fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\t%t\n",
			u.ID,
			nullOr(u.FirebaseUID),
			nullOr(u.Email),
			u.Role,
			u.Enabled,
		)
	}
	return 0
}

func adminSetRole(ctx context.Context, env AdminEnv, now func() time.Time, args []string, stdout, stderr io.Writer) int {
	sel, err := parseSelector(args)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n%s\n", err, adminUsage)
		return 2
	}
	if sel.role == "" {
		fmt.Fprintf(stderr, "set-role requires --role\n%s\n", adminUsage)
		return 2
	}
	id, code := resolveUserID(ctx, env, sel, stderr)
	if code != 0 {
		return code
	}
	if err := env.Users.UpdateRole(ctx, id, sel.role, now()); err != nil {
		if errors.Is(err, users.ErrInvalidRole) {
			fmt.Fprintf(stderr, "invalid role: %q\n%s\n", sel.role, adminUsage)
			return 2
		}
		fmt.Fprintf(stderr, "set-role: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "user id=%d role=%s\n", id, sel.role)
	return 0
}

func adminSetEnabled(ctx context.Context, env AdminEnv, now func() time.Time, args []string, enabled bool, stdout, stderr io.Writer) int {
	sel, err := parseSelector(args)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n%s\n", err, adminUsage)
		return 2
	}
	id, code := resolveUserID(ctx, env, sel, stderr)
	if code != 0 {
		return code
	}
	if err := env.Users.SetEnabled(ctx, id, enabled, now()); err != nil {
		fmt.Fprintf(stderr, "set-enabled: %v\n", err)
		return 1
	}
	verb := "enabled"
	if !enabled {
		verb = "disabled"
	}
	fmt.Fprintf(stdout, "user id=%d %s\n", id, verb)
	return 0
}

func resolveUserID(ctx context.Context, env AdminEnv, sel userSelector, stderr io.Writer) (int64, int) {
	if sel.uid == "" && sel.email == "" {
		fmt.Fprintf(stderr, "identify user with --uid or --email\n%s\n", adminUsage)
		return 0, 2
	}
	if sel.uid != "" {
		u, ok, err := env.Users.LookupByUID(ctx, sel.uid)
		if err != nil {
			fmt.Fprintf(stderr, "lookup: %v\n", err)
			return 0, 1
		}
		if !ok {
			fmt.Fprintf(stderr, "no user with uid=%s\n", sel.uid)
			return 0, 1
		}
		return u.ID, 0
	}
	u, ok, err := env.Users.LookupByEmail(ctx, sel.email)
	if err != nil {
		fmt.Fprintf(stderr, "lookup: %v\n", err)
		return 0, 1
	}
	if !ok {
		fmt.Fprintf(stderr, "no user with email=%s\n", sel.email)
		return 0, 1
	}
	return u.ID, 0
}

func nullOr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return "-"
}
