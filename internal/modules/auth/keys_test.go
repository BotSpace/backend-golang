package auth_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/botspace/backend/internal/modules/auth"
	"github.com/botspace/backend/internal/shared/config"
	"github.com/google/uuid"
)

func TestLoadKeys_DevGeneratesAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	cfg := config.JWTConfig{
		PrivateKeyPath: filepath.Join(dir, "priv.pem"),
		PublicKeyPath:  filepath.Join(dir, "pub.pem"),
		AccessTTL:      time.Minute,
		Issuer:         "keys-test",
	}

	// dev: missing keys are generated and written to disk
	priv, pub, err := auth.LoadKeys(cfg, "development")
	if err != nil {
		t.Fatalf("LoadKeys dev: %v", err)
	}
	if _, err := os.Stat(cfg.PrivateKeyPath); err != nil {
		t.Errorf("private key not written: %v", err)
	}

	// sign + verify round-trip with the generated pair
	iss := auth.NewTokenIssuer(priv, pub, cfg)
	id := uuid.New()
	tok, _, err := iss.NewAccessToken(id, "admin")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	gotID, role, err := iss.VerifyAccessToken(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if gotID != id.String() || role != "admin" {
		t.Errorf("got (%s,%s), want (%s,admin)", gotID, role, id)
	}

	// production: missing keys must fail (no silent auto-generation)
	missing := config.JWTConfig{
		PrivateKeyPath: filepath.Join(dir, "nope.pem"),
		PublicKeyPath:  filepath.Join(dir, "nope2.pem"),
	}
	if _, _, err := auth.LoadKeys(missing, "production"); err == nil {
		t.Error("expected error in production with missing keys")
	}
}
