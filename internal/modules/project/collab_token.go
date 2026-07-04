package project

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// defaultCollabTokenTTL matches Django's SIMPLE_JWT ACCESS_TOKEN_LIFETIME
// (config/conf/jwt.py) — collab-token deliberately reuses the exact same
// short lifetime, not this backend's own APP_JWT_ACCESS_TTL, since it's
// meant to be re-minted by the constructor on demand, same as Django's.
const defaultCollabTokenTTL = 15 * time.Minute

// CollabTokenSigner mints short-lived RS256 tokens shaped like Django's
// rest_framework_simplejwt AccessToken plus a project_id claim
// (core/apps/api/views/project.py collab_token), signed with the SAME RSA
// private key Django's SIGNING_KEY uses.
//
// This is deliberately NOT this backend's own auth.TokenIssuer (see
// internal/modules/auth/jwt.go), which signs with a completely different
// keypair (k8s secret backend-golang-jwt, used only to verify this
// backend's own /auth logins). The standalone `websocket` service
// (JscorpTech/websocket, internal/auth/jwt.go there) verifies incoming
// ?token= against the jwt-secret k8s Secret's PUBLIC_KEY — the exact same
// keypair Django's VERIFYING_KEY checks against — and reads the
// `project_id` claim (int or string, either works) to pick the
// "project_<id>" broadcast room. A token signed with the wrong keypair
// would be silently rejected by that service (401 "Token xato"), so
// IssueCollabToken refuses to fall back to any other key: see
// NewCollabTokenSignerFromEnv's nil-on-unset contract.
type CollabTokenSigner struct {
	key *rsa.PrivateKey
	ttl time.Duration
}

// NewCollabTokenSignerFromEnv loads the RSA private key from the PRIVATE_KEY
// env var — PEM content (not a file path), matching Django's SIGNING_KEY
// env contract exactly (config/conf/jwt.py: `env.str("PRIVATE_KEY", ...)`)
// so the same k8s Secret (jwt-secret) can be envFrom'd into this deployment
// unchanged, no re-encoding needed.
//
// Returns (nil, nil) when PRIVATE_KEY is unset — callers MUST treat a nil
// signer as "collab-token not configured" and fail the request loudly
// (IssueCollabToken does exactly this), never silently substitute a
// different key: a token signed with any other key is worthless to the
// websocket service and would fail confusingly far from this code.
func NewCollabTokenSignerFromEnv(ttl time.Duration) (*CollabTokenSigner, error) {
	raw := os.Getenv("PRIVATE_KEY")
	if raw == "" {
		return nil, nil
	}
	key, err := parseRSAPrivateKeyPEM([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("collab token signer: parse PRIVATE_KEY: %w", err)
	}
	if ttl <= 0 {
		ttl = defaultCollabTokenTTL
	}
	return &CollabTokenSigner{key: key, ttl: ttl}, nil
}

func parseRSAPrivateKeyPEM(raw []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("invalid PEM block")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PRIVATE_KEY is not an RSA key")
		}
		return rsaKey, nil
	}
	// Django's private.pem is PKCS8 ("BEGIN PRIVATE KEY"); PKCS1 ("BEGIN RSA
	// PRIVATE KEY") is accepted too in case the mounted secret ever changes
	// encoding.
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// Sign mirrors Django's collab_token view (AccessToken.for_user(user) +
// token["project_id"] = project.id): userID only needs to be PRESENT in the
// claims (the websocket service checks `ok` on claims["user_id"], not its
// type — see its serveWs) — projectRef (an int32 legacy id or a uuid
// string, see IssueCollabToken) is what actually selects the broadcast
// room.
func (s *CollabTokenSigner) Sign(userID uuid.UUID, projectRef any) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"token_type": "access",
		"user_id":    userID.String(),
		"project_id": projectRef,
		"iat":        now.Unix(),
		"exp":        now.Add(s.ttl).Unix(),
		"jti":        uuid.NewString(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign RS256: %w", err)
	}
	return signed, nil
}
