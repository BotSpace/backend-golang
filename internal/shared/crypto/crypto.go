// Package crypto provides authenticated symmetric encryption for secrets at
// rest (credential data, bot tokens) — the Go counterpart to Django's Fernet
// usage. AES-256-GCM with a random nonce per message; output is base64.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNoKey is returned when APP_CRYPTO_KEY is unset and no key was provided.
var ErrNoKey = errors.New("crypto: APP_CRYPTO_KEY not set")

// Cipher encrypts/decrypts short secrets. Construct with New / FromEnv.
type Cipher struct {
	aead cipher.AEAD
}

// New builds a Cipher from any-length key material (hashed to 32 bytes).
func New(key []byte) (*Cipher, error) {
	if len(key) == 0 {
		return nil, ErrNoKey
	}
	sum := sha256.Sum256(key) // normalise to a 32-byte AES-256 key
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// FromEnv builds a Cipher from APP_CRYPTO_KEY.
func FromEnv() (*Cipher, error) {
	return New([]byte(os.Getenv("APP_CRYPTO_KEY")))
}

// Encrypt returns base64(nonce || ciphertext || tag).
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	return plaintext, nil
}
