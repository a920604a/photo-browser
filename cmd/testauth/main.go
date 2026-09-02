// Command testauth is an acceptance-only Firebase substitute. It hosts a JWKS
// document and mints RS256 tokens signed by the same key so integration tests
// can exercise the real auth middleware without contacting Google.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"photo-browser/internal/auth"
)

func main() {
	kid := envOr("MINT_KID", "test-kid-1")
	signer := auth.NewTestSigner(kid)
	issuer := os.Getenv("MINT_ISSUER")
	audience := os.Getenv("MINT_AUDIENCE")
	if issuer == "" || audience == "" {
		log.Fatal("MINT_ISSUER and MINT_AUDIENCE must be set")
	}
	listen := envOr("LISTEN", ":8090")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(signer.JWKS())
	})
	mux.HandleFunc("GET /mint", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("sub") == "" {
			http.Error(w, "sub required", http.StatusBadRequest)
			return
		}
		now := time.Now()
		expOffset := 3600
		if raw := q.Get("exp"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "exp must be int seconds", http.StatusBadRequest)
				return
			}
			expOffset = n
		}
		claims := map[string]any{
			"iss":            issuer,
			"aud":            audience,
			"sub":            q.Get("sub"),
			"email":          q.Get("email"),
			"email_verified": q.Get("verified") == "1",
			"iat":            now.Unix() - 5,
			"exp":            now.Add(time.Duration(expOffset) * time.Second).Unix(),
		}
		fmt.Fprint(w, signer.Sign(claims))
	})

	log.Printf("testauth listening on %s issuer=%s aud=%s", listen, issuer, audience)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
