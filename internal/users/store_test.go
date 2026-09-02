package users_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"photo-browser/internal/database"
	"photo-browser/internal/users"
)

func openStore(t *testing.T) *users.Store {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "photo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return users.NewStore(db)
}

func sqlStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  Foo@Example.COM ": "foo@example.com",
		"bar@example.com":    "bar@example.com",
		"":                   "",
		"   ":                "",
	}
	for in, want := range cases {
		if got := users.NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAddAndLookup(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	id, err := s.Add(ctx, users.User{
		FirebaseUID:     sqlStr("uid-a"),
		Email:           sqlStr("Alice@Example.com"),
		NormalizedEmail: sqlStr(users.NormalizeEmail("Alice@Example.com")),
		Role:            "admin",
		Enabled:         true,
	}, now)
	if err != nil || id == 0 {
		t.Fatalf("Add: id=%d err=%v", id, err)
	}

	got, ok, err := s.LookupByUID(ctx, "uid-a")
	if err != nil || !ok || got.Role != "admin" {
		t.Fatalf("LookupByUID: ok=%v err=%v got=%+v", ok, err, got)
	}
	if !got.Enabled {
		t.Fatal("expected enabled=true")
	}

	got, ok, err = s.LookupByEmail(ctx, "ALICE@example.com")
	if err != nil || !ok || got.ID != id {
		t.Fatalf("LookupByEmail: ok=%v err=%v got=%+v", ok, err, got)
	}
}

func TestAddRejectsEmptyIdentity(t *testing.T) {
	s := openStore(t)
	_, err := s.Add(context.Background(), users.User{Role: "member", Enabled: true}, time.Now())
	if err == nil {
		t.Fatal("expected error for empty uid+email")
	}
}

func TestAddRejectsInvalidRole(t *testing.T) {
	s := openStore(t)
	_, err := s.Add(context.Background(), users.User{
		FirebaseUID: sqlStr("uid"), Role: "root", Enabled: true,
	}, time.Now())
	if err != users.ErrInvalidRole {
		t.Fatalf("err=%v want ErrInvalidRole", err)
	}
}

func TestAddDuplicateUID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	base := users.User{FirebaseUID: sqlStr("dup"), Role: "member", Enabled: true}
	if _, err := s.Add(ctx, base, time.Now()); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := s.Add(ctx, base, time.Now()); err != users.ErrDuplicate {
		t.Fatalf("err=%v want ErrDuplicate", err)
	}
}

func TestAddDuplicateEmail(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	base := users.User{
		Email:           sqlStr("bob@example.com"),
		NormalizedEmail: sqlStr("bob@example.com"),
		Role:            "member",
		Enabled:         true,
	}
	if _, err := s.Add(ctx, base, time.Now()); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := s.Add(ctx, base, time.Now()); err != users.ErrDuplicate {
		t.Fatalf("err=%v want ErrDuplicate", err)
	}
}

func TestSetEnabledDisablesWithoutDeleting(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, _ := s.Add(ctx, users.User{FirebaseUID: sqlStr("u1"), Role: "member", Enabled: true}, time.Now())

	if err := s.SetEnabled(ctx, id, false, time.Now()); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, ok, err := s.LookupByUID(ctx, "u1")
	if err != nil || !ok {
		t.Fatalf("lookup after disable: ok=%v err=%v", ok, err)
	}
	if got.Enabled {
		t.Fatal("expected enabled=false")
	}
	if err := s.SetEnabled(ctx, 999999, true, time.Now()); err != users.ErrNotFound {
		t.Fatalf("SetEnabled unknown id: err=%v", err)
	}
}

func TestUpdateRole(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	id, _ := s.Add(ctx, users.User{FirebaseUID: sqlStr("u2"), Role: "member", Enabled: true}, time.Now())

	if err := s.UpdateRole(ctx, id, "admin", time.Now()); err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	got, _, _ := s.LookupByUID(ctx, "u2")
	if got.Role != "admin" {
		t.Fatalf("role=%q", got.Role)
	}
	if err := s.UpdateRole(ctx, id, "root", time.Now()); err != users.ErrInvalidRole {
		t.Fatalf("bad role err=%v", err)
	}
}

func TestList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_, _ = s.Add(ctx, users.User{FirebaseUID: sqlStr("u1"), Role: "admin", Enabled: true}, time.Now())
	_, _ = s.Add(ctx, users.User{FirebaseUID: sqlStr("u2"), Role: "member", Enabled: true}, time.Now())
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].FirebaseUID.String != "u1" || list[1].FirebaseUID.String != "u2" {
		t.Fatalf("list=%+v", list)
	}
}
