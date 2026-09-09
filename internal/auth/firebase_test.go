package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"photo-browser/internal/auth"
)

const (
	testIssuer  = "https://securetoken.google.com/demo-proj"
	testAud     = "demo-proj"
	testKid     = "test-kid-1"
	testFixedTs = int64(1_700_000_000)
)

type fixture struct {
	verifier *auth.Verifier
	signer   *auth.TestSigner
	server   *httptest.Server
	hits     *int32
}

func (f *fixture) close() { f.server.Close() }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	signer := auth.NewTestSigner(testKid)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(signer.JWKS())
	}))
	v := &auth.Verifier{
		Issuer:   testIssuer,
		Audience: testAud,
		JWKSURL:  srv.URL,
		Now:      func() time.Time { return time.Unix(testFixedTs, 0) },
		Refresh:  time.Hour,
	}
	return &fixture{verifier: v, signer: signer, server: srv, hits: &hits}
}

func baseClaims() map[string]any {
	return map[string]any{
		"iss":            testIssuer,
		"aud":            testAud,
		"sub":            "user-1",
		"email":          "alice@example.com",
		"email_verified": true,
		"iat":            testFixedTs - 10,
		"exp":            testFixedTs + 3600,
	}
}

func TestVerifyValidToken(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	tok := f.signer.Sign(baseClaims())
	c, err := f.verifier.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.UID != "user-1" || c.Email != "alice@example.com" || !c.EmailVerified {
		t.Fatalf("claims=%+v", c)
	}
	if c.Expires.Unix() != testFixedTs+3600 {
		t.Fatalf("exp=%d", c.Expires.Unix())
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	claims := baseClaims()
	claims["exp"] = testFixedTs - 60
	claims["iat"] = testFixedTs - 3600
	tok := f.signer.Sign(claims)
	_, err := f.verifier.Verify(context.Background(), tok)
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Fatalf("err=%v want ErrTokenExpired", err)
	}
}

func TestVerifyRejectsIssuerMismatch(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	claims := baseClaims()
	claims["iss"] = "https://securetoken.google.com/other-proj"
	_, err := f.verifier.Verify(context.Background(), f.signer.Sign(claims))
	if !errors.Is(err, auth.ErrIssuerMismatch) {
		t.Fatalf("err=%v want ErrIssuerMismatch", err)
	}
}

func TestVerifyRejectsAudienceMismatch(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	claims := baseClaims()
	claims["aud"] = "not-us"
	_, err := f.verifier.Verify(context.Background(), f.signer.Sign(claims))
	if !errors.Is(err, auth.ErrAudienceMismatch) {
		t.Fatalf("err=%v want ErrAudienceMismatch", err)
	}
}

func TestVerifyRejectsUnknownKID(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	other := auth.NewTestSigner("other-kid")
	tok := other.Sign(baseClaims())
	_, err := f.verifier.Verify(context.Background(), tok)
	if !errors.Is(err, auth.ErrKeyNotFound) {
		t.Fatalf("err=%v want ErrKeyNotFound", err)
	}
}

func TestVerifyRejectsHS256(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	// Manually craft an HS256 header + signature with a bogus secret.
	// Easier: use jwt library from another test path — inline base64 header.
	tok := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6InRlc3Qta2lkLTEifQ.eyJzdWIiOiJ4In0.sig"
	_, err := f.verifier.Verify(context.Background(), tok)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err=%v want ErrInvalidToken", err)
	}
}

func TestVerifyCachesJWKS(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	tok := f.signer.Sign(baseClaims())
	for i := 0; i < 5; i++ {
		if _, err := f.verifier.Verify(context.Background(), tok); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(f.hits); got != 1 {
		t.Fatalf("jwks hits=%d want 1", got)
	}
}

func TestVerifyMissingKIDHeader(t *testing.T) {
	f := newFixture(t)
	defer f.close()
	// signer.Sign always sets kid; craft a token without it via jwt library indirectly by
	// stripping the header. Simpler: reuse malformed HS256 test path — the parser rejects
	// missing kid via keyfunc path. Use empty signer manually.
	other := &auth.TestSigner{KID: "", PrivateKey: f.signer.PrivateKey}
	tok := other.Sign(baseClaims())
	_, err := f.verifier.Verify(context.Background(), tok)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err=%v want ErrInvalidToken", err)
	}
}
