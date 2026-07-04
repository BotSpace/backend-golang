package project

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// generateTestRSAKeyPEM returns a fresh, throwaway 2048-bit RSA keypair —
// PKCS8 private key PEM (the exact encoding Django's private.pem/the
// jwt-secret k8s Secret's PRIVATE_KEY use, "BEGIN PRIVATE KEY") plus the
// matching *rsa.PublicKey for verification. Never use real secret material
// in a test; this generates its own per-run key.
func generateTestRSAKeyPEM(t *testing.T) (privPEM []byte, pub *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), &key.PublicKey
}

// verifyLikeWebsocketService mirrors, byte-for-byte, the standalone
// websocket service's token verification (JscorpTech/websocket a.k.a.
// archive/websocket in this monorepo: internal/auth/jwt.go's
// VerifyAndParseJWT + cmd/main.go's serveWs) — RS256 only, then reads
// user_id/project_id straight out of jwt.MapClaims with no further schema.
// Used so these tests assert CollabTokenSigner produces a token that
// service actually accepts, not just "some validly-signed JWT".
func verifyLikeWebsocketService(t *testing.T, tokenString string, pub *rsa.PublicKey) jwt.MapClaims {
	t.Helper()
	token, err := jwt.Parse(tokenString, func(tok *jwt.Token) (interface{}, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodRSA); !ok {
			t.Fatalf("unexpected signing method: %v", tok.Header["alg"])
		}
		return pub, nil
	})
	if err != nil {
		t.Fatalf("websocket-style verify failed: %v", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		t.Fatalf("invalid token/claims")
	}
	return claims
}

func TestCollabTokenSigner_SignsTokenTheWebsocketServiceAccepts(t *testing.T) {
	privPEM, pub := generateTestRSAKeyPEM(t)
	t.Setenv("PRIVATE_KEY", string(privPEM))

	signer, err := NewCollabTokenSignerFromEnv(0)
	if err != nil {
		t.Fatalf("build signer: %v", err)
	}
	if signer == nil {
		t.Fatal("expected a signer when PRIVATE_KEY is set")
	}

	tokenString, err := signer.Sign(uuid.New(), int32(1408))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims := verifyLikeWebsocketService(t, tokenString, pub)

	// serveWs requires claims["user_id"] to be PRESENT (any type) — see
	// cmd/main.go's `userIDRaw, ok := claimData["user_id"]` (500s without one).
	if _, ok := claims["user_id"]; !ok {
		t.Fatal("expected a user_id claim")
	}
	// serveWs picks room "project_<id>" whenever project_id decodes as a
	// float64 — our int32 project ref round-trips through JSON as float64,
	// exactly like Django's int project.id claim does.
	pid, ok := claims["project_id"].(float64)
	if !ok {
		t.Fatalf("expected project_id to decode as float64 (JSON number), got %T: %v", claims["project_id"], claims["project_id"])
	}
	if pid != 1408 {
		t.Fatalf("expected project_id 1408, got %v", pid)
	}
	if claims["token_type"] != "access" {
		t.Fatalf("expected token_type=access (mirrors Django's AccessToken), got %v", claims["token_type"])
	}
}

func TestCollabTokenSigner_UUIDProjectRef(t *testing.T) {
	privPEM, pub := generateTestRSAKeyPEM(t)
	t.Setenv("PRIVATE_KEY", string(privPEM))

	signer, err := NewCollabTokenSignerFromEnv(time.Minute)
	if err != nil || signer == nil {
		t.Fatalf("build signer: %v", err)
	}

	projectUUID := uuid.New()
	tokenString, err := signer.Sign(uuid.New(), projectUUID.String())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	claims := verifyLikeWebsocketService(t, tokenString, pub)
	pid, ok := claims["project_id"].(string)
	if !ok || pid != projectUUID.String() {
		t.Fatalf("expected project_id string %s, got %#v", projectUUID, claims["project_id"])
	}
}

func TestNewCollabTokenSignerFromEnv_UnsetIsNilNotError(t *testing.T) {
	t.Setenv("PRIVATE_KEY", "")
	signer, err := NewCollabTokenSignerFromEnv(0)
	if err != nil {
		t.Fatalf("expected no error when PRIVATE_KEY is unset, got %v", err)
	}
	if signer != nil {
		t.Fatal("expected a nil signer when PRIVATE_KEY is unset")
	}
}

func TestNewCollabTokenSignerFromEnv_UnparseableKeyErrors(t *testing.T) {
	t.Setenv("PRIVATE_KEY", "not a pem")
	if _, err := NewCollabTokenSignerFromEnv(0); err == nil {
		t.Fatal("expected an error for unparseable PRIVATE_KEY")
	}
}
