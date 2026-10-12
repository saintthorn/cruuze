// Package auth holds credential primitives: opaque tokens and identity providers.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// NewToken returns a random URL-safe token with 256 bits of entropy.
func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:errcheck // crypto/rand.Read never fails
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the digest stored in place of a token, so a database
// leak does not expose usable credentials.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
