// Package seal encrypts the few secrets the server keeps for others: the
// tokens of connected trackers, and an import's credentials while it runs.
package seal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

// Key encrypts and decrypts. The zero value is not usable; see ParseKey.
type Key struct{ aead cipher.AEAD }

// ParseKey reads a 32-byte key written in base64, as `openssl rand -base64 32`
// prints one.
func ParseKey(encoded string) (*Key, error) {
	encoded = strings.TrimSpace(encoded)
	var raw []byte
	var err error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err = enc.DecodeString(encoded); err == nil {
			break
		}
	}
	if err != nil || len(raw) != 32 {
		return nil, errors.New("must be 32 bytes in base64 (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Key{aead: aead}, nil
}

// Seal encrypts plain. bound names what the secret belongs to (a row's ID,
// say): it isn't secret, but the result only opens when given the same.
func (k *Key) Seal(plain []byte, bound string) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, plain, []byte(bound)), nil
}

// Open decrypts what Seal made for the same bound.
func (k *Key) Open(sealed []byte, bound string) ([]byte, error) {
	size := k.aead.NonceSize()
	if len(sealed) < size {
		return nil, errors.New("sealed value is too short")
	}
	return k.aead.Open(nil, sealed[:size], sealed[size:], []byte(bound))
}
