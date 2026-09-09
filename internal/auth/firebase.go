package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the minimal set of Firebase-issued fields the app cares about.
type Claims struct {
	UID           string
	Email         string
	EmailVerified bool
	IssuedAt      time.Time
	Expires       time.Time
}

// Sentinel errors surface enough context for logging/tests without leaking to callers.
var (
	ErrInvalidToken     = errors.New("auth: invalid token")
	ErrTokenExpired     = errors.New("auth: token expired")
	ErrIssuerMismatch   = errors.New("auth: issuer mismatch")
	ErrAudienceMismatch = errors.New("auth: audience mismatch")
	ErrKeyNotFound      = errors.New("auth: signing key not found")
)

// Verifier validates Firebase ID tokens against a cached JWKS.
type Verifier struct {
	Issuer     string
	Audience   string
	JWKSURL    string
	HTTPClient *http.Client
	Now        func() time.Time
	Refresh    time.Duration // JWKS TTL; default 1h

	mu    sync.Mutex
	cache *keySet
}

type keySet struct {
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) refresh(ctx context.Context) (*keySet, error) {
	client := v.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	var body struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("jwks decode: %w", err)
	}
	ks := &keySet{keys: map[string]*rsa.PublicKey{}, fetched: v.now()}
	for _, k := range body.Keys {
		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() {
			continue
		}
		ks.keys[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
	}
	return ks, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	ttl := v.Refresh
	if ttl <= 0 {
		ttl = time.Hour
	}
	if v.cache == nil || v.now().Sub(v.cache.fetched) > ttl {
		ks, err := v.refresh(ctx)
		if err != nil {
			return nil, err
		}
		v.cache = ks
	}
	if k, ok := v.cache.keys[kid]; ok {
		return k, nil
	}
	// Force refresh once in case of key rotation.
	ks, err := v.refresh(ctx)
	if err != nil {
		return nil, err
	}
	v.cache = ks
	if k, ok := ks.keys[kid]; ok {
		return k, nil
	}
	return nil, ErrKeyNotFound
}

// Verify parses the token, validates iss/aud/exp/nbf, and returns claims.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(v.Issuer),
		jwt.WithAudience(v.Audience),
		jwt.WithLeeway(30*time.Second),
		jwt.WithTimeFunc(v.now),
	)
	tok, err := parser.Parse(raw, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, ErrInvalidToken
		}
		return v.key(ctx, kid)
	})
	if err != nil {
		switch {
		case errors.Is(err, jwt.ErrTokenExpired), errors.Is(err, jwt.ErrTokenNotValidYet):
			return Claims{}, ErrTokenExpired
		case errors.Is(err, jwt.ErrTokenInvalidIssuer):
			return Claims{}, ErrIssuerMismatch
		case errors.Is(err, jwt.ErrTokenInvalidAudience):
			return Claims{}, ErrAudienceMismatch
		case errors.Is(err, ErrKeyNotFound):
			return Claims{}, ErrKeyNotFound
		}
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, ErrInvalidToken
	}
	return Claims{
		UID:           stringOf(mc["sub"]),
		Email:         stringOf(mc["email"]),
		EmailVerified: boolOf(mc["email_verified"]),
		IssuedAt:      timeOf(mc["iat"]),
		Expires:       timeOf(mc["exp"]),
	}, nil
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func timeOf(v any) time.Time {
	switch n := v.(type) {
	case float64:
		return time.Unix(int64(n), 0)
	case json.Number:
		i, _ := n.Int64()
		return time.Unix(i, 0)
	}
	return time.Time{}
}
