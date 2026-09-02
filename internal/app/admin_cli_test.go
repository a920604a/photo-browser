package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photo-browser/internal/app"
	"photo-browser/internal/database"
	"photo-browser/internal/users"
)

func newAdminEnv(t *testing.T) app.AdminEnv {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return app.AdminEnv{
		Users: users.NewStore(db),
		Now:   func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
}

func TestAdminAddUserByEmailInsertsNormalized(t *testing.T) {
	env := newAdminEnv(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"add-user", "--email=Alice@Example.COM", "--role=admin"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	got, ok, err := env.Users.LookupByEmail(context.Background(), "alice@example.com")
	if err != nil || !ok {
		t.Fatalf("lookup ok=%v err=%v", ok, err)
	}
	if got.Role != "admin" {
		t.Fatalf("role=%q", got.Role)
	}
	if !strings.Contains(stdout.String(), "added user id=") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestAdminAddUserRequiresIdentity(t *testing.T) {
	env := newAdminEnv(t)
	stderr := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"add-user", "--role=member"}, &bytes.Buffer{}, stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%q", code, stderr.String())
	}
}

func TestAdminAddUserRejectsUnknownFlag(t *testing.T) {
	env := newAdminEnv(t)
	stderr := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"add-user", "--uid=x", "--not-a-flag"}, &bytes.Buffer{}, stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
}

func TestAdminAddUserDuplicate(t *testing.T) {
	env := newAdminEnv(t)
	_, err := env.Users.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "u1", Valid: true},
		Role:        "member", Enabled: true,
	}, env.Now())
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"add-user", "--uid=u1"}, &bytes.Buffer{}, stderr)
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestAdminListUsersFormatted(t *testing.T) {
	env := newAdminEnv(t)
	_, _ = env.Users.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "u1", Valid: true},
		Role:        "admin", Enabled: true,
	}, env.Now())
	stdout := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"list-users"}, stdout, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "id\tuid\t") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	if !strings.Contains(lines[1], "u1\t-\tadmin\ttrue") {
		t.Fatalf("row=%q", lines[1])
	}
}

func TestAdminDisableIdempotent(t *testing.T) {
	env := newAdminEnv(t)
	_, _ = env.Users.Add(context.Background(), users.User{
		FirebaseUID: sql.NullString{String: "u1", Valid: true},
		Role:        "member", Enabled: true,
	}, env.Now())
	for i := 0; i < 2; i++ {
		code := app.RunAdmin(context.Background(), env,
			[]string{"disable", "--uid=u1"}, &bytes.Buffer{}, &bytes.Buffer{})
		if code != 0 {
			t.Fatalf("attempt %d exit=%d", i, code)
		}
	}
	got, _, _ := env.Users.LookupByUID(context.Background(), "u1")
	if got.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestAdminSetRoleUnknownUser(t *testing.T) {
	env := newAdminEnv(t)
	stderr := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env,
		[]string{"set-role", "--uid=nobody", "--role=admin"}, &bytes.Buffer{}, stderr)
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
	if !strings.Contains(stderr.String(), "no user") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestAdminNoSubcommandExits2(t *testing.T) {
	env := newAdminEnv(t)
	stderr := &bytes.Buffer{}
	code := app.RunAdmin(context.Background(), env, nil, &bytes.Buffer{}, stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
