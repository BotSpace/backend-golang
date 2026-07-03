package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/botspace/backend/internal/shared/config"
	"github.com/golang-jwt/jwt/v5"
)

// LoadKeys reads the RS256 key pair from the configured paths. In non-production
// environments a missing pair is generated on the fly (dev convenience) and
// written to disk. In production missing keys are a fatal error — supply your
// own keys (generated once per environment with `make gen-keys` and reused
// across deploys; never reuse the dev keys).
func LoadKeys(cfg config.JWTConfig, env string) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	if env != "production" {
		if _, err := os.Stat(cfg.PrivateKeyPath); errors.Is(err, os.ErrNotExist) {
			if err := generateAndWrite(cfg.PrivateKeyPath, cfg.PublicKeyPath); err != nil {
				return nil, nil, fmt.Errorf("generate dev keys: %w", err)
			}
		}
	}

	privPEM, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read private key (%s): %w", cfg.PrivateKeyPath, err)
	}
	priv, err := jwt.ParseRSAPrivateKeyFromPEM(privPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse private key: %w", err)
	}

	pubPEM, err := os.ReadFile(cfg.PublicKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read public key (%s): %w", cfg.PublicKeyPath, err)
	}
	pub, err := jwt.ParseRSAPublicKeyFromPEM(pubPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("parse public key: %w", err)
	}
	return priv, pub, nil
}

// generateAndWrite creates a 2048-bit RSA key pair and writes PKCS#8 private
// and PKIX public PEM files (0600 private, 0644 public).
func generateAndWrite(privPath, pubPath string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(privPath), 0o750); err != nil {
		return err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	return os.WriteFile(pubPath, pubPEM, 0o600)
}
