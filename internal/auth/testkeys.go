package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// TestSigner exposes an RSA keypair plus JWKS serializer suitable for tests
// and the acceptance-only `testauth` binary. Not intended for production use.
type TestSigner struct {
	KID        string
	PrivateKey *rsa.PrivateKey
}

// NewTestSigner generates a fresh 2048-bit RSA key. Callers can seed determinism
// by wrapping rand.Reader externally; the plan does not require reproducibility.
func NewTestSigner(kid string) *TestSigner {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("auth.NewTestSigner: %v", err))
	}
	return &TestSigner{KID: kid, PrivateKey: key}
}

// JWKS returns the signer's public key as a Firebase-shaped JWKS document.
func (s *TestSigner) JWKS() []byte {
	pub := s.PrivateKey.Public().(*rsa.PublicKey)
	nBytes := pub.N.Bytes()
	eBytes := bigEndianEncode(pub.E)
	doc := map[string]any{
		"keys": []map[string]string{{
			"kid": s.KID,
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(nBytes),
			"e":   base64.RawURLEncoding.EncodeToString(eBytes),
		}},
	}
	out, _ := json.Marshal(doc)
	return out
}

// Sign returns a signed RS256 JWT with the provided claims and the signer's KID.
func (s *TestSigner) Sign(claims map[string]any) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	tok.Header["kid"] = s.KID
	signed, err := tok.SignedString(s.PrivateKey)
	if err != nil {
		panic(fmt.Sprintf("auth.TestSigner.Sign: %v", err))
	}
	return signed
}

func bigEndianEncode(e int) []byte {
	// RSA public exponent typically 65537 (0x010001). Produce minimal big-endian bytes.
	if e == 0 {
		return []byte{0}
	}
	var out []byte
	for e > 0 {
		out = append([]byte{byte(e & 0xff)}, out...)
		e >>= 8
	}
	return out
}
